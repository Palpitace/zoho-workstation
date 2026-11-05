// input: 合成旧格式快照、Zoho 工单号/链接、Jev 分类结果和 HTTP 筛选请求。
// output: 字段/日期/五列表头组合筛选、稳定计数、首份来信边界和历史显示证据。
// pos: 工单列表 API 回归测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/app"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
)

func TestTicketListIdentityFiltersAndClassification(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Reserve(ctx, []string{"1", "2", "3"}); err != nil {
		t.Fatal(err)
	}
	for i, fixture := range []struct{ id, product, status, raw string }{
		{"1", "P1", "Open", `{"id":"1","ticketNumber":"101","webUrl":"https://desk.zoho.com/support/example/ShowHomePage.do#Cases/dv/abc"}`},
		{"2", "P2", "Closed", `{"id":"2","ticketNumber":102,"webUrl":"javascript:alert(1)"}`},
		{"3", "", "Open", `{"id":"3"}`},
	} {
		raw := ticket.RawSnapshot{Ticket: json.RawMessage(fixture.raw)}
		s := ticket.Snapshot{ID: fixture.id, DepartmentID: "7", Subject: "Synthetic", Product: fixture.product, Status: fixture.status, Complete: true}
		if err = db.Save(ctx, raw, s); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			hash := seedSummary(t, db, s, raw, "synthetic")
			run, _, e := db.StartAnalysis(ctx, store.AnalysisRequest{TicketID: "1", Kind: "jev", InputHash: hash, SchemaVersion: analysis.JevSchema, RequestedModel: "synthetic", Round: app.DevelopmentRound}, false)
			if e != nil {
				t.Fatal(e)
			}
			if e = db.FinishAnalysis(ctx, run.ID, "synthetic", json.RawMessage(`{"answers":{"category":{"choice":"hardware"},"emotion":{"choice":"neutral"},"priority":{"choice":"high"}}}`), nil); e != nil {
				t.Fatal(e)
			}
			run, _, e = db.StartAnalysis(ctx, store.AnalysisRequest{TicketID: "1", Kind: "bub", InputHash: "old", SchemaVersion: "old", RequestedModel: "synthetic", Round: app.DevelopmentRound}, false)
			if e != nil {
				t.Fatal(e)
			}
			if e = db.FinishAnalysis(ctx, run.ID, "synthetic", json.RawMessage(`{"summary":{}}`), nil); e != nil {
				t.Fatal(e)
			}
		}
	}
	service, err := app.New(config.Config{Port: "8080", SyncInterval: time.Hour, Jev: config.Model{Enabled: true, Name: "synthetic"}, Bub: config.Model{Enabled: true, Name: "synthetic"}}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		count int
	}{
		{"", 3}, {"?product=P1", 1}, {"?product=P1&status=Closed", 0}, {"?status=Open", 2}, {"?product=__missing__", 1}, {"?q=absent", 0},
		{"?q=%23101", 1}, {"?q=p1", 1}, {"?q=10&field=ticket_number", 0},
		{"?q=1&field=id", 1}, {"?q=p&field=product", 2}, {"?q=SYNTH&field=subject", 3},
		{"?emotion=neutral", 1}, {"?emotion=unknown", 2}, {"?priority=high&status=Open&product=P1", 1},
		{"?priority=high&emotion=angry", 0}, {"?sync_status=synced", 3}, {"?sync_status=failed", 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/tickets"+tc.query, nil)
			req.Host = "localhost:8080"
			w := httptest.NewRecorder()
			service.Handler().ServeHTTP(w, req)
			var got struct {
				Data []struct {
					ID     string `json:"id"`
					Number string `json:"ticket_number"`
					URL    string `json:"web_url"`
					AI     map[string]struct {
						Category string `json:"category"`
						Stale    bool   `json:"stale"`
						Eligible bool   `json:"automatic_eligible"`
					} `json:"ai"`
				} `json:"data"`
				Filters map[string][]struct {
					Value string `json:"value"`
					Count int    `json:"count"`
				} `json:"filters"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
				t.Fatalf("list HTTP %d", w.Code)
			}
			if len(got.Data) != tc.count {
				t.Fatalf("filter returned %d, want %d", len(got.Data), tc.count)
			}
			if len(got.Filters["status"]) != 2 || len(got.Filters["product"]) != 3 {
				t.Fatal("filters must include choices outside current result")
			}
			for _, key := range []string{"status", "product", "emotion", "priority", "sync_status"} {
				count := 0
				for _, choice := range got.Filters[key] {
					count += choice.Count
				}
				if count != 3 {
					t.Fatalf("%s facet counts changed with search: %d", key, count)
				}
			}
			for _, r := range got.Data {
				switch r.ID {
				case "1":
					if r.Number != "101" || r.URL != "https://desk.zoho.com/support/example/ShowHomePage.do#Cases/dv/abc" || r.AI["jev"].Category != "hardware" || r.AI["jev"].Stale || r.AI["bub"].Stale || !r.AI["jev"].Eligible {
						t.Fatalf("identity/classification/version lost: %+v", r)
					}
				case "2":
					if r.Number != "102" || r.URL != "" {
						t.Fatal("numeric number or unsafe URL mishandled")
					}
				case "3":
					if r.Number != "" || r.URL != "" {
						t.Fatal("internal ID must not masquerade as ticket number")
					}
				}
			}
		})
	}
}

func TestTicketDateSearch(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Reserve(ctx, []string{"1", "2", "3"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2", "3"} {
		messages := []ticket.Message{
			{ID: "private", Source: "comment", Visibility: "private", Direction: "in", CreatedTime: "2026-11-01T00:00:00Z"},
			{ID: "agent", Source: "thread", Role: "agent", Visibility: "public", Direction: "in", CreatedTime: "2026-11-01T00:00:00Z"},
		}
		if id != "3" {
			at := "2026-11-04T16:00:00Z"
			if id == "2" {
				at = "2026-11-05T16:00:00Z"
			}
			messages = append(messages, ticket.Message{ID: "customer", Source: "thread", Visibility: "public", Direction: "in", Role: "unknown", CreatedTime: at})
		}
		snapshot := ticket.Snapshot{ID: id, DepartmentID: "7", Subject: "Printer", Product: "P1", Status: "Open", Complete: true,
			CreatedTime: "2026-11-01T00:00:00Z", ModifiedTime: "2026-11-06T00:00:00Z", Messages: messages}
		if err := db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{}`)}, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	service, err := app.New(config.Config{Port: "8080", SyncInterval: time.Hour}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		field, from, until string
		count, code        int
	}{
		{"first_incoming", "2026-11-05T00:00:00+08:00", "2026-11-06T00:00:00+08:00", 1, 200},
		{"first_incoming", "2026-11-05T16:00:00Z", "", 1, 200},
		{"created", "2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", 3, 200},
		{"updated", "2026-11-06T00:00:00Z", "", 3, 200},
		{"first_incoming", "invalid", "", 0, 400},
		{"first_incoming", "2026-11-06T00:00:00Z", "2026-11-05T00:00:00Z", 0, 400},
		{"invalid", "", "", 0, 400},
	} {
		query := url.Values{"date_field": {tc.field}, "from": {tc.from}, "until": {tc.until}, "q": {"printer"}, "status": {"Open"}, "product": {"P1"}}
		req := httptest.NewRequest("GET", "/api/tickets?"+query.Encode(), nil)
		req.Host = "localhost:8080"
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("%+v: HTTP %d", tc, w.Code)
		}
		if tc.code != 200 {
			continue
		}
		var got struct {
			Data []struct {
				ID    string `json:"id"`
				First string `json:"first_incoming_time"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Data) != tc.count {
			t.Fatalf("%+v: count=%d", tc, len(got.Data))
		}
		for _, r := range got.Data {
			if (r.ID == "3") != (r.First == "") {
				t.Fatalf("first incoming must not use creation/agent/private times: %+v", r)
			}
		}
	}
}

func TestTicketSyncFiltersAndHistory(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids := []string{"pending", "failed", "incomplete", "synced", "merged", "deleted", "stopped"}
	if err := db.Reserve(ctx, ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id != "pending" {
			if err := db.Save(ctx, ticket.RawSnapshot{Ticket: json.RawMessage(`{}`)}, ticket.Snapshot{ID: id, DepartmentID: "7", Complete: id != "incomplete"}); err != nil {
				t.Fatal(err)
			}
		}
		switch id {
		case "failed":
			err = db.Failure(ctx, id, "synthetic failure")
		case "merged":
			err = db.StopSync(ctx, id, "ticket confirmed merged in Zoho")
		case "deleted":
			err = db.StopSync(ctx, id, "ticket confirmed deleted in Zoho")
		case "stopped":
			err = db.StopSync(ctx, id, "ticket no longer in configured department")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	service, err := app.New(config.Config{Port: "8080", SyncInterval: time.Hour}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		req := httptest.NewRequest("GET", "/api/tickets?sync_status="+id+"&show_history=true", nil)
		req.Host = "localhost:8080"
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, req)
		var got struct {
			Data []struct{ ID, SyncStatus string } `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Data) != 1 || got.Data[0].ID != id {
			t.Fatalf("%s sync filter failed: %s", id, w.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/api/tickets?show_history=false", nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()
	service.Handler().ServeHTTP(w, req)
	var got struct {
		Data    []json.RawMessage `json:"data"`
		Filters map[string][]struct {
			Count int `json:"count"`
		} `json:"filters"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Data) != 4 {
		t.Fatal("history visibility was not applied")
	}
	count := 0
	for _, c := range got.Filters["sync_status"] {
		count += c.Count
	}
	if count != 4 {
		t.Fatal("facet counts include hidden history")
	}
}
