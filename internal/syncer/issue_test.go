// input: 固定样本的合并/删除/部门迁移、不可见及缺少线程响应。
// output: 单票失败独立重试、历史保留、合并/删除/部门停止和跨重启水位推进证据。
// pos: Issue 1 的同步回归测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package syncer_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/syncer"
	"zoho-workstation/internal/ticket"
	"zoho-workstation/internal/zoho"
)

func TestIssueFailureDoesNotBlockWatermark(t *testing.T) {
	for _, mode := range []string{"moved_before", "moved_during", "deleted", "deleted_during", "merged", "missing_404", "missing_403", "incomplete", "initial_incomplete"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var reads, conversations atomic.Int32
			var repaired atomic.Bool
			stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/v2/token":
					fmt.Fprint(w, `{"access_token":"synthetic"}`)
				case "/api/v1/tickets/search":
					fmt.Fprint(w, `{"data":[]}`) // Absence alone is not a move.
				case "/api/v1/tickets/1", "/api/v1/tickets/2":
					id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
					department, count := "7", "0"
					deleted := false
					if id == "1" {
						n := reads.Add(1)
						if !repaired.Load() {
							switch mode {
							case "deleted":
								deleted = true
							case "deleted_during":
								deleted = n%2 == 0
							case "moved_before":
								department = "8"
							case "moved_during":
								if n%2 == 0 {
									department = "8"
								}
							case "merged", "missing_404":
								w.WriteHeader(404)
								return
							case "missing_403":
								w.WriteHeader(403)
								return
							case "incomplete", "initial_incomplete":
								count = "4"
							}
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"isDeleted": deleted, "id": id, "departmentId": department, "subject": "new snapshot", "createdTime": stamp, "modifiedTime": stamp, "threadCount": count, "commentCount": "0"})
				case "/api/v1/tickets/1/History":
					if mode == "merged" {
						fmt.Fprint(w, `{"data":[{"eventName":"TicketMergedChild"}]}`)
					} else {
						fmt.Fprint(w, `{"data":[]}`)
					}
				case "/api/v1/tickets/1/threads":
					conversations.Add(1)
					if strings.Contains(mode, "incomplete") && !repaired.Load() {
						fmt.Fprint(w, `{"data":[{"id":"10"},{"id":"11"},{"id":"12"}]}`)
					} else {
						fmt.Fprint(w, `{"data":[]}`)
					}
				case "/api/v1/tickets/1/threads/10", "/api/v1/tickets/1/threads/11", "/api/v1/tickets/1/threads/12":
					json.NewEncoder(w).Encode(map[string]string{"id": r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "content": "synthetic", "createdTime": stamp})
				case "/api/v1/tickets/1/comments", "/api/v1/tickets/2/threads", "/api/v1/tickets/2/comments":
					fmt.Fprint(w, `{"data":[]}`)
				default:
					t.Errorf("outside frozen scope: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "app.db")
			db, err := store.Open(path, "7")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			if err = db.Reserve(ctx, []string{"1", "2"}); err != nil {
				t.Fatal(err)
			}
			if mode != "initial_incomplete" {
				for _, id := range []string{"1", "2"} {
					if err = db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"` + id + `"}`)}, ticket.Snapshot{ID: id, DepartmentID: "7", Subject: "historical snapshot", Complete: true}); err != nil {
						t.Fatal(err)
					}
				}
			}
			client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
			opts := syncer.Options{MaxTickets: 1}
			syncer.Run(ctx, client, db, opts)
			state, _ := db.State(ctx)
			if state.Watermark != "" || state.BatchUpper == "" {
				t.Fatal("unfinished batch advanced")
			}
			upper := state.BatchUpper
			db.Close()
			db, err = store.Open(path, "7")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = syncer.Run(ctx, client, db, opts); err != nil {
				t.Fatal(err)
			}
			state, _ = db.State(ctx)
			if state.Watermark != upper || state.BatchUpper != "" {
				t.Fatalf("one failed ticket blocked durable cursor: %+v", state)
			}
			bad, _ := db.Get(ctx, "1")
			body, _ := json.Marshal(bad)
			var view map[string]any
			json.Unmarshal(body, &view)
			moved := strings.HasPrefix(mode, "moved") || strings.HasPrefix(mode, "deleted") || mode == "merged"
			if (view["sync_stopped"] == true) != moved || bad.SyncError == "" {
				t.Fatalf("wrong move/failure classification: %s", body)
			}
			if strings.Contains(mode, "incomplete") && !strings.Contains(bad.SyncError, "threadCount_mismatch") {
				t.Fatal("incomplete sync did not preserve its diagnostic reason")
			}
			if mode != "initial_incomplete" && bad.Subject != "historical snapshot" {
				t.Fatal("old snapshot overwritten")
			}
			if mode == "initial_incomplete" && (bad.Complete || bad.Pending || len(bad.Messages) != 3) {
				t.Fatal("partial evidence lost or declared complete")
			}
			if (mode == "moved_before" || mode == "deleted") && conversations.Load() != 0 {
				t.Fatal("out-of-department conversation fetched")
			}
			attempts := reads.Load()
			if _, err = syncer.Run(ctx, client, db, opts); err != nil {
				t.Fatal(err)
			}
			if reads.Load() != attempts {
				t.Fatal("stopped or cooling-down ticket re-fetched")
			}
			repaired.Store(true)
			// Age only this synthetic DB's durable retry timestamp, without a runtime clock hook.
			sqlDB, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = sqlDB.Exec("UPDATE tickets SET checked_at=? WHERE id='1'", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))
			sqlDB.Close()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = syncer.Run(ctx, client, db, opts); err != nil {
				t.Fatal(err)
			}
			bad, _ = db.Get(ctx, "1")
			if moved {
				if reads.Load() != attempts || bad.Subject != "historical snapshot" {
					t.Fatal("confirmed move resumed reads")
				}
			} else if bad.SyncError != "" || !bad.Complete || bad.Subject != "new snapshot" {
				t.Fatal("retry was lost after watermark advanced")
			}
		})
	}
}

func TestSlowBatchDoesNotRetryAlreadyCoveredFailures(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Reserve(ctx, []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	if err = db.Failure(ctx, "1", "conversation incomplete"); err != nil {
		t.Fatal(err)
	}
	upper := time.Now().Add(-20 * time.Minute).UTC().Format(time.RFC3339Nano)
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqlDB.Exec("UPDATE sync_state SET batch_upper=?", upper); err != nil {
		t.Fatal(err)
	}
	if _, err = sqlDB.Exec("UPDATE tickets SET checked_at=? WHERE id='1'", time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	var failedReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/v2/token":
			fmt.Fprint(w, `{"access_token":"synthetic"}`)
		case "/api/v1/tickets/1":
			failedReads.Add(1)
			w.WriteHeader(404)
		case "/api/v1/tickets/1/History":
			fmt.Fprint(w, `{"data":[]}`)
		case "/api/v1/tickets/search":
			fmt.Fprint(w, `{"data":[]}`)
		case "/api/v1/tickets/2":
			fmt.Fprint(w, `{"id":"2","departmentId":"7","threadCount":"0","commentCount":"0","createdTime":"2020-01-01T00:00:00Z","modifiedTime":"2020-01-01T00:00:00Z"}`)
		case "/api/v1/tickets/2/threads", "/api/v1/tickets/2/comments":
			fmt.Fprint(w, `{"data":[]}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
	result, err := syncer.Run(ctx, client, db, syncer.Options{MaxTickets: 1})
	state, _ := db.State(ctx)
	if err != nil || result.Fetched != 1 || result.Remaining != 0 || failedReads.Load() != 0 || state.Watermark != upper {
		t.Fatalf("long batch retried covered failure: %+v %+v %v", result, state, err)
	}
	_, err = syncer.Run(ctx, client, db, syncer.Options{MaxTickets: 1})
	if err == nil || failedReads.Load() != 1 {
		t.Fatal("next window lost due retry")
	}
}
