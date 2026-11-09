// input: 固定历史前缀、带机型适用/排除条件的知识快照、已记录模型结果和人工反馈。
// output: 下一次单独调用、冻结的技术修订候选及批次结束判断。
// pos: 有限离线流程的纯函数编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package lab

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/ticket"
)

type Step struct {
	Key, Task, NodeID, Variant, Phase string
	Revision                          int
	Input                             Input
}
type Candidate struct {
	Active   bool            `json:"active"`
	Key      string          `json:"key"`
	TicketID string          `json:"ticket_id"`
	Proposal Proposal        `json:"proposal"`
	Entry    knowledge.Entry `json:"entry"`
}

// Next is deterministic over the persisted batch. One failed call can be retried
// once; a twice-failed step is retained and skipped rather than looping forever.
func Next(b Batch) *Step {
	for _, s := range b.Spec.Samples {
		if s.Split != "learn" {
			continue
		}
		step := Step{Key: "learn:" + s.TicketID, Task: "propose", NodeID: s.TicketID, Variant: "learning", Phase: "learning", Input: Input{Ticket: LearningInput(s), Sources: sources(b.Spec.Knowledge, LearningInput(s)), Feedback: []Feedback{}}}
		if !settled(b.State.Calls, step.Key) {
			return &step
		}
	}
	for _, s := range b.Spec.Samples {
		if s.Split != "development" {
			continue
		}
		for _, node := range s.Nodes {
			step, reply, evaluation := dialogue(b, node, "candidate", true)
			if step != nil {
				return step
			}
			if reply == nil || evaluation == nil {
				continue
			}
			in := inputFor(b, node, "candidate")
			in.Reply = reply
			in.Evaluation = evaluation
			proposal := Step{Key: "improve:" + node.ID, Task: "propose", NodeID: node.ID, Variant: "development", Phase: "development", Input: in}
			if !settled(b.State.Calls, proposal.Key) {
				return &proposal
			}
		}
	}
	for _, s := range b.Spec.Samples {
		if s.Split != "holdout" {
			continue
		}
		for _, node := range s.Nodes {
			for _, variant := range []string{"baseline", "candidate"} {
				if step, _, _ := dialogue(b, node, variant, false); step != nil {
					return step
				}
			}
		}
	}
	return nil
}

func dialogue(b Batch, node Node, variant string, improve bool) (*Step, *Reply, *Evaluation) {
	in := inputFor(b, node, variant)
	phase := "holdout"
	limit := 0
	if improve {
		phase = "development"
		limit = MaxRewrites
	}
	var reply *Reply
	var evaluation *Evaluation
	for revision := 0; revision <= limit; revision++ {
		key := fmt.Sprintf("%s:%s:%d", node.ID, variant, revision)
		step := Step{Key: key + ":reply", Task: "reply", NodeID: node.ID, Variant: variant, Phase: phase, Revision: revision, Input: in}
		if !settled(b.State.Calls, step.Key) {
			return &step, nil, nil
		}
		saved := successful(b.State.Calls, step.Key)
		if saved == nil {
			return nil, nil, nil
		}
		var value Reply
		if json.Unmarshal(saved.Result, &value) != nil {
			return nil, nil, nil
		}
		reply = &value
		// Evaluator gets a fresh context: same prefix/sources and current reply, no
		// previous model verdicts or human scores to bias its independent judgment.
		evalInput := inputFor(b, node, variant)
		evalInput.Reply = reply
		evalInput.Feedback = []Feedback{}
		step = Step{Key: key + ":evaluate", Task: "evaluate", NodeID: node.ID, Variant: variant, Phase: phase, Revision: revision, Input: evalInput}
		if !settled(b.State.Calls, step.Key) {
			return &step, nil, nil
		}
		saved = successful(b.State.Calls, step.Key)
		if saved == nil {
			return nil, reply, nil
		}
		var score Evaluation
		if json.Unmarshal(saved.Result, &score) != nil {
			return nil, reply, nil
		}
		evaluation = &score
		if score.Verdict == "pass" {
			break
		}
		in.Reply = reply
		in.Evaluation = evaluation
	}
	return nil, reply, evaluation
}

