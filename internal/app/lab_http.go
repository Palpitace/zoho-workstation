// input: 同源受保护的批次选择、执行、逐版本人工反馈与草稿确认请求。
// output: 固定离线快照、可审核结果及不会自动发布的 FAQ 修订。
// pos: 离线实验 HTTP 边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/lab"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
)

func (a *App) labRoutes(r *gin.Engine) {
	r.GET("/api/lab/batches", func(c *gin.Context) {
		b, err := a.db.ListLabBatches(c.Request.Context())
		if err != nil {
			apiError(c, 500, "无法读取离线批次")
			return
		}
		c.JSON(200, gin.H{"data": b, "enabled": a.cfg.Bub.Enabled})
	})
	r.GET("/api/lab/candidates", a.labCandidates)
	r.POST("/api/lab/batches", a.createLabBatch)
	r.GET("/api/lab/batches/:id", func(c *gin.Context) {
		b, ok := a.labBatch(c)
		if !ok {
			return
		}
		if c.Query("revision") == strconv.FormatInt(b.Revision, 10) {
			c.JSON(200, gin.H{"unchanged": true})
			return
		}
		c.JSON(200, gin.H{"batch": b, "candidates": lab.Candidates(b), "validation_complete": lab.ValidationComplete(b)})
	})
	r.POST("/api/lab/batches/:id/start", a.startLabBatch)
	r.POST("/api/lab/batches/:id/pause", func(c *gin.Context) {
		b, ok := a.labBatch(c)
		if !ok {
			return
		}
		_, err := a.db.MutateLabBatch(c.Request.Context(), b.ID, func(s *lab.State) error {
			if s.Status != "running" {
				return store.ErrLabConflict
			}
			s.Status = "paused"
			return nil
		})
		if err != nil {
			apiError(c, 409, "批次当前不能暂停")
			return
		}
		c.JSON(200, gin.H{"status": "paused"})
	})
	r.POST("/api/lab/batches/:id/feedback", a.labFeedback)
	r.POST("/api/lab/batches/:id/drafts", a.labDraft)
}

func (a *App) labBatch(c *gin.Context) (lab.Batch, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, "批次 ID 无效")
		return lab.Batch{}, false
	}
	b, err := a.db.LabBatch(c.Request.Context(), id)
	if err != nil {
		apiError(c, 404, "批次不存在")
		return b, false
	}
	return b, true
}

type labSelection struct {
	ID      string `json:"id"`
	Number  string `json:"ticket_number"`
	Subject string `json:"subject"`
	Created string `json:"created_time"`
	Nodes   int    `json:"nodes"`
	Reason  string `json:"reason,omitempty"`
}

func (a *App) labRange(from, until string) (time.Time, time.Time, error) {
	start := a.cfg.SyncStartTime
	end := time.Now().UTC()
	var err error
	if from != "" {
		start, err = time.Parse(time.RFC3339, from)
		if err != nil {
			return start, end, errors.New("日期格式无效")
		}
	}
	if until != "" {
		end, err = time.Parse(time.RFC3339, until)
		if err != nil {
			return start, end, errors.New("日期格式无效")
		}
	}
	if start.Before(a.cfg.SyncStartTime) || !end.After(start) {
		return start, end, errors.New("范围不得早于已授权同步起点，结束时间必须晚于开始时间")
	}
	return start, end, nil
}

func (a *App) labCandidates(c *gin.Context) {
	start, end, err := a.labRange(c.Query("from"), c.Query("until"))
	if err != nil {
		apiError(c, 400, err.Error())
		return
	}
	records, err := a.db.List(c.Request.Context(), "", "")
	if err != nil {
		apiError(c, 500, "无法读取本地工单")
		return
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedTime > records[j].CreatedTime })
	result := []labSelection{}
	for _, r := range records {
		created, e := time.Parse(time.RFC3339Nano, r.CreatedTime)
		if e != nil || created.Before(start) || !created.Before(end) || r.Pending || r.SyncStopped || !r.Complete {
			continue
		}
		record, e := a.db.Get(c.Request.Context(), r.ID)
		if e != nil {
			continue
		}
		sample, e := lab.PrepareSample(ticket.RedactedSnapshot(record.Snapshot, record.Raw), displayTicket(record, nil).TicketNumber)
		selection := labSelection{ID: r.ID, Number: sample.TicketNumber, Subject: sample.Subject, Created: r.CreatedTime, Nodes: len(sample.Nodes)}
		if e != nil {
			selection.Reason = e.Error()
		}
		result = append(result, selection)
		if len(result) == lab.MaxTickets {
			break
		}
	}
	c.JSON(200, gin.H{"data": result, "from": start.Format(time.RFC3339), "until": end.Format(time.RFC3339), "limit": lab.MaxTickets})
}

