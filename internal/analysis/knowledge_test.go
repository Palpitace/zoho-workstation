// input: 合成工单、案例和建议输出。
// output: 建议/执行/结果分离、角色证据、未知引用和事实链路隔离的回归。
// pos: 知识模型边界的小型契约测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis_test

import (
	"encoding/json"
	"testing"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/knowledge"
)

func TestKnowledgeEvidenceAndIndependentInputs(t *testing.T) {
	conversation := json.RawMessage(`{"conversation_hash":"h","product":"P1","messages":[{"id":"a","role":"agent","source":"thread","visibility":"public","body":"Please calibrate P1"},{"id":"c","role":"customer","source":"thread","visibility":"public","body":"I have not calibrated P1"},{"id":"p","role":"agent","source":"comment","visibility":"private","body":"Internal"}]}`)
	factual := json.RawMessage(`{"summary":{"device_info":[],"customer_feedback":[],"support_responses":[]}}`)
	input, hash, err := analysis.KnowledgeInputBytes(conversation, factual, "v", []knowledge.Source{{ID: "knowledge:1:page-1", Text: "Calibration", Kind: "sop"}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, kind, body string
		ok               bool
	}{
		{"customer only", "case", `{"title":"Print offset","product":"P1","issue":"Needs help","steps":[],"message_ids":["c"]}`, true},
		{"suggestion with unknown result", "case", `{"title":"Print offset","product":"P1","issue":"Not calibrated","steps":[{"action":"Calibrate","execution":"unknown","outcome":"unknown","message_ids":["a"]}],"message_ids":["c"]}`, true},
		{"agent cannot confirm outcome", "case", `{"title":"Print offset","product":"P1","issue":"Unknown","steps":[{"action":"Calibrate","execution":"confirmed","outcome":"resolved","message_ids":["a"]}],"message_ids":["c"]}`, false},
		{"private action", "case", `{"title":"Print offset","product":"P1","issue":"Unknown","steps":[{"action":"Calibrate","execution":"unknown","outcome":"unknown","message_ids":["p"]}],"message_ids":["c"]}`, false},
		{"invented model", "case", `{"title":"Print offset","product":"P999","issue":"Unknown","steps":[],"message_ids":["c"]}`, false},
		{"cited advice", "advice", `{"steps":[{"action":"Confirm calibration prerequisites","reason":"No confirmed execution","source_ids":["knowledge:1:page-1"],"current_message_ids":["c"]}],"missing_information":[],"conflicts":[]}`, true},
		{"invented source", "advice", `{"steps":[{"action":"Calibrate","reason":"History","source_ids":["invented"],"current_message_ids":[]}],"missing_information":[],"conflicts":[]}`, false},
		{"no sources no invented action", "advice", `{"steps":[],"missing_information":["Need compatible SOP"],"conflicts":[]}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := analysis.ValidateKnowledge(tc.kind, json.RawMessage(tc.body), input); (err == nil) != tc.ok {
				t.Fatalf("ok=%t err=%v", tc.ok, err)
			}
		})
	}
	_, nextHash, _ := analysis.KnowledgeInputBytes(conversation, factual, "v", []knowledge.Source{{ID: "knowledge:2:page-1", Text: "Calibration", Kind: "sop"}})
	if nextHash == hash {
		t.Fatal("new SOP version did not invalidate advice")
	}
}
