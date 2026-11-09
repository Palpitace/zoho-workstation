// input: 隔离 SQLite、乱序候选和带同步屏障、429/取消的合成 Zoho 服务。
// output: 三路并发、批次上限、限流停发及水位/后续恢复的回归证据。
// pos: 同步请求并发边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package syncer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/syncer"
	"zoho-workstation/internal/zoho"
)

func TestBoundedConcurrentSyncAndRecovery(t *testing.T) {
	for _, mode := range []string{"ok", "429", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var mu sync.Mutex
			seen := map[string]bool{}
			active, peak := 0, 0
			gate := make(chan struct{})
			recovering := false
			stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/oauth/v2/token":
					fmt.Fprint(w, `{"access_token":"synthetic"}`)
				case strings.HasSuffix(r.URL.Path, "/threads") || strings.HasSuffix(r.URL.Path, "/comments"):
					fmt.Fprint(w, `{"data":[]}`)
				case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/"):
					id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
					mu.Lock()
					first := !seen[id]
					seen[id] = true
					isRecovery := recovering
					active++
					if active > peak {
						peak = active
					}
					if first && len(seen) == 3 {
						close(gate)
						if mode == "cancel" {
							cancel()
						}
					}
					mu.Unlock()
					defer func() { mu.Lock(); active--; mu.Unlock() }()
					if !isRecovery {
						select {
						case <-gate:
						case <-r.Context().Done():
							return
						}
						if mode == "429" {
							if id == "1" {
								w.Header().Set("Retry-After", "30")
								w.WriteHeader(429)
								return
							}
							<-r.Context().Done()
							return
						}
					}
					json.NewEncoder(w).Encode(map[string]string{"id": id, "departmentId": "7", "createdTime": stamp, "modifiedTime": stamp, "threadCount": "0", "commentCount": "0"})
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer remote.Close()
			db, err := store.Open(filepath.Join(t.TempDir(), "app.db"), "7")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ids := []string{"1", "2", "4", "3", "5", "6"}
			if err = db.Reserve(context.Background(), ids); err != nil {
				t.Fatal(err)
			}
			client := zoho.New(config.Zoho{BaseURL: remote.URL, AccountsURL: remote.URL, DepartmentID: "7"}, remote.Client())
			for _, invalid := range []int{-1, 6} {
				if _, err := syncer.Run(ctx, client, db, syncer.Options{Concurrency: invalid}); err == nil {
					t.Fatal("unbounded concurrency accepted")
				}
			}
			result, err := syncer.Run(ctx, client, db, syncer.Options{MaxTickets: 3, Concurrency: 3})
			state, _ := db.State(context.Background())
			mu.Lock()
			gotPeak, started := peak, len(seen)
			var unstarted []string
			for _, id := range ids {
				if !seen[id] {
					unstarted = append(unstarted, id)
				}
			}
			recovering = true
			mu.Unlock()
			if gotPeak != 3 || started != 3 {
				t.Fatalf("concurrency/batch bound: peak=%d started=%d", gotPeak, started)
			}
			if state.Watermark != "" || state.BatchUpper == "" {
				t.Fatal("unfinished/interrupted window advanced")
			}
			switch mode {
			case "ok":
				if err != nil || result.Fetched != 3 || result.Remaining != 3 {
					t.Fatalf("batch: %+v %v", result, err)
				}
			case "429":
				if zoho.RetryAfter(err) != 30*time.Second {
					t.Fatalf("rate limit lost: %v", err)
				}
			case "cancel":
				if err == nil {
					t.Fatal("cancellation reported success")
				}
			}
			// Unstarted work must be recoverable; failed IDs retain their separate cooldown.
			if _, err = syncer.Run(context.Background(), client, db, syncer.Options{Concurrency: 3}); err != nil {
				t.Fatal(err)
			}
			for _, id := range unstarted {
				record, e := db.Get(context.Background(), id)
				if e != nil || record.Pending || !record.Complete {
					t.Fatalf("unstarted ticket not recovered: id=%s pending=%v complete=%v error=%v", id, record.Pending, record.Complete, e)
				}
			}
		})
	}
}
