package handles

import (
	"fmt"
	"slices"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/pkg/generic"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

type RecursiveMoveReq struct {
	SrcDir         string `json:"src_dir"`
	DstDir         string `json:"dst_dir"`
	ConflictPolicy string `json:"conflict_policy"`
}

// FsRecursiveMove recursively moves files (individual item permission checks skipped for performance).
func FsRecursiveMove(c *gin.Context) {
	var req RecursiveMoveReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}

	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanMove() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	srcDir, err := user.JoinPath(req.SrcDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	srcMeta, err := op.GetNearestMeta(srcDir)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !common.CanWrite(user, srcMeta, srcDir) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	common.GinAppendValues(c, conf.MetaKey, srcMeta)

	dstDir, err := user.JoinPath(req.DstDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	dstMeta, err := op.GetNearestMeta(dstDir)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !common.CanWrite(user, dstMeta, dstDir) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}

	switch req.ConflictPolicy {
	case CANCEL, OVERWRITE, SKIP:
	default:
		common.ErrorStrResp(c, fmt.Sprintf("invalid conflict policy [%s]", req.ConflictPolicy), 400)
		return
	}

	rootFiles, err := fs.List(c.Request.Context(), srcDir, &fs.ListArgs{Refresh: true})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}

	dstFiles, err := fs.List(c.Request.Context(), dstDir, &fs.ListArgs{Refresh: true})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	dstByName := indexObjsByName(dstFiles)
	existingFileNames := make([]string, 0, len(dstFiles))
	if req.ConflictPolicy != OVERWRITE {
		for _, dstFile := range dstFiles {
			existingFileNames = append(existingFileNames, dstFile.GetName())
		}
	}

	// record the file path
	filePathMap := make(map[model.Obj]string)
	movingFiles := generic.NewQueue[model.Obj]()
	movingFileNames := make([]string, 0, len(rootFiles))
	for _, file := range rootFiles {
		movingFiles.Push(file)
		filePathMap[file] = srcDir
	}

	for !movingFiles.IsEmpty() {

		movingFile := movingFiles.Pop()
		movingFilePath := filePathMap[movingFile]
		movingFileName := fmt.Sprintf("%s/%s", movingFilePath, movingFile.GetName())
		if movingFile.IsDir() {
			// directory, recursive move
			subFilePath := movingFileName
			subFiles, err := fs.List(c.Request.Context(), movingFileName, &fs.ListArgs{Refresh: true})
			if err != nil {
				common.ErrorResp(c, err, 500)
				return
			}
			for _, subFile := range subFiles {
				movingFiles.Push(subFile)
				filePathMap[subFile] = subFilePath
			}
		} else {
			if movingFilePath == dstDir {
				// same directory, don't move
				continue
			}

			if req.ConflictPolicy == OVERWRITE {
				if hasDirObj(dstByName[movingFile.GetName()]) {
					common.ErrorStrResp(c, fmt.Sprintf("cannot overwrite folder [%s] with file", movingFile.GetName()), 403)
					return
				}
			} else if slices.Contains(existingFileNames, movingFile.GetName()) {
				if req.ConflictPolicy == CANCEL {
					common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", movingFile.GetName()), 403)
					return
				} else if req.ConflictPolicy == SKIP {
					continue
				}
			} else {
				existingFileNames = append(existingFileNames, movingFile.GetName())
			}
			movingFileNames = append(movingFileNames, movingFileName)

		}

	}

	var count = 0
	addedTasks := make([]task.TaskExtensionInfo, 0, len(movingFileNames))
	for i, fileName := range movingFileNames {
		// move
		t, err := fs.MoveWithOptions(c.Request.Context(), fileName, dstDir, fs.TransferOptions{
			SkipHook:  len(movingFileNames) > i+1,
			Overwrite: req.ConflictPolicy == OVERWRITE,
		})
		if t != nil {
			addedTasks = append(addedTasks, t)
		}
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		count++
	}

	common.SuccessWithMsgResp(c, fmt.Sprintf("Successfully moved %d %s", count, common.Pluralize(count, "file", "files")), gin.H{
		"tasks": getTaskInfos(addedTasks),
	})
}
