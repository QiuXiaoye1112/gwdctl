package fs

import (
	"context"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
)

// putDirect put the file and return after finish
func putDirectly(ctx context.Context, dstDirPath string, file model.FileStreamer, skipHook ...bool) error {
	storage, dstDirActualPath, err := op.GetStorageAndActualPath(dstDirPath)
	if err != nil {
		_ = file.Close()
		return errors.WithMessage(err, "failed get storage")
	}
	if storage.Config().NoUpload {
		_ = file.Close()
		return errors.WithStack(errs.UploadNotSupported)
	}
	if utils.IsBool(skipHook...) {
		ctx = context.WithValue(ctx, conf.SkipHookKey, struct{}{})
	}
	return op.Put(ctx, storage, dstDirActualPath, file, nil)
}

func getDirectUploadInfo(ctx context.Context, tool, dstDirPath, dstName string, fileSize int64, overwrite bool) (any, error) {
	storage, dstDirActualPath, err := op.GetStorageAndActualPath(dstDirPath)
	if err != nil {
		return nil, errors.WithMessage(err, "failed get storage")
	}
	return op.GetDirectUploadInfo(ctx, tool, storage, dstDirActualPath, dstName, fileSize, overwrite)
}
