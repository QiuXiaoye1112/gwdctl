package op

import (
	"context"
	"net/url"
	stdpath "path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/singleflight"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

var listG singleflight.Group[[]model.Obj]

// List files in storage, not contains virtual file
func List(ctx context.Context, storage driver.Driver, path string, args model.ListArgs) ([]model.Obj, error) {
	return list(ctx, storage, path, args, nil)
}

func list(ctx context.Context, storage driver.Driver, path string, args model.ListArgs, resultValidator func([]model.Obj) error) ([]model.Obj, error) {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return nil, errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	path = utils.FixAndCleanPath(path)
	log.Debugf("op.List %s", path)
	key := Key(storage, path)
	if !args.Refresh && storage.GetStorage().CacheExpiration > 0 {
		if dirCache, exists := Cache.dirCache.Get(key); exists {
			log.Debugf("use cache when list %s", path)
			objs := dirCache.GetSortedObjects(storage)
			if resultValidator != nil {
				if err := resultValidator(objs); err == nil {
					return objs, nil
				}
			} else {
				return objs, nil
			}
		}
	}

	flightKey := key
	if base, ok := ctx.Value(conf.ThumbnailBaseURLKey).(*url.URL); ok && storage.Config().NoCache {
		flightKey += ":" + strconv.Quote(base.String())
	}
	objs, err, _ := listG.Do(flightKey, func() ([]model.Obj, error) {
		dir, err := GetUnwrap(ctx, storage, path)
		if err != nil {
			return nil, errors.WithMessage(err, "failed get dir")
		}
		log.Debugf("list dir: %+v", dir)
		if !dir.IsDir() {
			return nil, errors.WithStack(errs.NotFolder)
		}
		files, err := storage.List(ctx, dir, args)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to list objs")
		}
		// warp obj name
		wrapObjsName(storage, files)
		// sort objs
		if storage.Config().LocalSort {
			model.SortFiles(files, storage.GetStorage().OrderBy, storage.GetStorage().OrderDirection)
		}
		model.ExtractFolder(files, storage.GetStorage().ExtractFolder)

		if !args.SkipHook {
			// call hooks
			go func(reqPath string, files []model.Obj) {
				HandleObjsUpdateHook(context.WithoutCancel(ctx), reqPath, files)
			}(utils.GetFullPath(storage.GetStorage().MountPath, path), files)
		}

		if !storage.Config().NoCache {
			if len(files) > 0 {
				log.Debugf("set cache: %s => %+v", key, files)

				ttl := storage.GetStorage().CacheExpiration

				customCachePolicies := storage.GetStorage().CustomCachePolicies
				if len(customCachePolicies) > 0 {
					for configPolicy := range strings.SplitSeq(customCachePolicies, "\n") {
						pattern, ttlstr, ok := strings.Cut(strings.TrimSpace(configPolicy), ":")
						if !ok {
							log.Warnf("Malformed custom cache policy entry: %s in storage %s for path %s. Expected format: pattern:ttl", configPolicy, storage.GetStorage().MountPath, path)
							continue
						}
						if match, err1 := doublestar.Match(pattern, path); err1 != nil {
							log.Warnf("Invalid glob pattern in custom cache policy: %s, error: %v", pattern, err1)
							continue
						} else if !match {
							continue
						}

						if configTtl, err1 := strconv.ParseInt(ttlstr, 10, 64); err1 == nil {
							ttl = int(configTtl)
							break
						}
					}
				}

				duration := time.Minute * time.Duration(ttl)
				if duration <= 0 {
					duration = 3 * time.Second
				}
				Cache.dirCache.SetWithTTL(key, newDirectoryCache(files), duration)
			} else {
				log.Debugf("del cache: %s", key)
				Cache.deleteDirectoryTree(key)
			}
		}
		return files, nil
	})
	if err != nil {
		return nil, err
	}
	if resultValidator != nil {
		if err := resultValidator(objs); err != nil {
			return nil, err
		}
	}
	return objs, nil
}

// Get object from list of files
func Get(ctx context.Context, storage driver.Driver, path string, excludeTempObj ...bool) (model.Obj, error) {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return nil, errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	path = utils.FixAndCleanPath(path)
	log.Debugf("op.Get %s", path)

	// is root folder
	if path == "/" {
		if getRooter, ok := storage.(driver.GetRooter); ok {
			rootObj, err := getRooter.GetRoot(ctx)
			if err != nil {
				return nil, errors.WithMessage(err, "failed get root obj")
			}
			return rootObj, nil
		}
		switch r := storage.(type) {
		case driver.IRootId:
			return &model.Object{
				ID:       r.GetRootId(),
				Name:     RootName,
				Modified: storage.GetStorage().Modified,
				IsFolder: true,
				Mask:     model.Locked,
			}, nil
		case driver.IRootPath:
			return &model.Object{
				Path:     r.GetRootPath(),
				Name:     RootName,
				Modified: storage.GetStorage().Modified,
				Mask:     model.Locked,
				IsFolder: true,
			}, nil
		}
		return nil, errors.New("please implement GetRooter or IRootPath or IRootId interface")
	}

	// try get from cache first
	dir, name := stdpath.Split(path)
	dirCache, dirCacheExists := Cache.dirCache.Get(Key(storage, dir))
	refreshList := false
	excludeTemp := utils.IsBool(excludeTempObj...)
	if dirCacheExists {
		files := dirCache.GetSortedObjects(storage)
		for _, f := range files {
			if f.GetName() == name {
				if excludeTemp && model.ObjHasMask(f, model.Temp) {
					refreshList = true
					break
				}
				return f, nil
			}
		}
	}

	// get the obj directly without list so that we can reduce the io
	if g, ok := storage.(driver.Getter); ok {
		obj, err := g.Get(ctx, path)
		if err == nil {
			return obj, nil
		}
		if !errs.IsNotImplementError(err) && !errs.IsNotSupportError(err) {
			return nil, errors.WithMessage(err, "failed to get obj")
		}
	}

	if !dirCacheExists || refreshList {
		var obj model.Obj
		list(ctx, storage, dir, model.ListArgs{Refresh: refreshList}, func(objs []model.Obj) error {
			for _, f := range objs {
				if f.GetName() == name {
					if excludeTemp && model.ObjHasMask(f, model.Temp) {
						return errs.ObjectNotFound
					}
					obj = f
					return nil
				}
			}
			return nil
		})
		if obj != nil {
			return obj, nil
		}
	}
	log.Debugf("cant find obj with name: %s", name)
	return nil, errors.WithStack(errs.ObjectNotFound)
}

