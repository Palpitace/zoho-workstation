// input: 合成 Zoho HTTP 服务、可取消请求和隔离 SQLite 文件。
// output: 初始/增量分批、重启、去重更新及取消不误推进水位的证据。
// pos: 同步编排公开边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package syncer_test

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

	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/syncer"
	"zoho-workstation/internal/zoho"
)

func TestSyncBatchesUpdatesAndCancellation(t *testing.T) {
	ctx := context.Background()
	var revision atomic.Int32
	var hold atomic.Bool
	var denied atomic.Bool
	reached := make(chan struct{}, 1)
	revision.Store(1)
	stamp := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		switch {
		case r.URL.Path == "/oauth/v2/token":
			write(map[string]string{"access_token": "synthetic"})
		case r.Method != http.MethodGet:
			t.Error("business write attempted")
			w.WriteHeader(405)
		case r.URL.Path == "/api/v1/tickets" || r.URL.Path == "/api/v1/tickets/search":
			if denied.Load() {
				w.WriteHeader(500)
				return
			}
			ids := []string{"1", "2", "3"}
			if r.URL.Path == "/api/v1/tickets/search" {
				ids = strings.Split(r.URL.Query().Get("id"), ",")
			}
			rows := []map[string]string{}
			for _, id := range ids {
				rows = append(rows, map[string]string{"id": id, "departmentId": "7", "modifiedTime": stamp})
			}
			write(map[string]any{"data": rows})
		case strings.HasSuffix(r.URL.Path, "/comments"):
			write(map[string]any{"data": []any{}})
		case strings.HasSuffix(r.URL.Path, "/threads"):
			write(map[string]any{"data": []map[string]string{{"id": "10"}}})
		case strings.HasSuffix(r.URL.Path, "/threads/10"):
			write(map[string]string{"id": "10", "content": "I have NOT upgraded.", "createdTime": stamp})
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/"):
			if hold.Load() {
				select {
				case reached <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return
			}
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
			write(map[string]string{"id": id, "departmentId": "7", "subject": fmt.Sprintf("revision %d", revision.Load()), "createdTime": stamp, "modifiedTime": stamp, "threadCount": "1", "commentCount": "0"})
		default:
			t.Error("unexpected path")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	opts := syncer.Options{SampleSize: 3, MaxTickets: 1}
	for round := 1; round <= 2; round++ {
		before, _ := db.State(ctx)
		revision.Store(int32(round))
		upper := ""
		for batch := 0; batch < 3; batch++ {
			result, err := syncer.Run(ctx, client, db, opts)
			if err != nil || result.Fetched != 1 || result.Failed != 0 || result.Remaining != 2-batch {
				t.Fatalf("round %d batch %d: %+v %v", round, batch, result, err)
			}
			state, _ := db.State(ctx)
			if batch == 0 {
				upper = state.BatchUpper
				if upper == "" {
					t.Fatal("batch window not persisted")
				}
				if err = db.Reserve(ctx, []string{"4"}); err == nil {
					t.Fatal("frozen sample expanded")
				}
				db.Close()
				db, err = store.Open(path, "7")
				if err != nil {
					t.Fatal(err)
				}
			}
			if batch < 2 && (state.Watermark != before.Watermark || state.BatchUpper != upper) {
				t.Fatal("partial batch advanced or lost its window")
			}
			if batch == 2 && (state.Watermark != upper || state.BatchUpper != "") {
				t.Fatal("finished batch did not commit window")
			}
		}
	}
	opts.MaxTickets = 0
	if _, err = syncer.Run(ctx, client, db, opts); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2", "3"} {
		record, err := db.Get(ctx, id)
		if err != nil || record.Subject != "revision 2" || len(record.Messages) != 1 || !record.Complete {
			t.Fatal("update/dedup snapshot invalid")
		}
	}
	before, _ := db.State(ctx)
	denied.Store(true)
	if _, err = syncer.Run(ctx, client, db, opts); err == nil {
		t.Fatal("global search failure reported success")
	}
	state, _ := db.State(ctx)
	if state.Watermark != before.Watermark || state.BatchUpper == "" {
		t.Fatal("global failure advanced cursor")
	}
	denied.Store(false)
	hold.Store(true)
	cancelCtx, cancel := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() { _, err := syncer.Run(cancelCtx, client, db, opts); finished <- err }()
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("cancellable request not reached")
	}
	cancel()
	select {
	case err = <-finished:
		if err == nil {
			t.Fatal("cancellation reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation blocked")
	}
	state, _ = db.State(ctx)
	if state.Watermark != before.Watermark || state.Failed != 1 || state.BatchUpper == "" {
		t.Fatal("cancellation lost retry or advanced cursor")
	}
}
