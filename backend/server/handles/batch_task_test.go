package handles

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/tache"
)

func TestHandleCopyRecursiveMoveAndCancelBatch_HTTP(t *testing.T) {
	drv, r := setupHandleMoveCopyEnv(t, "/batchhttp")
	SetupTaskRoute(r.Group("/api/task"))

	src := drv.SeedDir("root", "src")
	_ = drv.SeedDir("root", "dst")
	rmSrc := drv.SeedDir("root", "rmsrc")
	rmNested := drv.SeedDir(rmSrc, "nested")
	_ = drv.SeedDir("root", "rmdst")

	drv.SeedFile(src, "f1.txt", "a")
	drv.SeedFile(src, "f2.txt", "b")
	drv.SeedFile(src, "f3.txt", "c")
	drv.SeedFile(rmNested, "m1.txt", "m1")
	drv.SeedFile(rmNested, "m2.txt", "m2")

	// 1. Copy batch A: two files must share identical non-empty batch_id and batch_label.
	copyRespA := postJSON(t, r, "/api/fs/copy", `{"src_dir":"/batchhttp/src","dst_dir":"/batchhttp/dst","names":["f1.txt","f2.txt"]}`)
	if copyRespA.Code != 200 {
		t.Fatalf("copy batch A failed: %d (%s)", copyRespA.Code, copyRespA.Message)
	}
	var copyDataA struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if err := json.Unmarshal(copyRespA.Data, &copyDataA); err != nil {
		t.Fatalf("unmarshal copy A tasks: %v", err)
	}
	if len(copyDataA.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in batch A, got %d", len(copyDataA.Tasks))
	}
	batchA := copyDataA.Tasks[0].BatchID
	if batchA == "" || copyDataA.Tasks[1].BatchID != batchA {
		t.Fatalf("expected shared non-empty batch_id in batch A, got %q and %q", batchA, copyDataA.Tasks[1].BatchID)
	}
	if copyDataA.Tasks[0].BatchLabel != "dst" || copyDataA.Tasks[1].BatchLabel != "dst" {
		t.Fatalf("expected batch_label 'dst', got %q and %q", copyDataA.Tasks[0].BatchLabel, copyDataA.Tasks[1].BatchLabel)
	}

	// 2. Copy batch B: distinct batch_id so canceling batch A leaves batch B pending.
	copyRespB := postJSON(t, r, "/api/fs/copy", `{"src_dir":"/batchhttp/src","dst_dir":"/batchhttp/dst","names":["f3.txt"]}`)
	if copyRespB.Code != 200 {
		t.Fatalf("copy batch B failed: %d (%s)", copyRespB.Code, copyRespB.Message)
	}
	var copyDataB struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if err := json.Unmarshal(copyRespB.Data, &copyDataB); err != nil {
		t.Fatalf("unmarshal copy B tasks: %v", err)
	}
	if len(copyDataB.Tasks) != 1 || copyDataB.Tasks[0].BatchID == "" || copyDataB.Tasks[0].BatchID == batchA {
		t.Fatalf("expected distinct batch_id for batch B, got %+v", copyDataB.Tasks)
	}
	if copyDataB.Tasks[0].BatchLabel != "f3.txt" {
		t.Fatalf("expected single-item batch_label 'f3.txt', got %q", copyDataB.Tasks[0].BatchLabel)
	}

	// 3. Cancel only batch A via POST /api/task/copy/cancel_batch.
	cancelReq := httptest.NewRequest(http.MethodPost, "/api/task/copy/cancel_batch?batch_id="+batchA, nil)
	cancelRec := httptest.NewRecorder()
	r.ServeHTTP(cancelRec, cancelReq)
	var cancelOut apiResp
	if err := json.Unmarshal(cancelRec.Body.Bytes(), &cancelOut); err != nil {
		t.Fatalf("unmarshal cancel_batch: %v", err)
	}
	if cancelOut.Code != 200 {
		t.Fatalf("cancel_batch returned %d (%s)", cancelOut.Code, cancelOut.Message)
	}
	for _, tk := range fs.CopyTaskManager.GetAll() {
		if tk.GetBatchID() == batchA && tk.GetState() == tache.StatePending {
			t.Fatalf("expected batch A task %s to be canceled, still pending", tk.GetID())
		}
		if tk.GetBatchID() == copyDataB.Tasks[0].BatchID && tk.GetState() != tache.StatePending {
			t.Fatalf("expected batch B task %s to remain pending, got %v", tk.GetID(), tk.GetState())
		}
	}

	// 4. Recursive move assigns a single shared batch_id across all nested files.
	rmResp := postJSON(t, r, "/api/fs/recursive_move", `{"src_dir":"/batchhttp/rmsrc","dst_dir":"/batchhttp/rmdst","conflict_policy":"overwrite"}`)
	if rmResp.Code != 200 {
		t.Fatalf("recursive_move failed: %d (%s)", rmResp.Code, rmResp.Message)
	}
	var rmData struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if err := json.Unmarshal(rmResp.Data, &rmData); err != nil {
		t.Fatalf("unmarshal recursive_move tasks: %v", err)
	}
	if len(rmData.Tasks) != 2 {
		t.Fatalf("expected 2 recursive_move tasks, got %d", len(rmData.Tasks))
	}
	if rmData.Tasks[0].BatchID == "" || rmData.Tasks[0].BatchID != rmData.Tasks[1].BatchID {
		t.Fatalf("expected shared batch_id across recursive_move tasks, got %q and %q", rmData.Tasks[0].BatchID, rmData.Tasks[1].BatchID)
	}
	if !strings.EqualFold(rmData.Tasks[0].BatchLabel, "rmdst") {
		t.Fatalf("expected recursive_move batch_label 'rmdst', got %q", rmData.Tasks[0].BatchLabel)
	}
}

