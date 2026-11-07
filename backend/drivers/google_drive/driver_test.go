package google_drive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
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
