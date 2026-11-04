// input: 合成 Zoho HTTP 响应及隔离 httptest 服务。
// output: 分页、限定 ID 局部/全局失败边界、完整线程及错误脱敏的行为证据。
// pos: 外部 API 边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package zoho_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zoho-workstation/internal/config"
	"zoho-workstation/internal/zoho"
)

func TestRecentPagesAndDepartmentBoundary(t *testing.T) {
	wrongDepartment := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/v2/token" {
			fmt.Fprint(w, `{"access_token":"synthetic","expires_in":3600}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/api/v1/tickets" || r.URL.Query().Get("departmentId") != "7" {
			t.Errorf("unexpected request")
			w.WriteHeader(400)
			return
		}
		from, _ := strconv.Atoi(r.URL.Query().Get("from"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows := []map[string]string{}
		for i := from; i < from+limit && i < 26; i++ {
			department := "7"
			if wrongDepartment {
				department = "8"
			}
			rows = append(rows, map[string]string{"id": strconv.Itoa(i + 1), "departmentId": department})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": rows})
	}))
	defer server.Close()
	client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
	ids, err := client.Recent(context.Background(), 26)
	if err != nil || len(ids) != 26 || ids[25] != "26" {
		t.Fatalf("pagination: count=%d error=%v", len(ids), err)
	}
	wrongDepartment = true
	if _, err := client.Recent(context.Background(), 1); err == nil {
		t.Fatal("cross-department response accepted")
	}
}

func TestChangedStaysInsideFrozenSample(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := from.Add(time.Hour)
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = strconv.Itoa(i + 1)
	}
	cases := []struct {
		name, mode, wantError string
		ids, want             []string
		searches, details     int32
		repeat                bool
	}{
		{name: "search success", mode: "search", want: []string{"2"}, searches: 1},
		{name: "search cannot expand IDs", mode: "foreign_id", wantError: "exceeded fixed sample", searches: 1},
		{name: "search cannot cross department", mode: "foreign_department", wantError: "exceeded fixed sample", searches: 1},
		{name: "other search errors do not fall back", mode: "server_error", wantError: "remote HTTP 500", searches: 1},
		{name: "OAuth refusal is not a search refusal", mode: "oauth_denied", wantError: "OAuth refresh"},
		{name: "403 falls back and caches only fixed metadata", mode: "fallback", want: []string{"2"}, searches: 1, details: 4, repeat: true},
		{name: "metadata bounds are inclusive", mode: "boundaries", want: []string{"1", "2"}, searches: 1, details: 2},
		{name: "outside window is unchanged", mode: "outside_window", want: []string{}, searches: 1, details: 2},
		{name: "moved metadata requires scope-checked Fetch", mode: "metadata_department", want: []string{"2"}, searches: 1, details: 2},
		{name: "wrong metadata ID retries only original fixed ID", mode: "metadata_id", want: []string{"2"}, searches: 1, details: 2},
		{name: "invalid metadata time requires complete Fetch", mode: "metadata_time", want: []string{"2"}, searches: 1, details: 2},
		{name: "local metadata absence requires complete Fetch", mode: "metadata_not_found", want: []string{"2"}, searches: 1, details: 2},
		{name: "local metadata refusal requires complete Fetch", mode: "metadata_forbidden", want: []string{"2"}, searches: 1, details: 2},
		{name: "global metadata scope refusal remains sanitized", mode: "metadata_denied", wantError: "OAUTH_SCOPE_MISMATCH", searches: 1, details: 2},
		{name: "metadata server failure remains global", mode: "metadata_server", wantError: "remote HTTP 500", searches: 1, details: 2},
		{name: "over fifty IDs rejected before reads", ids: tooMany, wantError: "invalid incremental scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var searches, details, oauth atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/v2/token" {
					oauth.Add(1)
					if tc.mode == "oauth_denied" {
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `{"errorCode":"FORBIDDEN","message":"SECRET INPUT"}`)
						return
					}
					fmt.Fprint(w, `{"access_token":"synthetic"}`)
					return
				}
				if r.Method != http.MethodGet {
					t.Error("unexpected business write")
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				switch r.URL.Path {
				case "/api/v1/tickets/search":
					searches.Add(1)
					if r.URL.Query().Get("id") != "1,2" || r.URL.Query().Get("departmentId") != "7" || r.URL.Query().Get("modifiedTimeRange") == "" {
						t.Error("incremental scope missing")
					}
					if tc.mode == "server_error" {
						w.WriteHeader(http.StatusInternalServerError)
						fmt.Fprint(w, `{"errorCode":"SECRET INPUT","message":"SECRET INPUT"}`)
						return
					}
					if tc.mode == "search" || strings.HasPrefix(tc.mode, "foreign_") {
						id, department := "2", "7"
						if tc.mode == "foreign_id" {
							id = "3"
						}
						if tc.mode == "foreign_department" {
							department = "8"
						}
						json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": id, "departmentId": department}}})
						return
					}
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"errorCode":"FORBIDDEN","message":"SECRET INPUT"}`)
				case "/api/v1/tickets/1", "/api/v1/tickets/2":
					details.Add(1)
					if r.URL.RawQuery != "" {
						t.Error("metadata read added undocumented fields or includes")
					}
					id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
					department := "7"
					modified := until.Format(time.RFC3339Nano)
					if id == "1" {
						modified = from.Add(-time.Second).Format(time.RFC3339Nano)
					}
					if tc.mode == "boundaries" && id == "1" {
						modified = from.Format(time.RFC3339Nano)
					}
					if id == "2" {
						switch tc.mode {
						case "outside_window":
							modified = until.Add(time.Second).Format(time.RFC3339Nano)
						case "metadata_department":
							department = "8"
						case "metadata_id":
							id = "3"
						case "metadata_time":
							modified = "invalid"
						case "metadata_not_found":
							w.WriteHeader(http.StatusNotFound)
							return
						case "metadata_forbidden":
							w.WriteHeader(http.StatusForbidden)
							fmt.Fprint(w, `{"errorCode":"FORBIDDEN","message":"SECRET INPUT"}`)
							return
						case "metadata_server":
							w.WriteHeader(http.StatusInternalServerError)
							return
						case "metadata_denied":
							w.WriteHeader(http.StatusForbidden)
							fmt.Fprint(w, `{"errorCode":"OAUTH_SCOPE_MISMATCH","message":"SECRET INPUT"}`)
							return
						}
					}
					json.NewEncoder(w).Encode(map[string]string{"id": id, "departmentId": department, "modifiedTime": modified})
				default:
					t.Errorf("unexpected collection or conversation read: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
			sample := tc.ids
			if sample == nil {
				sample = []string{"1", "2"}
			}
			attempts := 1
			if tc.repeat {
				attempts = 2
			}
			for attempt := 0; attempt < attempts; attempt++ {
				ids, err := client.Changed(context.Background(), sample, from, until)
				if tc.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "SECRET INPUT") {
						t.Fatalf("unsafe or missing diagnostic: %v", err)
					}
				} else if err != nil || strings.Join(ids, ",") != strings.Join(tc.want, ",") {
					t.Fatalf("changed IDs = %v, want %v: %v", ids, tc.want, err)
				}
			}
			if searches.Load() != tc.searches || details.Load() != tc.details {
				t.Fatalf("reads exceeded expected scope: search=%d metadata=%d", searches.Load(), details.Load())
			}
			if tc.mode == "oauth_denied" && oauth.Load() != 1 {
				t.Fatal("OAuth denial incorrectly entered metadata fallback")
			}
		})
	}
}