func GetUnwrap(ctx context.Context, storage driver.Driver, path string) (model.Obj, error) {
	obj, err := Get(ctx, storage, path, true)
	if err != nil {
		return nil, err
	}
	return model.UnwrapObjName(obj), err
}

var linkG = singleflight.Group[*objWithLink]{}

// Link get link, if is an url. should have an expiry time
func Link(ctx context.Context, storage driver.Driver, path string, args model.LinkArgs) (*model.Link, model.Obj, error) {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return nil, nil, errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}

	mode := storage.Config().LinkCacheMode
	if mode == -1 {
		mode = storage.(driver.LinkCacheModeResolver).ResolveLinkCacheMode(path)
	}
	typeKey := "proxy/" + args.Type
	if args.Redirect {
		typeKey = "redirect/" + args.Type
	}
	if mode&driver.LinkCacheIP != 0 {
		typeKey += "/" + args.IP
	}
	if mode&driver.LinkCacheUA != 0 {
		typeKey += "/" + args.Header.Get("User-Agent")
	}
	key := Key(storage, path)
	if ol, exists := Cache.linkCache.GetType(key, typeKey); exists {
		if ol.acquire() {
			return ol.link, ol.obj, nil
		}
	}

	fn := func() (*objWithLink, error) {
		file, err := GetUnwrap(ctx, storage, path)
		if err != nil {
			return nil, errors.WithMessage(err, "failed to get file")
		}
		if file.IsDir() {
			return nil, errors.WithStack(errs.NotFile)
		}

		link, err := storage.Link(ctx, file, args)
		if err != nil {
			return nil, errors.Wrapf(err, "failed get link")
		}
		ol, err := admitLink(link, file)
		if err != nil {
			return nil, err
		}
		if ol.policy.expiration != nil {
			Cache.linkCache.SetTypeWithTTL(key, typeKey, ol, *ol.policy.expiration)
		} else {
			Cache.linkCache.SetTypeWithExpirable(key, typeKey, ol, &link.SyncClosers)
		}
		return ol, nil
	}
	for {
		ol, err, _ := linkG.Do(key+"/"+typeKey, fn)
		if err != nil {
			return nil, nil, err
		}
		if ol.acquire() {
			return ol.link, ol.obj, nil
		}
	}
}

// Other api
func Other(ctx context.Context, storage driver.Driver, args model.FsOtherArgs) (any, error) {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return nil, errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	o, ok := storage.(driver.Other)
	if !ok {
		return nil, errs.NotImplement
	}
	obj, err := GetUnwrap(ctx, storage, args.Path)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get obj")
	}
	return o.Other(ctx, model.OtherArgs{
		Obj:    obj,
		Method: args.Method,
		Data:   args.Data,
	})
}

// TransferOptions controls conflict resolution during same-storage or fallback transfers.
type TransferOptions struct {
	Overwrite bool
}

type keyedLockEntry struct {
	mu   sync.Mutex
	refs int
}

type keyedLockMap struct {
	mu      sync.Mutex
	entries map[string]*keyedLockEntry
}

func newKeyedLockMap() *keyedLockMap {
	return &keyedLockMap{entries: make(map[string]*keyedLockEntry)}
}

// Lock serializes concurrent operations targeting the same key without holding nested locks.
func (m *keyedLockMap) Lock(key string) func() {
	m.mu.Lock()
	entry, ok := m.entries[key]
	if !ok {
		entry = &keyedLockEntry{}
		m.entries[key] = entry
	}
	entry.refs++
	m.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		m.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(m.entries, key)
		}
		m.mu.Unlock()
	}
}

var transferLocks = newKeyedLockMap()

// ListExistingByName queries dirPath directly (bypassing directory cache) for objects matching name.
func ListExistingByName(ctx context.Context, storage driver.Driver, dirPath, name string) ([]model.Obj, error) {
	objs, err := list(ctx, storage, dirPath, model.ListArgs{Refresh: true, SkipHook: true}, nil)
	if err != nil {
		return nil, err
	}
	var matches []model.Obj
	for _, obj := range objs {
		if obj.GetName() == name {
			matches = append(matches, obj)
		}
	}
	return matches, nil
}

// ValidateTransferConflicts filters self-references and checks overwrite and type compatibility rules.
func ValidateTransferConflicts(srcIsDir bool, srcID string, conflicts []model.Obj, opts TransferOptions) ([]model.Obj, error) {
	var realConflicts []model.Obj
	for _, c := range conflicts {
		if srcID != "" && c.GetID() == srcID {
			continue
		}
		realConflicts = append(realConflicts, c)
	}
	if len(realConflicts) == 0 {
		return nil, nil
	}
	if !opts.Overwrite {
		return realConflicts, errors.WithStack(errs.ObjectAlreadyExists)
	}
	for _, c := range realConflicts {
		if c.IsDir() != srcIsDir {
			if c.IsDir() {
				return nil, errors.New("cannot overwrite folder with file")
			}
			return nil, errors.New("cannot overwrite file with folder")
		}
	}
	return realConflicts, nil
}

