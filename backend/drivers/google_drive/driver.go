package google_drive

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
)

type GoogleDrive struct {
	model.Storage
	Addition
	AccessToken string
	tokenExpiry time.Time
	tokenMu     sync.RWMutex
	refreshMu   sync.Mutex
	sizeIdxOnce sync.Once
	sizeIdx     *folderSizeIndex
}

func (d *GoogleDrive) Config() driver.Config {
	return config
}

func (d *GoogleDrive) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *GoogleDrive) Init(ctx context.Context) error {
	if d.ChunkSize <= 0 {
		d.ChunkSize = 16
	}
	if err := d.refreshToken(); err != nil {
		return err
	}
	d.maybeRefreshFolderSizes(false)
	return nil
}

func (d *GoogleDrive) Drop(ctx context.Context) error {
	d.stopSizeIndex()
	return nil
}

type ShortcutObj interface {
	GetTargetID() string
}

func getTargetID(obj model.Obj) string {
	for obj != nil {
		if sc, ok := obj.(ShortcutObj); ok && sc.GetTargetID() != "" {
			return sc.GetTargetID()
		}
		if unwrap, ok := obj.(model.ObjUnwrap); ok {
			obj = unwrap.Unwrap()
		} else {
			break
		}
	}
	return ""
}

func getReadID(obj model.Obj) string {
	if obj == nil {
		return ""
	}
	if tid := getTargetID(obj); tid != "" {
		return tid
	}
	return obj.GetID()
}

func (d *GoogleDrive) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	files, err := d.getFiles(ctx, getReadID(dir))
	if err != nil {
		return nil, err
	}
	idx := d.getSizeIndex()
	d.maybeRefreshFolderSizes(args.Refresh)
	return utils.SliceConvert(files, func(src File) (model.Obj, error) {
		obj := fileToObj(src)
		if obj.IsDir() {
			if gdObj, ok := obj.(*GoogleDriveObj); ok {
				gdObj.sizeIndex = idx
			}
		}
		return obj, nil
	})
}

type NativeMimeTypeObj interface {
	GetNativeMimeType() string
}

func getNativeMimeType(obj model.Obj) string {
	for obj != nil {
		if m, ok := obj.(NativeMimeTypeObj); ok && m.GetNativeMimeType() != "" {
			return m.GetNativeMimeType()
		}
		if unwrap, ok := obj.(model.ObjUnwrap); ok {
			obj = unwrap.Unwrap()
		} else {
			break
		}
	}
	return ""
}

func (d *GoogleDrive) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	readID := getReadID(file)
	fileURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?includeItemsFromAllDrives=true&supportsAllDrives=true", readID)
	mimeType := getNativeMimeType(file)
	var token string
	if mimeType == "" {
		var metadata File
		_, err := d.requestWithContext(ctx, fileURL, http.MethodGet, func(req *resty.Request) {
			req.SetQueryParam("fields", "mimeType")
		}, &metadata)
		if err != nil {
			return nil, err
		}
		mimeType = metadata.MimeType
		token = d.getAccessToken()
	} else {
		var err error
		token, err = d.ensureValidToken()
		if err != nil {
			return nil, err
		}
	}
	contentURL := fileURL + "&alt=media&acknowledgeAbuse=true"
	if mimeType == "application/vnd.google-apps.document" {
		contentURL = fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s/export?mimeType=%s", readID, url.QueryEscape("text/markdown"))
	}
	link := model.Link{
		URL: contentURL,
		Header: http.Header{
			"Authorization": []string{"Bearer " + token},
		},
	}
	if ttl := d.tokenRemainingTTL(); ttl > 0 {
		exp := min(ttl, 5*time.Minute)
		link.Expiration = &exp
	}
	return &link, nil
}

func (d *GoogleDrive) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	data := base.Json{
		"name":     dirName,
		"parents":  []string{getReadID(parentDir)},
		"mimeType": "application/vnd.google-apps.folder",
	}
	var created File
	_, err := d.requestWithContext(ctx, "https://www.googleapis.com/drive/v3/files", http.MethodPost, func(req *resty.Request) {
		req.SetQueryParam("fields", "id,name,mimeType,size,modifiedTime,createdTime")
		req.SetBody(data)
	}, &created)
	if err != nil {
		return nil, err
	}
	if created.Id == "" {
		return nil, nil
	}
	obj := fileToObj(created)
	if gdObj, ok := obj.(*GoogleDriveObj); ok {
		gdObj.sizeIndex = d.getSizeIndex()
	}
	return obj, nil
}

func (d *GoogleDrive) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	var file struct {
		Parents []string `json:"parents"`
	}
	apiURL := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(srcObj.GetID())
	_, err := d.requestWithContext(ctx, apiURL, http.MethodGet, func(req *resty.Request) {
		req.SetQueryParam("fields", "parents")
	}, &file)
	if err != nil {
		return err
	}
	query := map[string]string{
		"addParents":    getReadID(dstDir),
		"removeParents": strings.Join(file.Parents, ","),
	}
	_, err = d.requestWithContext(ctx, apiURL, http.MethodPatch, func(req *resty.Request) {
		req.SetQueryParams(query)
	}, nil)
	if err == nil {
		d.scheduleFolderSizeRefresh(sizeIndexWriteDebounce)
	}
	return err
}

