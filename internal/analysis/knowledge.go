// input: 脱敏工单、当前事实总结、已检索知识及显式模型配置。
// output: 独立案例/建议输入、无工具 Python 调用和引用/角色验证。
// pos: 知识提取与内部建议的模型边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/ticket"
)

const CaseSchema = "bub-case-v1"
const AdviceSchema = "bub-advice-v1"

type CaseStep struct {
	Action     string   `json:"action"`
	Execution  string   `json:"execution"`
	Outcome    string   `json:"outcome"`
	MessageIDs []string `json:"message_ids"`
}
type Case struct {
	Title      string     `json:"title"`
	Product    string     `json:"product"`
	Issue      string     `json:"issue"`
	Steps      []CaseStep `json:"steps"`
	MessageIDs []string   `json:"message_ids"`
}
type AdviceStep struct {
	Action            string   `json:"action"`
	Reason            string   `json:"reason"`
	SourceIDs         []string `json:"source_ids"`
	CurrentMessageIDs []string `json:"current_message_ids"`
}
type Advice struct {
	Steps              []AdviceStep `json:"steps"`
	MissingInformation []string     `json:"missing_information"`
	Conflicts          []string     `json:"conflicts"`
}
type KnowledgeInput struct {
	Ticket         json.RawMessage    `json:"ticket"`
	Summary        json.RawMessage    `json:"summary"`
	Sources        []knowledge.Source `json:"sources,omitempty"`
	SummaryVersion string             `json:"summary_version"`
}

func KnowledgeInputBytes(conversation, summaryResult json.RawMessage, version string, sources []knowledge.Source) (json.RawMessage, string, error) {
	var saved struct {
		Summary json.RawMessage `json:"summary"`
	}
	if json.Unmarshal(summaryResult, &saved) != nil || len(saved.Summary) == 0 {
		return nil, "", errors.New("current factual summary is unavailable")
	}
	sources = RedactedSources(sources)
	body, err := json.Marshal(KnowledgeInput{conversation, saved.Summary, sources, version})
	digest := sha256.Sum256(body)
	hash := hex.EncodeToString(digest[:])
	if len(body) > 768*1024 {
		return nil, hash, errors.New("knowledge input too large; no content was truncated")
	}
	return body, hash, err
}

func CheckKnowledge() error {
	if info, err := os.Stat("python/knowledge.py"); err != nil || info.IsDir() {
		return errors.New("Bub knowledge entry unavailable; restore complete package")
	}
	return nil
}

func Knowledge(ctx context.Context, cfg config.Model, kind string, input json.RawMessage) (json.RawMessage, string, error) {
	schema := CaseSchema
	if kind == "advice" {
		schema = AdviceSchema
	} else if kind != "case" {
		return nil, "", errors.New("unknown knowledge task")
	}
	body, _ := json.Marshal(map[string]any{"task": kind, "input": input, "schema_version": schema, "config": map[string]string{"api_base": cfg.URL, "api_key": cfg.Key, "model": cfg.Name}})
	var envelope struct {
		OK            bool            `json:"ok"`
		Result        json.RawMessage `json:"result"`
		ReturnedModel string          `json:"returned_model"`
		Error         string          `json:"error"`
		Usage         json.RawMessage `json:"usage"`
		RequestID     string          `json:"request_id"`
	}
	if err := pythonJSON(ctx, "python/knowledge.py", body, &envelope); err != nil {
		return nil, "", err
	}
	if !envelope.OK {
		code := "knowledge_failed"
		switch envelope.Error {
		case "invalid_input", "invalid_knowledge", "model_timeout", "model_connection_failed", "model_upstream_failed", "model_request_failed", "proxy_dependency_missing", "proxy_configuration_invalid", "python_environment_invalid":
			code = envelope.Error
		}
		return nil, envelope.ReturnedModel, errors.New("Bub: " + code)
	}
	if err := ValidateKnowledge(kind, envelope.Result, input); err != nil {
		return nil, envelope.ReturnedModel, err
	}
	var in KnowledgeInput
	_ = json.Unmarshal(input, &in)
	result := map[string]any{kind: envelope.Result, "usage": envelope.Usage, "request_id": envelope.RequestID}
	if kind == "advice" {
		result["sources"] = in.Sources
	}
	output, err := json.Marshal(result)
	return output, envelope.ReturnedModel, err
}

