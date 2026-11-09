// input: 当前工单版本、持久分析历史和已有调度开关。
// output: 列表/详情共用任务状态与 Bub 分类型积压、活动工单及失败记录。
// pos: 只读任务展示派生，不创建第二套队列。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"time"
	"zoho-workstation/internal/store"
)

type taskItem struct {
	TicketID       string `json:"ticket_id"`
	TicketNumber   string `json:"ticket_number,omitempty"`
	Kind           string `json:"kind"`
	State          string `json:"state"`
	Attempts       int    `json:"attempts"`
	StartedAt      string `json:"started_at,omitempty"`
	ElapsedSeconds int64  `json:"elapsed_seconds,omitempty"`
	LastFailedAt   string `json:"last_failed_at,omitempty"`
	NextRetryAt    string `json:"next_retry_at,omitempty"`
	Error          string `json:"error,omitempty"`
	Reason         string `json:"reason,omitempty"`
}
type bubTasks struct {
	Unit   string         `json:"unit"`
	Counts map[string]int `json:"counts"`
	Active []taskItem     `json:"active"`
	Tasks  []taskItem     `json:"tasks"`
}

func (a *App) taskState(record store.Record, kind string, view analysisView) string {
	if view.TaskState == "running" {
		return "running"
	}
	model, _, _ := a.model(kind)
	if !model.Enabled {
		return "disabled"
	}
	if !a.automaticallyEligible(record) {
		return "inactive"
	}
	if view.WaitingFor != "" {
		return "waiting"
	}
	a.mu.Lock()
	queued := a.recovery[kind][record.ID] != 0
	a.mu.Unlock()
	if queued {
		return "pending"
	}
	return view.TaskState
}

func taskFor(record store.Record, kind string, view analysisView) taskItem {
	item := taskItem{TicketID: record.ID, TicketNumber: displayTicket(record, nil).TicketNumber, Kind: kind, State: view.TaskState, Attempts: view.Attempts, LastFailedAt: view.LastFailedAt, NextRetryAt: view.NextRetryAt, Reason: view.WaitingReason}
	if view.Current != nil {
		item.Error = view.Current.Error
		if view.Current.Status == "running" {
			item.StartedAt = view.Current.StartedAt
		}
	}
	return item
}

// The active row comes from the same history read as its pending/retry state.
func addTask(tasks *bubTasks, record store.Record, kind string, view analysisView) {
	item := taskFor(record, kind, view)
	if item.State != "running" {
		tasks.Counts[item.State]++
	}
	if item.State != "success" && item.State != "running" {
		tasks.Tasks = append(tasks.Tasks, item)
	}
	if view.Active == nil {
		return
	}
	active := taskItem{TicketID: record.ID, TicketNumber: item.TicketNumber, Kind: kind, State: "running", StartedAt: view.Active.StartedAt, Attempts: view.Attempts}
	if start, err := time.Parse(time.RFC3339Nano, active.StartedAt); err == nil {
		active.ElapsedSeconds = max(0, int64(time.Since(start).Seconds()))
	}
	tasks.Active = append(tasks.Active, active)
	tasks.Counts["running"]++
}
