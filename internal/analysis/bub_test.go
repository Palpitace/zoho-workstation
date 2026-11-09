// input: 合成 Bub JSON、固定消息引用、隔离子进程及本地模拟代理。
// output: 引用诊断路径白名单； 整单事实/来源约束、代理继承与凭据隔离、离线环境安全诊断证据。
// pos: Python 输出进入 Go 的验证边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/config"
)

func TestBubOptionalSectionsRequireRoleEvidence(t *testing.T) {
	input := json.RawMessage(`{"product":"Printer P1","description":"Model P1; unable to print","messages":[{"id":"m1","role":"agent","body":"Please upgrade","visibility":"public"},{"id":"m2","role":"customer","body":"I have not tried it yet"},{"id":"m3","role":"agent","body":"Internal plan","visibility":"private"}]}`)
	result := `{"device_info":[{"text":"Printer P1","message_ids":[]}],"customer_feedback":[{"text":"Customer has not upgraded","message_ids":["m2"]}],"support_responses":[{"text":"Agent suggested upgrading; not confirmed executed","message_ids":["m1"]}]}`
	if err := analysis.ValidateBub(json.RawMessage(result), input); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"customer_feedback", "support_responses"} {
		var fragmented map[string][]map[string]any
		json.Unmarshal([]byte(result), &fragmented)
		fragmented[section] = append(fragmented[section], fragmented[section][0])
		body, _ := json.Marshal(fragmented)
		if err := analysis.ValidateBub(body, input); err == nil {
			t.Fatalf("per-message fragments accepted for %s", section)
		}
	}
	if err := analysis.ValidateBub(json.RawMessage(`{"device_info":[],"customer_feedback":[],"support_responses":[]}`), json.RawMessage(`{"messages":[]}`)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.ReplaceAll(result, `"m1"`, `"invented"`), strings.ReplaceAll(result, `"m2"`, `"m1"`), strings.ReplaceAll(result, `"m1"`, `"m3"`), strings.ReplaceAll(result, `"Printer P1"`, `"Invented printer"`), `{"device_info":null,"customer_feedback":[],"support_responses":[]}`, `{"current_situation":{"text":"old result","message_ids":[]}}`} {
		if err := analysis.ValidateBub(json.RawMessage(invalid), input); err == nil {
			t.Fatal("unsupported summary accepted")
		}
	}
}

