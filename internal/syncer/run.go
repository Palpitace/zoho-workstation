// input: 只读 Zoho 客户端、SQLite 存储、采样限制和回查窗口。
// output: 冻结样本的公平分批持久化结果及跨重启恢复、完整成功后提交的固定水位。
// pos: 拉取、清洗与保存的同步编排边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package syncer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
	"zoho-workstation/internal/zoho"
)

type Options struct {
	SampleSize int
	MaxTickets int
	Lookback   time.Duration
}

type Result struct {
	Fetched   int `json:"fetched"`
	Failed    int `json:"failed"`
	Remaining int `json:"remaining"`
}

// Run is serialized by the application; the sample itself is frozen in SQLite.
func Run(ctx context.Context, client *zoho.Client, db *store.Store, opts Options) (result Result, runErr error) {
	if opts.SampleSize == 0 {
		opts.SampleSize = 50
	}
	if opts.Lookback == 0 {
		opts.Lookback = 24 * time.Hour
	}
	if opts.SampleSize < 1 || opts.SampleSize > 50 || opts.MaxTickets < 0 || opts.Lookback < 0 {
		return result, errors.New("invalid synchronization limits")
	}
	state, err := db.State(ctx)
	if err != nil {
		return result, err
	}
	until, err := db.Start(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		if runErr == nil && result.Remaining > 0 {
			return
		}
		// Cancellation must not erase the failure record or advance the cursor.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := db.Finish(finishCtx, until.Format(time.RFC3339Nano), runErr); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("record synchronization result: %w", err))
		}
	}()
	ids, err := db.IDs(ctx)
	if err != nil {
		return result, err
	}
	if len(ids) == 0 {
		ids, err = client.Recent(ctx, opts.SampleSize)
		if err != nil {
			return result, fmt.Errorf("select sample: %w", err)
		}
		if err = db.Reserve(ctx, ids); err != nil {
			return result, err
		}
	} else if state.Watermark != "" && state.Failed == 0 && state.Ready == state.Selected {
		since, err := time.Parse(time.RFC3339Nano, state.Watermark)
		if err != nil {
			return result, errors.New("stored synchronization watermark is invalid")
		}
		ids, err = client.Changed(ctx, ids, since.Add(-opts.Lookback), until)
		if err != nil {
			return result, fmt.Errorf("find changed tickets: %w", err)
		}
	}
	records, err := db.List(ctx, "", "")
	if err != nil {
		return result, err
	}
	checked := make(map[string]bool, len(records))
	attemptedAt := make(map[string]time.Time, len(records))
	for _, record := range records {
		checkedAt, err := time.Parse(time.RFC3339Nano, record.CheckedAt)
		if err == nil {
			attemptedAt[record.ID] = checkedAt
		}
		checked[record.ID] = !record.Pending && record.SyncError == "" && record.Complete && err == nil && !checkedAt.Before(until)
	}
	pending := ids[:0]
	for _, id := range ids {
		if !checked[id] {
			pending = append(pending, id)
		}
	}
	ids = pending
	// Failed attempts remain eligible, but move behind tickets tried less recently.
	// Persisted attempt times preserve this ordering across process restarts.
	sort.SliceStable(ids, func(i, j int) bool { return attemptedAt[ids[i]].Before(attemptedAt[ids[j]]) })
	result.Remaining = len(ids)
	for _, id := range ids {
		if opts.MaxTickets > 0 && result.Fetched+result.Failed >= opts.MaxTickets {
			break
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Remaining--
		if err := syncTicket(ctx, client, db, id); err != nil {
			result.Failed++
			failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			failureErr := db.Failure(failureCtx, id, err.Error())
			cancel()
			if failureErr != nil {
				return result, fmt.Errorf("record ticket failure: %w", failureErr)
			}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			continue
		}
		result.Fetched++
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("synchronization incomplete: %d ticket(s) failed", result.Failed)
	}
	return result, nil
}

func syncTicket(ctx context.Context, client *zoho.Client, db *store.Store, id string) error {
	raw, err := client.Fetch(ctx, id)
	if err != nil {
		return err
	}
	snapshot, err := ticket.Normalize(raw)
	if err != nil {
		return fmt.Errorf("normalize ticket: %w", err)
	}
	if !snapshot.Complete {
		previous, err := db.Get(ctx, id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && previous.Complete {
			return errors.New("conversation incomplete; previous complete snapshot retained")
		}
	}
	if err = db.Save(ctx, raw, snapshot); err != nil {
		return fmt.Errorf("save ticket: %w", err)
	}
	if !snapshot.Complete {
		return errors.New("conversation incomplete; partial snapshot saved")
	}
	return nil
}
