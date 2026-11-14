package handles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type handleMemNode struct {
	id       string
	parentID string
	name     string
	isDir    bool
	content  []byte
	modified time.Time
}

type handleMemDrive struct {
	model.Storage
	driverName  string
	mu          sync.Mutex
	nextID      int64
	nodes       map[string]*handleMemNode
	children    map[string][]string
	failListFor map[string]error
}

func newHandleMemDrive(driverName string) *handleMemDrive {
	return &handleMemDrive{
		driverName: driverName,
		nodes: map[string]*handleMemNode{
			"root": {id: "root", name: "root", isDir: true, modified: time.Now()},
		},
		children:    map[string][]string{"root": nil},
		failListFor: make(map[string]error),
	}
}

func (d *handleMemDrive) Config() driver.Config        { return driver.Config{Name: d.driverName, LocalSort: true} }
func (d *handleMemDrive) GetAddition() driver.Additional { return &struct{}{} }
func (d *handleMemDrive) Init(ctx context.Context) error { return nil }
func (d *handleMemDrive) Drop(ctx context.Context) error { return nil }
func (d *handleMemDrive) GetRoot(ctx context.Context) (model.Obj, error) {
	return &model.Object{ID: "root", Name: "root", IsFolder: true}, nil
}

func (d *handleMemDrive) allocID() string {
	n := atomic.AddInt64(&d.nextID, 1)
	return fmt.Sprintf("h-id-%d", n)
}

func (d *handleMemDrive) addChildLocked(parentID, name string, isDir bool, content []byte) *handleMemNode {
	id := d.allocID()
	node := &handleMemNode{
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

func (d *handleMemDrive) SeedDir(parentID, name string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if parentID == "" {
		parentID = "root"
	}
	return d.addChildLocked(parentID, name, true, nil).id
}

func (d *handleMemDrive) SeedFile(parentID, name, content string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if parentID == "" {
		parentID = "root"
	}
	return d.addChildLocked(parentID, name, false, []byte(content)).id
}

func (d *handleMemDrive) ChildrenOf(parentID string) []*handleMemNode {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := d.children[parentID]
	out := make([]*handleMemNode, 0, len(ids))
	for _, id := range ids {
		if n, ok := d.nodes[id]; ok {
			cp := *n
			cp.content = append([]byte(nil), n.content...)
			out = append(out, &cp)
		}
	}
	return out
}

func (d *handleMemDrive) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
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

func (d *handleMemDrive) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotImplement
}

func (d *handleMemDrive) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.addChildLocked(parentDir.GetID(), dirName, true, nil)
	return &model.Object{ID: n.id, Name: n.name, Modified: n.modified, IsFolder: true}, nil
}

func (d *handleMemDrive) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
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

func (d *handleMemDrive) copySubtreeLocked(srcID, dstParentID string) *handleMemNode {
	src := d.nodes[srcID]
	cloned := d.addChildLocked(dstParentID, src.name, src.isDir, src.content)
	if src.isDir {
		for _, childID := range d.children[srcID] {
			d.copySubtreeLocked(childID, cloned.id)
		}
	}
	return cloned
}

func (d *handleMemDrive) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.nodes[srcObj.GetID()]; !ok {
		return nil, errs.ObjectNotFound
	}
	cloned := d.copySubtreeLocked(srcObj.GetID(), dstDir.GetID())
	return &model.Object{ID: cloned.id, Name: cloned.name, Size: int64(len(cloned.content)), Modified: cloned.modified, IsFolder: cloned.isDir}, nil
}

func (d *handleMemDrive) removeSubtreeLocked(id string) {
	if node, ok := d.nodes[id]; ok && node.isDir {
		for _, cid := range append([]string(nil), d.children[id]...) {
			d.removeSubtreeLocked(cid)
		}
		delete(d.children, id)
	}
	delete(d.nodes, id)
}

