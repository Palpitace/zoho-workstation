// input: 临时 SQLite 文件和合成工单。
// output: 样本/快照/额度保留、幂等更新及非破坏性迁移的验证。
// pos: 数据持久化行为测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

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
	if err = db.Reserve(ctx, []string{"3"}); err == nil {
		t.Fatal("frozen sample silently expanded")
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
