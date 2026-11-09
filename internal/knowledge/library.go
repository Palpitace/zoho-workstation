// input: 具名知识库、部门绑定、Markdown 正文及本地嵌入向量。
// output: 分类契约、保留适用条件的文本块和有界混合检索结果。
// pos: 无外部依赖的知识组织与召回规则。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package knowledge

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const ChunkVersion = "markdown-parent-v2"

type Library struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	DepartmentIDs []string `json:"department_ids"`
}

type Category struct {
	ID        int64  `json:"id"`
	LibraryID int64  `json:"library_id"`
	Name      string `json:"name"`
}

type Vector struct {
	EntryID      int64     `json:"entry_id"`
	BlockID      string    `json:"block_id"`
	SourceHash   string    `json:"source_hash"`
	Model        string    `json:"model"`
	ChunkVersion string    `json:"chunk_version"`
	Values       []float32 `json:"values"`
}

// Applicable fails closed for unknown models whenever an exclusion exists.
// Applicability is metadata reviewed with the complete parent FAQ, never guessed
// from a nearby heading or a semantic similarity score.
func Applicable(entry Entry, product string) bool {
	product = normalizedProduct(product)
	if (len(entry.ExcludedProducts) > 0 || len(entry.ApplicableProducts) > 0) && product == "" {
		return false
	}
	for _, excluded := range entry.ExcludedProducts {
		if normalizedProduct(excluded) == product {
			return false
		}
	}
	if len(entry.ApplicableProducts) > 0 {
		found := false
		for _, candidate := range entry.ApplicableProducts {
			if normalizedProduct(candidate) == product {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	expected := normalizedProduct(entry.Product)
	return (expected == "" || expected == product) && (entry.Kind != "case" || (product != "" && expected != ""))
}

var markdownHeading = regexp.MustCompile(`^(#{1,6})[ \t]+(.+)$`)
var fontTag = regexp.MustCompile(`(?is)</?font\b[^>]*>`)
var wrappedHeading = regexp.MustCompile(`(?m)^(#{1,6})[ \t]*\n(?:[ \t]*\n)*([^\n#>!][^\n]*)$`)
var htmlComment = regexp.MustCompile(`(?s)<!--(.*?)-->`)

// CleanMarkdown removes export styling only. Comments containing OCR are retained
// visibly as unverified source material rather than discarded or made authoritative.
func CleanMarkdown(body string) (string, []string) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = fontTag.ReplaceAllString(body, "")
	flags := []string{}
	recovered := wrappedHeading.ReplaceAllString(body, "$1 $2")
	if recovered != body {
		flags = append(flags, "wrapped_heading_requires_review")
	}
	body = recovered
	body = htmlComment.ReplaceAllStringFunc(body, func(comment string) string {
		content := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(comment, "<!--"), "-->"))
		if content == "" {
			return ""
		}
		flags = append(flags, "comment_requires_review")
		return "\n> 待核对的原文注释：\n> " + strings.ReplaceAll(content, "\n", "\n> ") + "\n"
	})
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Trim(trimmed, "# \t") == "" && strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n")), flags
}

// MarkdownBlocks splits only at the shallowest repeated heading level. Every
// child keeps the document's preamble (including model restrictions); subordinate
// steps, tables and bilingual subsections stay together. Oversized semantic units
// require explicit editing rather than silently dropping prerequisites.
func MarkdownBlocks(markdown string) ([]Block, error) {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" || len(markdown) > 512*1024 {
		return nil, errors.New("markdown is empty or exceeds supported limits")
	}
	lines := strings.Split(markdown, "\n")
	counts := map[int]int{}
	inFence := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if !inFence {
			if m := markdownHeading.FindStringSubmatch(line); m != nil {
				counts[len(m[1])]++
			}
		}
	}
	level := 0
	for i := 1; i <= 6; i++ {
		if counts[i] > 1 {
			level = i
			break
		}
	}
	if level == 0 {
		if len(markdown) > 16*1024 {
			return nil, errors.New("semantic section exceeds 16 KiB; split into complete procedures")
		}
		return []Block{{ID: "section-1", Text: markdown}}, nil
	}
	preamble := []string{}
	sections := [][]string{}
	var section []string
	inFence = false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
		}
		m := markdownHeading.FindStringSubmatch(line)
		if !inFence && m != nil && len(m[1]) == level {
			if section != nil {
				sections = append(sections, section)
			}
			section = []string{line}
		} else if section == nil {
			preamble = append(preamble, line)
		} else {
			section = append(section, line)
		}
	}
	if section != nil {
		sections = append(sections, section)
	}
	prefix := strings.TrimSpace(strings.Join(preamble, "\n"))
	blocks := make([]Block, 0, len(sections))
	for i, section := range sections {
		text := strings.TrimSpace(strings.Join(section, "\n"))
		if prefix != "" {
			text = prefix + "\n\n" + text
		}
		if len(text) > 16*1024 {
			return nil, errors.New("semantic section exceeds 16 KiB; split into complete procedures")
		}
		blocks = append(blocks, Block{ID: fmt.Sprintf("section-%d", i+1), Text: text})
	}
	return blocks, nil
}

