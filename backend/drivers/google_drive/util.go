package google_drive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/stream"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
	log "github.com/sirupsen/logrus"
)

// do others that not defined in Driver interface

// Google Drive API field constants
const (
	// File list query fields
	FilesListFields = "files(id,name,mimeType,size,modifiedTime,createdTime,thumbnailLink,shortcutDetails,md5Checksum,sha1Checksum,sha256Checksum),nextPageToken"
	// Single file query fields
	FileInfoFields = "id,name,mimeType,size,md5Checksum,sha1Checksum,sha256Checksum"
)

func (d *GoogleDrive) getAccessToken() string {
	d.tokenMu.RLock()
	defer d.tokenMu.RUnlock()
	return d.AccessToken
}

func (d *GoogleDrive) setAccessToken(token string) {
	d.tokenMu.Lock()
	d.AccessToken = token
	d.tokenMu.Unlock()
}

func (d *GoogleDrive) refreshToken() error {
	return d.refreshTokenIfNeeded("")
}

func (d *GoogleDrive) refreshTokenIfNeeded(failedToken string) error {
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()

	if failedToken != "" && d.getAccessToken() != failedToken {
		return nil
	}
	if d.ClientID == "" || d.ClientSecret == "" || d.RefreshToken == "" {
		return fmt.Errorf("empty ClientID, ClientSecret or RefreshToken")
	}
	tokenURL := "https://www.googleapis.com/oauth2/v4/token"
	var resp base.TokenResp
	var e TokenError
	res, err := base.RestyClient.R().SetResult(&resp).SetError(&e).
		SetFormData(map[string]string{
			"client_id":     d.ClientID,
			"client_secret": d.ClientSecret,
			"refresh_token": d.RefreshToken,
			"grant_type":    "refresh_token",
		}).Post(tokenURL)
	if err != nil {
		return err
	}
	log.Debug(res.String())
	if e.Error != "" {
		return fmt.Errorf("%s", e.Error)
	}
	if resp.AccessToken == "" {
		return fmt.Errorf("token refresh returned empty access token (HTTP %d)", res.StatusCode())
	}
	d.setAccessToken(resp.AccessToken)
	return nil
}

func (d *GoogleDrive) request(apiURL string, method string, callback base.ReqCallback, resp interface{}) ([]byte, error) {
	return d.requestWithContext(context.Background(), apiURL, method, callback, resp)
}

func (d *GoogleDrive) requestWithContext(ctx context.Context, apiURL string, method string, callback base.ReqCallback, resp interface{}) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		token := d.getAccessToken()
		req := base.RestyClient.R()
		if ctx != nil {
			req.SetContext(ctx)
		}
		req.SetHeader("Authorization", "Bearer "+token)
		req.SetQueryParam("includeItemsFromAllDrives", "true")
		req.SetQueryParam("supportsAllDrives", "true")
		if callback != nil {
			callback(req)
		}
		if resp != nil {
			req.SetResult(resp)
		}
		var e Error
		req.SetError(&e)
		res, err := req.Execute(method, apiURL)
		if err != nil {
			return nil, err
		}
		if (res.StatusCode() == http.StatusUnauthorized || e.Error.Code == 401) && attempt == 0 {
			if err = d.refreshTokenIfNeeded(token); err != nil {
				return nil, err
			}
			continue
		}
		if res.StatusCode() < 200 || res.StatusCode() >= 300 {
			if e.Error.Code != 0 {
				return nil, fmt.Errorf("%s: %v", e.Error.Message, e.Error.Errors)
			}
			return nil, fmt.Errorf("request failed with HTTP %d: %s", res.StatusCode(), res.String())
		}
		return res.Body(), nil
	}
	return nil, fmt.Errorf("request failed after token refresh retry")
}

