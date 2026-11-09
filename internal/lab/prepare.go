// input: 已脱敏完整会话与指定正式知识版本。
// output: 不含后续信息的模拟节点、确定性分组和隔离后的固定知识。
// pos: 离线批次的数据准备。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package lab

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/ticket"
)

// PrepareSample accepts already-redacted data. Current mutable ticket fields are
// for the review screen only, never for historically simulated model inputs.
func PrepareSample(snapshot ticket.Snapshot, number string) (Sample, error) {
	s := Sample{TicketID: snapshot.ID, TicketNumber: number, Subject: snapshot.Subject, Snapshot: snapshot, Nodes: []Node{}}
	if !snapshot.Complete {
		return s, errors.New("工单会话不完整")
	}
	messages := append([]ticket.Message{}, snapshot.Messages...)
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].CreatedTime < messages[j].CreatedTime })
	public := []ticket.Message{}
	for _, m := range messages {
		if m.Source == "comment" || m.Visibility != "public" || strings.TrimSpace(m.Body) == "" || (m.Status != "" && m.Status != "SUCCESS") {
			continue
		}
		if _, err := time.Parse(time.RFC3339Nano, m.CreatedTime); err != nil {
			return s, errors.New("公开消息时间不明确")
		}
		public = append(public, m)
	}
	sort.SliceStable(public, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, public[i].CreatedTime)
		b, _ := time.Parse(time.RFC3339Nano, public[j].CreatedTime)
		return a.Before(b)
	})
	first := ""
	agentSince := false
	for i, m := range public {
		if m.Role == "agent" {
			agentSince = true
			continue
		}
		if m.Role != "customer" || m.MixedAuthors {
			continue
		}
		if first == "" {
			first = strings.Join(strings.Fields(strings.ToLower(m.Body)), " ")
		}
		if len(s.Nodes) > 0 && !agentSince {
			continue
		}
		cutoff, _ := time.Parse(time.RFC3339Nano, m.CreatedTime)
		valid := true
		prefix := append([]ticket.Message{}, public[:i+1]...)
		for j := range prefix {
			p := &prefix[j]
			// Zoho does not provide the old body of edited messages. Do not reconstruct it.
			if p.ModifiedTime != "" {
				modified, err := time.Parse(time.RFC3339Nano, p.ModifiedTime)
				if err != nil || modified.After(cutoff) {
					valid = false
				}
			}
			p.ModifiedTime = ""
			if j > 0 {
				previous, _ := time.Parse(time.RFC3339Nano, prefix[j-1].CreatedTime)
				current, _ := time.Parse(time.RFC3339Nano, p.CreatedTime)
				if current.Equal(previous) {
					valid = false
				}
			}
		}
		if i+1 < len(public) {
			next, _ := time.Parse(time.RFC3339Nano, public[i+1].CreatedTime)
			if next.Equal(cutoff) {
				valid = false
			}
		}
		if !valid {
			continue
		}
		body := wireSnapshot(snapshot.ID, prefix)
		if len(body) > 512*1024 {
			return s, errors.New("模拟节点超过输入大小限制")
		}
		label := "首次来信"
		if len(s.Nodes) > 0 {
			label = "客服处理后的客户补充"
		}
		s.Nodes = append(s.Nodes, Node{ID: snapshot.ID + ":" + m.ID, CutoffID: m.ID, CutoffTime: m.CreatedTime, Label: label, Ticket: body})
		agentSince = false
		if len(s.Nodes) == 3 {
			break
		}
	}
	if len(s.Nodes) == 0 {
		return s, errors.New("没有作者与时间可确认的客户模拟节点")
	}
	s.Group = Hash(first)
	return s, nil
}

func wireSnapshot(id string, messages []ticket.Message) json.RawMessage {
	body, _ := json.Marshal(map[string]any{"ticket_id": id, "conversation_hash": Hash(messages), "subject": "", "description": "", "product": "", "complete": true, "messages": messages})
	return body
}

// Learning input may contain later outcomes, but only for learning tickets.
func LearningInput(sample Sample) json.RawMessage {
	messages := []ticket.Message{}
	for _, m := range sample.Snapshot.Messages {
		if m.Source != "comment" && m.Visibility == "public" && (m.Status == "" || m.Status == "SUCCESS") {
			messages = append(messages, m)
		}
	}
	return wireSnapshot(sample.TicketID, messages)
}

func Split(samples []Sample) error {
	if len(samples) > MaxTickets {
		return errors.New("每批最多 50 张工单")
	}
	groups := map[string][]int{}
	for i, s := range samples {
		groups[s.Group] = append(groups[s.Group], i)
	}
	if len(groups) < 5 {
		return errors.New("至少需要 5 组不同的有效工单才能分离学习、开发与留出样本")
	}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hold := max(1, len(keys)/5)
	dev := hold
	for i, key := range keys {
		split := "learn"
		if i < hold {
			split = "holdout"
		} else if i < hold+dev {
			split = "development"
		}
		for _, index := range groups[key] {
			samples[index].Split = split
		}
	}
	return nil
}

// LinkGroups unions user-confirmed related cases without splitting automatic
// duplicate groups. Labels are local grouping hints, never sent to a model.
func LinkGroups(samples []Sample, labels map[string]string) error {
	parents := map[string]string{}
	var root func(string) string
	root = func(key string) string {
		if parents[key] == "" {
			parents[key] = key
		}
		if parents[key] != key {
			parents[key] = root(parents[key])
		}
		return parents[key]
	}
	for _, sample := range samples {
		label := strings.TrimSpace(labels[sample.TicketID])
		if len(label) > 160 {
			return errors.New("关联组名称不能超过 160 字节")
		}
		if label == "" {
			continue
		}
		a, b := root(sample.Group), root("manual:"+Hash(label))
		if a > b {
			a, b = b, a
		}
		parents[b] = a
	}
	for i := range samples {
		samples[i].Group = root(samples[i].Group)
	}
	return nil
}

// Current automatic cases and knowledge derived from any batch member are not
// historical baseline knowledge. Exclude entire selected-ticket lineage.
func Baseline(entries []knowledge.Entry, samples []Sample) []knowledge.Entry {
	ids := map[string]bool{}
	for _, s := range samples {
		ids[s.TicketID] = true
	}
	out := []knowledge.Entry{}
	for _, e := range entries {
		if e.Kind != "sop" || e.Status != "published" || ids[e.SourceTicketID] {
			continue
		}
		contaminated := false
		for _, id := range e.SourceTickets {
			if ids[id] {
				contaminated = true
			}
		}
		if !contaminated {
			out = append(out, e)
		}
	}
	return out
}
