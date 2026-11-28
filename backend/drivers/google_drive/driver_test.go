package google_drive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/go-resty/resty/v2"
)

func TestShortcutSeparation(t *testing.T) {
	// B01: Verify shortcut retains its own ID and target ID separately
	shortcutFile := File{
		Id:           "shortcut-123",
		Name:         "MyShortcut",
		MimeType:     "application/vnd.google-apps.shortcut",
		ModifiedTime: time.Now(),
		CreatedTime:  time.Now(),
		Size:         "1024",
	}
	shortcutFile.ShortcutDetails.TargetId = "original-target-456"
	shortcutFile.ShortcutDetails.TargetMimeType = "application/vnd.google-apps.folder"

	obj := fileToObj(shortcutFile)

	// obj.GetID() must be the shortcut's own ID so Remove/Rename/Move operate on shortcut
	if obj.GetID() != "shortcut-123" {
		t.Fatalf("expected shortcut obj.GetID() to be shortcut-123, got %s", obj.GetID())
	}
	if !obj.IsDir() {
		t.Fatalf("expected shortcut to folder to report IsDir() == true")
	}

	// getTargetID must return the original target ID
	targetID := getTargetID(obj)
	if targetID != "original-target-456" {
		t.Fatalf("expected target ID original-target-456, got %s", targetID)
	}

	// getReadID must dereference the target ID for read/link operations
	readID := getReadID(obj)
	if readID != "original-target-456" {
		t.Fatalf("expected getReadID() to dereference to original-target-456, got %s", readID)
	}

	// For wrapped object (e.g. ObjWrapName), getReadID must still unwrap and dereference
	wrapped := &model.ObjWrapName{Name: "Wrapped", Obj: obj}
	if getReadID(wrapped) != "original-target-456" {
		t.Fatalf("expected unwrapped read ID to be original-target-456, got %s", getReadID(wrapped))
	}

	// Normal file should have empty targetID and readID == obj.GetID()
	normalFile := File{
		Id:           "normal-789",
		Name:         "NormalFile.txt",
		MimeType:     "text/plain",
		ModifiedTime: time.Now(),
		CreatedTime:  time.Now(),
		Size:         "512",
	}
	normalObj := fileToObj(normalFile)
	if normalObj.GetID() != "normal-789" {
		t.Fatalf("expected normal obj.GetID() to be normal-789, got %s", normalObj.GetID())
	}
	if getTargetID(normalObj) != "" {
		t.Fatalf("expected empty target ID for normal file, got %s", getTargetID(normalObj))
	}
	if getReadID(normalObj) != "normal-789" {
		t.Fatalf("expected getReadID() for normal file to be normal-789, got %s", getReadID(normalObj))
	}
}

func TestCleanUploadUrl(t *testing.T) {
	// B03: Verify URL query parameter merging doesn't introduce duplicate '?'
	rawSession := "https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&upload_id=SESSION123"
	cleaned := cleanUploadUrl(rawSession)

	// Must contain upload_id=SESSION123 cleanly without being corrupted by '?'
	if !bytes.Contains([]byte(cleaned), []byte("upload_id=SESSION123")) {
		t.Fatalf("upload_id was corrupted: %s", cleaned)
	}
	if bytes.Count([]byte(cleaned), []byte("?")) != 1 {
		t.Fatalf("expected exactly one '?' in url, got: %s", cleaned)
	}
}

func TestChunkUploadFailureReporting(t *testing.T) {
	// B04: Verify 503 HTML error page is reported as error and NOT success
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("<html><body>503 Service Unavailable</body></html>"))
	}))
	defer ts.Close()

	d := &GoogleDrive{
		Addition: Addition{
			ChunkSize: 5, // 5 MiB
		},
		AccessToken: "test-token",
	}

	data := make([]byte, 6*1024*1024)
	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "test.bin",
			Size: int64(len(data)),
		},
		Reader: bytes.NewReader(data),
	}

	var progress float64
	up := func(p float64) {
		progress = p
	}

	oldClient := base.HttpClient
	base.HttpClient = ts.Client()
	defer func() { base.HttpClient = oldClient }()

	err := d.chunkUpload(context.Background(), streamer, ts.URL, up)
	if err == nil {
		t.Fatalf("expected error on 503 response, but got nil (falsely reported success!)")
	}
	if progress == 100 {
		t.Fatalf("progress should not reach 100 on failed upload")
	}
}