func (d *GoogleDrive) getFiles(ctx context.Context, id string) ([]File, error) {
	pageToken := "first"
	res := make([]File, 0)
	for pageToken != "" {
		if pageToken == "first" {
			pageToken = ""
		}
		var resp Files
		orderBy := "folder,name,modifiedTime desc"
		if d.OrderBy != "" {
			orderBy = d.OrderBy + " " + d.OrderDirection
		}
		query := map[string]string{
			"orderBy":  orderBy,
			"fields":   FilesListFields,
			"pageSize": "1000",
			"q":        fmt.Sprintf("'%s' in parents and trashed = false", id),
			//"includeItemsFromAllDrives": "true",
			//"supportsAllDrives":         "true",
			"pageToken": pageToken,
		}
		_, err := d.requestWithContext(ctx, "https://www.googleapis.com/drive/v3/files", http.MethodGet, func(req *resty.Request) {
			req.SetQueryParams(query)
		}, &resp)
		if err != nil {
			return nil, err
		}
		pageToken = resp.NextPageToken

		// Batch process shortcuts, API calls only for file shortcuts
		shortcutTargetIds := make([]string, 0)
		shortcutIndices := make([]int, 0)

		// Collect target IDs of all file shortcuts (skip folder shortcuts)
		for i := range resp.Files {
			if resp.Files[i].MimeType == "application/vnd.google-apps.shortcut" &&
				resp.Files[i].ShortcutDetails.TargetId != "" &&
				resp.Files[i].ShortcutDetails.TargetMimeType != "application/vnd.google-apps.folder" {
				shortcutTargetIds = append(shortcutTargetIds, resp.Files[i].ShortcutDetails.TargetId)
				shortcutIndices = append(shortcutIndices, i)
			}
		}

		// Batch get target file info (only for file shortcuts)
		if len(shortcutTargetIds) > 0 {
			targetFiles := d.batchGetTargetFilesInfo(ctx, shortcutTargetIds)
			// Update shortcut file info
			for j, targetId := range shortcutTargetIds {
				if targetFile, exists := targetFiles[targetId]; exists {
					fileIndex := shortcutIndices[j]
					if targetFile.Size != "" {
						resp.Files[fileIndex].Size = targetFile.Size
					}
					if targetFile.MD5Checksum != "" {
						resp.Files[fileIndex].MD5Checksum = targetFile.MD5Checksum
					}
					if targetFile.SHA1Checksum != "" {
						resp.Files[fileIndex].SHA1Checksum = targetFile.SHA1Checksum
					}
					if targetFile.SHA256Checksum != "" {
						resp.Files[fileIndex].SHA256Checksum = targetFile.SHA256Checksum
					}
				}
			}
		}

		res = append(res, resp.Files...)
	}
	return res, nil
}

// getTargetFileInfo gets target file details for shortcuts
func (d *GoogleDrive) getTargetFileInfo(ctx context.Context, targetId string) (File, error) {
	var targetFile File
	apiURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s", url.PathEscape(targetId))
	query := map[string]string{
		"fields": FileInfoFields,
	}
	_, err := d.requestWithContext(ctx, apiURL, http.MethodGet, func(req *resty.Request) {
		req.SetQueryParams(query)
	}, &targetFile)
	if err != nil {
		return File{}, err
	}
	return targetFile, nil
}

// batchGetTargetFilesInfo batch gets target file info, sequential processing to avoid concurrency complexity
func (d *GoogleDrive) batchGetTargetFilesInfo(ctx context.Context, targetIds []string) map[string]File {
	if len(targetIds) == 0 {
		return make(map[string]File)
	}

	result := make(map[string]File)
	// Sequential processing to avoid concurrency complexity
	for _, targetId := range targetIds {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		file, err := d.getTargetFileInfo(ctx, targetId)
		if err == nil {
			result[targetId] = file
		}
	}
	return result
}

