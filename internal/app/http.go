// input: 同源 HTTP 查询/操作、后台状态和 SQLite 工单/分析记录。
// output: 本机查询、受保护的知识/恢复操作、共享 Bub 运行状态及独立分析结果。
// pos: Gin 管理界面 API 与静态资源边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
	"zoho-workstation/internal/store"
)

type AIStatus struct {
	Status             string `json:"status"`
	Stale              bool   `json:"stale"`
	AutomaticEligible  bool   `json:"automatic_eligible"`
	Priority           string `json:"priority,omitempty"`
	Emotion            string `json:"emotion,omitempty"`
	Category           string `json:"category,omitempty"`
	Error              string `json:"error,omitempty"`
	NextRetryAt        string `json:"next_retry_at,omitempty"`
	LastFailedAt       string `json:"last_failed_at,omitempty"`
	FailureBeforeStart bool   `json:"failure_before_start"`
	WaitingReason      string `json:"waiting_reason,omitempty"`
}
type ticketView struct {
	store.Record
	MessageSegments   map[string][]MessageSegment `json:"message_segments,omitempty"`
	AI                map[string]AIStatus         `json:"ai"`
	TicketNumber      string                      `json:"ticket_number,omitempty"`
	WebURL            string                      `json:"web_url,omitempty"`
	FirstIncomingTime string                      `json:"first_incoming_time,omitempty"`
	SyncStatus        string                      `json:"sync_status"`
}

// Read display-only identity from retained raw data, including existing snapshots.
// These fields never enter model inputs or invalidate successful AI results.
func displayTicket(record store.Record, ai map[string]AIStatus) ticketView {
	view := ticketView{Record: record, AI: ai, FirstIncomingTime: firstIncoming(record), SyncStatus: syncStatus(record)}
	var identity struct {
		Number json.Number `json:"ticketNumber"`
		URL    string      `json:"webUrl"`
	}
	if json.Unmarshal(record.Raw.Ticket, &identity) == nil {
		view.TicketNumber = identity.Number.String()
		if u, err := url.Parse(identity.URL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
			view.WebURL = identity.URL
		}
	}
	return view
}

type analysisView struct {
	store.AnalysisView
	FailureBeforeStart bool   `json:"failure_before_start"`
	WaitingFor         string `json:"waiting_for,omitempty"`
	WaitingReason      string `json:"waiting_reason,omitempty"`
}

type counts struct {
	Pending      int `json:"pending"`
	Running      int `json:"running"`
	Failed       int `json:"failed"`
	Success      int `json:"success"`
	Stale        int `json:"stale"`
	RetryWaiting int `json:"retry_waiting"`
}

