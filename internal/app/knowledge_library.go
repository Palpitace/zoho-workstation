// input: 同源知识库、分类、Markdown 版本、批次确认与本地资源请求。
// output: 部门隔离的管理接口及保留版本的导入导出。
// pos: 知识库管理 HTTP 能力。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/knowledge"
)

func (a *App) hasLibrary(ctx context.Context, id int64) bool {
	libs, e := a.db.ListLibraries(ctx)
	if e != nil {
		return false
	}
	for _, lib := range libs {
		if lib.ID == id {
			return true
		}
	}
	return false
}
func queryID(c *gin.Context, name string) int64 {
	id, _ := strconv.ParseInt(c.Query(name), 10, 64)
	return id
}
func (a *App) libraryRoutes(router *gin.Engine) {
	router.GET("/api/knowledge/libraries", func(c *gin.Context) {

		libs, e := a.db.ListLibraries(c.Request.Context())
		if e != nil {
			apiError(c, 500, "无法读取知识库")
			return
		}
		c.JSON(200, gin.H{"data": libs, "default_department_id": a.cfg.Zoho.DepartmentID})
	})
	router.POST("/api/knowledge/libraries", func(c *gin.Context) {
		var lib knowledge.Library
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		if c.ShouldBindJSON(&lib) != nil {
			apiError(c, 400, "知识库格式无效")
			return
		}
		saved, e := a.db.SaveLibrary(c.Request.Context(), lib)
		if e != nil {
			apiError(c, 400, "请检查知识库名称和部门")
			return
		}
		c.JSON(200, saved)
	})
	router.GET("/api/knowledge/categories", func(c *gin.Context) {
		categories, e := a.db.ListCategories(c.Request.Context(), queryID(c, "library_id"))
		if e != nil {
			apiError(c, 400, "无法读取分类")
			return
		}
		c.JSON(200, gin.H{"data": categories})
	})
	router.POST("/api/knowledge/categories", func(c *gin.Context) {
		var category knowledge.Category
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&category) != nil {
			apiError(c, 400, "分类格式无效")
			return
		}
		saved, e := a.db.SaveCategory(c.Request.Context(), category)
		if e != nil {
			apiError(c, 400, "分类名称无效或已存在")
			return
		}
		c.JSON(200, saved)
	})
	router.POST("/api/knowledge/:id/move", func(c *gin.Context) {
		var body struct {
			CategoryID int64 `json:"category_id"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
		if c.ShouldBindJSON(&body) != nil {
			apiError(c, 400, "分类格式无效")
			return
		}
		if e := a.db.MoveKnowledge(c.Request.Context(), id, body.CategoryID); e != nil {
			apiError(c, 400, "无法移动条目")
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})
	router.POST("/api/knowledge/import-batch", a.batchImport)
	router.GET("/api/knowledge/import-batch/:batch", func(c *gin.Context) {
		b, e := loadImport(c.Param("batch"))
		if e != nil {
			apiError(c, 404, "导入批次不存在")
			return
		}
		c.JSON(200, b)
	})
	router.POST("/api/knowledge/import-batch/:batch/confirm", a.confirmImport)
	router.POST("/api/knowledge/import-batch/:batch/retry", a.retryImport)
	router.GET("/api/knowledge/assets/:asset", func(c *gin.Context) {
		name := c.Param("asset")
		if !assetID.MatchString(name) {
			c.Status(404)
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "private, max-age=86400")
		c.File(filepath.Join(knowledgeDir(), "assets", name))
	})
	router.POST("/api/knowledge/assets", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 9*1024*1024)
		f, _, e := c.Request.FormFile("file")
		if e != nil {
			apiError(c, 400, "请选择图片")
			return
		}
		defer f.Close()
		if c.Request.MultipartForm != nil {
			defer c.Request.MultipartForm.RemoveAll()
		}
		raw, e := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
		if e != nil {
			apiError(c, 400, "图片读取失败")
			return
		}
		url, e := saveImage(raw)
		if e != nil {
			apiError(c, 400, "支持 PNG、JPEG、GIF、WebP，最多 8 MiB")
			return
		}
		c.JSON(201, gin.H{"url": url})
	})
	router.GET("/api/knowledge/:id/export", a.exportKnowledge)
	a.embeddingRoutes(router)
}
func (a *App) confirmImport(c *gin.Context) {
	a.importMu.Lock()
	defer a.importMu.Unlock() // serialize manifest confirmation and category creation
	batch, e := loadImport(c.Param("batch"))
	if e != nil {
		apiError(c, 404, "导入批次不存在")
		return
	}
	var body struct {
		ItemIDs        []string     `json:"item_ids"`
		Publish        bool         `json:"publish"`
		ConfirmedRisks bool         `json:"confirmed_risks"`
		Items          []importItem `json:"items"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024*1024)
	if c.ShouldBindJSON(&body) != nil || len(body.ItemIDs) == 0 {
		apiError(c, 400, "请选择导入条目")
		return
	}
	chosen := map[string]bool{}
	for _, id := range body.ItemIDs {
		chosen[id] = true
	}
	overrides := map[string]importItem{}
	for _, item := range body.Items {
		overrides[item.ID] = item
	}
	result := []knowledge.Entry{}
	failures := []string{}
	ctx := c.Request.Context()
	categories, _ := a.db.ListCategories(ctx, batch.LibraryID)
	categoryIDs := map[string]int64{}
	for _, category := range categories {
		categoryIDs[category.Name] = category.ID
	}
	for i := range batch.Items {
		item := &batch.Items[i]
		if !chosen[item.ID] {
			continue
		}
		if item.Status == "failed" || item.Status == "duplicate" {
			continue
		}
		if edit, ok := overrides[item.ID]; ok {
			if strings.TrimSpace(edit.Title) != "" {
				item.Title = edit.Title
			}
			if strings.TrimSpace(edit.Markdown) != "" {
				item.Markdown = edit.Markdown
			}
			item.Product = edit.Product
			item.ExcludedProducts = edit.ExcludedProducts
			item.ApplicableProducts = edit.ApplicableProducts
			if edit.Category != "" {
				item.Category = edit.Category
			}
			if validKnowledgeScope(edit.Scope) && edit.Scope != "" && item.Scope != "policy" {
				item.Scope = edit.Scope
			}
		}
		if body.Publish && item.RequiresConfirmation && !body.ConfirmedRisks {
			failures = append(failures, item.ID+": 需确认待核对内容")
			continue
		}
		if body.Publish && hasMissingImages(item.Markdown) {
			failures = append(failures, item.ID+": 请补齐图片后发布")
			continue
		}
		categoryID := categoryIDs[item.Category]
		if categoryID == 0 {
			category, e := a.db.SaveCategory(ctx, knowledge.Category{LibraryID: batch.LibraryID, Name: item.Category})
			if e != nil {
				failures = append(failures, item.ID+": 分类无法保存")
				continue
			}
			categoryID = category.ID
			categoryIDs[item.Category] = categoryID
		}
		entry := knowledge.Entry{Kind: "sop", LibraryID: batch.LibraryID, CategoryID: categoryID, Title: redactDocumentText(item.Title), Scope: item.Scope, Product: item.Product, ExcludedProducts: item.ExcludedProducts, ApplicableProducts: item.ApplicableProducts, Markdown: item.Markdown, SourceName: item.SourceName, SourceHash: item.Hash, ReviewFlags: item.Warnings}
		entry.SourceHash = importIdentity(batch.LibraryID, *item)
		if item.EntryID > 0 {
			previous, err := a.db.Knowledge(ctx, item.EntryID)
			if err != nil {
				failures = append(failures, item.ID+": 原版本不可用")
				continue
			}
			if previous.SourceHash != entry.SourceHash {
				entry.ReplacesID = previous.ID
			}
		}
		saved, e := a.db.SaveKnowledge(ctx, entry)
		if e != nil {
			failures = append(failures, item.ID+": 内容未保存，请检查章节长度与适用条件")
			continue
		}
		if body.Publish {
			if e = a.db.PublishKnowledge(ctx, saved.ID); e != nil {
				failures = append(failures, item.ID+": 草稿已保存，发布失败")
			} else {
				saved.Status = "published"
			}
		}
		item.EntryID = saved.ID
		item.Status = "ready"
		if saved.Status == "published" || saved.Status == "archived" {
			item.Status = "duplicate"
		}
		result = append(result, saved)
	}
	if e = saveImport(batch); e != nil {
		apiError(c, 500, "条目已保存，但预览状态保存失败；重新导入会去重")
		return
	}
	c.JSON(200, gin.H{"data": result, "errors": failures})
}
func hasMissingImages(markdown string) bool {
	if unsupportedImages(markdown) {
		return true
	}
	for _, m := range imageLink.FindAllStringSubmatch(markdown, -1) {
		name := strings.TrimPrefix(m[1], "/api/knowledge/assets/")
		if !assetID.MatchString(name) {
			return true
		}
		if _, e := os.Stat(filepath.Join(knowledgeDir(), "assets", name)); e != nil {
			return true
		}
	}
	return false
}
func (a *App) retryImport(c *gin.Context) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	batch, e := loadImport(c.Param("batch"))
	if e != nil {
		apiError(c, 404, "批次不存在")
		return
	}
	var body struct {
		ItemIDs []string `json:"item_ids"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	if c.ShouldBindJSON(&body) != nil {
		apiError(c, 400, "重试格式无效")
		return
	}
	chosen := map[string]bool{}
	for _, id := range body.ItemIDs {
		chosen[id] = true
	}
	for i := range batch.Items {
		item := &batch.Items[i]
		if !chosen[item.ID] || item.Status == "duplicate" {
			continue
		}
		if item.OriginalMarkdown == "" {
			if item.SourceFile == "" || filepath.Base(item.SourceFile) != item.SourceFile {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(knowledgeDir(), "originals", item.SourceFile))
			if err != nil {
				continue
			}
			markdown, warnings, err := analysis.ConvertDocument(c.Request.Context(), filepath.Base(item.SourceName), raw)
			if err != nil {
				item.Error = err.Error()
				continue
			}
			if _, err = knowledge.MarkdownBlocks(markdown); err != nil {
				item.Error = "章节过长，请先拆分完整流程"
				continue
			}
			item.OriginalMarkdown = markdown
			item.Markdown = markdown
			item.Warnings = append(item.Warnings, warnings...)
		}
		flags := []string{}
		for _, flag := range item.Warnings {
			if !strings.Contains(flag, "图片") {
				flags = append(flags, flag)
			}
		}
		item.Markdown, item.Warnings = localizeImages(c.Request.Context(), item.Markdown, item.SourceName, nil, flags)
		item.RequiresConfirmation = len(item.Warnings) > 0
		item.Status = "ready"
		item.Error = ""
	}
	if saveImport(batch) != nil {
		apiError(c, 500, "重试结果保存失败")
		return
	}
	c.JSON(200, batch)
}
func (a *App) exportKnowledge(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	entry, e := a.db.Knowledge(c.Request.Context(), id)
	if e != nil {
		apiError(c, 404, "条目不存在")
		return
	}
	markdown := entry.Markdown
	if markdown == "" {
		for _, block := range entry.Blocks {
			markdown += block.Text + "\n\n"
		}
	}
	if c.Query("format") != "zip" {
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="knowledge-%d.md"`, id))
		c.Data(200, "text/markdown; charset=utf-8", []byte(markdown))
		return
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	seen := map[string]bool{}
	for _, match := range imageLink.FindAllStringSubmatch(markdown, -1) {
		name := strings.TrimPrefix(match[1], "/api/knowledge/assets/")
		if !assetID.MatchString(name) || seen[name] {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(knowledgeDir(), "assets", name))
		if e != nil {
			archive.Close()
			apiError(c, 409, "有图片缺失，请补齐后导出 ZIP")
			return
		}
		f, e := archive.Create("assets/" + name)
		if e != nil {
			apiError(c, 500, "导出失败")
			return
		}
		_, _ = f.Write(raw)
		seen[name] = true
		markdown = strings.ReplaceAll(markdown, match[1], "assets/"+name)
	}
	f, _ := archive.Create(fmt.Sprintf("knowledge-%d.md", id))
	_, _ = f.Write([]byte(markdown))
	category := "未分类"
	categories, _ := a.db.ListCategories(c.Request.Context(), entry.LibraryID)
	for _, v := range categories {
		if v.ID == entry.CategoryID {
			category = v.Name
		}
	}
	metadata, _ := json.MarshalIndent(map[string]any{"version": 1, "items": []map[string]any{{"path": fmt.Sprintf("knowledge-%d.md", id), "title": entry.Title, "category": category, "scope": entry.Scope, "applicable_products": entry.ApplicableProducts, "excluded_products": entry.ExcludedProducts, "review_flags": entry.ReviewFlags, "source_name": entry.SourceName, "source_hash": entry.SourceHash, "source_entry_id": entry.ID}}}, "", "  ")
	f, _ = archive.Create("_manifest.json")
	_, _ = f.Write(metadata)
	if archive.Close() != nil {
		apiError(c, 500, "导出失败")
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="knowledge-%d.zip"`, id))
	c.Data(200, "application/zip", buffer.Bytes())
}
