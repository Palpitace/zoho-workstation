// input: 隔离 SQLite 中相同能力的本地 invalid 与供应商 failed 记录、合成邮件引用。
// output: 恢复候选不混入无法自动处理的本地失败，列表不携带详情专用分段。
// pos: 状态与详情公开 HTTP 回归边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/app"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
)

func TestRecoveryCandidatesExcludeInvalidInputs(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Reserve(ctx, []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	var failedRun int64
	for _, id := range []string{"1", "2"} {
		raw := ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"` + id + `"}`), Threads: []json.RawMessage{json.RawMessage(`{"id":"1","contentType":"html","content":"<p>New</p><blockquote>Old</blockquote>"}`)}}
		snapshot := ticket.Snapshot{ID: id, DepartmentID: "7", Complete: true, Messages: []ticket.Message{{ID: "1", Source: "thread", Body: "New\nOld"}}}
		if err = db.Save(ctx, raw, snapshot); err != nil {
			t.Fatal(err)
		}
		input, hash, err := analysis.Input(snapshot, raw)
		if err != nil {
			t.Fatal(err)
		}
		_, hash, err = analysis.BubInput(input, hash)
		if err != nil {
			t.Fatal(err)
		}
		req := store.AnalysisRequest{TicketID: id, Kind: "bub", InputHash: hash, SchemaVersion: analysis.BubSchema, RequestedModel: "synthetic", Round: app.DevelopmentRound}
		if id == "1" {
			if err = db.RecordAnalysisFailure(ctx, req, errors.New("synthetic local validation")); err != nil {
				t.Fatal(err)
			}
		} else {
			run, started, err := db.StartAnalysis(ctx, req, false)
			if err != nil || !started {
				t.Fatalf("seed failed run: %v", err)
			}
			failedRun = run.ID
			if err = db.FinishAnalysis(ctx, run.ID, "", nil, errors.New("synthetic upstream failure")); err != nil {
				t.Fatal(err)
			}
		}
	}
	service, err := app.New(config.Config{Port: "8080", SyncInterval: time.Hour, Bub: config.Model{Enabled: true, Name: "synthetic"}}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) []byte {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "localhost:8080"
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("%s: HTTP %d", path, response.Code)
		}
		return response.Body.Bytes()
	}
	var status struct {
		Candidates map[string][]app.RetryCandidate `json:"retry_candidates"`
	}
	if err = json.Unmarshal(get("/api/status"), &status); err != nil {
		t.Fatal(err)
	}
	candidates := status.Candidates["bub"]
	if len(candidates) != 1 || candidates[0].TicketID != "2" || candidates[0].RunID != failedRun {
		t.Fatalf("local invalid contaminated recovery batch: %+v", candidates)
	}
	var list struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(get("/api/tickets"), &list); err != nil {
		t.Fatal(err)
	}
	for _, row := range list.Data {
		if _, present := row["message_segments"]; present {
			t.Fatal("list computed detail-only quote segments")
		}
	}
	var detail map[string]json.RawMessage
	if err = json.Unmarshal(get("/api/tickets/1"), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail["message_segments"]) == 0 {
		t.Fatal("detail lost quote segments")
	}
}
