package handles

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	stdpath "path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/internal/sharing"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	gocache "github.com/OpenListTeam/go-cache"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	packageDownloadConcurrency    = 6
	packageDownloadPrefetchWindow = 6
	packageDownloadInlineLimit    = 4 * 1024 * 1024 // 4 MiB per small-file slot
	packageDownloadTicketTTL      = 5 * time.Minute
	packageDownloadCopyBufferSize = 64 * 1024
)

type packageDownloadReq struct {
	Path     string   `json:"path"`
	Names    []string `json:"names"`
	Password string   `json:"password"`
}

type packageDownloadTicket struct {
	IsShare  bool
	ShareID  string
	ShareRel string
	BaseDir  string
	Names    []string
	Password string
	SaveName string
	User     *model.User
}

type packageDownloadEntry struct {
	ZipPath    string
	FullPath   string // owner path or share-relative path
	Size       int64
	ModTime    time.Time
	IsDir      bool
	InitialObj model.Obj
}

type accessTokenInvalidator interface {
	InvalidateAccessToken(failedToken string)
}

var packageDownloadTickets = gocache.NewMemCache[*packageDownloadTicket]()

func newPackageDownloadTicketID() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validatePackageItemName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/\\")
}

func sanitizeArchiveName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	cleaned = strings.Trim(cleaned, ".")
	if cleaned == "" {
		return "download"
	}
	return cleaned
}

func FsPackageDownloadCreate(c *gin.Context) {
	var req packageDownloadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if len(req.Names) == 0 {
		common.ErrorStrResp(c, "no files selected", 400)
		return
	}
	for _, name := range req.Names {
		if !validatePackageItemName(name) {
			common.ErrorStrResp(c, "invalid file name", 400)
			return
		}
	}

	var ticket *packageDownloadTicket
	if strings.HasPrefix(req.Path, "/@s") {
		if !setting.GetBool("package_download") {
			common.ErrorStrResp(c, "package download is disabled", 403)
			return
		}
		sharePath := strings.TrimPrefix(req.Path, "/@s")
		sid, relDir, _ := strings.Cut(strings.TrimPrefix(sharePath, "/"), "/")
		if sid == "" {
			common.ErrorStrResp(c, "invalid share id", 400)
			return
		}
		relDir = utils.FixAndCleanPath(relDir)
		var shareObj *model.Sharing
		for _, name := range req.Names {
			targetRel := stdpath.Join(relDir, name)
			s, _, err := sharing.Get(c.Request.Context(), sid, targetRel, model.SharingListArgs{
				Pwd: req.Password,
			})
			if dealError(c, err) {
				return
			}
			shareObj = s
		}
		if shareObj != nil {
			_ = countAccess(c.ClientIP(), shareObj)
		}
		saveName := stdpath.Base(relDir)
		if len(req.Names) == 1 {
			saveName = req.Names[0]
		} else if saveName == "/" || saveName == "." || saveName == "" {
			saveName = "share"
		}
		ticket = &packageDownloadTicket{
			IsShare:  true,
			ShareID:  sid,
			ShareRel: relDir,
			Names:    append([]string(nil), req.Names...),
			Password: req.Password,
			SaveName: sanitizeArchiveName(saveName),
		}
	} else {
		user, ok := c.Request.Context().Value(conf.UserKey).(*model.User)
		if !ok || user == nil {
			common.ErrorStrResp(c, "Please login first", 401)
			return
		}
		if !user.IsAdmin() && !setting.GetBool("package_download") {
			common.ErrorStrResp(c, "package download is disabled", 403)
			return
		}
		baseDir, err := user.JoinPath(req.Path)
		if err != nil {
			common.ErrorResp(c, err, 403)
			return
		}
		baseMeta, err := op.GetNearestMeta(baseDir)
		if err != nil && !errors.Is(err, errs.MetaNotFound) {
			common.ErrorResp(c, err, 500, true)
			return
		}
		if !common.CanAccess(user, baseMeta, baseDir, req.Password) {
			common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
			return
		}
		ctx := context.WithValue(c.Request.Context(), conf.UserKey, user)
		ctx = context.WithValue(ctx, conf.MetaKey, baseMeta)
		for _, name := range req.Names {
			itemPath := stdpath.Join(baseDir, name)
			itemMeta, metaErr := op.GetNearestMeta(itemPath)
			if metaErr != nil && !errors.Is(metaErr, errs.MetaNotFound) {
				common.ErrorResp(c, metaErr, 500, true)
				return
			}
			if !common.CanAccess(user, itemMeta, itemPath, req.Password) {
				common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
				return
			}
			if _, err := fs.Get(ctx, itemPath, &fs.GetArgs{NoLog: true}); err != nil {
				common.ErrorResp(c, err, 404)
				return
			}
		}
		saveName := stdpath.Base(baseDir)
		if len(req.Names) == 1 {
			saveName = req.Names[0]
		} else if saveName == "/" || saveName == "." || saveName == "" {
			saveName = "download"
		}
		ticket = &packageDownloadTicket{
			IsShare:  false,
			BaseDir:  baseDir,
			Names:    append([]string(nil), req.Names...),
			Password: req.Password,
			SaveName: sanitizeArchiveName(saveName),
			User:     user,
		}
	}

	ticketID, err := newPackageDownloadTicketID()
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	packageDownloadTickets.Set(ticketID, ticket, gocache.WithEx[*packageDownloadTicket](packageDownloadTicketTTL))
	common.SuccessResp(c, gin.H{
		"ticket":   ticketID,
		"url":      "/api/fs/package_download/" + ticketID,
		"filename": ticket.SaveName + ".zip",
	})
}

