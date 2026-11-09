// input: 隔离 SQLite、合成证据、固定 Python 进程替身和受保护 HTTP 请求。
// output: 知识边界及Bub列表/详情/运行状态一致、重启恢复的集成证据。
// pos: 知识链路端到端回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
	"zoho-workstation/internal/zoho"
)

func TestAutomaticKnowledgeAndPublicationBoundary(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if output, err := exec.Command(python, "-m", "venv", "--without-pip", "python/.venv").CombinedOutput(); err != nil {
		t.Fatalf("venv: %v %s", err, output)
	}
	if err := os.WriteFile("python/main.py", []byte("import json\nprint(json.dumps({'ok':True}))\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import json,sys,os,time
r=json.load(sys.stdin)
f=os.open('model-running', os.O_CREAT|os.O_EXCL|os.O_WRONLY)
try:
 time.sleep(0.02)
 if r['task']=='case':
  result={'title':'P1 print offset','product':'P1','issue':'Calibration requested; no confirmed result','steps':[{'action':'Calibrate P1','execution':'unknown','outcome':'unknown','message_ids':['a']}],'message_ids':['c']}
 else:
  result={'steps':[{'action':'Verify calibration prerequisites','reason':'Customer has not confirmed execution','source_ids':[r['input']['sources'][0]['id']],'current_message_ids':['c']}],'missing_information':[],'conflicts':[]}
 print(json.dumps({'ok':True,'returned_model':'synthetic-actual','result':result}))
finally:
 os.close(f);os.unlink('model-running')
`
	if err := os.WriteFile("python/knowledge.py", []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(filepath.Join("data", "app.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Environment: "development", Port: "8080", SyncInterval: time.Hour, SyncLookback: time.Hour, Bub: config.Model{Enabled: true, Name: "synthetic", URL: "https://synthetic.invalid", Key: "synthetic"}, Jev: config.Model{Name: "synthetic-jev"}}
	snapshots := []ticket.Snapshot{}
	for _, id := range []string{"1", "2"} {
		if err := db.Reserve(ctx, []string{id}); err != nil {
			t.Fatal(err)
		}
		snapshot := ticket.Snapshot{ID: id, DepartmentID: "7", Subject: "P1 calibration offset", Product: "P1", Complete: true, Messages: []ticket.Message{{ID: "c", Role: "customer", Source: "thread", Visibility: "public", Body: "My P1 printing is offset; I have not calibrated."}, {ID: "a", Role: "agent", Source: "thread", Visibility: "public", Body: "Please calibrate P1."}}}
		raw := ticket.RawSnapshot{Ticket: json.RawMessage(`{}`)}
		if err := db.Save(ctx, raw, snapshot); err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, snapshot)
		input, hash, _ := analysis.Input(snapshot, raw)
		_, hash, _ = analysis.BubInput(input, hash)
		req := store.AnalysisRequest{TicketID: id, Kind: "bub", InputHash: hash, SchemaVersion: analysis.BubSchema, RequestedModel: cfg.Bub.Name, Round: DevelopmentRound}
		run, _, err := db.StartAnalysis(ctx, req, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.FinishAnalysis(ctx, run.ID, "synthetic-actual", json.RawMessage(`{"summary":{"device_info":[],"customer_feedback":[{"text":"Not calibrated","message_ids":["c"]}],"support_responses":[{"text":"Support suggested calibration","message_ids":["a"]}]}}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	draft, err := db.SaveKnowledge(ctx, knowledge.Entry{Kind: "sop", Title: "P1 calibration", Product: "P1", SourceHash: "original", Blocks: []knowledge.Block{{ID: "section-1", Text: "SOP_SENTINEL Verify media before calibration."}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PublishKnowledge(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer external.Close()
	cfg.Zoho = config.Zoho{BaseURL: external.URL, AccountsURL: external.URL, DepartmentID: "7"}
	service, err := New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ready := false
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		ready = true
		for _, id := range []string{"1", "2"} {
			record, _ := db.Get(ctx, id)
			v, e := service.knowledgeView(ctx, record, "advice")
			if e != nil || v.Success == nil || v.Stale {
				ready = false
				break
			}
		}
		if ready {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("automatic knowledge/advice did not finish")
	}
	service.Close()
	entries, err := db.ListKnowledge(ctx, "", "published")
	if err != nil || len(entries) != 3 {
		t.Fatalf("cases not accumulated: %d %v", len(entries), err)
	}
	record, _ := db.Get(ctx, "1")
	_, jevInput, err := requestForRecord(ctx, db, cfg, record, "jev", DevelopmentRound)
	if err != nil || bytes.Contains(jevInput, []byte("SOP_SENTINEL")) || bytes.Contains(jevInput, []byte("Verify calibration prerequisites")) {
		t.Fatalf("Jev consumed knowledge: %v", err)
	}
	old, err := service.knowledgeView(ctx, record, "advice")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Advice  analysis.Advice    `json:"advice"`
		Sources []knowledge.Source `json:"sources"`
	}
	if json.Unmarshal(old.Success.Result, &response) != nil || len(response.Sources) == 0 {
		t.Fatal("source snapshots missing")
	}
	// Re-extraction of the same input can correct a case without changing the fact summary.
	origin, _ := db.Get(ctx, "2")
	caseReq, _, err := requestForRecord(ctx, db, cfg, origin, "case", DevelopmentRound)
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := db.Analysis(ctx, caseReq)
	var corrected struct {
		Case analysis.Case `json:"case"`
	}
	_ = json.Unmarshal(previous.Success.Result, &corrected)
	corrected.Case.Issue = "P1 calibration offset; corrected interpretation, still unknown outcome"
	correctedResult, _ := json.Marshal(corrected)
	run, started, err := db.StartAnalysis(ctx, caseReq, true)
	if err != nil || !started {
		t.Fatalf("re-extract: %v", err)
	}
	if err := db.FinishAnalysis(ctx, run.ID, "synthetic-actual", correctedResult, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.saveCase(ctx, caseReq, correctedResult); err != nil {
		t.Fatal(err)
	}
	refreshed, _ := db.ListKnowledge(ctx, "", "published")
	found := false
	for _, item := range refreshed {
		if item.Kind == "case" && item.SourceTicketID == "2" && strings.Contains(item.Blocks[0].Text, "corrected interpretation") {
			found = true
		}
	}
	if !found {
		t.Fatal("same-input correction left old published case")
	}
	// Reference set now changed; store a current synthetic advice to isolate the SOP test below.
	adviceReq, adviceInput, err := requestForRecord(ctx, db, cfg, record, "advice", DevelopmentRound)
	if err != nil {
		t.Fatal(err)
	}
	adviceRun, _, err := db.StartAnalysis(ctx, adviceReq, false)
	if err != nil {
		t.Fatal(err)
	}
	adviceResult, returned, callErr := analysis.Knowledge(ctx, cfg.Bub, "advice", adviceInput)
	if callErr != nil {
		t.Fatal(callErr)
	}
	if err := db.FinishAnalysis(ctx, adviceRun.ID, returned, adviceResult, nil); err != nil {
		t.Fatal(err)
	}
	// Publishing an update must stale only dependent advice; source evidence remains readable.
	replacement, err := db.SaveKnowledge(ctx, knowledge.Entry{Kind: "sop", Title: "P1 calibration", Product: "P1", SourceHash: "revision", ReplacesID: draft.ID, Blocks: []knowledge.Block{{ID: "section-1", Text: "Changed calibration prerequisites."}}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := service.knowledgeView(ctx, record, "advice")
	if before.Stale {
		t.Fatal("unpublished draft changed advice")
	}
	if err := db.PublishKnowledge(ctx, replacement.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := service.knowledgeView(ctx, record, "advice")
	if !after.Stale {
		t.Fatal("published replacement did not stale advice")
	}
	// A changed source conversation withdraws its old case from retrieval, without deleting evidence.
	changed := snapshots[1]
	changed.Messages = append(changed.Messages, ticket.Message{ID: "new", Role: "customer", Source: "thread", Visibility: "public", Body: "New contradictory result"})
	if err := db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{}`)}, changed); err != nil {
		t.Fatal(err)
	}
	input, _, _ := analysis.Input(record.Snapshot, record.Raw)
	input, _, _ = analysis.BubInput(input, "h")
	sources, err := relevantKnowledge(ctx, db, cfg, record, input, json.RawMessage(`{"summary":"calibration offset"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Kind == "case" && source.TicketID == "2" {
			t.Fatal("outdated case entered retrieval")
		}
	}
	// HTTP publishing remains an explicit CSRF-protected operation.
	handler := service.Handler()
	request := httptest.NewRequest("POST", fmt.Sprintf("/api/knowledge/%d/archive", replacement.ID), nil)
	request.Host = "localhost:8080"
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request)
	if denied.Code != 403 {
		t.Fatal("unprotected knowledge mutation")
	}
	sourceID := int64(0)
	for _, item := range entries {
		if item.Kind == "case" && item.SourceTicketID == "1" {
			sourceID = item.ID
		}
	}
	request = httptest.NewRequest("POST", "/api/knowledge/draft", strings.NewReader(fmt.Sprintf(`{"title":"Derived SOP","source_entry_id":%d}`, sourceID)))
	request.Host = "localhost:8080"
	request.Header.Set("X-CSRF-Token", service.csrf)
	request.Header.Set("Content-Type", "application/json")
	saved := httptest.NewRecorder()
	handler.ServeHTTP(saved, request)
	if saved.Code != 201 {
		t.Fatalf("case SOP draft: %d %s", saved.Code, saved.Body.String())
	}
	var derived knowledge.Entry
	_ = json.Unmarshal(saved.Body.Bytes(), &derived)
	if derived.Status != "draft" || derived.SourceTicketID == "" {
		t.Fatalf("derived SOP lost provenance: %+v", derived)
	}
}

func TestKnowledgeDocumentImportIsLocalDraft(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("python", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "python/.venv"), "python/.venv"); err != nil {
		t.Fatal(err)
	}
	parser, err := os.ReadFile(filepath.Join(root, "python/documents.py"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("python/documents.py", parser, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join("data", "app.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := New(config.Config{Port: "8080", SyncInterval: time.Hour}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Handler()
	upload := func(title string) *httptest.ResponseRecorder {
		var content bytes.Buffer
		form := multipart.NewWriter(&content)
		file, err := form.CreateFormFile("file", "synthetic-sop.md")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("# P1 calibration\nConfirm media before calibration. Contact synthetic@example.invalid.\n"))
		_ = form.WriteField("product", "P1")
		_ = form.WriteField("title", title)
		_ = form.Close()
		req := httptest.NewRequest("POST", "/api/knowledge/import", &content)
		req.Host = "localhost:8080"
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.Header.Set("X-CSRF-Token", service.csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := upload("Calibration SOP")
	if first.Code != 201 {
		t.Fatalf("import: %d %s", first.Code, first.Body.String())
	}
	var entry knowledge.Entry
	if err := json.Unmarshal(first.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Status != "draft" || strings.Contains(first.Body.String(), "synthetic@example.invalid") || len(entry.Blocks) != 1 {
		t.Fatal("import did not produce redacted draft")
	}
	sources, err := db.KnowledgeSources(context.Background(), "", "P1", "calibration", 5)
	if err != nil || len(sources) != 0 {
		t.Fatal("unreviewed document entered recommendations")
	}
	duplicate := upload("Calibration SOP")
	var again knowledge.Entry
	_ = json.Unmarshal(duplicate.Body.Bytes(), &again)
	if duplicate.Code != 201 || again.ID != entry.ID {
		t.Fatal("duplicate import created new version")
	}
	corrected := upload("Corrected title")
	if corrected.Code != 409 {
		t.Fatal("duplicate metadata change silently discarded")
	}
	if _, err := os.Stat(filepath.Join("data", "knowledge", entry.SourceHash+".md")); err != nil {
		t.Fatal("original document not retained")
	}
}

func TestBubTaskStatusMatchesDetailAndSurvivesRecovery(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Port: "8080", SyncInterval: time.Hour, Bub: config.Model{Enabled: true, Name: "mock"}}
	service, err := New(cfg, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	db.Reserve(ctx, []string{"1"})
	snap := ticket.Snapshot{ID: "1", DepartmentID: "7", Complete: true, Subject: "synthetic", Messages: []ticket.Message{{ID: "m", Role: "customer", Body: "Help"}}}
	db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{"ticketNumber":"123"}`)}, snap)
	record, _ := db.Get(ctx, "1")
	req, _, err := requestForRecord(ctx, db, cfg, record, "bub", DevelopmentRound)
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Handler()
	check := func(want string, running int) {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost:8080/api/status", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var response struct {
			Tasks bubTasks `json:"bub_tasks"`
		}
		json.Unmarshal(w.Body.Bytes(), &response)
		if response.Tasks.Counts[want] != 1 || len(response.Tasks.Active) != running {
			t.Fatalf("unexpected tasks: %s", w.Body.String())
		}
		states, views, e := service.recordViews(ctx, record)
		if e != nil || views["bub"].TaskState != want || states["bub"].TaskState != want {
			t.Fatalf("states disagree: %v %v %v", states, views, e)
		}
		if running == 1 && response.Tasks.Active[0].TicketNumber != "123" {
			t.Fatal("missing ticket number")
		}
	}
	check("pending", 0)
	run, _, _ := db.StartAnalysis(ctx, req, false)
	check("running", 1)
	db.FinishAnalysis(ctx, run.ID, "", nil, fmt.Errorf("Bub: model_rate_limited; HTTP 429; request_id=req-safe"))
	check("retry_waiting", 0)
	run, _, _ = db.StartAnalysis(ctx, req, true)
	check("running", 1)
	db.RecoverAnalysis(ctx)
	check("retry_waiting", 0)
	run, _, _ = db.StartAnalysis(ctx, req, true)
	db.FinishAnalysis(ctx, run.ID, "mock", json.RawMessage(`{"summary":{"device_info":[],"customer_feedback":[],"support_responses":[]}}`), nil)
	states, _, _ := service.recordViews(ctx, record)
	if states["bub"].TaskState != "success" || states["bub"].Attempts != 3 {
		t.Fatalf("success lost: %v", states)
	}
}
