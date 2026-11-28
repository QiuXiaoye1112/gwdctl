package handles

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
)

func TestUploadNestedFoldersAndPackageMultipleRoots(t *testing.T) {
	const mount = "/folder-zip"
	_, router := setupHandleMoveCopyEnv(t, mount)
	sqlDB, err := db.GetDb().DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	router.POST("/api/fs/mkdir", FsMkdir)
	router.PUT("/api/fs/put", FsStream)
	router.POST("/api/fs/package_download", FsPackageDownloadCreate)

	folders := []string{"Folder A", "Folder A/nested", "Folder B", "Folder B/empty", "Empty Folder"}
	for _, p := range folders {
		resp := postJSON(t, router, "/api/fs/mkdir", fmt.Sprintf("{\"path\":%q}", mount+"/"+p))
		if resp.Code != 200 {
			t.Fatalf("mkdir %s: %d %s", p, resp.Code, resp.Message)
		}
	}
	files := map[string]string{"Folder A/same.txt": "A", "Folder A/nested/中文.txt": "A/nested", "Folder B/same.txt": "B"}
	for f, content := range files {
		req := httptest.NewRequest(http.MethodPut, "/api/fs/put", strings.NewReader(content))
		req.Header.Set("File-Path", mount+"/"+f)
		req.Header.Set("Overwrite", "skip")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var resp apiResp
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Code != 200 {
			t.Fatalf("upload %s: %s (%v)", f, rec.Body.String(), err)
		}
	}
	resp := postJSON(t, router, "/api/fs/package_download", `{"path":"/folder-zip","names":["Folder A","Folder B","Empty Folder"]}`)
	var data struct{ Ticket string }
	if err := json.Unmarshal(resp.Data, &data); err != nil || resp.Code != 200 || data.Ticket == "" {
		t.Fatalf("create ZIP ticket: %d %s (%v)", resp.Code, resp.Message, err)
	}
	ticket, ok := packageDownloadTickets.Get(data.Ticket)
	if !ok {
		t.Fatal("missing ZIP ticket")
	}
	entries, err := collectPackageEntries(context.Background(), ticket)
	if err != nil {
		t.Fatal(err)
	}
	// The in-memory storage fixture has no Link implementation. Use the actual
	// recursive ticket entries and ZIP writer, supplying fixture file content.
	var out bytes.Buffer
	if err := writeStreamingZip(context.Background(), &out, entries, func(ctx context.Context, e packageDownloadEntry) (io.ReadCloser, error) {
		want, ok := files[e.ZipPath]
		if !ok || e.Size != int64(len(want)) {
			return nil, fmt.Errorf("unexpected entry %q size %d", e.ZipPath, e.Size)
		}
		return io.NopCloser(strings.NewReader(want)), nil
	}); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range z.File {
		if seen[e.Name] {
			t.Fatalf("duplicate zip path %q", e.Name)
		}
		seen[e.Name] = true
		if e.FileInfo().IsDir() {
			continue
		}
		f, err := e.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(f)
		f.Close()
		want, ok := files[e.Name]
		if err != nil || !ok || string(got) != want {
			t.Fatalf("zip content mismatch %q %q %v", e.Name, got, err)
		}
	}
	for name := range files {
		if !seen[name] {
			t.Fatalf("missing %q", name)
		}
	}
	for _, directory := range []string{"Folder B/empty/", "Empty Folder/"} {
		if !seen[directory] {
			t.Fatalf("missing empty directory %q", directory)
		}
	}
}