func (d *handleMemDrive) Remove(ctx context.Context, obj model.Obj) error {
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

func (d *handleMemDrive) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, ok := d.nodes[srcObj.GetID()]
	if !ok {
		return errs.ObjectNotFound
	}
	n.name = newName
	n.modified = time.Now()
	return nil
}

func (d *handleMemDrive) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.addChildLocked(dstDir.GetID(), file.GetName(), false, data)
	return &model.Object{ID: n.id, Name: n.name, Size: int64(len(n.content)), Modified: n.modified}, nil
}

func setupHandleMoveCopyEnv(t *testing.T, mountPath string) (*handleMemDrive, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	conf.Conf = conf.DefaultConfig(t.TempDir())
	conf.Conf.Database.Type = "sqlite3"
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}
	db.Init(database)
	fs.CopyTaskManager = tache.NewManager[*fs.FileTransferTask](tache.WithWorks(0))
	fs.MoveTaskManager = tache.NewManager[*fs.FileTransferTask](tache.WithWorks(0))

	drv := newHandleMemDrive("handleMemDrive-" + t.Name())
	op.RegisterDriver(func() driver.Driver { return drv })
	storageID, err := op.CreateStorage(context.Background(), model.Storage{
		MountPath:       mountPath,
		Driver:          drv.Config().Name,
		Addition:        "{}",
		CacheExpiration: 60,
		Status:          op.WORK,
	})
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	t.Cleanup(func() {
		_ = op.DeleteStorageById(context.Background(), storageID)
		db.Close()
	})

	r := gin.New()
	r.Use(func(c *gin.Context) {
		admin := &model.User{ID: 1, Username: "admin", Role: model.ADMIN, Permission: ^int32(0), BasePath: "/"}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), conf.UserKey, admin))
		c.Next()
	})
	r.POST("/api/fs/move", FsMove)
	r.POST("/api/fs/copy", FsCopy)
	r.POST("/api/fs/recursive_move", FsRecursiveMove)
	r.POST("/api/fs/rename", FsRename)
	r.POST("/api/fs/remove", FsRemove)
	r.POST("/api/fs/remove_empty_directory", FsRemoveEmptyDirectory)
	return drv, r
}

type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func postJSON(t *testing.T, r *gin.Engine, path, body string) apiResp {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out apiResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response from %s: %v (raw: %s)", path, err, rec.Body.String())
	}
	return out
}

func waitHandleTasksIdle(t *testing.T, mgr *tache.Manager[*fs.FileTransferTask]) {
	t.Helper()
	queue := append([]*fs.FileTransferTask(nil), mgr.GetAll()...)
	for len(queue) > 0 {
		tk := queue[0]
		queue = queue[1:]
		if tk.GetState() != tache.StatePending {
			continue
		}
		if tk.Ctx() == nil {
			tk.SetCtx(context.Background())
		}
		err := tk.RunWithNextTaskCallback(func(next *fs.FileTransferTask) error {
			queue = append(queue, next)
			return nil
		})
		if err != nil {
			tk.SetErr(err)
			tk.SetState(tache.StateFailed)
		} else {
			tk.SetState(tache.StateSucceeded)
		}
	}
}

