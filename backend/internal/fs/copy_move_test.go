package fs

import (
	"bytes"
	"context"
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
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/tache"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fsTestNode struct {
	id       string
	parentID string
	name     string
	isDir    bool
	content  []byte
	modified time.Time
}

// fsMemDrive models an ID-keyed remote drive (like Google Drive).
type fsMemDrive struct {
	model.Storage
	driverName string
	mu         sync.Mutex
	nextID     int64
	nodes      map[string]*fsTestNode
	children   map[string][]string
}

// fsNoCopyDrive omits driver.Copy/CopyResult so op.Copy returns NotImplement
// and triggers copyNamedInStorage fallback.
type fsNoCopyDrive struct {
	*fsMemDrive
}

func (d *fsNoCopyDrive) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	return d.fsMemDrive.moveImpl(ctx, srcObj, dstDir)
}

func (d *fsNoCopyDrive) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	return d.fsMemDrive.makeDirImpl(ctx, parentDir, dirName)
}

func (d *fsNoCopyDrive) Remove(ctx context.Context, obj model.Obj) error {
	return d.fsMemDrive.removeImpl(ctx, obj)
}

func (d *fsNoCopyDrive) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	return d.fsMemDrive.putImpl(ctx, dstDir, file, up)
}

func newFSMemDrive(driverName string) *fsMemDrive {
	return &fsMemDrive{
		driverName: driverName,
		nodes: map[string]*fsTestNode{
			"root": {id: "root", name: "root", isDir: true, modified: time.Now()},
		},
		children: map[string][]string{"root": nil},
	}
}

func (d *fsMemDrive) Config() driver.Config {
	return driver.Config{Name: d.driverName, LocalSort: true}
}

func (d *fsMemDrive) GetAddition() driver.Additional { return &struct{}{} }
func (d *fsMemDrive) Init(ctx context.Context) error  { return nil }
func (d *fsMemDrive) Drop(ctx context.Context) error  { return nil }
func (d *fsMemDrive) GetRoot(ctx context.Context) (model.Obj, error) {
	return &model.Object{ID: "root", Name: "root", IsFolder: true}, nil
}

func (d *fsMemDrive) allocID() string {
	n := atomic.AddInt64(&d.nextID, 1)
	return fmt.Sprintf("%s-id-%d", d.driverName, n)
}

func (d *fsMemDrive) addChildLocked(parentID, name string, isDir bool, content []byte) *fsTestNode {
	id := d.allocID()
	node := &fsTestNode{
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

func (d *fsMemDrive) SeedDir(parentID, name string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if parentID == "" {
		parentID = "root"
	}
	return d.addChildLocked(parentID, name, true, nil).id
}

func (d *fsMemDrive) SeedFile(parentID, name, content string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if parentID == "" {
		parentID = "root"
	}
	return d.addChildLocked(parentID, name, false, []byte(content)).id
}

func (d *fsMemDrive) ChildrenOf(parentID string) []*fsTestNode {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := d.children[parentID]
	out := make([]*fsTestNode, 0, len(ids))
	for _, id := range ids {
		if n, ok := d.nodes[id]; ok {
			cp := *n
			cp.content = append([]byte(nil), n.content...)
			out = append(out, &cp)
		}
	}
	return out
}

func (d *fsMemDrive) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dirID := dir.GetID()
	if dirID == "" {
		dirID = "root"
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

type memRangeReader struct {
	data []byte
}

func (r *memRangeReader) RangeRead(ctx context.Context, rng http_range.Range) (io.ReadCloser, error) {
	start := rng.Start
	if start < 0 {
		start = 0
	}
	if start > int64(len(r.data)) {
		start = int64(len(r.data))
	}
	end := int64(len(r.data))
	if rng.Length > 0 && start+rng.Length < end {
		end = start + rng.Length
	}
	return io.NopCloser(bytes.NewReader(r.data[start:end])), nil
}

func (d *fsMemDrive) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	d.mu.Lock()
	n, ok := d.nodes[file.GetID()]
	var data []byte
	if ok {
		data = append([]byte(nil), n.content...)
	}
	d.mu.Unlock()
	if !ok {
		return nil, errs.ObjectNotFound
	}
	return &model.Link{
		RangeReader:   &memRangeReader{data: data},
		ContentLength: int64(len(data)),
	}, nil
}

func (d *fsMemDrive) makeDirImpl(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.addChildLocked(parentDir.GetID(), dirName, true, nil)
	return &model.Object{ID: n.id, Name: n.name, Modified: n.modified, IsFolder: true}, nil
}

func (d *fsMemDrive) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	return d.makeDirImpl(ctx, parentDir, dirName)
}

func (d *fsMemDrive) moveImpl(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, ok := d.nodes[srcObj.GetID()]
	if !ok {
		return nil, errs.ObjectNotFound
	}
	oldParent := n.parentID
	oldChildren := d.children[oldParent]
	filtered := oldChildren[:0]
	for _, cid := range oldChildren {
		if cid != n.id {
			filtered = append(filtered, cid)
		}
	}
	d.children[oldParent] = filtered
	n.parentID = dstDir.GetID()
	n.modified = time.Now()
	d.children[n.parentID] = append(d.children[n.parentID], n.id)
	return &model.Object{ID: n.id, Name: n.name, Size: int64(len(n.content)), Modified: n.modified, IsFolder: n.isDir}, nil
}

func (d *fsMemDrive) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	return d.moveImpl(ctx, srcObj, dstDir)
}

