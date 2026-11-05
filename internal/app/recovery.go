// input: 用户确认的失败运行 ID、当前快照和现有分析历史。
// output: 校验过的一次性恢复批次，继续复用原有单能力并发及完成唤醒。
// pos: 环境修复后的显式恢复入口。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"errors"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/store"
)

type RetryCandidate struct {
	TicketID string `json:"ticket_id"`
	RunID    int64  `json:"run_id"`
}

var errStaleRecovery = errors.New("待重试工单已变化，请刷新运行状态后重新确认")

// RequestRecovery never resets history or grants retries to future inputs.
// Pending confirmations are deliberately in-memory: restart requires reconfirmation.
func (a *App) RequestRecovery(kind string, candidates []RetryCandidate) error {
	model, _, err := a.model(kind)
	if err != nil {
		return err
	}
	if !model.Enabled {
		return errors.New("analysis capability is disabled")
	}
	if len(candidates) == 0 || len(candidates) > 50 {
		return errors.New("请选择 1 至 50 张待重试工单")
	}
	a.mu.Lock()
	ctx := a.ctx
	ready := a.started && !a.closed
	a.mu.Unlock()
	if !ready {
		return errors.New("service is stopping")
	}
	if kind == "bub" {
		if err := analysis.CheckPython(ctx); err != nil {
			return errors.Join(errPythonUnavailable, err)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("service is stopping")
	}
	if a.busy[kind] || len(a.recovery[kind]) > 0 {
		return store.ErrBusy
	}
	confirmed := make(map[string]int64, len(candidates))
	for _, candidate := range candidates {
		if _, duplicate := confirmed[candidate.TicketID]; duplicate {
			return errStaleRecovery
		}
		record, err := a.db.Get(ctx, candidate.TicketID)
		if err != nil {
			return err
		}
		if !automaticallyEligible(record) {
			return errStaleRecovery
		}
		req, _, err := requestForRecord(ctx, a.db, a.cfg, record, kind, DevelopmentRound)
		if err != nil {
			return err
		}
		view, err := a.db.Analysis(ctx, req)
		if err != nil {
			return err
		}
		if !view.RetryExhausted || view.Current.ID != candidate.RunID {
			return errStaleRecovery
		}
		confirmed[candidate.TicketID] = candidate.RunID
	}
	a.recovery[kind] = confirmed
	select {
	case a.analysisWake <- struct{}{}:
	default:
	}
	return nil
}
