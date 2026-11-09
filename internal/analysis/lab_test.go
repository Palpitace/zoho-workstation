// input: 合成历史节点、回复、独立评估和技术/政策来源。
// output: 本地引用、只读政策、非法模型输出及失败元信息的回归证据。
// pos: 离线 FAQ Python 边界的 Go 验证。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"zoho-workstation/internal/analysis"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/lab"
)

func TestLabSubprocessPreservesRejectedResponseMetadata(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	python, err := analysis.PythonExecutable()
	if err != nil {
		t.Fatal(err)
	}
	python, err = filepath.Abs(python)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if output, err := exec.Command(python, "-m", "venv", "--without-pip", "python/.venv").CombinedOutput(); err != nil {
		t.Fatalf("synthetic Python environment: %v %s", err, output)
	}
	script := `import json, sys
value = json.load(sys.stdin)
assert value['input']['sources'] == []
assert value['input']['feedback'] == []
assert value['schema_version'] == 'bub-lab-v1'
print('synthetic-private-provider-body', file=sys.stderr)
print(json.dumps({'ok': False, 'error': 'synthetic-private-provider-body', 'result': {'private': 'discard'}, 'returned_model': 'synthetic-returned', 'request_id': 'request-1', 'usage': {'total_tokens': 12}}))
sys.exit(1)
`
	if err := os.WriteFile("python/lab.py", []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	if err := analysis.CheckLab(); err != nil {
		t.Fatal(err)
	}
	result, err := analysis.Lab(context.Background(), config.Model{URL: "https://example.invalid", Key: "synthetic-key", Name: "deepseek:synthetic"}, "reply", lab.Input{Ticket: json.RawMessage(`{"conversation_hash":"h","messages":[]}`)})
	if err == nil || err.Error() != "Bub: lab_failed" || result.Result != nil || result.ReturnedModel != "synthetic-returned" || result.RequestID != "request-1" || string(result.Usage) != `{"total_tokens": 12}` {
		t.Fatalf("failed response lost metadata or leaked body: result=%+v err=%v", result, err)
	}
}

func TestLabReferencesAndReadOnlyPolicy(t *testing.T) {
	in := lab.Input{Ticket: json.RawMessage(`{"conversation_hash":"h","messages":[{"id":"c","role":"customer","visibility":"public","source":"thread","status":"SUCCESS","body":"Cannot print"},{"id":"private","role":"agent","visibility":"private","body":"Internal"}]}`),
		Sources: []knowledge.Source{{ID: "knowledge:1:p1", EntryID: 1, Kind: "sop", Scope: "technical"}, {ID: "knowledge:2:p1", EntryID: 2, Kind: "sop", Scope: "policy"}}}
	for _, tc := range []struct {
		name, task, result string
		ok                 bool
	}{
		{"clarification without knowledge", "reply", `{"text":"What size are your labels?","language":"en","rationale":"需要尺寸信息","missing_information":["尺寸"],"message_ids":["c"],"source_ids":[]}`, true},
		{"future reply reference", "reply", `{"text":"Try this","language":"en","rationale":"说明","missing_information":[],"message_ids":["future"],"source_ids":[]}`, false},
		{"private reply reference", "reply", `{"text":"Try this","language":"en","rationale":"说明","missing_information":[],"message_ids":["private"],"source_ids":[]}`, false},
		{"evaluation uncertainty", "evaluate", `{"verdict":"revise","score":2,"findings":[{"code":"evidence","severity":"warning","reason":"缺少操作依据","message_ids":[],"source_ids":[]}],"suggestions":["先询问尺寸"]}`, true},
		{"contradictory evaluation", "evaluate", `{"verdict":"pass","score":5,"findings":[{"code":"policy","severity":"blocker","reason":"未授权承诺","message_ids":[],"source_ids":[]}],"suggestions":[]}`, false},
		{"empty proposal", "propose", `{"items":[]}`, true},
		{"technical revision", "propose", `{"items":[{"title":"打印","product":"","question":"不能打印","answer":"确认尺寸","conditions":"尚需核对","reason":"补充前提","scope":"technical","replaces_id":1,"message_ids":["c"],"source_ids":["knowledge:1:p1"]}]}`, true},
		{"policy revision", "propose", `{"items":[{"title":"打印","product":"","question":"不能打印","answer":"确认尺寸","conditions":"尚需核对","reason":"补充前提","scope":"technical","replaces_id":2,"message_ids":["c"],"source_ids":[]}]}`, false},
		{"policy inference", "propose", `{"items":[{"title":"打印","product":"","question":"不能打印","answer":"确认尺寸","conditions":"尚需核对","reason":"补充前提","scope":"technical","replaces_id":0,"message_ids":["c"],"source_ids":["knowledge:2:p1"]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := analysis.ValidateLab(tc.task, json.RawMessage(tc.result), in); (err == nil) != tc.ok {
				t.Fatalf("accepted=%t err=%v", tc.ok, err)
			}
		})
	}
	large, _ := json.Marshal(lab.Proposals{Items: []lab.Proposal{{Title: "打印", Question: "不能打印", Answer: strings.Repeat("排", 4000), Conditions: strings.Repeat("条", 1000), Reason: "补充前提", Scope: "technical", MessageIDs: []string{"c"}, SourceIDs: []string{}}}})
	if err := analysis.ValidateLab("propose", large, in); err == nil {
		t.Fatal("proposal larger than a retrievable knowledge block was accepted")
	}
}
