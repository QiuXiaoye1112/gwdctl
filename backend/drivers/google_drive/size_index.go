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
	sizeMetaFields         = "id,parents,mimeType,size,trashed"
	sizeIndexFields        = "files(" + sizeMetaFields + "),nextPageToken,incompleteSearch"
	driveFolderMime        = "application/vnd.google-apps.folder"
	sizeIndexMaxPages      = 200
	sizeIndexRefreshMinAge = 30 * time.Second
	sizeIndexWriteDebounce = 5 * time.Second
	sizeIndexRetryDelay    = 10 * time.Second
	sizeIndexMaxRetries    = 2
)

type sizeMetaFile struct {
	Id       string   `json:"id"`
	Parents  []string `json:"parents"`
	MimeType string   `json:"mimeType"`
	Size     string   `json:"size"`
	Trashed  bool     `json:"trashed"`
}

type sizeMetaResp struct {
	NextPageToken    string         `json:"nextPageToken"`
	Files            []sizeMetaFile `json:"files"`
	IncompleteSearch bool           `json:"incompleteSearch"`
}

type folderSizeIndex struct {
	mu            sync.RWMutex
	sizes         map[string]int64
	files         map[string]sizeMetaFile
	children      sizeChildIndex
	changeToken   string
	ready         bool
	refreshing    bool
	dirty         bool
	retries       int
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

// refreshFolderSizes is used at mount startup and for explicit manual refresh.
func (d *GoogleDrive) refreshFolderSizes(manual bool) {
	idx := d.getSizeIndex()
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.closed {
		return
	}
	if manual && idx.ready && !idx.dirty && time.Since(idx.lastUpdated) < sizeIndexRefreshMinAge {
		return
	}
	if idx.refreshing {
		if manual {
			idx.dirty = true
		}
		return
	}
	idx.retries = 0
	d.startFolderSizeRefreshLocked(idx)
}

func (d *GoogleDrive) startFolderSizeRefreshLocked(idx *folderSizeIndex) {
	idx.refreshing = true
	idx.dirty = false
	go d.runFolderSizeRefresh(idx.bgCtx, idx)
}

// Multiple successful writes during one batch share a scan. A write that
// lands during a scan stays dirty so another scan follows.
func (d *GoogleDrive) scheduleFolderSizeRefresh(delay time.Duration) {
	idx := d.getSizeIndex()
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.closed {
		return
	}
	idx.dirty = true
	idx.retries = 0
	d.armFolderSizeRefreshLocked(idx, delay)
}

func (d *GoogleDrive) armFolderSizeRefreshLocked(idx *folderSizeIndex, delay time.Duration) {
	if idx.refreshing || idx.debounceTimer != nil {
		return
	}
	idx.debounceTimer = time.AfterFunc(delay, func() {
		idx.mu.Lock()
		defer idx.mu.Unlock()
		idx.debounceTimer = nil
		if !idx.closed && idx.dirty && !idx.refreshing {
			d.startFolderSizeRefreshLocked(idx)
		}
	})
}

func (d *GoogleDrive) runFolderSizeRefresh(ctx context.Context, idx *folderSizeIndex) {
	start := time.Now()
	// The scheduler allows one writer. Reads only touch the published sizes
	// under the lock; querying Drive and preparing any rebuild never blocks them.
	update, err := d.prepareSizeIndexUpdate(ctx, idx)
	idx.mu.Lock()
	idx.refreshing = false
	completed := err == nil && !idx.closed
	if completed {
		update.publish(idx)
		idx.ready = true
		idx.lastUpdated = time.Now()
		idx.retries = 0
	}
	if !idx.closed {
		if idx.dirty {
			// New confirmed changes must get another pass, even after a failure.
			idx.retries = 0
			d.armFolderSizeRefreshLocked(idx, sizeIndexWriteDebounce)
		} else if err != nil && ctx.Err() == nil && idx.retries < sizeIndexMaxRetries {
			idx.retries++
			idx.dirty = true
			d.armFolderSizeRefreshLocked(idx, sizeIndexRetryDelay*time.Duration(idx.retries))
		}
	}
	idx.mu.Unlock()
	elapsed := time.Since(start).Round(time.Millisecond)
	if completed {
		log.Infof("google_drive: folder size index %s (%d changes) in %v", update.mode, len(update.changes), elapsed)
	} else if err != nil && ctx.Err() == nil {
		log.Warnf("google_drive: folder size index refresh failed; keeping previous results: %v", err)
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
		if resp.IncompleteSearch {
			return nil, true, nil
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
	return aggregateFolderSizes(sizeMetadataByID(files))
}

func aggregateFolderSizes(files map[string]sizeMetaFile) map[string]int64 {
	sizes := make(map[string]int64)
	for _, file := range files {
		addFolderSize(sizes, func(id string) sizeMetaFile { return files[id] }, file, 1)
	}
	return sizes
}

func addFolderSize(sizes map[string]int64, lookup func(string) sizeMetaFile, file sizeMetaFile, sign int64) {
	if file.MimeType == driveFolderMime || file.Trashed || file.Size == "" {
		return
	}
	sz, err := strconv.ParseInt(file.Size, 10, 64)
	if err != nil || sz <= 0 || len(file.Parents) == 0 {
		return
	}
	queue := append([]string(nil), file.Parents...)
	visited := make(map[string]struct{}, len(queue))
	for len(queue) > 0 {
		pid := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if pid == "" {
			continue
		}
		if _, seen := visited[pid]; seen {
			continue
		}
		visited[pid] = struct{}{}
		sizes[pid] += sign * sz
		if parent := lookup(pid); parent.MimeType == driveFolderMime {
			queue = append(queue, parent.Parents...)
		}
	}
}
