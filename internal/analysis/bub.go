// input: 完整脱敏会话、显式配置、固定 Python 环境和代理白名单。
// output: 白名单HTTP诊断、整单汇总校验及安全环境/模型错误。
// pos: 无 Shell 的 Python 子进程与 Go 结果验证边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"zoho-workstation/internal/config"
)

const BubSchema = "bub-summary-v6"

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
		if len(items) > 10 || (section > 0 && len(items) > 1) {
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
	command, err := pythonCommand(ctx, entry)
	if err != nil {
		return nil, "", err
	}
	command.Stdin = bytes.NewReader(body)
	var output limitedOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	processErr := command.Run()
	var envelope struct {
		HTTPStatus    int             `json:"http_status"`
		OK            bool            `json:"ok"`
		Result        json.RawMessage `json:"result"`
		ReturnedModel string          `json:"returned_model"`
		RequestID     string          `json:"request_id"`
		Usage         json.RawMessage `json:"usage"`
		Error         string          `json:"error"`
		Diagnostics   []struct {
			Code string            `json:"code"`
			Loc  []json.RawMessage `json:"loc"`
		} `json:"diagnostics"`
	}
	if json.Unmarshal(output.Bytes(), &envelope) != nil {
		return nil, "", errors.New("Bub Python process failed or returned invalid JSON")
	}
	if !envelope.OK {
		diagnostic := ""
		if len(envelope.Diagnostics) > 0 {
			switch code := envelope.Diagnostics[0].Code; code {
			case "unknown_reference", "missing_reference", "missing_customer_evidence", "missing_support_evidence", "output_truncated", "blank_output", "unexpected_completion_count", "incomplete_generation", "schema_validation":
				diagnostic = " (" + code + ")"
				loc := envelope.Diagnostics[0].Loc
				if len(loc) == 3 {
					var section, field string
					var index int
					if json.Unmarshal(loc[0], &section) == nil && json.Unmarshal(loc[1], &index) == nil && json.Unmarshal(loc[2], &field) == nil && index >= 0 && index < 10 && field == "message_ids" && (section == "device_info" || section == "customer_feedback" || section == "support_responses") {
						diagnostic += fmt.Sprintf(" at %s[%d].message_ids", section, index)
					}
				}
			}
		}
		switch envelope.Error {
		case "model_rate_limited", "model_http_error", "model_response_invalid", "invalid_input", "model_timeout", "model_connection_failed", "model_upstream_failed", "model_request_failed", "invalid_summary", "summary_failed", "proxy_dependency_missing", "proxy_configuration_invalid", "python_environment_invalid":
			return nil, envelope.ReturnedModel, fmt.Errorf("%w%s", modelError(envelope.Error, envelope.HTTPStatus, envelope.RequestID), diagnostic)
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

// PythonExecutable supports the relocatable download package and source venv.
func PythonExecutable() (string, error) {
	candidates := []string{"runtime/python/bin/python3", "python/.venv/bin/python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"runtime/python/python.exe", "python/.venv/Scripts/python.exe"}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return filepath.Abs(path)
		}
	}
	return "", errors.New("Bub Python environment is unavailable")
}

// CheckPython constructs SDK transports with the same isolated environment as Bub.
// It never receives credentials or makes provider requests.
func CheckPython(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command, err := pythonCommand(ctx, "python/main.py", "--check")
	if err != nil {
		return err
	}
	var output limitedOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	processErr := command.Run()
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	decodeErr := json.Unmarshal(output.Bytes(), &result)
	if decodeErr == nil && !result.OK {
		switch result.Error {
		case "proxy_dependency_missing":
			return errors.New("Bub: proxy_dependency_missing; restore the complete package or install the locked Python environment")
		case "proxy_configuration_invalid":
			return errors.New("Bub: proxy_configuration_invalid; correct HTTP(S)_PROXY/ALL_PROXY and restart")
		case "python_environment_invalid":
			return errors.New("Bub: python_environment_invalid; check local certificate and Python environment settings")
		}
	}
	if processErr != nil || decodeErr != nil || !result.OK {
		return errors.New("Bub Python dependencies unavailable; restore the complete package or install the locked environment")
	}
	return nil
}

func pythonCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	python, err := PythonExecutable()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, python, args...)
	packages, err := filepath.Abs("python/packages")
	if err != nil {
		return nil, errors.New("Python packages path unavailable")
	}
	childPath := filepath.Dir(python) + string(os.PathListSeparator) + "/usr/bin:/bin"
	if runtime.GOOS == "windows" {
		childPath = filepath.Dir(python) + ";" + filepath.Join(os.Getenv("SystemRoot"), "System32")
	}
	command.Env = []string{"PATH=" + childPath, "PYTHONPATH=" + packages, "PYTHONNOUSERSITE=1", "PYTHONIOENCODING=utf-8", "PYTHONDONTWRITEBYTECODE=1"}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
			if value := os.Getenv(key); value != "" {
				command.Env = append(command.Env, key+"="+value)
			}
		}
	}
	// Preserve transport routing without exposing parent credentials or SDK config.
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if value := os.Getenv(key); value != "" {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	return command, nil
}
