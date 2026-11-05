// input: 隔离 SQLite、合成分析版本与供应商结果。
// output: 旧输入隔离、重试保留首次终结状态、本地失败不计次及关闭额度后的持久计数证据。
// pos: AI 结果存储边界集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"zoho-workstation/internal/store"
)

func TestAnalysisVersionsFailuresAndUnlimitedRequestCounts(t *testing.T) {
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
	if view.Current != nil || !view.Stale || view.Success == nil || view.HasFinishedAttempt {
		t.Fatal("changed input must mark previous result stale")
	}
	if err = db.RecordAnalysisFailure(ctx, req, errors.New("synthetic input too large")); err != nil {
		t.Fatal(err)
	}
	view, err = db.Analysis(ctx, req)
	if err != nil || view.Current == nil || view.Current.Status != "invalid" || view.Success == nil || view.Success.InputHash != "new" || !view.Stale {
		t.Fatalf("local validation lost status or prior success: %+v %v", view, err)
	}
	invalidID := view.Current.ID
	if err = db.RecordAnalysisFailure(ctx, req, errors.New("same synthetic validation failure")); err != nil {
		t.Fatal(err)
	}
	view, err = db.Analysis(ctx, req)
	if err != nil || view.Current.ID != invalidID {
		t.Fatal("same invalid version appended another row")
	}
	if _, started, err = db.StartAnalysis(ctx, req, false); err != nil || started {
		t.Fatalf("invalid input retried automatically: %v", err)
	}
	budget, err := db.AnalysisBudget(ctx, "round1")
	if err != nil || budget.Total != 3 || budget.Round != 3 {
		t.Fatalf("local validation consumed request budget: %+v %v", budget, err)
	}
	if _, started, err = db.StartAnalysis(ctx, req, true); err != nil || !started {
		t.Fatalf("forced valid retry unavailable: %v", err)
	}
	if err = db.RecordAnalysisFailure(ctx, req, nil); err == nil {
		t.Fatal("nil local failure accepted")
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
	for i := 4; i < 61; i++ {
		run, ok, e := db.StartAnalysis(ctx, req, true)
		if e != nil || !ok {
			t.Fatalf("request count incorrectly blocked request %d: %v", i+1, e)
		}
		view, e = db.Analysis(ctx, req)
		if e != nil || view.Current.Status != "running" || !view.HasFinishedAttempt {
			t.Fatal("retry erased the completed first attempt needed to release Bub")
		}
		if e = db.FinishAnalysis(ctx, run.ID, "", nil, errors.New("synthetic failure")); e != nil {
			t.Fatal(e)
		}
	}
	if _, ok, e := db.StartAnalysis(ctx, req, false); e != nil || ok {
		t.Fatal("removing quotas must not remove automatic failure retry limits")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	budget, err = db.AnalysisBudget(ctx, "round1")
	if err != nil || budget.Total != 61 || budget.Round != 61 {
		t.Fatalf("history counts changed after restart: %+v %v", budget, err)
	}
	for _, kind := range []string{"jev", "bub"} {
		req.Kind, req.InputHash = kind, "fresh-"+kind
		run, ok, e := db.StartAnalysis(ctx, req, false)
		if e != nil || !ok {
			t.Fatalf("automatic %s blocked beyond old limits: %v", kind, e)
		}
		if _, ok, e := db.StartAnalysis(ctx, req, true); e != nil || ok {
			t.Fatal("overlapping input admitted")
		}
		if e = db.FinishAnalysis(ctx, run.ID, "synthetic", json.RawMessage(`{}`), nil); e != nil {
			t.Fatal(e)
		}
	}
}

func TestOperationLocksShareCanonicalDatabaseAndRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	first, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	alias := filepath.Join(dir, "alias.db")
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(alias, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, operation := range []string{"sync", "analysis"} {
		t.Run(operation, func(t *testing.T) {
			release, err := first.Lock(operation)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if unexpectedRelease, err := second.Lock(operation); !errors.Is(err, store.ErrBusy) || unexpectedRelease != nil {
				t.Fatalf("same database admitted another %s operation: %v", operation, err)
			}
			other := "analysis"
			if operation == "analysis" {
				other = "sync"
			}
			otherRelease, err := second.Lock(other)
			if err != nil {
				t.Fatalf("independent operation blocked: %v", err)
			}
			otherRelease()
			release()
			nextRelease, err := second.Lock(operation)
			if err != nil {
				t.Fatalf("operation lock not released: %v", err)
			}
			nextRelease()
		})
	}
	if release, err := first.Lock("arbitrary"); err == nil || release != nil {
		t.Fatal("unknown lock operation accepted")
	}
}
