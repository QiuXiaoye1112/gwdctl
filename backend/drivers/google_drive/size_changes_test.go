package google_drive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/go-resty/resty/v2"
)

type sizeChangeTransportFunc func(*http.Request) (*http.Response, error)

func (f sizeChangeTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func sizeChangeResponse(r *http.Request, status int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestSizeChangesOnlyUpdateAffectedTotals(t *testing.T) {
	original := sizeMetadataByID([]sizeMetaFile{
		{Id: "a", MimeType: driveFolderMime, Parents: []string{"root"}},
		{Id: "b", MimeType: driveFolderMime, Parents: []string{"root"}},
		{Id: "other", MimeType: driveFolderMime, Parents: []string{"root"}},
		{Id: "nested", MimeType: driveFolderMime, Parents: []string{"a"}},
		{Id: "overwrite", MimeType: "text/plain", Parents: []string{"a"}, Size: "100"},
		{Id: "move", MimeType: "text/plain", Parents: []string{"a"}, Size: "20"},
		{Id: "delete", MimeType: "text/plain", Parents: []string{"b"}, Size: "5"},
		{Id: "child", MimeType: "text/plain", Parents: []string{"nested"}, Size: "40"},
		{Id: "stable", MimeType: "text/plain", Parents: []string{"other"}, Size: "90"},
	})
	oldClient := base.RestyClient
	defer func() { base.RestyClient = oldClient }()
	used := 0
	base.RestyClient = resty.New().SetTransport(sizeChangeTransportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/drive/v3/changes" {
			return nil, fmt.Errorf("unexpected full listing: %s", r.URL.Path)
		}
		used++
		switch r.URL.Query().Get("pageToken") {
		case "initial":
			return sizeChangeResponse(r, 200, `{"nextPageToken":"second","changes":[{"fileId":"overwrite","file":{"id":"overwrite","mimeType":"text/plain","parents":["a"],"size":"300"}},{"fileId":"upload","file":{"id":"upload","mimeType":"text/plain","parents":["a"],"size":"42"}}]}`)
		case "second":
			return sizeChangeResponse(r, 200, `{"newStartPageToken":"next","changes":[{"fileId":"overwrite","file":{"id":"overwrite","mimeType":"text/plain","parents":["a"],"size":"200"}},{"fileId":"move","file":{"id":"move","mimeType":"text/plain","parents":["b"],"size":"20"}},{"fileId":"delete","removed":true}]}`)
		case "next":
			return sizeChangeResponse(r, 200, `{"newStartPageToken":"latest","changes":[{"fileId":"nested","file":{"id":"nested","mimeType":"application/vnd.google-apps.folder","parents":["b"]}},{"fileId":"folder-upload","file":{"id":"folder-upload","mimeType":"application/vnd.google-apps.folder","parents":["nested"]}},{"fileId":"another-upload","file":{"id":"another-upload","mimeType":"text/plain","parents":["folder-upload"],"size":"7"}}]}`)
		default:
			return nil, fmt.Errorf("unexpected cursor %q", r.URL.Query().Get("pageToken"))
		}
	}))
	d := &GoogleDrive{AccessToken: "test-token"}
	idx := &folderSizeIndex{files: original, children: newSizeChildIndex(original), sizes: aggregateFolderSizes(original), changeToken: "initial", ready: true}
	u, err := d.prepareSizeIndexUpdate(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}
	if u.files != nil || u.delta["other"] != 0 || idx.sizes["a"] != 160 {
		t.Fatalf("did not stage a targeted update: %+v", u)
	}
	u.publish(idx)
	if want := map[string]int64{"a": 282, "b": 20, "nested": 40, "other": 90, "root": 392}; idx.changeToken != "next" || !reflect.DeepEqual(idx.sizes, want) {
		t.Fatalf("incorrect file deltas or cursor: %v %q; want %v", idx.sizes, idx.changeToken, want)
	}
	u, err = d.prepareSizeIndexUpdate(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}
	u.publish(idx)
	if want := map[string]int64{"a": 242, "b": 67, "nested": 47, "folder-upload": 7, "other": 90, "root": 399}; idx.changeToken != "latest" || !reflect.DeepEqual(idx.sizes, want) || used != 3 {
		t.Fatalf("incorrect nested folder update: %v %q requests=%d; want %v", idx.sizes, idx.changeToken, used, want)
	}
}

