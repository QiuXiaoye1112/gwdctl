package archives

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/archive/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

type Archives struct {
}

func (Archives) AcceptedExtensions() []string {
	return []string{
		".br", ".bz2", ".gz", ".lz4", ".lz", ".mz", ".sz", ".s2", ".xz", ".zz", ".zst", ".tar",
		".tgz", ".tlz4", ".tlz", ".tbz2", ".txz", ".tzst",
	}
}

func (Archives) AcceptedMultipartExtensions() map[string]tool.MultipartExtension {
	return map[string]tool.MultipartExtension{}
}

func (Archives) GetMeta(ss []*stream.SeekableStream, args model.ArchiveArgs) (model.ArchiveMeta, error) {
	fsys, err := getFs(ss[0], args)
	if err != nil {
		return nil, err
	}
	files, err := fsys.ReadDir(".")
	if err != nil {
		return nil, filterPassword(err)
	}

	tree := make([]model.ObjTree, 0, len(files))
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			continue
		}
		tree = append(tree, &model.ObjectTree{Object: *toModelObj(info)})
	}
	return &model.ArchiveMetaInfo{
		Comment:   "",
		Encrypted: false,
		Tree:      tree,
	}, nil
}

func (Archives) List(ss []*stream.SeekableStream, args model.ArchiveInnerArgs) ([]model.Obj, error) {
	fsys, err := getFs(ss[0], args.ArchiveArgs)
	if err != nil {
		return nil, err
	}
	innerPath := strings.TrimPrefix(args.InnerPath, "/")
	if innerPath == "" {
		innerPath = "."
	}
	obj, err := fsys.ReadDir(innerPath)
	if err != nil {
		return nil, filterPassword(err)
	}
	return utils.SliceConvert(obj, func(src os.DirEntry) (model.Obj, error) {
		info, err := src.Info()
		if err != nil {
			return nil, err
		}
		return toModelObj(info), nil
	})
}

func (Archives) Extract(ss []*stream.SeekableStream, args model.ArchiveInnerArgs) (io.ReadCloser, int64, error) {
	fsys, err := getFs(ss[0], args.ArchiveArgs)
	if err != nil {
		return nil, 0, err
	}
	file, err := fsys.Open(strings.TrimPrefix(args.InnerPath, "/"))
	if err != nil {
		return nil, 0, filterPassword(err)
	}
	stat, err := file.Stat()
	if err != nil {
		return nil, 0, filterPassword(err)
	}
	return file, stat.Size(), nil
}

func (Archives) Decompress(ss []*stream.SeekableStream, outputPath string, args model.ArchiveInnerArgs, up model.UpdateProgress) error {
	fsys, err := getFs(ss[0], args.ArchiveArgs)
	if err != nil {
		return err
	}
	isDir := false
	path := strings.TrimPrefix(args.InnerPath, "/")
	if path == "" {
		isDir = true
		path = "."
	} else {
		stat, err := fsys.Stat(path)
		if err != nil {
			return filterPassword(err)
		}
		if stat.IsDir() {
			isDir = true
			outputPath = filepath.Join(outputPath, stat.Name())
			err = os.Mkdir(outputPath, 0700)
			if err != nil {
				return filterPassword(err)
			}
		}
	}
	if isDir {
		totalFiles := 0
		_ = fs.WalkDir(fsys, path, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d != nil && !d.IsDir() {
				totalFiles++
			}
			return nil
		})
		processed := 0
		err = fs.WalkDir(fsys, path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p == path || p == "." {
				return nil
			}
			relPath := strings.TrimPrefix(p, path+"/")
			dstPath := filepath.Join(outputPath, relPath)
			if dstPath == outputPath {
				if d != nil && d.IsDir() {
					return nil
				}
			}
			if !strings.HasPrefix(dstPath, outputPath+string(os.PathSeparator)) {
				return fmt.Errorf("illegal file path: %s", relPath)
			}
			if d.IsDir() {
				err = os.MkdirAll(dstPath, 0700)
			} else {
				dir := filepath.Dir(dstPath)
				err = decompress(fsys, p, dir, func(_ float64) {})
				if err == nil && totalFiles > 0 {
					processed++
					up(float64(processed) * 100.0 / float64(totalFiles))
				}
			}
			return err
		})
	} else {
		err = decompress(fsys, path, outputPath, up)
	}
	if err == nil {
		up(100)
	}
	return filterPassword(err)
}

var _ tool.Tool = (*Archives)(nil)

func init() {
	tool.RegisterTool(Archives{})
}