func TestHandleMoveCopy_GuardsAndOverwrite(t *testing.T) {
	drv, r := setupHandleMoveCopyEnv(t, "/hmc")
	srcDir := drv.SeedDir("root", "src")
	dstDir := drv.SeedDir("root", "dst")
	drv.SeedFile(srcDir, "a.txt", "new-a")
	drv.SeedFile(dstDir, "a.txt", "old-a")
	drv.SeedFile(srcDir, "b.txt", "new-b")
	drv.SeedDir(dstDir, "b.txt") // dst has folder "b.txt"

	// Same-dir move & copy with overwrite=true must return 400.
	resp := postJSON(t, r, "/api/fs/move", `{"src_dir":"/hmc/src","dst_dir":"/hmc/src","names":["a.txt"],"overwrite":true}`)
	if resp.Code != 400 {
		t.Fatalf("same-dir move overwrite got code %d (%s), want 400", resp.Code, resp.Message)
	}
	resp = postJSON(t, r, "/api/fs/copy", `{"src_dir":"/hmc/src","dst_dir":"/hmc/src","names":["a.txt"],"overwrite":true}`)
	if resp.Code != 400 {
		t.Fatalf("same-dir copy overwrite got code %d (%s), want 400", resp.Code, resp.Message)
	}

	// Cross-type overwrite (file overwriting folder) must return 403.
	resp = postJSON(t, r, "/api/fs/move", `{"src_dir":"/hmc/src","dst_dir":"/hmc/dst","names":["b.txt"],"overwrite":true}`)
	if resp.Code != 403 {
		t.Fatalf("cross-type move overwrite got code %d (%s), want 403", resp.Code, resp.Message)
	}
	resp = postJSON(t, r, "/api/fs/copy", `{"src_dir":"/hmc/src","dst_dir":"/hmc/dst","names":["b.txt"],"overwrite":true}`)
	if resp.Code != 403 {
		t.Fatalf("cross-type copy overwrite got code %d (%s), want 403", resp.Code, resp.Message)
	}

	// Valid cross-folder copy overwrite -> returns tasks, replaces old-a with 1 entry.
	resp = postJSON(t, r, "/api/fs/copy", `{"src_dir":"/hmc/src","dst_dir":"/hmc/dst","names":["a.txt"],"overwrite":true}`)
	if resp.Code != 200 {
		t.Fatalf("copy overwrite got code %d (%s), want 200", resp.Code, resp.Message)
	}
	var taskPayload struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if err := json.Unmarshal(resp.Data, &taskPayload); err != nil || len(taskPayload.Tasks) != 1 {
		t.Fatalf("expected 1 returned task in data.tasks, got err=%v data=%s", err, string(resp.Data))
	}
	waitHandleTasksIdle(t, fs.CopyTaskManager)
	var aCount int
	var aContent string
	for _, c := range drv.ChildrenOf(dstDir) {
		if c.name == "a.txt" {
			aCount++
			aContent = string(c.content)
		}
	}
	if aCount != 1 || aContent != "new-a" {
		t.Fatalf("after copy overwrite expected 1 a.txt=new-a, got count=%d content=%q", aCount, aContent)
	}

	// Move without overwrite when a.txt exists -> 403, 0 tasks queued.
	resp = postJSON(t, r, "/api/fs/move", `{"src_dir":"/hmc/src","dst_dir":"/hmc/dst","names":["a.txt"],"overwrite":false}`)
	if resp.Code != 403 {
		t.Fatalf("move without overwrite got code %d (%s), want 403", resp.Code, resp.Message)
	}

	// Move WITH overwrite -> returns 1 task, replaces dst/a.txt and removes src/a.txt.
	drv.SeedFile(dstDir, "a.txt", "stale-dup") // simulate duplicate before overwrite
	resp = postJSON(t, r, "/api/fs/move", `{"src_dir":"/hmc/src","dst_dir":"/hmc/dst","names":["a.txt"],"overwrite":true}`)
	if resp.Code != 200 {
		t.Fatalf("move overwrite got code %d (%s), want 200", resp.Code, resp.Message)
	}
	taskPayload.Tasks = nil
	if err := json.Unmarshal(resp.Data, &taskPayload); err != nil || len(taskPayload.Tasks) != 1 {
		t.Fatalf("expected 1 move task in data.tasks, got err=%v data=%s", err, string(resp.Data))
	}
	waitHandleTasksIdle(t, fs.MoveTaskManager)
	aCount = 0
	for _, c := range drv.ChildrenOf(dstDir) {
		if c.name == "a.txt" {
			aCount++
			aContent = string(c.content)
		}
	}
	if aCount != 1 || aContent != "new-a" {
		t.Fatalf("after move overwrite expected 1 a.txt=new-a, got count=%d content=%q", aCount, aContent)
	}
	for _, c := range drv.ChildrenOf(srcDir) {
		if c.name == "a.txt" {
			t.Fatalf("src a.txt should have been moved out")
		}
	}
}

