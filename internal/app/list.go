// input: 列表查询条件、完整本地快照及当前 AI 状态。
// output: 字段/日期/表头组合筛选、首份公开来信时间及稳定选项计数。
// pos: 只读工单列表查询边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"github.com/gin-gonic/gin"
	"net/url"
	"sort"
	"strings"
	"time"
	"zoho-workstation/internal/store"
)

type filterChoice struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

func firstIncoming(record store.Record) string {
	if !record.Complete {
		return ""
	}
	var first time.Time
	for _, message := range record.Messages {
		if message.Source != "thread" || message.Visibility != "public" || message.Direction != "in" || message.Role == "agent" {
			continue
		}
		switch strings.ToUpper(message.Status) {
		case "DRAFT", "FAILED", "PENDING":
			continue
		}
		at, err := time.Parse(time.RFC3339, message.CreatedTime)
		if err == nil && (first.IsZero() || at.Before(first)) {
			first = at
		}
	}
	if first.IsZero() {
		return ""
	}
	return first.UTC().Format(time.RFC3339Nano)
}

func syncStatus(record store.Record) string {
	if record.SyncStopped {
		switch record.SyncError {
		case "ticket confirmed merged in Zoho":
			return "merged"
		case "ticket confirmed deleted in Zoho":
			return "deleted"
		default:
			return "stopped"
		}
	}
	if record.SyncError != "" {
		return "failed"
	}
	if record.Pending {
		return "pending"
	}
	if !record.Complete {
		return "incomplete"
	}
	return "synced"
}

func matchesText(view ticketView, field, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	id := strings.TrimPrefix(query, "#")
	switch field {
	case "subject":
		return strings.Contains(strings.ToLower(view.Subject), query)
	case "ticket_number":
		return view.TicketNumber != "" && view.TicketNumber == id
	case "id":
		return view.ID == id
	case "product":
		return strings.Contains(strings.ToLower(view.Product), query)
	default:
		return matchesText(view, "subject", query) || matchesText(view, "ticket_number", query) || matchesText(view, "id", query) || matchesText(view, "product", query)
	}
}

func facetValues(view ticketView) map[string]string {
	product, emotion, priority := view.Product, view.AI["jev"].Emotion, view.AI["jev"].Priority
	if product == "" {
		product = "__missing__"
	}
	if emotion == "" {
		emotion = "unknown"
	}
	if priority == "" {
		priority = "unknown"
	}
	status := view.Status
	if status == "" {
		status = "__missing__"
	}
	return map[string]string{"status": status, "product": product, "emotion": emotion, "priority": priority, "sync_status": view.SyncStatus}
}

func (a *App) listTickets(c *gin.Context) {
	query := c.Request.URL.Query()
	field, dateField := query.Get("field"), query.Get("date_field")
	switch field {
	case "", "all", "subject", "ticket_number", "id", "product":
	default:
		apiError(c, 400, "invalid search field")
		return
	}
	switch dateField {
	case "", "first_incoming", "created", "updated":
	default:
		apiError(c, 400, "invalid date field")
		return
	}
	bounds := map[string]time.Time{}
	for _, key := range []string{"from", "until"} {
		if value := query.Get(key); value != "" {
			at, err := time.Parse(time.RFC3339, value)
			if err != nil {
				apiError(c, 400, "invalid date range")
				return
			}
			bounds[key] = at
		}
	}
	from, until := bounds["from"], bounds["until"]
	if !from.IsZero() && !until.IsZero() && !from.Before(until) {
		apiError(c, 400, "invalid date range")
		return
	}
	records, err := a.db.List(c.Request.Context(), "", "")
	if err != nil {
		apiError(c, 500, "cannot read tickets")
		return
	}
	counts := map[string]map[string]int{}
	for _, key := range []string{"status", "product", "emotion", "priority", "sync_status"} {
		counts[key] = map[string]int{}
	}
	output := []ticketView{}
	for _, record := range records {
		if query.Get("show_history") == "false" && record.SyncStopped {
			continue
		}
		if !record.Pending {
			record, err = a.db.Get(c.Request.Context(), record.ID)
			if err != nil {
				apiError(c, 500, "cannot read ticket")
				return
			}
		}
		ai, _, err := a.recordViews(c.Request.Context(), record)
		if err != nil {
			apiError(c, 500, "cannot read analysis state")
			return
		}
		view := displayTicket(record, ai)
		values := facetValues(view)
		for key, value := range values {
			counts[key][value]++
		}
		if !matchesText(view, field, query.Get("q")) || !matchesFacets(values, query) {
			continue
		}
		if !from.IsZero() || !until.IsZero() {
			value := view.FirstIncomingTime
			if dateField == "created" {
				value = view.CreatedTime
			}
			if dateField == "updated" {
				value = view.ModifiedTime
			}
			at, err := time.Parse(time.RFC3339, value)
			if err != nil || (!from.IsZero() && at.Before(from)) || (!until.IsZero() && !at.Before(until)) {
				continue
			}
		}
		view.Messages = nil
		view.Description = ""
		output = append(output, view)
	}
	filters := map[string][]filterChoice{}
	for key, counts := range counts {
		choices := []filterChoice{}
		for value, count := range counts {
			choices = append(choices, filterChoice{value, count})
		}
		sort.Slice(choices, func(i, j int) bool { return choices[i].Value < choices[j].Value })
		filters[key] = choices
	}
	c.JSON(200, gin.H{"data": output, "filters": filters})
}

func matchesFacets(values map[string]string, query url.Values) bool {
	for key, value := range values {
		if expected := query.Get(key); expected != "" && expected != value {
			return false
		}
	}
	return true
}
