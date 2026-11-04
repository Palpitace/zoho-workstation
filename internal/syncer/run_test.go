// input: 合成 Zoho HTTP 服务和隔离 SQLite 文件。
// output: 分批采样与增量的跨重启进度、失败恢复、幂等更新和水位保护证据。
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

func TestSyncResumesFrozenSampleAfterFailureAndRestart(t *testing.T) {
	ctx := context.Background()
	var failSecond atomic.Bool
	var incompleteSecond atomic.Bool
	var allChanged atomic.Bool
	var revision atomic.Int32
	revision.Store(1)
	stamp := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(value any) {
			if err := json.NewEncoder(w).Encode(value); err != nil {
				t.Error(err)
			}
		}
		switch {
		case r.URL.Path == "/oauth/v2/token":
			write(map[string]any{"access_token": "synthetic", "expires_in": 3600})
		case r.Method != http.MethodGet:
			t.Errorf("unexpected business write: %s", r.Method)
			w.WriteHeader(405)
		case r.URL.Path == "/api/v1/tickets":
			write(map[string]any{"data": []map[string]string{{"id": "1", "departmentId": "7"}, {"id": "2", "departmentId": "7"}, {"id": "3", "departmentId": "7"}}})
		case r.URL.Path == "/api/v1/tickets/search":
			changed := []map[string]string{{"id": "2", "departmentId": "7", "modifiedTime": stamp}}
			if allChanged.Load() {
				changed = []map[string]string{{"id": "1", "departmentId": "7", "modifiedTime": stamp}, {"id": "2", "departmentId": "7", "modifiedTime": stamp}, {"id": "3", "departmentId": "7", "modifiedTime": stamp}}
			}
			write(map[string]any{"data": changed})
		case strings.HasSuffix(r.URL.Path, "/comments"):
			write(map[string]any{"data": []map[string]any{{"id": "20", "commentedTime": stamp, "content": "Check warranty, do not assume the customer upgraded.", "contentType": "plainText", "isPublic": false, "commenter": map[string]string{"type": "AGENT"}}}})
		case strings.HasSuffix(r.URL.Path, "/threads"):
			write(map[string]any{"data": []map[string]string{{"id": "10"}}})
		case strings.HasSuffix(r.URL.Path, "/threads/10"):
			write(map[string]any{"id": "10", "createdTime": stamp, "content": "I have not upgraded the firmware.", "contentType": "plainText", "visibility": "public", "author": map[string]string{"type": "END_USER"}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
			if id == "2" && failSecond.Load() {
				w.WriteHeader(503)
				return
			}
			threadCount := "1"
			if id == "2" && incompleteSecond.Load() {
				threadCount = "2"
			}
			write(map[string]any{"id": id, "departmentId": "7", "subject": fmt.Sprintf("Printer issue revision %d", revision.Load()), "createdTime": stamp, "modifiedTime": stamp, "threadCount": threadCount, "commentCount": "1"})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
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
	opts := syncer.Options{SampleSize: 3, MaxTickets: 1, Lookback: 24 * time.Hour}
	partial, err := syncer.Run(ctx, client, db, opts)
	if err != nil || partial.Fetched != 1 || partial.Failed != 0 || partial.Remaining != 2 {
		t.Fatalf("partial: %+v %v", partial, err)
	}
	state, err := db.State(ctx)
	if err != nil || state.Watermark != "" || state.Selected != 3 || state.Ready != 1 {
		t.Fatalf("partial run advanced or lost reservation: %+v %v", state, err)
	}
	initialUpper := state.BatchUpper
	if initialUpper == "" {
		t.Fatal("partial run did not persist its synchronization window")
	}
	if err := db.Reserve(ctx, []string{"4"}); err == nil {
		t.Fatal("unfinished sample replaced with a new ticket")
	}

	failSecond.Store(true)
	failed, err := syncer.Run(ctx, client, db, opts)
	if err == nil || failed.Failed != 1 || failed.Fetched != 0 || failed.Remaining != 1 {
		t.Fatalf("failed run: %+v %v", failed, err)
	}
	state, _ = db.State(ctx)
	if state.Watermark != "" || state.Ready != 1 || state.Failed != 1 || state.BatchUpper != initialUpper {
		t.Fatalf("failure advanced watermark: %+v", state)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	failSecond.Store(false)
	recovered, err := syncer.Run(ctx, client, db, opts)
	if err != nil || recovered.Fetched != 1 || recovered.Failed != 0 || recovered.Remaining != 1 {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	state, _ = db.State(ctx)
	if state.Watermark != "" || state.Ready != 2 || state.Failed != 0 || state.BatchUpper != initialUpper {
		t.Fatalf("recovery advanced unfinished window: %+v", state)
	}
	completed, err := syncer.Run(ctx, client, db, opts)
	if err != nil || completed.Fetched != 1 || completed.Failed != 0 || completed.Remaining != 0 {
		t.Fatalf("final initial batch: %+v %v", completed, err)
	}
	state, _ = db.State(ctx)
	if state.Watermark != initialUpper || state.Ready != 3 || state.Failed != 0 || state.BatchUpper != "" {
		t.Fatalf("recovery state: %+v", state)
	}

	revision.Store(2)
	allChanged.Store(true)
	var incrementalUpper string
	for batch := 0; batch < 3; batch++ {
		updated, err := syncer.Run(ctx, client, db, opts)
		if err != nil || updated.Fetched != 1 || updated.Failed != 0 || updated.Remaining != 2-batch {
			t.Fatalf("incremental batch %d: %+v %v", batch+1, updated, err)
		}
		state, err = db.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if batch == 0 {
			incrementalUpper = state.BatchUpper
			if incrementalUpper == "" || incrementalUpper == initialUpper {
				t.Fatalf("new incremental window not started: %+v", state)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(path, "7")
			if err != nil {
				t.Fatal(err)
			}
		}
		if batch < 2 && (state.Watermark != initialUpper || state.BatchUpper != incrementalUpper) {
			t.Fatalf("incremental partial advanced or changed window: %+v", state)
		}
		if batch == 2 && (state.Watermark != incrementalUpper || state.BatchUpper != "") {
			t.Fatalf("incremental final batch did not commit window: %+v", state)
		}
	}
	for _, id := range []string{"1", "2", "3"} {
		view, err := db.Get(ctx, id)
		if err != nil || view.Subject != "Printer issue revision 2" || len(view.Messages) != 2 || !view.Complete {
			t.Fatalf("ticket %s update or dedup failed: %+v %v", id, view, err)
		}
	}
	opts.MaxTickets = 0
	if _, err = syncer.Run(ctx, client, db, opts); err != nil {
		t.Fatal(err)
	}
	view, _ := db.Get(ctx, "2")
	if len(view.Messages) != 2 {
		t.Fatal("repeat sync duplicated messages")
	}
	before, _ := db.State(ctx)
	revision.Store(3)
	allChanged.Store(false)
	for _, mode := range []string{"HTTP failure", "incomplete conversation"} {
		failSecond.Store(mode == "HTTP failure")
		incompleteSecond.Store(mode == "incomplete conversation")
		if _, err := syncer.Run(ctx, client, db, opts); err == nil {
			t.Fatalf("%s reported success", mode)
		}
		state, _ = db.State(ctx)
		view, _ = db.Get(ctx, "2")
		if state.Watermark != before.Watermark || state.Failed != 1 || !view.Complete || view.Subject != "Printer issue revision 2" {
			t.Fatalf("%s overwrote success or advanced cursor: %+v %+v", mode, state, view)
		}
	}
}