func TestHandleMove_PropagatesRealDstListErrors(t *testing.T) {
	drv, r := setupHandleMoveCopyEnv(t, "/hmcerr")
	srcDir := drv.SeedDir("root", "src")
	dstDir := drv.SeedDir("root", "dst")
	drv.SeedFile(srcDir, "a.txt", "data")
	drv.failListFor[dstDir] = errors.New("simulated remote permission/network error")

	resp := postJSON(t, r, "/api/fs/move", `{"src_dir":"/hmcerr/src","dst_dir":"/hmcerr/dst","names":["a.txt"],"overwrite":true}`)
	if resp.Code == 200 {
		t.Fatalf("expected error response when dst list fails with non-NotFound error, got 200")
	}
}

func TestHandleRecursiveMove_OverwriteAndTaskReturn(t *testing.T) {
	drv, r := setupHandleMoveCopyEnv(t, "/hrm")
	srcDir := drv.SeedDir("root", "src")
	dstDir := drv.SeedDir("root", "dst")
	drv.SeedFile(srcDir, "doc.txt", "new-doc")
	drv.SeedFile(dstDir, "doc.txt", "old-doc")
	drv.SeedFile(srcDir, "extra.txt", "extra")
	drv.SeedFile(dstDir, "keep.txt", "keep")

	resp := postJSON(t, r, "/api/fs/recursive_move", `{"src_dir":"/hrm/src","dst_dir":"/hrm/dst","conflict_policy":"overwrite"}`)
	if resp.Code != 200 {
		t.Fatalf("recursive_move overwrite got code %d (%s), want 200", resp.Code, resp.Message)
	}
	var payload struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if err := json.Unmarshal(resp.Data, &payload); err != nil || len(payload.Tasks) != 2 {
		t.Fatalf("expected 2 recursive_move tasks returned, got err=%v data=%s", err, string(resp.Data))
	}
	waitHandleTasksIdle(t, fs.MoveTaskManager)

	got := map[string]string{}
	counts := map[string]int{}
	for _, c := range drv.ChildrenOf(dstDir) {
		got[c.name] = string(c.content)
		counts[c.name]++
	}
	if counts["doc.txt"] != 1 || got["doc.txt"] != "new-doc" {
		t.Fatalf("expected overwritten doc.txt=new-doc without duplicates, counts=%v got=%v", counts, got)
	}
	if got["extra.txt"] != "extra" || got["keep.txt"] != "keep" {
		t.Fatalf("unexpected files in dstDir: %v", got)
	}
	if !strings.Contains(resp.Message, "Recursive move") && resp.Code != 200 {
		t.Fatalf("unexpected resp: %+v", resp)
	}
}

