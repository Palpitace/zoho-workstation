// input: 固定配置、Go 独占存储和只读 Zoho 客户端。
// output: 有界同步、共享 Bub 槽轮转总结/案例/建议、独立 Jev 与持久恢复；环境故障先阻断。
// pos: 单 Go 服务的后台生命周期编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/syncer"
	"zoho-workstation/internal/zoho"
)

type App struct {
	cfg             config.Config
	db              *store.Store
	zoho            *zoho.Client
	csrf            string
	mu              sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	started, closed bool
	busy            map[string]bool
	lastError       map[string]string
	analysisWake    chan struct{}
	bubNext         int
	recovery        map[string]map[string]int64
	startedAt       string
	nextSync        time.Time
	lastSyncResult  syncer.Result
	releases        []func()
	wg              sync.WaitGroup
}

var errPythonUnavailable = errors.New("Bub Python environment is unavailable")

func New(cfg config.Config, db *store.Store, client *zoho.Client) (*App, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, errors.New("session token unavailable")
	}
	if cfg.SyncInterval <= 0 {
		return nil, errors.New("sync interval must be positive")
	}
	if cfg.SyncStartTime.IsZero() {
		cfg.SyncStartTime, _ = time.Parse(time.RFC3339, config.DefaultSyncStartTime)
	}
	if cfg.SyncBatchSize == 0 {
		cfg.SyncBatchSize = 9
	}
	if cfg.SyncConcurrency == 0 {
		cfg.SyncConcurrency = 3
	}
	if cfg.SyncBatchSize < 1 || cfg.SyncBatchSize > 50 || cfg.SyncConcurrency < 1 || cfg.SyncConcurrency > 5 {
		return nil, errors.New("invalid sync batch size or concurrency")
	}
	return &App{cfg: cfg, db: db, zoho: client, csrf: hex.EncodeToString(token[:]), busy: map[string]bool{}, lastError: map[string]string{}, analysisWake: make(chan struct{}, 1), recovery: map[string]map[string]int64{}}, nil
}

// Start is called only after the HTTP listener is successfully acquired.
func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started || a.closed {
		return errors.New("application already started or closed")
	}
	for _, kind := range []string{"sync", "analysis"} {
		release, err := a.db.Lock(kind)
		if err != nil {
			for _, r := range a.releases {
				r()
			}
			a.releases = nil
			return err
		}
		a.releases = append(a.releases, release)
	}
	if err := a.db.RecoverAnalysis(ctx); err != nil {
		for _, r := range a.releases {
			r()
		}
		a.releases = nil
		return err
	}
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.started = true
	a.startedAt = time.Now().UTC().Format(time.RFC3339Nano)
	a.nextSync = time.Now().Add(a.cfg.SyncInterval)
	a.wg.Add(1)
	go a.loop()
	return nil
}

func (a *App) Close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
	for _, release := range a.releases {
		release()
	}
}

func (a *App) loop() {
	defer a.wg.Done()
	_ = a.RequestSync()
	a.scheduleAnalysis()
	ticker := time.NewTicker(a.cfg.SyncInterval)
	defer ticker.Stop()
	retryTicker := time.NewTicker(30 * time.Second)
	defer retryTicker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-retryTicker.C:
			a.scheduleAnalysis()
		case <-a.analysisWake:
			a.scheduleAnalysis()
		case tick := <-ticker.C:
			a.mu.Lock()
			a.nextSync = tick.Add(a.cfg.SyncInterval)
			a.mu.Unlock()
			_ = a.RequestSync()
			a.scheduleAnalysis()
		}
	}
}

// Automatic analysis follows saved business data, not the development manifest.
func (a *App) automaticallyEligible(record store.Record) bool {
	if created, err := time.Parse(time.RFC3339Nano, record.CreatedTime); err == nil && created.Before(a.cfg.SyncStartTime) {
		return false
	}
	return !record.Pending && !record.SyncStopped && record.Complete
}