func TestSizeIndexRecoversInvalidCursorButRejectsPartialSnapshot(t *testing.T) {
	oldClient := base.RestyClient
	defer func() { base.RestyClient = oldClient }()
	paths := []string{}
	d := &GoogleDrive{AccessToken: "test-token"}
	idx := &folderSizeIndex{files: map[string]sizeMetaFile{}, children: make(sizeChildIndex), sizes: map[string]int64{"root": 5}, changeToken: "expired", ready: true}
	incomplete := true
	base.RestyClient = resty.New().SetTransport(sizeChangeTransportFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path+":"+r.URL.Query().Get("pageToken"))
		switch r.URL.Path {
		case "/drive/v3/changes/startPageToken":
			return sizeChangeResponse(r, 200, `{"startPageToken":"fresh"}`)
		case "/drive/v3/files":
			body, _ := json.Marshal(sizeMetaResp{Files: []sizeMetaFile{{Id: "existing", MimeType: "text/plain", Parents: []string{"root"}, Size: "50"}}, IncompleteSearch: incomplete})
			return sizeChangeResponse(r, 200, string(body))
		case "/drive/v3/changes":
			if r.URL.Query().Get("pageToken") == "expired" {
				return sizeChangeResponse(r, 410, `{"error":{"code":410,"message":"Invalid cursor"}}`)
			}
			return sizeChangeResponse(r, 200, `{"newStartPageToken":"current","changes":[{"fileId":"during-scan","file":{"id":"during-scan","mimeType":"text/plain","parents":["root"],"size":"7"}}]}`)
		default:
			return nil, fmt.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	if _, err := d.prepareSizeIndexUpdate(context.Background(), idx); err == nil || idx.sizes["root"] != 5 || idx.changeToken != "expired" {
		t.Fatal("an incomplete rebuild must not clear the last complete snapshot")
	}
	incomplete = false
	paths = nil
	u, err := d.prepareSizeIndexUpdate(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}
	u.publish(idx)
	want := []string{"/drive/v3/changes:expired", "/drive/v3/changes/startPageToken:", "/drive/v3/files:", "/drive/v3/changes:fresh"}
	if !reflect.DeepEqual(paths, want) || idx.sizes["root"] != 57 || idx.changeToken != "current" {
		t.Fatalf("incorrect cursor recovery: %v, total=%d token=%s", paths, idx.sizes["root"], idx.changeToken)
	}
}

func TestFolderSizeIndexEmptyFolderCleanupAndDelete(t *testing.T) {
	before := sizeMetadataByID([]sizeMetaFile{
		{Id: "a", MimeType: driveFolderMime, Parents: []string{"root"}},
		{Id: "empty", MimeType: driveFolderMime, Parents: []string{"a"}},
		{Id: "nested", MimeType: driveFolderMime, Parents: []string{"empty"}},
		{Id: "full", MimeType: driveFolderMime, Parents: []string{"a"}},
		{Id: "zero", MimeType: "text/plain", Parents: []string{"full"}, Size: "0"},
		{Id: "file", MimeType: "text/plain", Parents: []string{"full"}, Size: "70"},
	})
	idx := &folderSizeIndex{files: before, children: newSizeChildIndex(before), sizes: aggregateFolderSizes(before)}
	u, ok := stageSizeChanges(idx, sizeChanges{"empty": nil, "nested": nil}, "cleaned")
	if !ok || len(u.delta) != 0 {
		t.Fatalf("removing confirmed empty directories must only change the graph: %v %v", ok, u.delta)
	}
	u.publish(idx)
	if _, exists := idx.files["empty"]; exists || len(idx.children["empty"]) != 0 || idx.sizes["a"] != 70 {
		t.Fatalf("cleanup corrupted totals or left an edge: %v %v", idx.sizes, idx.children)
	}
	if _, ok := stageSizeChanges(idx, sizeChanges{"full": nil}, "uncertain"); ok {
		t.Fatal("non-empty subtree removal without enough changes must fall back")
	}
	u, ok = stageSizeChanges(idx, sizeChanges{"full": nil, "zero": nil, "file": nil}, "fully-confirmed")
	if !ok || u.delta["a"] != -70 || u.delta["root"] != -70 {
		t.Fatalf("known subtree deletion must subtract exactly once: %v %v", ok, u.delta)
	}
	u.publish(idx)
	if len(idx.sizes) != 0 || len(idx.children["full"]) != 0 {
		t.Fatal("deleted subtree left an aggregate or edge")
	}
}

func TestFolderSizeIndexIncrementalStressAgainstFullAggregation(t *testing.T) {
	const folders, entries, rounds = 180, 5000, 300
	idFor := func(prefix string, n int) string { return prefix + strconv.Itoa(n) }
	files := make(map[string]sizeMetaFile, folders+entries)
	for i := range folders {
		parent := "root"
		if i >= 20 {
			parent = idFor("d", (i-20)/4)
		}
		id := idFor("d", i)
		files[id] = sizeMetaFile{Id: id, MimeType: driveFolderMime, Parents: []string{parent}}
	}
	for i := range entries {
		id := idFor("f", i)
		files[id] = sizeMetaFile{Id: id, MimeType: "text/plain", Parents: []string{idFor("d", i%folders)}, Size: strconv.Itoa(i % 7777)}
	}
	idx := &folderSizeIndex{files: files, children: newSizeChildIndex(files), sizes: aggregateFolderSizes(files), ready: true}
	done := make(chan struct{})
	var readers sync.WaitGroup
	var invalidRead atomic.Bool
	defer func() {
		close(done)
		readers.Wait()
		if invalidRead.Load() {
			t.Error("an index reader returned an invalid size")
		}
	}()
	for i := range 12 {
		readers.Go(func() {
			for j := i; ; j++ {
				select {
				case <-done:
					return
				default:
					n, ok := idx.lookup(idFor("d", j%folders))
					if !ok || n < 0 {
						invalidRead.Store(true)
					}
				}
			}
		})
	}
	rng := rand.New(rand.NewSource(89))
	for round := range rounds {
		changes := make(sizeChanges)
		for range 64 {
			id := idFor("f", rng.Intn(entries+800))
			if rng.Intn(7) == 0 {
				changes[id] = nil
				continue
			}
			parents := []string{idFor("d", rng.Intn(folders))}
			if rng.Intn(15) == 0 {
				parents = append(parents, idFor("d", rng.Intn(folders)))
			}
			changes[id] = &sizeMetaFile{Id: id, MimeType: "text/plain", Parents: parents, Size: strconv.Itoa(rng.Intn(9000000))}
		}
		if round%7 == 0 {
			// Move a folder to another branch, including its unchanged children.
			id := idFor("d", 20+rng.Intn(folders-20))
			changes[id] = &sizeMetaFile{Id: id, MimeType: driveFolderMime, Parents: []string{idFor("d", rng.Intn(20))}}
		}
		u, ok := stageSizeChanges(idx, changes, strconv.Itoa(round))
		if !ok {
			t.Fatalf("valid batch %d requested a rebuild", round)
		}
		idx.mu.Lock()
		u.publish(idx)
		idx.mu.Unlock()
		expected := aggregateFolderSizes(idx.files)
		if !reflect.DeepEqual(idx.sizes, expected) || !reflect.DeepEqual(idx.children, newSizeChildIndex(idx.files)) {
			t.Fatalf("batch %d differs from a full rebuild", round)
		}
	}
}

func TestFolderSizeIndexFailureRetainsSnapshotAndSchedulesBoundedRetry(t *testing.T) {
	oldClient := base.RestyClient
	defer func() { base.RestyClient = oldClient }()
	base.RestyClient = resty.New().SetTransport(sizeChangeTransportFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("synthetic connection failure")
	}))
	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(context.Background())
	idx := d.getSizeIndex()
	idx.sizes["a"] = 17
	idx.files, idx.children, idx.changeToken, idx.ready = make(map[string]sizeMetaFile), make(sizeChildIndex), "known", true
	for attempt := 1; attempt <= sizeIndexMaxRetries+1; attempt++ {
		d.runFolderSizeRefresh(context.Background(), idx)
		idx.mu.Lock()
		if idx.sizes["a"] != 17 || idx.changeToken != "known" {
			idx.mu.Unlock()
			t.Fatal("failed fetch changed a known value")
		}
		timer := idx.debounceTimer
		if timer != nil {
			timer.Stop()
			idx.debounceTimer = nil
		}
		// Simulate the scheduled timer beginning the next attempt.
		idx.dirty = false
		idx.mu.Unlock()
		if (timer != nil) != (attempt <= sizeIndexMaxRetries) {
			t.Fatalf("unexpected retry on attempt %d", attempt)
		}
	}
}

