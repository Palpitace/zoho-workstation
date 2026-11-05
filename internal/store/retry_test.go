// input: 合成 SQLite 历史和模拟已经过去的持久失败时间。
// output: 超过两次失败仍可恢复、指数退避上限、版本隔离与重启保留证据。
// pos: 自动分析持续退避回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
	"zoho-workstation/internal/store"
)

func TestAutomaticRetryContinuesAfterHistoricalFailures(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retry.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Reserve(ctx, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	req := store.AnalysisRequest{TicketID: "1", Kind: "bub", InputHash: "input", SchemaVersion: "schema", RequestedModel: "model", Round: "test"}
	// Age persisted timestamps rather than sleeping through the real retry delays.
	fixture, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	for attempt := 1; attempt <= 8; attempt++ {
		run, started, err := db.StartAnalysis(ctx, req, false)
		if err != nil || !started {
			t.Fatalf("attempt %d stopped permanently: %v", attempt, err)
		}
		if err := db.FinishAnalysis(ctx, run.ID, "", nil, errors.New("synthetic temporary failure")); err != nil {
			t.Fatal(err)
		}
		view, err := db.Analysis(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := time.Parse(time.RFC3339Nano, view.NextRetryAt)
		last, _ := time.Parse(time.RFC3339Nano, view.LastFailedAt)
		expected := min(time.Minute*time.Duration(1<<min(attempt-1, 5)), 30*time.Minute)
		if next.Sub(last) != expected {
			t.Fatalf("retry delay %v, expected %v", next.Sub(last), expected)
		}
		if _, started, err := db.StartAnalysis(ctx, req, false); err != nil || started {
			t.Fatal("cooldown bypassed")
		}
		if _, err := fixture.Exec("UPDATE ai_runs SET completed_at=? WHERE id=?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), run.ID); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, started, err := db.StartAnalysis(ctx, req, false); err != nil || !started {
		t.Fatal("restart stopped eligible retry")
	}
	count, _ := db.AnalysisBudget(ctx, "test")
	if count.Total != 9 {
		t.Fatal("retry erased history")
	}
	req.InputHash = "new"
	if _, started, err := db.StartAnalysis(ctx, req, false); err != nil || !started {
		t.Fatal("new version inherited cooldown")
	}
}
