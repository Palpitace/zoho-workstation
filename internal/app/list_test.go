// input: 合成旧格式快照、Zoho 工单号/链接、Jev 分类结果和 HTTP 筛选请求。
// output: 列表真实工单身份、独立分析状态及组合下拉筛选的公开接口证据。
// pos: 工单列表 API 回归测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
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
				Filters struct{ Statuses, Products []string } `json:"filters"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
				t.Fatalf("list HTTP %d", w.Code)
			}
			if len(got.Data) != tc.count {
				t.Fatalf("filter returned %d, want %d", len(got.Data), tc.count)
			}
			if len(got.Filters.Statuses) != 2 || len(got.Filters.Products) != 3 {
				t.Fatal("filters must include choices outside current result")
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
