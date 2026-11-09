// input: 已脱敏案例、经人工发布的 SOP 和当前工单的机型/检索词。
// output: 带稳定来源定位的知识条目与有限、机型相容的参考片段。
// pos: 无数据库或模型依赖的知识契约与词面检索。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package knowledge

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type Block struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type Entry struct {
	ID             int64    `json:"id"`
	ReplacesID     int64    `json:"replaces_id,omitempty"`
	Kind           string   `json:"kind"`
	Status         string   `json:"status"`
	Title          string   `json:"title"`
	Product        string   `json:"product"`
	SourceName     string   `json:"source_name"`
	SourceHash     string   `json:"source_hash"`
	SourceTicketID string   `json:"source_ticket_id,omitempty"`
	MessageIDs     []string `json:"message_ids"`
	Blocks         []Block  `json:"blocks"`
	CreatedAt      string   `json:"created_at"`
	PublishedAt    string   `json:"published_at,omitempty"`
}

type Source struct {
	ID         string   `json:"id"`
	EntryID    int64    `json:"entry_id"`
	BlockID    string   `json:"block_id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	Product    string   `json:"product"`
	Text       string   `json:"text"`
	TicketID   string   `json:"ticket_id,omitempty"`
	SourceHash string   `json:"source_hash"`
	MessageIDs []string `json:"message_ids"`
}

// SelectSources uses literal keywords rather than claiming semantic or multilingual
// matching. A product match alone cannot turn an unrelated block into evidence.
func SelectSources(entries []Entry, excludeTicketID, product, query string, limit int) []Source {
	if limit <= 0 {
		return []Source{}
	}
	limit = min(limit, 24)
	type candidate struct {
		source Source
		score  int
	}
	var candidates []candidate
	product = normalizedProduct(product)
	terms := keywords(query)
	for _, entry := range entries {
		if entry.Status != "published" || (entry.Kind == "case" && entry.SourceTicketID == excludeTicketID) {
			continue
		}
		entryProduct := normalizedProduct(entry.Product)
		if entryProduct != "" && entryProduct != product {
			continue
		}
		// An unknown-model case cannot establish compatibility with a known model.
		if entry.Kind == "case" && (product == "" || entryProduct == "") {
			continue
		}
		for _, block := range entry.Blocks {
			body := strings.ToLower(entry.Title + "\n" + block.Text)
			score := 0
			for term := range terms {
				if term != product && strings.Contains(body, term) {
					score++
				}
			}
			if score == 0 {
				continue
			}
			candidates = append(candidates, candidate{Source{
				ID: fmt.Sprintf("knowledge:%d:%s", entry.ID, block.ID), EntryID: entry.ID,
				BlockID: block.ID, Title: entry.Title, Kind: entry.Kind, Product: entry.Product,
				Text: block.Text, TicketID: entry.SourceTicketID, SourceHash: entry.SourceHash,
				MessageIDs: entry.MessageIDs,
			}, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.source.Kind != b.source.Kind {
			return a.source.Kind == "sop"
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.source.EntryID > b.source.EntryID
	})
	selected, size := []Source{}, 0
	for _, candidate := range candidates {
		if len(selected) == limit {
			break
		}
		if size+len(candidate.source.Text) > 64*1024 {
			continue
		}
		selected = append(selected, candidate.source)
		size += len(candidate.source.Text)
	}
	return selected
}

func normalizedProduct(product string) string {
	product = strings.ToLower(strings.TrimSpace(product))
	switch product {
	case "unknown", "others", "other", "未知", "未提供", "未确认", "其他":
		return ""
	}
	return product
}

func keywords(query string) map[string]bool {
	terms := map[string]bool{}
	stop := map[string]bool{"the": true, "and": true, "for": true, "with": true, "this": true, "that": true, "have": true, "has": true, "was": true, "are": true, "from": true, "can": true, "not": true, "but": true, "you": true, "your": true, "please": true}
	var word, han []rune
	flush := func() {
		if len(word) >= 3 && !stop[string(word)] {
			terms[string(word)] = true
		}
		for i := 1; i < len(han); i++ {
			terms[string(han[i-1:i+1])] = true
		}
		word, han = nil, nil
	}
	for _, r := range strings.ToLower(query) {
		switch {
		case unicode.Is(unicode.Han, r):
			if len(word) > 0 {
				flush()
			}
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-':
			if len(han) > 0 {
				flush()
			}
			word = append(word, r)
		default:
			flush()
		}
	}
	flush()
	return terms
}