func (d *GoogleDrive) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	data := base.Json{
		"name": newName,
	}
	apiURL := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(srcObj.GetID())
	_, err := d.requestWithContext(ctx, apiURL, http.MethodPatch, func(req *resty.Request) {
		req.SetBody(data)
	}, nil)
	return err
}

func (d *GoogleDrive) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	if srcObj.IsDir() {
		return errs.NotSupport
	}
	data := base.Json{
		"name":    srcObj.GetName(),
		"parents": []string{getReadID(dstDir)},
	}
	apiURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s/copy", url.PathEscape(srcObj.GetID()))
	_, err := d.requestWithContext(ctx, apiURL, http.MethodPost, func(req *resty.Request) {
		req.SetBody(data)
	}, nil)
	if err == nil {
		d.scheduleFolderSizeRefresh(sizeIndexWriteDebounce)
	}
	return err
}

func (d *GoogleDrive) Remove(ctx context.Context, obj model.Obj) error {
	if obj == nil || obj.GetID() == "" {
		return errs.ObjectNotFound
	}
	data := base.Json{
		"trashed": true,
	}
	apiURL := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(obj.GetID())
	_, err := d.requestWithContext(ctx, apiURL, http.MethodPatch, func(req *resty.Request) {
		req.SetBody(data)
	}, nil)
	if err == nil {
		d.scheduleFolderSizeRefresh(sizeIndexWriteDebounce)
	}
	return err
}

func (d *GoogleDrive) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	obj := stream.GetExist()
	if stream.GetSize() >= 0 && stream.GetSize() <= smallUploadThreshold {
		return d.uploadSmallFile(ctx, dstDir, stream, obj, up)
	}

	var (
		apiURL string
		data   base.Json
	)
	if obj != nil {
		apiURL = fmt.Sprintf("https://www.googleapis.com/upload/drive/v3/files/%s?uploadType=resumable&supportsAllDrives=true", url.PathEscape(obj.GetID()))
		data = base.Json{}
	} else {
		data = base.Json{
			"name":    stream.GetName(),
			"parents": []string{getReadID(dstDir)},
		}
		apiURL = "https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true"
	}

	var putUrl string
	refreshed := false
	for retryAttempt := 0; ; {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		var e Error
		token := d.getAccessToken()
		req := base.NoRedirectClient.R().
			SetHeaders(map[string]string{
				"Authorization":           "Bearer " + token,
				"X-Upload-Content-Type":   stream.GetMimetype(),
				"X-Upload-Content-Length": strconv.FormatInt(stream.GetSize(), 10),
			}).
			SetError(&e).SetBody(data)
		if ctx != nil {
			req.SetContext(ctx)
		}
		var (
			res *resty.Response
			err error
		)
		if obj != nil {
			res, err = req.Patch(apiURL)
		} else {
			res, err = req.Post(apiURL)
		}
		if err != nil {
			if retryAttempt < maxGoogleRetries {
				if sleepErr := retrySleep(ctx, googleBackoffDelay(retryAttempt, "")); sleepErr != nil {
					return sleepErr
				}
				retryAttempt++
				continue
			}
			return err
		}
		if (res.StatusCode() == http.StatusUnauthorized || e.Error.Code == 401) && !refreshed {
			refreshed = true
			if err = d.refreshTokenIfNeeded(token); err != nil {
				return err
			}
			continue
		}
		if res.IsError() {
			if isRetryableGoogleStatus(res.StatusCode(), res.Body(), &e) && retryAttempt < maxGoogleRetries {
				if sleepErr := retrySleep(ctx, googleBackoffDelay(retryAttempt, res.Header().Get("Retry-After"))); sleepErr != nil {
					return sleepErr
				}
				retryAttempt++
				continue
			}
			if e.Error.Code != 0 {
				return fmt.Errorf("%s: %v", e.Error.Message, e.Error.Errors)
			}
			return fmt.Errorf("init upload session failed with HTTP %d: %s", res.StatusCode(), res.String())
		}
		putUrl = res.Header().Get("location")
		if putUrl == "" {
			return fmt.Errorf("init upload session failed: missing Location header in response (HTTP %d)", res.StatusCode())
		}
		break
	}
	if err := d.chunkUpload(ctx, stream, putUrl, up); err != nil {
		return err
	}
	d.scheduleFolderSizeRefresh(sizeIndexWriteDebounce)
	return nil
}

func (d *GoogleDrive) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	if d.DisableDiskUsage {
		return nil, errs.NotImplement
	}
	about, err := d.getAbout(ctx)
	if err != nil {
		return nil, err
	}
	var total, used int64
	if about.StorageQuota.Limit == nil {
		total = 0
	} else {
		total, err = strconv.ParseInt(*about.StorageQuota.Limit, 10, 64)
		if err != nil {
			return nil, err
		}
	}
	used, err = strconv.ParseInt(about.StorageQuota.Usage, 10, 64)
	if err != nil {
		return nil, err
	}
	return &model.StorageDetails{
		DiskUsage: model.DiskUsage{
			TotalSpace: total,
			UsedSpace:  used,
		},
	}, nil
}

var (
	_ driver.Driver      = (*GoogleDrive)(nil)
	_ driver.MkdirResult = (*GoogleDrive)(nil)
)
