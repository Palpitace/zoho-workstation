// input: 合成旧工单详情、404/403 和只读生命周期历史响应。
// output: 有证据的合并/删除停止与未知/恢复/限流仍重试的回归证据。
// pos: Zoho 工单失效判定测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package zoho_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/zoho"
)

func TestLifecycleRequiresEvidence(t *testing.T) {
	for _, tc := range []struct {
		name            string
		detail, history int
		events, want    string
	}{
		{"merged missing", 404, 200, `{"eventName":"TicketMergedChild"}`, "ticket confirmed merged in Zoho"},
		{"merged deleted", 200, 200, `{"eventName":"TicketMergedChild"}`, "ticket confirmed merged in Zoho"},
		{"deleted missing", 404, 200, `{"eventName":"TicketDeleted"}`, "ticket confirmed deleted in Zoho"},
		{"not master", 404, 200, `{"eventName":"TicketMergedMaster"}`, "remote HTTP 404"},
		{"restored", 404, 200, `{"eventName":"TicketRestored"},{"eventName":"TicketMergedChild"}`, "remote HTTP 404"},
		{"missing alone", 404, 200, ``, "remote HTTP 404"},
		{"history unavailable", 404, 403, ``, "remote HTTP 404"},
		{"deleted history unavailable", 200, 403, ``, "ticket confirmed deleted in Zoho"},
		{"forbidden", 403, 200, `{"eventName":"TicketMergedChild"}`, "remote HTTP 403"},
		{"history rate limit", 404, 429, ``, "remote HTTP 429"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			historyCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/v2/token":
					fmt.Fprint(w, `{"access_token":"synthetic"}`)
				case "/api/v1/tickets/1":
					w.WriteHeader(tc.detail)
					if tc.detail == 200 {
						fmt.Fprint(w, `{"id":"1","departmentId":"7","isDeleted":true,"modifiedTime":"2020-01-01T00:00:00Z"}`)
					}
				case "/api/v1/tickets/1/History":
					historyCalls++
					if r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("from") != "0" {
						t.Error("history must be bounded")
					}
					w.Header().Set("Retry-After", "31")
					w.WriteHeader(tc.history)
					fmt.Fprintf(w, `{"data":[%s]}`, tc.events)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
			_, err := client.Fetch(context.Background(), "1")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
			if tc.detail == 403 && historyCalls != 0 {
				t.Fatal("permission failure triggered history lookup")
			}
			if tc.detail != 403 && historyCalls != 1 {
				t.Fatal("missing bounded history check")
			}
			if tc.history == 429 && zoho.RetryAfter(err) != 31*time.Second {
				t.Fatal("lost rate limit")
			}
		})
	}
}

func TestDeletedMetadataIgnoresOldModifiedTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/v2/token":
			fmt.Fprint(w, `{"access_token":"synthetic"}`)
		case "/api/v1/tickets/search":
			fmt.Fprint(w, `{"data":[]}`)
		case "/api/v1/tickets/1":
			fmt.Fprint(w, `{"id":"1","departmentId":"7","isDeleted":true,"modifiedTime":"2020-01-01T00:00:00Z"}`)
		default:
			t.Error("outside fixed sample")
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
	ids, err := client.Changed(context.Background(), []string{"1"}, time.Now().Add(-time.Hour), time.Now())
	if err != nil || strings.Join(ids, ",") != "1" {
		t.Fatalf("deleted ID missed: %v %v", ids, err)
	}
}
