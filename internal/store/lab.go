// input: 固定脱敏批次、模型调用状态和追加的人工评分。
// output: 不可覆盖的批次输入与回复/评分历史，以及可恢复的执行状态。
// pos: SQLite 离线实验生命周期边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"zoho-workstation/internal/lab"
	"zoho-workstation/internal/ticket"
)

var ErrLabConflict = errors.New("lab history cannot be overwritten")

const labColumns = "id,revision,created_at,updated_at,spec_json,state_json"

func (s *Store) initLab() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS lab_batches (
  id INTEGER PRIMARY KEY, revision INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  spec_json TEXT NOT NULL, state_json TEXT NOT NULL
 )`)
	return err
}

func (s *Store) CreateLabBatch(ctx context.Context, spec lab.Spec) (lab.Batch, error) {
	if spec.Schema != lab.Schema || strings.TrimSpace(spec.Model) == "" || strings.TrimSpace(spec.ModelURLHash) == "" || len(spec.Samples) == 0 || len(spec.Samples) > lab.MaxTickets {
		return lab.Batch{}, errors.New("invalid lab batch specification")
	}
	seen := map[string]bool{}
	for _, sample := range spec.Samples {
		if sample.TicketID == "" || seen[sample.TicketID] || sample.Group == "" || (sample.Split != "learn" && sample.Split != "development" && sample.Split != "holdout") || len(sample.Nodes) > 3 {
			return lab.Batch{}, errors.New("invalid lab sample")
		}
		seen[sample.TicketID] = true
	}
	body, err := json.Marshal(spec)
	if err != nil || len(body) > 32*1024*1024 {
		return lab.Batch{}, errors.New("lab batch input exceeds supported limits")
	}
	state := lab.State{Status: "draft", Phase: "learning", Calls: []lab.Call{}, Feedback: []lab.Feedback{}, Drafts: map[string]int64{}}
	stateBody, _ := json.Marshal(state)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, "INSERT INTO lab_batches(created_at,updated_at,spec_json,state_json) VALUES(?,?,?,?)", now, now, string(body), string(stateBody))
	if err != nil {
		return lab.Batch{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return lab.Batch{}, err
	}
	return s.LabBatch(ctx, id)
}

func (s *Store) LabBatch(ctx context.Context, id int64) (lab.Batch, error) {
	return scanLab(s.db.QueryRowContext(ctx, "SELECT "+labColumns+" FROM lab_batches WHERE id=?", id))
}

func scanLab(row interface{ Scan(...any) error }) (lab.Batch, error) {
	var batch lab.Batch
	var spec, state string
	err := row.Scan(&batch.ID, &batch.Revision, &batch.CreatedAt, &batch.UpdatedAt, &spec, &state)
	if err == nil {
		err = json.Unmarshal([]byte(spec), &batch.Spec)
	}
	if err == nil {
		err = json.Unmarshal([]byte(state), &batch.State)
	}
	return batch, err
}

// ListLabBatches omits the large evidence/result payloads; detail stays available
// through LabBatch. Counts and call status remain available without extra rows.
func (s *Store) ListLabBatches(ctx context.Context) ([]lab.Batch, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+labColumns+" FROM lab_batches ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batches := []lab.Batch{}
	for rows.Next() {
		batch, err := scanLab(rows)
		if err != nil {
			return nil, err
		}
		batch.Spec.Knowledge = nil
		batch.Spec.Feedback = nil
		for i := range batch.Spec.Samples {
			batch.Spec.Samples[i].Snapshot = ticket.Snapshot{}
			batch.Spec.Samples[i].Nodes = nil
		}
		for i := range batch.State.Calls {
			batch.State.Calls[i].Input = nil
			batch.State.Calls[i].Result = nil
			batch.State.Calls[i].Usage = nil
		}
		batch.State.Feedback = nil
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}

// MutateLabBatch reads the latest state while holding the database transaction.
// The callback must not call another Store method (the store has one connection).
func (s *Store) MutateLabBatch(ctx context.Context, id int64, change func(*lab.State) error) (lab.Batch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return lab.Batch{}, err
	}
	defer tx.Rollback()
	batch, err := scanLab(tx.QueryRowContext(ctx, "SELECT "+labColumns+" FROM lab_batches WHERE id=?", id))
	if err != nil {
		return lab.Batch{}, err
	}
	original, _ := json.Marshal(batch.State)
	if err = change(&batch.State); err != nil {
		return lab.Batch{}, err
	}
	var before lab.State
	if err = json.Unmarshal(original, &before); err != nil {
		return lab.Batch{}, err
	}
	if err = validateLabChange(before, batch.State); err != nil {
		return lab.Batch{}, err
	}
	body, err := json.Marshal(batch.State)
	if err != nil || len(body) > 128*1024*1024 {
		return lab.Batch{}, errors.New("lab history exceeds supported limits")
	}
	batch.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, "UPDATE lab_batches SET state_json=?,updated_at=?,revision=revision+1 WHERE id=? AND revision=?", string(body), batch.UpdatedAt, id, batch.Revision)
	if err != nil {
		return lab.Batch{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return lab.Batch{}, err
	}
	if count != 1 {
		return lab.Batch{}, ErrLabConflict
	}
	if err = tx.Commit(); err != nil {
		return lab.Batch{}, err
	}
	batch.Revision++
	return batch, nil
}

func validateLabChange(before, after lab.State) error {
	switch after.Status {
	case "draft", "running", "paused", "completed", "failed":
	default:
		return errors.New("invalid lab status")
	}
	if len(after.Phase) > 64 || len(after.Error) > 512 || len(after.Calls) > 4096 || len(after.Feedback) > 4096 || len(after.Drafts) > 256 {
		return errors.New("lab history exceeds supported limits")
	}
	if len(after.Calls) < len(before.Calls) || len(after.Feedback) < len(before.Feedback) {
		return ErrLabConflict
	}
	if err := validateLabCalls(before.Calls, after.Calls); err != nil {
		return err
	}
	if err := validateLabFeedback(before.Feedback, after.Feedback, after.Calls); err != nil {
		return err
	}
	for key, value := range before.Drafts {
		if after.Drafts[key] != value {
			return ErrLabConflict
		}
	}
	return nil
}

// RecoverLabBatches does not repeat an uncertain paid request on startup. The
// operator can resume the same batch; interrupted attempts remain in history.
func (s *Store) RecoverLabBatches(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM lab_batches WHERE json_extract(state_json,'$.status')='running' OR EXISTS(SELECT 1 FROM json_each(json_extract(state_json,'$.calls')) WHERE json_extract(value,'$.status')='running')`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, err = s.MutateLabBatch(ctx, id, func(state *lab.State) error {
			state.Status = "paused"
			state.Error = "process interrupted; resume manually"
			for i := range state.Calls {
				if state.Calls[i].Status == "running" {
					state.Calls[i].Status = "interrupted"
					state.Calls[i].Error = "process interrupted"
					state.Calls[i].CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func validateLabCalls(before, after []lab.Call) error {
	running := 0
	for i, call := range after {
		if call.ID != i+1 || call.Key == "" || call.NodeID == "" || len(call.Key) > 256 || call.InputHash == "" || len(call.Error) > 512 || !json.Valid(call.Input) {
			return errors.New("invalid lab call")
		}
		if call.Task != "reply" && call.Task != "evaluate" && call.Task != "propose" {
			return errors.New("invalid lab task")
		}
		if i < len(before) {
			old := before[i]
			if old.Status == "running" {
				fixed := call
				fixed.Status = old.Status
				fixed.CompletedAt = old.CompletedAt
				fixed.Result = old.Result
				fixed.ReturnedModel = old.ReturnedModel
				fixed.Usage = old.Usage
				fixed.RequestID = old.RequestID
				fixed.Error = old.Error
				if !reflect.DeepEqual(fixed, old) {
					return ErrLabConflict
				}
			} else if !reflect.DeepEqual(call, old) {
				return ErrLabConflict
			}
		} else if call.Status != "running" {
			return errors.New("lab calls must be reserved before completion")
		}
		switch call.Status {
		case "running":
			running++
		case "success":
			if call.CompletedAt == "" || !json.Valid(call.Result) {
				return errors.New("invalid completed lab call")
			}
		case "failed", "interrupted":
			if call.CompletedAt == "" {
				return errors.New("invalid completed lab call")
			}
		default:
			return errors.New("invalid lab call status")
		}
	}
	if running > 1 {
		return ErrLabConflict
	}
	return nil
}

func validateLabFeedback(before, after []lab.Feedback, calls []lab.Call) error {
	for i, feedback := range after {
		if i < len(before) {
			if !reflect.DeepEqual(before[i], feedback) {
				return ErrLabConflict
			}
			continue
		}
		if feedback.ID != i+1 || feedback.Score < 1 || feedback.Score > 5 || feedback.CallID < 1 || feedback.CallID > len(calls) || len(feedback.Note) > 16000 || len(feedback.EditedReply) > 24000 || len(feedback.Tags) > 16 {
			return errors.New("invalid lab feedback")
		}
		call := calls[feedback.CallID-1]
		if call.Status != "success" || call.Task != "reply" || feedback.NodeID != call.NodeID {
			return errors.New("feedback requires a completed reply version")
		}
		for _, tag := range feedback.Tags {
			if len(tag) > 64 {
				return errors.New("invalid feedback tag")
			}
		}
	}
	return nil
}
