// input: 临时 SQLite 文件和合成工单。
// output: Issue #10 范围恢复/缓存待确认；滚动范围原子更新、重启与历史保留；允许追加新工单； 样本/快照/额度保留、幂等更新及非破坏性迁移的验证。
// pos: 数据持久化行为测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
)

func TestSampleAndSnapshotSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Reserve(ctx, []string{"1", "2", "1"}); err != nil {
		t.Fatal(err)
	}
	s := ticket.Snapshot{ID: "1", DepartmentID: "7", Subject: "Printer issue", Status: "Open", Complete: true, Messages: []ticket.Message{{ID: "9", Source: "thread", Body: "Not fixed"}}}
	raw := ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"1","departmentId":"7"}`), Complete: true}
	if err = db.Save(ctx, raw, s); err != nil {
		t.Fatal(err)
	}
	if err = db.Failure(ctx, "2", "temporary read failure"); err != nil {
		t.Fatal(err)
	}
	if err = db.Save(ctx, raw, s); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids, err := db.IDs(ctx)
	if err != nil || len(ids) != 2 {
		t.Fatalf("reserved sample lost: %v", err)
	}
	got, err := db.Get(ctx, "1")
	if err != nil || len(got.Messages) != 1 || got.Messages[0].Body != "Not fixed" {
		t.Fatalf("snapshot lost: %v", err)
	}
	s.Status = "Reopened"
	if err = db.Save(ctx, raw, s); err != nil {
		t.Fatal(err)
	}
	if err = db.Failure(ctx, "1", "retry later"); err != nil {
		t.Fatal(err)
	}
	got, err = db.Get(ctx, "1")
	if err != nil || got.Status != "Reopened" {
		t.Fatal("failure erased successful snapshot")
	}
	if err = db.Reserve(ctx, []string{"3"}); err != nil {
		t.Fatal("continuous discovery cannot add a new ticket")
	}
	if other, err := store.Open(path, "8"); err == nil {
		other.Close()
		t.Fatal("database reused for another department")
	}
}

func TestStopFlagMigrationPreservesHistoryAndBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Original tickets schema, before the additive sync_stopped column.
	_, err = old.Exec(`CREATE TABLE tickets (
 id TEXT PRIMARY KEY, selected_at TEXT NOT NULL,
 subject TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', modified_time TEXT NOT NULL DEFAULT '',
 raw_json TEXT, snapshot_json TEXT, sync_error TEXT NOT NULL DEFAULT '', checked_at TEXT NOT NULL DEFAULT ''
 );
 INSERT INTO tickets(id,selected_at,raw_json,snapshot_json,sync_error) VALUES
 ('1','2026-01-01','{"ticket":{"id":"1"}}','{"id":"1","department_id":"7","subject":"preserved","complete":true}','old failure');
 CREATE TABLE ai_runs (
 id INTEGER PRIMARY KEY,ticket_id TEXT NOT NULL,kind TEXT NOT NULL,input_hash TEXT NOT NULL,schema_version TEXT NOT NULL,requested_model TEXT NOT NULL,round TEXT NOT NULL,status TEXT NOT NULL,returned_model TEXT NOT NULL DEFAULT '',started_at TEXT NOT NULL,completed_at TEXT NOT NULL DEFAULT '',result_json TEXT,error TEXT NOT NULL DEFAULT ''
 );
 INSERT INTO ai_runs(id,ticket_id,kind,input_hash,schema_version,requested_model,round,status,started_at,error)
 VALUES (1,'1','bub','hash','v1','synthetic','development-1','failed','2026-01-01','old model failure');`)
	old.Close()
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	record, err := db.Get(context.Background(), "1")
	if err != nil || record.Subject != "preserved" || record.SyncStopped || record.SyncError != "old failure" {
		t.Fatalf("migration changed history: %+v %v", record, err)
	}
	budget, err := db.AnalysisBudget(context.Background(), "development-1")
	if err != nil || budget.Round != 1 || budget.Total != 1 {
		t.Fatalf("migration reset budget: %+v %v", budget, err)
	}
}

func TestRollingScopePreservesHistoryAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scope.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	if err = db.Reserve(ctx, []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	snapshot := ticket.Snapshot{ID: "1", DepartmentID: "7", Subject: "old", Complete: true}
	if err = db.Save(ctx, ticket.RawSnapshot{}, snapshot); err != nil {
		t.Fatal(err)
	}
	req := store.AnalysisRequest{TicketID: "1", Kind: "bub", InputHash: "synthetic", SchemaVersion: "test", RequestedModel: "test", Round: "test"}
	run, _, err := db.StartAnalysis(ctx, req, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.FinishAnalysis(ctx, run.ID, "test", json.RawMessage("{}"), nil); err != nil {
		t.Fatal(err)
	}
	db.EnableRecentScope()
	if rows, err := db.List(ctx, "", ""); err != nil || len(rows) != 1 || !rows[0].OutsideScope {
		t.Fatalf("unconfirmed cache missing or active: %v %v", rows, err)
	}
	if err = db.ReplaceRecent(ctx, []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	if err = db.ReplaceRecent(ctx, []string{"2", "3"}); err != nil {
		t.Fatal(err)
	}
	if err = db.ReplaceRecent(ctx, []string{"3", "3"}); err == nil {
		t.Fatal("duplicate selection accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = db.ReplaceRecent(cancelled, []string{"4"}); err == nil {
		t.Fatal("cancelled replacement committed")
	}
	db.Close()
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	db.EnableRecentScope()
	ids, err := db.IDs(ctx)
	if err != nil || len(ids) != 2 || ids[0] != "2" || ids[1] != "3" {
		t.Fatalf("selection lost: %v %v", ids, err)
	}
	state, err := db.State(ctx)
	if err != nil || state.Selected != 2 || state.Ready != 0 {
		t.Fatalf("counts include excluded history: %+v %v", state, err)
	}
	view, err := db.Analysis(ctx, req)
	if err != nil || view.Success == nil {
		t.Fatal("excluded AI history was lost")
	}
	old, err := db.Get(ctx, "1")
	if err != nil || !old.OutsideScope || old.Subject != "old" {
		t.Fatalf("old snapshot lost or active: %+v %v", old, err)
	}
	if err = db.ReplaceRecent(ctx, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	old, err = db.Get(ctx, "1")
	if err != nil || old.OutsideScope || old.Subject != "old" {
		t.Fatal("could not restore retained history")
	}
}

func TestUnconfirmedScopeShowsCachedHistoryWithoutAnalysis(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	if err = db.Reserve(ctx, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err = db.Save(ctx, ticket.RawSnapshot{}, ticket.Snapshot{ID: "1", DepartmentID: "7", Complete: true, CreatedTime: "2026-11-06T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 61; i++ {
		id := strconv.Itoa(i)
		if err = db.Reserve(ctx, []string{id}); err != nil {
			t.Fatal(err)
		}
		if err = db.Save(ctx, ticket.RawSnapshot{}, ticket.Snapshot{ID: id, DepartmentID: "7", Complete: true, CreatedTime: time.Date(2026, 11, 6, 0, i, 0, 0, time.UTC).Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
	}
	db.EnableRecentScope()
	rows, err := db.List(ctx, "", "")
	if err != nil || len(rows) != 50 || !rows[0].OutsideScope {
		t.Fatalf("cached history hidden or eligible: %+v %v", rows, err)
	}
	for _, r := range rows {
		n, _ := strconv.Atoi(r.ID)
		if n < 12 {
			t.Fatal("fallback did not select newest creation times")
		}
	}
	ids, err := db.IDs(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatal("unconfirmed cache scheduled for sync")
	}
	state, err := db.State(ctx)
	if err != nil || state.Ready != 50 || state.Selected != 50 {
		t.Fatalf("cache counts lost: %+v %v", state, err)
	}
	if err = db.ReplaceRecent(ctx, []string{}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	db.EnableRecentScope()
	rows, err = db.List(ctx, "", "")
	if err != nil || len(rows) != 0 {
		t.Fatal("confirmed empty selection resurrected cache")
	}
	old, err := db.Get(ctx, "1")
	if err != nil || !old.Complete {
		t.Fatal("empty selection erased snapshot")
	}
}
