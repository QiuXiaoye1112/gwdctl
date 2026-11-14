package op

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type memNode struct {
	id       string
	parentID string
	name     string
	isDir    bool
	content  []byte
	modified time.Time
}

// memIDDrive simulates an ID-keyed cloud drive (like Google Drive) where
// multiple items in the same folder can share the same name.
type memIDDrive struct {
	model.Storage
	mu                       sync.Mutex
	nextID                   int64
	nodes                    map[string]*memNode
	children                 map[string][]string
	failListFor              map[string]error
	returnNilOnCopy          bool
	inPlaceOverwriteOnRename bool
	failNextMove             error
	failNextCopy             error
	failNextRemove           error
	failNextRename           error
	moveDelay                time.Duration
	copyDelay                time.Duration
	moveCalls                atomic.Int32
	copyCalls                atomic.Int32
	mkdirCalls               atomic.Int32
	removeCalls              atomic.Int32
	renameCalls              atomic.Int32
	listCalls                atomic.Int32
}

func newMemIDDrive(mountPath string) *memIDDrive {
	d := &memIDDrive{
		Storage: model.Storage{
			MountPath:       mountPath,
			Driver:          "memIDDrive",
			CacheExpiration: 60,
			Status:          WORK,
		},
		nodes: map[string]*memNode{
			"root": {id: "root", name: "root", isDir: true, modified: time.Now()},
		},
		children: map[string][]string{
			"root": nil,
		},
		failListFor: make(map[string]error),
	}
	return d
}

func (d *memIDDrive) Config() driver.Config {
	return driver.Config{
		Name:     "memIDDrive",
		LocalSort: true,
	}
}

func (d *memIDDrive) GetAddition() driver.Additional { return &struct{}{} }
func (d *memIDDrive) Init(ctx context.Context) error  { return nil }
func (d *memIDDrive) Drop(ctx context.Context) error  { return nil }
func (d *memIDDrive) GetRoot(ctx context.Context) (model.Obj, error) {
	return &model.Object{
		ID:       "root",
		Name:     "root",
		IsFolder: true,
	}, nil
}

func (d *memIDDrive) allocID() string {
	n := atomic.AddInt64(&d.nextID, 1)
	return fmt.Sprintf("id-%d", n)
}

func (d *memIDDrive) addChildLocked(parentID, name string, isDir bool, content []byte) *memNode {
	id := d.allocID()
	node := &memNode{
		id:       id,
		parentID: parentID,
		name:     name,
		isDir:    isDir,
		content:  append([]byte(nil), content...),
		modified: time.Now(),
	}
	d.nodes[id] = node
	d.children[parentID] = append(d.children[parentID], id)
	if isDir {
		d.children[id] = nil
	}
	return node
}

func (d *memIDDrive) SeedDir(parentID, name string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if parentID == "" {
		parentID = "root"
	}
	return d.addChildLocked(parentID, name, true, nil).id
}

func (d *memIDDrive) SeedFile(parentID, name, content string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if parentID == "" {
		parentID = "root"
	}
	return d.addChildLocked(parentID, name, false, []byte(content)).id
}

func (d *memIDDrive) ChildrenOf(parentID string) []*memNode {
	d.mu.Lock()
	defer d.mu.Unlock()
	if parentID == "" {
		parentID = "root"
	}
	ids := d.children[parentID]
	out := make([]*memNode, 0, len(ids))
	for _, id := range ids {
		if n, ok := d.nodes[id]; ok {
			cp := *n
			cp.content = append([]byte(nil), n.content...)
			out = append(out, &cp)
		}
	}
	return out
}

func (d *memIDDrive) NodeExists(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.nodes[id]
	return ok
}