func getFreshOnMiss(ctx context.Context, storage driver.Driver, path string, excludeTemp bool) (model.Obj, error) {
	path = utils.FixAndCleanPath(path)
	if path == "/" {
		return Get(ctx, storage, path, excludeTemp)
	}
	parentPath, name := stdpath.Split(path)
	_, parentCached := Cache.dirCache.Get(Key(storage, parentPath))
	obj, err := Get(ctx, storage, path, excludeTemp)
	if err == nil || !errs.IsObjectNotFound(err) || !parentCached {
		return obj, err
	}
	matches, listErr := ListExistingByName(ctx, storage, parentPath, name)
	if listErr != nil {
		return nil, listErr
	}
	if len(matches) == 0 {
		return nil, errors.WithStack(errs.ObjectNotFound)
	}
	if excludeTemp && model.ObjHasMask(matches[0], model.Temp) {
		return nil, errors.WithStack(errs.ObjectNotFound)
	}
	return matches[0], nil
}

func getUnwrapFreshOnMiss(ctx context.Context, storage driver.Driver, path string) (model.Obj, error) {
	obj, err := getFreshOnMiss(ctx, storage, path, true)
	if err != nil {
		return nil, err
	}
	return model.UnwrapObjName(obj), nil
}

func removeConflictsByID(ctx context.Context, storage driver.Driver, dstDirPath string, conflicts []model.Obj, excludeIDs ...string) error {
	if len(conflicts) == 0 {
		return nil
	}
	remover, ok := storage.(driver.Remove)
	if !ok {
		return errs.NotImplement
	}
	skipIDs := make(map[string]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		if id != "" {
			skipIDs[id] = struct{}{}
		}
	}
	dstKey := Key(storage, dstDirPath)
	var firstErr error
	for _, conflict := range conflicts {
		id := conflict.GetID()
		if id != "" {
			if _, skip := skipIDs[id]; skip {
				continue
			}
		}
		if err := remover.Remove(ctx, model.UnwrapObjName(conflict)); err != nil {
			if !errs.IsObjectNotFound(err) && firstErr == nil {
				firstErr = errors.WithStack(err)
			}
			if !errs.IsObjectNotFound(err) {
				continue
			}
		}
		if !conflict.IsDir() {
			Cache.linkCache.DeleteKey(stdpath.Join(dstKey, conflict.GetName()))
		} else {
			Cache.deleteDirectoryTree(stdpath.Join(dstKey, conflict.GetName()))
		}
	}
	if firstErr != nil {
		Cache.DeleteDirectory(storage, dstDirPath)
	}
	return firstErr
}

func objIDOrEmpty(obj model.Obj) string {
	if obj == nil {
		return ""
	}
	return obj.GetID()
}

var mkdirG singleflight.Group[any]

func MakeDir(ctx context.Context, storage driver.Driver, path string) error {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	path = utils.FixAndCleanPath(path)
	key := Key(storage, path)
	_, err, _ := mkdirG.Do(key, func() (any, error) {
		parentPath, dirName := stdpath.Split(path)
		_, parentCachedBefore := Cache.dirCache.Get(Key(storage, parentPath))
		// check if dir exists
		f, err := Get(ctx, storage, path)
		if err == nil {
			if f.IsDir() {
				return nil, nil
			}
			return nil, errors.New("file exists")
		}
		if !errs.IsObjectNotFound(err) {
			return nil, errors.WithMessage(err, "failed to check if dir exists")
		}
		// Refresh once when parent was served from the short dirCache so recent external folders are not duplicated.
		if parentCachedBefore && path != "/" {
			existing, listErr := ListExistingByName(ctx, storage, parentPath, dirName)
			if listErr != nil {
				if !errs.IsObjectNotFound(listErr) {
					return nil, errors.WithMessage(listErr, "failed to check if dir exists")
				}
			} else if len(existing) > 0 {
				for _, e := range existing {
					if e.IsDir() {
						return nil, nil
					}
				}
				return nil, errors.New("file exists")
			}
		}
		if err = MakeDir(ctx, storage, parentPath); err != nil {
			return nil, errors.WithMessagef(err, "failed to make parent dir [%s]", parentPath)
		}
		parentDir, err := getUnwrapFreshOnMiss(ctx, storage, parentPath)
		// this should not happen
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to get parent dir [%s]", parentPath)
		}
		if !parentDir.IsDir() {
			return nil, errs.NotFolder
		}
		if model.ObjHasMask(parentDir, model.NoWrite) {
			return nil, errors.WithStack(errs.PermissionDenied)
		}

		var newObj model.Obj
		switch s := storage.(type) {
		case driver.MkdirResult:
			newObj, err = s.MakeDir(ctx, parentDir, dirName)
		case driver.Mkdir:
			err = s.MakeDir(ctx, parentDir, dirName)
		default:
			return nil, errs.NotImplement
		}
		if err != nil && !errs.IsObjectAlreadyExists(err) {
			return nil, errors.WithStack(err)
		}
		if storage.Config().NoCache {
			return nil, nil
		}
		if dirCache, exist := Cache.dirCache.Get(Key(storage, parentPath)); exist {
			if newObj == nil {
				t := time.Now()
				newObj = &model.Object{
					Name:     dirName,
					IsFolder: true,
					Modified: t,
					Ctime:    t,
					Mask:     model.Temp,
				}
			}
			dirCache.UpdateObject("", wrapObjName(storage, newObj))
		}
		return nil, nil
	})
	return err
}

func Move(ctx context.Context, storage driver.Driver, srcPath, dstDirPath string) error {
	return MoveWithOptions(ctx, storage, srcPath, dstDirPath, TransferOptions{})
}

