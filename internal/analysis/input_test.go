// input: 合成规范化工单、原始联系人与展示字段变化。
// output: 脱敏共享输入及仅语义变化触发新 Hash 的行为证据。
// pos: 模型输入边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis_test

import (
	"encoding/json"
	"strings"
	"testing"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/ticket"
)

func TestInputRedactsAndIgnoresDisplayChanges(t *testing.T) {
	s := ticket.Snapshot{ID: "1", Subject: "Help Ada Example", Description: "Email ada@example.invalid", DepartmentID: "7", Status: "Open", Complete: true, Messages: []ticket.Message{{ID: "2", Role: "customer", Body: "I have NOT updated firmware", ModifiedTime: "old"}}}
	raw := ticket.RawSnapshot{Contact: json.RawMessage(`{"name":"Ada Example","email":"ada@example.invalid"}`)}
	body, hash, err := analysis.Input(s, raw)
	if err != nil || strings.Contains(string(body), "Ada Example") || strings.Contains(string(body), "ada@example.invalid") || !strings.Contains(string(body), "NOT updated") {
		t.Fatalf("input not minimized: %v", err)
	}
	s.Status = "Closed"
	s.Priority = "High"
	s.ModifiedTime = "later"
	s.Messages[0].ModifiedTime = "later"
	_, same, err := analysis.Input(s, raw)
	if err != nil || hash != same {
		t.Fatal("display changes invalidated model input")
	}
	s.Messages[0].Body = "I upgraded, but the problem remains"
	_, changed, _ := analysis.Input(s, raw)
	if changed == hash {
		t.Fatal("new customer fact did not invalidate input")
	}
}
