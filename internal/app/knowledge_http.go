// input: 同源受保护的文档上传、SOP 草稿/发布操作及知识查询。
// output: 有界本地导入、不可变版本、显式发布和独立建议状态。
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
	"zoho-workstation/internal/ticket"
)

func (a *App) knowledgeRoutes(router *gin.Engine) {
	router.GET("/api/knowledge", func(c *gin.Context) {
		entries, err := a.db.ListKnowledge(c.Request.Context(), c.Query("q"), c.Query("status"))
		if err != nil {
			apiError(c, 500, "无法读取知识库")
			return
		}
		// The list needs metadata only; full evidence is loaded at the detail endpoint.
		for i := range entries {
			entries[i].Blocks = []knowledge.Block{}
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
	router.POST("/api/knowledge/import", a.importKnowledge)
	router.POST("/api/knowledge/draft", a.draftKnowledge)
	for _, action := range []string{"publish", "archive"} {
		router.POST("/api/knowledge/:id/"+action, func(c *gin.Context) {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil {
				apiError(c, 400, "知识条目 ID 无效")
				return
			}
			if action == "publish" {
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
		})
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
		busy := a.busy["bub"] || a.busy["case"] || a.busy["advice"]
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
	entry, err := a.db.SaveKnowledge(c.Request.Context(), knowledge.Entry{Kind: "sop", Title: redactDocumentText(title), Product: redactDocumentText(strings.TrimSpace(c.PostForm("product"))), SourceName: redactDocumentText(name), SourceHash: hash, ReplacesID: replaces, Blocks: blocks})
	if err != nil {
		apiError(c, 400, "导入未保存，请检查标题、正文大小和被替代版本")
		return
	}
	if entry.Title != redactDocumentText(title) || entry.Product != redactDocumentText(strings.TrimSpace(c.PostForm("product"))) || entry.ReplacesID != replaces {
		apiError(c, 409, "相同文档已存在，请打开已有条目并通过“保存修订草稿”调整标题、产品或版本")
		return
	}
	c.JSON(201, entry)
}

func (a *App) draftKnowledge(c *gin.Context) {
	var body struct {
		Title         string `json:"title"`
		Product       string `json:"product"`
		Text          string `json:"text"`
		SourceEntryID int64  `json:"source_entry_id"`
		ReplacesID    int64  `json:"replaces_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 300*1024)
	if c.ShouldBindJSON(&body) != nil {
		apiError(c, 400, "草稿格式无效")
		return
	}
	entry := knowledge.Entry{Kind: "sop", Title: redactDocumentText(strings.TrimSpace(body.Title)), Product: redactDocumentText(strings.TrimSpace(body.Product)), SourceName: "人工整理草稿", ReplacesID: body.ReplacesID}
	if body.SourceEntryID != 0 {
		source, err := a.db.Knowledge(c.Request.Context(), body.SourceEntryID)
		if err != nil || source.Status == "archived" {
			apiError(c, 409, "来源已失效，请重新选择")
			return
		}
		if strings.TrimSpace(body.Text) == "" {
			body.Text = knowledgeDraftBody(source)
		}
		if entry.Title == "" {
			entry.Title = source.Title + " · SOP 草稿"
		}
		if entry.Product == "" {
			entry.Product = source.Product
		}
		entry.SourceName = "知识条目 #" + strconv.FormatInt(source.ID, 10)
		entry.SourceTicketID = source.SourceTicketID
		entry.MessageIDs = source.MessageIDs
	}
	if strings.TrimSpace(body.Text) == "" || len(body.Text) > 256*1024 {
		apiError(c, 400, "草稿正文不能为空且不能超过 256 KiB")
		return
	}
	// Split at rune boundaries, retaining all text and explicit section positions.
	remaining := redactDocumentText(body.Text)
	for len(remaining) > 0 {
		end := min(len(remaining), 8*1024)
		for end < len(remaining) && (remaining[end]&0xc0) == 0x80 {
			end--
		}
		text := remaining[:end]
		remaining = remaining[end:]
		if strings.TrimSpace(text) != "" {
			entry.Blocks = append(entry.Blocks, knowledge.Block{ID: "section-" + strconv.Itoa(len(entry.Blocks)+1), Text: text})
		}
	}
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
