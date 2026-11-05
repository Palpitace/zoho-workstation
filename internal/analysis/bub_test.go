// input: 合成 Bub JSON、固定消息引用、隔离子进程及本地模拟代理。
// output: 总结契约、必要代理继承及凭据环境隔离的回归证据。
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

func TestBubFactsRequireValidReferencesAndExecution(t *testing.T) {
	input := json.RawMessage(`{"messages":[{"id":"m1","role":"agent","body":"Please upgrade"},{"id":"m2","role":"customer","body":"I have not tried it yet"}]}`)
	result := `{"current_situation":{"text":"Upgrade suggested, not confirmed executed","message_ids":["m1","m2"]},"key_progress":[],"attempted_measures":[{"action":"Upgrade firmware","execution_status":"suggested","result":"No execution result available","result_status":"unknown","message_ids":["m1","m2"]}],"blockers":[],"next_steps":[{"text":"Ask for execution confirmation","basis":"recommendation","message_ids":["m2"]}]}`
	if err := analysis.ValidateBub(json.RawMessage(result), input); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.ReplaceAll(result, `"m1"`, `"invented"`), strings.Replace(result, `"result_status":"unknown"`, `"result_status":"customer_reported"`, 1), `{"current_situation":null}`} {
		if err := analysis.ValidateBub(json.RawMessage(invalid), input); err == nil {
			t.Fatal("unsupported fact structure accepted")
		}
	}
}

func TestBubSubprocessUsesProxyWithoutInheritingCredentials(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "python/.venv/bin/python")); err != nil {
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
	input := json.RawMessage(`{"conversation_hash":"synthetic","jev_reference":null,"messages":[{"id":"m1","role":"customer","body":"Help"}]}`)

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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, _, err = analysis.Bub(ctx, config.Model{URL: "https://model.invalid", Key: "synthetic-key", Name: "deepseek:synthetic"}, input)
	if connects.Load() != 1 || err == nil || err.Error() != "Bub: model_connection_failed" {
		t.Fatalf("proxy requests=%d, safe error=%v", connects.Load(), err)
	}
}