func (d *memIDDrive) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	d.listCalls.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	dirID := dir.GetID()
	if dirID == "" {
		dirID = "root"
	}
	if err, fail := d.failListFor[dirID]; fail && err != nil {
		return nil, err
	}
	if _, ok := d.nodes[dirID]; !ok {
		return nil, errs.ObjectNotFound
	}
	ids := d.children[dirID]
	objs := make([]model.Obj, 0, len(ids))
	for _, id := range ids {
		n := d.nodes[id]
		objs = append(objs, &model.Object{
			ID:       n.id,
			Name:     n.name,
			Size:     int64(len(n.content)),
			Modified: n.modified,
			IsFolder: n.isDir,
		})
	}
	return objs, nil
}

func (d *memIDDrive) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotImplement
}

func (d *memIDDrive) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	d.mkdirCalls.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	parentID := parentDir.GetID()
	if parentID == "" {
		parentID = "root"
	}
	n := d.addChildLocked(parentID, dirName, true, nil)
	return &model.Object{
		ID:       n.id,
		Name:     n.name,
		Modified: n.modified,
		IsFolder: true,
	}, nil
}

func (d *memIDDrive) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	d.moveCalls.Add(1)
	if d.moveDelay > 0 {
		time.Sleep(d.moveDelay)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failNextMove != nil {
		err := d.failNextMove
		d.failNextMove = nil
		return nil, err
	}
	srcNode, ok := d.nodes[srcObj.GetID()]
	if !ok {
		return nil, errs.ObjectNotFound
	}
	dstID := dstDir.GetID()
	if dstID == "" {
		dstID = "root"
	}
	if _, ok := d.nodes[dstID]; !ok {
		return nil, errs.ObjectNotFound
	}
	// Remove from old parent slice
	oldParent := srcNode.parentID
	oldChildren := d.children[oldParent]
	filtered := oldChildren[:0]
	for _, cid := range oldChildren {
		if cid != srcNode.id {
			filtered = append(filtered, cid)
		}
	}
	d.children[oldParent] = filtered
	// Attach to dst parent WITHOUT deduplicating by name (matches Google Drive).
	srcNode.parentID = dstID
	srcNode.modified = time.Now()
	d.children[dstID] = append(d.children[dstID], srcNode.id)
	return &model.Object{
		ID:       srcNode.id,
		Name:     srcNode.name,
		Size:     int64(len(srcNode.content)),
		Modified: srcNode.modified,
		IsFolder: srcNode.isDir,
	}, nil
}

func (d *memIDDrive) copySubtreeLocked(srcID, dstParentID string) *memNode {
	src := d.nodes[srcID]
	cloned := d.addChildLocked(dstParentID, src.name, src.isDir, src.content)
	if src.isDir {
		for _, childID := range d.children[srcID] {
			d.copySubtreeLocked(childID, cloned.id)
		}
	}
	return cloned
}

func (d *memIDDrive) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	d.copyCalls.Add(1)
	if d.copyDelay > 0 {
		time.Sleep(d.copyDelay)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failNextCopy != nil {
		err := d.failNextCopy
		d.failNextCopy = nil
		return nil, err
	}
	srcNode, ok := d.nodes[srcObj.GetID()]
	if !ok {
		return nil, errs.ObjectNotFound
	}
	dstID := dstDir.GetID()
	if dstID == "" {
		dstID = "root"
	}
	if _, ok := d.nodes[dstID]; !ok {
		return nil, errs.ObjectNotFound
	}
	cloned := d.copySubtreeLocked(srcNode.id, dstID)
	if d.returnNilOnCopy {
		return nil, nil
	}
	return &model.Object{
		ID:       cloned.id,
		Name:     cloned.name,
		Size:     int64(len(cloned.content)),
		Modified: cloned.modified,
		IsFolder: cloned.isDir,
	}, nil
}

func (d *memIDDrive) removeSubtreeLocked(id string) {
	if node, ok := d.nodes[id]; ok && node.isDir {
		for _, cid := range append([]string(nil), d.children[id]...) {
			d.removeSubtreeLocked(cid)
		}
		delete(d.children, id)
	}
	delete(d.nodes, id)
}