// MoveWithOptions moves srcPath into dstDirPath while enforcing same-name conflict rules.
func MoveWithOptions(ctx context.Context, storage driver.Driver, srcPath, dstDirPath string, opts TransferOptions) error {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	srcPath = utils.FixAndCleanPath(srcPath)
	if utils.PathEqual(srcPath, "/") {
		return errors.New("move root folder is not allowed")
	}
	srcDirPath := stdpath.Dir(srcPath)
	dstDirPath = utils.FixAndCleanPath(dstDirPath)
	if dstDirPath == srcDirPath {
		return errors.New("move in place")
	}
	if utils.IsSubPath(srcPath, dstDirPath) {
		return errors.New("cannot move directory into itself")
	}
	switch storage.(type) {
	case driver.MoveResult, driver.Move:
	default:
		return errs.NotImplement
	}

	srcRawObj, err := getFreshOnMiss(ctx, storage, srcPath, true)
	if err != nil {
		return errors.WithMessage(err, "failed to get src object")
	}
	if model.ObjHasMask(srcRawObj, model.NoMove) {
		return errors.WithStack(errs.PermissionDenied)
	}
	srcObj := model.UnwrapObjName(srcRawObj)
	dstDir, err := getUnwrapFreshOnMiss(ctx, storage, dstDirPath)
	if err != nil {
		return errors.WithMessage(err, "failed to get dst dir")
	}
	if !dstDir.IsDir() {
		return errs.NotFolder
	}
	if model.ObjHasMask(dstDir, model.NoWrite) {
		return errors.WithStack(errs.PermissionDenied)
	}

	dstPath := stdpath.Join(dstDirPath, srcRawObj.GetName())
	unlock := transferLocks.Lock(Key(storage, dstPath))

	rawConflicts, err := ListExistingByName(ctx, storage, dstDirPath, srcRawObj.GetName())
	if err != nil {
		unlock()
		return errors.WithMessage(err, "failed to check dst conflicts")
	}
	conflicts, err := ValidateTransferConflicts(srcObj.IsDir(), srcObj.GetID(), rawConflicts, opts)
	if err != nil {
		unlock()
		return err
	}
	if len(conflicts) > 0 {
		if _, ok := storage.(driver.Remove); !ok {
			unlock()
			return errs.NotImplement
		}
		if srcObj.IsDir() {
			// Release outer directory lock before recursing so child transfers acquire leaf locks cleanly.
			unlock()
			return mergeMoveDir(ctx, storage, srcPath, dstPath, opts)
		}
	}
	defer unlock()

	var newObj model.Obj
	switch s := storage.(type) {
	case driver.MoveResult:
		newObj, err = s.Move(ctx, srcObj, dstDir)
	case driver.Move:
		err = s.Move(ctx, srcObj, dstDir)
	default:
		err = errs.NotImplement
	}
	if err != nil {
		return errors.WithStack(err)
	}

	srcKey := Key(storage, srcDirPath)
	dstKey := Key(storage, dstDirPath)
	if !srcRawObj.IsDir() {
		Cache.linkCache.DeleteKey(stdpath.Join(srcKey, srcRawObj.GetName()))
		Cache.linkCache.DeleteKey(stdpath.Join(dstKey, srcRawObj.GetName()))
	}
	if !storage.Config().NoCache {
		if cache, exist := Cache.dirCache.Get(srcKey); exist {
			if srcRawObj.IsDir() {
				Cache.deleteDirectoryTree(stdpath.Join(srcKey, srcRawObj.GetName()))
			}
			cache.RemoveObject(srcRawObj.GetName())
		}
	}
	if removeErr := removeConflictsByID(ctx, storage, dstDirPath, conflicts, srcObj.GetID(), objIDOrEmpty(newObj)); removeErr != nil {
		return removeErr
	}
	if !storage.Config().NoCache {
		if cache, exist := Cache.dirCache.Get(dstKey); exist {
			if newObj == nil {
				newObj = &model.ObjWrapMask{Obj: srcRawObj, Mask: model.Temp}
			} else {
				newObj = wrapObjName(storage, newObj)
			}
			cache.ReplaceObject(srcRawObj.GetName(), newObj)
		}
	}

	if ctx.Value(conf.SkipHookKey) != nil || !needHandleObjsUpdateHook() {
		return nil
	}
	if !srcObj.IsDir() {
		go objsUpdateHook(context.WithoutCancel(ctx), storage, dstDirPath, false)
	} else {
		go objsUpdateHook(context.WithoutCancel(ctx), storage, stdpath.Join(dstDirPath, srcObj.GetName()), true)
	}
	return nil
}

// mergeMoveDir moves contents of srcDirPath into existing dstDirPath when overwrite is enabled,
// preserving non-conflicting items in dstDirPath and removing srcDirPath only after it empties.
func mergeMoveDir(ctx context.Context, storage driver.Driver, srcDirPath, dstDirPath string, opts TransferOptions) error {
	srcObjs, err := list(ctx, storage, srcDirPath, model.ListArgs{Refresh: true, SkipHook: true}, nil)
	if err != nil {
		return errors.WithMessagef(err, "failed to list src dir [%s]", srcDirPath)
	}
	dstObjs, err := list(ctx, storage, dstDirPath, model.ListArgs{Refresh: true, SkipHook: true}, nil)
	if err != nil {
		return errors.WithMessagef(err, "failed to list dst dir [%s]", dstDirPath)
	}
	dstByName := make(map[string][]model.Obj, len(dstObjs))
	for _, obj := range dstObjs {
		dstByName[obj.GetName()] = append(dstByName[obj.GetName()], obj)
	}
	for _, srcChild := range srcObjs {
		matches := dstByName[srcChild.GetName()]
		if _, err := ValidateTransferConflicts(srcChild.IsDir(), srcChild.GetID(), matches, opts); err != nil {
			return err
		}
	}
	for _, srcChild := range srcObjs {
		childSrcPath := stdpath.Join(srcDirPath, srcChild.GetName())
		matches := dstByName[srcChild.GetName()]
		if len(matches) == 0 {
			if err := MoveWithOptions(ctx, storage, childSrcPath, dstDirPath, opts); err != nil {
				return err
			}
			continue
		}
		if srcChild.IsDir() {
			if err := mergeMoveDir(ctx, storage, childSrcPath, stdpath.Join(dstDirPath, srcChild.GetName()), opts); err != nil {
				return err
			}
			continue
		}
		if err := MoveWithOptions(ctx, storage, childSrcPath, dstDirPath, opts); err != nil {
			return err
		}
	}
	remaining, err := list(ctx, storage, srcDirPath, model.ListArgs{Refresh: true, SkipHook: true}, nil)
	if err != nil {
		return errors.WithMessagef(err, "failed to verify emptied src dir [%s]", srcDirPath)
	}
	if len(remaining) > 0 {
		return errors.Errorf("source directory [%s] is not empty after merge move", srcDirPath)
	}
	return Remove(ctx, storage, srcDirPath)
}

