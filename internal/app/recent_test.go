// input: 合成完整工单与滚动范围配置。
// output: Issue #10 范围恢复/缓存待确认；启动前范围隔离、自动分析资格与手动输入拒绝验证。
// pos: 最近工单分析边界回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
)

func TestRecentScopeExcludesOldAnalysis(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Reserve(ctx, []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2"} {
		if err = db.Save(ctx, ticket.RawSnapshot{}, ticket.Snapshot{ID: id, DepartmentID: "7", Complete: true}); err != nil {
			t.Fatal(err)
		}
	}
	service, err := New(config.Config{Port: "8080", SyncInterval: time.Hour, RecentLimit: 50}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.List(ctx, "", "")
	if err != nil || len(rows) != 2 || !rows[0].OutsideScope || service.automaticallyEligible(rows[0]) {
		t.Fatal("startup cache must be visible but inactive")
	}
	for _, path := range []string{"/api/tickets", "/api/status"} {
		request := httptest.NewRequest("GET", path, nil)
		request.Host = "localhost:8080"
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, request)
		if w.Code != 200 {
			t.Fatalf("cache API %s returned %d", path, w.Code)
		}
		var body struct {
			Data []json.RawMessage
			Sync store.State
		}
		if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if path == "/api/tickets" && len(body.Data) != 2 {
			t.Fatal("HTTP list hid cached tickets")
		}
		if path == "/api/status" && (body.Sync.Ready != 2 || body.Sync.RecentConfirmed) {
			t.Fatal("HTTP status misrepresented unconfirmed cache")
		}
	}
	if err = db.ReplaceRecent(ctx, []string{"2"}); err != nil {
		t.Fatal(err)
	}
	old, err := db.Get(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	current, err := db.Get(ctx, "2")
	if err != nil {
		t.Fatal(err)
	}
	if service.automaticallyEligible(old) || !service.automaticallyEligible(current) {
		t.Fatal("analysis eligibility did not follow rolling scope")
	}
	for _, kind := range []string{"bub", "jev", "case", "advice"} {
		req, input, err := analysisRequest(ctx, db, service.cfg, "1", kind, "test")
		if err == nil || req.InputHash != "" || len(input) != 0 {
			t.Fatalf("%s can dispatch excluded input", kind)
		}
	}
}
