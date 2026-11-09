// input: Zoho OAuth 配置、限定部门与调用方 context。
// output: 单响应最新范围及有界异常重试； 有界创建时间发现与分批增量； 完整只读会话、固定 ID 复核、失效元数据复核及安全限流诊断。
// pos: Zoho HTTP 与本地采样的边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package zoho

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zoho-workstation/internal/config"
	"zoho-workstation/internal/ticket"
)

var ErrOutsideDepartment = errors.New("ticket confirmed outside configured department")
var ErrDeleted = errors.New("ticket confirmed deleted in Zoho")

var idPattern = regexp.MustCompile(`^[0-9]+$`)

type Client struct {
	cfg          config.Zoho
	http         *http.Client
	mu           sync.Mutex
	token        string
	expires      time.Time
	searchDenied atomic.Bool
}

type responseError struct {
	status     int
	retryAfter time.Duration
	code       string
	method     string
}

func (e *responseError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("remote HTTP %d (%s)", e.status, e.code)
	}
	return fmt.Sprintf("remote HTTP %d", e.status)
}

// RetryAfter exposes only the safe wait duration of an HTTP 429 response.
func RetryAfter(err error) time.Duration {
	var response *responseError
	if errors.As(err, &response) && response.status == http.StatusTooManyRequests {
		return response.retryAfter
	}
	return 0
}

func New(cfg config.Zoho, transport *http.Client) *Client {
	if transport == nil {
		transport = &http.Client{Timeout: 40 * time.Second}
	}
	copy := *transport
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
	return &Client{cfg: cfg, http: &copy}
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {c.cfg.ClientID}, "client_secret": {c.cfg.ClientSecret}, "refresh_token": {c.cfg.RefreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.AccountsURL, "/")+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("invalid OAuth endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("OAuth refresh: %w", err)
	}
	var result struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if json.Unmarshal(body, &result) != nil || result.Token == "" {
		return "", errors.New("OAuth refresh did not return access token")
	}
	if result.Expires <= 60 {
		result.Expires = 3600
	}
	c.token, c.expires = result.Token, time.Now().Add(time.Duration(result.Expires-60)*time.Second)
	return c.token, nil
}

func (c *Client) do(req *http.Request) (json.RawMessage, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, errors.New("network request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return json.RawMessage(`{"data":[]}`), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		diagnostic := &responseError{status: resp.StatusCode, method: req.Method}
		if resp.StatusCode == http.StatusTooManyRequests {
			diagnostic.retryAfter = 30 * time.Second
			value := resp.Header.Get("Retry-After")
			if seconds, e := strconv.ParseUint(value, 10, 64); e == nil && seconds > 0 {
				diagnostic.retryAfter = time.Duration(min(seconds, uint64((1<<63-1)/time.Second))) * time.Second
			} else if errors.Is(e, strconv.ErrRange) {
				diagnostic.retryAfter = time.Duration(1<<63 - 1)
			} else if at, e := http.ParseTime(value); e == nil && time.Until(at) > 0 {
				diagnostic.retryAfter = time.Until(at)
			}
		}
		var failure struct {
			Code string `json:"errorCode"`
		}
		// Only documented, fixed diagnostic codes may leave the HTTP boundary.
		// Never return the provider's message, which may echo request data.
		if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&failure) == nil {
			switch failure.Code {
			case "OAUTH_SCOPE_MISMATCH", "SCOPE_MISMATCH", "FORBIDDEN", "UNAUTHORIZED", "INVALID_OAUTH", "INSUFFICIENT_PRIVILEGES":
				diagnostic.code = failure.Code
			}
		}
		return nil, diagnostic
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil || len(body) > 32<<20 {
		return nil, errors.New("response unreadable or exceeds 32 MiB")
	}
	if !json.Valid(body) {
		return nil, errors.New("remote response is not JSON")
	}
	return body, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.BaseURL, "/")+"/api/v1"+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, errors.New("invalid Desk endpoint")
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	req.Header.Set("orgId", c.cfg.OrgID)
	return c.do(req)
}