func selectOldConflictsForPrune(keepID string, conflicts, afterObjs []model.Obj) []model.Obj {
	if keepID != "" {
		for _, obj := range afterObjs {
			if obj.GetID() == keepID {
				return conflicts
			}
		}
	}
	preIDs := make(map[string]struct{}, len(conflicts))
	for _, c := range conflicts {
		if id := c.GetID(); id != "" {
			preIDs[id] = struct{}{}
		}
	}
	toDelete := make([]model.Obj, 0, len(afterObjs)-1)
	kept := false
	for i := len(afterObjs) - 1; i >= 0; i-- {
		obj := afterObjs[i]
		_, isOld := preIDs[obj.GetID()]
		if !kept && !isOld {
			kept = true
			continue
		}
		toDelete = append(toDelete, obj)
	}
	if !kept && len(toDelete) > 0 {
		toDelete = toDelete[1:]
	}
	return toDelete
}

func Rename(ctx context.Context, storage driver.Driver, srcPath, dstName string) error {
	return RenameWithOptions(ctx, storage, srcPath, dstName, TransferOptions{})
}

func RenameWithOptions(ctx context.Context, storage driver.Driver, srcPath, dstName string, opts TransferOptions) error {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	srcPath = utils.FixAndCleanPath(srcPath)
	if utils.PathEqual(srcPath, "/") {
		return errors.New("rename root folder is not allowed")
	}
	srcRawObj, err := getFreshOnMiss(ctx, storage, srcPath, true)
	if err != nil {
		return errors.WithMessage(err, "failed to get src object")
	}
	if model.ObjHasMask(srcRawObj, model.NoRename) {
		return errors.WithStack(errs.PermissionDenied)
	}
	oldName := srcRawObj.GetName()
	srcObj := model.UnwrapObjName(srcRawObj)
	dirPath := utils.FixAndCleanPath(stdpath.Dir(srcPath))
	dstPath := stdpath.Join(dirPath, dstName)

	unlock := transferLocks.Lock(Key(storage, dstPath))
	defer unlock()

	var conflicts []model.Obj
	if oldName != dstName {
		rawConflicts, listErr := ListExistingByName(ctx, storage, dirPath, dstName)
		if listErr != nil {
			return listErr
		}
		srcID := srcRawObj.GetID()
		for _, c := range rawConflicts {
			if srcID != "" && c.GetID() == srcID {
				continue
			}
			if c.IsDir() != srcRawObj.IsDir() {
				if c.IsDir() {
					return errors.New("cannot overwrite folder with file")
				}
				return errors.New("cannot overwrite file with folder")
			}
		}
		conflicts, err = ValidateTransferConflicts(srcRawObj.IsDir(), srcID, rawConflicts, opts)
		if err != nil {
			return err
		}
	}

	var newObj model.Obj
	switch s := storage.(type) {
	case driver.RenameResult:
		newObj, err = s.Rename(ctx, srcObj, dstName)
	case driver.Rename:
		err = s.Rename(ctx, srcObj, dstName)
	default:
		return errs.NotImplement
	}
	if err != nil {
		return errors.WithStack(err)
	}

	dirKey := Key(storage, dirPath)
	if !srcRawObj.IsDir() {
		Cache.linkCache.DeleteKey(stdpath.Join(dirKey, oldName))
		Cache.linkCache.DeleteKey(stdpath.Join(dirKey, dstName))
	}
	if !storage.Config().NoCache && srcRawObj.IsDir() {
		Cache.deleteDirectoryTree(stdpath.Join(dirKey, oldName))
		if len(conflicts) > 0 && oldName != dstName {
			Cache.deleteDirectoryTree(stdpath.Join(dirKey, dstName))
		}
	}

	if len(conflicts) > 0 {
		newID := objIDOrEmpty(newObj)
		if newID != "" {
			if removeErr := removeConflictsByID(ctx, storage, dirPath, conflicts, newID); removeErr != nil {
				if !storage.Config().NoCache {
					Cache.DeleteDirectory(storage, dirPath)
				}
				return removeErr
			}
		} else {
			// Driver did not return a post-rename ID; inspect live entries so
			// path-based in-place overwrites (<= 1 left) are preserved while
			// ID-keyed drivers like Google Drive (2+ left) remove old conflicts.
			afterObjs, afterErr := ListExistingByName(ctx, storage, dirPath, dstName)
			if afterErr != nil {
				if !storage.Config().NoCache {
					Cache.DeleteDirectory(storage, dirPath)
				}
				return afterErr
			}
			if len(afterObjs) > 1 {
				toDelete := selectOldConflictsForPrune(srcObj.GetID(), conflicts, afterObjs)
				if removeErr := removeConflictsByID(ctx, storage, dirPath, toDelete, srcObj.GetID()); removeErr != nil {
					if !storage.Config().NoCache {
						Cache.DeleteDirectory(storage, dirPath)
					}
					return removeErr
				}
			}
		}
	}

	if !storage.Config().NoCache {
		if cache, exist := Cache.dirCache.Get(dirKey); exist {
			if newObj == nil {
				newObj = &model.ObjWrapMask{Obj: &model.ObjWrapName{Name: dstName, Obj: srcObj}, Mask: model.Temp}
			}
			newObj = wrapObjName(storage, newObj)
			if oldName != dstName {
				cache.RemoveObjectsByName(oldName)
				cache.ReplaceObject(dstName, newObj)
			} else {
				cache.UpdateObject(oldName, newObj)
			}
		}
	}

	if ctx.Value(conf.SkipHookKey) != nil || !needHandleObjsUpdateHook() {
		return nil
	}
	if !srcObj.IsDir() {
		go objsUpdateHook(context.WithoutCancel(ctx), storage, dirPath, false)
	} else {
		go objsUpdateHook(context.WithoutCancel(ctx), storage, stdpath.Join(dirPath, dstName), true)
	}
	return nil
}

