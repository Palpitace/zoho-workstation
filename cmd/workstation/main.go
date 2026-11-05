// input: 根 .env、命令行采样数量及本地 SQLite 数据。
// output: 受控开发命令、显式 IP 监听的静态页面与自动同步/分析服务。
// pos: Go 服务启动与生命周期编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zoho-workstation/internal/app"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/syncer"
	"zoho-workstation/internal/zoho"
)

func main() {
	if err := run(); err != nil {
		slog.Error("operation failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	if command != "sample" && command != "serve" && command != "prepare" && command != "analyze" {
		return errors.New("usage: workstation sample | serve | prepare | analyze")
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
		flags.StringVar(&round, "round", app.DevelopmentRound, "persistent development round, at most 20 model requests")
		flags.BoolVar(&force, "force", false, "rerun within model request budget")
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
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	db, err := store.Open(cfg.DatabasePath, cfg.Zoho.DepartmentID)
	if err != nil {
		return err
	}
	defer db.Close()
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
		result, err := syncer.Run(ctx, client, db, syncer.Options{SampleSize: *size, MaxTickets: *maxTickets, Lookback: cfg.SyncLookback})
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
