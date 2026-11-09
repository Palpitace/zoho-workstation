// input: 已发布且部门可访问的知识版本、固定本地嵌入模型和检索测试请求。
// output: 增量索引、持久查询向量、混合召回及关键词降级状态。
// pos: 知识检索与本机推理生命周期的连接层。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
	"time"

	"zoho-workstation/internal/embedding"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/store"
)

func (a *App) embeddingRoutes(router *gin.Engine) {
	router.GET("/api/knowledge/embedding", func(c *gin.Context) {
		s := a.embeddings.Status()
		entries, _ := a.db.ListKnowledge(c.Request.Context(), "", "published")
		vectors, _ := a.db.KnowledgeVectors(c.Request.Context(), embedding.ModelID, knowledge.ChunkVersion)
		count := 0
		for _, entry := range entries {
			count += len(entry.Blocks)
		}
		c.JSON(200, gin.H{"state": s.State, "progress": s.Progress, "error": s.Error, "model": s.Model, "degraded": s.Degraded, "indexed": len(vectors), "total": count})
	})
	router.POST("/api/knowledge/embedding/download", func(c *gin.Context) {
		if a.embeddings.Enable() != nil {
			apiError(c, 409, "服务正在关闭")
			return
		}
		c.JSON(202, a.embeddings.Status())
	})
	router.POST("/api/knowledge/retrieve", func(c *gin.Context) {
		var body struct {
			LibraryID    int64  `json:"library_id"`
			DepartmentID string `json:"department_id"`
			Product      string `json:"product"`
			Query        string `json:"query"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
		if c.ShouldBindJSON(&body) != nil || strings.TrimSpace(body.Query) == "" || len(body.Query) > 16*1024 || body.DepartmentID == "" {
			apiError(c, 400, "请选择部门并输入问题")
			return
		}
		entries, e := a.db.ScopedKnowledge(c.Request.Context(), body.LibraryID, body.DepartmentID, "published")
		if e != nil {
			apiError(c, 500, "知识查询失败")
			return
		}
		query, e := a.embeddings.Embed(c.Request.Context(), body.Query, true)
		mode := "hybrid"
		if e != nil {
			mode = "keyword"
			query = nil
		}
		vectors, _ := a.db.KnowledgeVectors(c.Request.Context(), embedding.ModelID, knowledge.ChunkVersion)
		sources := knowledge.HybridSources(entries, vectors, query, "", body.Product, body.Query, 8)
		c.JSON(200, gin.H{"sources": sources, "mode": mode})
	})
}
func (a *App) embeddingLoop() {
	defer a.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if a.embeddings.Status().State == "ready" {
				a.indexKnowledge(a.ctx)
			}
		}
	}
}
func (a *App) indexKnowledge(ctx context.Context) {
	entries, e := a.db.ListKnowledge(ctx, "", "published")
	if e != nil {
		return
	}
	vectors, e := a.db.KnowledgeVectors(ctx, embedding.ModelID, knowledge.ChunkVersion)
	if e != nil {
		return
	}
	seen := map[string]string{}
	for _, v := range vectors {
		seen[fmt.Sprintf("%d:%s", v.EntryID, v.BlockID)] = v.SourceHash
	}
	for _, entry := range entries {
		for _, block := range entry.Blocks {
			if ctx.Err() != nil {
				return
			}
			if seen[fmt.Sprintf("%d:%s", entry.ID, block.ID)] == entry.SourceHash {
				continue
			}
			values, e := a.embeddings.Embed(ctx, knowledge.EmbeddingText(entry.Title, block.Text), false)
			if e != nil {
				continue
			}
			_ = a.db.SaveKnowledgeVector(ctx, knowledge.Vector{EntryID: entry.ID, BlockID: block.ID, SourceHash: entry.SourceHash, Model: embedding.ModelID, ChunkVersion: knowledge.ChunkVersion, Values: values})
		}
	}
	// Status reads only use this cache; they never launch inference. New ticket
	// queries are embedded once after the corresponding successful Bub summary.
	records, e := a.db.List(ctx, "", "")
	if e != nil {
		return
	}
	for _, record := range records {
		if ctx.Err() != nil {
			return
		}
		if record.Pending || !record.Complete || record.SyncStopped {
			continue
		}
		full, e := a.db.Get(ctx, record.ID)
		if e != nil {
			continue
		}
		req, conversation, e := requestForRecord(ctx, a.db, a.cfg, full, "bub", DevelopmentRound)
		if e != nil {
			continue
		}
		view, e := a.db.Analysis(ctx, req)
		if e != nil || view.Success == nil || view.Stale {
			continue
		}
		var input struct {
			Subject     string `json:"subject"`
			Description string `json:"description"`
		}
		if json.Unmarshal(conversation, &input) != nil {
			continue
		}
		query := input.Subject + " " + input.Description + " " + summarySearchText(view.Success.Result)
		hash := contentHash(embedding.QueryVersion + "\n" + query)
		cached, _ := a.db.KnowledgeQuery(ctx, hash, embedding.ModelID)
		if len(cached) > 0 {
			continue
		}
		values, e := a.embeddings.Embed(ctx, query, true)
		if e == nil {
			_ = a.db.SaveKnowledgeQuery(ctx, hash, embedding.ModelID, values)
		}
	}
}

// ScopedHybrid performs no network or process operation and is safe for status reads.
func scopedHybrid(ctx context.Context, db *store.Store, department, exclude, product, query string) ([]knowledge.Source, error) {
	if department == "" {
		if state, err := db.State(ctx); err == nil {
			department = state.DepartmentID
		}
	}
	entries, e := db.ScopedKnowledge(ctx, 0, department, "published")
	if e != nil {
		return nil, e
	}
	vector, _ := db.KnowledgeQuery(ctx, contentHash(embedding.QueryVersion+"\n"+query), embedding.ModelID)
	vectors, _ := db.KnowledgeVectors(ctx, embedding.ModelID, knowledge.ChunkVersion)
	return knowledge.HybridSources(entries, vectors, vector, exclude, product, query, 20), nil
}