func FsPackageDownloadStream(c *gin.Context) {
	ticketID := c.Param("ticket")
	ticket, ok := packageDownloadTickets.Get(ticketID)
	if !ok || ticket == nil {
		common.ErrorPage(c, errors.New("download link expired or invalid"), http.StatusNotFound)
		return
	}

	if ticket.IsShare {
		s, err := op.GetSharingById(ticket.ShareID)
		if err != nil {
			common.ErrorPage(c, errs.SharingNotFound, http.StatusNotFound)
			return
		}
		if !s.Valid() {
			common.ErrorPage(c, errs.InvalidSharing, http.StatusForbidden)
			return
		}
		if !s.Verify(ticket.Password) {
			common.ErrorPage(c, errs.WrongShareCode, http.StatusForbidden)
			return
		}
	}

	entries, err := collectPackageEntries(c.Request.Context(), ticket)
	if err != nil {
		common.ErrorPage(c, err, http.StatusInternalServerError)
		return
	}
	if len(entries) == 0 {
		common.ErrorPage(c, errors.New("no files to download"), http.StatusNotFound)
		return
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", utils.GenerateContentDisposition(ticket.SaveName+".zip"))
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Accel-Buffering", "no")

	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return
	}

	c.Status(http.StatusOK)
	var shareObj *model.Sharing
	if ticket.IsShare {
		shareObj, err = op.GetSharingById(ticket.ShareID)
		if err != nil {
			log.Errorf("package download share lookup error: %v", err)
			return
		}
	}

	err = writeStreamingZip(c.Request.Context(), c.Writer, entries, func(ctx context.Context, entry packageDownloadEntry) (io.ReadCloser, error) {
		return openPackageEntryStream(ctx, ticket, shareObj, entry)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Errorf("package download stream error (%s): %v", ticket.SaveName, err)
	}
}

type dirListNode struct {
	zipPrefix string
	fullPath  string
	modTime   time.Time
	children  []*dirListNode
	entries   []packageDownloadEntry
}

func collectPackageEntries(ctx context.Context, ticket *packageDownloadTicket) ([]packageDownloadEntry, error) {
	listSem := make(chan struct{}, packageDownloadConcurrency)
	var roots []*dirListNode

	if ticket.IsShare {
		for _, name := range ticket.Names {
			relPath := stdpath.Join(ticket.ShareRel, name)
			_, obj, err := sharing.Get(ctx, ticket.ShareID, relPath, model.SharingListArgs{Pwd: ticket.Password})
			if err != nil {
				return nil, err
			}
			if !obj.IsDir() {
				roots = append(roots, &dirListNode{
					entries: []packageDownloadEntry{{
						ZipPath:    obj.GetName(),
						FullPath:   relPath,
						Size:       obj.GetSize(),
						ModTime:    obj.ModTime(),
						InitialObj: obj,
					}},
				})
				continue
			}
			zipPrefix := obj.GetName()
			if len(ticket.Names) == 1 {
				zipPrefix = ""
			}
			node := &dirListNode{
				zipPrefix: zipPrefix,
				fullPath:  relPath,
				modTime:   obj.ModTime(),
			}
			roots = append(roots, node)
		}
		if err := populateNodesConcurrently(ctx, roots, listSem, func(nodeCtx context.Context, path string) ([]model.Obj, error) {
			_, objs, err := sharing.List(nodeCtx, ticket.ShareID, path, model.SharingListArgs{Pwd: ticket.Password})
			return objs, err
		}); err != nil {
			return nil, err
		}
	} else {
		baseMeta, err := op.GetNearestMeta(ticket.BaseDir)
		if err != nil && !errors.Is(err, errs.MetaNotFound) {
			return nil, err
		}
		ownerCtx := context.WithValue(ctx, conf.UserKey, ticket.User)
		ownerCtx = context.WithValue(ownerCtx, conf.MetaKey, baseMeta)
		for _, name := range ticket.Names {
			fullPath := stdpath.Join(ticket.BaseDir, name)
			obj, err := fs.Get(ownerCtx, fullPath, &fs.GetArgs{NoLog: true})
			if err != nil {
				return nil, err
			}
			if !obj.IsDir() {
				roots = append(roots, &dirListNode{
					entries: []packageDownloadEntry{{
						ZipPath:    obj.GetName(),
						FullPath:   fullPath,
						Size:       obj.GetSize(),
						ModTime:    obj.ModTime(),
						InitialObj: obj,
					}},
				})
				continue
			}
			zipPrefix := obj.GetName()
			if len(ticket.Names) == 1 {
				zipPrefix = ""
			}
			node := &dirListNode{
				zipPrefix: zipPrefix,
				fullPath:  fullPath,
				modTime:   obj.ModTime(),
			}
			roots = append(roots, node)
		}
		if err := populateNodesConcurrently(ownerCtx, roots, listSem, func(nodeCtx context.Context, path string) ([]model.Obj, error) {
			meta, err := op.GetNearestMeta(path)
			if err != nil && !errors.Is(err, errs.MetaNotFound) {
				return nil, err
			}
			if !common.CanAccess(ticket.User, meta, path, ticket.Password) {
				return nil, nil
			}
			subCtx := context.WithValue(nodeCtx, conf.MetaKey, meta)
			return fs.List(subCtx, path, &fs.ListArgs{NoLog: true})
		}); err != nil {
			return nil, err
		}
	}

	var result []packageDownloadEntry
	for _, root := range roots {
		flattenDirNode(root, &result)
	}
	return result, nil
}

func populateNodesConcurrently(
	ctx context.Context,
	nodes []*dirListNode,
	sem chan struct{},
	listFn func(context.Context, string) ([]model.Obj, error),
) error {
	var dirNodes []*dirListNode
	for _, n := range nodes {
		if n.fullPath != "" {
			dirNodes = append(dirNodes, n)
		}
	}
	if len(dirNodes) == 0 {
		return nil
	}

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, n := range dirNodes {
		wg.Add(1)
		go func(node *dirListNode) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-listCtx.Done():
				return
			}
			objs, err := listFn(listCtx, node.fullPath)
			<-sem
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			sorted := make([]model.Obj, len(objs))
			copy(sorted, objs)
			sort.Slice(sorted, func(i, j int) bool {
				if sorted[i].IsDir() != sorted[j].IsDir() {
					return sorted[i].IsDir()
				}
				return sorted[i].GetName() < sorted[j].GetName()
			})
			for _, obj := range sorted {
				childPath := stdpath.Join(node.fullPath, obj.GetName())
				childZip := stdpath.Join(node.zipPrefix, obj.GetName())
				if obj.IsDir() {
					node.children = append(node.children, &dirListNode{
						zipPrefix: childZip,
						fullPath:  childPath,
						modTime:   obj.ModTime(),
					})
				} else {
					node.entries = append(node.entries, packageDownloadEntry{
						ZipPath:    childZip,
						FullPath:   childPath,
						Size:       obj.GetSize(),
						ModTime:    obj.ModTime(),
						InitialObj: obj,
					})
				}
			}
		}(n)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	var nextLevel []*dirListNode
	for _, n := range dirNodes {
		nextLevel = append(nextLevel, n.children...)
	}
	if len(nextLevel) > 0 {
		return populateNodesConcurrently(ctx, nextLevel, sem, listFn)
	}
	return nil
}