func cleanUploadUrl(rawUrl string) string {
	u, err := url.Parse(rawUrl)
	if err != nil {
		return rawUrl
	}
	q := u.Query()
	if q.Get("supportsAllDrives") == "" {
		q.Set("supportsAllDrives", "true")
	}
	if q.Get("includeItemsFromAllDrives") == "" {
		q.Set("includeItemsFromAllDrives", "true")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// waitForCompletion handles the final completion state when all bytes have been transmitted to
// the server but Google Drive returned HTTP 308 instead of 200/201.
// In accordance with Google Drive resumable upload specifications, it only sends empty status queries
// (Content-Range: bytes */totalSize), does not resend data, accumulates query attempts, applies
// progressive backoff, and respects context cancellation.
func (d *GoogleDrive) waitForCompletion(ctx context.Context, uploadUrl string, totalSize int64, up driver.UpdateProgress) error {
	const maxAttempts = 5
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if utils.IsCanceled(ctx) {
			return ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadUrl, http.NoBody)
		if err != nil {
			return err
		}
		token := d.getAccessToken()
		req.ContentLength = 0
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", totalSize))

		res, err := base.HttpClient.Do(req)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
				continue
			}
		}

		status := res.StatusCode
		io.Copy(io.Discard, res.Body)
		res.Body.Close()

		switch status {
		case http.StatusOK, http.StatusCreated:
			if up != nil {
				up(100)
			}
			return nil
		case 308:
			// Server acknowledged bytes but has not finalized the file.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
				continue
			}
		case http.StatusUnauthorized:
			if refErr := d.refreshTokenIfNeeded(token); refErr != nil {
				return refErr
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
				continue
			}
		default:
			return fmt.Errorf("status query failed with HTTP %d", status)
		}
	}
	return fmt.Errorf("upload stalled: all %d bytes sent to server, but final HTTP 200/201 completion not confirmed after %d attempts", totalSize, maxAttempts)
}

type chunkAttemptReader struct {
	mu        *sync.Mutex
	rs        io.ReadSeeker
	activeGen *int
	gen       int
}

func (r *chunkAttemptReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if *r.activeGen != r.gen {
		return 0, io.EOF
	}
	return r.rs.Read(p)
}