func settled(calls []Call, key string) bool {
	count := 0
	for _, c := range calls {
		if c.Key == key {
			if c.Status == "success" || c.Status == "running" {
				return true
			}
			count++
		}
	}
	return count >= 2
}
func successful(calls []Call, key string) *Call {
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Key == key && calls[i].Status == "success" {
			return &calls[i]
		}
	}
	return nil
}

func inputFor(b Batch, node Node, variant string) Input {
	entries := append([]knowledge.Entry{}, b.Spec.Knowledge...)
	if variant == "candidate" {
		candidates := Candidates(b)
		replaced := map[int64]bool{}
		// Candidates already marks superseded revisions; consume that one rule.
		chosen := []knowledge.Entry{}
		for _, c := range candidates {
			if !c.Active {
				continue
			}
			if c.Proposal.ReplacesID != 0 {
				replaced[c.Proposal.ReplacesID] = true
			}
			chosen = append(chosen, c.Entry)
		}
		kept := []knowledge.Entry{}
		for _, e := range entries {
			if !replaced[e.ID] {
				kept = append(kept, e)
			}
		}
		entries = append(kept, chosen...)
	}
	feedback := []Feedback{}
	for _, f := range b.Spec.Feedback {
		if f.NodeID == node.ID {
			feedback = append(feedback, f)
		}
	}
	return Input{Ticket: node.Ticket, Sources: sources(entries, node.Ticket), Feedback: feedback}
}

func sources(entries []knowledge.Entry, body json.RawMessage) []knowledge.Source {
	var snap struct {
		Messages []ticket.Message `json:"messages"`
	}
	_ = json.Unmarshal(body, &snap)
	text := ""
	for _, m := range snap.Messages {
		text += "\n" + m.Body
	}
	var official, experimental []knowledge.Entry
	products := map[int64]string{}
	blockCounts := map[int64]int{}
	rankingText := strings.ToLower(text)
	for _, e := range entries {
		product := strings.TrimSpace(e.Product)
		if !prefixApplicable(e, text) {
			continue
		}
		products[e.ID] = e.Product
		blockCounts[e.ID] = len(e.Blocks)
		if product != "" {
			rankingText = strings.ReplaceAll(rankingText, strings.ToLower(product), " ")
		}
		// Carry reviewed model conditions into the cited text before ranking.
		e.Blocks = append([]knowledge.Block{}, e.Blocks...)
		for i := range e.Blocks {
			e.Blocks[i].Text = knowledge.ContextText(e, e.Blocks[i].Text)
		}
		e.ApplicableProducts = nil
		e.ExcludedProducts = nil
		// Applicability was checked against the prefix; rank all compatible
		// entries together instead of stopping at the first document matches.
		e.Product = ""
		if e.ID < 0 {
			experimental = append(experimental, e)
		} else {
			official = append(official, e)
		}
	}
	candidates := knowledge.SelectSources(experimental, "", "", rankingText, 4)
	baseline := knowledge.SelectSources(official, "", "", rankingText, 8-len(candidates))
	selected := append(candidates, baseline...)
	counts := map[int64]int{}
	for _, s := range selected {
		counts[s.EntryID]++
	}
	for i := range selected {
		selected[i].Product = products[selected[i].EntryID]
		if selected[i].Scope == "technical" && counts[selected[i].EntryID] < blockCounts[selected[i].EntryID] {
			selected[i].Scope = "reference"
		}
	}
	return selected
}

// prefixApplicable uses only historical message text. A bare exclusion with no
// positive model identification cannot establish which procedure is applicable.
func prefixApplicable(entry knowledge.Entry, text string) bool {
	known := false
	if product := strings.TrimSpace(entry.Product); product != "" {
		if !modelMention(text, product) {
			return false
		}
		known = true
	}
	if len(entry.ApplicableProducts) > 0 {
		matched := false
		for _, product := range entry.ApplicableProducts {
			if modelMention(text, product) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
		known = true
	}
	for _, product := range entry.ExcludedProducts {
		if modelMention(text, product) {
			return false
		}
	}
	return len(entry.ExcludedProducts) == 0 || known
}

func modelMention(text, model string) bool {
	text = strings.ToLower(text)
	model = strings.ToLower(model)
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], model)
		if index < 0 {
			return false
		}
		index += offset
		before := []rune(text[:index])
		after := []rune(text[index+len(model):])
		boundary := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
		if (len(before) == 0 || boundary(before[len(before)-1])) && (len(after) == 0 || boundary(after[0])) {
			return true
		}
		offset = index + len(model)
	}
	return false
}

