// input: 根 .env、命令行采样数量及本地 SQLite 数据。
// output: 落盘轮转日志、离线检查、受控命令、有界并发采样、显式 IP 监听和自动同步/分析服务。
// pos: Go 服务启动与生命周期编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"gopkg.in/natefinch/lumberjack.v2"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"zoho-workstation/internal/analysis"

	"zoho-workstation/internal/app"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/syncer"
	"zoho-workstation/internal/zoho"
)

func main() { os.Exit(exitCode()) }

func exitCode() int {
	if err := os.MkdirAll("logs", 0700); err != nil {
		fmt.Fprintln(os.Stderr, "cannot create logs directory")
		return 1
	}
	// Check write access before configuring slog; logger errors must not be hidden.
	file, err := os.OpenFile(filepath.Join("logs", "workstation.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot open logs/workstation.log")
		return 1
	}
	file.Close()
	output := &lumberjack.Logger{Filename: filepath.Join("logs", "workstation.log"), MaxSize: 10, MaxBackups: 3, MaxAge: 7}
	defer output.Close()
	level := new(slog.LevelVar)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, output), &slog.HandlerOptions{Level: level})))
	if err := run(level); err != nil {
		slog.Error("operation failed", "error", err)
		return 1
	}
	slog.Info("workstation stopped")
	return 0
}

func run(logLevel *slog.LevelVar) error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	if command != "sample" && command != "serve" && command != "prepare" && command != "analyze" && command != "check" {
		return errors.New("usage: workstation sample | serve | prepare | analyze | check")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	size := flags.Int("size", 50, "unique ticket cap for initial sample, 1–50; frozen after selection")
	defaultMax := 50
	if command == "analyze" {
		defaultMax = 1
	}
	if command == "prepare" {
		defaultMax = 20
	}
	maxTickets := flags.Int("max", defaultMax, "maximum tickets to process in this run")
	round, force, kind := app.DevelopmentRound, false, "jev"
	if command == "analyze" {
		flags.StringVar(&round, "round", app.DevelopmentRound, "persistent label for model request counts; no cumulative quota")
		flags.BoolVar(&force, "force", false, "rerun a completed input; an active request remains protected")
		flags.StringVar(&kind, "kind", "jev", "analysis capability: jev or bub")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *size < 1 || *size > 50 || *maxTickets < 1 || *maxTickets > 50 {
		return errors.New("size and max must be between 1 and 50")
	}
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}
	var level slog.Level
	level.UnmarshalText([]byte(cfg.LogLevel))
	logLevel.Set(level)
	db, err := store.Open(cfg.DatabasePath, cfg.Zoho.DepartmentID)
	if err != nil {
		return err
	}
	defer db.Close()
	if command == "check" {
		for _, name := range []string{"sync", "analysis"} {
			release, err := db.Lock(name)
			if err != nil {
				return err
			}
			release()
		}
		if _, err := os.Stat("web/dist/index.html"); err != nil {
			return errors.New("frontend build not available")
		}
		if cfg.Bub.Enabled {
			if err := analysis.CheckPython(context.Background()); err != nil {
				return err
			}
			if _, err := os.Stat("python/main.py"); err != nil {
				return errors.New("Bub entry not available")
			}
		}
		slog.Info("configuration and local runtime verified; no network requests sent")
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if command == "prepare" {
		release, err := db.Lock("analysis")
		if err != nil {
			return err
		}
		defer release()
		return app.PrepareInputs(ctx, db, cfg, *maxTickets)
	}
	if command == "analyze" {
		return app.AnalyzeTickets(ctx, db, cfg, *maxTickets, kind, round, force)
	}
	client := zoho.New(cfg.Zoho, nil)
	if command == "sample" {
		release, err := db.Lock("sync")
		if err != nil {
			return err
		}
		defer release()
		result, err := syncer.Run(ctx, client, db, syncer.Options{SampleSize: *size, MaxTickets: *maxTickets, Lookback: cfg.SyncLookback, Concurrency: cfg.SyncConcurrency})
		slog.Info("sample run finished", "fetched", result.Fetched, "failed", result.Failed, "remaining", result.Remaining)
		return err
	}
	address := net.JoinHostPort(cfg.ListenHost, cfg.Port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	service, err := app.New(cfg, db, client)
	if err != nil {
		return err
	}
	if err = service.Start(ctx); err != nil {
		return err
	}
	defer service.Close()
	server := &http.Server{Addr: address, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	slog.Info("workstation ready", "listen", address, "local_url", fmt.Sprintf("http://127.0.0.1:%s", cfg.Port))
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
