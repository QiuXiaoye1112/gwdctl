package handles

import (
	"fmt"
	stdpath "path"
	"strings"

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

type MkdirReq struct {
	Path string `json:"path" form:"path"`
}

func FsMkdir(c *gin.Context) {
	var req MkdirReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	reqPath, err := user.JoinPath(req.Path)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	parentPath := stdpath.Dir(reqPath)
	parentMeta, err := op.GetNearestMeta(parentPath)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !user.CanWriteContent() && !common.CanWriteContentBypassUserPerms(parentMeta, parentPath) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	if !common.CanWrite(user, parentMeta, parentPath) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	if err := fs.MakeDir(c.Request.Context(), reqPath); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c)
}

type MoveCopyReq struct {
	SrcDir       string   `json:"src_dir"`
	DstDir       string   `json:"dst_dir"`
	Names        []string `json:"names"`
	Overwrite    bool     `json:"overwrite"`
	SkipExisting bool     `json:"skip_existing"`
	Merge        bool     `json:"merge"`
	FollowSeed   bool     `json:"follow_seed"`
}

// FsMove performs batch move (individual item permission checks skipped for performance).
func FsMove(c *gin.Context) {
	handleMoveCopy(c, false)
}

// FsCopy performs batch copy (individual item permission checks skipped for performance).
func FsCopy(c *gin.Context) {
	handleMoveCopy(c, true)
}

