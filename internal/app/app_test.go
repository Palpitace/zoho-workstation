// input: 隔离 SQLite、合成 Zoho/Jev HTTP 服务及已有 Bub 总结及本机 API 请求。
// output: 不受研发名单限制的自动失败恢复、独立查看、管理防护及跨重启缓存/计数及超旧额度继续处理的行为证据。
// pos: 单服务生命周期集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"zoho-workstation/internal/app"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
	"zoho-workstation/internal/zoho"
)

func TestAutomaticAnalysisRecoveryAndProtectedOperations(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	var models, syncs atomic.Int32
	held := make(chan struct{})
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/v2/token":
			fmt.Fprint(w, `{"access_token":"synthetic"}`)
		case "/api/v1/tickets":
			fmt.Fprint(w, `{"data":[]}`)
		case "/api/v1/tickets/1":
			syncs.Add(1)
			w.WriteHeader(503)
		case "/jev":
			n := models.Add(1)
			if n == 1 {
				w.WriteHeader(503)
				return
			}
			if n == 3 {
				select {
				case <-held:
				case <-r.Context().Done():
					return
				}
			}
			var req struct {
				Questions map[string]struct {
					Criteria map[string]string `json:"criteria"`
				} `json:"questions"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			answers := map[string]any{}
			for key, q := range req.Questions {
				p := map[string]float64{}
				for k := range q.Criteria {
					p[k] = 0
				}
				p["unknown"] = 1
				answers[key] = map[string]any{"type": "choice", "choice": "unknown", "confidence": 1, "probabilities": p}
			}
			json.NewEncoder(w).Encode(map[string]any{"model": "synthetic-actual", "answers": answers, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer external.Close()
	dbPath := filepath.Join("data", "app.db")
	db, err := store.Open(dbPath, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Reserve(ctx, []string{"1", "2"})
	snapshot := ticket.Snapshot{ID: "1", DepartmentID: "7", Subject: "Synthetic issue", Status: "Open", Complete: true, Messages: []ticket.Message{{ID: "m1", Role: "customer", Body: "I have not upgraded"}}}
	if err = db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"1"}`)}, snapshot); err != nil {
		t.Fatal(err)
	}
	// Existing history beyond both retired limits must not block new work.
	for i := 0; i < 61; i++ {
		run, started, e := db.StartAnalysis(ctx, store.AnalysisRequest{TicketID: "1", Kind: "jev", InputHash: "historical", SchemaVersion: "v1", RequestedModel: "synthetic", Round: app.DevelopmentRound}, true)
		if e != nil || !started {
			t.Fatalf("seed historical requests: %v", e)
		}
		if e = db.FinishAnalysis(ctx, run.ID, "synthetic", json.RawMessage(`{}`), nil); e != nil {
			t.Fatal(e)
		}
	}
	os.MkdirAll("data/review", 0700)
	os.WriteFile("data/review/manifest.json", []byte(`[{"ticket_id":"1","split":"holdout"}]`), 0600)
	cfg := config.Config{Environment: "development", Port: "8080", DatabasePath: dbPath, SyncInterval: 30 * time.Millisecond, SyncLookback: time.Hour, Zoho: config.Zoho{BaseURL: external.URL, AccountsURL: external.URL, DepartmentID: "7"}, Bub: config.Model{Name: "synthetic"}, Jev: config.Model{Enabled: true, Name: "synthetic", Key: "synthetic", URL: external.URL + "/jev"}}
	seedSummary(t, db, snapshot, ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"1"}`)}, "synthetic")
	service, err := app.New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	call := func(method, path, token, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Host = "127.0.0.1:8080"
		req.Header.Set("X-CSRF-Token", token)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		service.Handler().ServeHTTP(rec, req)
		return rec
	}
	var status struct {
		CSRF   string       `json:"csrf_token"`
		Budget store.Budget `json:"budget"`
		AI     map[string]struct {
			Success int `json:"success"`
		} `json:"ai"`
	}
	fixture, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	aged := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		// Simulate time passing in persisted failure history; production cooldown remains real.
		if !aged {
			result, err := fixture.Exec("UPDATE ai_runs SET completed_at=? WHERE status='failed'", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))
			if err != nil {
				t.Fatal(err)
			}
			count, _ := result.RowsAffected()
			aged = count > 0
		}
		json.Unmarshal(call("GET", "/api/status", "", "").Body.Bytes(), &status)
		if status.AI["jev"].Success == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.AI["jev"].Success != 1 || models.Load() != 2 || syncs.Load() == 0 || status.Budget.Total != 64 {
		t.Fatalf("automatic recovery: requests=%d sync=%d budget=%d", models.Load(), syncs.Load(), status.Budget.Total)
	}
	var pending struct {
		AI map[string]struct {
			Status string `json:"status"`
		} `json:"ai"`
	}
	if e := json.Unmarshal(call("GET", "/api/tickets/2", "", "").Body.Bytes(), &pending); e != nil || pending.AI["jev"].Status != "pending" {
		t.Fatal("old quota still masks pending analysis")
	}
	var statusJSON map[string]json.RawMessage
	json.Unmarshal(call("GET", "/api/status", "", "").Body.Bytes(), &statusJSON)
	var counts map[string]int
	json.Unmarshal(statusJSON["budget"], &counts)
	if _, ok := counts["total_limit"]; ok {
		t.Fatal("status still advertises removed quota")
	}
	if r := call("POST", "/api/sync", "", ""); r.Code != 403 {
		t.Fatal("missing token accepted")
	}
	if r := call("POST", "/api/sync", status.CSRF, "https://evil.invalid"); r.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	if r := call("GET", "/api/tickets/2", "", ""); r.Code != 200 {
		t.Fatal("reserved pending ticket is not viewable")
	}
	if r := call("POST", "/api/tickets/2/analysis/jev", status.CSRF, ""); r.Code != 404 {
		t.Fatal("unsaved ticket sent to analysis")
	}
	if r := call("POST", "/api/tickets/1/analysis/jev", status.CSRF, ""); r.Code != 202 {
		t.Fatalf("manual rerun: %d %s", r.Code, r.Body.String())
	}
	if r := call("POST", "/api/tickets/1/analysis/jev", status.CSRF, ""); r.Code != 409 {
		t.Fatal("overlapping rerun accepted")
	}
	if r := call("GET", "/api/tickets/1", "", ""); r.Code != 200 {
		t.Fatal("model run blocked detail")
	}
	close(held)
	service.Close()
	service, err = app.New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	time.Sleep(80 * time.Millisecond)
	if models.Load() > 3 {
		t.Fatal("successful input unnecessarily rerun after restart")
	}
	if r := call("GET", "/api/tickets/1/analysis", "", ""); r.Code != 200 {
		t.Fatal("saved result missing after restart")
	}
}