func TestChunkUploadSuccessAndContentLength(t *testing.T) {
	// B03: Verify Content-Length is properly set on chunks and not chunked transfer
	var chunkCount int
	totalDataSize := int64(10 * 1024 * 1024) // 10 MiB = 2 chunks of 5 MiB
	expectedChunkSize := int64(5 * 1024 * 1024)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunkCount++
		// Verify ContentLength is set on request
		if r.ContentLength != expectedChunkSize {
			t.Errorf("chunk %d: expected ContentLength %d, got %d", chunkCount, expectedChunkSize, r.ContentLength)
		}
		if len(r.TransferEncoding) > 0 && r.TransferEncoding[0] == "chunked" {
			t.Errorf("chunk %d: request should NOT have Transfer-Encoding: chunked", chunkCount)
		}

		if chunkCount == 1 {
			// First chunk returns 308 Resume Incomplete
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", expectedChunkSize-1))
			w.WriteHeader(308)
		} else {
			// Second chunk completes upload
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id": "uploaded-file-id", "name": "test.bin"}`))
		}
	}))
	defer ts.Close()

	d := &GoogleDrive{
		Addition: Addition{
			ChunkSize: 5,
		},
		AccessToken: "test-token",
	}

	data := make([]byte, totalDataSize)
	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "test.bin",
			Size: totalDataSize,
		},
		Reader: bytes.NewReader(data),
	}

	var finalProgress float64
	up := func(p float64) {
		finalProgress = p
	}

	// base.HttpClient is used by chunkUpload
	oldClient := base.HttpClient
	base.HttpClient = ts.Client()
	defer func() { base.HttpClient = oldClient }()

	err := d.chunkUpload(context.Background(), streamer, ts.URL, up)
	if err != nil {
		t.Fatalf("unexpected error during chunk upload: %v", err)
	}
	if chunkCount != 2 {
		t.Fatalf("expected 2 chunks, got %d", chunkCount)
	}
	if finalProgress != 100 {
		t.Fatalf("expected 100%% progress, got %f", finalProgress)
	}
}

func TestChunkUpload308WithoutRangeFails(t *testing.T) {
	// Issue 2: Server returns 308 without Range header. Must NOT report success or guess nextOffset.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 308 Resume Incomplete with NO Range header
		w.WriteHeader(308)
	}))
	defer ts.Close()

	d := &GoogleDrive{
		Addition: Addition{
			ChunkSize: 5,
		},
		AccessToken: "test-token",
	}

	data := make([]byte, 5*1024*1024)
	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "test.bin",
			Size: int64(len(data)),
		},
		Reader: bytes.NewReader(data),
	}

	oldClient := base.HttpClient
	base.HttpClient = ts.Client()
	defer func() { base.HttpClient = oldClient }()

	err := d.chunkUpload(context.Background(), streamer, ts.URL, func(p float64) {})
	if err == nil {
		t.Fatalf("expected error when 308 has no Range header, but got nil (falsely reported success!)")
	}
}

func TestSmallFileRequest503Fails(t *testing.T) {
	// Issue 3: Upstream returns 503 HTML error page on request(). Must report error, not err = nil.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("<html><body>503 Service Unavailable</body></html>"))
	}))
	defer ts.Close()

	d := &GoogleDrive{
		AccessToken: "test-token",
	}

	oldResty := base.RestyClient
	base.RestyClient = resty.New().SetTransport(ts.Client().Transport)
	defer func() { base.RestyClient = oldResty }()

	_, err := d.request(ts.URL, http.MethodGet, nil, nil)
	if err == nil {
		t.Fatalf("expected error on 503 HTML response in request(), got nil")
	}
}

type onlyReader struct {
	r io.Reader
}

func (o *onlyReader) Read(p []byte) (int, error) {
	return o.r.Read(p)
}

func TestChunkUploadPartialConfirmationNonSeekableStream(t *testing.T) {
	// Issue 4: Non-seekable stream where server partially confirms chunk (512 KiB of 1 MiB).
	// Must resume remaining bytes from buffered chunk without triggering 'stream not cached' error.
	var reqCount int
	totalSize := int64(1024 * 1024) // 1 MiB

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		// Drain incoming chunk body
		_, _ = io.Copy(io.Discard, r.Body)
		if reqCount == 1 {
			// First request sends 1 MiB, server only confirms 512 KiB
			w.Header().Set("Range", "bytes=0-524287")
			w.WriteHeader(308)
		} else if reqCount == 2 {
			// Second request sends remaining 512 KiB (524288 to 1048575)
			rangeHeader := r.Header.Get("Content-Range")
			if rangeHeader != "bytes 524288-1048575/1048576" {
				t.Errorf("expected Content-Range 'bytes 524288-1048575/1048576', got %q", rangeHeader)
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id": "uploaded-file", "name": "test.bin"}`))
		} else {
			t.Errorf("unexpected request #%d", reqCount)
		}
	}))
	defer ts.Close()

	d := &GoogleDrive{
		Addition: Addition{
			ChunkSize: 1, // 1 MiB chunk
		},
		AccessToken: "test-token",
	}

	data := make([]byte, totalSize)
	// onlyReader does NOT implement io.Seeker or io.ReaderAt.
	// This forces stream.NewStreamSectionReader to use hybridSectionReader!
	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "test.bin",
			Size: totalSize,
		},
		Reader: &onlyReader{r: bytes.NewReader(data)},
	}

	oldClient := base.HttpClient
	base.HttpClient = ts.Client()
	defer func() { base.HttpClient = oldClient }()

	var progress float64
	err := d.chunkUpload(context.Background(), streamer, ts.URL, func(p float64) {
		progress = p
	})
	if err != nil {
		t.Fatalf("expected success with partial chunk resume on non-seekable stream, got error: %v", err)
	}
	if reqCount != 2 {
		t.Fatalf("expected 2 requests, got %d", reqCount)
	}
	if progress != 100 {
		t.Fatalf("expected progress 100, got %f", progress)
	}
}

