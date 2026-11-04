// input: 根 .env、命令行采样数量及本地 SQLite 数据。
// output: 受控同步、脱敏及分析命令，监听本机的 Gin 工单与分析查询服务。
// pos: Go 服务启动与生命周期编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
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
	round, force, kind := developmentRound, false, "jev"
	if command == "analyze" {
		flags.StringVar(&round, "round", developmentRound, "persistent development round, at most 20 model requests")
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
		return prepareInputs(ctx, db, cfg, *maxTickets)
	}
	if command == "analyze" {
		return analyzeTickets(ctx, db, cfg, *maxTickets, kind, round, force)
	}
	client := zoho.New(cfg.Zoho, nil)
	if command == "sample" {
		result, err := syncer.Run(ctx, client, db, syncer.Options{SampleSize: *size, MaxTickets: *maxTickets, Lookback: cfg.SyncLookback})
		slog.Info("sample run finished", "fetched", result.Fetched, "failed", result.Failed, "remaining", result.Remaining)
		return err
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.SetTrustedProxies(nil)
	router.GET("/api/status", func(c *gin.Context) {
		state, e := db.State(c.Request.Context())
		if e != nil {
			c.JSON(500, gin.H{"error": "cannot read sync status"})
			return
		}
		c.JSON(200, gin.H{"sync": state, "jev_enabled": cfg.Jev.Enabled, "bub_enabled": cfg.Bub.Enabled, "wecom_enabled": false})
	})
	router.GET("/api/tickets", func(c *gin.Context) {
		records, e := db.List(c.Request.Context(), c.Query("q"), c.Query("status"))
		if e != nil {
			c.JSON(500, gin.H{"error": "cannot read tickets"})
			return
		}
		c.JSON(200, gin.H{"data": records})
	})
	router.GET("/api/tickets/:id", func(c *gin.Context) {
		record, e := db.Get(c.Request.Context(), c.Param("id"))
		if e != nil {
			c.JSON(404, gin.H{"error": "ticket not available"})
			return
		}
		c.JSON(200, record)
	})
	router.GET("/api/tickets/:id/analysis", func(c *gin.Context) {
		views := map[string]store.AnalysisView{}
		for _, kind := range []string{"jev", "bub"} {
			request, _, e := analysisRequest(c.Request.Context(), db, cfg, c.Param("id"), kind, developmentRound)
			if e != nil {
				c.JSON(404, gin.H{"error": "analysis input not available"})
				return
			}
			view, e := db.Analysis(c.Request.Context(), request)
			if e != nil {
				c.JSON(500, gin.H{"error": "cannot read analysis"})
				return
			}
			views[kind] = view
		}
		c.JSON(200, views)
	})
	router.Static("/assets", "./web/dist/assets")
	router.GET("/", func(c *gin.Context) {
		if _, e := os.Stat("web/dist/index.html"); e != nil {
			c.JSON(503, gin.H{"error": "frontend build not available"})
			return
		}
		c.File("web/dist/index.html")
	})
	server := &http.Server{Addr: "127.0.0.1:" + cfg.Port, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	slog.Info("query API ready", "address", fmt.Sprintf("http://127.0.0.1:%s", cfg.Port))
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