// HybridSources combines independently ranked lexical and cosine matches using
// reciprocal rank fusion. The caller supplies only department-authorized entries
// and vectors for the selected pinned model/chunk version. Exact version/hash
// checks here prevent stale indexes from reviving unpublished or replaced text.
func HybridSources(entries []Entry, vectors []Vector, queryVector []float32, excludeTicketID, product, query string, limit int) []Source {
	if limit <= 0 {
		return []Source{}
	}
	lexical := SelectSources(entries, excludeTicketID, product, query, 24)
	if len(queryVector) == 0 {
		if len(lexical) > limit {
			return lexical[:limit]
		}
		return lexical
	}
	type ranked struct {
		source Source
		score  float64
	}
	allowed := map[string]Source{}
	code := queryErrorCode(query)
	for _, entry := range entries {
		if entry.Status != "published" || !Applicable(entry, product) || (entry.Kind == "case" && entry.SourceTicketID == excludeTicketID) {
			continue
		}
		for _, block := range entry.Blocks {
			if code != "" && !containsCode(entry.Title+"\n"+block.Text, code) {
				continue
			}
			id := fmt.Sprintf("knowledge:%d:%s", entry.ID, block.ID)
			allowed[id] = Source{ID: id, EntryID: entry.ID, BlockID: block.ID, Title: entry.Title, Kind: entry.Kind, Scope: entry.Scope, Product: entry.Product, Text: ContextText(entry, block.Text), TicketID: entry.SourceTicketID, SourceHash: entry.SourceHash, MessageIDs: entry.MessageIDs}
		}
	}
	semantic := []ranked{}
	for _, vector := range vectors {
		id := fmt.Sprintf("knowledge:%d:%s", vector.EntryID, vector.BlockID)
		source, ok := allowed[id]
		if !ok || source.SourceHash != vector.SourceHash || vector.ChunkVersion != ChunkVersion {
			continue
		}
		score, ok := Cosine(queryVector, vector.Values)
		// A minimum similarity is still only a retrieval heuristic, not factual proof.
		if ok && score >= 0.35 {
			semantic = append(semantic, ranked{source, score})
		}
	}
	sort.SliceStable(semantic, func(i, j int) bool {
		if semantic[i].score == semantic[j].score {
			return semantic[i].source.ID < semantic[j].source.ID
		}
		return semantic[i].score > semantic[j].score
	})
	// Confidence gates the semantic channel, not every useful neighbour. A
	// lower-ranked bilingual source can still provide the relevant evidence.
	if len(semantic) > 0 && semantic[0].score < 0.40 {
		semantic = nil
	}
	if len(semantic) > 24 {
		semantic = semantic[:24]
	}
	merged := map[string]ranked{}
	for i, source := range lexical {
		merged[source.ID] = ranked{source, 2 / float64(1+i+1)}
	}
	for i, item := range semantic {
		r := merged[item.source.ID]
		r.source = item.source
		r.score += 1 / float64(1+i+1)
		merged[item.source.ID] = r
	}
	candidates := make([]ranked, 0, len(merged))
	for _, item := range merged {
		candidates = append(candidates, item)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].source.ID < candidates[j].source.ID
		}
		return candidates[i].score > candidates[j].score
	})
	result := []Source{}
	size := 0
	for _, item := range candidates {
		if len(result) >= min(limit, 24) {
			break
		}
		if size+len(item.source.Text) > 64*1024 {
			continue
		}
		result = append(result, item.source)
		size += len(item.source.Text)
	}
	return result
}

func Cosine(a, b []float32) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, aa, bb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
			return 0, false
		}
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0, false
	}
	return dot / math.Sqrt(aa*bb), true
}

// ContextText keeps reviewed model conditions attached to retrieved evidence.
// It changes the reference view only; the immutable Markdown body stays intact.
func ContextText(entry Entry, text string) string {
	var context []string
	if len(entry.ApplicableProducts) > 0 {
		context = append(context, "适用机型："+strings.Join(entry.ApplicableProducts, "、"))
	}
	if len(entry.ExcludedProducts) > 0 {
		context = append(context, "不适用机型："+strings.Join(entry.ExcludedProducts, "、"))
	}
	if len(context) == 0 {
		return text
	}
	return strings.Join(context, "\n") + "\n\n" + text
}

var markdownImage = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
var markdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
var rawURL = regexp.MustCompile(`https?://[^\s<>]+`)
var ocrLabel = regexp.MustCompile(`(?m)^>.*待核对的原文注释.*$`)

// EmbeddingText removes opaque URL destinations from the indexing view only.
// Visible link/alt text and prose remain; retrieval returns full original evidence.
func EmbeddingText(title, text string) string {
	text = title + "\n" + text
	text = markdownImage.ReplaceAllString(text, "$1")
	text = markdownLink.ReplaceAllString(text, "$1")
	text = rawURL.ReplaceAllString(text, "")
	text = ocrLabel.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

var explicitCode = regexp.MustCompile(`(?i)(?:error\s+code|错误码|错误代码)\s*[:：]?\s*([a-z0-9][a-z0-9_-]*)`)

func queryErrorCode(query string) string {
	match := explicitCode.FindStringSubmatch(query)
	if len(match) > 1 && strings.ContainsAny(match[1], "0123456789") {
		return strings.ToLower(match[1])
	}
	return ""
}
func containsCode(text, code string) bool {
	text = strings.ToLower(text)
	word := func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' }
	for offset := 0; offset < len(text); {
		i := strings.Index(text[offset:], code)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(code)
		left, right := true, true
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:i])
			left = !word(r)
		}
		if end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			right = !word(r)
		}
		if left && right {
			return true
		}
		offset = end
	}
	return false
}