// Only fixed local entries can be passed here; callers never accept a path from HTTP.
func pythonJSON(ctx context.Context, entry string, body []byte, target any) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command, err := pythonCommand(ctx, entry)
	if err != nil {
		return err
	}
	command.Stdin = bytes.NewReader(body)
	var output limitedOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	runErr := command.Run()
	if ctx.Err() != nil {
		return errors.New("local Python task timed out or was cancelled")
	}
	if json.Unmarshal(output.Bytes(), target) != nil {
		return errors.New("local Python task returned invalid JSON")
	}
	if runErr != nil {
		var failure struct {
			OK *bool `json:"ok"`
		}
		_ = json.Unmarshal(output.Bytes(), &failure)
		if failure.OK == nil || *failure.OK {
			return errors.New("local Python task failed")
		}
	}
	return nil
}

func strictDecode(body []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errors.New("Bub knowledge schema invalid")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("Bub knowledge contains extra output")
	}
	return nil
}
func usefulText(text string) bool { return strings.TrimSpace(text) != "" && len(text) <= 16000 }

func ValidateKnowledge(kind string, result, input json.RawMessage) error {
	var in KnowledgeInput
	var snapshot struct {
		Messages []ticket.Message `json:"messages"`
	}
	if json.Unmarshal(input, &in) != nil || json.Unmarshal(in.Ticket, &snapshot) != nil {
		return errors.New("knowledge input invalid")
	}
	messages := map[string]ticket.Message{}
	for _, m := range snapshot.Messages {
		if _, exists := messages[m.ID]; exists {
			return errors.New("ambiguous message references")
		}
		messages[m.ID] = m
	}
	references := func(ids []string, required bool) (bool, bool, error) {
		if required && len(ids) == 0 {
			return false, false, errors.New("knowledge evidence missing")
		}
		customer, support := false, false
		for _, id := range ids {
			m, ok := messages[id]
			if !ok || m.Visibility != "public" || m.Source == "comment" || strings.EqualFold(m.Status, "DRAFT") || strings.EqualFold(m.Status, "FAILED") || strings.EqualFold(m.Status, "PENDING") {
				return false, false, errors.New("knowledge references unavailable or private evidence")
			}
			customer = customer || (m.Role == "customer" && !m.MixedAuthors)
			support = support || m.Role == "agent"
		}
		return customer, support, nil
	}
	if kind == "case" {
		var c Case
		if err := strictDecode(result, &c); err != nil {
			return err
		}
		if !usefulText(c.Title) || len(c.Title) > 1024 || !usefulText(c.Issue) || len(CaseText(c)) > 16*1024 || c.Steps == nil || c.MessageIDs == nil || len(c.Steps) > 10 {
			return errors.New("case content invalid")
		}
		if !caseProductSupported(c, in.Ticket) {
			return errors.New("case product lacks literal evidence")
		}
		if _, _, err := references(c.MessageIDs, true); err != nil {
			return err
		}
		for _, step := range c.Steps {
			customer, support, err := references(step.MessageIDs, true)
			if err != nil {
				return err
			}
			if !usefulText(step.Action) || !support {
				return errors.New("case action requires support evidence")
			}
			if step.Execution != "confirmed" && step.Execution != "unknown" {
				return errors.New("case execution invalid")
			}
			if step.Outcome != "resolved" && step.Outcome != "unresolved" && step.Outcome != "unknown" {
				return errors.New("case outcome invalid")
			}
			if (step.Execution == "confirmed" || step.Outcome != "unknown") && !customer {
				return errors.New("case outcome requires customer confirmation")
			}
			if step.Outcome != "unknown" && step.Execution != "confirmed" {
				return errors.New("case outcome requires confirmed execution")
			}
		}
		return nil
	}
	if kind != "advice" {
		return errors.New("unknown knowledge task")
	}
	var advice Advice
	if err := strictDecode(result, &advice); err != nil {
		return err
	}
	if advice.Steps == nil || advice.MissingInformation == nil || advice.Conflicts == nil || len(advice.Steps) > 3 || len(advice.MissingInformation) > 10 || len(advice.Conflicts) > 10 {
		return errors.New("advice content invalid")
	}
	sources := map[string]bool{}
	for _, s := range in.Sources {
		sources[s.ID] = true
	}
	for _, step := range advice.Steps {
		if !usefulText(step.Action) || !usefulText(step.Reason) || len(step.SourceIDs) == 0 || step.CurrentMessageIDs == nil {
			return errors.New("advice lacks source")
		}
		for _, id := range step.SourceIDs {
			if !sources[id] {
				return errors.New("advice references unknown knowledge")
			}
		}
		if _, _, err := references(step.CurrentMessageIDs, false); err != nil {
			return err
		}
	}
	for _, text := range append(advice.MissingInformation, advice.Conflicts...) {
		if !usefulText(text) {
			return errors.New("advice text invalid")
		}
	}
	return nil
}