func TestBubSubprocessUsesProxyWithoutInheritingCredentials(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if _, err := analysis.PythonExecutable(); err != nil {
		t.Fatalf("Bub integration requires make setup: %v", err)
	}
	entry, _ := json.Marshal(filepath.Join(root, "python/main.py"))
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	t.Setenv("ZOHO_CLIENT_SECRET", "synthetic-private-zoho")
	t.Setenv("JEV_API_KEY", "synthetic-private-jev")
	t.Setenv("BUB_API_KEY", "synthetic-private-bub")
	t.Setenv("UNRELATED_ENV", "synthetic-private-unrelated")
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("python", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "python/.venv"), "python/.venv"); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"conversation_hash":"synthetic","messages":[{"id":"m1","role":"customer","body":"Help"}]}`)

	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "CONNECT" && r.Host == "model.invalid:443" {
			connects.Add(1)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	expectedEnv := map[string]string{}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		value := proxy.URL
		if strings.EqualFold(key, "NO_PROXY") {
			value = "unrelated.invalid"
		}
		t.Setenv(key, value)
		expectedEnv[key] = value
	}
	expected, _ := json.Marshal(expectedEnv)
	// Probe the actual child environment, then run the unchanged real entry/SDK.
	probe := fmt.Sprintf("import os, runpy\nassert not any(k in os.environ for k in ('ZOHO_CLIENT_SECRET','JEV_API_KEY','BUB_API_KEY','UNRELATED_ENV'))\nexpected = %s\nassert all(os.environ.get(k) == v for k, v in expected.items())\nrunpy.run_path(%s, run_name='__main__')\n", expected, entry)
	if err := os.WriteFile("python/main.py", []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, _, err = analysis.Bub(ctx, config.Model{URL: "https://model.invalid", Key: "synthetic-key", Name: "deepseek:synthetic"}, input)
	if connects.Load() != 1 || err == nil || err.Error() != "Bub: model_connection_failed" {
		t.Fatalf("proxy requests=%d, safe error=%v", connects.Load(), err)
	}
}

func TestBubSingleIncomingMessage(t *testing.T) {
	for _, tc := range []struct {
		name, role, direction, visibility, status, text string
		ok                                              bool
	}{
		{"customer without reply", "customer", "in", "public", "SUCCESS", "Printing overlaps", true},
		{"unknown verbatim", "unknown", "in", "public", "SUCCESS", "Labels overlap", true},
		{"unknown paraphrase", "unknown", "in", "public", "SUCCESS", "Customer confirmed a defect", false},
		{"agent is not customer", "agent", "in", "public", "SUCCESS", "Labels overlap", false},
		{"private unknown", "unknown", "in", "private", "SUCCESS", "Labels overlap", false},
		{"outgoing unknown", "unknown", "out", "public", "SUCCESS", "Labels overlap", false},
		{"unsent unknown", "unknown", "in", "public", "DRAFT", "Labels overlap", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"messages": []map[string]any{{"id": "m1", "source": "thread", "role": tc.role, "direction": tc.direction, "visibility": tc.visibility, "status": tc.status, "body": "Feedback: Labels overlap\nDevice: synthetic"}}})
			result, _ := json.Marshal(map[string]any{"device_info": []any{}, "customer_feedback": []map[string]any{{"text": tc.text, "message_ids": []string{"m1"}}}, "support_responses": []any{}})
			if err := analysis.ValidateBub(result, input); (err == nil) != tc.ok {
				t.Fatalf("accepted=%t want=%t: %v", err == nil, tc.ok, err)
			}
		})
	}
}

func TestBubOfflineEnvironmentCheck(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct{ proxy, code string }{
		{"socks5h://synthetic:private@127.0.0.1:7890", ""},
		{"unsupported://synthetic:private@127.0.0.1:7890", "proxy_configuration_invalid"},
	} {
		t.Setenv("ALL_PROXY", tc.proxy)
		err := analysis.CheckPython(context.Background())
		if tc.code == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.code) || strings.Contains(err.Error(), "synthetic:private") {
			t.Fatalf("expected safe %s, got %v", tc.code, err)
		}
	}
}

func TestBubEvidenceDiagnosticPathIsSafe(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("python", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "python/.venv"), "python/.venv"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ location, suffix string }{
		{`["customer_feedback",0,"message_ids"]`, " at customer_feedback[0].message_ids"},
		{`["device_info",9,"message_ids"]`, " at device_info[9].message_ids"},
		{`["synthetic-private",0,"message_ids"]`, ""},
		{`["customer_feedback",0,"synthetic-private"]`, ""},
		{`["customer_feedback",1.5,"message_ids"]`, ""},
		{`["customer_feedback",100000,"message_ids"]`, ""},
	} {
		t.Run(tc.location, func(t *testing.T) {
			body := `{"ok":false,"error":"invalid_summary","diagnostics":[{"code":"missing_reference","loc":` + tc.location + `}]}`
			script := "print(" + fmt.Sprintf("%q", body) + ")\n"
			if err := os.WriteFile("python/main.py", []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := analysis.Bub(context.Background(), config.Model{Name: "deepseek:synthetic", Key: "synthetic", URL: "https://model.invalid"}, json.RawMessage(`{}`))
			want := "Bub: invalid_summary (missing_reference)" + tc.suffix
			if err == nil || err.Error() != want {
				t.Fatalf("safe error = %v, want %s", err, want)
			}
		})
	}
}
