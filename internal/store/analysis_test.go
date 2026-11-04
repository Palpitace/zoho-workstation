// input: 隔离 SQLite、合成分析版本与供应商结果。
// output: 旧输入隔离、失败保留成功结果、重启恢复和持久预算的行为证据。
// pos: AI 结果存储边界集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"zoho-workstation/internal/store"
)

func TestAnalysisVersionsFailuresAndPersistentBudget(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Reserve(ctx, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	req := store.AnalysisRequest{TicketID: "1", Kind: "jev", InputHash: "old", SchemaVersion: "v1", RequestedModel: "test-model", Round: "round1"}
	old, started, err := db.StartAnalysis(ctx, req, false)
	if err != nil || !started {
		t.Fatalf("start old: %v", err)
	}
	req.InputHash = "new"
	current, started, err := db.StartAnalysis(ctx, req, false)
	if err != nil || !started {
		t.Fatalf("start new: %v", err)
	}
	if err = db.FinishAnalysis(ctx, current.ID, "actual-model", json.RawMessage(`{"value":"new"}`), nil); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishAnalysis(ctx, old.ID, "actual-model", json.RawMessage(`{"value":"old"}`), nil); err != nil {
		t.Fatal(err)
	}
	view, err := db.Analysis(ctx, req)
	if err != nil || view.Success == nil || view.Success.InputHash != "new" || view.Stale {
		t.Fatalf("old result won: %+v %v", view, err)
	}
	if _, started, err = db.StartAnalysis(ctx, req, false); err != nil || started {
		t.Fatalf("cache reused incorrectly: %v", err)
	}
	retry, started, err := db.StartAnalysis(ctx, req, true)
	if err != nil || !started {
		t.Fatalf("manual retry: %v", err)
	}
	if err = db.FinishAnalysis(ctx, retry.ID, "", nil, errors.New("provider unavailable")); err != nil {
		t.Fatal(err)
	}
	view, _ = db.Analysis(ctx, req)
	if view.Current.Status != "failed" || view.Success == nil || view.Success.InputHash != "new" || view.Stale {
		t.Fatal("failure erased success")
	}
	req.InputHash = "next"
	view, _ = db.Analysis(ctx, req)
	if view.Current != nil || !view.Stale || view.Success == nil {
		t.Fatal("changed input must mark previous result stale")
	}
	if _, _, err = db.StartAnalysis(ctx, req, false); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.RecoverAnalysis(ctx); err != nil {
		t.Fatal(err)
	}
	view, _ = db.Analysis(ctx, req)
	if view.Current == nil || view.Current.Status != "failed" || !view.Stale {
		t.Fatal("interrupted analysis not recovered")
	}
	for i := 4; i < 20; i++ {
		run, ok, e := db.StartAnalysis(ctx, req, true)
		if e != nil || !ok {
			t.Fatalf("budget rejected request %d: %v", i+1, e)
		}
		if e = db.FinishAnalysis(ctx, run.ID, "", nil, errors.New("synthetic failure")); e != nil {
			t.Fatal(e)
		}
	}
	if _, ok, e := db.StartAnalysis(ctx, req, true); e == nil || ok {
		t.Fatal("round budget bypassed")
	}
	budget, err := db.AnalysisBudget(ctx, "round1")
	if err != nil || budget.Total != 20 || budget.Round != 20 {
		t.Fatalf("persistent budget: %+v %v", budget, err)
	}
	for _, round := range []string{"round2", "round3"} {
		req.Round = round
		for i := 0; i < 20; i++ {
			run, ok, e := db.StartAnalysis(ctx, req, true)
			if e != nil || !ok {
				t.Fatalf("later authorized round: %v", e)
			}
			if e = db.FinishAnalysis(ctx, run.ID, "", nil, errors.New("synthetic failure")); e != nil {
				t.Fatal(e)
			}
		}
	}
	req.Round = "round4"
	if _, ok, e := db.StartAnalysis(ctx, req, true); e == nil || ok {
		t.Fatal("total budget bypassed by new round")
	}
}