func TestChunkUpload308FullRangeStillRequires200OK(t *testing.T) {
	// Issue 2: Even if 308 Range covers all bytes, 308 cannot substitute for final 200/201 response.
	totalSize := int64(1024 * 1024)
	queryReceived := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		cr := r.Header.Get("Content-Range")
		if cr == "bytes */1048576" {
			// This is the status query!
			queryReceived = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id": "final-file-id"}`))
			return
		}
		// First upload chunk: returns 308 covering all bytes
		w.Header().Set("Range", "bytes=0-1048575")
		w.WriteHeader(308)
	}))
	defer ts.Close()

	d := &GoogleDrive{
		Addition: Addition{
			ChunkSize: 1,
		},
		AccessToken: "test-token",
	}

	data := make([]byte, totalSize)
	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "test.bin",
			Size: totalSize,
		},
		Reader: bytes.NewReader(data),
	}

	oldClient := base.HttpClient
	base.HttpClient = ts.Client()
	defer func() { base.HttpClient = oldClient }()

	var progress float64
	err := d.chunkUpload(context.Background(), streamer, ts.URL, func(p float64) {
		progress = p
	})
	if err != nil {
		t.Fatalf("expected upload to query and succeed on 200 OK, got error: %v", err)
	}
	if !queryReceived {
		t.Fatalf("expected status query (Content-Range: bytes */totalSize) when 308 covered full range")
	}
	if progress != 100 {
		t.Fatalf("expected progress 100, got %f", progress)
	}
}

func TestSmallFileUpload308FailsWhen200NeverReceived(t *testing.T) {
	// Issue 1: 1 KiB file where server only returns 308 (never 200/201) must fail with error
	var totalRequests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		totalRequests++
		_, _ = io.Copy(io.Discard, r.Body)

		// Upload PUT requests always return 308 (never 200/201)
		w.Header().Set("Range", "bytes=0-1023")
		w.WriteHeader(308)
	}))
	defer ts.Close()

	d := &GoogleDrive{
		Addition: Addition{
			ChunkSize: 5,
		},
		AccessToken: "test-token",
	}

	oldClient := base.HttpClient
	base.HttpClient = ts.Client()
	defer func() { base.HttpClient = oldClient }()

	data := make([]byte, 1024)
	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "small.bin",
			Size: 1024,
		},
		Reader: bytes.NewReader(data),
	}

	err := d.chunkUpload(context.Background(), streamer, ts.URL, func(p float64) {})
	if err == nil {
		t.Fatalf("expected chunkUpload() to return error when server only returns 308 and never 200/201, but got nil!")
	}
	if totalRequests == 0 {
		t.Fatalf("expected requests to reach mock server")
	}
}

func TestEmptyFileUploadSucceedsOn200(t *testing.T) {
	// Empty file (0 bytes) upload succeeds when server returns 200 OK
	var received bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Header.Get("Content-Range") != "bytes */0" {
			t.Errorf("expected Content-Range: bytes */0 for empty file, got: %s", r.Header.Get("Content-Range"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "empty-file-id"}`))
	}))
	defer ts.Close()

	d := &GoogleDrive{
		Addition: Addition{
			ChunkSize: 5,
		},
		AccessToken: "test-token",
	}

	oldClient := base.HttpClient
	base.HttpClient = ts.Client()
	defer func() { base.HttpClient = oldClient }()

	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "empty.bin",
			Size: 0,
		},
		Reader: bytes.NewReader([]byte{}),
	}

	var progress float64
	err := d.chunkUpload(context.Background(), streamer, ts.URL, func(p float64) {
		progress = p
	})
	if err != nil {
		t.Fatalf("expected empty file upload to succeed, got: %v", err)
	}
	if !received {
		t.Fatalf("expected mock server to receive empty file upload request")
	}
	if progress != 100 {
		t.Fatalf("expected progress 100 for empty file, got %f", progress)
	}
}

func TestAllBytesConfirmed308DoesNotLoopEndlessly(t *testing.T) {
	// Issue 3: Persistent full-range 308 must not loop thousands of times;
	// it must cap at 5 status queries with backoff and exit cleanly.
	var requestCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Range", "bytes=0-1023")
		w.WriteHeader(308)
	}))
	defer ts.Close()

	d := &GoogleDrive{
		Addition: Addition{
			ChunkSize: 5,
		},
		AccessToken: "test-token",
	}

	oldClient := base.HttpClient
	base.HttpClient = ts.Client()
	defer func() { base.HttpClient = oldClient }()

	data := make([]byte, 1024)
	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "test.bin",
			Size: 1024,
		},
		Reader: bytes.NewReader(data),
	}

	start := time.Now()
	err := d.chunkUpload(context.Background(), streamer, ts.URL, func(p float64) {})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected error after retry exhaustion, got nil")
	}
	// Strictly 1 initial data upload + 5 status queries = 6 requests
	if requestCount != 6 {
		t.Fatalf("expected exactly 6 requests (1 initial upload + 5 status queries), got %d", requestCount)
	}
	// Total backoff is 100ms + 200ms + 300ms + 400ms + 500ms = 1500ms (~1.5s)
	if elapsed < 1400*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("expected total backoff around 1.5s, got %v", elapsed)
	}
}

type metadataTransport struct{ mime string }

func (m metadataTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := fmt.Sprintf(`{"id":"doc-id","mimeType":%q}`, m.mime)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestLinkExportsGoogleDocuments(t *testing.T) {
	old := base.RestyClient
	defer func() { base.RestyClient = old }()
	for _, tc := range []struct{ mime, wantPath, wantQuery string }{
		{"application/vnd.google-apps.document", "/drive/v3/files/doc-id/export", "text/markdown"},
		{"application/pdf", "/drive/v3/files/doc-id", ""},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			base.RestyClient = resty.New().SetTransport(metadataTransport{mime: tc.mime})
			d := &GoogleDrive{AccessToken: "test-token"}
			link, err := d.Link(context.Background(), &model.Object{ID: "doc-id"}, model.LinkArgs{})
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(link.URL)
			if err != nil {
				t.Fatal(err)
			}
			if u.Path != tc.wantPath || u.Query().Get("mimeType") != tc.wantQuery {
				t.Fatalf("unexpected link: %s", link.URL)
			}
			if tc.wantQuery == "" && u.Query().Get("alt") != "media" {
				t.Fatalf("regular file missing media request: %s", link.URL)
			}
		})
	}
}

type recordedRemoveReq struct {
	method string
	path   string
	query  url.Values
	body   map[string]any
}

type captureRemoveTransport struct {
	reqs *[]recordedRemoveReq
}

func (c captureRemoveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
	}
	*c.reqs = append(*c.reqs, recordedRemoveReq{
		method: req.Method,
		path:   req.URL.Path,
		query:  req.URL.Query(),
		body:   decoded,
	})
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"ok","trashed":true}`)),
	}, nil
}

