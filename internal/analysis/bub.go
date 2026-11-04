// input: 共享脱敏 JSON、显式模型配置及固定 Python 环境。
// output: 经结构与消息引用校验的 Bub 总结 JSON 和实际模型标识。
// pos: 无 Shell 的 Python 子进程与 Go 结果验证边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"zoho-workstation/internal/config"
)

const BubSchema = "bub-summary-v1"

type fact struct {
	Text       string   `json:"text"`
	MessageIDs []string `json:"message_ids"`
}
type summary struct {
	CurrentSituation  *fact  `json:"current_situation"`
	KeyProgress       []fact `json:"key_progress"`
	AttemptedMeasures []struct {
		Action          string   `json:"action"`
		ExecutionStatus string   `json:"execution_status"`
		Result          string   `json:"result"`
		ResultStatus    string   `json:"result_status"`
		MessageIDs      []string `json:"message_ids"`
	} `json:"attempted_measures"`
	Blockers  []fact `json:"blockers"`
	NextSteps []struct {
		Text       string   `json:"text"`
		Basis      string   `json:"basis"`
		MessageIDs []string `json:"message_ids"`
	} `json:"next_steps"`
}

func ValidateBub(result, input json.RawMessage) error {
	var source struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if json.Unmarshal(input, &source) != nil {
		return errors.New("Bub source is invalid")
	}
	ids := map[string]bool{}
	for _, m := range source.Messages {
		ids[m.ID] = true
	}
	var output summary
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&output) != nil || output.CurrentSituation == nil || output.KeyProgress == nil || output.AttemptedMeasures == nil || output.Blockers == nil || output.NextSteps == nil || len(output.NextSteps) > 3 {
		return errors.New("Bub summary schema invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("Bub summary contains extra output")
	}
	refs := func(text string, references []string) bool {
		if strings.TrimSpace(text) == "" || references == nil || (len(ids) > 0 && len(references) == 0) {
			return false
		}
		for _, id := range references {
			if !ids[id] {
				return false
			}
		}
		return true
	}
	if !refs(output.CurrentSituation.Text, output.CurrentSituation.MessageIDs) {
		return errors.New("Bub current situation lacks valid evidence")
	}
	for _, f := range append(output.KeyProgress, output.Blockers...) {
		if !refs(f.Text, f.MessageIDs) {
			return errors.New("Bub fact lacks valid evidence")
		}
	}
	for _, m := range output.AttemptedMeasures {
		if !refs(m.Action, m.MessageIDs) || strings.TrimSpace(m.Result) == "" {
			return errors.New("Bub measure lacks valid evidence")
		}
		if m.ExecutionStatus != "suggested" && m.ExecutionStatus != "customer_confirmed_executed" && m.ExecutionStatus != "unknown" {
			return errors.New("Bub execution status invalid")
		}
		if m.ResultStatus != "customer_reported" && m.ResultStatus != "unknown" {
			return errors.New("Bub result status invalid")
		}
		if m.ResultStatus == "customer_reported" && m.ExecutionStatus != "customer_confirmed_executed" {
			return errors.New("Bub result cannot imply an unconfirmed action was executed")
		}
	}
	for _, f := range output.NextSteps {
		if !refs(f.Text, f.MessageIDs) || (f.Basis != "recommendation" && f.Basis != "hypothesis") {
			return errors.New("Bub recommendation invalid")
		}
	}
	return nil
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		return 0, errors.New("Python output exceeds limit")
	}
	return b.Buffer.Write(p)
}

func Bub(ctx context.Context, cfg config.Model, input json.RawMessage) (json.RawMessage, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	python, err := filepath.Abs("python/.venv/bin/python")
	if err != nil {
		return nil, "", errors.New("Python path unavailable")
	}
	entry, err := filepath.Abs("python/main.py")
	if err != nil {
		return nil, "", errors.New("Python entry unavailable")
	}
	body, err := json.Marshal(struct {
		Input         json.RawMessage   `json:"input"`
		Config        map[string]string `json:"config"`
		SchemaVersion string            `json:"schema_version"`
	}{input, map[string]string{"api_base": cfg.URL, "api_key": cfg.Key, "model": cfg.Name}, BubSchema})
	if err != nil {
		return nil, "", errors.New("Bub input encoding failed")
	}
	command := exec.CommandContext(ctx, python, entry)
	command.Env = []string{"PATH=/usr/bin:/bin", "PYTHONIOENCODING=utf-8", "PYTHONDONTWRITEBYTECODE=1"}
	command.Stdin = bytes.NewReader(body)
	var output limitedOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	processErr := command.Run()
	var envelope struct {
		OK            bool            `json:"ok"`
		Result        json.RawMessage `json:"result"`
		ReturnedModel string          `json:"returned_model"`
		RequestID     string          `json:"request_id"`
		Usage         json.RawMessage `json:"usage"`
		Error         string          `json:"error"`
		Diagnostics   []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if json.Unmarshal(output.Bytes(), &envelope) != nil {
		return nil, "", errors.New("Bub Python process failed or returned invalid JSON")
	}
	if !envelope.OK {
		diagnostic := ""
		if len(envelope.Diagnostics) > 0 {
			switch code := envelope.Diagnostics[0].Code; code {
			case "unknown_reference", "missing_reference", "reported_result_without_execution", "missing_customer_evidence", "missing_next_step", "output_truncated", "blank_output", "unexpected_completion_count", "incomplete_generation", "schema_validation", "empty_conversation_not_unknown", "empty_conversation_has_claims":
				diagnostic = " (" + code + ")"
			}
		}
		switch envelope.Error {
		case "invalid_input", "model_timeout", "model_request_failed", "invalid_summary", "summary_failed":
			return nil, envelope.ReturnedModel, errors.New("Bub: " + envelope.Error + diagnostic)
		default:
			return nil, "", errors.New("Bub did not return a successful structured result")
		}
	}
	if processErr != nil {
		return nil, "", errors.New("Bub Python process failed or timed out")
	}
	if err = ValidateBub(envelope.Result, input); err != nil {
		return nil, envelope.ReturnedModel, err
	}
	result, err := json.Marshal(struct {
		Summary   json.RawMessage `json:"summary"`
		Usage     json.RawMessage `json:"usage,omitempty"`
		RequestID string          `json:"request_id,omitempty"`
	}{envelope.Result, envelope.Usage, envelope.RequestID})
	return result, envelope.ReturnedModel, err
}
