// input: 已保存工单、根配置和研发轮次/数量限制。
// output: 本地脱敏开发副本、独立 Jev/Bub 结果及不含原文的调用统计。
// pos: 分析命令编排与本地验证入口。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
)

const developmentRound = "development-1"

type reviewItem struct {
	TicketID string `json:"ticket_id"`
	Hash     string `json:"input_hash"`
	Path     string `json:"path"`
	Split    string `json:"split"`
}

func analysisRequest(ctx context.Context, db *store.Store, cfg config.Config, id, kind, round string) (store.AnalysisRequest, json.RawMessage, error) {
	snapshot, err := db.Get(ctx, id)
	if err != nil {
		return store.AnalysisRequest{}, nil, err
	}
	input, hash, err := analysis.Input(snapshot.Snapshot, snapshot.Raw)
	model, schema := cfg.Jev.Name, analysis.JevSchema
	if kind == "bub" {
		model, schema = cfg.Bub.Name, analysis.BubSchema
	}
	return store.AnalysisRequest{TicketID: id, Kind: kind, InputHash: hash, SchemaVersion: schema, RequestedModel: model, Round: round}, input, err
}

func prepareInputs(ctx context.Context, db *store.Store, cfg config.Config, limit int) error {
	records, err := db.List(ctx, "", "")
	if err != nil {
		return err
	}
	dir := filepath.Join("data", "review")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	manifest := []reviewItem{}
	manifestPath := filepath.Join(dir, "manifest.json")
	var fixed []reviewItem
	if previous, e := os.ReadFile(manifestPath); e == nil {
		if json.Unmarshal(previous, &fixed) != nil {
			return errors.New("existing review manifest is invalid")
		}
		byID := make(map[string]store.Record, len(records))
		for _, record := range records {
			byID[record.ID] = record
		}
		records = nil
		for _, selected := range fixed {
			record, ok := byID[selected.TicketID]
			if !ok || record.Pending {
				return errors.New("fixed review sample is unavailable")
			}
			records = append(records, record)
		}
		limit = len(records)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	roles := map[string]int{}
	attachments, incomplete := 0, 0
	for _, record := range records {
		if record.Pending {
			continue
		}
		if len(manifest) >= limit {
			break
		}
		request, input, e := analysisRequest(ctx, db, cfg, record.ID, "jev", developmentRound)
		if e != nil {
			return e
		}
		path := filepath.Join(dir, request.InputHash+".json")
		if e = os.WriteFile(path, input, 0600); e != nil {
			return e
		}
		manifest = append(manifest, reviewItem{record.ID, request.InputHash, path, "development"})
		if len(fixed) > 0 {
			manifest[len(manifest)-1].Split = fixed[len(manifest)-1].Split
		}
		if !record.Complete {
			incomplete++
		}
		for _, message := range record.Messages {
			roles[message.Role]++
			attachments += len(message.Attachments)
		}
	}
	// With 20 inputs, reserve the final three for final checking; this is a fixed
	// local split, not human-verified business labels.
	if len(fixed) == 0 && len(manifest) >= 10 {
		for i := len(manifest) - 3; i < len(manifest); i++ {
			manifest[i].Split = "holdout"
		}
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(manifestPath, body, 0600); err != nil {
		return err
	}
	slog.Info("redacted inputs prepared", "count", len(manifest), "incomplete", incomplete, "message_roles", roles, "attachments", attachments)
	return nil
}

func analyzeTickets(ctx context.Context, db *store.Store, cfg config.Config, limit int, kind, round string, force bool) error {
	if kind != "jev" && kind != "bub" {
		return errors.New("analysis kind must be jev or bub")
	}
	model := cfg.Jev
	if kind == "bub" {
		model = cfg.Bub
	}
	if !model.Enabled {
		return errors.New("requested analysis capability is disabled")
	}
	if kind == "bub" {
		if _, err := os.Stat("python/.venv/bin/python"); err != nil {
			return errors.New("Bub Python environment is not installed")
		}
	}
	release, err := lockAnalysis(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer release()
	if err := db.RecoverAnalysis(ctx); err != nil {
		return err
	}
	manifestBytes, err := os.ReadFile("data/review/manifest.json")
	if err != nil {
		return errors.New("prepare local review inputs before development model calls")
	}
	var selected []reviewItem
	if json.Unmarshal(manifestBytes, &selected) != nil {
		return errors.New("review manifest is invalid")
	}
	attempted, failed := 0, 0
	for _, selected := range selected {
		if selected.Split != "development" {
			continue
		}
		if attempted >= limit {
			break
		}
		req, input, err := analysisRequest(ctx, db, cfg, selected.TicketID, kind, round)
		if err != nil {
			return err
		}
		if req.InputHash != selected.Hash {
			return errors.New("review input changed; prepare the local review copy again")
		}
		if kind == "jev" && len(input) > 24*1024 {
			return errors.New("Jev input exceeds conservative 24 KiB limit; no request sent")
		}
		run, start, err := db.StartAnalysis(ctx, req, force)
		if err != nil {
			return err
		}
		if !start {
			continue
		}
		attempted++
		callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		var output json.RawMessage
		var returnedModel string
		var callErr error
		if kind == "jev" {
			output, returnedModel, callErr = analysis.Jev(callCtx, nil, model, input)
		} else {
			output, returnedModel, callErr = analysis.Bub(callCtx, model, input)
		}
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err = db.FinishAnalysis(finishCtx, run.ID, returnedModel, output, callErr)
		finishCancel()
		if err != nil {
			return err
		}
		if callErr != nil {
			failed++
			slog.Warn("analysis attempt failed", "kind", kind, "error", callErr)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	budget, err := db.AnalysisBudget(ctx, round)
	if err != nil {
		return err
	}
	slog.Info("analysis run finished", "kind", kind, "attempted", attempted, "failed", failed, "round_requests", budget.Round, "goal_requests", budget.Total)
	if failed > 0 {
		return fmt.Errorf("analysis failed for %d ticket(s)", failed)
	}
	return nil
}

// The current local deployment targets Linux. Flock is released by the kernel
// even after a crash, so a second command cannot "recover" an active request.
func lockAnalysis(databasePath string) (func(), error) {
	canonical, err := filepath.EvalSymlinks(databasePath)
	if err != nil {
		return nil, errors.New("analysis database path unavailable")
	}
	lock, err := os.OpenFile(canonical+".analysis.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("analysis lock unavailable")
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("analysis is already running for this database")
	}
	return func() { syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); lock.Close() }, nil
}
