// input: 临时 SQLite 文件和合成工单。
// output: 样本额度、失败恢复、幂等更新及重启持久性的验证。
// pos: 数据持久化行为测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
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
