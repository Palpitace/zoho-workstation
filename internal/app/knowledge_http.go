// input: 同源受保护的分类资料上传、SOP 草稿/发布/恢复操作及知识查询。
// output: 有界本地导入、不可变版本对照、显式发布/恢复和独立建议状态。
// pos: 知识库与人工确认的 HTTP 边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/lab"
	"zoho-workstation/internal/ticket"
)

func (a *App) knowledgeRoutes(router *gin.Engine) {
	router.GET("/api/knowledge", func(c *gin.Context) {
		entries, err := a.db.ListKnowledge(c.Request.Context(), "", c.Query("status"))
		filtered := entries[:0]
		for _, entry := range entries {
			if library := queryID(c, "library_id"); library != 0 && entry.LibraryID != library {
				continue
			}
			if category := queryID(c, "category_id"); category != 0 && entry.CategoryID != category {
				continue
			}
			if q := strings.ToLower(c.Query("q")); q != "" && !strings.Contains(strings.ToLower(entry.Title+" "+entry.Markdown), q) {
				continue
			}
			filtered = append(filtered, entry)
		}
		entries = filtered
		if err != nil {
			apiError(c, 500, "无法读取知识库")
			return
		}
		// The list needs metadata only; full evidence is loaded at the detail endpoint.
		for i := range entries {
			entries[i].Blocks = []knowledge.Block{}
			entries[i].Markdown = ""
			entries[i].MessageIDs = []string{}
		}
		c.JSON(200, gin.H{"data": entries})
	})
	router.GET("/api/knowledge/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			apiError(c, 400, "知识条目 ID 无效")
			return
		}
		entry, err := a.db.Knowledge(c.Request.Context(), id)
		if err != nil {
			apiError(c, 404, "知识条目不存在")
			return
		}
		c.JSON(200, entry)
	})
	router.GET("/api/knowledge/:id/history", a.knowledgeHistory)
	router.POST("/api/knowledge/:id/restore", a.restoreKnowledge)
	router.POST("/api/knowledge/import", a.importKnowledge)
	router.POST("/api/knowledge/draft", a.draftKnowledge)
	for _, action := range []string{"publish", "archive"} {
		router.POST("/api/knowledge/:id/"+action, func(c *gin.Context) { a.setKnowledgeStatus(c, action) })
	}
	router.GET("/api/tickets/:id/knowledge", func(c *gin.Context) {
		record, err := a.db.Get(c.Request.Context(), c.Param("id"))
		if err != nil || record.Pending {
			apiError(c, 404, "工单尚未保存")
			return
		}
		caseView, err := a.knowledgeView(c.Request.Context(), record, "case")
		if err != nil {
			apiError(c, 500, "无法读取案例状态")
			return
		}
		adviceView, err := a.knowledgeView(c.Request.Context(), record, "advice")
		if err != nil {
			apiError(c, 500, "无法读取建议状态")
			return
		}
		a.mu.Lock()
		busy := a.busy["bub"] || a.busy["case"] || a.busy["advice"] || a.busy["lab"]
		a.mu.Unlock()
		c.JSON(200, gin.H{"case": caseView, "advice": adviceView, "enabled": a.cfg.Bub.Enabled, "busy": busy, "automatic_eligible": a.automaticallyEligible(record)})
	})
}

func redactDocumentText(text string) string {
	return ticket.RedactedSnapshot(ticket.Snapshot{Description: text}, ticket.RawSnapshot{}).Description
}