func TestRemoveMovesToTrash(t *testing.T) {
	old := base.RestyClient
	defer func() { base.RestyClient = old }()

	var reqs []recordedRemoveReq
	base.RestyClient = resty.New().SetTransport(captureRemoveTransport{reqs: &reqs})
	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(context.Background())

	t.Run("regular_file", func(t *testing.T) {
		reqs = nil
		if err := d.Remove(context.Background(), &model.Object{ID: "file-123"}); err != nil {
			t.Fatalf("Remove returned error: %v", err)
		}
		if len(reqs) != 1 {
			t.Fatalf("expected 1 request, got %d", len(reqs))
		}
		got := reqs[0]
		if got.method != http.MethodPatch {
			t.Fatalf("expected PATCH to trash instead of DELETE, got %s", got.method)
		}
		if got.path != "/drive/v3/files/file-123" {
			t.Fatalf("unexpected path: %s", got.path)
		}
		if got.query.Get("supportsAllDrives") != "true" || got.query.Get("includeItemsFromAllDrives") != "true" {
			t.Fatalf("expected AllDrives query params true, got %v", got.query)
		}
		if len(got.body) != 1 || got.body["trashed"] != true {
			t.Fatalf("expected JSON body {\"trashed\":true}, got %v", got.body)
		}
	})

	t.Run("shortcut_trashes_shortcut_not_target", func(t *testing.T) {
		reqs = nil
		shortcutFile := File{
			Id:       "shortcut-123",
			Name:     "MyShortcut",
			MimeType: "application/vnd.google-apps.shortcut",
		}
		shortcutFile.ShortcutDetails.TargetId = "original-target-456"
		shortcutFile.ShortcutDetails.TargetMimeType = "application/vnd.google-apps.folder"
		obj := fileToObj(shortcutFile)

		if err := d.Remove(context.Background(), obj); err != nil {
			t.Fatalf("Remove shortcut returned error: %v", err)
		}
		if len(reqs) != 1 || reqs[0].path != "/drive/v3/files/shortcut-123" {
			t.Fatalf("expected shortcut's own ID to be trashed, got reqs=%+v", reqs)
		}
	})

	t.Run("nil_or_empty_id", func(t *testing.T) {
		reqs = nil
		if err := d.Remove(context.Background(), nil); !errs.IsObjectNotFound(err) {
			t.Fatalf("expected ObjectNotFound for nil obj, got %v", err)
		}
		if err := d.Remove(context.Background(), &model.Object{}); !errs.IsObjectNotFound(err) {
			t.Fatalf("expected ObjectNotFound for empty ID obj, got %v", err)
		}
		if len(reqs) != 0 {
			t.Fatalf("expected no HTTP calls on invalid object, got %d", len(reqs))
		}
	})
}

type persistent401Transport struct {
	apiCalls     int
	refreshCalls int
}

func (p *persistent401Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Host, "googleapis.com") && strings.Contains(req.URL.Path, "oauth2") {
		p.refreshCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"access_token":"refreshed-%d"}`, p.refreshCalls))),
			Request:    req,
		}, nil
	}
	p.apiCalls++
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":401,"message":"Invalid Credentials"}}`)),
		Request:    req,
	}, nil
}

func TestRequest401RetriesAtMostOnce(t *testing.T) {
	old := base.RestyClient
	defer func() { base.RestyClient = old }()

	tr := &persistent401Transport{}
	base.RestyClient = resty.New().SetTransport(tr)
	d := &GoogleDrive{
		Addition: Addition{
			ClientID:     "cid",
			ClientSecret: "csec",
			RefreshToken: "rtok",
		},
		AccessToken: "expired-token",
	}

	_, err := d.requestWithContext(context.Background(), "https://www.googleapis.com/drive/v3/files/abc", http.MethodGet, nil, nil)
	if err == nil {
		t.Fatalf("expected error on persistent 401, got nil")
	}
	if tr.refreshCalls != 1 {
		t.Fatalf("expected exactly 1 token refresh, got %d", tr.refreshCalls)
	}
	if tr.apiCalls != 2 {
		t.Fatalf("expected exactly 2 API calls (initial + 1 retry), got %d", tr.apiCalls)
	}
}

