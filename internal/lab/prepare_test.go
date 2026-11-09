// input: 合成历史消息、重复工单与固定知识。
// output: 历史前缀隔离、分组留出及派生知识隔离验证。
// pos: 离线准备的行为测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package lab

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/ticket"
)

func TestHistoricalPrefixesAndSplit(t *testing.T) {
	samples := []Sample{}
	for i := 0; i < 10; i++ {
		snap := ticket.Snapshot{ID: fmt.Sprint(i), Subject: "FUTURE_SUBJECT", Description: "FUTURE_DESCRIPTION", Product: "FUTURE_MODEL", Complete: true, Messages: []ticket.Message{
			{ID: "c1", Role: "customer", Visibility: "public", Source: "thread", Status: "SUCCESS", CreatedTime: "2026-11-06T01:00:00Z", Body: fmt.Sprintf("printer problem %d", i)},
			{ID: "a1", Role: "agent", Visibility: "public", Source: "thread", Status: "SUCCESS", CreatedTime: "2026-11-06T02:00:00Z", Body: "Please reconnect"},
			{ID: "c2", Role: "customer", Visibility: "public", Source: "thread", Status: "SUCCESS", CreatedTime: "2026-11-06T03:00:00Z", Body: "Still not working"},
			{ID: "a2", Role: "agent", Visibility: "public", Source: "thread", Status: "SUCCESS", CreatedTime: "2026-11-06T04:00:00Z", Body: "FUTURE_RESOLUTION"},
		}}
		sample, err := PrepareSample(snap, "N"+snap.ID)
		if err != nil {
			t.Fatal(err)
		}
		samples = append(samples, sample)
	}
	if len(samples[0].Nodes) != 2 {
		t.Fatal("expected first and follow-up nodes")
	}
	for i, n := range samples[0].Nodes {
		if strings.Contains(string(n.Ticket), "FUTURE_") {
			t.Fatal("future metadata leaked")
		}
		var prefix struct {
			Messages []ticket.Message `json:"messages"`
		}
		_ = json.Unmarshal(n.Ticket, &prefix)
		if len(prefix.Messages) != 1+i*2 {
			t.Fatal("wrong historical prefix")
		}
	}
	duplicate := samples[0]
	duplicate.TicketID = "duplicate"
	samples = append(samples, duplicate)
	if err := Split(samples); err != nil {
		t.Fatal(err)
	}
	if err := LinkGroups(samples, map[string]string{samples[1].TicketID: "related", samples[2].TicketID: "related"}); err != nil {
		t.Fatal(err)
	}
	if samples[1].Group != samples[2].Group {
		t.Fatal("confirmed related cases split")
	}
	if samples[0].Split != samples[10].Split {
		t.Fatal("duplicate crossed partitions")
	}
	counts := map[string]int{}
	for _, s := range samples {
		counts[s.Split]++
	}
	if len(counts) != 3 {
		t.Fatal(counts)
	}
	entries := []knowledge.Entry{{ID: 1, Kind: "sop", Status: "published"}, {ID: 2, Kind: "sop", Status: "published", SourceTicketID: samples[0].TicketID}, {ID: 3, Kind: "case", Status: "published"}}
	safe := Baseline(entries, samples)
	if len(safe) != 1 || safe[0].ID != 1 {
		t.Fatal("batch-derived or automatic knowledge leaked")
	}
}

func TestEditedAndAmbiguousMessagesCannotBecomeHistoricalEvidence(t *testing.T) {
	base := ticket.Snapshot{ID: "x", Complete: true, Messages: []ticket.Message{{ID: "c", Role: "customer", Visibility: "public", CreatedTime: "2026-11-06T01:00:00Z", ModifiedTime: "2026-11-06T02:00:00Z", Body: "edited later"}}}
	if _, err := PrepareSample(base, ""); err == nil {
		t.Fatal("future edit accepted")
	}
	base.Messages[0].ModifiedTime = ""
	base.Messages[0].Role = "unknown"
	if _, err := PrepareSample(base, ""); err == nil {
		t.Fatal("unknown author treated as customer")
	}
}