func (d *fsMemDrive) copySubtreeLocked(srcID, dstParentID string) *fsTestNode {
	src := d.nodes[srcID]
	cloned := d.addChildLocked(dstParentID, src.name, src.isDir, src.content)
	if src.isDir {
		for _, childID := range d.children[srcID] {
			d.copySubtreeLocked(childID, cloned.id)
		}
	}
	return cloned
}

func (d *fsMemDrive) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.nodes[srcObj.GetID()]; !ok {
		return nil, errs.ObjectNotFound
	}
	cloned := d.copySubtreeLocked(srcObj.GetID(), dstDir.GetID())
	return &model.Object{ID: cloned.id, Name: cloned.name, Size: int64(len(cloned.content)), Modified: cloned.modified, IsFolder: cloned.isDir}, nil
}

func (d *fsMemDrive) removeSubtreeLocked(id string) {
	if node, ok := d.nodes[id]; ok && node.isDir {
		for _, cid := range append([]string(nil), d.children[id]...) {
			d.removeSubtreeLocked(cid)
		}
		delete(d.children, id)
	}
	delete(d.nodes, id)
}

func (d *fsMemDrive) removeImpl(ctx context.Context, obj model.Obj) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, ok := d.nodes[obj.GetID()]
	if !ok {
		return errs.ObjectNotFound
	}
	oldChildren := d.children[n.parentID]
	filtered := oldChildren[:0]
	for _, cid := range oldChildren {
		if cid != n.id {
			filtered = append(filtered, cid)
		}
	}
	d.children[n.parentID] = filtered
	d.removeSubtreeLocked(n.id)
	return nil
}

func (d *fsMemDrive) Remove(ctx context.Context, obj model.Obj) error {
	return d.removeImpl(ctx, obj)
}

func (d *fsMemDrive) putImpl(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if up != nil {
		up(100)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.addChildLocked(dstDir.GetID(), file.GetName(), false, data)
	return &model.Object{ID: n.id, Name: n.name, Size: int64(len(n.content)), Modified: n.modified}, nil
}

func (d *fsMemDrive) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	return d.putImpl(ctx, dstDir, file, up)
}

type fsTestEnv struct {
	storageIDs []uint
}

func setupFSTestEnv(t *testing.T) *fsTestEnv {
	t.Helper()
	conf.Conf = conf.DefaultConfig(t.TempDir())
	conf.Conf.Database.Type = "sqlite3"
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}
	db.Init(database)

	CopyTaskManager = tache.NewManager[*FileTransferTask](tache.WithWorks(0))
	MoveTaskManager = tache.NewManager[*FileTransferTask](tache.WithWorks(0))
	env := &fsTestEnv{}
	t.Cleanup(func() {
		for _, id := range env.storageIDs {
			_ = op.DeleteStorageById(context.Background(), id)
		}
		db.Close()
	})
	return env
}