func flattenDirNode(node *dirListNode, out *[]packageDownloadEntry) {
	if node.fullPath != "" && len(node.entries) == 0 && len(node.children) == 0 && node.zipPrefix != "" {
		*out = append(*out, packageDownloadEntry{
			ZipPath: node.zipPrefix,
			ModTime: node.modTime,
			IsDir:   true,
		})
		return
	}
	*out = append(*out, node.entries...)
	for _, child := range node.children {
		flattenDirNode(child, out)
	}
}

func openPackageEntryStream(
	ctx context.Context,
	ticket *packageDownloadTicket,
	shareObj *model.Sharing,
	entry packageDownloadEntry,
) (io.ReadCloser, error) {
	targetPath := entry.FullPath
	if ticket.IsShare {
		unwrapPath, err := op.GetSharingUnwrapPath(shareObj, entry.FullPath)
		if err != nil {
			return nil, err
		}
		targetPath = unwrapPath
	}
	storage, actualPath, err := op.GetStorageAndActualPath(targetPath)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		link, obj, err := linkForPackageEntry(ctx, storage, actualPath, entry.InitialObj)
		if err != nil {
			return nil, err
		}
		rc, err := openStreamFromLink(ctx, link, obj)
		if err == nil {
			return rc, nil
		}
		if attempt == 0 && errors.Is(err, net.HttpStatusCodeError(http.StatusUnauthorized)) {
			if inv, ok := storage.(accessTokenInvalidator); ok {
				auth := link.Header.Get("Authorization")
				inv.InvalidateAccessToken(strings.TrimPrefix(auth, "Bearer "))
			}
			op.DeleteLinkCache(storage, actualPath)
			continue
		}
		return nil, err
	}
	return nil, errors.New("unreachable")
}

