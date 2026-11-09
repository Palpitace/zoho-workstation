// input: 隔离 SQLite、合成历史会话、受保护 API 与固定 Python 替身。
// output: 手动批次到有限模型处理、留出发布门槛和人工反馈下一轮的集成验证。
// pos: 离线实验最小真实进程链路测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/lab"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
	"zoho-workstation/internal/zoho"
)

func TestOfflineBatchReviewAndNextRound(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if output, err := exec.Command(python, "-m", "venv", "--without-pip", "python/.venv").CombinedOutput(); err != nil {
		t.Fatalf("venv %v: %s", err, output)
	}
	if err := os.WriteFile("python/main.py", []byte("import json\nprint(json.dumps({'ok':True}))\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import json,sys,os,time
r=json.load(sys.stdin)
lock=os.open('model-running',os.O_CREAT|os.O_EXCL|os.O_WRONLY)
try:
 time.sleep(0.01)
 data=r['input'];messages=data['ticket']['messages'];mid=messages[0]['id']
 if r['task']!='propose':
  assert 'FUTURE_ONLY' not in json.dumps(data)
 if r['task']=='reply':
  text='Please confirm the media.' if not data.get('reply') else 'Please confirm media compatibility before calibration.'
  result={'text':text,'language':'en','rationale':'先确认条件，不把历史建议视为成功结果','missing_information':[],'source_ids':[],'message_ids':[mid]}
 elif r['task']=='evaluate':
  assert not data.get('feedback') and not data.get('evaluation')
  passed='compatibility' in data['reply']['text']
  result={'verdict':'pass' if passed else 'revise','score':4 if passed else 2,'findings':[],'suggestions':[] if passed else ['先确认耗材条件']}
 else:
  result={'items':[{'title':'打印偏移排查','product':'','question':'打印偏移怎么办','answer':'确认耗材条件后再建议校准，效果待验证','conditions':'型号及耗材尚未确认','reason':'记录历史排查条件','scope':'technical','replaces_id':0,'message_ids':[mid],'source_ids':[]}]}
 print(json.dumps({'ok':True,'result':result,'returned_model':'synthetic-v1','usage':{'total_tokens':100},'request_id':'synthetic-request'}))
finally:
 os.close(lock);os.unlink('model-running')
`
	if err := os.WriteFile("python/lab.py", []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(filepath.Join("data", "app.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids := []string{}
	for i := 0; i < 5; i++ {
		id := fmt.Sprint(i + 1)
		ids = append(ids, id)
		if err := db.Reserve(ctx, []string{id}); err != nil {
			t.Fatal(err)
		}
		snapshot := ticket.Snapshot{ID: id, DepartmentID: "7", CreatedTime: "2026-11-06T00:00:00Z", Subject: "FUTURE_ONLY", Description: "FUTURE_ONLY", Complete: true, Messages: []ticket.Message{
			{ID: "c", Source: "thread", Role: "customer", Visibility: "public", Status: "SUCCESS", CreatedTime: "2026-11-06T01:00:00Z", Body: fmt.Sprintf("Printer offset on media %d", i)},
			{ID: "a", Source: "thread", Role: "agent", Visibility: "public", Status: "SUCCESS", CreatedTime: "2026-11-06T02:00:00Z", Body: "FUTURE_ONLY later support reply"},
		}}
		if err := db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{}`)}, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer external.Close()
	cfg := config.Config{Port: "8080", SyncInterval: time.Hour, SyncLookback: time.Hour, Bub: config.Model{Enabled: true, Name: "synthetic", URL: "https://synthetic.invalid", Key: "synthetic"}, Zoho: config.Zoho{BaseURL: external.URL, AccountsURL: external.URL, DepartmentID: "7"}}
	service, err := New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Handler()
	request := func(method, path string, body any, token bool) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://localhost:8080"+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		if token {
			r.Header.Set("X-CSRF-Token", service.csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	baseline, err := db.SaveKnowledge(ctx, knowledge.Entry{Kind: "sop", Scope: "technical", Title: "Media compatibility", SourceTicketID: "prior-case", SourceHash: "synthetic-baseline", Blocks: []knowledge.Block{{ID: "faq", Text: "Confirm media compatibility before calibration."}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PublishKnowledge(ctx, baseline.ID); err != nil {
		t.Fatal(err)
	}
	createBody := map[string]any{"ticket_ids": ids, "knowledge_ids": []int64{baseline.ID}, "from": "2026-11-05T00:00:00+08:00", "until": "2026-11-07T00:00:00+08:00"}
	if w := request("POST", "/api/lab/batches", createBody, false); w.Code != 403 {
		t.Fatal("missing csrf accepted")
	}
	w := request("POST", "/api/lab/batches", createBody, true)
	if w.Code != 201 {
		t.Fatalf("create %d: %s", w.Code, w.Body.String())
	}
	var batch lab.Batch
	if err := json.Unmarshal(w.Body.Bytes(), &batch); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/lab/batches/%d", batch.ID)
	// Creation freezes input but must not call a model or become running.
	if batch.State.Status != "draft" || len(batch.State.Calls) != 0 {
		t.Fatal("batch started without explicit action")
	}
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if w := request("POST", base+"/drafts", map[string]string{"key": "1:0"}, true); w.Code != 409 {
		t.Fatal("unvalidated proposal became draft")
	}
	if w := request("POST", base+"/start", nil, true); w.Code != 202 {
		t.Fatalf("start %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		batch, err = db.LabBatch(ctx, batch.ID)
		if err != nil {
			t.Fatal(err)
		}
		if batch.State.Status == "completed" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if batch.State.Status != "completed" || !lab.ValidationComplete(batch) {
		t.Fatalf("batch incomplete %s %s calls=%+v", batch.State.Status, batch.State.Error, batch.State.Calls)
	}
	if len(batch.State.Calls) != 12 {
		t.Fatalf("expected bounded 12 independent calls, got %d", len(batch.State.Calls))
	}
	for _, call := range batch.State.Calls {
		if call.Status != "success" {
			t.Fatalf("call %d failed: %s", call.ID, call.Error)
		}
	}
	var devCall, heldCall int
	for _, s := range batch.Spec.Samples {
		for _, call := range batch.State.Calls {
			if call.Task == "reply" && call.NodeID == s.Nodes[0].ID {
				if s.Split == "development" {
					devCall = call.ID
				} else if s.Split == "holdout" {
					heldCall = call.ID
				}
			}
		}
	}
	for _, id := range []int{devCall, heldCall} {
		w = request("POST", base+"/feedback", map[string]any{"call_id": id, "score": 4, "tags": []string{}, "note": "确认型号后再给排查", "edited_reply": ""}, true)
		if w.Code != 201 {
			t.Fatalf("feedback %d %s", w.Code, w.Body.String())
		}
	}
	batch, _ = db.LabBatch(ctx, batch.ID)
	proposals := lab.Candidates(batch)
	if len(proposals) == 0 {
		t.Fatal("missing proposals")
	}
	w = request("POST", base+"/drafts", map[string]string{"key": proposals[0].Key}, true)
	if w.Code != 201 {
		t.Fatalf("draft %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"draft"`) {
		t.Fatal("implicitly published")
	}
	var promoted knowledge.Entry
	if json.Unmarshal(w.Body.Bytes(), &promoted) != nil {
		t.Fatal("invalid draft")
	}
	lineage := map[string]bool{}
	for _, id := range promoted.SourceTickets {
		lineage[id] = true
	}
	if !lineage["prior-case"] {
		t.Fatal("lost pinned-knowledge lineage")
	}
	for _, s := range batch.Spec.Samples {
		if lineage[s.TicketID] != (s.Split != "holdout") {
			t.Fatal("incorrect training lineage")
		}
	}
	w = request("POST", "/api/lab/batches", map[string]int64{"parent_id": batch.ID}, true)
	if w.Code != 201 {
		t.Fatalf("next round %d %s", w.Code, w.Body.String())
	}
	var next lab.Batch
	_ = json.Unmarshal(w.Body.Bytes(), &next)
	if next.State.Status != "draft" || len(next.Spec.Feedback) != 1 || next.Spec.Feedback[0].CallID != devCall || next.Spec.Feedback[0].BatchID != batch.ID || next.Spec.Feedback[0].RatedReply == nil {
		t.Fatal("feedback or manual-start isolation")
	}
	if lab.Hash(next.Spec.Samples) != lab.Hash(batch.Spec.Samples) {
		t.Fatal("next round repartitioned/replaced frozen evidence")
	}
}
