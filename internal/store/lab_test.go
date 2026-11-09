// input: 临时数据库、合成历史快照与并发评分。
// output: 固定批次输入、追加记录、重启暂停和评分保留的行为验证。
// pos: 离线批次持久化集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"zoho-workstation/internal/lab"
	"zoho-workstation/internal/store"
)

func TestLabBatchHistoryAndRecovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lab.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	spec := lab.Spec{Schema: lab.Schema, Model: "deepseek:test", ModelURLHash: "endpoint", Samples: []lab.Sample{{TicketID: "1", Group: "a", Split: "development"}}}
	batch, err := db.CreateLabBatch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Samples[0].TicketID = "changed"
	batch, err = db.MutateLabBatch(ctx, batch.ID, func(state *lab.State) error {
		state.Status = "running"
		state.Calls = append(state.Calls, lab.Call{ID: 1, Key: "reply", Task: "reply", NodeID: "1:first", InputHash: "hash", Input: json.RawMessage(`{"ticket":{}}`), Status: "running", StartedAt: "2026-11-09T00:00:00Z"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err = db.MutateLabBatch(ctx, batch.ID, func(state *lab.State) error {
		state.Calls[0].Status = "success"
		state.Calls[0].Result = json.RawMessage(`{"text":"Check power."}`)
		state.Calls[0].CompletedAt = "2026-11-09T00:01:00Z"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.MutateLabBatch(ctx, batch.ID, func(state *lab.State) error {
				state.Feedback = append(state.Feedback, lab.Feedback{ID: len(state.Feedback) + 1, CallID: 1, NodeID: "1:first", Score: 4, Note: "Keep device conditions.", CreatedAt: "2026-11-09T00:02:00Z"})
				return nil
			})
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.MutateLabBatch(ctx, batch.ID, func(state *lab.State) error {
		state.Calls[0].Result = json.RawMessage(`{"text":"overwritten"}`)
		return nil
	}); err == nil {
		t.Fatal("completed reply was mutable")
	}
	if _, err = db.MutateLabBatch(ctx, batch.ID, func(state *lab.State) error { state.Feedback[0].Score = 1; return nil }); err == nil {
		t.Fatal("old feedback was mutable")
	}
	_, err = db.MutateLabBatch(ctx, batch.ID, func(state *lab.State) error {
		state.Calls = append(state.Calls, lab.Call{ID: 2, Key: "evaluate", Task: "evaluate", NodeID: "1:first", InputHash: "hash2", Input: json.RawMessage(`{"ticket":{}}`), Status: "running", StartedAt: "2026-11-09T00:03:00Z"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.RecoverLabBatches(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.LabBatch(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Samples[0].TicketID != "1" || got.State.Status != "paused" || got.State.Calls[1].Status != "interrupted" || len(got.State.Calls) != 2 || len(got.State.Feedback) != 2 || got.State.Calls[0].Status != "success" {
		t.Fatalf("restart lost fixed input or history: %+v", got)
	}
	listed, err := db.ListLabBatches(ctx)
	if err != nil || len(listed) != 1 || listed[0].State.Calls[0].Input != nil || listed[0].State.Calls[0].Result != nil {
		t.Fatalf("list exposes full calls: %+v %v", listed, err)
	}
}
