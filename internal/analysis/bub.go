// input: 完整脱敏会话及可选 Jev 参考、显式配置、固定 Python 环境和代理白名单。
// output: 三类事实和未知作者入站原文摘录的校验与模型标识，或安全错误。
// pos: 无 Shell 的 Python 子进程与 Go 结果验证边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"zoho-workstation/internal/config"
)

const BubSchema = "bub-summary-v4"

type fact struct {
	Text       string   `json:"text"`
	MessageIDs []string `json:"message_ids"`
}
type summary struct {
	DeviceInfo       []fact `json:"device_info"`
	CustomerFeedback []fact `json:"customer_feedback"`
	SupportResponses []fact `json:"support_responses"`
}

func ValidateBub(result, input json.RawMessage) error {
	type evidence struct {
		ID           string `json:"id"`
		Role         string `json:"role"`
		MixedAuthors bool   `json:"mixed_authors"`
		Visibility   string `json:"visibility"`
		Status       string `json:"status"`
		Source       string `json:"source"`
		Direction    string `json:"direction"`
		Body         string `json:"body"`
	}
	var source struct {
		Product     string     `json:"product"`
		Description string     `json:"description"`
		Messages    []evidence `json:"messages"`
	}
	if json.Unmarshal(input, &source) != nil {
		return errors.New("Bub source is invalid")
	}
	messages := map[string]evidence{}
	for _, m := range source.Messages {
		messages[m.ID] = m
	}
	var output summary
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&output) != nil || output.DeviceInfo == nil || output.CustomerFeedback == nil || output.SupportResponses == nil {
		return errors.New("Bub summary schema invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("Bub summary contains extra output")
	}
	for section, items := range [][]fact{output.DeviceInfo, output.CustomerFeedback, output.SupportResponses} {
		if len(items) > 10 {
			return errors.New("Bub summary exceeds section limit")
		}
		for _, item := range items {
			text := strings.TrimSpace(item.Text)
			if text == "" || item.MessageIDs == nil {
				return errors.New("Bub fact lacks valid evidence")
			}
			if len(item.MessageIDs) == 0 {
				if section == 0 && (text == strings.TrimSpace(source.Product) || strings.Contains(source.Description, text)) {
					continue
				}
				return errors.New("Bub fact lacks valid evidence")
			}
			customer, support := false, false
			for _, id := range item.MessageIDs {
				message, ok := messages[id]
				if !ok {
					return errors.New("Bub fact references unknown message")
				}
				customer = customer || message.Role == "customer" || message.MixedAuthors
				support = support || message.Role == "agent" || message.MixedAuthors
				if section == 2 && (message.Visibility == "private" || strings.EqualFold(message.Status, "DRAFT") || strings.EqualFold(message.Status, "FAILED") || strings.EqualFold(message.Status, "PENDING")) {
					return errors.New("Bub support response cites an internal or unsent message")
				}
			}
			if section == 1 && !customer {
				m := messages[item.MessageIDs[0]]
				if len(item.MessageIDs) != 1 || m.Role != "unknown" || m.Source != "thread" || m.Direction != "in" || m.Visibility != "public" || m.Status != "SUCCESS" || !strings.Contains(m.Body, text) {
					return errors.New("Bub feedback lacks customer evidence or a verbatim incoming quote")
				}
			}
			if section == 2 && !support {
				return errors.New("Bub response lacks support evidence")
			}
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
	// Preserve transport routing without exposing parent credentials or SDK config.
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if value := os.Getenv(key); value != "" {
			command.Env = append(command.Env, key+"="+value)
		}
	}
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
			case "unknown_reference", "missing_reference", "reference_version_mismatch", "missing_customer_evidence", "missing_support_evidence", "output_truncated", "blank_output", "unexpected_completion_count", "incomplete_generation", "schema_validation":
				diagnostic = " (" + code + ")"
			}
		}
		switch envelope.Error {
		case "invalid_input", "model_timeout", "model_connection_failed", "model_upstream_failed", "model_request_failed", "invalid_summary", "summary_failed":
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