func (d *memIDDrive) Remove(ctx context.Context, obj model.Obj) error {
	d.removeCalls.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failNextRemove != nil {
		err := d.failNextRemove
		d.failNextRemove = nil
		return err
	}
	n, ok := d.nodes[obj.GetID()]
	if !ok {
		return errs.ObjectNotFound
	}
	parentChildren := d.children[n.parentID]
	filtered := parentChildren[:0]
	for _, cid := range parentChildren {
		if cid != n.id {
			filtered = append(filtered, cid)
		}
	}
	d.children[n.parentID] = filtered
	d.removeSubtreeLocked(n.id)
	return nil
}

func (d *memIDDrive) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	d.renameCalls.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failNextRename != nil {
		err := d.failNextRename
		d.failNextRename = nil
		return err
	}
	n, ok := d.nodes[srcObj.GetID()]
	if !ok {
		return errs.ObjectNotFound
	}
	if d.inPlaceOverwriteOnRename {
		siblings := d.children[n.parentID]
		filtered := make([]string, 0, len(siblings))
		for _, cid := range siblings {
			if cid != n.id && d.nodes[cid] != nil && d.nodes[cid].name == newName {
				d.removeSubtreeLocked(cid)
				continue
			}
			filtered = append(filtered, cid)
		}
		d.children[n.parentID] = filtered
	}
	n.name = newName
	n.modified = time.Now()
	return nil
}

func (d *memIDDrive) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	dstID := dstDir.GetID()
	if dstID == "" {
		dstID = "root"
	}
	n := d.addChildLocked(dstID, file.GetName(), false, data)
	return &model.Object{
		ID:       n.id,
		Name:     n.name,
		Size:     int64(len(n.content)),
		Modified: n.modified,
		IsFolder: false,
	}, nil
}

func setupOpTransferEnv(t *testing.T, mountPath string) *memIDDrive {
	t.Helper()
	conf.Conf = conf.DefaultConfig(t.TempDir())
	conf.Conf.Database.Type = "sqlite3"
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}
	db.Init(database)
	metaCache.Clear()
	metaSnapshotMu.Lock()
	metaSnapshot = nil
	metaSnapshotLoaded = false
	metaSnapshotMu.Unlock()

	drv := newMemIDDrive(mountPath)
	storagesMap.Store(mountPath, drv)
	t.Cleanup(func() {
		storagesMap.Delete(mountPath)
		Cache.DeleteDirectoryTree(drv, "/")
		metaCache.Clear()
		metaSnapshotMu.Lock()
		metaSnapshot = nil
		metaSnapshotLoaded = false
		metaSnapshotMu.Unlock()
		db.Close()
	})
	return drv
}

func TestMoveWithOptions_NoOverwriteConflict_PreventsDuplicates(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")
	srcDir := drv.SeedDir("root", "src")
	dstDir := drv.SeedDir("root", "dst")
	srcFileID := drv.SeedFile(srcDir, "report.pdf", "new-version")
	dstFileID := drv.SeedFile(dstDir, "report.pdf", "old-version")

	err := MoveWithOptions(ctx, drv, "/src/report.pdf", "/dst", TransferOptions{Overwrite: false})
	if !errs.IsObjectAlreadyExists(err) {
		t.Fatalf("expected ObjectAlreadyExists error, got: %v", err)
	}
	if drv.moveCalls.Load() != 0 || drv.removeCalls.Load() != 0 {
		t.Fatalf("expected zero mutations on conflict, got move=%d remove=%d", drv.moveCalls.Load(), drv.removeCalls.Load())
	}
	if !drv.NodeExists(srcFileID) || !drv.NodeExists(dstFileID) {
		t.Fatalf("both source and destination files must remain intact")
	}
	if children := drv.ChildrenOf(dstDir); len(children) != 1 || string(children[0].content) != "old-version" {
		t.Fatalf("unexpected dst children: %+v", children)
	}
}

