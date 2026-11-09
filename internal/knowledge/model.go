// input: 已脱敏案例、带知识库归属/机型条件/来源链的正式 Markdown 版本及检索词。
// output: 带稳定来源定位和适用条件的知识条目与有界词面参考片段。
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
	LibraryID          int64    `json:"library_id"`
	CategoryID         int64    `json:"category_id"`
	Markdown           string   `json:"markdown"`
	MarkdownMigrated   bool     `json:"markdown_migrated,omitempty"`
	ApplicableProducts []string `json:"applicable_products"`
	ExcludedProducts   []string `json:"excluded_products"`
	ReviewFlags        []string `json:"review_flags"`
	ID                 int64    `json:"id"`
	ReplacesID         int64    `json:"replaces_id,omitempty"`
	Kind               string   `json:"kind"`
	Scope              string   `json:"scope"`
	SourceTickets      []string `json:"source_tickets"`
	LabBatchID         int64    `json:"lab_batch_id,omitempty"`
	Status             string   `json:"status"`
	Title              string   `json:"title"`
	Product            string   `json:"product"`
	SourceName         string   `json:"source_name"`
	SourceHash         string   `json:"source_hash"`
	SourceTicketID     string   `json:"source_ticket_id,omitempty"`
	MessageIDs         []string `json:"message_ids"`
	Blocks             []Block  `json:"blocks"`
	CreatedAt          string   `json:"created_at"`
	PublishedAt        string   `json:"published_at,omitempty"`
}

type Source struct {
	ID         string   `json:"id"`
	EntryID    int64    `json:"entry_id"`
	BlockID    string   `json:"block_id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	Scope      string   `json:"scope"`
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
	delete(terms, product)
	minimumMatches := 1
	if len(terms) >= 3 {
		minimumMatches = 2
	}
	code := queryErrorCode(query)
	for _, entry := range entries {
		if entry.Status != "published" || (entry.Kind == "case" && entry.SourceTicketID == excludeTicketID) {
			continue
		}
		if !Applicable(entry, product) {
			continue
		}
		requiredMatches := minimumMatches
		if product != "" && (normalizedProduct(entry.Product) != "" || len(entry.ApplicableProducts) > 0) {
			requiredMatches = 1
		}
		for _, block := range entry.Blocks {
			if code != "" && !containsCode(entry.Title+"\n"+block.Text, code) {
				continue
			}
			body := strings.ToLower(entry.Title + "\n" + block.Text)
			score := 0
			for term := range terms {
				if term != product && strings.Contains(body, term) {
					score++
				}
			}
			if score < requiredMatches {
				continue
			}
			candidates = append(candidates, candidate{Source{
				ID: fmt.Sprintf("knowledge:%d:%s", entry.ID, block.ID), EntryID: entry.ID,
				BlockID: block.ID, Title: entry.Title, Kind: entry.Kind, Scope: entry.Scope, Product: entry.Product,
				Text: ContextText(entry, block.Text), TicketID: entry.SourceTicketID, SourceHash: entry.SourceHash,
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
	stop := map[string]bool{"the": true, "and": true, "for": true, "with": true, "this": true, "that": true, "have": true, "has": true, "was": true, "are": true, "from": true, "can": true, "not": true, "but": true, "you": true, "your": true, "please": true, "how": true, "what": true, "which": true, "when": true, "where": true, "why": true, "does": true, "did": true, "should": true, "would": true, "could": true, "will": true}
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