func (a *App) recordViews(ctx context.Context, record store.Record) (map[string]AIStatus, map[string]analysisView, error) {
	a.mu.Lock()
	startedAt := a.startedAt
	a.mu.Unlock()
	summaries := map[string]AIStatus{}
	views := map[string]analysisView{}
	for _, kind := range []string{"jev", "bub"} {
		model, schema, _ := a.model(kind)
		waiting := false
		req := store.AnalysisRequest{TicketID: record.ID, Kind: kind, SchemaVersion: schema, RequestedModel: model.Name, Round: DevelopmentRound}
		if !record.Pending {
			prepared, _, err := requestForRecord(ctx, a.db, a.cfg, record, kind, DevelopmentRound)
			waiting = errors.Is(err, errAwaitingBub)
			if err != nil && !waiting && prepared.InputHash == "" {
				return nil, nil, err
			}
			req = prepared
		}
		view, err := a.db.Analysis(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		failedAt, _ := time.Parse(time.RFC3339Nano, view.LastFailedAt)
		started, _ := time.Parse(time.RFC3339Nano, startedAt)
		historical := !failedAt.IsZero() && !started.IsZero() && failedAt.Before(started)
		views[kind] = analysisView{AnalysisView: view, FailureBeforeStart: historical}
		if waiting {
			views[kind] = analysisView{AnalysisView: view, WaitingFor: "bub"}
		}
		s := AIStatus{Status: "pending", Stale: view.Stale, AutomaticEligible: a.automaticallyEligible(record), NextRetryAt: view.NextRetryAt, LastFailedAt: view.LastFailedAt, FailureBeforeStart: historical}
		if view.Current != nil {
			s.Status = view.Current.Status
			s.Error = view.Current.Error
			if s.Status == "invalid" {
				s.Status = "failed"
			}
		}
		if waiting {
			s.Status = "waiting"
		}
		if !model.Enabled {
			s.Status = "disabled"
		}
		if kind == "jev" && view.Success != nil && !view.Stale {
			var result struct {
				Answers map[string]struct {
					Choice string `json:"choice"`
				} `json:"answers"`
			}
			if json.Unmarshal(view.Success.Result, &result) == nil {
				s.Priority = result.Answers["priority"].Choice
				s.Emotion = result.Answers["emotion"].Choice
				s.Category = result.Answers["category"].Choice
			}
		}
		summaries[kind] = s
	}
	if summaries["jev"].Status == "waiting" {
		reason := "等待当前会话的 Bub 总结完成"
		if summaries["bub"].NextRetryAt != "" {
			reason = "Bub 总结失败，正在等待自动重试"
		} else if summaries["bub"].Status == "failed" {
			reason = "Bub 总结失败，等待总结恢复"
		} else if summaries["bub"].Status == "disabled" {
			reason = "Bub 已关闭，尚无当前总结"
		}
		s := summaries["jev"]
		s.WaitingReason = reason
		summaries["jev"] = s
		v := views["jev"]
		v.WaitingReason = reason
		views["jev"] = v
	}
	return summaries, views, nil
}

func (a *App) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.SetTrustedProxies(nil)
	router.Use(func(c *gin.Context) {
		host, port, err := net.SplitHostPort(c.Request.Host)
		if err != nil || port != a.cfg.Port || (host != "localhost" && host != "127.0.0.1" && host != "::1") {
			c.AbortWithStatusJSON(403, gin.H{"error": "local host required"})
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		if len(c.Request.URL.Path) >= 5 && c.Request.URL.Path[:5] == "/api/" {
			c.Header("Cache-Control", "no-store")
		}
		if c.Request.Method == http.MethodPost {
			supplied := c.GetHeader("X-CSRF-Token")
			if subtle.ConstantTimeCompare([]byte(supplied), []byte(a.csrf)) != 1 || !a.allowedOrigin(c.GetHeader("Origin")) {
				c.AbortWithStatusJSON(403, gin.H{"error": "invalid session token or origin"})
				return
			}
		}
		c.Next()
	})
	a.knowledgeRoutes(router)
	router.GET("/api/status", a.status)
	router.GET("/api/tickets", a.listTickets)

	router.GET("/api/tickets/:id", func(c *gin.Context) {
		record, err := a.db.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			apiError(c, 404, "ticket not available")
			return
		}
		ai, _, err := a.recordViews(c.Request.Context(), record)
		if err != nil {
			apiError(c, 500, "cannot read analysis state")
			return
		}
		view := displayTicket(record, ai)
		view.MessageSegments = displayMessageSegments(record)
		c.JSON(200, view)
	})
	router.GET("/api/tickets/:id/analysis", func(c *gin.Context) {
		record, err := a.db.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			apiError(c, 404, "ticket not available")
			return
		}
		_, views, err := a.recordViews(c.Request.Context(), record)
		if err != nil {
			apiError(c, 500, "cannot read analysis")
			return
		}
		c.JSON(200, views)
	})
	router.POST("/api/analysis/:kind/retry", func(c *gin.Context) {
		var body struct {
			Candidates []RetryCandidate `json:"candidates"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
		if c.ShouldBindJSON(&body) != nil {
			apiError(c, 400, "invalid recovery request")
			return
		}
		operationResponse(c, a.RequestRecovery(c.Param("kind"), body.Candidates))
	})
	router.POST("/api/sync", func(c *gin.Context) { operationResponse(c, a.RequestSync()) })
	router.POST("/api/tickets/:id/analysis/:kind", func(c *gin.Context) { operationResponse(c, a.RequestAnalysis(c.Param("id"), c.Param("kind"))) })
	router.Static("/assets", "./web/dist/assets")
	router.GET("/", func(c *gin.Context) {
		if _, err := os.Stat("web/dist/index.html"); err != nil {
			apiError(c, 503, "frontend build not available")
			return
		}
		c.File("web/dist/index.html")
	})
	return router
}

func (a *App) allowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return false
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return false
	}
	return u.Port() == a.cfg.Port || (a.cfg.Environment == "development" && u.Port() == "5173")
}

func (a *App) status(c *gin.Context) {
	ctx := c.Request.Context()
	syncState, err := a.db.State(ctx)
	if err != nil {
		apiError(c, 500, "cannot read sync status")
		return
	}
	budget, err := a.db.AnalysisBudget(ctx, DevelopmentRound)
	if err != nil {
		apiError(c, 500, "cannot read request counts")
		return
	}
	records, err := a.db.List(ctx, "", "")
	if err != nil {
		apiError(c, 500, "cannot read tickets")
		return
	}
	totals := map[string]*counts{"jev": {}, "bub": {}}
	eligibleCount, stoppedCount := 0, 0
	candidates := map[string][]RetryCandidate{"bub": {}, "jev": {}}
	for _, record := range records {
		if record.SyncStopped {
			stoppedCount++
		}
		if a.automaticallyEligible(record) {
			eligibleCount++
		}
		if !record.Pending {
			record, err = a.db.Get(ctx, record.ID)
			if err != nil {
				apiError(c, 500, "cannot read ticket")
				return
			}
		}
		summaries, views, err := a.recordViews(ctx, record)
		if err != nil {
			apiError(c, 500, "cannot read analysis status")
			return
		}
		for kind, s := range summaries {
			n := totals[kind]
			switch s.Status {
			case "success":
				n.Success++
			case "failed":
				n.Failed++
			case "running":
				n.Running++
			case "pending", "waiting":
				n.Pending++
			}
			if s.Status == "failed" {
				if s.NextRetryAt != "" {
					n.RetryWaiting++
				}
				if s.AutomaticEligible && views[kind].Current != nil && views[kind].Current.Status == "failed" {
					candidates[kind] = append(candidates[kind], RetryCandidate{TicketID: record.ID, RunID: views[kind].Current.ID})
				}
			}
			if s.Stale {
				n.Stale++
			}
		}
	}
	a.mu.Lock()
	runtime := gin.H{"sync_start_time": a.cfg.SyncStartTime.Format(time.RFC3339), "next_sync_at": a.nextSync.UTC().Format(time.RFC3339), "last_sync_batch": a.lastSyncResult, "stopped_count": stoppedCount, "started_at": a.startedAt, "recovery_pending": map[string]int{"bub": len(a.recovery["bub"]), "jev": len(a.recovery["jev"])}, "sync_running": a.busy["sync"], "jev_running": a.busy["jev"], "bub_running": a.busy["bub"] || a.busy["case"] || a.busy["advice"], "case_running": a.busy["case"], "advice_running": a.busy["advice"], "automatic": a.started && !a.closed, "ai_scope": "all_synced", "ai_scope_count": eligibleCount, "sync_interval_seconds": int(a.cfg.SyncInterval / time.Second), "sync_batch_size": a.cfg.SyncBatchSize, "sync_concurrency": a.cfg.SyncConcurrency, "last_error": map[string]string{"sync": a.lastError["sync"], "jev": a.lastError["jev"], "bub": a.lastError["bub"], "analysis": a.lastError["analysis"], "case": a.lastError["case"], "advice": a.lastError["advice"]}}
	a.mu.Unlock()
	c.JSON(200, gin.H{"retry_candidates": candidates, "sync": syncState, "jev_enabled": a.cfg.Jev.Enabled, "bub_enabled": a.cfg.Bub.Enabled, "wecom_enabled": false, "csrf_token": a.csrf, "runtime": runtime, "ai": totals, "budget": gin.H{"total": budget.Total, "round": budget.Round}})
}

func apiError(c *gin.Context, status int, message string) { c.JSON(status, gin.H{"error": message}) }
func operationResponse(c *gin.Context, err error) {
	switch {
	case err == nil:
		c.JSON(202, gin.H{"status": "accepted"})
	case errors.Is(err, errStaleRecovery):
		apiError(c, 409, err.Error())
	case errors.Is(err, errAwaitingBub), errors.Is(err, errNoKnowledge):
		apiError(c, 409, err.Error())
	case errors.Is(err, store.ErrBusy):
		apiError(c, 409, "operation already running")
	case errors.Is(err, errPythonUnavailable):
		apiError(c, 503, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		apiError(c, 404, "ticket not available")
	default:
		apiError(c, 400, err.Error())
	}
}