func handleMoveCopy(c *gin.Context, isCopy bool) {
	var req MoveCopyReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if len(req.Names) == 0 {
		common.ErrorStrResp(c, "Empty file names", 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if isCopy && !user.CanCopy() || !isCopy && !user.CanMove() {
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
	if (isCopy && !common.CanRead(user, srcMeta, srcDir)) || (!isCopy && !common.CanWrite(user, srcMeta, srcDir)) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
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

	if stdpath.Clean(srcDir) == stdpath.Clean(dstDir) && req.Overwrite {
		common.ErrorStrResp(c, "cannot move or copy to the same directory", 400)
		return
	}

	dstObjs, err := fs.List(c.Request.Context(), dstDir, &fs.ListArgs{Refresh: true, NoLog: true})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	dstByName := indexObjsByName(dstObjs)

	if !strings.HasSuffix(srcDir, "/") {
		srcDir += "/"
	}
	for i, name := range req.Names {
		if err := checkRelativePath(name); err != nil {
			common.ErrorResp(c, err, 403)
			return
		}
		// ensure req.Names is not a relative path
		srcPath := stdpath.Join(srcDir, name)
		if !strings.HasPrefix(srcPath+"/", srcDir) {
			req.Names[i] = ""
			continue
		}
		req.Names[i] = srcPath
		base := stdpath.Base(srcPath)
		if base == "." || base == "/" {
			common.ErrorStrResp(c, fmt.Sprintf("invalid file name [%s]", name), 400)
			return
		}

		dstMatches := dstByName[base]
		if len(dstMatches) > 0 {
			if !req.Overwrite {
				if isCopy {
					if !req.SkipExisting && !req.Merge {
						common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", name), 403)
						return
					} else if !req.Merge || !hasDirObj(dstMatches) {
						req.Names[i] = ""
						continue
					}
				} else {
					if !req.SkipExisting {
						common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", name), 403)
						return
					}
					req.Names[i] = ""
					continue
				}
			} else {
				srcObj, err := fs.Get(c.Request.Context(), srcPath, &fs.GetArgs{NoLog: true})
				if err != nil && !errs.IsObjectNotFound(err) {
					common.ErrorResp(c, err, 500)
					return
				}
				if srcObj != nil {
					if _, conflictErr := op.ValidateTransferConflicts(srcObj.IsDir(), srcObj.GetID(), dstMatches, op.TransferOptions{Overwrite: true}); conflictErr != nil {
						common.ErrorResp(c, conflictErr, 403)
						return
					}
				}
			}
		}

		if req.FollowSeed {
			if err := checkFollowSeedConflicts(c, srcPath, dstByName, req.Overwrite, req.SkipExisting); err != nil {
				common.ErrorStrResp(c, err.Error(), 403)
				return
			}
		}
	}

	// Create tasks with overwrite policy propagated into background execution.
	var addedTasks []task.TaskExtensionInfo
	opLabel := "move"
	if isCopy {
		opLabel = "copy"
	}
	for i, p := range req.Names {
		if p == "" {
			continue
		}
		opts := fs.TransferOptions{
			SkipHook:  len(req.Names) > i+1,
			Overwrite: req.Overwrite,
		}
		var t task.TaskExtensionInfo
		if isCopy {
			if req.Merge {
				t, err = fs.MergeWithOptions(c.Request.Context(), p, dstDir, opts)
			} else {
				t, err = fs.CopyWithOptions(c.Request.Context(), p, dstDir, opts)
			}
		} else {
			t, err = fs.MoveWithOptions(c.Request.Context(), p, dstDir, opts)
		}
		if t != nil {
			addedTasks = append(addedTasks, t)
		}
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		if req.FollowSeed {
			seedTasks, followErr := followSeedTransfer(c, opLabel, p, dstDir, dstByName, req.Overwrite, req.SkipExisting)
			if followErr != nil {
				common.ErrorResp(c, followErr, 500)
				return
			}
			addedTasks = append(addedTasks, seedTasks...)
		}
	}

	// Return immediately with task information
	if len(addedTasks) > 0 {
		msgVerb := "move"
		if isCopy {
			msgVerb = "copy"
		}
		common.SuccessResp(c, gin.H{
			"message": fmt.Sprintf("Successfully created %d %s task(s)", len(addedTasks), msgVerb),
			"tasks":   getTaskInfos(addedTasks),
		})
	} else {
		msgVerb := "Move"
		if isCopy {
			msgVerb = "Copy"
		}
		common.SuccessResp(c, gin.H{
			"message": fmt.Sprintf("%s operations completed immediately", msgVerb),
		})
	}
}

func indexObjsByName(objs []model.Obj) map[string][]model.Obj {
	byName := make(map[string][]model.Obj, len(objs))
	for _, obj := range objs {
		byName[obj.GetName()] = append(byName[obj.GetName()], obj)
	}
	return byName
}

func hasDirObj(objs []model.Obj) bool {
	for _, obj := range objs {
		if obj.IsDir() {
			return true
		}
	}
	return false
}

type RenameReq struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Overwrite  bool   `json:"overwrite"`
	FollowSeed bool   `json:"follow_seed"`
}

func FsRename(c *gin.Context) {
	var req RenameReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanRename() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	reqPath, err := user.JoinPath(req.Path)
	if err == nil {
		err = checkRelativePath(req.Name)
	}
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	parentPath := stdpath.Dir(reqPath)
	parentMeta, err := op.GetNearestMeta(parentPath)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !common.CanWrite(user, parentMeta, parentPath) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	if err := fs.RenameWithOptions(c.Request.Context(), reqPath, req.Name, fs.TransferOptions{Overwrite: req.Overwrite}); err != nil {
		switch {
		case errs.IsObjectAlreadyExists(err):
			common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", req.Name), 409)
		case errs.IsObjectNotFound(err):
			common.ErrorResp(c, err, 404)
		case strings.Contains(err.Error(), "cannot overwrite folder with file"),
			strings.Contains(err.Error(), "cannot overwrite file with folder"):
			common.ErrorStrResp(c, err.Error(), 403)
		default:
			common.ErrorResp(c, err, 500)
		}
		return
	}
	if req.FollowSeed {
		if err := followSeedRename(c, reqPath, req.Name); err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
	}
	common.SuccessResp(c)
}

func checkRelativePath(path string) error {
	if strings.ContainsAny(path, "/\\") || path == "" || path == "." || path == ".." {
		return errs.RelativePath
	}
	return nil
}

type RemoveReq struct {
	Dir        string   `json:"dir"`
	Names      []string `json:"names"`
	FollowSeed bool     `json:"follow_seed"`
}

// FsRemove performs batch remove (individual item permission checks skipped for performance).
func FsRemove(c *gin.Context) {
	var req RemoveReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if len(req.Names) == 0 {
		common.ErrorStrResp(c, "Empty file names", 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanRemove() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	reqPath, err := user.JoinPath(req.Dir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	meta, err := op.GetNearestMeta(reqPath)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !common.CanWrite(user, meta, reqPath) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	if !strings.HasSuffix(reqPath, "/") {
		reqPath += "/"
	}
	for i, name := range req.Names {
		if err := checkRelativePath(name); err != nil {
			common.ErrorResp(c, err, 403)
			return
		}
		fullPath := stdpath.Join(reqPath, name)
		if !strings.HasPrefix(fullPath+"/", reqPath) {
			req.Names[i] = ""
			continue
		}
		req.Names[i] = fullPath
	}
	for _, path := range req.Names {
		if path == "" {
			continue
		}
		source, _ := fs.Get(c.Request.Context(), path, &fs.GetArgs{NoLog: true})
		err := fs.Remove(c.Request.Context(), path)
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		if req.FollowSeed && source != nil && !source.IsDir() {
			if err = followSeedRemove(c, path); err != nil {
				common.ErrorResp(c, err, 500)
				return
			}
		}
	}
	//fs.ClearCache(req.Dir)
	common.SuccessResp(c)
}

func seedSidecarPaths(filePath string) []string {
	return []string{filePath + ".oss", filePath + ".torrent", filePath + ".cas", filePath + ".cas.torrent"}
}

func checkFollowSeedConflicts(c *gin.Context, srcPath string, dstByName map[string][]model.Obj, overwrite, skipExisting bool) error {
	source, err := fs.Get(c.Request.Context(), srcPath, &fs.GetArgs{NoLog: true})
	if err != nil || source == nil || source.IsDir() {
		return nil
	}
	for _, sidecarPath := range seedSidecarPaths(srcPath) {
		obj, err := fs.Get(c.Request.Context(), sidecarPath, &fs.GetArgs{NoLog: true})
		if err != nil || obj == nil || obj.IsDir() {
			continue
		}
		sidecarName := stdpath.Base(sidecarPath)
		dstMatches := dstByName[sidecarName]
		if len(dstMatches) == 0 {
			continue
		}
		if !overwrite {
			if !skipExisting {
				return fmt.Errorf("file [%s] exists", sidecarName)
			}
			continue
		}
		if _, err := op.ValidateTransferConflicts(obj.IsDir(), obj.GetID(), dstMatches, op.TransferOptions{Overwrite: true}); err != nil {
			return err
		}
	}
	return nil
}

func followSeedTransfer(c *gin.Context, operation, srcPath, dstDir string, dstByName map[string][]model.Obj, overwrite, skipExisting bool) ([]task.TaskExtensionInfo, error) {
	source, err := fs.Get(c.Request.Context(), srcPath, &fs.GetArgs{NoLog: true})
	if err != nil || source == nil || source.IsDir() {
		return nil, nil
	}
	var tasks []task.TaskExtensionInfo
	opts := fs.TransferOptions{SkipHook: true, Overwrite: overwrite}
	for _, sidecarPath := range seedSidecarPaths(srcPath) {
		obj, err := fs.Get(c.Request.Context(), sidecarPath, &fs.GetArgs{NoLog: true})
		if err != nil || obj == nil || obj.IsDir() {
			continue
		}
		sidecarName := stdpath.Base(sidecarPath)
		if !overwrite && skipExisting && len(dstByName[sidecarName]) > 0 {
			continue
		}
		var current task.TaskExtensionInfo
		switch operation {
		case "copy":
			current, err = fs.CopyWithOptions(c.Request.Context(), sidecarPath, dstDir, opts)
		case "move":
			current, err = fs.MoveWithOptions(c.Request.Context(), sidecarPath, dstDir, opts)
		default:
			return nil, fmt.Errorf("unsupported seed sidecar operation %q", operation)
		}
		if err != nil {
			return tasks, fmt.Errorf("%s seed sidecar %s: %w", operation, sidecarPath, err)
		}
		if current != nil {
			tasks = append(tasks, current)
		}
	}
	return tasks, nil
}

func followSeedRename(c *gin.Context, srcPath, newName string) error {
	source, err := fs.Get(c.Request.Context(), srcPath, &fs.GetArgs{NoLog: true})
	if err != nil || source == nil || source.IsDir() {
		return nil
	}
	for _, sidecarPath := range seedSidecarPaths(srcPath) {
		obj, err := fs.Get(c.Request.Context(), sidecarPath, &fs.GetArgs{NoLog: true})
		if err != nil || obj == nil || obj.IsDir() {
			continue
		}
		suffix := strings.TrimPrefix(sidecarPath, srcPath)
		if err = fs.Rename(c.Request.Context(), sidecarPath, newName+suffix, true); err != nil {
			return fmt.Errorf("rename seed sidecar %s: %w", sidecarPath, err)
		}
	}
	return nil
}

func followSeedRemove(c *gin.Context, srcPath string) error {
	for _, sidecarPath := range seedSidecarPaths(srcPath) {
		obj, err := fs.Get(c.Request.Context(), sidecarPath, &fs.GetArgs{NoLog: true})
		if err != nil || obj == nil || obj.IsDir() {
			continue
		}
		if err = fs.Remove(c.Request.Context(), sidecarPath); err != nil {
			return fmt.Errorf("remove seed sidecar %s: %w", sidecarPath, err)
		}
	}
	return nil
}

type RemoveEmptyDirectoryReq struct {
	SrcDir string `json:"src_dir"`
}

// FsRemoveEmptyDirectory recursively removes empty directories (individual item permission checks skipped for performance).
func FsRemoveEmptyDirectory(c *gin.Context) {
	var req RemoveEmptyDirectoryReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}

	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !user.CanRemove() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	srcDir, err := user.JoinPath(req.SrcDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}

	meta, err := op.GetNearestMeta(srcDir)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !common.CanWrite(user, meta, srcDir) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	common.GinAppendValues(c, conf.MetaKey, meta)

	rootFiles, err := fs.List(c.Request.Context(), srcDir, &fs.ListArgs{})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}

	// record the file path
	filePathMap := make(map[model.Obj]string)
	// record the parent file
	fileParentMap := make(map[model.Obj]model.Obj)
	// removing files
	removingFiles := generic.NewQueue[model.Obj]()
	// removed files
	removedFiles := make(map[string]bool)
	for _, file := range rootFiles {
		if !file.IsDir() {
			continue
		}
		removingFiles.Push(file)
		filePathMap[file] = srcDir
	}

	for !removingFiles.IsEmpty() {

		removingFile := removingFiles.Pop()
		removingFilePath := fmt.Sprintf("%s/%s", filePathMap[removingFile], removingFile.GetName())

		if removedFiles[removingFilePath] {
			continue
		}

		subFiles, err := fs.List(c.Request.Context(), removingFilePath, &fs.ListArgs{Refresh: true})
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}

		if len(subFiles) == 0 {
			// remove empty directory
			err = fs.Remove(c.Request.Context(), removingFilePath)
			removedFiles[removingFilePath] = true
			if err != nil {
				common.ErrorResp(c, err, 500)
				return
			}
			// recheck parent folder
			parentFile, exist := fileParentMap[removingFile]
			if exist {
				removingFiles.Push(parentFile)
			}

		} else {
			// recursive remove
			for _, subFile := range subFiles {
				if !subFile.IsDir() {
					continue
				}
				removingFiles.Push(subFile)
				filePathMap[subFile] = removingFilePath
				fileParentMap[subFile] = removingFile
			}
		}

	}

	common.SuccessResp(c)
}