// Copy Just copy file[s] in a storage
func Copy(ctx context.Context, storage driver.Driver, srcPath, dstDirPath string) error {
	return CopyWithOptions(ctx, storage, srcPath, dstDirPath, TransferOptions{})
}

// CopyWithOptions copies srcPath into dstDirPath while enforcing same-name conflict rules.
func CopyWithOptions(ctx context.Context, storage driver.Driver, srcPath, dstDirPath string, opts TransferOptions) error {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	srcPath = utils.FixAndCleanPath(srcPath)
	dstDirPath = utils.FixAndCleanPath(dstDirPath)
	if dstDirPath == stdpath.Dir(srcPath) {
		return errors.New("copy in place is not supported")
	}
	switch storage.(type) {
	case driver.CopyResult, driver.Copy:
	default:
		return errs.NotImplement
	}

	srcRawObj, err := getFreshOnMiss(ctx, storage, srcPath, true)
	if err != nil {
		return errors.WithMessage(err, "failed to get src object")
	}
	// if model.ObjHasMask(srcRawObj, model.NoCopy) {
	// 	return errors.WithStack(errs.PermissionDenied)
	// }
	srcObj := model.UnwrapObjName(srcRawObj)
	if srcObj.IsDir() {
		return errs.NotSupport
	}
	dstDir, err := getUnwrapFreshOnMiss(ctx, storage, dstDirPath)
	if err != nil {
		return errors.WithMessage(err, "failed to get dst dir")
	}
	if !dstDir.IsDir() {
		return errs.NotFolder
	}
	if model.ObjHasMask(dstDir, model.NoWrite) {
		return errors.WithStack(errs.PermissionDenied)
	}

	dstPath := stdpath.Join(dstDirPath, srcRawObj.GetName())
	unlock := transferLocks.Lock(Key(storage, dstPath))
	defer unlock()

	rawConflicts, err := ListExistingByName(ctx, storage, dstDirPath, srcRawObj.GetName())
	if err != nil {
		return errors.WithMessage(err, "failed to check dst conflicts")
	}
	conflicts, err := ValidateTransferConflicts(srcObj.IsDir(), "", rawConflicts, opts)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		if _, ok := storage.(driver.Remove); !ok {
			return errs.NotImplement
		}
	}

	var newObj model.Obj
	switch s := storage.(type) {
	case driver.CopyResult:
		newObj, err = s.Copy(ctx, srcObj, dstDir)
	case driver.Copy:
		err = s.Copy(ctx, srcObj, dstDir)
	default:
		err = errs.NotImplement
	}
	if err != nil {
		return errors.WithStack(err)
	}

	dstKey := Key(storage, dstDirPath)
	if !srcRawObj.IsDir() {
		Cache.linkCache.DeleteKey(stdpath.Join(dstKey, srcRawObj.GetName()))
	}
	if removeErr := removeConflictsByID(ctx, storage, dstDirPath, conflicts, objIDOrEmpty(newObj)); removeErr != nil {
		return removeErr
	}
	if !storage.Config().NoCache {
		if newObj == nil {
			// Driver didn't return the newly created object (new ID on Google Drive), so invalidate dst dirCache
			// rather than caching srcRawObj with the source file's ID.
			Cache.DeleteDirectory(storage, dstDirPath)
		} else if cache, exist := Cache.dirCache.Get(dstKey); exist {
			cache.ReplaceObject(srcRawObj.GetName(), wrapObjName(storage, newObj))
		}
	}

	if ctx.Value(conf.SkipHookKey) != nil || !needHandleObjsUpdateHook() {
		return nil
	}
	if !srcObj.IsDir() {
		go objsUpdateHook(context.WithoutCancel(ctx), storage, dstDirPath, false)
	} else {
		go objsUpdateHook(context.WithoutCancel(ctx), storage, stdpath.Join(dstDirPath, srcObj.GetName()), true)
	}
	return nil
}

func Remove(ctx context.Context, storage driver.Driver, path string) error {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	path = utils.FixAndCleanPath(path)
	if utils.PathEqual(path, "/") {
		return errors.New("delete root folder is not allowed")
	}
	rawObj, err := getFreshOnMiss(ctx, storage, path, true)
	if err != nil {
		// if object not found, it's ok
		if errs.IsObjectNotFound(err) {
			log.Debugf("%s have been removed", path)
			return nil
		}
		return errors.WithMessage(err, "failed to get object")
	}
	if model.ObjHasMask(rawObj, model.NoRemove) {
		return errors.WithStack(errs.PermissionDenied)
	}
	dirPath := stdpath.Dir(path)

	switch s := storage.(type) {
	case driver.Remove:
		err = s.Remove(ctx, model.UnwrapObjName(rawObj))
		if err == nil {
			Cache.removeDirectoryObject(storage, dirPath, rawObj)
		}
	default:
		return errs.NotImplement
	}
	return errors.WithStack(err)
}

func Put(ctx context.Context, storage driver.Driver, dstDirPath string, file model.FileStreamer, up driver.UpdateProgress) error {
	_, err := putInternal(ctx, storage, dstDirPath, file, up)
	return err
}