// Recent uses one bounded response, so a changing list cannot shift a page boundary.
func (c *Client) Recent(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 50 {
		return nil, errors.New("sample limit must be between 1 and 50")
	}
	for attempt := 1; attempt <= 3; attempt++ {
		body, err := c.get(ctx, "/tickets", url.Values{"departmentId": {c.cfg.DepartmentID}, "sortBy": {"-createdTime"}, "from": {"0"}, "limit": {strconv.Itoa(limit)}})
		if err != nil {
			return nil, err
		}
		rows, err := list(body)
		if err != nil {
			return nil, err
		}
		if len(rows) > limit {
			return nil, errors.New("ticket API ignored sampling limit")
		}
		ids := []string{}
		seen := map[string]bool{}
		duplicate := false
		for _, row := range rows {
			id, department := field(row, "id"), field(row, "departmentId")
			if !idPattern.MatchString(id) || department != c.cfg.DepartmentID {
				return nil, errors.New("ticket identity or department mismatch")
			}
			if seen[id] {
				duplicate = true
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if !duplicate {
			slog.Info("recent ticket selection completed", "stage", "selection", "attempt", attempt, "selected", len(ids), "recovered", attempt > 1)
			return ids, nil
		}
		slog.Warn("recent ticket selection duplicate", "stage", "selection", "attempt", attempt, "will_retry", attempt < 3)
	}
	return nil, errors.New("recent selection unstable after 3 attempts; retry pending")
}

// Changed prefers documented search; a search 403 switches only this client's
// fixed sample to individual ticket metadata checks, never a history scan.
func (c *Client) Changed(ctx context.Context, ids []string, since, until time.Time) ([]string, error) {
	return c.ChangedKnown(ctx, ids, since, until, nil)
}

// ChangedKnown avoids reloading complete conversations whose verified metadata time is unchanged.
func (c *Client) ChangedKnown(ctx context.Context, ids []string, since, until time.Time, known map[string]string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	if len(ids) > 50 {
		var changed []string
		for offset := 0; offset < len(ids); offset += 50 {
			part, err := c.ChangedKnown(ctx, ids[offset:min(offset+50, len(ids))], since, until, known)
			if err != nil {
				return nil, err
			}
			changed = append(changed, part...)
		}
		return changed, nil
	}
	if until.Before(since) {
		return nil, errors.New("invalid incremental scope")
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			return nil, errors.New("invalid sample ID")
		}
		allowed[id] = true
	}
	if c.searchDenied.Load() {
		return c.changedMetadata(ctx, ids, since, until, known)
	}
	changed := []string{}
	seen := map[string]bool{}
	for offset := 0; offset <= 999; offset += 25 {
		body, err := c.get(ctx, "/tickets/search", url.Values{
			"departmentId": {c.cfg.DepartmentID}, "id": {strings.Join(ids, ",")},
			"modifiedTimeRange": {since.UTC().Format(time.RFC3339) + "," + until.UTC().Format(time.RFC3339)},
			"sortBy":            {"-modifiedTime"}, "from": {strconv.Itoa(offset)}, "limit": {"25"},
		})
		if err != nil {
			var status *responseError
			if errors.As(err, &status) && status.status == http.StatusForbidden && status.method == http.MethodGet {
				if c.searchDenied.CompareAndSwap(false, true) {
					slog.WarnContext(ctx, "Zoho search access denied; using fixed-ID ticket metadata checks", "http_status", http.StatusForbidden, "sample_size", len(allowed))
				}
				return c.changedMetadata(ctx, ids, since, until, known)
			}
			return nil, err
		}
		rows, err := list(body)
		if err != nil {
			return nil, err
		}
		added := 0
		for _, row := range rows {
			id := field(row, "id")
			if !allowed[id] || field(row, "departmentId") != c.cfg.DepartmentID {
				return nil, errors.New("incremental response exceeded fixed sample")
			}
			if !seen[id] {
				if c.checkTicket(row, id) != nil || !sameModifiedTime(field(row, "modifiedTime"), known[id]) {
					changed = append(changed, id)
				}
				seen[id] = true
				added++
			}
		}
		if len(rows) < 25 {
			missing := []string{}
			for _, id := range ids {
				if !seen[id] {
					missing = append(missing, id)
				}
			}
			rechecked, err := c.changedMetadata(ctx, missing, since, until, known)
			if err != nil {
				return nil, err
			}
			return append(changed, rechecked...), nil
		}
		if added == 0 {
			return nil, errors.New("incremental pagination made no progress")
		}
	}
	return nil, errors.New("incremental pagination exceeded documented bound")
}

func (c *Client) changedMetadata(ctx context.Context, ids []string, since, until time.Time, known map[string]string) ([]string, error) {
	changed := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		body, err := c.get(ctx, "/tickets/"+id, nil)
		if err != nil {
			var response *responseError
			local := errors.As(err, &response) && response.method == http.MethodGet &&
				(response.status == http.StatusNotFound || response.status == http.StatusGone ||
					(response.status == http.StatusForbidden && (response.code == "" || response.code == "FORBIDDEN" || response.code == "INSUFFICIENT_PRIVILEGES")))
			if !local {
				return nil, err
			}
			// Retry this original fixed ID through Fetch, which validates scope
			// and lets syncer persist its failure without blocking other IDs.
			changed = append(changed, id)
			continue
		}
		if c.checkTicket(body, id) != nil {
			changed = append(changed, id)
			continue
		}
		modified, err := time.Parse(time.RFC3339Nano, field(body, "modifiedTime"))
		if err != nil {
			changed = append(changed, id)
			continue
		}
		if sameModifiedTime(field(body, "modifiedTime"), known[id]) {
			continue
		}
		if !modified.Before(since) && !modified.After(until) {
			changed = append(changed, id)
		}
	}
	return changed, nil
}

