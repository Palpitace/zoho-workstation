// input: 隔离 SQLite、合成 Jev HTTP 服务和仅接收 stdin 的临时 Python 入口。
// output: Bub 先总结、Jev 只读当前总结，失败等待、持续处理及缓存复用的公共边界证据。
// pos: 总结到评估流水线集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/app"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
	"zoho-workstation/internal/zoho"
)

func TestAutomaticJevWaitsForCurrentBubSummary(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal(err)
	}
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprintf("bub_success=%t", success), func(t *testing.T) {
			t.Chdir(t.TempDir())
			ctx := context.Background()
			if output, err := exec.Command(python, "-m", "venv", "--without-pip", "python/.venv").CombinedOutput(); err != nil {
				t.Fatalf("isolated Python: %v %s", err, output)
			}
			entry := `import json, os, sys, time
request = json.load(sys.stdin)
with open("captured-" + request["input"]["ticket_id"] + ".json", "w") as f:
    json.dump(request, f)
while not os.path.exists("release"):
    time.sleep(0.01)
if os.path.exists("fail"):
    print(json.dumps({"ok": False, "error": "model_upstream_failed"}))
else:
    print(json.dumps({"ok": True, "returned_model": "synthetic-bub-actual", "result": {
        "device_info": [],
        "customer_feedback": [{"text": "Customer has not upgraded; disconnections persist.", "message_ids": ["m2"]}],
        "support_responses": [{"text": "Support suggested upgrading; execution is unconfirmed.", "message_ids": ["m1"]}]
    }}))
`
			if err := os.WriteFile("python/main.py", []byte(entry), 0600); err != nil {
				t.Fatal(err)
			}
			if !success {
				if err := os.WriteFile("fail", nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int32
			states := make(chan json.RawMessage, 10)
			external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/v2/token":
					fmt.Fprint(w, `{"access_token":"synthetic"}`)
				case "/jev":
					calls.Add(1)
					var request struct {
						State     json.RawMessage `json:"state"`
						Questions map[string]struct {
							Criteria map[string]string `json:"criteria"`
						} `json:"questions"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					states <- request.State
					answers := map[string]any{}
					for key, q := range request.Questions {
						p := map[string]float64{}
						for choice := range q.Criteria {
							p[choice] = 0
						}
						p["unknown"] = 1
						answers[key] = map[string]any{"type": "choice", "choice": "unknown", "confidence": 1, "probabilities": p}
					}
					json.NewEncoder(w).Encode(map[string]any{"model": "synthetic-jev-actual", "answers": answers, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
				default:
					w.WriteHeader(503)
				}
			}))
			defer external.Close()
			db, err := store.Open(filepath.Join("data", "app.db"), "7")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.Reserve(ctx, []string{"1", "2", "3", "4"}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"1", "2", "3", "4"} {
				snapshot := ticket.Snapshot{ID: id, DepartmentID: "7", Subject: "Synthetic issue", Complete: id != "4", Messages: []ticket.Message{
					{ID: "m1", Role: "agent", Body: "Please upgrade firmware."},
					{ID: "m2", Role: "customer", Body: "RAW_ONLY_SENTINEL I have not upgraded. Printer still disconnects."},
				}}
				if err = db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{}`)}, snapshot); err != nil {
					t.Fatal(err)
				}
			}
			if err = db.StopSync(ctx, "3", "ticket confirmed deleted in Zoho"); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Environment: "development", Port: "8080", SyncInterval: time.Hour, SyncLookback: time.Hour,
				Zoho: config.Zoho{BaseURL: external.URL, AccountsURL: external.URL, DepartmentID: "7"},
				Jev:  config.Model{Enabled: true, Name: "synthetic-jev", Key: "synthetic", URL: external.URL + "/jev"},
				Bub:  config.Model{Enabled: true, Name: "synthetic:bub", Key: "synthetic", URL: "https://synthetic.invalid"}}
			service, err := app.New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
			if err != nil {
				t.Fatal(err)
			}
			if err = service.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			read := func(id string) map[string]app.AIStatus {
				req := httptest.NewRequest("GET", "/api/tickets/"+id, nil)
				req.Host = "localhost:8080"
				w := httptest.NewRecorder()
				service.Handler().ServeHTTP(w, req)
				var got struct {
					AI map[string]app.AIStatus `json:"ai"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
					t.Fatalf("ticket unavailable: %d", w.Code)
				}
				return got.AI
			}
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				if calls.Load() > 0 {
					t.Fatal("Jev called before Bub summary completed")
				}
				if _, e := os.Stat("captured-1.json"); e == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			captured, err := os.ReadFile("captured-1.json")
			if err != nil {
				t.Fatal("Bub did not start independently")
			}
			if !strings.Contains(string(captured), "RAW_ONLY_SENTINEL") {
				t.Fatal("Bub lost complete conversation")
			}
			if strings.Contains(string(captured), `"jev_reference": {`) {
				t.Fatal("Bub must not receive Jev assessment")
			}
			if state := read("1"); state["jev"].Status != "waiting" {
				t.Fatalf("missing waiting state: %+v", state)
			}
			if err = os.WriteFile("release", nil, 0600); err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); {
				s := read("2")
				if success && s["jev"].Status == "success" && s["bub"].Status == "success" {
					break
				}
				if !success && s["bub"].Status == "failed" {
					budget, e := db.AnalysisBudget(ctx, app.DevelopmentRound)
					if e != nil {
						t.Fatal(e)
					}
					if budget.Total == 4 && read("2")["bub"].Status == "failed" {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if success {
				for _, id := range []string{"1", "2"} {
					s := read(id)
					if s["jev"].Status != "success" || s["bub"].Status != "success" {
						t.Fatalf("pipeline stalled: %+v", s)
					}
				}
				for i := 0; i < 2; i++ {
					select {
					case state := <-states:
						if strings.Contains(string(state), "RAW_ONLY_SENTINEL") || !strings.Contains(string(state), "Customer has not upgraded; disconnections persist.") {
							t.Fatal("Jev must receive summary, not raw conversation")
						}
					case <-time.After(time.Second):
						t.Fatal("missing Jev input")
					}
				}
			} else {
				if s := read("2"); s["jev"].Status != "waiting" || s["bub"].Status != "failed" || calls.Load() != 0 {
					t.Fatalf("failed Bub must not permit Jev: %+v", s)
				}
			}
			for _, id := range []string{"3", "4"} {
				s := read(id)
				if s["bub"].AutomaticEligible {
					t.Fatal("stopped/incomplete eligible")
				}
				if _, e := os.Stat("captured-" + id + ".json"); !os.IsNotExist(e) {
					t.Fatal("stopped/incomplete sent to Bub")
				}
			}
			service.Close()
			if success {
				service, err = app.New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
				if err != nil {
					t.Fatal(err)
				}
				if err = service.Start(ctx); err != nil {
					t.Fatal(err)
				}
				defer service.Close()
				time.Sleep(100 * time.Millisecond)
				if calls.Load() != 2 {
					t.Fatal("restart repeated successful evaluations")
				}

				service.Close()
				// A replacement summary for unchanged conversation invalidates
				// only the downstream assessment, even if old Jev succeeded.
				record, e := db.Get(ctx, "1")
				if e != nil {
					t.Fatal(e)
				}
				seedSummary(t, db, record.Snapshot, record.Raw, cfg.Bub.Name)
				state := read("1")
				if !state["jev"].Stale || state["bub"].Stale {
					t.Fatal("summary replacement did not invalidate only Jev")
				}
				service, err = app.New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
				if err != nil {
					t.Fatal(err)
				}
				if err = service.Start(ctx); err != nil {
					t.Fatal(err)
				}
				defer service.Close()
				for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
					if read("1")["jev"].Status == "success" {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				if state := read("1"); state["jev"].Status != "success" || state["jev"].Stale || calls.Load() != 3 {
					t.Fatal("replacement summary was not assessed")
				}
				// Repeating identical summary content must not stale current Jev.
				seedSummary(t, db, record.Snapshot, record.Raw, cfg.Bub.Name)
				if read("1")["jev"].Stale {
					t.Fatal("identical summary rerun invalidated Jev")
				}
				service.Close()
				record.Messages[1].Body += " Now I upgraded, but it still fails."
				if err = db.Save(ctx, record.Raw, record.Snapshot); err != nil {
					t.Fatal(err)
				}
				state = read("1")
				if state["jev"].Status != "waiting" || !state["jev"].Stale || !state["bub"].Stale {
					t.Fatal("new conversation reused old summary or assessment")
				}
				// Disabled Bub must not cause a hidden raw-conversation fallback.
				cfg.Bub.Enabled = false
				service, err = app.New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
				if err != nil {
					t.Fatal(err)
				}
				if err = service.Start(ctx); err != nil {
					t.Fatal(err)
				}
				defer service.Close()
				statusRequest := httptest.NewRequest("GET", "/api/status", nil)
				statusRequest.Host = "localhost:8080"
				statusResponse := httptest.NewRecorder()
				service.Handler().ServeHTTP(statusResponse, statusRequest)
				var status struct {
					Token string `json:"csrf_token"`
				}
				if statusResponse.Code != 200 || json.Unmarshal(statusResponse.Body.Bytes(), &status) != nil {
					t.Fatal("status unavailable while waiting")
				}
				request := httptest.NewRequest("POST", "/api/tickets/1/analysis/jev", nil)
				request.Host = "localhost:8080"
				request.Header.Set("X-CSRF-Token", status.Token)
				response := httptest.NewRecorder()
				service.Handler().ServeHTTP(response, request)
				if response.Code != 409 {
					t.Fatalf("manual Jev without current summary: %d", response.Code)
				}
				time.Sleep(50 * time.Millisecond)
				if calls.Load() != 3 {
					t.Fatal("Jev fell back to raw conversation")
				}
			}
		})
	}
}

// Seed a current summary through the public store boundary for HTTP fixtures.
func seedSummary(t *testing.T, db *store.Store, snapshot ticket.Snapshot, raw ticket.RawSnapshot, model string) string {
	t.Helper()
	input, conversationHash, err := analysis.Input(snapshot, raw)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := analysis.BubInput(input, conversationHash)
	if err != nil {
		t.Fatal(err)
	}
	req := store.AnalysisRequest{TicketID: snapshot.ID, Kind: "bub", InputHash: hash, SchemaVersion: analysis.BubSchema, RequestedModel: model, Round: app.DevelopmentRound}
	run, ok, err := db.StartAnalysis(context.Background(), req, true)
	if err != nil || !ok {
		t.Fatalf("seed summary: %v", err)
	}
	result := json.RawMessage(`{"summary":{"device_info":[],"customer_feedback":[],"support_responses":[]}}`)
	if err = db.FinishAnalysis(context.Background(), run.ID, model, result, nil); err != nil {
		t.Fatal(err)
	}
	_, jevHash, err := analysis.SummaryInput(snapshot.ID, conversationHash, hash, analysis.BubSchema, model, model, result)
	if err != nil {
		t.Fatal(err)
	}
	return jevHash
}