func TestMoveWithOptions_OverwriteFile_ReplacesAndRemovesHistoricalDuplicates(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")
	srcDir := drv.SeedDir("root", "src")
	dstDir := drv.SeedDir("root", "dst")
	srcFileID := drv.SeedFile(srcDir, "photo.jpg", "fresh")
	dup1 := drv.SeedFile(dstDir, "photo.jpg", "dup-1")
	dup2 := drv.SeedFile(dstDir, "photo.jpg", "dup-2")
	keepID := drv.SeedFile(dstDir, "keep.jpg", "keep")

	// Pre-warm cache to verify overwrite also cleans up cached views.
	if _, err := List(ctx, drv, "/dst", model.ListArgs{}); err != nil {
		t.Fatal(err)
	}

	err := MoveWithOptions(ctx, drv, "/src/photo.jpg", "/dst", TransferOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("MoveWithOptions overwrite failed: %v", err)
	}
	if drv.NodeExists(dup1) || drv.NodeExists(dup2) {
		t.Fatalf("historical duplicate IDs (%s, %s) were not removed", dup1, dup2)
	}
	if !drv.NodeExists(srcFileID) || !drv.NodeExists(keepID) {
		t.Fatalf("moved file or unrelated sibling missing")
	}
	if len(drv.ChildrenOf(srcDir)) != 0 {
		t.Fatalf("src dir should be empty, got %+v", drv.ChildrenOf(srcDir))
	}
	dstChildren := drv.ChildrenOf(dstDir)
	if len(dstChildren) != 2 {
		t.Fatalf("expected 2 items in dst, got %d", len(dstChildren))
	}
	cachedDst, err := List(ctx, drv, "/dst", model.ListArgs{})
	if err != nil {
		t.Fatal(err)
	}
	photoCount := 0
	for _, obj := range cachedDst {
		if obj.GetName() == "photo.jpg" {
			photoCount++
			if obj.GetID() != srcFileID {
				t.Fatalf("cached photo.jpg id = %s, want %s", obj.GetID(), srcFileID)
			}
		}
	}
	if photoCount != 1 {
		t.Fatalf("expected 1 cached photo.jpg after overwrite, got %d", photoCount)
	}
}

func TestMoveWithOptions_OverwriteFailurePreservesDestination(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")
	srcDir := drv.SeedDir("root", "src")
	dstDir := drv.SeedDir("root", "dst")
	srcFileID := drv.SeedFile(srcDir, "doc.txt", "new")
	dstFileID := drv.SeedFile(dstDir, "doc.txt", "existing")
	drv.failNextMove = errors.New("simulated remote move failure")

	err := MoveWithOptions(ctx, drv, "/src/doc.txt", "/dst", TransferOptions{Overwrite: true})
	if err == nil || !strings.Contains(err.Error(), "simulated remote move failure") {
		t.Fatalf("expected simulated remote move failure, got: %v", err)
	}
	if !drv.NodeExists(dstFileID) {
		t.Fatalf("destination file %s must NOT be deleted when move fails", dstFileID)
	}
	if !drv.NodeExists(srcFileID) {
		t.Fatalf("source file %s must still exist when move fails", srcFileID)
	}
}

func TestMoveWithOptions_CrossTypeAndSelfGuards(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")
	srcDir := drv.SeedDir("root", "src")
	dstDir := drv.SeedDir("root", "dst")
	fileID := drv.SeedFile(srcDir, "mix", "file-data")
	drv.SeedDir(dstDir, "mix")
	subDirID := drv.SeedDir(srcDir, "folder")
	drv.SeedFile(dstDir, "folder", "existing-file")

	// File overwriting folder -> reject
	if err := MoveWithOptions(ctx, drv, "/src/mix", "/dst", TransferOptions{Overwrite: true}); err == nil || !strings.Contains(err.Error(), "cannot overwrite folder with file") {
		t.Fatalf("expected cannot overwrite folder with file error, got: %v", err)
	}
	if !drv.NodeExists(fileID) {
		t.Fatalf("source file deleted on cross-type rejection")
	}

	// Folder overwriting file -> reject
	if err := MoveWithOptions(ctx, drv, "/src/folder", "/dst", TransferOptions{Overwrite: true}); err == nil || !strings.Contains(err.Error(), "cannot overwrite file with folder") {
		t.Fatalf("expected cannot overwrite file with folder error, got: %v", err)
	}
	if !drv.NodeExists(subDirID) {
		t.Fatalf("source folder deleted on cross-type rejection")
	}

	// Same-dir move even with Overwrite: true -> reject
	if err := MoveWithOptions(ctx, drv, "/src/mix", "/src", TransferOptions{Overwrite: true}); err == nil {
		t.Fatalf("expected error on same-dir overwrite move")
	}
	if !drv.NodeExists(fileID) {
		t.Fatalf("source file deleted on same-dir move rejection")
	}

	// Move folder into itself / descendant -> reject
	nestedDir := drv.SeedDir(subDirID, "child")
	_ = nestedDir
	if err := MoveWithOptions(ctx, drv, "/src/folder", "/src/folder/child", TransferOptions{Overwrite: true}); err == nil {
		t.Fatalf("expected error moving folder into its own descendant")
	}
}

