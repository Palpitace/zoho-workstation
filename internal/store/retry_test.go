// input: 合成 SQLite 历史和模拟已经过去的持久失败时间。
// output: 网络持续重试、三次相同证据失败停止、手动恢复与版本/重启隔离。
// pos: 自动分析持续退避回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestRepeatedBubEvidenceFailureStopsOnlySameSignature(t *testing.T) {
	for _, kind := range []string{"evidence", "network", "different"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "retry.db")
			db, err := store.Open(path, "7")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			if err = db.Reserve(ctx, []string{"1"}); err != nil {
				t.Fatal(err)
			}
			fixture, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer fixture.Close()
			req := store.AnalysisRequest{TicketID: "1", Kind: "bub", InputHash: "input", SchemaVersion: "schema", RequestedModel: "model", Round: "test"}
			for i := 0; i < 3; i++ {
				run, started, err := db.StartAnalysis(ctx, req, false)
				if err != nil || !started {
					t.Fatalf("attempt %d prematurely blocked", i)
				}
				message := "Bub: invalid_summary (missing_support_evidence) at support_responses[0].message_ids"
				if kind == "network" {
					message = "Bub: model_http_error; HTTP 503"
				}
				if kind == "different" && i == 1 {
					message = "Bub: invalid_summary (missing_customer_evidence)"
				}
				if err = db.FinishAnalysis(ctx, run.ID, "", nil, errors.New(message)); err != nil {
					t.Fatal(err)
				}
				if _, err = fixture.Exec("UPDATE ai_runs SET completed_at=? WHERE id=?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), run.ID); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			db, err = store.Open(path, "7")
			if err != nil {
				t.Fatal(err)
			}
			view, err := db.Analysis(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "evidence" && (view.TaskState != "manual" || view.NextRetryAt != "") {
				t.Fatalf("repeated evidence failure still retries: %+v", view)
			}
			_, started, err := db.StartAnalysis(ctx, req, false)
			if err != nil || started == (kind == "evidence") {
				t.Fatalf("kind=%s started=%t err=%v", kind, started, err)
			}
			if kind == "evidence" {
				run, started, err := db.StartAnalysis(ctx, req, true)
				if err != nil || !started {
					t.Fatal("manual retry blocked")
				}
				if err = db.FinishAnalysis(ctx, run.ID, "", json.RawMessage(`{}`), nil); err != nil {
					t.Fatal(err)
				}
				view, err = db.Analysis(ctx, req)
				if err != nil || view.TaskState != "success" {
					t.Fatal("successful manual retry did not recover")
				}
				req.InputHash = "new"
				if _, started, err = db.StartAnalysis(ctx, req, false); err != nil || !started {
					t.Fatal("new input inherited stop")
				}
			}
		})
	}
}
