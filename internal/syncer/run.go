// input: 只读 Zoho 客户端、SQLite 存储、固定采样限制和回查窗口。
// output: 有界并发读取、串行持久化及删除/移出停止；限流取消不丢水位。
// pos: 拉取、清洗与保存的同步编排边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package syncer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
	"zoho-workstation/internal/zoho"
)

type Options struct {
	SampleSize  int
	MaxTickets  int
	Concurrency int
	Lookback    time.Duration
}
type Result struct {
	Fetched   int `json:"fetched"`
	Failed    int `json:"failed"`
	Stopped   int `json:"stopped"`
	Remaining int `json:"remaining"`
}

// Run is serialized by the application. Failed IDs are durable retry work,
// independent of the watermark, and are retried at most once per five minutes.
func Run(ctx context.Context, client *zoho.Client, db *store.Store, opts Options) (result Result, runErr error) {
	if opts.SampleSize == 0 {
		opts.SampleSize = 50
	}
	if opts.Concurrency == 0 {
		opts.Concurrency = 1
	}
	if opts.Lookback == 0 {
		opts.Lookback = 24 * time.Hour
	}
	if opts.SampleSize < 1 || opts.SampleSize > 50 || opts.MaxTickets < 0 || opts.Lookback < 0 || opts.Concurrency < 1 || opts.Concurrency > 5 {
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
	scanned := false
	defer func() {
		watermark := ""
		if scanned && result.Remaining == 0 {
			watermark = until.Format(time.RFC3339Nano)
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := db.Finish(finishCtx, watermark, runErr); err != nil {
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
	}
	records, err := db.List(ctx, "", "")
	if err != nil {
		return result, err
	}
	byID := make(map[string]store.Record, len(records))
	attemptedAt := make(map[string]time.Time, len(records))
	for _, r := range records {
		byID[r.ID] = r
		attemptedAt[r.ID], _ = time.Parse(time.RFC3339Nano, r.CheckedAt)
	}
	candidates, retries := []string{}, []string{}
	now := time.Now()
	for _, id := range ids {
		r := byID[id]
		checked := attemptedAt[id]
		if r.SyncStopped {
			continue
		}
		if r.SyncError != "" {
			if !checked.IsZero() && !checked.Before(until) {
				continue
			}
			// Keep this ID outside Changed's time window until it succeeds. A search
			// omission or newer watermark must never discard its retry obligation.
			if checked.IsZero() || !now.Before(checked.Add(5*time.Minute)) {
				retries = append(retries, id)
			}
			continue
		}
		if !checked.IsZero() && !checked.Before(until) && !r.Pending && r.Complete {
			continue
		}
		if r.Pending || !r.Complete {
			retries = append(retries, id)
		} else {
			candidates = append(candidates, id)
		}
	}
	if state.Watermark != "" {
		since, err := time.Parse(time.RFC3339Nano, state.Watermark)
		if err != nil {
			return result, errors.New("stored synchronization watermark is invalid")
		}
		candidates, err = client.Changed(ctx, candidates, since.Add(-opts.Lookback), until)
		if err != nil {
			return result, fmt.Errorf("find changed tickets: %w", err)
		}
	}
	ids = append(candidates, retries...)
	sort.SliceStable(ids, func(i, j int) bool { return attemptedAt[ids[i]].Before(attemptedAt[ids[j]]) })
	result.Remaining = len(ids)
	limit := len(ids)
	if opts.MaxTickets > 0 {
		limit = min(limit, opts.MaxTickets)
	}
	for offset := 0; offset < limit; offset += opts.Concurrency {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		wave := ids[offset:min(offset+opts.Concurrency, limit)]
		waveCtx, cancelWave := context.WithCancel(ctx)
		replies := make([]struct {
			raw ticket.RawSnapshot
			err error
		}, len(wave))
		var workers sync.WaitGroup
		for i, id := range wave {
			workers.Go(func() {
				replies[i].raw, replies[i].err = client.Fetch(waveCtx, id)
				if zoho.RetryAfter(replies[i].err) > 0 {
					cancelWave()
				}
			})
		}
		workers.Wait()
		cancelWave()
		var rateLimit error
		for _, reply := range replies {
			if zoho.RetryAfter(reply.err) > zoho.RetryAfter(rateLimit) {
				rateLimit = reply.err
			}
		}
		for i, id := range wave {
			err := replies[i].err
			// A peer cancelled by 429 stays pending, without a false five-minute
			// failure cooldown. No new wave starts until the caller's backoff.
			if rateLimit != nil && ctx.Err() == nil && errors.Is(err, context.Canceled) {
				continue
			}
			result.Remaining--
			if errors.Is(err, zoho.ErrOutsideDepartment) || errors.Is(err, zoho.ErrDeleted) {
				if err = db.StopSync(ctx, id, err.Error()); err != nil {
					return result, err
				}
				result.Stopped++
				continue
			}
			if err == nil {
				err = saveTicket(ctx, db, id, replies[i].raw)
			}
			if err != nil {
				result.Failed++
				failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				failureErr := db.Failure(failureCtx, id, err.Error())
				cancel()
				if failureErr != nil {
					return result, fmt.Errorf("record ticket failure: %w", failureErr)
				}
				continue
			}
			result.Fetched++
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if rateLimit != nil {
			return result, rateLimit
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	scanned = true
	if result.Failed > 0 {
		return result, fmt.Errorf("synchronization deferred: %d ticket(s) failed; retry state retained", result.Failed)
	}
	return result, nil
}

func saveTicket(ctx context.Context, db *store.Store, id string, raw ticket.RawSnapshot) error {
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
			return fmt.Errorf("conversation incomplete (%s); previous complete snapshot retained", strings.Join(snapshot.Warnings, ", "))
		}
	}
	if err = db.Save(ctx, raw, snapshot); err != nil {
		return fmt.Errorf("save ticket: %w", err)
	}
	if !snapshot.Complete {
		return fmt.Errorf("conversation incomplete (%s); partial snapshot saved", strings.Join(snapshot.Warnings, ", "))
	}
	return nil
}