func TestMoveWithOptions_FolderMergeOverwriteAndRollbackSafety(t *testing.T) {
	ctx := context.Background()

	t.Run("merge_preserves_uncollided_and_removes_empty_src", func(t *testing.T) {
		drv := setupOpTransferEnv(t, "/")
		srcRoot := drv.SeedDir("root", "src")
		dstRoot := drv.SeedDir("root", "dst")

		srcAlbum := drv.SeedDir(srcRoot, "album")
		newPhotoID := drv.SeedFile(srcAlbum, "a.jpg", "new-a")
		extraID := drv.SeedFile(srcAlbum, "extra.jpg", "extra")
		srcSub := drv.SeedDir(srcAlbum, "sub")
		nestedID := drv.SeedFile(srcSub, "nested.txt", "nested")

		dstAlbum := drv.SeedDir(dstRoot, "album")
		oldPhotoID := drv.SeedFile(dstAlbum, "a.jpg", "old-a")
		keepID := drv.SeedFile(dstAlbum, "keep.jpg", "keep")
		otherDir := drv.SeedDir(dstAlbum, "otherdir")
		otherChild := drv.SeedFile(otherDir, "c.txt", "c")

		if err := MoveWithOptions(ctx, drv, "/src/album", "/dst", TransferOptions{Overwrite: true}); err != nil {
			t.Fatalf("merge move failed: %v", err)
		}
		if drv.NodeExists(srcAlbum) {
			t.Fatalf("expected empty srcAlbum %s to be removed after merge", srcAlbum)
		}
		if drv.NodeExists(oldPhotoID) {
			t.Fatalf("expected overwritten a.jpg %s to be deleted", oldPhotoID)
		}
		for _, id := range []string{newPhotoID, extraID, srcSub, nestedID, keepID, otherDir, otherChild} {
			if !drv.NodeExists(id) {
				t.Fatalf("expected node %s to remain present", id)
			}
		}
		objs, err := List(ctx, drv, "/dst/album", model.ListArgs{})
		if err != nil {
			t.Fatal(err)
		}
		names := make(map[string]int)
		for _, o := range objs {
			names[o.GetName()]++
		}
		for _, want := range []string{"a.jpg", "extra.jpg", "sub", "keep.jpg", "otherdir"} {
			if names[want] != 1 {
				t.Fatalf("unexpected count for %q in /dst/album: %v", want, names)
			}
		}
	})

	t.Run("nested_type_mismatch_preserves_src_folder", func(t *testing.T) {
		drv := setupOpTransferEnv(t, "/")
		srcRoot := drv.SeedDir("root", "src")
		dstRoot := drv.SeedDir("root", "dst")
		srcAlbum := drv.SeedDir(srcRoot, "album")
		srcConflictFile := drv.SeedFile(srcAlbum, "item", "file")
		dstAlbum := drv.SeedDir(dstRoot, "album")
		drv.SeedDir(dstAlbum, "item") // dst has folder named "item"

		err := MoveWithOptions(ctx, drv, "/src/album", "/dst", TransferOptions{Overwrite: true})
		if err == nil || !strings.Contains(err.Error(), "cannot overwrite folder with file") {
			t.Fatalf("expected cannot overwrite folder with file on nested conflict, got: %v", err)
		}
		if !drv.NodeExists(srcAlbum) || !drv.NodeExists(srcConflictFile) {
			t.Fatalf("src folder and unmoved conflicting child must remain intact on merge error")
		}
	})
}

