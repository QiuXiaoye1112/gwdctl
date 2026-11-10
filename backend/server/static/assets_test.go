package static

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestArchivedAssetsSurviveNewBuild(t *testing.T) {
	dir := t.TempDir()
	first := fstest.MapFS{"assets/old.js": &fstest.MapFile{Data: []byte("old")}, "index.html": &fstest.MapFile{Data: []byte("old index")}}
	archiveAssets(first, dir)
	second := fstest.MapFS{"assets/new.js": &fstest.MapFile{Data: []byte("new")}, "index.html": &fstest.MapFile{Data: []byte("new index")}}
	archiveAssets(second, dir)
	overlay := assetOverlay{current: second, archive: os.DirFS(dir)}
	for path, want := range map[string]string{"assets/old.js": "old", "assets/new.js": "new", "index.html": "new index"} {
		got, err := fs.ReadFile(overlay, path)
		if err != nil || string(got) != want {
			t.Fatalf("%s: got %q, %v", path, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); !os.IsNotExist(err) {
		t.Fatalf("index should not be archived: %v", err)
	}
	if _, err := fs.ReadFile(overlay, "assets/missing.js"); !os.IsNotExist(err) {
		t.Fatalf("missing asset: %v", err)
	}
}
