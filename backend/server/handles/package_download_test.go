package handles

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type trackingReadCloser struct {
	io.Reader
	closed atomic.Bool
}

func (t *trackingReadCloser) Close() error {
	t.closed.Store(true)
	return nil
}

func TestDedupeZipPath(t *testing.T) {
	used := make(map[string]struct{})
	cases := []struct {
		raw   string
		isDir bool
		want  string
	}{
		{"photos/a.jpg", false, "photos/a.jpg"},
		{"photos/a.jpg", false, "photos/a (1).jpg"},
		{"photos/a.jpg", false, "photos/a (2).jpg"},
		{"README", false, "README"},
		{"README", false, "README (1)"},
		{".gitignore", false, ".gitignore"},
		{".gitignore", false, ".gitignore (1)"},
		{"empty_dir/", true, "empty_dir"},
		{"empty_dir", true, "empty_dir (1)"},
		{`\win\path\img.png`, false, "win/path/img.png"},
	}
	for _, tc := range cases {
		if got := dedupeZipPath(tc.raw, tc.isDir, used); got != tc.want {
			t.Fatalf("dedupeZipPath(%q, %v) = %q, want %q", tc.raw, tc.isDir, got, tc.want)
		}
	}
}

func TestWriteStreamingZipConcurrentPrefetchAndContents(t *testing.T) {
	const fileCount = 12
	entries := make([]packageDownloadEntry, 0, fileCount+2)
	expected := make(map[string][]byte, fileCount)

	now := time.Now().Truncate(2 * time.Second)
	for i := 0; i < fileCount; i++ {
		zipPath := fmt.Sprintf("album/img_%02d.jpg", i)
		payload := bytes.Repeat([]byte{byte('a' + (i % 26))}, 1024+i*13)
		expected[zipPath] = payload
		entries = append(entries, packageDownloadEntry{
			ZipPath: zipPath,
			Size:    int64(len(payload)),
			ModTime: now,
		})
	}
	// Duplicate entry name should be deduped cleanly.
	dupPayload := []byte("duplicate-content")
	expected["album/img_00 (1).jpg"] = dupPayload
	entries = append(entries, packageDownloadEntry{
		ZipPath: "album/img_00.jpg",
		Size:    int64(len(dupPayload)),
		ModTime: now,
	})
	// Empty directory entry should be preserved.
	entries = append(entries, packageDownloadEntry{
		ZipPath: "album/empty",
		ModTime: now,
		IsDir:   true,
	})

	// Large file (> packageDownloadInlineLimit) streamed on demand.
	largePayload := bytes.Repeat([]byte("Z"), packageDownloadInlineLimit+128*1024)
	expected["album/large.bin"] = largePayload
	entries = append(entries, packageDownloadEntry{
		ZipPath: "album/large.bin",
		Size:    int64(len(largePayload)),
		ModTime: now,
	})

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var openCount atomic.Int32
	var trackersMu sync.Mutex
	var trackers []*trackingReadCloser

	var out bytes.Buffer
	err := writeStreamingZip(context.Background(), &out, entries, func(ctx context.Context, entry packageDownloadEntry) (io.ReadCloser, error) {
		openCount.Add(1)
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		defer inFlight.Add(-1)

		var payload []byte
		if entry.ZipPath == "album/img_00.jpg" && entry.Size == int64(len(dupPayload)) {
			payload = dupPayload
		} else {
			time.Sleep(15 * time.Millisecond)
			payload = expected[entry.ZipPath]
		}
		tr := &trackingReadCloser{Reader: bytes.NewReader(payload)}
		trackersMu.Lock()
		trackers = append(trackers, tr)
		trackersMu.Unlock()
		return tr, nil
	})
	if err != nil {
		t.Fatalf("writeStreamingZip failed: %v", err)
	}
	for idx, tr := range trackers {
		if !tr.closed.Load() {
			t.Fatalf("stream %d was not closed", idx)
		}
	}
	if maxInFlight.Load() < 2 {
		t.Fatalf("expected concurrent prefetch (maxInFlight >= 2), got %d", maxInFlight.Load())
	}

	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader failed: %v", err)
	}
	if len(zr.File) != len(entries) {
		t.Fatalf("expected %d zip entries, got %d", len(entries), len(zr.File))
	}

	seenDir := false
	for _, f := range zr.File {
		if f.Name == "album/empty/" {
			seenDir = true
			if !f.FileInfo().IsDir() {
				t.Fatalf("expected album/empty/ to be dir")
			}
			continue
		}
		want, ok := expected[f.Name]
		if !ok {
			t.Fatalf("unexpected zip entry: %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", f.Name, err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", f.Name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("content mismatch for %s: got len %d, want %d", f.Name, len(got), len(want))
		}
	}
	if !seenDir {
		t.Fatalf("missing empty directory entry album/empty/")
	}
	if openCount.Load() != int32(fileCount+2) {
		t.Fatalf("unexpected openCount: %d", openCount.Load())
	}
}

func TestWriteStreamingZipCancelClosesStreams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entries := make([]packageDownloadEntry, 8)
	for i := range entries {
		entries[i] = packageDownloadEntry{
			ZipPath: fmt.Sprintf("f_%d.bin", i),
			Size:    packageDownloadInlineLimit + 1024, // force stream-on-demand or slow prefetch
		}
	}
	// Make first 6 small so workers start concurrently, and hang on read until canceled.
	for i := 0; i < 6; i++ {
		entries[i].Size = 100
	}

	var firstOpened atomic.Int32
	pr, pw := io.Pipe()
	defer pw.Close()

	err := writeStreamingZip(ctx, io.Discard, entries, func(ctx context.Context, entry packageDownloadEntry) (io.ReadCloser, error) {
		if firstOpened.Add(1) == 1 {
			cancel()
		}
		<-ctx.Done()
		return io.NopCloser(pr), ctx.Err()
	})
	if err == nil {
		t.Fatalf("expected error on canceled context")
	}
}
