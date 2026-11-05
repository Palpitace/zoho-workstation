// input: 合成 HTML/纯文本邮件及旧快照。
// output: 引用分段保真、嵌套/多段折叠和安全文本回退的验证。
// pos: 详情展示派生边界测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"encoding/json"
	"strings"
	"testing"

	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
)

func TestDisplayMessageQuotesPreserveEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, kind, body string
		quotes           int
	}{
		{"nested HTML", "html", `<p>New reply</p><blockquote>Old reply<blockquote>Older reply</blockquote></blockquote><p>New inline answer</p><div class="gmail_quote">Another old reply</div>`, 3},
		{"mail clients", "html", `<p>New</p><div id="divRplyFwdMsg">From: someone</div><div class="zmail_extra">Old</div>`, 2},
		{"safe rendering", "html", `<p onclick="attack()">New</p><script>secret()</script><blockquote><a href="https://example.invalid/track">Old text</a><img src="https://example.invalid/pixel" onerror="attack()" alt="Old picture"></blockquote>`, 1},
		{"English nested text", "text", "New\nOn Tue, someone wrote:\n> Old\n>> Older\nNew inline\n> More old", 3},
		{"Chinese header", "text", "新回复\n某人写道：\n> 旧回复\n新内容", 1},
		{"German header", "text", "Neu\nAm Dienstag schrieb jemand:\n> Alt", 1},
		{"forward header without reliable boundary", "text", "New\n----- Forwarded message -----\nFrom: sender\nSubject: old\nAmbiguous inline reply", 0},
		{"header without explicit quotes", "text", "On Tue, someone wrote:\nI did NOT upgrade", 0},
		{"ambiguous repeated HTML", "html", `<p>Same text</p><blockquote>Same text</blockquote>`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			thread, _ := json.Marshal(map[string]string{"id": "1", "contentType": tc.kind, "content": tc.body})
			raw := ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"ticket"}`), Threads: []json.RawMessage{thread}}
			snapshot, err := ticket.Normalize(raw)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshot.Messages[0].Body
			record := store.Record{Snapshot: snapshot, Raw: raw}
			parts := displayMessageSegments(record)["thread:1"]
			var flatten func([]MessageSegment) (string, int)
			flatten = func(parts []MessageSegment) (string, int) {
				text, count := "", 0
				for _, p := range parts {
					text += p.Text
					if p.Quote {
						count++
					}
					body, n := flatten(p.Children)
					text += body
					count += n
				}
				return text, count
			}
			got, count := flatten(parts)
			if count != tc.quotes {
				t.Fatalf("quotes=%d want %d: %+v", count, tc.quotes, parts)
			}
			if count > 0 && got != before {
				t.Fatalf("display changed body: %q != %q", got, before)
			}
			if snapshot.Messages[0].Body != before {
				t.Fatal("mutated business evidence")
			}
			if tc.name == "safe rendering" && (strings.Contains(got, "attack()") || strings.Contains(got, "secret()") || strings.Contains(got, "https://")) {
				t.Fatal("HTML executable attributes leaked")
			}
			// No raw response is required to keep an older database readable.
			record.Raw = ticket.RawSnapshot{}
			displayMessageSegments(record)
		})
	}
}