func (e *fsTestEnv) mountDrive(t *testing.T, mountPath string, drv driver.Driver) {
	t.Helper()
	op.RegisterDriver(func() driver.Driver { return drv })
	id, err := op.CreateStorage(context.Background(), model.Storage{
		MountPath:       mountPath,
		Driver:          drv.Config().Name,
		Addition:        "{}",
		CacheExpiration: 60,
		Status:          op.WORK,
	})
	if err != nil {
		t.Fatalf("create storage %s: %v", mountPath, err)
	}
	e.storageIDs = append(e.storageIDs, id)
}

func waitManagerIdle(t *testing.T, mgr *tache.Manager[*FileTransferTask]) []*FileTransferTask {
	t.Helper()
	queue := append([]*FileTransferTask(nil), mgr.GetAll()...)
	executed := make([]*FileTransferTask, 0, len(queue))
	for len(queue) > 0 {
		tk := queue[0]
		queue = queue[1:]
		if tk.Ctx() == nil {
			tk.SetCtx(context.Background())
		}
		err := tk.RunWithNextTaskCallback(func(next *FileTransferTask) error {
			queue = append(queue, next)
			return nil
		})
		if err != nil {
			tk.SetErr(err)
			tk.SetState(tache.StateFailed)
		} else {
			tk.SetState(tache.StateSucceeded)
		}
		executed = append(executed, tk)
	}
	return executed
}

func TestFileTransfer_OverwritePropagatesToChildTasksAndCrossStorage(t *testing.T) {
	ctx := context.Background()
	env := setupFSTestEnv(t)

	drvA := newFSMemDrive("fsMemDriveA-" + t.Name())
	drvB := newFSMemDrive("fsMemDriveB-" + t.Name())
	env.mountDrive(t, "/drvA1", drvA)
	env.mountDrive(t, "/drvB1", drvB)

	srcFolder := drvA.SeedDir("root", "folder")
	drvA.SeedFile(srcFolder, "dup.txt", "new-dup")
	drvA.SeedFile(srcFolder, "only.txt", "only-src")
	srcNested := drvA.SeedDir(srcFolder, "nested")
	drvA.SeedFile(srcNested, "sub.txt", "sub-src")

	dstFolder := drvB.SeedDir("root", "folder")
	drvB.SeedFile(dstFolder, "dup.txt", "old-dup")
	drvB.SeedFile(dstFolder, "keep.txt", "keep-dst")

	// Step 1: Overwrite=false -> dup.txt child task must fail, leaving "old-dup" untouched and non-duplicated.
	if _, err := CopyWithOptions(ctx, "/drvA1/folder", "/drvB1", TransferOptions{Overwrite: false}); err != nil {
		t.Fatalf("enqueue non-overwrite copy failed: %v", err)
	}
	tasks := waitManagerIdle(t, CopyTaskManager)
	dupFailed := false
	for _, tk := range tasks {
		if strings.Contains(tk.GetName(), "dup.txt") && tk.GetState() == tache.StateFailed {
			dupFailed = true
		}
	}
	if !dupFailed {
		t.Fatalf("expected child dup.txt copy task to fail when Overwrite=false")
	}
	var dupCount int
	var dupContent string
	for _, c := range drvB.ChildrenOf(dstFolder) {
		if c.name == "dup.txt" {
			dupCount++
			dupContent = string(c.content)
		}
	}
	if dupCount != 1 || dupContent != "old-dup" {
		t.Fatalf("expected 1 dup.txt with old-dup when Overwrite=false, got count=%d content=%q", dupCount, dupContent)
	}

	// Reset task manager and Step 2: Overwrite=true -> child tasks inherit Overwrite=true and replace old-dup.
	CopyTaskManager.RemoveAll()
	if _, err := CopyWithOptions(ctx, "/drvA1/folder", "/drvB1", TransferOptions{Overwrite: true}); err != nil {
		t.Fatalf("enqueue overwrite copy failed: %v", err)
	}
	tasks = waitManagerIdle(t, CopyTaskManager)
	for _, tk := range tasks {
		if tk.GetState() != tache.StateSucceeded {
			t.Fatalf("expected all overwrite copy tasks to succeed, task %q state=%v err=%v", tk.GetName(), tk.GetState(), tk.GetErr())
		}
	}
	dupCount = 0
	keepFound := false
	onlyFound := false
	for _, c := range drvB.ChildrenOf(dstFolder) {
		switch c.name {
		case "dup.txt":
			dupCount++
			dupContent = string(c.content)
		case "keep.txt":
			keepFound = true
		case "only.txt":
			onlyFound = true
		}
	}
	if dupCount != 1 || dupContent != "new-dup" || !keepFound || !onlyFound {
		t.Fatalf("unexpected dstFolder state: dupCount=%d dupContent=%q keepFound=%v onlyFound=%v", dupCount, dupContent, keepFound, onlyFound)
	}
}

