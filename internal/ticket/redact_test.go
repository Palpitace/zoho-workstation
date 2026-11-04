// input: 含合成个人信息和工单事实的规范化快照。
// output: 脱敏副本不泄漏已知信息、不改变原证据及关键事实的验证。
// pos: AI 输入脱敏边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package ticket_test

import (
	"encoding/json"
	"strings"
	"testing"

	"zoho-workstation/internal/ticket"
)

func TestRedactionPreservesEvidenceAndDoesNotMutateOriginal(t *testing.T) {
	raw := ticket.RawSnapshot{Contact: json.RawMessage(`{"firstName":"Alice","lastName":"Smith","email":"alice@example.invalid","phone":"+1 415 555 0123","street":"23 Example Road"}`)}
	original := ticket.Snapshot{ID: "9007199254740993", Subject: "Alice Smith: not working", Description: "Firmware 2026.11.04 failed at 10:30 on 2026-11-04. Error 1234567890. Product AlicePro.", Messages: []ticket.Message{{ID: "m1", Role: "customer", Source: "thread", Status: "FAILED", Body: "I did not upgrade.\nRegards,\nAlice Smith\nEmail: alice@example.invalid\nPhone: +1 415 555 0123\nAddress: 23 Example Road", Attachments: []ticket.Attachment{{ID: "a1", Name: "Alice error.txt", URL: "https://example.invalid/private?token=secret"}}}}, Complete: true}
	got := ticket.RedactedSnapshot(original, raw)
	encoded, _ := json.Marshal(got)
	for _, sensitive := range []string{"Alice Smith", "alice@example.invalid", "415 555 0123", "23 Example Road", "private?token"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("personal data retained: %s", sensitive)
		}
	}
	if !strings.Contains(got.Description, "Error 1234567890") || !strings.Contains(got.Description, "2026-11-04") || !strings.Contains(got.Description, "AlicePro") || !strings.Contains(got.Messages[0].Body, "I did not upgrade.") || got.Messages[0].Status != "FAILED" {
		t.Fatalf("facts changed: %+v", got)
	}
	if original.Messages[0].Attachments[0].URL == "" || !strings.Contains(original.Messages[0].Body, "Alice Smith") {
		t.Fatal("original evidence mutated")
	}
}

func TestRedactionCoversUnstructuredContactFieldsAndQuotedHeaders(t *testing.T) {
	s := ticket.Snapshot{Description: "Name: Jordan Example\nEmail: jordan@example.invalid\nTel: +44 20 7946 0123\n地址：示例路123号\nOn 2026-11-04, Pat Doe <pat@example.invalid> wrote:\nDo not reset firmware 3.14.15.\nIP\n203.0.113.7\nLatitude\n12.345\nCity\nExample City\nName\nUnlisted Person\nChat ID\n456789\nQuestion\nThe printer still does not work", Complete: true}
	s.Description += "\nCustomer Info\n{&quot;sn&quot;:&quot;SERIAL-PRIVATE&quot;,&quot;mac&quot;:&quot;02:00:00:AB:CD:EF&quot;,&quot;model&quot;:&quot;PM241&quot;}"
	got := ticket.RedactedSnapshot(s, ticket.RawSnapshot{})
	if strings.Contains(got.Description, "SERIAL-PRIVATE") || strings.Contains(got.Description, "02:00:00:AB:CD:EF") || !strings.Contains(got.Description, "PM241") {
		t.Fatal("encoded device identifiers retained or product erased")
	}
	for _, secret := range []string{"Jordan Example", "jordan@example.invalid", "7946 0123", "示例路123号", "Pat Doe", "pat@example.invalid", "203.0.113.7", "12.345", "Example City", "Unlisted Person", "456789"} {
		if strings.Contains(got.Description, secret) {
			t.Fatalf("unstructured personal data retained: %s", secret)
		}
	}
	if !strings.Contains(got.Description, "2026-11-04") || !strings.Contains(got.Description, "Do not reset firmware 3.14.15.") {
		t.Fatal("quote facts or date removed")
	}
}
