// input: 固定配置、Go 独占存储和只读 Zoho 客户端。
// output: 有界并发同步、可取消退避及独立 Jev/Bub 调度，不按累计次数拦截。
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
	selectionMu     sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	started, closed bool
	busy            map[string]bool
	lastError       map[string]string
	eligible        []string
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
	if cfg.SyncBatchSize == 0 {
		cfg.SyncBatchSize = 9
	}
	if cfg.SyncConcurrency == 0 {
		cfg.SyncConcurrency = 3
	}
	if cfg.SyncBatchSize < 1 || cfg.SyncBatchSize > 50 || cfg.SyncConcurrency < 1 || cfg.SyncConcurrency > 5 {
		return nil, errors.New("invalid sync batch size or concurrency")
	}
	return &App{cfg: cfg, db: db, zoho: client, csrf: hex.EncodeToString(token[:]), busy: map[string]bool{}, lastError: map[string]string{}}, nil
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
	a.loadSelection()
	_ = a.RequestSync()
	a.scheduleAnalysis()
	ticker := time.NewTicker(a.cfg.SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			_ = a.RequestSync()
			a.scheduleAnalysis()
		}
	}
}

func (a *App) loadSelection() {
	a.selectionMu.Lock()
	defer a.selectionMu.Unlock()
	body, err := os.ReadFile("data/review/manifest.json")
	if errors.Is(err, os.ErrNotExist) || len(body) == 0 || string(body) == "[]" {
		state, e := a.db.State(a.ctx)
		if e != nil || state.Ready < 20 {
			return
		}
		if e = PrepareInputs(a.ctx, a.db, a.cfg, 20); e != nil {
			a.setError("analysis", e)
			return
		}
		body, err = os.ReadFile("data/review/manifest.json")
	}
	if err != nil {
		a.setError("analysis", errors.New("cannot read development sample"))
		return
	}
	var manifest []reviewItem
	if json.Unmarshal(body, &manifest) != nil {
		a.setError("analysis", errors.New("development sample manifest invalid"))
		return
	}
	ids, err := a.db.IDs(a.ctx)
	if err != nil {
		return
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	selected := []string{}
	seen := map[string]bool{}
	for _, item := range manifest {
		if item.Split == "development" && allowed[item.TicketID] && !seen[item.TicketID] {
			selected = append(selected, item.TicketID)
			seen[item.TicketID] = true
		}
	}
	a.mu.Lock()
	a.eligible = selected
	a.mu.Unlock()
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
			result, err := syncer.Run(ctx, a.zoho, a.db, syncer.Options{SampleSize: 50, MaxTickets: a.cfg.SyncBatchSize, Concurrency: a.cfg.SyncConcurrency, Lookback: a.cfg.SyncLookback})
			cancel()
			a.mu.Lock()
			a.lastError["sync"] = ""
			if err != nil {
				a.lastError["sync"] = err.Error()
			}
			a.mu.Unlock()
			slog.Info("automatic sync batch finished", "duration_ms", time.Since(started).Milliseconds(), "processed", result.Fetched+result.Failed+result.Stopped, "fetched", result.Fetched, "failed", result.Failed, "stopped", result.Stopped, "remaining", result.Remaining)
			if a.ctx.Err() != nil {
				return
			}
			a.loadSelection()
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
	default:
		return config.Model{}, "", errors.New("unknown analysis capability")
	}
}

func (a *App) scheduleAnalysis() {
	a.mu.Lock()
	ids := append([]string(nil), a.eligible...)
	a.mu.Unlock()
	for _, kind := range []string{"jev", "bub"} {
		model, _, _ := a.model(kind)
		if !model.Enabled {
			continue
		}
		for _, id := range ids {
			started, err := a.startAnalysis(id, kind, false)
			if errors.Is(err, store.ErrBusy) || started {
				break
			}
			if err != nil {
				a.setError(kind, err)
				if errors.Is(err, errPythonUnavailable) {
					break
				}
			}
			if a.ctx.Err() != nil {
				return
			}
		}
	}
}

func (a *App) RequestAnalysis(id, kind string) error {
	_, err := a.startAnalysis(id, kind, true)
	return err
}

// Only automatic summaries wait for the first bounded Jev attempt. A failed or
// unavailable assessment is not a barrier; manual summaries remain independent.
func (a *App) awaitingJev(id string) (bool, error) {
	if !a.cfg.Jev.Enabled {
		return false, nil
	}
	req, _, err := analysisRequest(a.ctx, a.db, a.cfg, id, "jev", DevelopmentRound)
	if err != nil && req.InputHash == "" {
		return false, err
	}
	view, err := a.db.Analysis(a.ctx, req)
	if err != nil {
		return false, err
	}
	if view.Current != nil {
		return !view.HasFinishedAttempt, nil
	}
	return true, nil
}

func (a *App) startAnalysis(id, kind string, force bool) (bool, error) {
	model, _, err := a.model(kind)
	if err != nil {
		return false, err
	}
	if !model.Enabled {
		return false, errors.New("analysis capability is disabled")
	}
	if kind == "bub" {
		if _, err := os.Stat("python/.venv/bin/python"); err != nil {
			return false, errPythonUnavailable
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started || a.closed {
		return false, errors.New("service is stopping")
	}
	if a.busy[kind] {
		return false, store.ErrBusy
	}
	if kind == "bub" && !force {
		waiting, err := a.awaitingJev(id)
		if err != nil || waiting {
			return false, err
		}
	}
	req, input, prepareErr := analysisRequest(a.ctx, a.db, a.cfg, id, kind, DevelopmentRound)
	if prepareErr == nil && kind == "jev" && len(input) > 24*1024 {
		prepareErr = errors.New("Jev input exceeds 24 KiB; conversation was not truncated")
	}
	if prepareErr != nil {
		if req.InputHash != "" {
			if e := a.db.RecordAnalysisFailure(a.ctx, req, prepareErr); e != nil {
				return false, e
			}
		}
		return false, prepareErr
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
		} else {
			output, returned, callErr = analysis.Bub(ctx, model, input)
		}
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(a.ctx), 3*time.Second)
		saveErr := a.db.FinishAnalysis(finishCtx, run.ID, returned, output, callErr)
		finishCancel()
		a.finish(kind, errors.Join(callErr, saveErr))
		// A first failure still releases Bub immediately, before any Jev retry.
		if kind == "jev" && !force && a.cfg.Bub.Enabled && a.ctx.Err() == nil {
			if _, err := a.startAnalysis(id, "bub", false); err != nil && !errors.Is(err, store.ErrBusy) {
				a.setError("bub", err)
			}
		}
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
