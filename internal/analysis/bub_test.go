// input: 合成 Bub JSON、固定消息引用和执行状态。
// output: 建议/执行结果区分、非法引用和结构错误拒绝的证据。
// pos: Python 输出进入 Go 的验证边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis_test

import (
	"encoding/json"
	"strings"
	"testing"
	"zoho-workstation/internal/analysis"
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