// PutWithOptions uploads file into dstDirPath while enforcing transfer conflict and overwrite semantics.
func PutWithOptions(ctx context.Context, storage driver.Driver, dstDirPath string, file model.FileStreamer, up driver.UpdateProgress, opts TransferOptions) error {
	dstDirPath = utils.FixAndCleanPath(dstDirPath)
	dstPath := stdpath.Join(dstDirPath, file.GetName())
	unlock := transferLocks.Lock(Key(storage, dstPath))
	defer unlock()

	var conflicts []model.Obj
	rawConflicts, listErr := ListExistingByName(ctx, storage, dstDirPath, file.GetName())
	if listErr != nil {
		if !errs.IsObjectNotFound(listErr) {
			_ = file.Close()
			return errors.WithMessage(listErr, "failed to check dst conflicts")
		}
	} else {
		var err error
		conflicts, err = ValidateTransferConflicts(false, "", rawConflicts, opts)
		if err != nil {
			_ = file.Close()
			return err
		}
	}

	newObj, err := putInternal(ctx, storage, dstDirPath, file, up)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		newID := objIDOrEmpty(newObj)
		if newID != "" {
			if removeErr := removeConflictsByID(ctx, storage, dstDirPath, conflicts, newID); removeErr != nil {
				return removeErr
			}
		} else {
			// Driver did not return a concrete object ID; only prune old duplicates when
			// multiple entries exist so path-based in-place overwrites remain untouched.
			afterObjs, afterErr := ListExistingByName(ctx, storage, dstDirPath, file.GetName())
			if afterErr == nil && len(afterObjs) > 1 {
				toDelete := make([]model.Obj, 0, len(afterObjs)-1)
				preIDs := make(map[string]struct{}, len(conflicts))
				for _, c := range conflicts {
					if c.GetID() != "" {
						preIDs[c.GetID()] = struct{}{}
					}
				}
				kept := false
				for i := len(afterObjs) - 1; i >= 0; i-- {
					obj := afterObjs[i]
					_, isOld := preIDs[obj.GetID()]
					if !kept && !isOld {
						kept = true
						continue
					}
					toDelete = append(toDelete, obj)
				}
				if !kept && len(toDelete) > 0 {
					toDelete = toDelete[1:]
				}
				if removeErr := removeConflictsByID(ctx, storage, dstDirPath, toDelete); removeErr != nil {
					return removeErr
				}
			}
		}
		if !storage.Config().NoCache {
			if newObj != nil {
				if cache, exist := Cache.dirCache.Get(Key(storage, dstDirPath)); exist {
					cache.ReplaceObject(newObj.GetName(), newObj)
				}
			} else {
				Cache.DeleteDirectory(storage, dstDirPath)
			}
		}
	}
	return nil
}

func putInternal(ctx context.Context, storage driver.Driver, dstDirPath string, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	defer func() {
		if err := file.Close(); err != nil {
			log.Errorf("failed to close file streamer, %v", err)
		}
	}()
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return nil, errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	// UrlTree PUT
	if storage.Config().OnlyIndices {
		var link string
		dstDirPath, link = urlTreeSplitLineFormPath(stdpath.Join(dstDirPath, file.GetName()))
		file = &stream.FileStream{Obj: &model.Object{Name: link}, Closers: utils.Closers{file}}
	}
	// if file exist and size = 0, delete it
	dstDirPath = utils.FixAndCleanPath(dstDirPath)
	dstPath := stdpath.Join(dstDirPath, file.GetName())
	tempName := file.GetName() + ".openlist_to_delete"
	tempPath := stdpath.Join(dstDirPath, tempName)
	fi, err := GetUnwrap(ctx, storage, dstPath)
	if err == nil {
		if fi.GetSize() == 0 {
			err = Remove(ctx, storage, dstPath)
			if err != nil {
				return nil, errors.WithMessagef(err, "while uploading, failed remove existing file which size = 0")
			}
		} else if storage.Config().NoOverwriteUpload && ctx.Value(conf.SkipNoOverwriteKey) == nil {
			// try to rename old obj
			err = Rename(ctx, storage, dstPath, tempName)
			if err != nil {
				return nil, err
			}
		} else {
			file.SetExist(fi)
		}
	}
	err = MakeDir(ctx, storage, dstDirPath)
	if err != nil && !errs.IsObjectAlreadyExists(err) {
		return nil, errors.WithMessagef(err, "failed to make dir [%s]", dstDirPath)
	}
	parentDir, err := GetUnwrap(ctx, storage, dstDirPath)
	// this should not happen
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get dir [%s]", dstDirPath)
	}
	if model.ObjHasMask(parentDir, model.NoWrite) {
		return nil, errors.WithStack(errs.PermissionDenied)
	}
	// if up is nil, set a default to prevent panic
	if up == nil {
		up = func(p float64) {}
	}

	// 如果小于0，则通过缓存获取完整大小，可能发生于流式上传
	if file.GetSize() < 0 {
		log.Warnf("file size < 0, try to get full size from cache")
		file.CacheFullAndWriter(nil, nil)
	}

	var newObj model.Obj
	switch s := storage.(type) {
	case driver.PutResult:
		newObj, err = s.Put(ctx, parentDir, file, up)
	case driver.Put:
		err = s.Put(ctx, parentDir, file, up)
	default:
		return nil, errs.NotImplement
	}
	var wrappedNewObj model.Obj
	if err == nil {
		Cache.linkCache.DeleteKey(Key(storage, dstPath))
		if newObj != nil {
			wrappedNewObj = wrapObjName(storage, newObj)
		}
		if !storage.Config().NoCache {
			if cache, exist := Cache.dirCache.Get(Key(storage, dstDirPath)); exist {
				cachedObj := wrappedNewObj
				if cachedObj == nil {
					cachedObj = wrapObjName(storage, &model.Object{
						Name:     file.GetName(),
						Size:     file.GetSize(),
						Modified: file.ModTime(),
						Ctime:    file.CreateTime(),
						Mask:     model.Temp,
					})
				}
				cache.UpdateObject(cachedObj.GetName(), cachedObj)
			}
		}

		if ctx.Value(conf.SkipHookKey) == nil && needHandleObjsUpdateHook() {
			go objsUpdateHook(context.WithoutCancel(ctx), storage, dstDirPath, false)
		}
	}
	log.Debugf("put file [%s] done", file.GetName())
	if storage.Config().NoOverwriteUpload && fi != nil && fi.GetSize() > 0 {
		if err != nil {
			// upload failed, recover old obj
			err := Rename(ctx, storage, tempPath, file.GetName())
			if err != nil {
				log.Errorf("failed recover old obj: %+v", err)
			}
		} else {
			// upload success, remove old obj
			err = Remove(ctx, storage, tempPath)
		}
	}
	return wrappedNewObj, errors.WithStack(err)
}