func (a *App) importKnowledge(c *gin.Context) {
	a.mu.Lock()
	if a.busy["document"] {
		a.mu.Unlock()
		apiError(c, 409, "已有文档正在导入，请稍后重试")
		return
	}
	a.busy["document"] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.busy["document"] = false; a.mu.Unlock() }()
	// Includes bounded multipart headers; parser process receives only one document.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 11*1024*1024)
	if c.Request.ParseMultipartForm(11*1024*1024) != nil {
		apiError(c, 400, "无法读取上传文档，文件上限 10 MiB")
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	scope := strings.TrimSpace(c.PostForm("scope"))
	if !validKnowledgeScope(scope) {
		apiError(c, 400, "知识分类无效")
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		apiError(c, 400, "请选择要导入的文档")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 10*1024*1024+1))
	if err != nil || len(raw) > 10*1024*1024 {
		apiError(c, 400, "文档读取失败或超过 10 MiB")
		return
	}
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	blocks, err := analysis.ExtractDocument(c.Request.Context(), name, raw)
	if err != nil {
		apiError(c, 422, err.Error())
		return
	}
	for i := range blocks {
		blocks[i].Text = redactDocumentText(blocks[i].Text)
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	// Original evidence is local/private; parsed and redacted blocks are separately versioned.
	dir := filepath.Join("data", "knowledge")
	if os.MkdirAll(dir, 0700) != nil {
		apiError(c, 500, "无法保存本地文档")
		return
	}
	path := filepath.Join(dir, hash+strings.ToLower(filepath.Ext(name)))
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		err = writePrivateFile(path, raw)
	}
	if err != nil {
		apiError(c, 500, "无法保存本地文档")
		return
	}
	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		title = name
	}
	replaces := int64(0)
	if value := c.PostForm("replaces_id"); value != "" {
		replaces, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			apiError(c, 400, "被替代版本无效")
			return
		}
	}
	entry, err := a.db.SaveKnowledge(c.Request.Context(), knowledge.Entry{Kind: "sop", Scope: scope, Title: redactDocumentText(title), Product: redactDocumentText(strings.TrimSpace(c.PostForm("product"))), SourceName: redactDocumentText(name), SourceHash: hash, ReplacesID: replaces, Blocks: blocks})
	if err != nil {
		apiError(c, 400, "导入未保存，请检查标题、正文大小和被替代版本")
		return
	}
	if entry.Title != redactDocumentText(title) || entry.Product != redactDocumentText(strings.TrimSpace(c.PostForm("product"))) || entry.ReplacesID != replaces || (scope != "" && entry.Scope != scope) {
		apiError(c, 409, "相同文档已存在，请打开已有条目并通过“保存修订草稿”调整标题、产品、分类或版本")
		return
	}
	c.JSON(201, entry)
}