func TestCopyRespectsCanceledContext(t *testing.T) {
	old := base.RestyClient
	defer func() { base.RestyClient = old }()

	var called bool
	base.RestyClient = resty.New().SetTransport(captureRemoveTransport{reqs: new([]recordedRemoveReq)})
	_ = called

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &GoogleDrive{AccessToken: "test-token"}
	err := d.Copy(ctx, &model.Object{ID: "src-1", Name: "a.txt"}, &model.Object{ID: "dst-1", IsFolder: true})
	if err == nil {
		t.Fatalf("expected context canceled error, got nil")
	}
}

func TestComputeFolderSizes(t *testing.T) {
	const folderMime = "application/vnd.google-apps.folder"
	files := []sizeMetaFile{
		// Multi-level tree: root -> a -> b -> c, plus root -> empty
		{Id: "a", Parents: []string{"root"}, MimeType: folderMime},
		{Id: "b", Parents: []string{"a"}, MimeType: folderMime},
		{Id: "c", Parents: []string{"b"}, MimeType: folderMime},
		{Id: "empty", Parents: []string{"root"}, MimeType: folderMime},
		{Id: "f1", Parents: []string{"c"}, MimeType: "application/octet-stream", Size: "1000"},
		{Id: "f2", Parents: []string{"a"}, MimeType: "image/jpeg", Size: "500"},
		// Folder carrying a bogus size must be ignored
		{Id: "bogus-folder", Parents: []string{"a"}, MimeType: folderMime, Size: "99999"},
		// Non-numeric / zero / empty sizes must be ignored
		{Id: "gdoc", Parents: []string{"a"}, MimeType: "application/vnd.google-apps.document", Size: ""},
		{Id: "zero", Parents: []string{"a"}, MimeType: "text/plain", Size: "0"},
		{Id: "bad", Parents: []string{"a"}, MimeType: "text/plain", Size: "not-a-number"},
		// Diamond DAG: shared file in both p1 and p2 under droot -> droot must count once
		{Id: "p1", Parents: []string{"droot"}, MimeType: folderMime},
		{Id: "p2", Parents: []string{"droot"}, MimeType: folderMime},
		{Id: "shared", Parents: []string{"p1", "p2"}, MimeType: "video/mp4", Size: "2048"},
		// Cyclic parent chain: cyc1 <-> cyc2 with a file inside cyc1
		{Id: "cyc1", Parents: []string{"cyc2"}, MimeType: folderMime},
		{Id: "cyc2", Parents: []string{"cyc1"}, MimeType: folderMime},
		{Id: "cycfile", Parents: []string{"cyc1"}, MimeType: "text/plain", Size: "77"},
	}

	got := computeFolderSizes(files)

	want := map[string]int64{
		"c":     1000,
		"b":     1000,
		"a":     1500,
		"root":  1500,
		"empty": 0,
		"p1":    2048,
		"p2":    2048,
		"droot": 2048,
		"cyc1":  77,
		"cyc2":  77,
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("folder %q: got size %d, want %d (all=%v)", id, got[id], w, got)
		}
	}
}

type sizeIndexMockTransport struct {
	allowIndex chan struct{}
	scans      atomic.Int32
	feeds      atomic.Int32
	afterCopy  chan struct{}
}

