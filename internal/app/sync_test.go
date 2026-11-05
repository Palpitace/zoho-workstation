// input: 合成 Zoho 接口、隔离数据库与长周期配置。
// output: 首轮批次自动接续、失败/限流等待及可取消关闭的行为证据。
// pos: 同步调度生命周期回归测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"zoho-workstation/internal/app"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/zoho"
)

func TestSyncDrainsBatchesAndCancelsBackoff(t *testing.T) {
	for _, status := range []int{200, 503, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Chdir(t.TempDir())
			var details atomic.Int32
			stamp := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/oauth/v2/token":
					fmt.Fprint(w, `{"access_token":"synthetic"}`)
				case r.URL.Path == "/api/v1/tickets":
					if r.URL.Query().Get("departmentId") != "7" {
						t.Error("department filter lost")
					}
					rows := []map[string]string{}
					for i := 1; i <= 7; i++ {
						rows = append(rows, map[string]string{"id": fmt.Sprint(i), "departmentId": "7"})
					}
					json.NewEncoder(w).Encode(map[string]any{"data": rows})
				case strings.HasSuffix(r.URL.Path, "/threads") || strings.HasSuffix(r.URL.Path, "/comments"):
					fmt.Fprint(w, `{"data":[]}`)
				case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/"):
					details.Add(1)
					if status != 200 {
						w.Header().Set("Retry-After", "30")
						w.WriteHeader(status)
						return
					}
					id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
					json.NewEncoder(w).Encode(map[string]string{"id": id, "departmentId": "7", "createdTime": stamp, "modifiedTime": stamp, "threadCount": "0", "commentCount": "0"})
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer remote.Close()
			db, err := store.Open(filepath.Join("data", "app.db"), "7")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cfg := config.Config{SyncInterval: time.Hour, SyncLookback: time.Hour, Zoho: config.Zoho{BaseURL: remote.URL, AccountsURL: remote.URL, DepartmentID: "7"}}
			service, err := app.New(cfg, db, zoho.New(cfg.Zoho, remote.Client()))
			if err != nil {
				t.Fatal(err)
			}
			if err = service.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				state, _ := db.State(context.Background())
				if (status == 200 && state.Ready == 7) || (status != 200 && state.Failed > 0) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			state, _ := db.State(context.Background())
			if status == 200 {
				if state.Ready != 7 || state.Watermark == "" {
					t.Fatalf("initial batches waited for regular polling: %+v", state)
				}
			} else {
				time.Sleep(1200 * time.Millisecond)
				want := int32(3)
				if status == 429 {
					want = 1
				}
				if details.Load() != want {
					t.Fatalf("failure backoff/rate limit bypassed: requests=%d want=%d", details.Load(), want)
				}
			}
			start := time.Now()
			service.Close()
			if time.Since(start) > time.Second {
				t.Fatal("close did not cancel batch wait")
			}
		})
	}
}