func linkForPackageEntry(
	ctx context.Context,
	storage driver.Driver,
	actualPath string,
	initialObj model.Obj,
) (*model.Link, model.Obj, error) {
	// Use the already-listed object (e.g. *GoogleDriveObj carrying NativeMimeType)
	// directly when available to avoid extra Get/metadata round trips.
	if initialObj != nil && !storage.Config().CheckStatus {
		link, err := storage.Link(ctx, initialObj, model.LinkArgs{})
		if err == nil {
			return link, initialObj, nil
		}
	}
	if initialObj != nil && storage.GetStorage().Status == op.WORK {
		link, err := storage.Link(ctx, initialObj, model.LinkArgs{})
		if err == nil {
			return link, initialObj, nil
		}
	}
	return op.Link(ctx, storage, actualPath, model.LinkArgs{})
}

func openStreamFromLink(ctx context.Context, link *model.Link, obj model.Obj) (io.ReadCloser, error) {
	if link.RangeReader != nil {
		size := link.ContentLength
		if size <= 0 && obj != nil {
			size = obj.GetSize()
		}
		r, err := link.RangeReader.RangeRead(ctx, http_range.Range{Start: 0, Length: size})
		if err != nil {
			link.Close()
			return nil, err
		}
		return &linkedReadCloser{Reader: r, link: link}, nil
	}
	if link.URL == "" {
		link.Close()
		return nil, errors.New("empty download link URL")
	}
	res, err := net.RequestHttp(ctx, http.MethodGet, link.Header, link.URL)
	if err != nil {
		link.Close()
		return nil, err
	}
	return &linkedReadCloser{
		Reader: &stream.RateLimitReader{
			Reader:  res.Body,
			Limiter: stream.ServerDownloadLimit,
			Ctx:     ctx,
		},
		body: res.Body,
		link: link,
	}, nil
}

type linkedReadCloser struct {
	io.Reader
	body io.Closer
	link *model.Link
}

func (l *linkedReadCloser) Close() error {
	var err error
	if l.body != nil {
		err = l.body.Close()
	}
	if l.link != nil {
		l.link.Close()
	}
	return err
}

type packageOpenFunc func(ctx context.Context, entry packageDownloadEntry) (io.ReadCloser, error)

type prefetchSlot struct {
	ready          chan struct{}
	data           []byte
	reader         io.Reader
	closer         io.Closer
	streamOnDemand bool
	err            error
}

func dedupeZipPath(rawPath string, isDir bool, used map[string]struct{}) string {
	clean := strings.ReplaceAll(rawPath, "\\", "/")
	clean = strings.TrimLeft(clean, "/")
	if isDir {
		clean = strings.TrimRight(clean, "/")
	}
	if clean == "" {
		clean = "file"
	}
	if _, exists := used[clean]; !exists {
		used[clean] = struct{}{}
		return clean
	}
	dir, base := stdpath.Split(clean)
	ext := ""
	stem := base
	if !isDir {
		ext = stdpath.Ext(base)
		if ext != "" && ext != base {
			stem = strings.TrimSuffix(base, ext)
		} else {
			ext = ""
		}
	}
	for i := 1; ; i++ {
		candidate := dir + fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, exists := used[candidate]; !exists {
			used[candidate] = struct{}{}
			return candidate
		}
	}
}