// fetchSnapshot retains exact business responses and merges truncated thread content separately.
func (c *Client) fetchSnapshot(ctx context.Context, id string) (ticket.RawSnapshot, error) {
	raw := ticket.RawSnapshot{Complete: true, Evidence: map[string]json.RawMessage{}}
	if !idPattern.MatchString(id) {
		return raw, errors.New("invalid ticket ID")
	}
	path := "/tickets/" + id
	fetch := func(p string, q url.Values) (json.RawMessage, error) {
		b, e := c.get(ctx, p, q)
		if e == nil {
			raw.Evidence[p+"?"+q.Encode()] = b
		}
		return b, e
	}
	var err error
	raw.Ticket, err = fetch(path, url.Values{"include": {"contacts,products"}})
	if err != nil {
		return raw, err
	}
	if err := c.checkTicket(raw.Ticket, id); err != nil {
		return ticket.RawSnapshot{}, err
	}
	threads, err := pages(ctx, fetch, path+"/threads", "sendDateTime")
	if err != nil {
		return raw, fmt.Errorf("thread list: %w", err)
	}
	for _, row := range threads {
		threadID := field(row, "id")
		if !idPattern.MatchString(threadID) {
			return raw, errors.New("invalid thread ID")
		}
		detail, err := fetch(path+"/threads/"+threadID, nil)
		if err != nil {
			return raw, fmt.Errorf("thread detail: %w", err)
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(detail, &obj) != nil {
			return raw, errors.New("invalid thread detail")
		}
		if string(obj["isContentTruncated"]) == "true" {
			full, e := fetch(path+"/threads/"+threadID+"/fullContent", nil)
			if e != nil {
				return raw, fmt.Errorf("thread full content: %w", e)
			}
			var content map[string]json.RawMessage
			if json.Unmarshal(full, &content) != nil || content["content"] == nil || string(content["isContentTruncated"]) == "true" {
				return raw, errors.New("full thread content missing")
			}
			obj["content"] = content["content"]
			obj["isContentTruncated"] = json.RawMessage("false")
			if content["contentType"] != nil {
				obj["contentType"] = content["contentType"]
			}
			detail, _ = json.Marshal(obj)
		}
		raw.Threads = append(raw.Threads, detail)
	}
	raw.Comments, err = pages(ctx, fetch, path+"/comments", "commentedTime")
	if err != nil {
		return raw, fmt.Errorf("comment list: %w", err)
	}
	for _, expected := range []struct {
		field string
		count int
	}{{"threadCount", len(raw.Threads)}, {"commentCount", len(raw.Comments)}} {
		// Zoho counters can be lower than complete paginated results. Extra fetched
		// evidence is not missing data; fewer rows still requires a retry.
		if n, e := strconv.Atoi(field(raw.Ticket, expected.field)); e == nil && n > expected.count {
			raw.Complete = false
			raw.Warnings = append(raw.Warnings, expected.field+"_mismatch")
		}
	}
	after, err := c.get(ctx, path, url.Values{"include": {"contacts,products"}})
	if err != nil {
		return raw, fmt.Errorf("ticket consistency check: %w", err)
	}
	raw.Evidence[path+"#after"] = after
	if err := c.checkTicket(after, id); err != nil {
		return ticket.RawSnapshot{}, err
	}
	if field(after, "modifiedTime") != field(raw.Ticket, "modifiedTime") {
		raw.Complete = false
		raw.Warnings = append(raw.Warnings, "ticket_changed_during_read")
	}
	return raw, nil
}

// A missing department or mismatched ticket identity is not proof of a move.
func (c *Client) checkTicket(body json.RawMessage, id string) error {
	if field(body, "id") != id {
		return errors.New("ticket identity mismatch")
	}
	department := field(body, "departmentId")
	if !idPattern.MatchString(department) {
		return errors.New("ticket department unavailable")
	}
	if department != c.cfg.DepartmentID {
		return ErrOutsideDepartment
	}
	var flags struct {
		Deleted bool `json:"isDeleted"`
	}
	if json.Unmarshal(body, &flags) == nil && flags.Deleted {
		return ErrDeleted
	}
	return nil
}

type readPage func(string, url.Values) (json.RawMessage, error)

func pages(ctx context.Context, fetch readPage, path, sortBy string) ([]json.RawMessage, error) {
	all := []json.RawMessage{}
	seen := map[string]bool{}
	for offset := 0; ; offset += 50 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, err := fetch(path, url.Values{"from": {strconv.Itoa(offset)}, "limit": {"50"}, "sortBy": {sortBy}})
		if err != nil {
			return nil, err
		}
		rows, err := list(body)
		if err != nil {
			return nil, err
		}
		added := 0
		for _, row := range rows {
			id := field(row, "id")
			if id == "" {
				return nil, errors.New("message ID missing")
			}
			if !seen[id] {
				seen[id] = true
				all = append(all, row)
				added++
			}
		}
		if len(rows) < 50 {
			return all, nil
		}
		if added == 0 {
			return nil, errors.New("pagination made no progress")
		}
	}
}

