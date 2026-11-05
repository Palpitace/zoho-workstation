// input: 工单、能力、输入 Hash、版本、模型标识及结构化分析结果。
// output: 独立分析记录、本地校验失败、成功结果保留和跨重启调用计数（不限制累计次数）。
// pos: SQLite AI 结果与调用计数边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type AnalysisRequest struct {
	TicketID       string `json:"ticket_id"`
	Kind           string `json:"kind"`
	InputHash      string `json:"input_hash"`
	SchemaVersion  string `json:"schema_version"`
	RequestedModel string `json:"requested_model"`
	Round          string `json:"round"`
}
type AnalysisRun struct {
	AnalysisRequest
	ID            int64           `json:"id"`
	Status        string          `json:"status"`
	ReturnedModel string          `json:"returned_model,omitempty"`
	StartedAt     string          `json:"started_at"`
	CompletedAt   string          `json:"completed_at,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
}
type AnalysisView struct {
	Current *AnalysisRun `json:"current"`
	Success *AnalysisRun `json:"success"`
	Stale   bool         `json:"stale"`
	// Derived from matching history, so a retry cannot re-block Bub after failure.
	HasFinishedAttempt bool `json:"-"`
}
type Budget struct {
	Total int `json:"total"`
	Round int `json:"round"`
}

func (s *Store) AnalysisBudget(ctx context.Context, round string) (Budget, error) {
	var b Budget
	err := s.db.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(round=?),0) FROM ai_runs WHERE status!='invalid'", round).Scan(&b.Total, &b.Round)
	return b, err
}

// RecordAnalysisFailure records a local rejection before any provider request.
// The caller supplies a safe error without credentials or ticket content.
func (s *Store) RecordAnalysisFailure(ctx context.Context, req AnalysisRequest, failure error) error {
	if failure == nil {
		return errors.New("local analysis failure is required")
	}
	if (req.Kind != "jev" && req.Kind != "bub") || req.InputHash == "" || req.SchemaVersion == "" || req.RequestedModel == "" || req.Round == "" {
		return errors.New("analysis metadata incomplete")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_runs(ticket_id,kind,input_hash,schema_version,requested_model,round,status,started_at,completed_at,error)
		SELECT ?,?,?,?,?,?,'invalid',?,?,? WHERE NOT EXISTS (
			SELECT 1 FROM ai_runs WHERE ticket_id=? AND kind=? AND input_hash=? AND schema_version=? AND requested_model=? AND status='invalid'
		)`, req.TicketID, req.Kind, req.InputHash, req.SchemaVersion, req.RequestedModel, req.Round, now, now, failure.Error(), req.TicketID, req.Kind, req.InputHash, req.SchemaVersion, req.RequestedModel)
	return err
}

// StartAnalysis records attempts before network access without a cumulative quota.
// Interrupted attempts remain counted; per-input dedup and retry guards still apply.
func (s *Store) StartAnalysis(ctx context.Context, req AnalysisRequest, force bool) (AnalysisRun, bool, error) {
	run := AnalysisRun{AnalysisRequest: req, Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if (req.Kind != "jev" && req.Kind != "bub") || req.InputHash == "" || req.SchemaVersion == "" || req.RequestedModel == "" || req.Round == "" {
		return run, false, errors.New("analysis metadata incomplete")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return run, false, err
	}
	defer tx.Rollback()
	var running, success, failures, invalid int
	err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(status='running'),0),coalesce(sum(status='success'),0),coalesce(sum(status='failed'),0),coalesce(sum(status='invalid'),0) FROM ai_runs WHERE ticket_id=? AND kind=? AND input_hash=? AND schema_version=? AND requested_model=?`, req.TicketID, req.Kind, req.InputHash, req.SchemaVersion, req.RequestedModel).Scan(&running, &success, &failures, &invalid)
	if err != nil {
		return run, false, err
	}
	if running > 0 || (!force && (success > 0 || failures >= 2 || invalid > 0)) {
		return run, false, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO ai_runs(ticket_id,kind,input_hash,schema_version,requested_model,round,status,started_at) VALUES(?,?,?,?,?,?,'running',?)`, req.TicketID, req.Kind, req.InputHash, req.SchemaVersion, req.RequestedModel, req.Round, run.StartedAt)
	if err != nil {
		return run, false, err
	}
	if run.ID, err = result.LastInsertId(); err != nil {
		return run, false, err
	}
	err = tx.Commit()
	return run, err == nil, err
}

func (s *Store) FinishAnalysis(ctx context.Context, id int64, returnedModel string, result json.RawMessage, runErr error) error {
	status, message := "success", ""
	if runErr != nil {
		status, message, result = "failed", runErr.Error(), nil
	} else if !json.Valid(result) {
		return errors.New("analysis result is not JSON")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE ai_runs SET status=?,returned_model=?,result_json=?,error=?,completed_at=? WHERE id=? AND status='running'`, status, returnedModel, []byte(result), message, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("analysis attempt is no longer running")
	}
	return err
}

// RecoverAnalysis is called once by the sole application process at startup.
func (s *Store) RecoverAnalysis(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ai_runs SET status='failed',error='interrupted before result was saved',completed_at=? WHERE status='running'`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) Analysis(ctx context.Context, req AnalysisRequest) (AnalysisView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,input_hash,schema_version,requested_model,round,status,returned_model,started_at,completed_at,result_json,error FROM ai_runs WHERE ticket_id=? AND kind=? ORDER BY id DESC`, req.TicketID, req.Kind)
	if err != nil {
		return AnalysisView{}, err
	}
	defer rows.Close()
	var view AnalysisView
	for rows.Next() {
		r := AnalysisRun{AnalysisRequest: AnalysisRequest{TicketID: req.TicketID, Kind: req.Kind}}
		var body []byte
		if err = rows.Scan(&r.ID, &r.InputHash, &r.SchemaVersion, &r.RequestedModel, &r.Round, &r.Status, &r.ReturnedModel, &r.StartedAt, &r.CompletedAt, &body, &r.Error); err != nil {
			return view, err
		}
		r.Result = json.RawMessage(body)
		matches := r.InputHash == req.InputHash && r.SchemaVersion == req.SchemaVersion && r.RequestedModel == req.RequestedModel
		if matches && r.Status != "running" {
			view.HasFinishedAttempt = true
		}
		if matches && view.Current == nil {
			copy := r
			view.Current = &copy
		}
		if r.Status == "success" && (view.Success == nil || (view.Stale && matches)) {
			copy := r
			view.Success = &copy
			view.Stale = !matches
		}
	}
	return view, rows.Err()
}