func (d *GoogleDrive) chunkUpload(ctx context.Context, file model.FileStreamer, rawUrl string, up driver.UpdateProgress) error {
	defaultChunkSize := int64(d.ChunkSize) * 1024 * 1024
	if defaultChunkSize <= 0 {
		defaultChunkSize = 10 * 1024 * 1024
	}

	uploadUrl := cleanUploadUrl(rawUrl)
	var offset int64 = 0
	totalSize := file.GetSize()
	completed := false

	// Handle empty file upload (0 bytes)
	if totalSize == 0 {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadUrl, http.NoBody)
		if err != nil {
			return err
		}
		req.ContentLength = 0
		req.Header.Set("Authorization", "Bearer "+d.getAccessToken())
		req.Header.Set("Content-Range", "bytes */0")

		res, err := base.HttpClient.Do(req)
		if err != nil {
			return err
		}
		status := res.StatusCode
		io.Copy(io.Discard, res.Body)
		res.Body.Close()

		switch status {
		case http.StatusOK, http.StatusCreated:
			if up != nil {
				up(100)
			}
			return nil
		case 308:
			return d.waitForCompletion(ctx, uploadUrl, 0, up)
		default:
			return fmt.Errorf("empty file upload failed with HTTP %d", status)
		}
	}

	ss, err := stream.NewStreamSectionReader(file, int(defaultChunkSize), &up)
	if err != nil {
		return err
	}

	for offset < totalSize && !completed {
		if utils.IsCanceled(ctx) {
			return ctx.Err()
		}

		chunkSize := min(totalSize-offset, defaultChunkSize)
		chunkStart := offset
		chunkEnd := offset + chunkSize

		reader, err := ss.GetSectionReader(chunkStart, chunkSize)
		if err != nil {
			return err
		}

		curOffset := chunkStart
		noProgressCount := 0
		var readerMu sync.Mutex
		activeGen := 0
		freeReader := func() {
			readerMu.Lock()
			activeGen++
			readerMu.Unlock()
			ss.FreeSectionReader(reader)
		}

		// Upload bytes within this chunk until the entire chunk is confirmed or upload finishes
		for curOffset < chunkEnd && !completed {
			if utils.IsCanceled(ctx) {
				freeReader()
				return ctx.Err()
			}

			// Seek within reader to current unconfirmed offset while invalidating any stale
			// http.Transport writeLoop still draining the previous attempt's body reader.
			relOffset := curOffset - chunkStart
			readerMu.Lock()
			activeGen++
			gen := activeGen
			_, seekErr := reader.Seek(relOffset, io.SeekStart)
			readerMu.Unlock()
			if seekErr != nil {
				freeReader()
				return fmt.Errorf("seek reader failed: %w", seekErr)
			}

			sendLen := chunkEnd - curOffset
			attemptReader := &chunkAttemptReader{
				mu:        &readerMu,
				rs:        reader,
				activeGen: &activeGen,
				gen:       gen,
			}
			limitedReader := driver.NewLimitedUploadStream(ctx, io.LimitReader(attemptReader, sendLen))

			req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadUrl, limitedReader)
			if err != nil {
				freeReader()
				return err
			}
			usedToken := d.getAccessToken()
			req.ContentLength = sendLen
			req.Header.Set("Authorization", "Bearer "+usedToken)
			req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", curOffset, chunkEnd-1, totalSize))

			res, err := base.HttpClient.Do(req)
			if err != nil {
				noProgressCount++
				if noProgressCount >= 5 {
					freeReader()
					return fmt.Errorf("upload chunk network error: %w", err)
				}
				time.Sleep(time.Duration(noProgressCount) * 100 * time.Millisecond)
				continue
			}

			bytes, readErr := io.ReadAll(res.Body)
			res.Body.Close()
			if readErr != nil {
				freeReader()
				return fmt.Errorf("read response body failed: %w", readErr)
			}

			switch res.StatusCode {
			case http.StatusOK, http.StatusCreated:
				// Final completion!
				completed = true
				curOffset = chunkEnd
				offset = totalSize
				if up != nil {
					up(100)
				}

			case 308: // Resume Incomplete
				rangeHeader := res.Header.Get("Range")
				var confirmed int64 = -1
				if rangeHeader != "" {
					var start, end int64
					if n, _ := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); n == 2 {
						confirmed = end + 1
					}
				}

				if confirmed <= curOffset {
					// Server returned 308 without Range, or Range did not advance beyond curOffset.
					// Strictly no progress made. Do NOT guess!
					noProgressCount++
					if noProgressCount >= 5 {
						freeReader()
						return fmt.Errorf("upload stalled: server returned HTTP 308 without progress (Range: %q, current: %d)", rangeHeader, curOffset)
					}
					select {
					case <-ctx.Done():
						freeReader()
						return ctx.Err()
					case <-time.After(time.Duration(noProgressCount) * 100 * time.Millisecond):
					}
				} else {
					// Forward progress made
					noProgressCount = 0
					if confirmed >= totalSize {
						// All bytes uploaded to server, but server responded with 308 instead of 200/201.
						// Transition to dedicated completion query loop without resending data.
						freeReader()
						return d.waitForCompletion(ctx, uploadUrl, totalSize, up)
					} else if confirmed >= chunkEnd {
						// Entire chunk confirmed!
						curOffset = confirmed
						offset = confirmed
						if up != nil {
							up(float64(confirmed) / float64(totalSize) * 100)
						}
					} else {
						// Partial confirmation within chunk
						curOffset = confirmed
						if up != nil {
							up(float64(confirmed) / float64(totalSize) * 100)
						}
					}
				}

			case http.StatusUnauthorized:
				if refErr := d.refreshTokenIfNeeded(usedToken); refErr != nil {
					freeReader()
					return refErr
				}
				noProgressCount++
				if noProgressCount >= 5 {
					freeReader()
					return fmt.Errorf("token refresh retry exceeded")
				}

			default:
				var e Error
				if jsonErr := utils.Json.Unmarshal(bytes, &e); jsonErr == nil && e.Error.Code != 0 {
					if e.Error.Code == 401 {
						if refErr := d.refreshTokenIfNeeded(usedToken); refErr != nil {
							freeReader()
							return refErr
						}
						noProgressCount++
						if noProgressCount < 5 {
							continue
						}
					}
					freeReader()
					return fmt.Errorf("upload chunk failed (HTTP %d): %s (%v)", res.StatusCode, e.Error.Message, e.Error.Errors)
				}
				freeReader()
				return fmt.Errorf("upload chunk failed with HTTP %d: %s", res.StatusCode, string(bytes))
			}
		}

		freeReader()
	}

	if !completed {
		return fmt.Errorf("upload incomplete: final 200/201 response was not received")
	}
	return nil
}

func (d *GoogleDrive) getAbout(ctx context.Context) (*AboutResp, error) {
	query := map[string]string{
		"fields": "storageQuota",
	}
	var resp AboutResp
	_, err := d.requestWithContext(ctx, "https://www.googleapis.com/drive/v3/about", http.MethodGet, func(req *resty.Request) {
		req.SetQueryParams(query)
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