func TestFolderSizeIndexCoalescesConcurrentWritesDuringSlowFeed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), sizeIndexWriteDebounce+4*time.Second)
	defer cancel()
	oldClient := base.RestyClient
	defer func() { base.RestyClient = oldClient }()
	first, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	base.RestyClient = resty.New().SetTransport(sizeChangeTransportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/drive/v3/changes" {
			return nil, fmt.Errorf("unexpected full metadata scan: %s", r.URL.Path)
		}
		switch seq := requests.Add(1); seq {
		case 1:
			close(first)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			return sizeChangeResponse(r, 200, `{"newStartPageToken":"second","changes":[]}`)
		case 2:
			return sizeChangeResponse(r, 200, `{"newStartPageToken":"last","changes":[{"fileId":"uploaded","file":{"id":"uploaded","parents":["root"],"mimeType":"text/plain","size":"42"}}]}`)
		default:
			return nil, fmt.Errorf("unexpected request %d", seq)
		}
	}))
	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(context.Background())
	idx := d.getSizeIndex()
	idx.files, idx.children, idx.changeToken, idx.ready = make(map[string]sizeMetaFile), make(sizeChildIndex), "first", true
	d.scheduleFolderSizeRefresh(0)
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal("first pass never started")
	}
	var writers sync.WaitGroup
	for range 96 {
		writers.Go(func() { d.scheduleFolderSizeRefresh(0) })
	}
	writers.Wait()
	if requests.Load() != 1 {
		t.Fatal("overlapping write notifications started concurrent index passes")
	}
	close(release)
	for {
		if got, _ := idx.lookup("root"); got == 42 {
			if requests.Load() != 2 {
				t.Fatal("expected one follow-up after coalescing the writes")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("follow-up changes were not published")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