func TestCopyWithOptions_OverwriteAndSameDirAndNilObj(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")
	srcDir := drv.SeedDir("root", "src")
	dstDir := drv.SeedDir("root", "dst")
	drv.SeedFile(srcDir, "note.txt", "new-note")
	oldDstID := drv.SeedFile(dstDir, "note.txt", "old-note")

	// No overwrite -> fail
	err := CopyWithOptions(ctx, drv, "/src/note.txt", "/dst", TransferOptions{Overwrite: false})
	if !errs.IsObjectAlreadyExists(err) {
		t.Fatalf("expected ObjectAlreadyExists, got %v", err)
	}
	if drv.copyCalls.Load() != 0 {
		t.Fatalf("expected zero copy calls on conflict")
	}

	// Same-dir copy -> explicit error (not errs.NotImplement!)
	err = CopyWithOptions(ctx, drv, "/src/note.txt", "/src", TransferOptions{Overwrite: true})
	if err == nil || errs.IsNotImplementError(err) {
		t.Fatalf("expected explicit non-NotImplement error for same-dir copy, got: %v", err)
	}

	// Overwrite with driver returning (nil, nil) on Copy -> deletes old & refreshes dst cache cleanly.
	drv.returnNilOnCopy = true
	if _, err := List(ctx, drv, "/dst", model.ListArgs{}); err != nil {
		t.Fatal(err)
	}
	if err := CopyWithOptions(ctx, drv, "/src/note.txt", "/dst", TransferOptions{Overwrite: true}); err != nil {
		t.Fatalf("CopyWithOptions overwrite failed: %v", err)
	}
	if drv.NodeExists(oldDstID) {
		t.Fatalf("old destination file %s should have been removed", oldDstID)
	}
	if len(drv.ChildrenOf("/src")) == 0 && len(drv.ChildrenOf(srcDir)) != 1 {
		t.Fatalf("source file should still exist after copy")
	}
	dstChildren := drv.ChildrenOf(dstDir)
	if len(dstChildren) != 1 || string(dstChildren[0].content) != "new-note" {
		t.Fatalf("expected single overwritten note.txt in dst, got %+v", dstChildren)
	}
	cachedDst, err := List(ctx, drv, "/dst", model.ListArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cachedDst) != 1 || cachedDst[0].GetID() != dstChildren[0].id {
		t.Fatalf("cached dst entries should match single new object: %+v", cachedDst)
	}
}

func TestMakeDir_StaleCachePreventsDuplicateFolder(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")

	// Warm root cache while "/" is empty.
	if objs, err := List(ctx, drv, "/", model.ListArgs{}); err != nil || len(objs) != 0 {
		t.Fatalf("unexpected initial list: len=%d err=%v", len(objs), err)
	}
	// Bypass cache: remotely create "Album".
	remoteID := drv.SeedDir("root", "Album")

	// Call MakeDir -> should refresh stale cache, notice Album already exists, and NOT call driver.MakeDir.
	if err := MakeDir(ctx, drv, "/Album"); err != nil {
		t.Fatalf("MakeDir on existing remote folder returned error: %v", err)
	}
	if drv.mkdirCalls.Load() != 0 {
		t.Fatalf("expected 0 driver MakeDir calls when stale cache missed existing folder, got %d", drv.mkdirCalls.Load())
	}
	children := drv.ChildrenOf("root")
	if len(children) != 1 || children[0].id != remoteID {
		t.Fatalf("expected single folder %s, got %+v", remoteID, children)
	}
}

