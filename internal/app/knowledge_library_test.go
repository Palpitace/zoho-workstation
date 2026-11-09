// input: 合成 ZIP、Markdown、本地 PNG、临时数据库及受保护请求。
// output: 批量分类、风险确认、图片重试、去重、导出和部门过滤的回归证据。
// pos: 知识库 HTTP 小型集成验证。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/store"
)

func TestBatchMarkdownImportReviewAndIsolation(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	db, e := store.Open(filepath.Join("data", "test.db"), "labelife")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a, e := New(config.Config{Port: "8080", SyncInterval: time.Hour, Zoho: config.Zoho{DepartmentID: "labelife"}}, db, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	handler := a.Handler()
	libs, _ := db.ListLibraries(ctx)
	lib := libs[0]
	request := func(method, url string, raw []byte, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+url, bytes.NewReader(raw))
		r.Header.Set("Origin", "http://localhost:8080")
		r.Header.Set("X-CSRF-Token", a.csrf)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	files := map[string][]byte{"faq/故障排查/blank.md": []byte("# Blank labels\nCheck paper.\n![Diagram](../images/one.png)"), "faq/images/one.png": png, "faq/会员咨询/member.md": []byte("# Membership\nRefund policy requires approval.")}
	for name, raw := range files {
		f, _ := z.Create(name)
		f.Write(raw)
	}
	z.Close()
	upload := func() *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		form.WriteField("library_id", fmt.Sprint(lib.ID))
		f, _ := form.CreateFormFile("files", "faq.zip")
		f.Write(archive.Bytes())
		form.Close()
		return request("POST", "/api/knowledge/import-batch", body.Bytes(), form.FormDataContentType())
	}
	w := upload()
	if w.Code != 201 {
		t.Fatalf("import %d %s", w.Code, w.Body.String())
	}
	var batch importBatch
	if json.Unmarshal(w.Body.Bytes(), &batch) != nil || len(batch.Items) != 2 {
		t.Fatal("preview missing items")
	}
	var technical, policy importItem
	for _, item := range batch.Items {
		if item.Scope == "policy" {
			policy = item
		} else {
			technical = item
		}
	}
	if !policy.RequiresConfirmation || technical.RequiresConfirmation || hasMissingImages(technical.Markdown) {
		t.Fatalf("risk/image preview %+v", batch.Items)
	}
	post := func(url string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		return request("POST", url, raw, "application/json")
	}
	w = post("/api/knowledge/import-batch/"+batch.ID+"/retry", map[string]any{"item_ids": []string{technical.ID}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var retried importBatch
	json.Unmarshal(w.Body.Bytes(), &retried)
	for _, item := range retried.Items {
		if item.ID == technical.ID && hasMissingImages(item.Markdown) {
			t.Fatal("retry lost local image")
		}
	}
	w = post("/api/knowledge/import-batch/"+batch.ID+"/confirm", map[string]any{"item_ids": []string{technical.ID, policy.ID}, "publish": true})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	entries, _ := db.ListKnowledge(ctx, "", "")
	if len(entries) != 1 || entries[0].Scope != "reference" || entries[0].Status != "published" {
		t.Fatal("policy bypassed confirmation")
	}
	entry := entries[0]
	w = upload()
	var again importBatch
	json.Unmarshal(w.Body.Bytes(), &again)
	found := false
	for _, item := range again.Items {
		if item.Scope == "reference" {
			found = item.Status == "duplicate"
		}
	}
	if !found {
		t.Fatal("repeat import not deduplicated")
	}
	w = request("GET", fmt.Sprintf("/api/knowledge/%d/export?format=zip", entry.ID), nil, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	exported, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if e != nil || len(exported.File) != 3 {
		t.Fatal("export did not include local image,md,source")
	}
	foreign, _ := db.SaveLibrary(ctx, knowledge.Library{Name: "Private", DepartmentIDs: []string{"other"}})
	hidden, e := db.SaveKnowledge(ctx, knowledge.Entry{LibraryID: foreign.ID, Kind: "sop", Title: "Secret printer", SourceHash: "private", Markdown: "secret printer procedure"})
	if e != nil {
		t.Fatal(e)
	}
	db.PublishKnowledge(ctx, hidden.ID)
	w = post("/api/knowledge/retrieve", map[string]any{"library_id": foreign.ID, "department_id": "labelife", "query": "secret printer"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret printer procedure") {
		t.Fatal("cross-department retrieval")
	}
	w = post("/api/knowledge/retrieve", map[string]any{"library_id": lib.ID, "department_id": "labelife", "query": "paper"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"keyword"`) {
		t.Fatal("embedding failure did not fall back")
	}
	w = request(http.MethodGet, "/api/knowledge/assets/../../secret", nil, "")
	if w.Code == 200 {
		t.Fatal("unsafe asset path")
	}
}

// TestPreparedFAQImport is opt-in and writes only ignored private acceptance data.
func TestPreparedFAQImport(t *testing.T) {
	if os.Getenv("VERIFY_PRIVATE_FAQ_IMPORT") != "1" {
		t.Skip("explicit uploaded FAQ acceptance only")
	}
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	t.Chdir(root)
	raw, e := os.ReadFile("data/knowledge-prepared/faq-drafts.zip")
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(".cache", "faq-acceptance", "app.db"), "labelife")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a, e := New(config.Config{Port: "8080", SyncInterval: time.Hour, Zoho: config.Zoho{DepartmentID: "labelife"}}, db, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	libs, _ := db.ListLibraries(context.Background())
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("library_id", fmt.Sprint(libs[0].ID))
	f, _ := form.CreateFormFile("files", "faq-drafts.zip")
	f.Write(raw)
	form.Close()
	req := httptest.NewRequest("POST", "http://localhost:8080/api/knowledge/import-batch", &body)
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("X-CSRF-Token", a.csrf)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("private import failed: status %d", w.Code)
	}
	var batch importBatch
	if e = json.Unmarshal(w.Body.Bytes(), &batch); e != nil {
		t.Fatal(e)
	}
	ready, failed, missing := 0, 0, 0
	for _, item := range batch.Items {
		if item.Status == "ready" {
			ready++
		}
		if item.Status == "failed" {
			failed++
		}
		if hasMissingImages(item.Markdown) {
			missing++
		}
	}
	if len(batch.Items) != 80 {
		t.Fatalf("expected80 units got%d", len(batch.Items))
	}
	if e = os.WriteFile("data/knowledge-prepared/import-preview.json", w.Body.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("preview items=%d ready=%d failed=%d missing-image-items=%d; no entries published", len(batch.Items), ready, failed, missing)
	if failed > 0 {
		t.Error("some prepared semantic units failed import")
	}
}

func TestImageReferencesAndLocalNetworkBoundary(t *testing.T) {
	markdown := "![步骤][diagram]\n\n[diagram]: https://example.com/step.png"
	converted := inlineReferenceImages(markdown)
	if unsupportedImages(converted) || !strings.Contains(converted, "![步骤](https://example.com/step.png)") {
		t.Fatal("reference image not normalized")
	}
	for _, body := range []string{`<img src="https://example.com/hidden.png">`, `![missing][unknown]`, `![shorthand]`} {
		if !hasMissingImages(body) {
			t.Fatal("unsupported missing image allowed publication")
		}
	}
	if _, e := fetchImage(context.Background(), "https://127.0.0.1/private"); e == nil {
		t.Fatal("private image fetch allowed")
	}
}