func writeStreamingZip(
	ctx context.Context,
	w io.Writer,
	entries []packageDownloadEntry,
	openFn packageOpenFunc,
) error {
	dlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	zw := zip.NewWriter(w)
	usedNames := make(map[string]struct{}, len(entries))
	resolvedNames := make([]string, len(entries))
	for i, e := range entries {
		resolvedNames[i] = dedupeZipPath(e.ZipPath, e.IsDir, usedNames)
	}

	windowSem := make(chan struct{}, packageDownloadPrefetchWindow)
	workerSem := make(chan struct{}, packageDownloadConcurrency)
	slotCh := make(chan *prefetchSlot, packageDownloadPrefetchWindow)
	feederDone := make(chan struct{})
	var wg sync.WaitGroup

	go func() {
		defer close(feederDone)
		defer close(slotCh)
		for _, entry := range entries {
			if entry.IsDir {
				continue
			}
			select {
			case windowSem <- struct{}{}:
			case <-dlCtx.Done():
				return
			}
			slot := &prefetchSlot{ready: make(chan struct{})}
			if entry.Size > packageDownloadInlineLimit || entry.Size < 0 {
				slot.streamOnDemand = true
				close(slot.ready)
			} else {
				wg.Add(1)
				go func(ent packageDownloadEntry, sl *prefetchSlot) {
					defer wg.Done()
					defer close(sl.ready)
					select {
					case workerSem <- struct{}{}:
					case <-dlCtx.Done():
						sl.err = dlCtx.Err()
						return
					}
					defer func() { <-workerSem }()

					rc, err := openFn(dlCtx, ent)
					if err != nil {
						sl.err = err
						return
					}
					buf, err := io.ReadAll(io.LimitReader(rc, packageDownloadInlineLimit+1))
					if err != nil {
						_ = rc.Close()
						sl.err = err
						return
					}
					if int64(len(buf)) <= packageDownloadInlineLimit {
						_ = rc.Close()
						sl.data = buf
					} else {
						sl.reader = io.MultiReader(bytes.NewReader(buf), rc)
						sl.closer = rc
					}
				}(entry, slot)
			}
			select {
			case slotCh <- slot:
			case <-dlCtx.Done():
				if slot.closer != nil {
					_ = slot.closer.Close()
				}
				return
			}
		}
	}()

	var activeSlot *prefetchSlot
	defer func() {
		cancel()
		<-feederDone
		wg.Wait()
		if activeSlot != nil && activeSlot.closer != nil {
			_ = activeSlot.closer.Close()
		}
		for sl := range slotCh {
			if sl.closer != nil {
				_ = sl.closer.Close()
			}
		}
	}()

	copyBuf := make([]byte, packageDownloadCopyBufferSize)
	for i, entry := range entries {
		zipName := resolvedNames[i]
		modTime := entry.ModTime
		if modTime.IsZero() {
			modTime = time.Now()
		}
		if entry.IsDir {
			hdr := &zip.FileHeader{
				Name:     zipName + "/",
				Method:   zip.Store,
				Modified: modTime,
			}
			if _, err := zw.CreateHeader(hdr); err != nil {
				return err
			}
			continue
		}

		select {
		case sl, ok := <-slotCh:
			if !ok {
				return dlCtx.Err()
			}
			activeSlot = sl
		case <-dlCtx.Done():
			return dlCtx.Err()
		}

		select {
		case <-activeSlot.ready:
		case <-dlCtx.Done():
			<-windowSem
			return dlCtx.Err()
		}

		if activeSlot.err != nil {
			<-windowSem
			return activeSlot.err
		}

		hdr := &zip.FileHeader{
			Name:     zipName,
			Method:   zip.Store,
			Modified: modTime,
		}
		ew, err := zw.CreateHeader(hdr)
		if err != nil {
			if activeSlot.closer != nil {
				_ = activeSlot.closer.Close()
				activeSlot.closer = nil
			}
			<-windowSem
			return err
		}

		if activeSlot.streamOnDemand {
			rc, err := openFn(dlCtx, entry)
			if err != nil {
				<-windowSem
				return err
			}
			_, copyErr := io.CopyBuffer(ew, rc, copyBuf)
			closeErr := rc.Close()
			<-windowSem
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		} else if activeSlot.reader != nil {
			_, copyErr := io.CopyBuffer(ew, activeSlot.reader, copyBuf)
			closeErr := activeSlot.closer.Close()
			activeSlot.closer = nil
			<-windowSem
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		} else {
			if len(activeSlot.data) > 0 {
				if _, err := ew.Write(activeSlot.data); err != nil {
					<-windowSem
					return err
				}
			}
			<-windowSem
		}
		activeSlot = nil
	}

	return zw.Close()
}