func (a *App) draftKnowledge(c *gin.Context) {
	var body struct {
		Title              string   `json:"title"`
		Markdown           string   `json:"markdown"`
		LibraryID          int64    `json:"library_id"`
		CategoryID         int64    `json:"category_id"`
		ApplicableProducts []string `json:"applicable_products"`
		ExcludedProducts   []string `json:"excluded_products"`
		ReviewFlags        []string `json:"review_flags"`
		Scope              string   `json:"scope"`
		Product            string   `json:"product"`
		Text               string   `json:"text"`
		SourceEntryID      int64    `json:"source_entry_id"`
		ReplacesID         int64    `json:"replaces_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 300*1024)
	if c.ShouldBindJSON(&body) != nil {
		apiError(c, 400, "草稿格式无效")
		return
	}
	if !validKnowledgeScope(body.Scope) {
		apiError(c, 400, "知识分类无效")
		return
	}
	if body.Markdown != "" {
		body.Text = body.Markdown
	}
	entry := knowledge.Entry{LibraryID: body.LibraryID, CategoryID: body.CategoryID, ApplicableProducts: body.ApplicableProducts, ExcludedProducts: body.ExcludedProducts, ReviewFlags: body.ReviewFlags, Kind: "sop", Scope: body.Scope, Title: redactDocumentText(strings.TrimSpace(body.Title)), Product: redactDocumentText(strings.TrimSpace(body.Product)), SourceName: "人工整理草稿", ReplacesID: body.ReplacesID}
	if body.SourceEntryID != 0 {
		source, err := a.db.Knowledge(c.Request.Context(), body.SourceEntryID)
		if err != nil || source.Status == "archived" {
			apiError(c, 409, "来源已失效，请重新选择")
			return
		}
		if strings.TrimSpace(body.Text) == "" {
			body.Text = knowledgeDraftBody(source)
		}
		if err := inheritKnowledgeSource(&entry, source); err != nil {
			apiError(c, 400, err.Error())
			return
		}
	}
	if strings.TrimSpace(body.Text) == "" || len(body.Text) > 256*1024 {
		apiError(c, 400, "草稿正文不能为空且不能超过 256 KiB")
		return
	}
	entry.Markdown = body.Text
	identity, _ := json.Marshal(entry)
	digest := sha256.Sum256(identity)
	entry.SourceHash = hex.EncodeToString(digest[:])
	saved, err := a.db.SaveKnowledge(c.Request.Context(), entry)
	if err != nil {
		apiError(c, 400, "草稿未保存，请检查标题和被替代版本")
		return
	}
	c.JSON(201, saved)
}

func validKnowledgeScope(scope string) bool {
	return scope == "" || scope == "reference" || scope == "technical" || scope == "policy"
}

func (a *App) knowledgeHistory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, "知识条目 ID 无效")
		return
	}
	entries, err := a.db.KnowledgeHistory(c.Request.Context(), id)
	if err != nil {
		apiError(c, 404, "无法读取版本历史")
		return
	}
	c.JSON(200, gin.H{"data": entries})
}

func (a *App) restoreKnowledge(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, "知识条目 ID 无效")
		return
	}
	entry, err := a.db.RestoreKnowledge(c.Request.Context(), id)
	if err != nil {
		apiError(c, 409, "无法恢复：请选择与当前正式版本不同的历史 SOP。恢复只生成草稿。")
		return
	}
	c.JSON(201, entry)
}

func (a *App) setKnowledgeStatus(c *gin.Context, action string) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, "知识条目 ID 无效")
		return
	}
	if action == "publish" {
		entry, readErr := a.db.Knowledge(c.Request.Context(), id)
		if readErr != nil {
			apiError(c, 404, "知识条目不存在")
			return
		}
		if hasMissingImages(entry.Markdown) {
			apiError(c, 409, "请补齐图片后发布")
			return
		}
		if entry.LabBatchID > 0 {
			batch, batchErr := a.db.LabBatch(c.Request.Context(), entry.LabBatchID)
			if batchErr != nil || !lab.ValidationComplete(batch) {
				apiError(c, 409, "留出对比尚未完整成功，不能发布此实验修订")
				return
			}
		}
		err = a.db.PublishKnowledge(c.Request.Context(), id)
	} else {
		err = a.db.ArchiveKnowledge(c.Request.Context(), id)
	}
	if err != nil {
		apiError(c, 409, "此版本无法操作，请刷新后检查条目状态及被替代版本")
		return
	}
	select {
	case a.analysisWake <- struct{}{}:
	default:
	}
	c.JSON(200, gin.H{"status": "ok"})
}

func inheritKnowledgeSource(entry *knowledge.Entry, source knowledge.Entry) error {
	if entry.Title == "" {
		entry.Title = source.Title + " · SOP 草稿"
	}
	if entry.Product == "" && entry.ApplicableProducts == nil {
		entry.Product = source.Product
	}
	entry.SourceName = "知识条目 #" + strconv.FormatInt(source.ID, 10)
	entry.LibraryID = source.LibraryID
	if entry.CategoryID == 0 {
		entry.CategoryID = source.CategoryID
	}
	if entry.ApplicableProducts == nil {
		entry.ApplicableProducts = source.ApplicableProducts
	}
	if entry.ExcludedProducts == nil {
		entry.ExcludedProducts = source.ExcludedProducts
	}
	entry.SourceTicketID = source.SourceTicketID
	entry.MessageIDs = source.MessageIDs
	entry.SourceTickets = append([]string{}, source.SourceTickets...)
	entry.LabBatchID = source.LabBatchID
	if entry.Scope == "" {
		entry.Scope = source.Scope
	}
	if source.Scope == "policy" && entry.Scope != "policy" {
		return errors.New("售后政策资料需保留政策分类")
	}
	return nil
}