// CaseText preserves uncertainty in the searchable evidence card and SOP drafts.
func CaseText(c Case) string {
	var b strings.Builder
	fmt.Fprintf(&b, "问题：%s\n", c.Issue)
	for i, step := range c.Steps {
		execution := map[string]string{"confirmed": "客户确认执行", "unknown": "是否执行未知"}[step.Execution]
		outcome := map[string]string{"resolved": "客户确认解决", "unresolved": "客户反馈未解决", "unknown": "效果未知"}[step.Outcome]
		fmt.Fprintf(&b, "\n%d. 客服建议：%s\n执行：%s；结果：%s\n证据消息：%s\n", i+1, step.Action, execution, outcome, strings.Join(step.MessageIDs, ", "))
	}
	if len(c.Steps) == 0 {
		b.WriteString("\n暂无有证据的客服排查步骤；效果未知。\n")
	}
	return b.String()
}

// A model label is usable for matching only when present verbatim in its cited evidence.
func caseProductSupported(c Case, body json.RawMessage) bool {
	if c.Product == "" {
		return true
	}
	if len(c.Product) > 256 {
		return false
	}
	var snapshot struct {
		Product  string           `json:"product"`
		Messages []ticket.Message `json:"messages"`
	}
	if json.Unmarshal(body, &snapshot) != nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.Product), c.Product) {
		return true
	}
	ids := map[string]bool{}
	for _, id := range c.MessageIDs {
		ids[id] = true
	}
	for _, step := range c.Steps {
		for _, id := range step.MessageIDs {
			ids[id] = true
		}
	}
	needle := strings.ToLower(c.Product)
	for _, m := range snapshot.Messages {
		if !ids[m.ID] {
			continue
		}
		text := strings.ToLower(m.Body)
		for start := 0; start < len(text); {
			offset := strings.Index(text[start:], needle)
			if offset < 0 {
				break
			}
			i := start + offset
			end := i + len(needle)
			left, right := rune(0), rune(0)
			if i > 0 {
				left, _ = utf8.DecodeLastRuneInString(text[:i])
			}
			if end < len(text) {
				right, _ = utf8.DecodeRuneInString(text[end:])
			}
			if !unicode.IsLetter(left) && !unicode.IsDigit(left) && !unicode.IsLetter(right) && !unicode.IsDigit(right) {
				return true
			}
			start = end
		}
	}
	return false
}

// RedactedSources keeps canonical knowledge local while minimizing model input.
func RedactedSources(sources []knowledge.Source) []knowledge.Source {
	out := append([]knowledge.Source{}, sources...)
	for i := range out {
		clean := ticket.RedactedSnapshot(ticket.Snapshot{Subject: out[i].Title, Description: out[i].Text, Product: out[i].Product}, ticket.RawSnapshot{})
		out[i].Title = clean.Subject
		out[i].Text = clean.Description
		out[i].Product = clean.Product
	}
	return out
}
