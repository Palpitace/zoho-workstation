// input: 隔离 SQLite、合成 Jev HTTP 服务和仅接收 stdin 的临时 Python 入口。
// output: 全部有效工单连续分析、三段 Bub 总结等待首次 Jev 结束、完整会话及同版参考/失败空参考的公共边界证据。
// pos: 分析参考调度集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
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
	"reflect"
	"sync"
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

func TestAutomaticBubReceivesCompletedJevReference(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python toolchain is required for the subprocess contract test")
	}
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprintf("jev_success=%t", success), func(t *testing.T) {
			t.Chdir(t.TempDir())
			ctx := context.Background()
			for _, dir := range []string{"python/.venv/bin", "data/review"} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(python, "python/.venv/bin/python"); err != nil {
				t.Fatal(err)
			}
			// This process records its public input and returns a valid synthetic
			// summary; it neither imports the SDK nor opens the database or network.
			const entry = `import json, os, sys
request = json.load(sys.stdin)
with open("captured.tmp", "w", encoding="utf-8") as output:
    json.dump(request, output)
os.replace("captured.tmp", "captured-" + request["input"]["ticket_id"] + ".json")
print(json.dumps({"ok": True, "returned_model": "synthetic-bub-actual", "result": {
    "device_info": [],
    "customer_feedback": [{"text": "Customer has not upgraded. Printer still disconnects.", "message_ids": ["m2"]}],
    "support_responses": [{"text": "Support suggested upgrading firmware; execution is not confirmed.", "message_ids": ["m1"]}]
}}))
`
			if err := os.WriteFile("python/main.py", []byte(entry), 0600); err != nil {
				t.Fatal(err)
			}
			if !success {
				if err := os.WriteFile("data/review/manifest.json", []byte(`[{"ticket_id":"1","split":"holdout"}]`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var jevCalls atomic.Int32
			external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/v2/token":
					fmt.Fprint(w, `{"access_token":"synthetic"}`)
				case "/api/v1/tickets/1":
					w.WriteHeader(503) // Preserve the saved conversation during sync.
				case "/jev":
					if jevCalls.Add(1) == 1 {
						close(started)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					if !success {
						w.WriteHeader(503)
						return
					}
					var request struct {
						Questions map[string]struct {
							Criteria map[string]string `json:"criteria"`
						} `json:"questions"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					answers := map[string]any{}
					for key, question := range request.Questions {
						probabilities := map[string]float64{}
						for choice := range question.Criteria {
							probabilities[choice] = 0
						}
						probabilities["unknown"] = 1
						answers[key] = map[string]any{"type": "choice", "choice": "unknown", "confidence": 1, "probabilities": probabilities}
					}
					json.NewEncoder(w).Encode(map[string]any{"model": "synthetic-jev-actual", "answers": answers, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
				default:
					w.WriteHeader(404)
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
			snapshot := ticket.Snapshot{ID: "1", DepartmentID: "7", Subject: "Synthetic issue", Complete: true, Messages: []ticket.Message{
				{ID: "m1", Role: "agent", Body: "Please upgrade firmware."},
				{ID: "m2", Role: "customer", Body: "I have not upgraded. Printer still disconnects."},
			}}
			raw := ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"1"}`)}
			if err = db.Save(ctx, raw, snapshot); err != nil {
				t.Fatal(err)
			}
			if success {
				for _, id := range []string{"2", "3", "4"} {
					other := snapshot
					other.ID = id
					other.Complete = id != "4"
					if e := db.Save(ctx, raw, other); e != nil {
						t.Fatal(e)
					}
				}
				if e := db.StopSync(ctx, "3", "ticket confirmed deleted in Zoho"); e != nil {
					t.Fatal(e)
				}
			}
			conversation, hash, err := analysis.Input(snapshot, raw)
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Environment: "development", Port: "8080", SyncInterval: time.Hour, SyncLookback: time.Hour,
				Zoho: config.Zoho{BaseURL: external.URL, AccountsURL: external.URL, DepartmentID: "7"},
				Jev:  config.Model{Enabled: true, Name: "synthetic-jev", Key: "synthetic", URL: external.URL + "/jev"},
				Bub:  config.Model{Enabled: true, Name: "synthetic:bub", Key: "synthetic", URL: "https://synthetic.invalid"}}
			if !success {
				old, begun, e := db.StartAnalysis(ctx, store.AnalysisRequest{TicketID: "1", Kind: "jev", InputHash: "old-conversation", SchemaVersion: analysis.JevSchema, RequestedModel: cfg.Jev.Name, Round: app.DevelopmentRound}, false)
				if e != nil || !begun {
					t.Fatalf("seed old result: %v", e)
				}
				if e = db.FinishAnalysis(ctx, old.ID, "old-model", json.RawMessage(`{"answers":{"priority":{"choice":"high"}}}`), nil); e != nil {
					t.Fatal(e)
				}
			}
			service, err := app.New(cfg, db, zoho.New(cfg.Zoho, external.Client()))
			if err != nil {
				t.Fatal(err)
			}
			if err = service.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer func() {
				unblock()
				service.Close()
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("automatic Jev request did not start")
			}
			for deadline := time.Now().Add(200 * time.Millisecond); time.Now().Before(deadline); {
				if _, e := os.Stat("captured-1.json"); e == nil {
					t.Fatal("Bub started before the first Jev attempt completed")
				}
				time.Sleep(5 * time.Millisecond)
			}
			unblock()
			var captured []byte
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				captured, err = os.ReadFile("captured-1.json")
				if err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err != nil {
				t.Fatal("Bub did not run after Jev completed")
			}
			var envelope struct {
				Input struct {
					Messages         []ticket.Message       `json:"messages"`
					ConversationHash string                 `json:"conversation_hash"`
					JevReference     *analysis.JevReference `json:"jev_reference"`
				} `json:"input"`
				Schema string `json:"schema_version"`
			}
			if err = json.Unmarshal(captured, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Schema != analysis.BubSchema || envelope.Input.ConversationHash != hash || !reflect.DeepEqual(envelope.Input.Messages, snapshot.Messages) {
				t.Fatal("Bub did not receive the complete versioned conversation")
			}
			jev, err := db.Analysis(ctx, store.AnalysisRequest{TicketID: "1", Kind: "jev", InputHash: hash, SchemaVersion: analysis.JevSchema, RequestedModel: cfg.Jev.Name})
			if err != nil || jev.Current == nil {
				t.Fatalf("Jev attempt was not persisted: %v", err)
			}
			ref := envelope.Input.JevReference
			if success {
				if ref == nil || ref.Kind != "model_assessment" || ref.RunID != jev.Current.ID || ref.ConversationHash != hash || ref.SchemaVersion != analysis.JevSchema || ref.RequestedModel != cfg.Jev.Name || ref.ReturnedModel != "synthetic-jev-actual" || ref.Choices["priority"] != "unknown" {
					t.Fatal("Bub did not receive the successful same-version Jev reference")
				}
			} else if ref != nil || jev.Success == nil || !jev.Stale {
				t.Fatal("failed current Jev must leave old success stale and pass null reference")
			}
			_, bubHash, err := analysis.BubInput(conversation, hash, ref)
			if err != nil {
				t.Fatal(err)
			}
			var bub store.AnalysisView
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				bub, err = db.Analysis(ctx, store.AnalysisRequest{TicketID: "1", Kind: "bub", InputHash: bubHash, SchemaVersion: analysis.BubSchema, RequestedModel: cfg.Bub.Name})
				if err == nil && bub.Success != nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err != nil || bub.Success == nil || bub.Stale {
				t.Fatalf("subprocess result was not saved for its exact input: %+v %v", bub, err)
			}
			if success {
				// With a one-hour Zoho interval, both capabilities must drain the
				// next complete ticket without waiting for another poll.
				var state struct {
					AI map[string]struct {
						Status   string `json:"status"`
						Eligible bool   `json:"automatic_eligible"`
					} `json:"ai"`
				}
				read := func(id string) {
					req := httptest.NewRequest("GET", "/api/tickets/"+id, nil)
					req.Host = "localhost:8080"
					w := httptest.NewRecorder()
					service.Handler().ServeHTTP(w, req)
					if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil {
						t.Fatal("cannot read ticket state")
					}
				}
				for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
					read("2")
					if state.AI["jev"].Status == "success" && state.AI["bub"].Status == "success" {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if state.AI["jev"].Status != "success" || state.AI["bub"].Status != "success" {
					t.Fatal("analysis stalled until next sync interval")
				}
				for _, id := range []string{"3", "4"} {
					read(id)
					if state.AI["jev"].Eligible || state.AI["bub"].Eligible {
						t.Fatal("stopped/incomplete ticket eligible")
					}
					if _, e := os.Stat("captured-" + id + ".json"); !os.IsNotExist(e) {
						t.Fatal("stopped/incomplete ticket sent to Bub")
					}
				}
			}

		})
	}
}