func TestFsStream_OverwriteSkipPreservesExistingFile(t *testing.T) {
	drv, r := setupHandleMoveCopyEnv(t, "/skiphttp")
	r.PUT("/api/fs/put", FsStream)

	drv.SeedFile("root", "dup.txt", "original-bytes")

	// Overwrite: skip -> HTTP 200 {skipped: true} and original content preserved.
	req := httptest.NewRequest(http.MethodPut, "/api/fs/put", bytes.NewBufferString("new-bytes"))
	req.Header.Set("File-Path", "/skiphttp/dup.txt")
	req.Header.Set("Overwrite", "skip")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var out apiResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal skip response: %v", err)
	}
	if out.Code != 200 {
		t.Fatalf("expected code 200 on Overwrite=skip, got %d (%s)", out.Code, out.Message)
	}
	var data struct {
		Skipped bool `json:"skipped"`
	}
	if err := json.Unmarshal(out.Data, &data); err != nil || !data.Skipped {
		t.Fatalf("expected skipped:true in response, got %s (err=%v)", out.Data, err)
	}
	children := drv.ChildrenOf("root")
	if len(children) != 1 || string(children[0].content) != "original-bytes" {
		t.Fatalf("expected original file untouched, got %+v", children)
	}

	// Overwrite: false -> returns code 403 file exists.
	reqFalse := httptest.NewRequest(http.MethodPut, "/api/fs/put", bytes.NewBufferString("new-bytes"))
	reqFalse.Header.Set("File-Path", "/skiphttp/dup.txt")
	reqFalse.Header.Set("Overwrite", "false")
	recFalse := httptest.NewRecorder()
	r.ServeHTTP(recFalse, reqFalse)

	var outFalse apiResp
	if err := json.Unmarshal(recFalse.Body.Bytes(), &outFalse); err != nil {
		t.Fatalf("unmarshal overwrite=false response: %v", err)
	}
	if outFalse.Code != 403 {
		t.Fatalf("expected code 403 on Overwrite=false, got %d (%s)", outFalse.Code, outFalse.Message)
	}
}