func (a *App) RequestSync() error {
	a.mu.Lock()
	if !a.started || a.closed {
		a.mu.Unlock()
		return errors.New("service is stopping")
	}
	if a.busy["sync"] {
		a.mu.Unlock()
		return store.ErrBusy
	}
	a.busy["sync"] = true
	a.wg.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.wg.Done()
		backoff := 5 * time.Second
		defer func() {
			a.mu.Lock()
			a.busy["sync"] = false
			a.mu.Unlock()
		}()
		for {
			started := time.Now()
			ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
			result, err := syncer.Run(ctx, a.zoho, a.db, syncer.Options{StartTime: a.cfg.SyncStartTime, MaxTickets: a.cfg.SyncBatchSize, Concurrency: a.cfg.SyncConcurrency, Lookback: a.cfg.SyncLookback})
			cancel()
			a.mu.Lock()
			a.lastSyncResult = result
			a.lastError["sync"] = ""
			if err != nil {
				a.lastError["sync"] = err.Error()
			}
			a.mu.Unlock()
			slog.Info("automatic sync batch finished", "duration_ms", time.Since(started).Milliseconds(), "processed", result.Fetched+result.Failed+result.Stopped, "fetched", result.Fetched, "failed", result.Failed, "stopped", result.Stopped, "remaining", result.Remaining)
			if a.ctx.Err() != nil {
				return
			}
			a.scheduleAnalysis()
			retryAfter := zoho.RetryAfter(err)
			if result.Remaining == 0 && retryAfter == 0 {
				return
			}
			// A single worker drains bounded batches without waiting for the
			// incremental polling interval. Never hold a lock while waiting.
			delay := time.Second
			if err != nil {
				delay = max(backoff, retryAfter)
				backoff = min(backoff*2, 2*time.Minute)
			} else {
				backoff = 5 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-a.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return nil
}

func (a *App) model(kind string) (config.Model, string, error) {
	switch kind {
	case "jev":
		return a.cfg.Jev, analysis.JevSchema, nil
	case "bub":
		return a.cfg.Bub, analysis.BubSchema, nil
	case "case":
		return a.cfg.Bub, analysis.CaseSchema, nil
	case "advice":
		return a.cfg.Bub, analysis.AdviceSchema, nil
	default:
		return config.Model{}, "", errors.New("unknown analysis capability")
	}
}

func (a *App) scheduleAnalysis() {
	records, err := a.db.List(a.ctx, "", "")
	if err != nil {
		a.setError("analysis", err)
		return
	}
	// Rotate the shared Bub slot so a busy summary backlog cannot starve knowledge.
	a.mu.Lock()
	next := a.bubNext
	a.mu.Unlock()
	bubKinds := []string{"bub", "case", "advice"}
	kinds := []string{"jev"}
	for i := range bubKinds {
		kinds = append(kinds, bubKinds[(next+i)%len(bubKinds)])
	}
	pythonChecked := false
	var pythonErr error
	for _, kind := range kinds {
		model, _, _ := a.model(kind)
		if !model.Enabled {
			continue
		}
		if kind != "jev" {
			if kind != "bub" && analysis.CheckKnowledge() != nil {
				continue
			}
			if !pythonChecked {
				pythonErr = analysis.CheckPython(a.ctx)
				pythonChecked = true
			}
			if pythonErr != nil {
				a.setError(kind, errors.Join(errPythonUnavailable, pythonErr))
				continue
			}
		}
		for _, record := range records {
			if !a.automaticallyEligible(record) {
				a.mu.Lock()
				delete(a.recovery[kind], record.ID)
				a.mu.Unlock()
				continue
			}
			started, err := a.startAnalysis(record.ID, kind, false)
			if errors.Is(err, store.ErrBusy) || started {
				break
			}
			if err != nil {
				a.setError(kind, err)
			}
			if a.ctx.Err() != nil {
				return
			}
		}
	}
}

func (a *App) RequestAnalysis(id, kind string) error {
	if kind != "jev" && a.cfg.Bub.Enabled {
		if kind != "bub" {
			if err := analysis.CheckKnowledge(); err != nil {
				return err
			}
		}
		a.mu.Lock()
		ctx := a.ctx
		a.mu.Unlock()
		if ctx != nil {
			if err := analysis.CheckPython(ctx); err != nil {
				return errors.Join(errPythonUnavailable, err)
			}
		}
	}
	_, err := a.startAnalysis(id, kind, true)
	return err
}

func (a *App) startAnalysis(id, kind string, force bool) (bool, error) {
	model, _, err := a.model(kind)
	if err != nil {
		return false, err
	}
	if !model.Enabled {
		return false, errors.New("analysis capability is disabled")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started || a.closed {
		return false, errors.New("service is stopping")
	}
	if a.busy[kind] || (kind != "jev" && (a.busy["bub"] || a.busy["case"] || a.busy["advice"])) {
		return false, store.ErrBusy
	}
	req, input, prepareErr := analysisRequest(a.ctx, a.db, a.cfg, id, kind, DevelopmentRound)
	if prepareErr == nil && kind == "jev" && len(input) > 24*1024 {
		prepareErr = errors.New("Jev summary input exceeds 24 KiB; input was not truncated")
	}
	if prepareErr != nil {
		delete(a.recovery[kind], id)
	}
	if errors.Is(prepareErr, errAwaitingBub) || errors.Is(prepareErr, errNoKnowledge) {
		if force {
			return false, prepareErr
		}
		return false, nil
	}
	if prepareErr != nil {
		if req.InputHash != "" {
			if e := a.db.RecordAnalysisFailure(a.ctx, req, prepareErr); e != nil {
				return false, e
			}
		}
		return false, prepareErr
	}
	if kind == "case" && !force {
		view, err := a.db.Analysis(a.ctx, req)
		if err != nil {
			return false, err
		}
		if view.Success != nil && !view.Stale {
			return false, a.saveCase(a.ctx, req, view.Success.Result)
		}
	}
	// A confirmation names a specific failed run, not future versions of a ticket.
	if runID, queued := a.recovery[kind][id]; queued {
		record, err := a.db.Get(a.ctx, id)
		if err != nil {
			return false, err
		}
		view, err := a.db.Analysis(a.ctx, req)
		if err != nil {
			return false, err
		}
		delete(a.recovery[kind], id)
		if a.automaticallyEligible(record) && view.Current != nil && view.Current.Status == "failed" && view.Current.ID == runID {
			force = true
		}
	}
	// Keep the exact redacted bytes used by both capabilities, never the raw input.
	if err = saveInput(req.InputHash, input); err != nil {
		return false, err
	}
	run, started, err := a.db.StartAnalysis(a.ctx, req, force)
	if err != nil || !started {
		return false, err
	}
	a.busy[kind] = true
	if kind != "jev" {
		for i, k := range []string{"bub", "case", "advice"} {
			if k == kind {
				a.bubNext = (i + 1) % 3
			}
		}
	}
	a.lastError[kind] = ""
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
		var output json.RawMessage
		var returned string
		var callErr error
		if kind == "jev" {
			output, returned, callErr = analysis.Jev(ctx, nil, model, input)
		} else if kind == "bub" {
			output, returned, callErr = analysis.Bub(ctx, model, input)
		} else {
			output, returned, callErr = analysis.Knowledge(ctx, model, kind, input)
		}
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(a.ctx), 3*time.Second)
		saveErr := a.db.FinishAnalysis(finishCtx, run.ID, returned, output, callErr)
		if kind == "case" && callErr == nil && saveErr == nil {
			saveErr = a.saveCase(finishCtx, req, output)
		}
		finishCancel()
		a.finish(kind, errors.Join(callErr, saveErr))
		if err := errors.Join(callErr, saveErr); err != nil {
			slog.Warn("analysis failed", "kind", kind, "ticket_id", id, "error", err)
		} else {
			slog.Info("analysis completed", "kind", kind, "ticket_id", id)
		}

		// Wake the coordinator after completion so the next ticket need not
		// wait for the next Zoho poll. The buffered signal coalesces wakeups.
		defer func() {
			select {
			case a.analysisWake <- struct{}{}:
			default:
			}
		}()

	}()
	return true, nil
}

func saveInput(hash string, input []byte) error {
	if err := os.MkdirAll("data/review", 0700); err != nil {
		return errors.New("cannot create local model input directory")
	}
	path := filepath.Join("data", "review", hash+".json")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect local model input")
	}
	return writePrivateFile(path, input)
}

// Publish a complete private artifact in one rename; interrupted writes never
// truncate an already published manifest or model input.
func writePrivateFile(path string, body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return errors.New("cannot create local review artifact")
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(body)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("cannot write local review artifact")
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return errors.New("cannot publish local review artifact")
	}
	return nil
}

func (a *App) finish(kind string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.busy[kind] = false
	a.lastError[kind] = ""
	if err != nil {
		a.lastError[kind] = err.Error()
	}
}
func (a *App) setError(kind string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastError[kind] = err.Error()
}