func (a *App) createLabBatch(c *gin.Context) {
	var body struct {
		Groups       map[string]string `json:"groups"`
		IDs          []string          `json:"ticket_ids"`
		KnowledgeIDs []int64           `json:"knowledge_ids"`
		From         string            `json:"from"`
		Until        string            `json:"until"`
		ParentID     int64             `json:"parent_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	if c.ShouldBindJSON(&body) != nil {
		apiError(c, 400, "批次格式无效")
		return
	}
	spec := lab.Spec{Schema: lab.Schema, Model: a.cfg.Bub.Name, ModelURLHash: lab.Hash(a.cfg.Bub.URL), Feedback: []lab.Feedback{}}
	if body.ParentID > 0 {
		parent, err := a.db.LabBatch(c.Request.Context(), body.ParentID)
		if err != nil || (parent.State.Status != "completed" && parent.State.Status != "paused") {
			apiError(c, 409, "上一批须已完成或暂停，才能创建新一轮")
			return
		}
		spec = parent.Spec
		spec.ParentID = parent.ID
		spec.Feedback = lab.DevelopmentFeedback(parent)
		spec.Schema = lab.Schema
		spec.Model = a.cfg.Bub.Name
		spec.ModelURLHash = lab.Hash(a.cfg.Bub.URL)
	} else {
		start, end, err := a.labRange(body.From, body.Until)
		if err != nil {
			apiError(c, 400, err.Error())
			return
		}
		if len(body.IDs) < 5 || len(body.IDs) > lab.MaxTickets {
			apiError(c, 400, "请选择 5～50 张已完整同步的工单")
			return
		}
		spec.StartTime = start.Format(time.RFC3339)
		spec.EndTime = end.Format(time.RFC3339)
		seen := map[string]bool{}
		for _, id := range body.IDs {
			record, err := a.db.Get(c.Request.Context(), id)
			created, e := time.Parse(time.RFC3339Nano, record.CreatedTime)
			if err != nil || e != nil || record.Pending || record.SyncStopped || !record.Complete || created.Before(start) || !created.Before(end) || seen[id] {
				apiError(c, 409, "选中的工单已失效、重复或不在所选范围，请刷新")
				return
			}
			seen[id] = true
			sample, err := lab.PrepareSample(ticket.RedactedSnapshot(record.Snapshot, record.Raw), displayTicket(record, nil).TicketNumber)
			if err != nil {
				apiError(c, 422, "工单 "+id+"："+err.Error())
				return
			}
			spec.Samples = append(spec.Samples, sample)
		}
		if err := lab.LinkGroups(spec.Samples, body.Groups); err != nil {
			apiError(c, 400, err.Error())
			return
		}
		if err := lab.Split(spec.Samples); err != nil {
			apiError(c, 422, err.Error())
			return
		}
		entries := []knowledge.Entry{}
		if len(body.KnowledgeIDs) > 100 {
			apiError(c, 400, "一次最多选择 100 份知识版本")
			return
		}
		unique := map[int64]bool{}
		for _, id := range body.KnowledgeIDs {
			entry, err := a.db.Knowledge(c.Request.Context(), id)
			if err != nil || entry.Kind != "sop" || entry.Status != "published" || unique[id] {
				apiError(c, 409, "请选择不重复的已发布 FAQ/SOP 版本")
				return
			}
			unique[id] = true
			entries = append(entries, entry)
		}
		spec.Knowledge = lab.Baseline(entries, spec.Samples)
		if len(spec.Knowledge) != len(entries) {
			apiError(c, 409, "所选知识源于本批工单，会泄漏后续结果；请取消该知识版本")
			return
		}
	}
	if strings.TrimSpace(spec.Model) == "" {
		apiError(c, 409, "请先配置 Bub 模型")
		return
	}
	batch, err := a.db.CreateLabBatch(c.Request.Context(), spec)
	if err != nil {
		apiError(c, 422, "批次未保存，输入过大或格式无效")
		return
	}
	c.JSON(201, batch)
}

func (a *App) startLabBatch(c *gin.Context) {
	batch, ok := a.labBatch(c)
	if !ok {
		return
	}
	if !a.cfg.Bub.Enabled {
		apiError(c, 409, "Bub 尚未启用")
		return
	}
	if analysis.CheckPython(c.Request.Context()) != nil || analysis.CheckLab() != nil {
		apiError(c, 503, "Bub Python 环境或离线入口不可用")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started || a.closed {
		apiError(c, 409, "服务尚未开始运行")
		return
	}
	if batch.Spec.Schema != lab.Schema || batch.Spec.Model != a.cfg.Bub.Name || batch.Spec.ModelURLHash != lab.Hash(a.cfg.Bub.URL) {
		apiError(c, 409, "模型配置或协议已变化，请创建新批次")
		return
	}
	batches, err := a.db.ListLabBatches(c.Request.Context())
	if err != nil {
		apiError(c, 500, "无法读取批次")
		return
	}
	for _, b := range batches {
		if b.State.Status == "running" {
			apiError(c, 409, "已有批次正在运行")
			return
		}
	}
	_, err = a.db.MutateLabBatch(c.Request.Context(), batch.ID, func(s *lab.State) error {
		if s.Status != "draft" && s.Status != "paused" {
			return store.ErrLabConflict
		}
		s.Status = "running"
		s.Error = ""
		return nil
	})
	if err != nil {
		apiError(c, 409, "此批次不能启动")
		return
	}
	select {
	case a.analysisWake <- struct{}{}:
	default:
	}
	c.JSON(202, gin.H{"status": "running"})
}

func (a *App) labFeedback(c *gin.Context) {
	batch, ok := a.labBatch(c)
	if !ok {
		return
	}
	var f lab.Feedback
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 48*1024)
	if c.ShouldBindJSON(&f) != nil || f.Score < 1 || f.Score > 5 || utf8.RuneCountInString(f.Note) > 4000 || utf8.RuneCountInString(f.EditedReply) > 6000 || len(f.Tags) > 8 {
		apiError(c, 400, "请填写 1～5 分，建议与改写请保持简短")
		return
	}
	allowed := map[string]bool{"事实错误": true, "机型不符": true, "遗漏诉求": true, "重复失败操作": true, "越权承诺": true, "表达问题": true}
	for _, tag := range f.Tags {
		if !allowed[tag] {
			apiError(c, 400, "问题标签无效")
			return
		}
	}
	f.Note = redactDocumentText(strings.TrimSpace(f.Note))
	f.EditedReply = redactDocumentText(strings.TrimSpace(f.EditedReply))
	_, err := a.db.MutateLabBatch(c.Request.Context(), batch.ID, func(s *lab.State) error {
		if f.CallID < 1 || f.CallID > len(s.Calls) {
			return store.ErrLabConflict
		}
		call := s.Calls[f.CallID-1]
		if call.Task != "reply" || call.Status != "success" {
			return store.ErrLabConflict
		}
		var rated lab.Reply
		if json.Unmarshal(call.Result, &rated) != nil {
			return store.ErrLabConflict
		}
		f.RatedReply = &rated
		f.ID = len(s.Feedback) + 1
		f.BatchID = batch.ID
		f.NodeID = call.NodeID
		f.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.Feedback = append(s.Feedback, f)
		return nil
	})
	if err != nil {
		apiError(c, 409, "回复版本已失效，评分未保存")
		return
	}
	c.JSON(201, f)
}

func (a *App) labDraft(c *gin.Context) {
	batch, ok := a.labBatch(c)
	if !ok {
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	if c.ShouldBindJSON(&body) != nil || !lab.ValidationComplete(batch) {
		apiError(c, 409, "留出对比尚未完整成功，请完成有效对比后再建立正式修订草稿")
		return
	}
	if id := batch.State.Drafts[body.Key]; id != 0 {
		entry, err := a.db.Knowledge(c.Request.Context(), id)
		if err == nil {
			c.JSON(200, entry)
			return
		}
	}
	for _, candidate := range lab.Candidates(batch) {
		if candidate.Key == body.Key && candidate.Active {
			entry := candidate.Entry
			entry.ID = 0
			entry.Status = "draft"
			entry.ReplacesID = candidate.Proposal.ReplacesID
			entry.SourceName = fmt.Sprintf("离线批次 #%d · 候选 %s", batch.ID, body.Key)
			entry.SourceHash = lab.Hash([]any{batch.ID, body.Key, candidate.Proposal})
			if entry.ReplacesID != 0 {
				origin, err := a.db.Knowledge(c.Request.Context(), entry.ReplacesID)
				if err != nil || origin.Scope != "technical" || origin.Status != "published" {
					apiError(c, 409, "被修订版本已变化或不属于技术知识")
					return
				}
			}
			// Every candidate depends on the learning/development corpus, even when it
			// cites a prior experimental candidate. Preserve the whole training lineage.
			for _, origin := range batch.Spec.Knowledge {
				entry.SourceTickets = append(entry.SourceTickets, origin.SourceTickets...)
				if origin.SourceTicketID != "" {
					entry.SourceTickets = append(entry.SourceTickets, origin.SourceTicketID)
				}
			}
			for _, s := range batch.Spec.Samples {
				if s.Split != "holdout" {
					entry.SourceTickets = append(entry.SourceTickets, s.TicketID)
				}
			}
			saved, err := a.db.SaveKnowledge(c.Request.Context(), entry)
			if err != nil {
				apiError(c, 409, "无法保存修订草稿，请检查被替代版本")
				return
			}
			_, err = a.db.MutateLabBatch(c.Request.Context(), batch.ID, func(s *lab.State) error { s.Drafts[body.Key] = saved.ID; return nil })
			if err != nil {
				apiError(c, 500, "草稿已保存，批次关联暂未保存；可重试")
				return
			}
			c.JSON(201, saved)
			return
		}
	}
	apiError(c, 404, "修订候选不存在")
}
