// input: 固定的历史节点、知识版本、上一版回复/评估与显式模型配置。
// output: 独立离线模型调用、引用校验以及成功/失败请求的供应商元信息。
// pos: FAQ 实验的固定 Python 进程边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"unicode/utf8"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/lab"
	"zoho-workstation/internal/ticket"
)

type LabResult struct {
	Result        json.RawMessage `json:"result"`
	ReturnedModel string          `json:"returned_model"`
	RequestID     string          `json:"request_id"`
	Usage         json.RawMessage `json:"usage"`
}

func CheckLab() error {
	if info, err := os.Stat("python/lab.py"); err != nil || info.IsDir() {
		return errors.New("Bub lab entry unavailable; restore complete package")
	}
	return nil
}

func Lab(ctx context.Context, cfg config.Model, task string, input lab.Input) (LabResult, error) {
	if !slices.Contains([]string{"reply", "evaluate", "propose"}, task) || (task == "evaluate" && (input.Reply == nil || input.Evaluation != nil || len(input.Feedback) != 0)) {
		return LabResult{}, errors.New("Bub: invalid_input")
	}
	// A nil Go slice is an empty collection at this protocol boundary, never JSON null.
	input.Sources = RedactedSources(input.Sources)
	for i := range input.Sources {
		input.Sources[i].MessageIDs = append([]string{}, input.Sources[i].MessageIDs...)
		if input.Sources[i].Scope == "" {
			input.Sources[i].Scope = "reference"
		}
	}
	input.Feedback = append([]lab.Feedback{}, input.Feedback...)
	for i := range input.Feedback {
		input.Feedback[i].Tags = append([]string{}, input.Feedback[i].Tags...)
	}
	body, err := json.Marshal(map[string]any{"task": task, "input": input, "schema_version": lab.Schema,
		"config": map[string]string{"api_base": cfg.URL, "api_key": cfg.Key, "model": cfg.Name}})
	if err != nil || len(body) > 1024*1024 {
		return LabResult{}, errors.New("Bub: invalid_input")
	}
	var envelope struct {
		LabResult
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := pythonJSON(ctx, "python/lab.py", body, &envelope); err != nil {
		return LabResult{}, err
	}
	result := envelope.LabResult
	if !envelope.OK {
		result.Result = nil
		code := "lab_failed"
		switch envelope.Error {
		case "invalid_input", "invalid_lab_result", "model_timeout", "model_connection_failed", "model_upstream_failed", "model_request_failed", "proxy_dependency_missing", "proxy_configuration_invalid", "python_environment_invalid":
			code = envelope.Error
		}
		return result, errors.New("Bub: " + code)
	}
	if err := ValidateLab(task, result.Result, input); err != nil {
		result.Result = nil
		return result, err
	}
	return result, nil
}

// ValidateLab checks structure and available evidence, not semantic correctness.
// Independent model evaluation and human review remain necessary.
func ValidateLab(task string, result json.RawMessage, input lab.Input) error {
	invalid := errors.New("Bub: invalid_lab_result")
	var snapshot struct {
		Messages []ticket.Message `json:"messages"`
	}
	if json.Unmarshal(input.Ticket, &snapshot) != nil || len(result) > 128*1024 {
		return invalid
	}
	messages := map[string]bool{}
	for _, message := range snapshot.Messages {
		if _, exists := messages[message.ID]; exists || message.ID == "" {
			return invalid
		}
		messages[message.ID] = message.Visibility == "public" && message.Source != "comment" &&
			!slices.Contains([]string{"DRAFT", "FAILED", "PENDING"}, strings.ToUpper(message.Status))
	}
	sources := map[string]knowledge.Source{}
	for _, source := range input.Sources {
		if _, exists := sources[source.ID]; exists || source.ID == "" {
			return invalid
		}
		sources[source.ID] = source
	}
	refs := func(messageIDs, sourceIDs []string) bool {
		if messageIDs == nil || sourceIDs == nil || len(messageIDs) > 60 || len(sourceIDs) > 24 {
			return false
		}
		for _, id := range messageIDs {
			if !messages[id] {
				return false
			}
		}
		for _, id := range sourceIDs {
			if _, exists := sources[id]; !exists {
				return false
			}
		}
		return true
	}
	text := func(value string, limit int) bool {
		return strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= limit
	}
	texts := func(values []string, limit int) bool {
		if values == nil || len(values) > limit {
			return false
		}
		for _, value := range values {
			if !text(value, 4000) {
				return false
			}
		}
		return true
	}
	switch task {
	case "reply":
		var reply lab.Reply
		if strictDecode(result, &reply) != nil || !text(reply.Text, 6000) || !text(reply.Language, 64) || !text(reply.Rationale, 2000) ||
			!texts(reply.MissingInformation, 8) || len(reply.MessageIDs) == 0 || !refs(reply.MessageIDs, reply.SourceIDs) {
			return invalid
		}
	case "evaluate":
		var evaluation lab.Evaluation
		if strictDecode(result, &evaluation) != nil || !slices.Contains([]string{"pass", "revise", "unsafe"}, evaluation.Verdict) ||
			evaluation.Score < 1 || evaluation.Score > 5 || evaluation.Findings == nil || len(evaluation.Findings) > 12 || !texts(evaluation.Suggestions, 8) {
			return invalid
		}
		for _, finding := range evaluation.Findings {
			if !slices.Contains([]string{"facts", "product", "coverage", "failed_step", "policy", "evidence", "language", "clarity"}, finding.Code) ||
				!slices.Contains([]string{"info", "warning", "blocker"}, finding.Severity) || !text(finding.Reason, 1000) ||
				!refs(finding.MessageIDs, finding.SourceIDs) || (evaluation.Verdict == "pass" && finding.Severity != "info") {
				return invalid
			}
		}
	case "propose":
		var proposals lab.Proposals
		if strictDecode(result, &proposals) != nil || proposals.Items == nil || len(proposals.Items) > 3 {
			return invalid
		}
		for _, item := range proposals.Items {
			if !text(item.Title, 256) || utf8.RuneCountInString(item.Product) > 128 || !text(item.Question, 1000) || !text(item.Answer, 4000) ||
				!text(item.Conditions, 2000) || !text(item.Reason, 1500) || item.Scope != "technical" || item.ReplacesID < 0 ||
				!refs(item.MessageIDs, item.SourceIDs) || len(item.MessageIDs)+len(item.SourceIDs) == 0 || len(lab.ProposalText(item)) > 12*1024 {
				return invalid
			}
			allowedReplacement := item.ReplacesID == 0
			for _, source := range input.Sources {
				allowedReplacement = allowedReplacement || (source.EntryID == item.ReplacesID && source.Scope == "technical" && source.Kind == "sop")
			}
			if !allowedReplacement {
				return invalid
			}
			for _, ref := range item.SourceIDs {
				if sources[ref].Scope == "policy" {
					return invalid
				}
			}
		}
	default:
		return invalid
	}
	return nil
}
