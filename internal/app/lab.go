// input: 手动启动的固定离线批次及 Bub 的共享并发名额。
// output: 单步持久调用、有限改写、可暂停恢复的批次执行。
// pos: 离线实验与既有服务生命周期的连接。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/lab"
	"zoho-workstation/internal/store"
)

func (a *App) startLabStep() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started || a.closed || a.ctx.Err() != nil {
		return false, nil
	}
	if a.busy["bub"] || a.busy["case"] || a.busy["advice"] || a.busy["lab"] {
		return false, store.ErrBusy
	}
	batches, err := a.db.ListLabBatches(a.ctx)
	if err != nil {
		return false, err
	}
	for i := len(batches) - 1; i >= 0; i-- {
		if batches[i].State.Status != "running" {
			continue
		}
		batch, err := a.db.LabBatch(a.ctx, batches[i].ID)
		if err != nil {
			return false, err
		}
		// No in-memory worker owns a persisted running call. Its completion may
		// have failed to save; keep the uncertain attempt and require a resume.
		for _, call := range batch.State.Calls {
			if call.Status == "running" {
				return false, a.db.RecoverLabBatches(a.ctx)
			}
		}
		if batch.Spec.Schema != lab.Schema || batch.Spec.Model != a.cfg.Bub.Name || batch.Spec.ModelURLHash != lab.Hash(a.cfg.Bub.URL) {
			_, err = a.db.MutateLabBatch(a.ctx, batch.ID, func(s *lab.State) error {
				s.Status = "paused"
				s.Error = "模型配置或协议已变化，请从当前批次创建新一轮"
				return nil
			})
			return false, err
		}
		step := lab.Next(batch)
		if step == nil {
			_, err = a.db.MutateLabBatch(a.ctx, batch.ID, func(s *lab.State) error { s.Status = "completed"; s.Phase = "review"; s.Error = ""; return nil })
			if err != nil {
				return false, err
			}
			continue
		}
		input, _ := json.Marshal(step.Input)
		call := lab.Call{ID: len(batch.State.Calls) + 1, Key: step.Key, Task: step.Task, NodeID: step.NodeID, Variant: step.Variant, Revision: step.Revision, InputHash: lab.Hash(step.Input), Input: input, Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		_, err = a.db.MutateLabBatch(a.ctx, batch.ID, func(s *lab.State) error {
			if s.Status != "running" || len(s.Calls)+1 != call.ID {
				return store.ErrLabConflict
			}
			s.Phase = step.Phase
			s.Calls = append(s.Calls, call)
			return nil
		})
		if err != nil {
			_, _ = a.db.MutateLabBatch(a.ctx, batch.ID, func(s *lab.State) error {
				s.Status = "paused"
				s.Error = "无法保存下一次调用；请检查磁盘与批次大小后继续"
				return nil
			})
			return false, err
		}
		a.busy["lab"] = true
		a.bubNext = 0
		a.wg.Add(1)
		go a.runLabCall(batch.ID, call, *step)
		return true, nil
	}
	return false, nil
}

func (a *App) runLabCall(batchID int64, call lab.Call, step lab.Step) {
	defer a.wg.Done()
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	result, callErr := analysis.Lab(ctx, a.cfg.Bub, step.Task, step.Input)
	cancel()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(a.ctx), 5*time.Second)
	_, saveErr := a.db.MutateLabBatch(finishCtx, batchID, func(s *lab.State) error {
		if call.ID > len(s.Calls) {
			return store.ErrLabConflict
		}
		c := &s.Calls[call.ID-1]
		if c.Status != "running" {
			return store.ErrLabConflict
		}
		c.Status = "success"
		c.Result = result.Result
		c.ReturnedModel = result.ReturnedModel
		c.RequestID = result.RequestID
		c.Usage = result.Usage
		c.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if callErr != nil {
			c.Status = "failed"
			c.Error = callErr.Error()
		}
		if a.ctx.Err() != nil {
			s.Status = "paused"
			s.Error = "服务已停止，可继续此固定批次"
		}
		return nil
	})
	finishCancel()
	a.finish("lab", errors.Join(callErr, saveErr))
	slog.Info("offline lab call finished", "batch_id", batchID, "call_id", call.ID, "task", step.Task, "success", callErr == nil && saveErr == nil)
	select {
	case a.analysisWake <- struct{}{}:
	default:
	}
}
