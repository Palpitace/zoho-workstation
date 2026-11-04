// input: 合成 Zoho 详情、线程和备注响应。
// output: 清洗的事实、排序、角色及不完整状态行为验证。
// pos: 工单规范化边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package ticket_test

import (
	"encoding/json"
	"strings"
	"testing"

	"zoho-workstation/internal/ticket"
)

func TestNormalizePreservesConversationEvidence(t *testing.T) {
	raw := ticket.RawSnapshot{
		Ticket: json.RawMessage(`{"id":9007199254740993,"departmentId":"4","subject":"Printer failure","description":"<p>Stopped at 10:30.<br>Not fixed.</p>","status":"Open","priority":"High","createdTime":"2026-11-01T18:00:00+08:00","modifiedTime":"2026-11-02T10:00:00Z","product":{"productName":"P12"}}`),
		Threads: []json.RawMessage{
			json.RawMessage(`{"id":"2","createdTime":"2026-11-02T10:00:00Z","contentType":"text/html","content":"<p>I have <b>not</b> upgraded.<br>It still fails.</p><blockquote>Please upgrade the firmware.</blockquote>","author":{"type":"END_USER"},"visibility":"public"}`),
			json.RawMessage(`{"id":"1","createdTime":"2026-11-02T09:00:00Z","contentType":"plainText","content":"Please upgrade the firmware.","author":{"type":"AGENT"},"visibility":"public","status":"DRAFT","direction":"out","isDescriptionThread":true}`),
			json.RawMessage(`{"id":"1","createdTime":"2026-11-02T09:00:00Z","contentType":"plainText","content":"Please upgrade the firmware.","author":{"type":"AGENT"},"visibility":"public","status":"DRAFT","direction":"out","isDescriptionThread":true}`),
		},
		Comments: []json.RawMessage{
			json.RawMessage(`{"id":"3","commentedTime":"2026-11-02T09:30:00Z","contentType":"html","content":"<div>Await customer confirmation.</div>","isPublic":false,"commenter":{"type":"AGENT"},"attachments":[{"id":"8","name":"error.txt","size":"12","href":"https://example.invalid/download"}]}`),
		},
		Complete: true,
	}
	got, err := ticket.Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "9007199254740993" || got.CreatedTime != "2026-11-01T10:00:00Z" || got.Product != "P12" {
		t.Fatalf("identity, time or product lost: %+v", got)
	}
	if !got.Complete || len(got.Messages) != 3 {
		t.Fatalf("unexpected completeness/messages: %+v", got)
	}
	if got.Messages[0].ID != "1" || got.Messages[1].ID != "3" || got.Messages[2].ID != "2" {
		t.Fatalf("wrong chronological order: %+v", got.Messages)
	}
	if got.Messages[0].Role != "agent" || got.Messages[2].Role != "customer" || got.Messages[1].Visibility != "private" || got.Messages[1].Source != "comment" {
		t.Fatalf("source or role lost: %+v", got.Messages)
	}
	if got.Messages[2].Body != "I have not upgraded.\nIt still fails.\nPlease upgrade the firmware." || !strings.Contains(got.Description, "Not fixed.") {
		t.Fatalf("key facts lost: %+v", got)
	}
	if got.Messages[1].Attachments[0].Size != 12 {
		t.Fatal("attachment metadata lost")
	}
	if got.Messages[0].Status != "DRAFT" || got.Messages[0].Direction != "out" || !got.Messages[0].IsDescriptionThread {
		t.Fatal("draft or description semantics lost")
	}
}

func TestNormalizeMarksMissingAndTruncatedEvidence(t *testing.T) {
	for _, item := range []struct{ name, message string }{
		{"summary only", `{"id":"1","createdTime":"2026-11-01T10:00:00Z","summary":"Short summary","direction":"in","visibility":"public"}`},
		{"truncated", `{"id":"1","createdTime":"2026-11-01T10:00:00Z","content":"Partial reply","contentType":"plainText","isContentTruncated":true,"author":{"type":"TEAM"}}`},
		{"invalid time", `{"id":"1","createdTime":"unknown","content":"Not fixed","contentType":"plainText"}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			got, err := ticket.Normalize(ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"1","createdTime":"2026-11-01T10:00:00Z","modifiedTime":"2026-11-01T10:00:00Z"}`), Threads: []json.RawMessage{json.RawMessage(item.message)}, Complete: true})
			if err != nil {
				t.Fatal(err)
			}
			if got.Complete || len(got.Warnings) == 0 || got.Messages[0].Role != "unknown" {
				t.Fatalf("uncertainty concealed: %+v", got)
			}
			if item.name == "summary only" && got.Messages[0].Body != "" {
				t.Fatal("summary substituted for full content")
			}
		})
	}
}

func TestOnlineChatDoesNotAttributeAllTranscriptToCustomer(t *testing.T) {
	raw := ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"1","departmentId":"7","createdTime":"2026-01-01T00:00:00Z","modifiedTime":"2026-01-01T00:00:00Z"}`), Complete: true, Threads: []json.RawMessage{json.RawMessage(`{"id":"2","createdTime":"2026-01-01T00:00:00Z","channel":"ONLINE_CHAT","author":{"type":"END_USER"},"content":"Customer: Help. Agent: Please upgrade."}`)}}
	got, err := ticket.Normalize(raw)
	if err != nil || got.Messages[0].Role != "unknown" || !got.Messages[0].MixedAuthors || !got.Complete {
		t.Fatal("mixed chat transcript attributed to one author or discarded")
	}
}
