// input: 当前完整工单、成功事实总结、本地案例与经确认发布的 SOP。
// output: 独立知识任务输入、来源版本检查和自动案例持久化。
// pos: 事实总结之外的知识/建议编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/store"
)

var errNoKnowledge = errors.New("暂无适用的已发布 SOP 或当前有效历史案例；有来源后将自动生成建议")

func knowledgeRequest(ctx context.Context, db *store.Store, cfg config.Config, record store.Record, kind, round string, conversation json.RawMessage, bubReq store.AnalysisRequest) (store.AnalysisRequest, json.RawMessage, error) {
	schema := analysis.CaseSchema
	if kind == "advice" {
		schema = analysis.AdviceSchema
	}
	req := store.AnalysisRequest{TicketID: record.ID, Kind: kind, InputHash: "awaiting-bub:" + bubReq.InputHash, SchemaVersion: schema, RequestedModel: cfg.Bub.Name, Round: round}
	view, err := db.Analysis(ctx, bubReq)
	if err != nil {
		return req, nil, err
	}
	if view.Success == nil || view.Stale {
		return req, nil, errAwaitingBub
	}
	summary := view.Success
	var sources []knowledge.Source
	if kind == "advice" {
		sources, err = relevantKnowledge(ctx, db, cfg, record, conversation, summary.Result)
		if err != nil {
			return req, nil, err
		}
	}
	input, hash, err := analysis.KnowledgeInputBytes(conversation, summary.Result, summary.SchemaVersion+"|"+summary.RequestedModel+"|"+summary.ReturnedModel, sources)
	req.InputHash = hash
	if err == nil && kind == "advice" && len(sources) == 0 {
		return req, nil, errNoKnowledge
	}
	return req, input, err
}

func relevantKnowledge(ctx context.Context, db *store.Store, cfg config.Config, record store.Record, conversation, summary json.RawMessage) ([]knowledge.Source, error) {
	var snapshot struct {
		Subject     string `json:"subject"`
		Product     string `json:"product"`
		Description string `json:"description"`
	}
	if json.Unmarshal(conversation, &snapshot) != nil {
		return nil, errors.New("knowledge search input invalid")
	}
	// A verified model from the current case can fill an absent Zoho product field.
	caseReq, _, caseErr := requestForRecord(ctx, db, cfg, record, "case", DevelopmentRound)
	if caseErr == nil && unknownProduct(snapshot.Product) {
		if view, e := db.Analysis(ctx, caseReq); e == nil && view.Success != nil && !view.Stale {
			var saved struct {
				Case analysis.Case `json:"case"`
			}
			if json.Unmarshal(view.Success.Result, &saved) == nil {
				snapshot.Product = saved.Case.Product
			}
		}
	}
	sources, err := db.KnowledgeSources(ctx, record.ID, snapshot.Product, snapshot.Subject+" "+snapshot.Description+" "+summarySearchText(summary), 20)
	if err != nil {
		return nil, err
	}
	selected := []knowledge.Source{}
	for _, source := range sources {
		if source.Kind == "case" {
			origin, err := db.Get(ctx, source.TicketID)
			if err != nil || origin.Pending || !origin.Complete || origin.SyncStopped {
				continue
			}
			req, _, err := requestForRecord(ctx, db, cfg, origin, "case", DevelopmentRound)
			if err != nil {
				continue
			}
			view, e := db.Analysis(ctx, req)
			if e != nil || view.Success == nil || view.Stale || caseSignature(req, view.Success.Result) != source.SourceHash {
				continue
			}
		}
		selected = append(selected, source)
		if len(selected) == 5 {
			break
		}
	}
	return selected, nil
}

func (a *App) saveCase(ctx context.Context, req store.AnalysisRequest, result json.RawMessage) error {
	current, input, err := analysisRequest(ctx, a.db, a.cfg, req.TicketID, "case", req.Round)
	if err != nil {
		return err
	}
	if current.InputHash != req.InputHash || current.SchemaVersion != req.SchemaVersion || current.RequestedModel != req.RequestedModel {
		return nil
	}
	var saved struct {
		Case analysis.Case `json:"case"`
	}
	if json.Unmarshal(result, &saved) != nil {
		return errors.New("saved case invalid")
	}
	var in analysis.KnowledgeInput
	_ = json.Unmarshal(input, &in)
	var snapshot struct {
		Product string `json:"product"`
	}
	_ = json.Unmarshal(in.Ticket, &snapshot)
	if unknownProduct(snapshot.Product) {
		snapshot.Product = saved.Case.Product
	}
	ids := append([]string{}, saved.Case.MessageIDs...)
	seen := map[string]bool{}
	for _, step := range saved.Case.Steps {
		ids = append(ids, step.MessageIDs...)
	}
	unique := []string{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	_, err = a.db.SaveKnowledge(ctx, knowledge.Entry{Kind: "case", Title: saved.Case.Title, Product: snapshot.Product, SourceName: "工单案例", SourceTicketID: req.TicketID, SourceHash: caseSignature(req, result), MessageIDs: unique, Blocks: []knowledge.Block{{ID: "case", Text: analysis.CaseText(saved.Case)}}})
	return err
}

func (a *App) knowledgeView(ctx context.Context, record store.Record, kind string) (analysisView, error) {
	req, _, err := requestForRecord(ctx, a.db, a.cfg, record, kind, DevelopmentRound)
	waiting := errors.Is(err, errAwaitingBub) || errors.Is(err, errNoKnowledge)
	if err != nil && !waiting && req.InputHash == "" {
		return analysisView{}, err
	}
	view, e := a.db.Analysis(ctx, req)
	if e != nil {
		return analysisView{}, e
	}
	out := analysisView{AnalysisView: view}
	if waiting {
		out.WaitingFor = "bub"
		if errors.Is(err, errNoKnowledge) {
			out.WaitingFor = "knowledge"
		}
		out.WaitingReason = err.Error()
	}
	return out, nil
}

func knowledgeDraftBody(entry knowledge.Entry) string {
	var body strings.Builder
	fmt.Fprintf(&body, "适用产品：%s\n\n", entry.Product)
	body.WriteString("发布前请确认适用条件、步骤顺序、注意事项和结束条件。历史案例效果不自动代表普遍有效。\n\n")
	for _, block := range entry.Blocks {
		body.WriteString(block.Text)
		body.WriteString("\n\n")
	}
	return body.String()
}

func caseSignature(req store.AnalysisRequest, result json.RawMessage) string {
	var saved struct {
		Case analysis.Case `json:"case"`
	}
	_ = json.Unmarshal(result, &saved)
	value, _ := json.Marshal(struct {
		Input, Schema, Model string
		Case                 analysis.Case
	}{req.InputHash, req.SchemaVersion, req.RequestedModel, saved.Case})
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
func unknownProduct(product string) bool {
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "", "unknown", "others", "other", "未知", "未提供", "未确认", "其他":
		return true
	}
	return false
}

func summarySearchText(result json.RawMessage) string {
	// Explicit JSON keys avoid introducing schema names and IDs as retrieval terms.
	var envelope struct {
		Summary map[string][]struct {
			Text string `json:"text"`
		} `json:"summary"`
	}
	if json.Unmarshal(result, &envelope) != nil {
		return ""
	}
	var text strings.Builder
	for _, key := range []string{"device_info", "customer_feedback", "support_responses"} {
		for _, fact := range envelope.Summary[key] {
			text.WriteString(fact.Text)
			text.WriteByte(' ')
		}
	}
	return text.String()
}