func TestTransferWithOptions_ConcurrentOverwrite_Race(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")
	drv.copyDelay = 15 * time.Millisecond
	src1 := drv.SeedDir("root", "src1")
	src2 := drv.SeedDir("root", "src2")
	dst := drv.SeedDir("root", "dst")
	drv.SeedFile(src1, "same.txt", "from-1")
	drv.SeedFile(src2, "same.txt", "from-2")
	drv.SeedFile(dst, "same.txt", "initial")

	var wg sync.WaitGroup
	errsCh := make(chan error, 2)
	for _, srcPath := range []string{"/src1/same.txt", "/src2/same.txt"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			errsCh <- CopyWithOptions(ctx, drv, p, "/dst", TransferOptions{Overwrite: true})
		}(srcPath)
	}
	wg.Wait()
	close(errsCh)
	for err := range errsCh {
		if err != nil {
			t.Fatalf("concurrent CopyWithOptions overwrite failed: %v", err)
		}
	}
	children := drv.ChildrenOf(dst)
	if len(children) != 1 || children[0].name != "same.txt" {
		t.Fatalf("expected strictly 1 same.txt in dst after concurrent overwrite, got %+v", children)
	}
}

func TestRenameWithOptions_ConflictsAndOverwrite(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")
	dir := drv.SeedDir("root", "dir")
	subFolderID := drv.SeedDir(dir, "SubFolder")
	srcID := drv.SeedFile(dir, "a.txt", "new-content")
	dstID1 := drv.SeedFile(dir, "b.txt", "old-content-1")

	// Warm dirCache BEFORE adding another historical duplicate bypassing cache.
	if objs, err := List(ctx, drv, "/dir", model.ListArgs{}); err != nil || len(objs) != 3 {
		t.Fatalf("unexpected initial list: len=%d err=%v", len(objs), err)
	}
	dstID2 := drv.SeedFile(dir, "b.txt", "old-content-2")

	// 1. Non-overwrite rename with conflict -> errs.ObjectAlreadyExists, 0 driver Rename calls, both preserved.
	err := RenameWithOptions(ctx, drv, "/dir/a.txt", "b.txt", TransferOptions{Overwrite: false})
	if !errs.IsObjectAlreadyExists(err) {
		t.Fatalf("expected ObjectAlreadyExists for conflicting rename, got %v", err)
	}
	if drv.renameCalls.Load() != 0 {
		t.Fatalf("expected 0 driver Rename calls on conflict, got %d", drv.renameCalls.Load())
	}

	// 2. Cross-type conflicts rejected regardless of Overwrite flag.
	for _, ov := range []bool{false, true} {
		err = RenameWithOptions(ctx, drv, "/dir/a.txt", "SubFolder", TransferOptions{Overwrite: ov})
		if err == nil || !strings.Contains(err.Error(), "cannot overwrite folder with file") {
			t.Fatalf("expected file->folder rejection (ov=%v), got %v", ov, err)
		}
		err = RenameWithOptions(ctx, drv, "/dir/SubFolder", "b.txt", TransferOptions{Overwrite: ov})
		if err == nil || !strings.Contains(err.Error(), "cannot overwrite file with folder") {
			t.Fatalf("expected folder->file rejection (ov=%v), got %v", ov, err)
		}
	}
	if drv.renameCalls.Load() != 0 || !drv.NodeExists(subFolderID) {
		t.Fatalf("cross-type rename must not call driver Rename or remove SubFolder")
	}

	// 3. Failed driver Rename when Overwrite=true preserves old targets intact.
	drv.failNextRename = errors.New("simulated rename failure")
	err = RenameWithOptions(ctx, drv, "/dir/a.txt", "b.txt", TransferOptions{Overwrite: true})
	if err == nil || drv.removeCalls.Load() != 0 || !drv.NodeExists(dstID1) || !drv.NodeExists(dstID2) {
		t.Fatalf("failed Rename must preserve all old conflict objects: err=%v removes=%d", err, drv.removeCalls.Load())
	}

	// 4. Overwrite=true renames src first, deletes BOTH old duplicate IDs, and keeps cache duplicate-free.
	if err := RenameWithOptions(ctx, drv, "/dir/a.txt", "b.txt", TransferOptions{Overwrite: true}); err != nil {
		t.Fatalf("RenameWithOptions overwrite failed: %v", err)
	}
	if drv.NodeExists(dstID1) || drv.NodeExists(dstID2) {
		t.Fatalf("old conflicting b.txt IDs (%s, %s) should have been removed", dstID1, dstID2)
	}
	children := drv.ChildrenOf(dir)
	var bFiles []*memNode
	for _, c := range children {
		if c.name == "b.txt" {
			bFiles = append(bFiles, c)
		}
		if c.name == "a.txt" {
			t.Fatalf("a.txt should no longer exist after rename")
		}
	}
	if len(bFiles) != 1 || bFiles[0].id != srcID || string(bFiles[0].content) != "new-content" {
		t.Fatalf("expected single b.txt with id %s and new-content, got %+v", srcID, bFiles)
	}
	cached, err := List(ctx, drv, "/dir", model.ListArgs{})
	if err != nil {
		t.Fatal(err)
	}
	bCacheCount := 0
	for _, obj := range cached {
		if obj.GetName() == "b.txt" {
			bCacheCount++
		}
		if obj.GetName() == "a.txt" {
			t.Fatalf("dirCache still contains stale a.txt: %+v", cached)
		}
	}
	if bCacheCount != 1 {
		t.Fatalf("expected single b.txt in dirCache, got %d: %+v", bCacheCount, cached)
	}

	// 5. Same-name rename succeeds without error or deleting target.
	if err := RenameWithOptions(ctx, drv, "/dir/b.txt", "b.txt", TransferOptions{Overwrite: false}); err != nil {
		t.Fatalf("same-name rename should succeed, got %v", err)
	}
	if !drv.NodeExists(srcID) {
		t.Fatalf("same-name rename must keep source node %s", srcID)
	}

	// 6. List error propagates rather than silently proceeding.
	drv.failListFor[dir] = errors.New("list failure")
	if err := RenameWithOptions(ctx, drv, "/dir/b.txt", "c.txt", TransferOptions{Overwrite: false}); err == nil {
		t.Fatalf("expected error when List fails")
	}
}