func PutURL(ctx context.Context, storage driver.Driver, dstDirPath, dstName, url string) error {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	dstDirPath = utils.FixAndCleanPath(dstDirPath)
	dstPath := stdpath.Join(dstDirPath, dstName)

	if _, err := Get(ctx, storage, dstPath); err == nil {
		return errors.WithStack(errs.ObjectAlreadyExists)
	}
	err := MakeDir(ctx, storage, dstDirPath)
	if err != nil {
		return errors.WithMessagef(err, "failed to make dir [%s]", dstDirPath)
	}
	dstDir, err := GetUnwrap(ctx, storage, dstDirPath)
	if err != nil {
		return errors.WithMessagef(err, "failed to get dir [%s]", dstDirPath)
	}
	if model.ObjHasMask(dstDir, model.NoWrite) {
		return errors.WithStack(errs.PermissionDenied)
	}
	var newObj model.Obj
	switch s := storage.(type) {
	case driver.PutURLResult:
		newObj, err = s.PutURL(ctx, dstDir, dstName, url)
	case driver.PutURL:
		err = s.PutURL(ctx, dstDir, dstName, url)
	default:
		return errors.WithStack(errs.NotImplement)
	}
	if err == nil {
		Cache.linkCache.DeleteKey(Key(storage, dstPath))
		if !storage.Config().NoCache {
			if cache, exist := Cache.dirCache.Get(Key(storage, dstDirPath)); exist {
				if newObj == nil {
					t := time.Now()
					newObj = &model.Object{
						Name:     dstName,
						Modified: t,
						Ctime:    t,
						Mask:     model.Temp,
					}
				}
				newObj = wrapObjName(storage, newObj)
				cache.UpdateObject(newObj.GetName(), newObj)
			}

			if ctx.Value(conf.SkipHookKey) == nil && needHandleObjsUpdateHook() {
				go objsUpdateHook(context.WithoutCancel(ctx), storage, dstDirPath, false)
			}
		}
	}
	log.Debugf("put url [%s](%s) done", dstName, url)
	return errors.WithStack(err)
}

func GetDirectUploadTools(storage driver.Driver) []string {
	du, ok := storage.(driver.DirectUploader)
	if !ok {
		return nil
	}
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return nil
	}
	return du.GetDirectUploadTools()
}

func GetDirectUploadInfo(ctx context.Context, tool string, storage driver.Driver, dstDirPath, dstName string, fileSize int64, overwrite bool) (any, error) {
	du, ok := storage.(driver.DirectUploader)
	if !ok {
		return nil, errors.WithStack(errs.NotImplement)
	}
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return nil, errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	dstDirPath = utils.FixAndCleanPath(dstDirPath)
	dstPath := stdpath.Join(dstDirPath, dstName)
	var err error
	if !overwrite {
		_, err = Get(ctx, storage, dstPath)
		if err == nil {
			return nil, errors.WithStack(errs.ObjectAlreadyExists)
		}
		if !errs.IsObjectNotFound(err) {
			return nil, errors.WithMessage(err, "failed to check if object exists")
		}
	}
	err = MakeDir(ctx, storage, dstDirPath)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to make dir [%s]", dstDirPath)
	}
	dstDir, err := GetUnwrap(ctx, storage, dstDirPath)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get dir [%s]", dstDirPath)
	}
	info, err := du.GetDirectUploadInfo(ctx, tool, dstDir, dstName, fileSize)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return info, nil
}

func objsUpdateHook(ctx context.Context, storage driver.Driver, dirPath string, recursive bool) {
	files, err := List(ctx, storage, dirPath, model.ListArgs{SkipHook: true})
	if err != nil {
		return
	}
	if !recursive {
		HandleObjsUpdateHook(ctx, utils.GetFullPath(storage.GetStorage().MountPath, dirPath), files)
		return
	}
	var limiter *rate.Limiter
	if l, _ := GetSettingItemByKey(conf.HandleHookRateLimit); l != nil {
		if f, e := strconv.ParseFloat(l.Value, 64); e == nil && f > .0 {
			limiter = rate.NewLimiter(rate.Limit(f), 1)
		}
	}
	recursivelyObjsUpdateHook(ctx, storage, dirPath, files, limiter)
}
func recursivelyObjsUpdateHook(ctx context.Context, storage driver.Driver, dirPath string, files []model.Obj, limiter *rate.Limiter) {
	HandleObjsUpdateHook(ctx, utils.GetFullPath(storage.GetStorage().MountPath, dirPath), files)
	for _, f := range files {
		if utils.IsCanceled(ctx) {
			return
		}
		if !f.IsDir() {
			continue
		}
		dstPath := stdpath.Join(dirPath, f.GetName())
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				return
			}
		}
		files, err := List(ctx, storage, dstPath, model.ListArgs{SkipHook: true})
		if err == nil {
			recursivelyObjsUpdateHook(ctx, storage, dstPath, files, limiter)
		}
	}
}

func needHandleObjsUpdateHook() bool {
	if len(objsUpdateHooks) < 1 {
		return false
	}
	needHandle, _ := GetSettingItemByKey(conf.HandleHookAfterWriting)
	return needHandle != nil && (needHandle.Value == "true" || needHandle.Value == "1")
}

func wrapObjsName(storage driver.Driver, objs []model.Obj) {
	if _, ok := storage.(driver.Getter); !ok {
		model.WrapObjsName(objs)
	}
}
func wrapObjName(storage driver.Driver, obj model.Obj) model.Obj {
	if _, ok := storage.(driver.Getter); !ok {
		return model.WrapObjName(obj)
	}
	return obj
}
