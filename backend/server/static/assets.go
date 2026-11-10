package static

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// assetOverlay keeps versioned assets accessible to tabs opened before an update.
type assetOverlay struct {
	current fs.FS
	archive fs.FS
}

func (a assetOverlay) Open(name string) (fs.File, error) {
	f, err := a.current.Open(name)
	if errors.Is(err, fs.ErrNotExist) && len(name) > len("assets/") && name[:len("assets/")] == "assets/" {
		return a.archive.Open(name)
	}
	return f, err
}

func archiveAssets(current fs.FS, dir string) {
	err := fs.WalkDir(current, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := current.Open(name)
		if err != nil {
			return err
		}
		defer src.Close()
		tmp, err := os.CreateTemp(filepath.Dir(target), ".asset-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err = io.Copy(tmp, src); err != nil {
			tmp.Close()
			return err
		}
		if err = tmp.Chmod(0o644); err != nil {
			tmp.Close()
			return err
		}
		if err = tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), target)
	})
	if err != nil {
		utils.Log.Warnf("could not preserve previous web assets: %v", err)
	}
}