func TestFetchReadsFullThreadAndPrivateComments(t *testing.T) {
	stillTruncated := false
	threadPages, commentPages := 0, 0
	page := func(w http.ResponseWriter, r *http.Request, comments bool) {
		from, _ := strconv.Atoi(r.URL.Query().Get("from"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items := []map[string]any{}
		for i := from; i < from+limit && i < 51; i++ {
			item := map[string]any{"id": strconv.Itoa(i + 2), "summary": "not complete"}
			if comments {
				item = map[string]any{"id": strconv.Itoa(i + 100), "content": "Check warranty", "isPublic": false}
			}
			items = append(items, item)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": items})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/v2/token":
			fmt.Fprint(w, `{"access_token":"synthetic","expires_in":3600}`)
		case "/api/v1/tickets/1":
			fmt.Fprint(w, `{"id":"1","departmentId":"7","threadCount":"51","commentCount":"51","modifiedTime":"2026-01-02T00:00:00Z"}`)
		case "/api/v1/tickets/1/threads":
			threadPages++
			page(w, r, false)
		case "/api/v1/tickets/1/threads/2":
			fmt.Fprint(w, `{"id":"2","content":"truncated","isContentTruncated":true}`)
		case "/api/v1/tickets/1/threads/2/fullContent":
			if stillTruncated {
				fmt.Fprint(w, `{"content":"still partial","isContentTruncated":true}`)
				return
			}
			fmt.Fprint(w, `{"content":"Customer has NOT upgraded firmware.","isContentTruncated":false}`)
		case "/api/v1/tickets/1/comments":
			commentPages++
			page(w, r, true)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/v1/tickets/1/threads/") {
				id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/1/threads/")
				json.NewEncoder(w).Encode(map[string]any{"id": id, "content": "complete reply"})
				return
			}
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := zoho.New(config.Zoho{BaseURL: server.URL, AccountsURL: server.URL, DepartmentID: "7"}, server.Client())
	raw, err := client.Fetch(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if !raw.Complete || len(raw.Threads) != 51 || len(raw.Comments) != 51 || threadPages != 2 || commentPages != 2 {
		t.Fatalf("incomplete snapshot: complete=%v", raw.Complete)
	}
	var thread map[string]any
	json.Unmarshal(raw.Threads[0], &thread)
	if thread["id"] != "2" || thread["content"] != "Customer has NOT upgraded firmware." {
		t.Fatal("full content was not merged with thread metadata")
	}
	stillTruncated = true
	if _, err = client.Fetch(context.Background(), "1"); err == nil {
		t.Fatal("still-truncated content accepted as complete")
	}
}
