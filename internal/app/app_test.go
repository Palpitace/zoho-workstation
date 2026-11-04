// input: 隔离 SQLite、合成 Zoho/Jev HTTP 服务及本机 API 请求。
// output: 自动失败恢复、独立查看、管理防护及跨重启缓存/预算的行为证据。
// pos: 单服务生命周期集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app_test

import (
	"context"
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
	os.MkdirAll("data/review", 0700)
	os.WriteFile("data/review/manifest.json", []byte(`[{"ticket_id":"1","split":"development"}]`), 0600)
	cfg := config.Config{Environment: "development", Port: "8080", DatabasePath: dbPath, SyncInterval: 30 * time.Millisecond, SyncLookback: time.Hour, Zoho: config.Zoho{BaseURL: external.URL, AccountsURL: external.URL, DepartmentID: "7"}, Jev: config.Model{Enabled: true, Name: "synthetic", Key: "synthetic", URL: external.URL + "/jev"}}
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
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		json.Unmarshal(call("GET", "/api/status", "", "").Body.Bytes(), &status)
		if status.AI["jev"].Success == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.AI["jev"].Success != 1 || models.Load() != 2 || syncs.Load() == 0 || status.Budget.Total != 2 {
		t.Fatalf("automatic recovery: requests=%d sync=%d budget=%d", models.Load(), syncs.Load(), status.Budget.Total)
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
