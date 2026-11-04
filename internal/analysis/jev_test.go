// input: 合成 TypeSafe HTTP 服务、正常/未知答案及错误响应。
// output: 正确请求、枚举校验、未知保留和错误不泄漏输入的行为证据。
// pos: Jev 外部协议边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/config"
)

func TestJevTypedAnswersAndFailures(t *testing.T) {
	mode := "unknown"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("protocol/auth mismatch")
		}
		var req struct {
			Model     string          `json:"model"`
			State     json.RawMessage `json:"state"`
			Questions map[string]struct {
				Type         string            `json:"type"`
				Instructions string            `json:"instructions"`
				Criteria     map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "test-model" || len(req.Questions) != 14 || req.Questions["priority"].Type != "choice" || req.Questions["emotion"].Instructions == "" || req.Questions["safety"].Criteria["unknown"] == "" {
			t.Error("typed questions incomplete")
		}
		if mode == "http_error" {
			w.WriteHeader(422)
			w.Write([]byte(`{"detail":"SECRET INPUT"}`))
			return
		}
		answers := map[string]any{}
		for key, q := range req.Questions {
			choice := "unknown"
			if key == "priority" && mode == "normal" {
				choice = "high"
			}
			if key == "priority" && mode == "invalid" {
				choice = "invented"
			}
			probs := map[string]float64{}
			for option := range q.Criteria {
				probs[option] = 0
			}
			probs[choice] = 1
			answers[key] = map[string]any{"type": "choice", "choice": choice, "confidence": 1, "probabilities": probs}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "actual-version", "answers": answers, "usage": map[string]int{"input_tokens": 20, "output_tokens": 14}})
	}))
	defer server.Close()
	cfg := config.Model{URL: server.URL, Key: "synthetic", Name: "test-model", Enabled: true}
	for _, mode = range []string{"normal", "unknown", "invalid", "http_error"} {
		body, model, err := analysis.Jev(context.Background(), server.Client(), cfg, json.RawMessage(`{"messages":[]}`))
		if mode == "invalid" || mode == "http_error" {
			if err == nil || strings.Contains(err.Error(), "SECRET INPUT") {
				t.Fatalf("unsafe error: %v", err)
			}
			continue
		}
		if err != nil || model != "actual-version" || !json.Valid(body) {
			t.Fatalf("valid response: %v", err)
		}
		var output struct {
			Answers map[string]struct {
				Choice string `json:"choice"`
			} `json:"answers"`
		}
		json.Unmarshal(body, &output)
		want := "unknown"
		if mode == "normal" {
			want = "high"
		}
		if output.Answers["priority"].Choice != want {
			t.Fatal("unknown or known answer changed")
		}
	}
}
