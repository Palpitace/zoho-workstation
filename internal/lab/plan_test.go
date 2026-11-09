// input: 合成固定批次、单机型/白名单/排除条件的知识和逐步模型结果。
// output: 有限改写、独立评估、留出隔离及跨轮反馈证据。
// pos: 离线流程的确定性行为测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package lab

import (
	"encoding/json"
	"strings"
	"testing"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/ticket"
)

func TestFiniteWorkflowAndIndependentHoldout(t *testing.T) {
	sample := func(id, split string) Sample {
		body := wireSnapshot(id, []ticket.Message{{ID: "c", Role: "customer", Source: "thread", Visibility: "public", Body: "My P10 printer has offset", CreatedTime: "2026-11-06T01:00:00Z"}})
		return Sample{TicketID: id, Split: split, Nodes: []Node{{ID: id + ":c", Ticket: body}}, Snapshot: ticket.Snapshot{ID: id, Messages: []ticket.Message{{ID: "c", Role: "customer", Visibility: "public", Body: "My P10 printer has offset"}}}}
	}
	batch := Batch{ID: 1, Spec: Spec{Samples: []Sample{sample("learning", "learn"), sample("dev", "development"), sample("held", "holdout")}, Feedback: []Feedback{{ID: 1, BatchID: 1, NodeID: "dev:c", Score: 2, Note: "Must address offset"}}}}
	attempted := map[string]int{}
	for count := 0; ; count++ {
		if count > 40 {
			t.Fatal("unbounded workflow")
		}
		step := Next(batch)
		if step == nil {
			break
		}
		attempted[step.Key]++
		if step.Task == "evaluate" && (step.Input.Evaluation != nil || len(step.Input.Feedback) > 0) {
			t.Fatal("evaluator is anchored to prior judgment")
		}
		if strings.HasPrefix(step.NodeID, "held") && (step.Task == "propose" || step.Revision > 0 || len(step.Input.Feedback) > 0) {
			t.Fatal("holdout used for optimization")
		}
		input, _ := json.Marshal(step.Input)
		call := Call{ID: len(batch.State.Calls) + 1, Key: step.Key, NodeID: step.NodeID, Task: step.Task, Variant: step.Variant, Revision: step.Revision, Input: input, Status: "success"}
		if step.Task == "reply" && attempted[step.Key] == 1 && step.NodeID == "dev:c" {
			call.Status = "failed"
		} else {
			switch step.Task {
			case "reply":
				call.Result, _ = json.Marshal(Reply{Text: "check media", MessageIDs: []string{"c"}})
			case "evaluate":
				call.Result, _ = json.Marshal(Evaluation{Verdict: "revise", Score: 2})
			case "propose":
				call.Result = json.RawMessage(`{"items":[]}`)
			}
		}
		batch.State.Calls = append(batch.State.Calls, call)
	}
	if attempted["dev:c:candidate:2:reply"] != 2 || attempted["dev:c:candidate:3:reply"] != 0 {
		t.Fatal("rewrite or retry bound incorrect", attempted)
	}
	batch.State.Status = "completed"
	if !ValidationComplete(batch) {
		t.Fatal("successful held comparisons not recognized")
	}
	batch.State.Calls[len(batch.State.Calls)-1].Status = "failed"
	if ValidationComplete(batch) {
		t.Fatal("failed comparison accepted")
	}
	batch.State.Feedback = []Feedback{{ID: 1, BatchID: 2, NodeID: "held:c", Score: 5}, {ID: 2, BatchID: 2, NodeID: "dev:c", Score: 4}}
	feedback := DevelopmentFeedback(batch)
	if len(feedback) != 2 || feedback[0].BatchID != 1 || feedback[1].BatchID != 2 {
		t.Fatal("lost inherited feedback or leaked holdout", feedback)
	}
}