func list(body json.RawMessage) ([]json.RawMessage, error) {
	var value struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &value) != nil || value.Data == nil {
		return nil, errors.New("invalid list response")
	}
	return value.Data, nil
}

func field(body json.RawMessage, key string) string {
	var value map[string]json.RawMessage
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(value[key], &s) == nil {
		return s
	}
	d := json.NewDecoder(bytes.NewReader(value[key]))
	d.UseNumber()
	var n json.Number
	if d.Decode(&n) == nil {
		return n.String()
	}
	return ""
}

// Discover reads one bounded newest-first page, stopping at the configured creation cutoff.
// Repeated full passes catch offset shifts caused by arrivals, deletions and merges.
func (c *Client) Discover(ctx context.Context, since time.Time, offset int) ([]string, int, bool, error) {
	if offset < 0 || since.IsZero() {
		return nil, offset, false, errors.New("invalid discovery boundary")
	}
	body, err := c.get(ctx, "/tickets", url.Values{"departmentId": {c.cfg.DepartmentID}, "sortBy": {"-createdTime"}, "from": {strconv.Itoa(offset)}, "limit": {"25"}})
	if err != nil {
		return nil, offset, false, err
	}
	rows, err := list(body)
	if err != nil {
		return nil, offset, false, err
	}
	if len(rows) > 25 {
		return nil, offset, false, errors.New("discovery page exceeded requested limit")
	}
	ids := []string{}
	done := len(rows) < 25
	var previous time.Time
	for _, row := range rows {
		id := field(row, "id")
		created, err := time.Parse(time.RFC3339Nano, field(row, "createdTime"))
		if err != nil || !idPattern.MatchString(id) || field(row, "departmentId") != c.cfg.DepartmentID {
			return nil, offset, false, errors.New("discovery identity, department or creation time invalid")
		}
		if !previous.IsZero() && created.After(previous) {
			return nil, offset, false, errors.New("discovery creation ordering invalid")
		}
		previous = created
		if created.Before(since) {
			done = true
			continue
		}
		ids = append(ids, id)
	}
	return ids, offset + len(rows), done, nil
}

func sameModifiedTime(remote, saved string) bool {
	a, err := time.Parse(time.RFC3339Nano, remote)
	if err != nil {
		return false
	}
	b, err := time.Parse(time.RFC3339Nano, saved)
	return err == nil && a.Equal(b)
}