func (m *sizeIndexMockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	q := req.URL.Query().Get("q")
	pageToken := req.URL.Query().Get("pageToken")

	var body string
	switch {
	case strings.HasSuffix(req.URL.Path, "/startPageToken"):
		body = `{"startPageToken":"initial"}`
	case strings.HasSuffix(req.URL.Path, "/changes"):
		m.feeds.Add(1)
		if pageToken == "steady" && m.afterCopy != nil {
			select {
			case <-m.afterCopy:
				body = `{"newStartPageToken":"after","changes":[{"fileId":"file-sub1","changeType":"file","file":{"id":"file-sub1","mimeType":"application/octet-stream","parents":["dir-sub"],"size":"4096"}}]}`
			default:
			}
		}
		if body == "" {
			body = `{"newStartPageToken":"steady","changes":[]}`
		}

	case strings.Contains(q, "'root' in parents"):
		body = `{
			"files": [
				{"id":"dir-a","name":"DirA","mimeType":"application/vnd.google-apps.folder"},
				{"id":"dir-empty","name":"EmptyDir","mimeType":"application/vnd.google-apps.folder"},
				{"id":"sc-dir","name":"DirAShortcut","mimeType":"application/vnd.google-apps.shortcut","shortcutDetails":{"targetId":"dir-a","targetMimeType":"application/vnd.google-apps.folder"}},
				{"id":"file-1","name":"hello.txt","mimeType":"text/plain","size":"100"}
			]
		}`
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/copy"):
		if m.afterCopy != nil {
			close(m.afterCopy)
		}
		body = `{"id":"copied-1"}`
	case q == "trashed = false" && pageToken == "":
		m.scans.Add(1)
		if m.allowIndex != nil {
			select {
			case <-m.allowIndex:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		body = `{
			"nextPageToken": "page-2",
			"files": [
				{"id":"dir-a","parents":["root"],"mimeType":"application/vnd.google-apps.folder"},
				{"id":"dir-sub","parents":["dir-a"],"mimeType":"application/vnd.google-apps.folder"},
				{"id":"file-a1","parents":["dir-a"],"mimeType":"application/octet-stream","size":"1024"}
			]
		}`
	case q == "trashed = false" && pageToken == "page-2":
		body = `{
			"files": [
				{"id":"file-sub1","parents":["dir-sub"],"mimeType":"application/octet-stream","size":"2048"},
				{"id":"file-1","parents":["root"],"mimeType":"text/plain","size":"100"}
			]
		}`
	default:
		body = `{"files":[]}`
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestListAttachesDynamicFolderSizeIndex(t *testing.T) {
	old := base.RestyClient
	defer func() { base.RestyClient = old }()

	allowIndex := make(chan struct{})
	mock := &sizeIndexMockTransport{allowIndex: allowIndex}
	base.RestyClient = resty.New().SetTransport(mock)

	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(context.Background())

	objs, err := d.List(context.Background(), &model.Object{ID: "root", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(objs) != 4 {
		t.Fatalf("expected 4 objects, got %d", len(objs))
	}

	byName := make(map[string]model.Obj, len(objs))
	for _, o := range objs {
		byName[o.GetName()] = o
	}

	// Before background index completes, List returns immediately with 0 for folders and exact size for files.
	if got := byName["DirA"].GetSize(); got != 0 {
		t.Fatalf("expected DirA size 0 before index ready, got %d", got)
	}
	if got := byName["hello.txt"].GetSize(); got != 100 {
		t.Fatalf("expected hello.txt size 100, got %d", got)
	}

	// A plain list didn't schedule an index. Explicitly start the same work
	// that Init kicks off, then let its paginated mock response complete.
	idx := d.getSizeIndex()
	idx.mu.RLock()
	refreshing := idx.refreshing
	idx.mu.RUnlock()
	if refreshing {
		t.Fatal("a normal listing unexpectedly started a full size scan")
	}
	if _, err := d.List(context.Background(), &model.Object{ID: "root", IsFolder: true}, model.ListArgs{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	idx.mu.RLock()
	refreshing = idx.refreshing
	idx.mu.RUnlock()
	if refreshing {
		t.Fatal("bypassing the directory cache unexpectedly started a size scan")
	}
	d.refreshFolderSizes(false)
	close(allowIndex)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := d.lookupFolderSize("dir-a"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Already-returned (cached) model.Obj instances must immediately report live recursive sizes.
	if got := byName["DirA"].GetSize(); got != 3072 {
		t.Fatalf("expected DirA recursive size 3072, got %d", got)
	}
	if got := byName["DirAShortcut"].GetSize(); got != 3072 {
		t.Fatalf("expected DirAShortcut recursive size 3072 via targetId, got %d", got)
	}
	if got := byName["EmptyDir"].GetSize(); got != 0 {
		t.Fatalf("expected EmptyDir size 0, got %d", got)
	}
	if got := byName["hello.txt"].GetSize(); got != 100 {
		t.Fatalf("expected hello.txt size 100, got %d", got)
	}
}

// A copy completed during the initial scan must cause a second background
// pass even if nobody returns to the UI after the write.
func TestFolderSizeIndexFollowsSuccessfulCopyDuringScan(t *testing.T) {
	old := base.RestyClient
	defer func() { base.RestyClient = old }()
	allowIndex := make(chan struct{})
	mock := &sizeIndexMockTransport{allowIndex: allowIndex, afterCopy: make(chan struct{})}
	base.RestyClient = resty.New().SetTransport(mock)
	ctx := context.Background()
	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(ctx)

	d.refreshFolderSizes(false)
	src := &model.Object{ID: "file-sub1", Name: "test.bin"}
	dest := &model.Object{ID: "dir-sub", IsFolder: true}
	if err := d.Copy(ctx, src, dest); err != nil {
		t.Fatal(err)
	}
	idx := d.getSizeIndex()
	idx.mu.RLock()
	dirty := idx.dirty
	idx.mu.RUnlock()
	if !dirty {
		t.Fatal("successful copy did not queue background size calculation")
	}
	close(allowIndex)

	deadline := time.Now().Add(sizeIndexWriteDebounce + 3*time.Second)
	for time.Now().Before(deadline) {
		if sz, ready := d.lookupFolderSize("dir-a"); ready && sz == 5120 {
			if got := mock.scans.Load(); got != 1 {
				t.Fatalf("expected only a startup full listing, got %d", got)
			}
			if got := mock.feeds.Load(); got != 2 {
				t.Fatalf("expected one catch-up and one incremental request, got %d", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	sz, ready := d.lookupFolderSize("dir-a")
	t.Fatalf("background index did not catch the copy: size=%d ready=%v scans=%d", sz, ready, mock.scans.Load())
}

type failIfCalledTransport struct {
	calls int
}

func (f *failIfCalledTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls++
	return nil, fmt.Errorf("unexpected HTTP request to %s", r.URL.String())
}

func TestLinkSkipsMetadataRequestForGoogleDriveObj(t *testing.T) {
	old := base.RestyClient
	defer func() { base.RestyClient = old }()

	tr := &failIfCalledTransport{}
	base.RestyClient = resty.New().SetTransport(tr)
	d := &GoogleDrive{
		AccessToken: "cached-token",
		tokenExpiry: time.Now().Add(30 * time.Minute),
	}

	imgObj := fileToObj(File{
		Id:       "img-1",
		Name:     "photo.jpg",
		MimeType: "image/jpeg",
		Size:     "2048",
	})
	wrappedImg := &model.ObjWrapName{Name: "photo.jpg", Obj: imgObj}
	link, err := d.Link(context.Background(), wrappedImg, model.LinkArgs{})
	if err != nil {
		t.Fatalf("Link failed for GoogleDriveObj image: %v", err)
	}
	if tr.calls != 0 {
		t.Fatalf("expected 0 metadata HTTP calls, got %d", tr.calls)
	}
	u, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/drive/v3/files/img-1" || u.Query().Get("alt") != "media" {
		t.Fatalf("unexpected image download URL: %s", link.URL)
	}
	if got := link.Header.Get("Authorization"); got != "Bearer cached-token" {
		t.Fatalf("unexpected Authorization header: %q", got)
	}
	if link.Expiration == nil || *link.Expiration <= 0 || *link.Expiration > 5*time.Minute {
		t.Fatalf("unexpected link expiration: %v", link.Expiration)
	}

	docObj := fileToObj(File{
		Id:       "gdoc-1",
		Name:     "Notes",
		MimeType: "application/vnd.google-apps.document",
	})
	docLink, err := d.Link(context.Background(), docObj, model.LinkArgs{})
	if err != nil {
		t.Fatalf("Link failed for GoogleDriveObj document: %v", err)
	}
	if tr.calls != 0 {
		t.Fatalf("expected 0 metadata HTTP calls for doc, got %d", tr.calls)
	}
	du, err := url.Parse(docLink.URL)
	if err != nil {
		t.Fatal(err)
	}
	if du.Path != "/drive/v3/files/gdoc-1/export" || du.Query().Get("mimeType") != "text/markdown" {
		t.Fatalf("unexpected doc export URL: %s", docLink.URL)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPutSmallFileUsesMultipartUpload(t *testing.T) {
	oldClient := base.HttpClient
	defer func() { base.HttpClient = oldClient }()

	payloadBytes := []byte("hello small google drive file!")
	var gotMethod, gotURL, gotMetaName, gotMetaParent string
	var gotBody []byte
	var calls int32

	base.HttpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			gotMethod = r.Method
			gotURL = r.URL.String()

			mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "multipart/related" {
				t.Fatalf("expected multipart/related Content-Type, got %q (err=%v)", r.Header.Get("Content-Type"), err)
			}
			mr := multipart.NewReader(r.Body, params["boundary"])

			p1, err := mr.NextPart()
			if err != nil {
				t.Fatalf("missing metadata part: %v", err)
			}
			var meta struct {
				Name    string   `json:"name"`
				Parents []string `json:"parents"`
			}
			if err := json.NewDecoder(p1).Decode(&meta); err != nil {
				t.Fatalf("decode metadata part: %v", err)
			}
			gotMetaName = meta.Name
			if len(meta.Parents) > 0 {
				gotMetaParent = meta.Parents[0]
			}

			p2, err := mr.NextPart()
			if err != nil {
				t.Fatalf("missing media part: %v", err)
			}
			gotBody, err = io.ReadAll(p2)
			if err != nil {
				t.Fatalf("read media part: %v", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"small-1","name":"note.txt"}`)),
				Request:    r,
			}, nil
		}),
	}

	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(context.Background())

	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "note.txt",
			Size: int64(len(payloadBytes)),
		},
		Reader:   bytes.NewReader(payloadBytes),
		Mimetype: "text/plain",
	}
	var progress float64
	err := d.Put(context.Background(), &model.Object{ID: "parent-dir-1", IsFolder: true}, streamer, func(p float64) {
		progress = p
	})
	if err != nil {
		t.Fatalf("Put small file failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected single HTTP call, got %d", calls)
	}
	if gotMethod != http.MethodPost || !strings.Contains(gotURL, "uploadType=multipart") {
		t.Fatalf("unexpected request: %s %s", gotMethod, gotURL)
	}
	if gotMetaName != "note.txt" || gotMetaParent != "parent-dir-1" {
		t.Fatalf("unexpected metadata: name=%q parent=%q", gotMetaName, gotMetaParent)
	}
	if !bytes.Equal(gotBody, payloadBytes) {
		t.Fatalf("uploaded content mismatch: got %q", gotBody)
	}
	if progress != 100 {
		t.Fatalf("expected progress 100, got %v", progress)
	}
}

func TestPutSmallFileOverwriteUsesMediaPatch(t *testing.T) {
	oldClient := base.HttpClient
	defer func() { base.HttpClient = oldClient }()

	payloadBytes := []byte("updated content")
	var gotMethod, gotURL string
	var gotBody []byte

	base.HttpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotMethod = r.Method
			gotURL = r.URL.String()
			gotBody, _ = io.ReadAll(r.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"exist-1"}`)),
				Request:    r,
			}, nil
		}),
	}

	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(context.Background())

	streamer := &stream.FileStream{
		Obj: &model.Object{
			Name: "photo.jpg",
			Size: int64(len(payloadBytes)),
		},
		Exist:    &model.Object{ID: "exist-1", Name: "photo.jpg"},
		Reader:   bytes.NewReader(payloadBytes),
		Mimetype: "image/jpeg",
	}
	if err := d.Put(context.Background(), &model.Object{ID: "parent-1", IsFolder: true}, streamer, nil); err != nil {
		t.Fatalf("Put overwrite failed: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("expected PATCH for overwrite, got %s", gotMethod)
	}
	if !strings.Contains(gotURL, "/files/exist-1") || !strings.Contains(gotURL, "uploadType=media") {
		t.Fatalf("unexpected overwrite URL: %s", gotURL)
	}
	if !bytes.Equal(gotBody, payloadBytes) {
		t.Fatalf("unexpected body: %q", gotBody)
	}
}

func TestPutSmallFileRetriesOnRateLimitAnd5xx(t *testing.T) {
	oldClient := base.HttpClient
	oldSleep := retrySleep
	defer func() {
		base.HttpClient = oldClient
		retrySleep = oldSleep
	}()

	var sleeps int
	retrySleep = func(ctx context.Context, d time.Duration) error {
		sleeps++
		return nil
	}

	var attempts int32
	base.HttpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			n := atomic.AddInt32(&attempts, 1)
			_, _ = io.Copy(io.Discard, r.Body)
			switch n {
			case 1:
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"code":403,"message":"Rate Limit","errors":[{"reason":"userRateLimitExceeded"}]}}`)),
					Request:    r,
				}, nil
			case 2:
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader(`service unavailable`)),
					Request:    r,
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"ok-1"}`)),
					Request:    r,
				}, nil
			}
		}),
	}

	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(context.Background())

	streamer := &stream.FileStream{
		Obj:      &model.Object{Name: "retry.txt", Size: 5},
		Reader:   strings.NewReader("hello"),
		Mimetype: "text/plain",
	}
	if err := d.Put(context.Background(), &model.Object{ID: "dir-1", IsFolder: true}, streamer, nil); err != nil {
		t.Fatalf("expected retryable small upload to succeed, got %v", err)
	}
	if attempts != 3 || sleeps != 2 {
		t.Fatalf("expected 3 attempts and 2 sleeps, got attempts=%d sleeps=%d", attempts, sleeps)
	}

	// Non-retryable 403 (storageQuotaExceeded) should fail immediately without sleeping.
	var nonRetryCalls int32
	sleeps = 0
	base.HttpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			atomic.AddInt32(&nonRetryCalls, 1)
			_, _ = io.Copy(io.Discard, r.Body)
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"storageQuotaExceeded"}]}}`)),
				Request:    r,
			}, nil
		}),
	}
	streamer2 := &stream.FileStream{
		Obj:      &model.Object{Name: "fail.txt", Size: 5},
		Reader:   strings.NewReader("hello"),
		Mimetype: "text/plain",
	}
	if err := d.Put(context.Background(), &model.Object{ID: "dir-1", IsFolder: true}, streamer2, nil); err == nil {
		t.Fatalf("expected storageQuotaExceeded to fail")
	}
	if nonRetryCalls != 1 || sleeps != 0 {
		t.Fatalf("expected immediate failure without retries, got calls=%d sleeps=%d", nonRetryCalls, sleeps)
	}
}

func TestChunkUploadRetriesOnRateLimit(t *testing.T) {
	oldClient := base.HttpClient
	oldSleep := retrySleep
	defer func() {
		base.HttpClient = oldClient
		retrySleep = oldSleep
	}()

	var sleeps int
	retrySleep = func(ctx context.Context, d time.Duration) error {
		sleeps++
		return nil
	}

	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		switch n {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`too many requests`))
		case 2:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"User rate limit","errors":[{"reason":"userRateLimitExceeded"}]}}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chunk-ok"}`))
		}
	}))
	defer ts.Close()
	base.HttpClient = ts.Client()

	data := make([]byte, 6*1024*1024)
	d := &GoogleDrive{
		Addition:    Addition{ChunkSize: 16},
		AccessToken: "test-token",
	}
	streamer := &stream.FileStream{
		Obj:    &model.Object{Name: "large.bin", Size: int64(len(data))},
		Reader: bytes.NewReader(data),
	}
	var progress float64
	if err := d.chunkUpload(context.Background(), streamer, ts.URL, func(p float64) { progress = p }); err != nil {
		t.Fatalf("chunkUpload rate-limit retry failed: %v", err)
	}
	if calls != 3 || sleeps != 2 || progress != 100 {
		t.Fatalf("unexpected counts: calls=%d sleeps=%d progress=%v", calls, sleeps, progress)
	}
}

func TestMakeDirReturnsCreatedObj(t *testing.T) {
	oldResty := base.RestyClient
	defer func() { base.RestyClient = oldResty }()

	base.RestyClient = resty.New().SetTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/drive/v3/files") {
			t.Fatalf("unexpected MakeDir request: %s %s", r.Method, r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"folder-99","name":"SubFolder","mimeType":"application/vnd.google-apps.folder"}`,
			)),
			Request: r,
		}, nil
	}))

	d := &GoogleDrive{AccessToken: "test-token"}
	defer d.Drop(context.Background())

	obj, err := d.MakeDir(context.Background(), &model.Object{ID: "parent-1", IsFolder: true}, "SubFolder")
	if err != nil {
		t.Fatalf("MakeDir failed: %v", err)
	}
	if obj == nil {
		t.Fatalf("expected created obj, got nil")
	}
	if obj.GetID() != "folder-99" || obj.GetName() != "SubFolder" || !obj.IsDir() {
		t.Fatalf("unexpected created obj: id=%q name=%q isDir=%v", obj.GetID(), obj.GetName(), obj.IsDir())
	}
}
