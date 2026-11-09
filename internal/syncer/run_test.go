// input: 合成 Zoho HTTP 服务、可取消请求和隔离 SQLite 文件。
// output: Issue #10 范围恢复/缓存待确认；滚动50张/分页失败保留范围；持续发现起始边界/分页恢复及 初始/增量分批、重启、去重更新及取消不误推进水位的证据。
// pos: 同步编排公开边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package syncer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
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

func TestContinuousDiscoveryPersistsPagesAndFindsLaterArrivals(t *testing.T) {
	ctx := context.Background()
	start, _ := time.Parse(time.RFC3339, config.DefaultSyncStartTime)
	count := 60
	threadReads := 0
	failPage := false
	seenOffsets := []int{}
	stamp := start.Add(time.Hour).Format(time.RFC3339)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		switch {
		case r.URL.Path == "/oauth/v2/token":
			write(map[string]string{"access_token": "synthetic"})
		case r.URL.Path == "/api/v1/tickets":
			if r.URL.Query().Get("departmentId") != "7" || r.URL.Query().Get("sortBy") != "-createdTime" || r.URL.Query().Get("limit") != "25" {
				t.Error("unbounded or unscoped discovery")
			}
			offset, _ := strconv.Atoi(r.URL.Query().Get("from"))
			seenOffsets = append(seenOffsets, offset)
			if failPage && offset == 25 {
				w.WriteHeader(503)
				return
			}
			rows := []map[string]string{}
			for i := offset; i < min(offset+25, count+1); i++ {
				created := start.Add(time.Duration(count-i-1) * time.Minute)
				if i == count {
					created = start.Add(-time.Second)
				}
				rows = append(rows, map[string]string{"id": strconv.Itoa(count - i + 1), "departmentId": "7", "createdTime": created.Format(time.RFC3339)})
			}
			write(map[string]any{"data": rows})
		case r.URL.Path == "/api/v1/tickets/search":
			w.WriteHeader(403)
		case strings.HasSuffix(r.URL.Path, "/threads"), strings.HasSuffix(r.URL.Path, "/comments"):
			if strings.HasSuffix(r.URL.Path, "/threads") {
				threadReads++
			}
			write(map[string]any{"data": []any{}})
		default:
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
			write(map[string]string{"id": id, "departmentId": "7", "createdTime": stamp, "modifiedTime": stamp, "threadCount": "0", "commentCount": "0"})
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
	opts := syncer.Options{StartTime: start, MaxTickets: 5}
	if _, err := syncer.Run(ctx, client, db, opts); err != nil {
		t.Fatal(err)
	}
	state, _ := db.State(ctx)
	if state.Selected != 25 || state.DiscoveryOffset != 25 || state.Watermark != "" {
		t.Fatalf("first page state: %+v", state)
	}
	failPage = true
	if _, err := syncer.Run(ctx, client, db, opts); err == nil {
		t.Fatal("page failure not reported")
	}
	state, _ = db.State(ctx)
	if state.DiscoveryOffset != 25 || state.Watermark != "" {
		t.Fatal("failed page advanced cursor")
	}
	db.Close()
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	failPage = false
	drain := func() {
		for i := 0; i < 30; i++ {
			result, err := syncer.Run(ctx, client, db, opts)
			if err != nil {
				t.Fatal(err)
			}
			if result.Remaining == 0 {
				return
			}
		}
		t.Fatal("bounded batches failed to drain")
	}
	drain()
	state, _ = db.State(ctx)
	rows, _ := db.List(ctx, "", "")
	if state.Selected != 60 || state.Ready != 60 || len(rows) != 60 || !state.DiscoveryDone || state.Watermark == "" {
		t.Fatalf("discovery stopped at old sample limit: %+v / %d", state, len(rows))
	}
	if _, err := db.Get(ctx, "1"); err == nil {
		t.Fatal("ticket before cutoff admitted")
	}
	if len(seenOffsets) != 4 || seenOffsets[0] != 0 || seenOffsets[1] != 25 || seenOffsets[2] != 25 || seenOffsets[3] != 50 {
		t.Fatalf("resume offsets %v", seenOffsets)
	}
	beforeReads := threadReads
	count = 61
	drain()
	state, _ = db.State(ctx)
	if threadReads != beforeReads+1 {
		t.Fatalf("overlap reloaded unchanged conversations: %d -> %d", beforeReads, threadReads)
	}
	if state.Selected != 61 || state.Ready != 61 {
		t.Fatalf("new arrival missing or duplicates: %+v", state)
	}
	if err := db.Reserve(ctx, []string{"999"}); err != nil {
		t.Fatal(err)
	}
	old := ticket.Snapshot{ID: "999", DepartmentID: "7", Subject: "Retained old ticket", CreatedTime: start.Add(-time.Hour).Format(time.RFC3339), ModifiedTime: stamp, Complete: true}
	if err := db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{}`)}, old); err != nil {
		t.Fatal(err)
	}
	beforeReads = threadReads
	drain()
	retained, err := db.Get(ctx, "999")
	if err != nil || retained.Subject != old.Subject || retained.SyncStopped || threadReads != beforeReads {
		t.Fatal("before-cutoff history was fetched, changed or stopped")
	}
}

func TestRollingLatestFiftyDoesNotFetchExcludedTickets(t *testing.T) {
	ctx := context.Background()
	newest := 100
	failPage := false
	duplicatePage := false
	fetched := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		switch {
		case r.URL.Path == "/oauth/v2/token":
			write(map[string]string{"access_token": "synthetic"})
		case r.URL.Path == "/api/v1/tickets":
			offset, _ := strconv.Atoi(r.URL.Query().Get("from"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if offset+limit > 50 || r.URL.Query().Get("sortBy") != "-createdTime" {
				t.Error("unbounded or unordered discovery")
			}
			if failPage {
				w.WriteHeader(503)
				return
			}
			rows := []map[string]string{}
			for i := offset; i < offset+limit; i++ {
				id := newest - i
				if duplicatePage {
					id = newest
				}
				rows = append(rows, map[string]string{"id": strconv.Itoa(id), "departmentId": "7"})
			}
			write(map[string]any{"data": rows})
		case strings.HasSuffix(r.URL.Path, "/threads") || strings.HasSuffix(r.URL.Path, "/comments"):
			write(map[string]any{"data": []any{}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
			fetched[id] = true
			write(map[string]string{"id": id, "departmentId": "7", "subject": "Synthetic", "createdTime": "2026-11-06T00:00:00Z", "modifiedTime": "2026-11-06T00:00:00Z", "threadCount": "0", "commentCount": "0"})
		default:
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
	db, err := store.Open(filepath.Join(t.TempDir(), "recent.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Reserve(ctx, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err = db.Save(ctx, ticket.RawSnapshot{}, ticket.Snapshot{ID: "1", DepartmentID: "7", Complete: true, CreatedTime: "2026-11-05T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	opts := syncer.Options{RecentLimit: 50, MaxTickets: 1}
	duplicatePage = true
	failed, failErr := syncer.Run(ctx, client, db, opts)
	if failErr == nil || failed.Stage != "selection" || len(fetched) != 0 {
		t.Fatalf("selection failure escaped stage: %+v %v", failed, failErr)
	}
	cached, e := db.List(ctx, "", "")
	if e != nil || len(cached) != 1 || !cached[0].OutsideScope {
		t.Fatal("first failed selection hid cache or activated it")
	}
	state, e := db.State(ctx)
	if e != nil || state.Watermark != "" || state.RecentConfirmed || state.LastError == "" {
		t.Fatalf("failed selection recorded as complete: %+v %v", state, e)
	}
	duplicatePage = false

	if _, err = syncer.Run(ctx, client, db, opts); err != nil {
		t.Fatal(err)
	}
	if fetched["1"] {
		t.Fatal("excluded ticket fetched")
	}
	ids, _ := db.IDs(ctx)
	if len(ids) != 50 {
		t.Fatalf("selected %d", len(ids))
	}
	newest = 101
	failPage = true
	if _, err = syncer.Run(ctx, client, db, opts); err == nil {
		t.Fatal("partial selection accepted")
	}
	r, err := db.Get(ctx, "51")
	if err != nil || r.OutsideScope {
		t.Fatal("partial failure replaced previous scope")
	}
	failPage = false
	duplicatePage = true
	if _, err = syncer.Run(ctx, client, db, opts); err == nil {
		t.Fatal("shifted duplicate page accepted")
	}
	duplicatePage = false
	if _, err = syncer.Run(ctx, client, db, opts); err != nil {
		t.Fatal(err)
	}
	r, err = db.Get(ctx, "51")
	if err != nil || !r.OutsideScope {
		t.Fatal("oldest ticket did not leave scope")
	}
	r, err = db.Get(ctx, "101")
	if err != nil || r.OutsideScope {
		t.Fatal("newest ticket not selected")
	}
	ids, _ = db.IDs(ctx)
	if len(ids) != 50 {
		t.Fatalf("rolling scope grew: %d", len(ids))
	}
}