func TestHandleRename_ConflictCheckAndOverwrite(t *testing.T) {
	drv, r := setupHandleMoveCopyEnv(t, "/hren")
	subDir := drv.SeedDir("root", "folder")
	idA := drv.SeedFile("root", "a.txt", "content-a")
	idB1 := drv.SeedFile("root", "b.txt", "content-b-old")

	// Rename without conflict succeeds immediately.
	resp := postJSON(t, r, "/api/fs/rename", `{"path":"/hren/a.txt","name":"c.txt","overwrite":false}`)
	if resp.Code != 200 {
		t.Fatalf("expected non-conflicting rename 200, got %d (%s)", resp.Code, resp.Message)
	}

	// Same-type conflict with overwrite=false -> 409 conflict code, preserves both items.
	resp = postJSON(t, r, "/api/fs/rename", `{"path":"/hren/c.txt","name":"b.txt","overwrite":false}`)
	if resp.Code != 409 {
		t.Fatalf("expected conflict rename code 409, got %d (%s)", resp.Code, resp.Message)
	}
	if len(drv.ChildrenOf("root")) != 3 {
		t.Fatalf("expected all 3 root items preserved on 409, got %+v", drv.ChildrenOf("root"))
	}

	// Simulate historical duplicate b.txt and confirm overwrite=true -> removes both old b.txt IDs, keeps c.txt (idA) renamed to b.txt.
	idB2 := drv.SeedFile("root", "b.txt", "content-b-dup")
	resp = postJSON(t, r, "/api/fs/rename", `{"path":"/hren/c.txt","name":"b.txt","overwrite":true}`)
	if resp.Code != 200 {
		t.Fatalf("expected overwrite rename 200, got %d (%s)", resp.Code, resp.Message)
	}
	var bNodes []*handleMemNode
	for _, c := range drv.ChildrenOf("root") {
		if c.name == "b.txt" {
			bNodes = append(bNodes, c)
		}
		if c.id == idB1 || c.id == idB2 {
			t.Fatalf("old conflicting b.txt (%s) should have been removed", c.id)
		}
	}
	if len(bNodes) != 1 || bNodes[0].id != idA || string(bNodes[0].content) != "content-a" {
		t.Fatalf("expected single b.txt with id %s and content-a, got %+v", idA, bNodes)
	}

	// Cross-type conflict (file over folder) rejected with 403 whether overwrite=false or true.
	for _, ov := range []bool{false, true} {
		resp = postJSON(t, r, "/api/fs/rename", fmt.Sprintf(`{"path":"/hren/b.txt","name":"folder","overwrite":%v}`, ov))
		if resp.Code != 403 {
			t.Fatalf("expected cross-type overwrite=%v to fail with 403, got %d (%s)", ov, resp.Code, resp.Message)
		}
	}
	_ = subDir

	// Real List error is propagated rather than silently overwriting.
	drv.failListFor["root"] = errors.New("simulated remote error")
	resp = postJSON(t, r, "/api/fs/rename", `{"path":"/hren/b.txt","name":"d.txt","overwrite":false}`)
	if resp.Code != 500 {
		t.Fatalf("expected 500 on real list error, got %d (%s)", resp.Code, resp.Message)
	}
}

func TestHandleRemove_StaleCacheAndEmptyDirectory(t *testing.T) {
	drv, r := setupHandleMoveCopyEnv(t, "/hdel")
	// Empty dir with an empty nested subdir, plus a non-empty dir containing a file.
	emptyParent := drv.SeedDir("root", "empty_parent")
	_ = drv.SeedDir(emptyParent, "empty_child")
	keepDir := drv.SeedDir("root", "keep_dir")
	_ = drv.SeedFile(keepDir, "keep.txt", "keep")

	// Warm root cache before seeding remote_only.txt directly on driver.
	if objs, err := fs.List(context.Background(), "/hdel", &fs.ListArgs{NoLog: true}); err != nil || len(objs) != 2 {
		t.Fatalf("unexpected initial list: len=%d err=%v", len(objs), err)
	}
	drv.SeedFile("root", "remote_only.txt", "stale-miss")

	// Remove finds remote_only.txt despite stale dirCache.
	resp := postJSON(t, r, "/api/fs/remove", `{"dir":"/hdel","names":["remote_only.txt"]}`)
	if resp.Code != 200 {
		t.Fatalf("expected FsRemove 200 on stale cache miss, got %d (%s)", resp.Code, resp.Message)
	}
	for _, c := range drv.ChildrenOf("root") {
		if c.name == "remote_only.txt" {
			t.Fatalf("remote_only.txt should have been removed")
		}
	}

	// Remove empty directories cleans empty tree and keeps non-empty dir untouched.
	resp = postJSON(t, r, "/api/fs/remove_empty_directory", `{"src_dir":"/hdel"}`)
	if resp.Code != 200 {
		t.Fatalf("expected FsRemoveEmptyDirectory 200, got %d (%s)", resp.Code, resp.Message)
	}
	rootChildren := drv.ChildrenOf("root")
	if len(rootChildren) != 1 || rootChildren[0].id != keepDir {
		t.Fatalf("expected only non-empty keepDir to remain, got %+v", rootChildren)
	}
}