func Candidates(b Batch) []Candidate {
	out := []Candidate{}
	for _, call := range b.State.Calls {
		if call.Task != "propose" || call.Status != "success" {
			continue
		}
		var proposals Proposals
		if json.Unmarshal(call.Result, &proposals) != nil {
			continue
		}
		var in Input
		_ = json.Unmarshal(call.Input, &in)
		var snap struct {
			ID       string           `json:"ticket_id"`
			Messages []ticket.Message `json:"messages"`
		}
		_ = json.Unmarshal(in.Ticket, &snap)
		for i, p := range proposals.Items {
			key := fmt.Sprintf("%d:%d", call.ID, i)
			// Negative IDs identify experimental entries, never persisted publications.
			entry := knowledge.Entry{ID: -1000000 + int64(call.ID*10+i+1), Kind: "sop", Status: "published", Scope: "technical", Title: p.Title, Product: p.Product, SourceTicketID: snap.ID, SourceTickets: []string{snap.ID}, LabBatchID: b.ID, MessageIDs: p.MessageIDs, SourceHash: Hash(p), Blocks: []knowledge.Block{{ID: "faq", Text: ProposalText(p)}}}
			// Original-language reported symptoms improve literal cross-language
			// retrieval, without promoting an actual later reply to a gold answer.
			for _, m := range snap.Messages {
				if m.Role == "customer" {
					words := []rune(m.Body)
					if len(words) > 512 {
						words = words[:512]
					}
					entry.Blocks[0].Text += "\n\n客户问题原文节选（仅为报告，非已确认结论）：\n" + string(words)
					break
				}
			}
			out = append(out, Candidate{Active: true, Key: key, TicketID: snap.ID, Proposal: p, Entry: entry})
		}
	}
	replaced := map[int64]bool{}
	for i := len(out) - 1; i >= 0; i-- {
		id := out[i].Proposal.ReplacesID
		if id != 0 {
			out[i].Active = !replaced[id]
			replaced[id] = true
		}
	}
	return out
}

func ProposalText(p Proposal) string {
	return "问题：" + p.Question + "\n\n解答与排查：" + p.Answer + "\n\n适用条件：" + p.Conditions + "\n\n修订原因：" + p.Reason
}

// Feedback is consumed only by a new manually created batch with unchanged
// partitions. Holdout ratings remain visible but are never training input.
func DevelopmentFeedback(b Batch) []Feedback {
	nodes := map[string]bool{}
	for _, s := range b.Spec.Samples {
		if s.Split == "development" {
			for _, n := range s.Nodes {
				nodes[n.ID] = true
			}
		}
	}
	combined := append(append([]Feedback{}, b.Spec.Feedback...), b.State.Feedback...)
	out := []Feedback{}
	counts := map[string]int{}
	sizes := map[string]int{}
	seen := map[string]bool{}
	for i := len(combined) - 1; i >= 0; i-- {
		f := combined[i]
		if f.BatchID == 0 {
			f.BatchID = b.ID
		}
		key := fmt.Sprintf("%d:%d", f.BatchID, f.ID)
		encoded, _ := json.Marshal(f)
		if nodes[f.NodeID] && !seen[key] && counts[f.NodeID] < 10 && sizes[f.NodeID]+len(encoded) <= 128*1024 {
			sizes[f.NodeID] += len(encoded)
			out = append(out, f)
			seen[key] = true
			counts[f.NodeID]++
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ValidationComplete checks execution, not whether the evaluator's conclusions
// are true. Publishing still requires human review even if every call succeeded.
func ValidationComplete(b Batch) bool {
	nodes := 0
	for _, s := range b.Spec.Samples {
		if s.Split != "holdout" {
			continue
		}
		for _, n := range s.Nodes {
			nodes++
			for _, variant := range []string{"baseline", "candidate"} {
				if successful(b.State.Calls, n.ID+":"+variant+":0:evaluate") == nil {
					return false
				}
			}
		}
	}
	return b.State.Status == "completed" && nodes > 0
}
