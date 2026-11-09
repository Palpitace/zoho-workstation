// input: 用户明确上传的 ZIP/多文件、直接图像链接及批量确认选择。
// output: 持久预览清单、保留原文件、本地图像和不可变 Markdown 草稿。
// pos: 知识批量导入与资源边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/knowledge"
)

type importItem struct {
	SourceFile           string   `json:"source_file,omitempty"`
	ApplicableProducts   []string `json:"applicable_products"`
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Category             string   `json:"category"`
	Scope                string   `json:"scope"`
	Markdown             string   `json:"markdown"`
	OriginalMarkdown     string   `json:"original_markdown"`
	Product              string   `json:"product"`
	ExcludedProducts     []string `json:"excluded_products"`
	Status               string   `json:"status"`
	Warnings             []string `json:"warnings"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	Error                string   `json:"error,omitempty"`
	SourceName           string   `json:"source_name"`
	Hash                 string   `json:"hash"`
	EntryID              int64    `json:"entry_id,omitempty"`
}
type importMetadata struct {
	Path               string   `json:"path"`
	Title              string   `json:"title"`
	Category           string   `json:"category"`
	Scope              string   `json:"scope"`
	ApplicableProducts []string `json:"applicable_products"`
	ExcludedProducts   []string `json:"excluded_products"`
	ReviewFlags        []string `json:"review_flags"`
	SourceName         string   `json:"source_name"`
	SourceHash         string   `json:"source_hash"`
}
type importBatch struct {
	ID        string       `json:"id"`
	LibraryID int64        `json:"library_id"`
	Items     []importItem `json:"items"`
}

var importID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var assetID = regexp.MustCompile(`^[a-f0-9]{64}\.(png|jpg|gif|webp)$`)
var imageLink = regexp.MustCompile(`!\[[^\]\n]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)`)

func knowledgeDir() string       { return filepath.Join("data", "knowledge") }
func batchFile(id string) string { return filepath.Join(knowledgeDir(), "imports", id+".json") }
func saveImport(batch importBatch) error {
	if err := os.MkdirAll(filepath.Dir(batchFile(batch.ID)), 0700); err != nil {
		return err
	}
	raw, _ := json.Marshal(batch)
	return writePrivateFile(batchFile(batch.ID), raw)
}
func loadImport(id string) (importBatch, error) {
	var b importBatch
	if !importID.MatchString(id) {
		return b, errors.New("invalid import")
	}
	raw, e := os.ReadFile(batchFile(id))
	if e != nil {
		return b, e
	}
	e = json.Unmarshal(raw, &b)
	return b, e
}
func contentHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func (a *App) batchImport(c *gin.Context) {
	a.mu.Lock()
	if a.busy["document"] {
		a.mu.Unlock()
		apiError(c, 409, "已有导入正在处理")
		return
	}
	a.busy["document"] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.busy["document"] = false; a.mu.Unlock() }()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 40*1024*1024)
	if c.Request.ParseMultipartForm(4*1024*1024) != nil {
		apiError(c, 400, "上传失败，批次上限 40 MiB")
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	lib, e := strconv.ParseInt(c.PostForm("library_id"), 10, 64)
	if e != nil || !a.hasLibrary(c.Request.Context(), lib) {
		apiError(c, 400, "请选择知识库")
		return
	}
	token := make([]byte, 16)
	if _, e = rand.Read(token); e != nil {
		apiError(c, 500, "无法创建导入")
		return
	}
	batch := importBatch{ID: hex.EncodeToString(token), LibraryID: lib, Items: []importItem{}}
	files := c.Request.MultipartForm.File["files"]
	files = append(files, c.Request.MultipartForm.File["files[]"]...)
	files = append(files, c.Request.MultipartForm.File["file"]...)
	if len(files) == 0 || len(files) > 100 {
		apiError(c, 400, "请选择 1 至 100 个文件")
		return
	}
	type input struct {
		name string
		raw  []byte
	}
	var inputs []input
	assets := map[string][]byte{}
	paths := map[string]bool{}
	manifest := map[string]importMetadata{}
	var expanded int64
	for _, f := range files {
		stream, err := f.Open()
		if err != nil {
			apiError(c, 400, "无法读取文件")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(stream, 32*1024*1024+1))
		stream.Close()
		if err != nil || len(raw) > 32*1024*1024 {
			apiError(c, 400, "单个归档超过限制")
			return
		}
		name := path.Base(strings.ReplaceAll(f.Filename, "\\", "/"))
		if err = saveOriginal(name, raw); err != nil {
			apiError(c, 500, "原文件保存失败")
			return
		}
		if strings.EqualFold(path.Ext(name), ".zip") {
			z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
			if err != nil || len(z.File) > 2000 {
				apiError(c, 400, "ZIP 格式或数量超出限制")
				return
			}
			for _, member := range z.File {
				if member.FileInfo().IsDir() {
					continue
				}
				n := strings.ReplaceAll(member.Name, "\\", "/")
				if strings.HasPrefix(n, "/") || path.Clean(n) != n || strings.HasPrefix(n, "../") {
					apiError(c, 400, "ZIP 包含无效路径")
					return
				}
				expanded += int64(member.UncompressedSize64)
				if expanded > 64*1024*1024 || member.UncompressedSize64 > 10*1024*1024 {
					apiError(c, 400, "ZIP 解压大小超限")
					return
				}
				r, err := member.Open()
				if err != nil {
					apiError(c, 400, "ZIP 读取失败")
					return
				}
				b, err := io.ReadAll(io.LimitReader(r, 10*1024*1024+1))
				r.Close()
				if err != nil || len(b) > 10*1024*1024 {
					apiError(c, 400, "ZIP 内容无效")
					return
				}
				if paths[n] {
					apiError(c, 400, "归档包含重复路径，请为文档和素材使用唯一名称")
					return
				}
				paths[n] = true
				if isImage(n) {
					assets[n] = b
				} else {
					inputs = append(inputs, input{n, b})
				}
			}
		} else {
			expanded += int64(len(raw))
			if paths[name] {
				apiError(c, 400, "文件名称重复，请重命名后上传")
				return
			}
			paths[name] = true
			inputs = append(inputs, input{name, raw})
		}
	}
	if expanded > 64*1024*1024 || len(inputs) > 250 {
		apiError(c, 400, "批次文档数量或大小超限")
		return
	}
	// Fetch each direct Markdown image once, with four bounded local workers.
	references := map[string]bool{}
	for _, in := range inputs {
		if strings.EqualFold(path.Ext(in.name), ".md") {
			for _, match := range imageLink.FindAllStringSubmatch(string(in.raw), -1) {
				if strings.HasPrefix(match[1], "https://") {
					references[match[1]] = true
				}
			}
		}
	}
	if len(references) > 400 {
		apiError(c, 400, "图片数量超过单批限制")
		return
	}
	jobs := make(chan string)
	var downloads sync.WaitGroup
	var assetMu sync.Mutex
	for worker := 0; worker < 4; worker++ {
		downloads.Add(1)
		go func() {
			defer downloads.Done()
			for ref := range jobs {
				raw, _ := cachedImage(c.Request.Context(), ref)
				assetMu.Lock()
				assets[ref] = raw
				assetMu.Unlock()
			}
		}()
	}
	for ref := range references {
		jobs <- ref
	}
	close(jobs)
	downloads.Wait()
	existing, _ := a.db.ListKnowledge(c.Request.Context(), "", "")
	seen := map[string]int64{}
	for _, entry := range existing {
		if entry.LibraryID == lib {
			seen[entry.SourceHash] = entry.ID
		}
	}
	for _, in := range inputs {
		if path.Base(in.name) == "_manifest.json" {
			var m struct {
				Items []importMetadata `json:"items"`
			}
			if json.Unmarshal(in.raw, &m) == nil {
				for _, v := range m.Items {
					manifest[v.Path] = v
				}
			}
		}
	}
	for _, in := range inputs {
		if path.Base(in.name) == "_manifest.json" {
			continue
		}
		category := "未分类"
		parts := strings.Split(in.name, "/")
		if len(parts) > 1 {
			category = parts[len(parts)-2]
		}
		var markdown string
		var warnings []string
		var err error
		if ext := strings.ToLower(path.Ext(in.name)); ext == ".md" || ext == ".txt" {
			if !utf8.Valid(in.raw) || bytes.ContainsRune(in.raw, 0) {
				err = errors.New("文本编码必须为 UTF-8")
			} else {
				markdown = string(bytes.TrimPrefix(in.raw, []byte{239, 187, 191}))
			}
		} else {
			markdown, warnings, err = analysis.ConvertDocument(c.Request.Context(), path.Base(in.name), in.raw)
		}
		item := importItem{SourceFile: contentHash(string(in.raw)) + strings.ToLower(path.Ext(in.name)), Title: strings.TrimSuffix(path.Base(in.name), path.Ext(in.name)), Category: category, Scope: "reference", SourceName: in.name, Warnings: warnings, Status: "ready", ExcludedProducts: []string{}}
		if saveOriginal(in.name, in.raw) != nil {
			err = errors.New("无法保存原始文档")
		}
		meta, organized := manifest[in.name]
		if organized {
			item.Title = meta.Title
			if meta.SourceName != "" {
				item.SourceName = meta.SourceName
				if len(meta.SourceHash) == 64 && !strings.Contains(item.SourceName, "[SHA256 ") {
					item.SourceName += " [SHA256 " + meta.SourceHash + "]"
				}
			}
			item.Category = meta.Category
			if validKnowledgeScope(meta.Scope) {
				item.Scope = meta.Scope
			}
			item.ApplicableProducts = meta.ApplicableProducts
			item.ExcludedProducts = meta.ExcludedProducts
			item.Warnings = append(item.Warnings, meta.ReviewFlags...)
		}
		if err != nil {
			item.Status = "failed"
			item.Error = err.Error()
			item.ID = strconv.Itoa(len(batch.Items) + 1)
			batch.Items = append(batch.Items, item)
			continue
		}
		if len(markdown) > 256*1024 {
			item.Status = "failed"
			item.Error = "正文超过 256 KiB"
			item.ID = strconv.Itoa(len(batch.Items) + 1)
			batch.Items = append(batch.Items, item)
			continue
		}
		var cleanFlags []string
		markdown, cleanFlags = knowledge.CleanMarkdown(markdown)
		markdown = inlineReferenceImages(markdown)
		item.Warnings = append(item.Warnings, cleanFlags...)
		if strings.Contains(in.name, "会员") {
			item.Scope = "policy"
			item.Warnings = append(item.Warnings, "会员与售后政策需明确核对")
		}
		for _, term := range []string{"不适用", "除外", "退款", "保修", "Stripe", "冒烟", "升级至", "2024", "2025", "2026"} {
			if strings.Contains(markdown, term) {
				item.Warnings = append(item.Warnings, "含机型限制、政策、内部升级或日期条件，需核对完整上下文")
				break
			}
		}
		// Retain complete parent context for each semantic block; never fabricate bilingual equivalence.
		blocks, err := knowledge.MarkdownBlocks(markdown)
		if organized && err == nil {
			blocks = []knowledge.Block{{ID: "faq", Text: markdown}}
		}
		if err != nil {
			item.Status = "failed"
			item.Error = "章节过长，请按完整操作流程拆分"
			item.Markdown = markdown
			item.OriginalMarkdown = markdown
			item.ID = strconv.Itoa(len(batch.Items) + 1)
			batch.Items = append(batch.Items, item)
			continue
		}
		for _, block := range blocks {
			next := item
			next.ID = strconv.Itoa(len(batch.Items) + 1)
			next.OriginalMarkdown = block.Text
			next.Markdown, next.Warnings = localizeImages(c.Request.Context(), block.Text, in.name, assets, append([]string{}, item.Warnings...))
			next.RequiresConfirmation = len(next.Warnings) > 0 || next.Scope == "policy"
			if !organized {
				next.Title = semanticTitle(block.Text, next.Title)
			}
			next.Hash = importIdentity(lib, next)
			if id, ok := seen[next.Hash]; ok {
				next.Status = "duplicate"
				next.EntryID = id
			} else {
				seen[next.Hash] = 0
			}
			if !organized {
				next.Title = semanticTitle(block.Text, next.Title)
			}
			batch.Items = append(batch.Items, next)
		}
	}
	if err := saveImport(batch); err != nil {
		apiError(c, 500, "无法保存导入预览")
		return
	}
	c.JSON(201, batch)
}
func semanticTitle(markdown, fallback string) string {
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "#") {
			title := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if title != "" && len(title) < 512 {
				return title
			}
		}
	}
	return fallback
}
func saveOriginal(name string, raw []byte) error {
	dir := filepath.Join(knowledgeDir(), "originals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	h := sha256.Sum256(raw)
	return writePrivateFile(filepath.Join(dir, hex.EncodeToString(h[:])+strings.ToLower(path.Ext(name))), raw)
}
func isImage(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}
func localizeImages(ctx context.Context, markdown, name string, assets map[string][]byte, warnings []string) (string, []string) {
	out := imageLink.ReplaceAllStringFunc(markdown, func(match string) string {
		ref := imageLink.FindStringSubmatch(match)[1]
		if strings.HasPrefix(ref, "/api/knowledge/assets/") {
			return match
		}
		var raw []byte
		var err error
		if u, e := url.Parse(ref); e == nil && u.Scheme == "https" {
			if cached, ok := assets[ref]; ok {
				raw = cached
				if len(raw) == 0 {
					err = errors.New("missing image")
				}
			} else {
				raw, err = cachedImage(ctx, u.String())
			}
		} else {
			decoded, e := url.PathUnescape(ref)
			if e != nil {
				err = e
			} else {
				raw = assets[path.Clean(path.Join(path.Dir(name), decoded))]
			}
			if len(raw) == 0 {
				err = errors.New("missing image")
			}
		}
		if err != nil {
			warnings = append(warnings, "图片缺失，需补齐图示后发布")
			return match
		}
		local, e := saveImage(raw)
		if e != nil {
			warnings = append(warnings, "图片格式或大小不支持")
			return match
		}
		return strings.Replace(match, ref, local, 1)
	})
	if unsupportedImages(out) {
		warnings = append(warnings, "含暂不支持的图片语法，请改为 Markdown 内联图片")
	}
	return out, uniqueStrings(warnings)
}
func uniqueStrings(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	return out
}
func saveImage(raw []byte) (string, error) {
	if len(raw) > 8*1024*1024 {
		return "", errors.New("large image")
	}
	ext := ""
	switch http.DetectContentType(raw) {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	case "image/gif":
		ext = "gif"
	case "image/webp":
		ext = "webp"
	default:
		return "", errors.New("invalid image")
	}
	h := sha256.Sum256(raw)
	name := hex.EncodeToString(h[:]) + "." + ext
	dir := filepath.Join(knowledgeDir(), "assets")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	if e := writePrivateFile(filepath.Join(dir, name), raw); e != nil {
		return "", e
	}
	return "/api/knowledge/assets/" + name, nil
}
func fetchImage(ctx context.Context, address string) ([]byte, error) {
	// Resolve and dial the checked address together; redirects cannot reach internal services.
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
				return nil, errors.New("private destination")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("no destination")
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != "https" {
			return errors.New("invalid redirect")
		}
		return nil
	}}
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, e
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("image unavailable")
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if len(raw) > 8*1024*1024 {
		return nil, errors.New("large image")
	}
	return raw, e
}

func cachedImage(ctx context.Context, address string) ([]byte, error) {
	dir := filepath.Join(knowledgeDir(), "image-cache")
	name := filepath.Join(dir, contentHash(address))
	if raw, e := os.ReadFile(name); e == nil {
		return raw, nil
	}
	raw, e := fetchImage(ctx, address)
	if e != nil {
		return nil, e
	}
	if _, e = saveImage(raw); e != nil {
		return nil, e
	}
	if os.MkdirAll(dir, 0700) == nil {
		_ = writePrivateFile(name, raw)
	}
	return raw, nil
}

func importIdentity(library int64, item importItem) string {
	raw, _ := json.Marshal(struct {
		Library                                           int64
		Title, Category, Scope, Product, Markdown, Source string
		Applicable, Excluded                              []string
	}{library, item.Title, item.Category, item.Scope, item.Product, item.Markdown, item.SourceName, item.ApplicableProducts, item.ExcludedProducts})
	return contentHash(string(raw))
}

var referenceDefinition = regexp.MustCompile(`(?m)^ {0,3}\[([^]\n]+)\]:[ \t]*<?([^>\s]+)>?(?:[ \t]+[^\n]*)?$`)
var referenceImage = regexp.MustCompile(`!\[([^]\n]*)\]\[([^]\n]*)\]`)
var htmlImage = regexp.MustCompile(`(?i)<img\b`)

func inlineReferenceImages(markdown string) string {
	definitions := map[string]string{}
	for _, match := range referenceDefinition.FindAllStringSubmatch(markdown, -1) {
		definitions[strings.ToLower(strings.TrimSpace(match[1]))] = match[2]
	}
	return referenceImage.ReplaceAllStringFunc(markdown, func(match string) string {
		m := referenceImage.FindStringSubmatch(match)
		key := m[2]
		if key == "" {
			key = m[1]
		}
		if ref, ok := definitions[strings.ToLower(strings.TrimSpace(key))]; ok {
			return "![" + m[1] + "](" + ref + ")"
		}
		return match
	})
}
func unsupportedImages(markdown string) bool {
	return htmlImage.MatchString(markdown) || strings.Contains(imageLink.ReplaceAllString(markdown, ""), "![")
}
