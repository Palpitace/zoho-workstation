// input: 同源 HTTP 查询/操作、后台状态和 SQLite 工单/分析记录。
// output: 工单详情、按实际会话/参考版本计算的分析状态及受保护操作。
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
	Status            string `json:"status"`
	Stale             bool   `json:"stale"`
	AutomaticEligible bool   `json:"automatic_eligible"`
	Priority          string `json:"priority,omitempty"`
	Emotion           string `json:"emotion,omitempty"`
	Error             string `json:"error,omitempty"`
}
type ticketView struct {
	store.Record
	AI map[string]AIStatus `json:"ai"`
}
type counts struct {
	Pending int `json:"pending"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
	Success int `json:"success"`
	Stale   int `json:"stale"`
}

func (a *App) eligibleID(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, candidate := range a.eligible {
		if candidate == id {
			return true
		}
	}
	return false
}

func (a *App) recordViews(ctx context.Context, record store.Record, budget store.Budget) (map[string]AIStatus, map[string]store.AnalysisView, error) {
	summaries := map[string]AIStatus{}
	views := map[string]store.AnalysisView{}
	for _, kind := range []string{"jev", "bub"} {
		model, schema, _ := a.model(kind)
		req := store.AnalysisRequest{TicketID: record.ID, Kind: kind, SchemaVersion: schema, RequestedModel: model.Name, Round: DevelopmentRound}
		if !record.Pending {
			prepared, _, err := requestForRecord(ctx, a.db, a.cfg, record, kind, DevelopmentRound)
			if err != nil && prepared.InputHash == "" {
				return nil, nil, err
			}
			req = prepared
		}
		view, err := a.db.Analysis(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		views[kind] = view
		s := AIStatus{Status: "pending", Stale: view.Stale, AutomaticEligible: a.eligibleID(record.ID)}
		if view.Current != nil {
			s.Status = view.Current.Status
			s.Error = view.Current.Error
			if s.Status == "invalid" {
				s.Status = "failed"
			}
		}
		if !model.Enabled {
			s.Status = "disabled"
		} else if s.Status == "pending" && (budget.Total >= 60 || budget.Round >= 20) {
			s.Status = "budget_exhausted"
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
			}
		}
		summaries[kind] = s
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
	router.GET("/api/status", a.status)
	router.GET("/api/tickets", func(c *gin.Context) {
		budget, err := a.db.AnalysisBudget(c.Request.Context(), DevelopmentRound)
		if err != nil {
			apiError(c, 500, "cannot read budget")
			return
		}
		records, err := a.db.List(c.Request.Context(), c.Query("q"), c.Query("status"))
		if err != nil {
			apiError(c, 500, "cannot read tickets")
			return
		}
		output := make([]ticketView, 0, len(records))
		for _, record := range records {
			if !record.Pending {
				record, err = a.db.Get(c.Request.Context(), record.ID)
				if err != nil {
					apiError(c, 500, "cannot read ticket")
					return
				}
			}
			ai, _, e := a.recordViews(c.Request.Context(), record, budget)
			if e != nil {
				apiError(c, 500, "cannot read analysis state")
				return
			}
			record.Messages = nil
			record.Description = ""
			output = append(output, ticketView{record, ai})
		}
		c.JSON(200, gin.H{"data": output})
	})
	router.GET("/api/tickets/:id", func(c *gin.Context) {
		record, err := a.db.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			apiError(c, 404, "ticket not available")
			return
		}
		budget, err := a.db.AnalysisBudget(c.Request.Context(), DevelopmentRound)
		if err != nil {
			apiError(c, 500, "cannot read budget")
			return
		}
		ai, _, err := a.recordViews(c.Request.Context(), record, budget)
		if err != nil {
			apiError(c, 500, "cannot read analysis state")
			return
		}
		c.JSON(200, ticketView{record, ai})
	})
	router.GET("/api/tickets/:id/analysis", func(c *gin.Context) {
		record, err := a.db.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			apiError(c, 404, "ticket not available")
			return
		}
		_, views, err := a.recordViews(c.Request.Context(), record, store.Budget{})
		if err != nil {
			apiError(c, 500, "cannot read analysis")
			return
		}
		c.JSON(200, views)
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
		apiError(c, 500, "cannot read budget")
		return
	}
	records, err := a.db.List(ctx, "", "")
	if err != nil {
		apiError(c, 500, "cannot read tickets")
		return
	}
	totals := map[string]*counts{"jev": {}, "bub": {}}
	for _, record := range records {
		if !record.Pending {
			record, err = a.db.Get(ctx, record.ID)
			if err != nil {
				apiError(c, 500, "cannot read ticket")
				return
			}
		}
		summaries, _, err := a.recordViews(ctx, record, budget)
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
			case "pending", "budget_exhausted":
				n.Pending++
			}
			if s.Stale {
				n.Stale++
			}
		}
	}
	a.mu.Lock()
	runtime := gin.H{"sync_running": a.busy["sync"], "jev_running": a.busy["jev"], "bub_running": a.busy["bub"], "automatic": a.started && !a.closed, "ai_scope": "development", "ai_scope_count": len(a.eligible), "sync_interval_seconds": int(a.cfg.SyncInterval / time.Second), "last_error": map[string]string{"sync": a.lastError["sync"], "jev": a.lastError["jev"], "bub": a.lastError["bub"], "analysis": a.lastError["analysis"]}}
	a.mu.Unlock()
	c.JSON(200, gin.H{"sync": syncState, "jev_enabled": a.cfg.Jev.Enabled, "bub_enabled": a.cfg.Bub.Enabled, "wecom_enabled": false, "csrf_token": a.csrf, "runtime": runtime, "ai": totals, "budget": gin.H{"total": budget.Total, "round": budget.Round, "total_limit": 60, "round_limit": 20}})
}

func apiError(c *gin.Context, status int, message string) { c.JSON(status, gin.H{"error": message}) }
func operationResponse(c *gin.Context, err error) {
	switch {
	case err == nil:
		c.JSON(202, gin.H{"status": "accepted"})
	case errors.Is(err, store.ErrBusy):
		apiError(c, 409, "operation already running")
	case errors.Is(err, store.ErrAnalysisBudget):
		apiError(c, 429, "development model request budget exhausted")
	case errors.Is(err, errPythonUnavailable):
		apiError(c, 503, "Bub Python environment is unavailable")
	case errors.Is(err, sql.ErrNoRows):
		apiError(c, 404, "ticket not available")
	default:
		apiError(c, 400, err.Error())
	}
}