func TestCandidateRetrievalKeepsLanguageAndModelBoundaries(t *testing.T) {
	body := wireSnapshot("learning", []ticket.Message{{ID: "c", Role: "customer", Visibility: "public", Body: "P10 printer offset after calibration"}})
	proposal := Proposal{Title: "打印偏移", Product: "P10", Question: "打印偏移怎么办", Answer: "先确认耗材", Scope: "technical"}
	input, _ := json.Marshal(Input{Ticket: body})
	result, _ := json.Marshal(Proposals{Items: []Proposal{proposal}})
	b := Batch{State: State{Calls: []Call{{ID: 1, Task: "propose", Status: "success", Input: input, Result: result}}}}
	candidates := Candidates(b)
	// Later alternatives for one published version supersede only the
	// experimental candidate, never discard its recorded history.
	versioned := b
	proposal.ReplacesID = 123
	versions, _ := json.Marshal(Proposals{Items: []Proposal{proposal}})
	versioned.State.Calls = []Call{{ID: 1, Task: "propose", Status: "success", Input: input, Result: versions}, {ID: 2, Task: "propose", Status: "success", Input: input, Result: versions}}
	revisions := Candidates(versioned)
	if revisions[0].Active || !revisions[1].Active {
		t.Fatal("superseded candidate remains eligible for promotion")
	}
	entries := []knowledge.Entry{candidates[0].Entry}
	for i := 0; i < 12; i++ {
		entries = append(entries, knowledge.Entry{ID: int64(i + 1), Kind: "sop", Status: "published", Scope: "technical", Title: "Offset printer", Product: "P10", Blocks: []knowledge.Block{{ID: "faq", Text: "printer offset after calibration"}}})
	}
	entries = append(entries, knowledge.Entry{ID: 99, Kind: "sop", Status: "published", Product: "P1", Title: "printer offset", Blocks: []knowledge.Block{{ID: "faq", Text: "P1 procedure printer offset"}}})
	entries = append(entries, knowledge.Entry{ID: 100, Kind: "sop", Status: "published", Scope: "technical", Title: "mixed document", Product: "P10", Blocks: []knowledge.Block{{ID: "one", Text: "printer offset after calibration"}, {ID: "other", Text: "unrelated refund policy"}}})
	got := sources(entries, body)
	found := false
	for _, s := range got {
		if s.EntryID == 100 && s.Scope != "reference" {
			t.Fatal("partial SOP may replace whole document")
		}
		if s.EntryID == 99 {
			t.Fatal("P1 matched P10")
		}
		if s.EntryID < 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("Chinese candidate invisible to original-language query")
	}
}

func TestPrefixWhitelistAndExclusions(t *testing.T) {
	entry := knowledge.Entry{ID: 1, Kind: "sop", Status: "published", Scope: "technical", Title: "Self test procedure", SourceHash: "fixed", ApplicableProducts: []string{"241BT", "2410BT"}, ExcludedProducts: []string{"2410BT"}, Blocks: []knowledge.Block{{ID: "faq", Text: "Use the self test procedure after confirming media."}}}
	for _, tc := range []struct {
		message string
		want    bool
	}{
		{"241BT printer self test procedure", true},
		{"2410BT printer self test procedure", false},
		{"241BT and 2410BT printer self test procedure", false},
		{"2411BT printer self test procedure", false},
		{"printer self test procedure", false},
	} {
		body := wireSnapshot("t", []ticket.Message{{ID: "c", Role: "customer", Visibility: "public", Body: tc.message}})
		got := sources([]knowledge.Entry{entry}, body)
		if (len(got) > 0) != tc.want {
			t.Fatalf("prefix %q: %+v", tc.message, got)
		}
		if len(got) > 0 && (!strings.Contains(got[0].Text, "不适用机型：2410BT") || !strings.Contains(got[0].Text, "适用机型：241BT")) {
			t.Fatal("retrieved source lost applicability context")
		}
	}
	if entry.Blocks[0].Text != "Use the self test procedure after confirming media." {
		t.Fatal("ranking mutated pinned snapshot")
	}
}
