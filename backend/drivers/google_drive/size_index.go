package google_drive

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	log "github.com/sirupsen/logrus"
)

const (
	sizeIndexFields        = "files(id,parents,mimeType,size),nextPageToken"
	sizeIndexMaxPages      = 200
	sizeIndexTTL           = 5 * time.Minute
	sizeIndexRefreshMinAge = 30 * time.Second
	sizeIndexWriteDebounce = 5 * time.Second
)

type sizeMetaFile struct {
	Id       string   `json:"id"`
	Parents  []string `json:"parents"`
	MimeType string   `json:"mimeType"`
	Size     string   `json:"size"`
}

type sizeMetaResp struct {
	NextPageToken string         `json:"nextPageToken"`
	Files         []sizeMetaFile `json:"files"`
}

type folderSizeIndex struct {
	mu            sync.RWMutex
	sizes         map[string]int64
	ready         bool
	refreshing    bool
	dirty         bool
	closed        bool
	lastUpdated   time.Time
	debounceTimer *time.Timer
	bgCtx         context.Context
	bgCancel      context.CancelFunc
}

func (d *GoogleDrive) getSizeIndex() *folderSizeIndex {
	d.sizeIdxOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		d.sizeIdx = &folderSizeIndex{
			sizes:    make(map[string]int64),
			bgCtx:    ctx,
			bgCancel: cancel,
		}
	})
	return d.sizeIdx
}

func (d *GoogleDrive) stopSizeIndex() {
	idx := d.getSizeIndex()
	idx.mu.Lock()
	idx.closed = true
	if idx.debounceTimer != nil {
		idx.debounceTimer.Stop()
		idx.debounceTimer = nil
	}
	cancel := idx.bgCancel
	idx.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (idx *folderSizeIndex) lookup(folderID string) (int64, bool) {
	if idx == nil || folderID == "" {
		return 0, false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if !idx.ready {
		return 0, false
	}
	return idx.sizes[folderID], true
}

func (d *GoogleDrive) lookupFolderSize(folderID string) (int64, bool) {
	if folderID == "" {
		return 0, false
	}
	return d.getSizeIndex().lookup(folderID)
}

func (d *GoogleDrive) maybeRefreshFolderSizes(forceHint bool) {
	idx := d.getSizeIndex()
	idx.mu.Lock()
	if idx.closed || idx.refreshing {
		idx.mu.Unlock()
		return
	}
	now := time.Now()
	need := !idx.ready ||
		idx.dirty ||
		now.Sub(idx.lastUpdated) >= sizeIndexTTL ||
		(forceHint && now.Sub(idx.lastUpdated) >= sizeIndexRefreshMinAge)
	if !need {
		idx.mu.Unlock()
		return
	}
	idx.refreshing = true
	idx.dirty = false
	bgCtx := idx.bgCtx
	idx.mu.Unlock()

	go d.runFolderSizeRefresh(bgCtx, idx)
}

func (d *GoogleDrive) scheduleFolderSizeRefresh(delay time.Duration) {
	idx := d.getSizeIndex()
	idx.mu.Lock()
	if idx.closed {
		idx.mu.Unlock()
		return
	}
	idx.dirty = true
	if idx.debounceTimer != nil {
		idx.debounceTimer.Stop()
	}
	idx.debounceTimer = time.AfterFunc(delay, func() {
		d.maybeRefreshFolderSizes(true)
	})
	idx.mu.Unlock()
}

func (d *GoogleDrive) runFolderSizeRefresh(ctx context.Context, idx *folderSizeIndex) {
	start := time.Now()
	files, truncated, err := d.fetchAllSizeMetadata(ctx)

	idx.mu.Lock()
	idx.refreshing = false
	shouldReschedule := idx.dirty && !idx.closed
	if err == nil && !idx.closed {
		idx.sizes = computeFolderSizes(files)
		idx.ready = true
		idx.lastUpdated = time.Now()
		folderCount := len(idx.sizes)
		idx.mu.Unlock()
		if truncated {
			log.Warnf("google_drive: folder size index truncated at %d pages (%d items, %d non-empty folders) in %v",
				sizeIndexMaxPages, len(files), folderCount, time.Since(start).Round(time.Millisecond))
		} else {
			log.Infof("google_drive: indexed %d items across %d non-empty folders in %v",
				len(files), folderCount, time.Since(start).Round(time.Millisecond))
		}
	} else {
		idx.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			log.Warnf("google_drive: folder size index refresh failed: %v", err)
		}
	}

	if shouldReschedule {
		d.scheduleFolderSizeRefresh(sizeIndexWriteDebounce)
	}
}

func (d *GoogleDrive) fetchAllSizeMetadata(ctx context.Context) ([]sizeMetaFile, bool, error) {
	var all []sizeMetaFile
	pageToken := ""
	for page := 0; page < sizeIndexMaxPages; page++ {
		if ctx != nil && ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		var resp sizeMetaResp
		query := map[string]string{
			"fields":   sizeIndexFields,
			"pageSize": "1000",
			"q":        "trashed = false",
		}
		if pageToken != "" {
			query["pageToken"] = pageToken
		}
		_, err := d.requestWithContext(ctx, "https://www.googleapis.com/drive/v3/files", http.MethodGet, func(req *resty.Request) {
			req.SetQueryParams(query)
		}, &resp)
		if err != nil {
			return nil, false, err
		}
		all = append(all, resp.Files...)
		if resp.NextPageToken == "" {
			return all, false, nil
		}
		pageToken = resp.NextPageToken
	}
	return all, true, nil
}

// computeFolderSizes aggregates recursive byte sizes for every ancestor folder
// using bottom-up parent traversal with per-file visited cycle/diamond protection.
func computeFolderSizes(files []sizeMetaFile) map[string]int64 {
	parentMap := make(map[string][]string)
	for _, f := range files {
		if f.MimeType == "application/vnd.google-apps.folder" && len(f.Parents) > 0 {
			parentMap[f.Id] = f.Parents
		}
	}

	sizes := make(map[string]int64)
	for _, f := range files {
		if f.MimeType == "application/vnd.google-apps.folder" || f.Size == "" {
			continue
		}
		sz, err := strconv.ParseInt(f.Size, 10, 64)
		if err != nil || sz <= 0 {
			continue
		}
		if len(f.Parents) == 0 {
			continue
		}
		queue := append([]string(nil), f.Parents...)
		visited := make(map[string]struct{}, len(queue)*2)
		for len(queue) > 0 {
			pid := queue[0]
			queue = queue[1:]
			if pid == "" {
				continue
			}
			if _, seen := visited[pid]; seen {
				continue
			}
			visited[pid] = struct{}{}
			sizes[pid] += sz
			if grandParents, ok := parentMap[pid]; ok && len(grandParents) > 0 {
				queue = append(queue, grandParents...)
			}
		}
	}
	return sizes
}