func TestRenameWithOptions_InPlaceOverwriteDoesNotDeleteTarget(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")
	drv.inPlaceOverwriteOnRename = true
	srcID := drv.SeedFile("root", "a.txt", "new-in-place")
	_ = drv.SeedFile("root", "b.txt", "old-in-place")

	if err := RenameWithOptions(ctx, drv, "/a.txt", "b.txt", TransferOptions{Overwrite: true}); err != nil {
		t.Fatalf("in-place RenameWithOptions overwrite failed: %v", err)
	}
	if drv.removeCalls.Load() != 0 {
		t.Fatalf("expected 0 extra Remove calls when driver replaced destination in-place, got %d", drv.removeCalls.Load())
	}
	children := drv.ChildrenOf("root")
	if len(children) != 1 || children[0].id != srcID || children[0].name != "b.txt" || string(children[0].content) != "new-in-place" {
		t.Fatalf("in-place renamed target was lost or corrupted: %+v", children)
	}
}

func TestRemove_StaleCacheFindsRemoteObject(t *testing.T) {
	ctx := context.Background()
	drv := setupOpTransferEnv(t, "/")

	// Warm root cache while "/" is empty.
	if objs, err := List(ctx, drv, "/", model.ListArgs{}); err != nil || len(objs) != 0 {
		t.Fatalf("unexpected initial list: len=%d err=%v", len(objs), err)
	}
	// Seed remote-only file bypassing cache.
	remoteID := drv.SeedFile("root", "remote-only.txt", "data")

	if err := Remove(ctx, drv, "/remote-only.txt"); err != nil {
		t.Fatalf("Remove on stale-cache object failed: %v", err)
	}
	if drv.NodeExists(remoteID) || drv.removeCalls.Load() != 1 {
		t.Fatalf("expected remote object %s removed once, exists=%v calls=%d", remoteID, drv.NodeExists(remoteID), drv.removeCalls.Load())
	}
}