func TestFileTransfer_SameStorageFallbackCopyNamedOverwrite(t *testing.T) {
	ctx := context.Background()
	env := setupFSTestEnv(t)

	inner := newFSMemDrive("fsNoCopyDrive-" + t.Name())
	drv := &fsNoCopyDrive{fsMemDrive: inner}
	env.mountDrive(t, "/nocopy1", drv)

	srcDir := inner.SeedDir("root", "src")
	dstDir := inner.SeedDir("root", "dst")
	inner.SeedFile(srcDir, "file.bin", "new-bytes")
	inner.SeedFile(dstDir, "file.bin", "old-bytes")

	// Non-overwrite must fail and preserve old-bytes without creating duplicates.
	tk1, err := CopyWithOptions(ctx, "/nocopy1/src/file.bin", "/nocopy1/dst", TransferOptions{Overwrite: false})
	if err != nil {
		t.Fatalf("enqueue fallback copy failed: %v", err)
	}
	waitManagerIdle(t, CopyTaskManager)
	if tk1.GetState() != tache.StateFailed {
		t.Fatalf("expected fallback copy without overwrite to fail, state=%v err=%v", tk1.GetState(), tk1.GetErr())
	}
	if children := inner.ChildrenOf(dstDir); len(children) != 1 || string(children[0].content) != "old-bytes" {
		t.Fatalf("expected old-bytes untouched, got %+v", children)
	}

	// Overwrite=true must replace old-bytes via temp + op.MoveWithOptions(Overwrite: true) leaving 1 file.
	CopyTaskManager.RemoveAll()
	tk2, err := CopyWithOptions(ctx, "/nocopy1/src/file.bin", "/nocopy1/dst", TransferOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("enqueue fallback overwrite copy failed: %v", err)
	}
	waitManagerIdle(t, CopyTaskManager)
	if tk2.GetState() != tache.StateSucceeded {
		t.Fatalf("expected fallback copy with overwrite to succeed, state=%v err=%v", tk2.GetState(), tk2.GetErr())
	}
	children := inner.ChildrenOf(dstDir)
	if len(children) != 1 || children[0].name != "file.bin" || string(children[0].content) != "new-bytes" {
		t.Fatalf("expected single overwritten file.bin with new-bytes, got %+v", children)
	}
}

func TestFileTransfer_MergeConflictRejectsFileVsFolder(t *testing.T) {
	ctx := context.Background()
	env := setupFSTestEnv(t)

	drvA := newFSMemDrive("fsMergeA-" + t.Name())
	drvB := newFSMemDrive("fsMergeB-" + t.Name())
	env.mountDrive(t, "/drvA2", drvA)
	env.mountDrive(t, "/drvB2", drvB)

	drvA.SeedFile("root", "item", "file-payload")
	dstDir := drvB.SeedDir("root", "item")
	drvB.SeedFile(dstDir, "inside.txt", "keep")

	tk, err := MergeWithOptions(ctx, "/drvA2/item", "/drvB2", TransferOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("enqueue merge failed: %v", err)
	}
	waitManagerIdle(t, CopyTaskManager)
	if tk.GetState() != tache.StateFailed {
		t.Fatalf("expected merge file-vs-folder conflict to fail, state=%v", tk.GetState())
	}
	if len(drvB.ChildrenOf(dstDir)) != 1 {
		t.Fatalf("destination folder must remain intact after conflict")
	}
}
