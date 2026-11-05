// input: 已保存原始线程 HTML 及完整规范化正文。
// output: 仅供展示的原文分段与可折叠引用，不修改正文、证据或模型 Hash。
// pos: 工单详情的邮件引用展示派生。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"bytes"
	"encoding/json"
	"strings"

	"golang.org/x/net/html"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
)

type MessageSegment struct {
	Text     string           `json:"text,omitempty"`
	Quote    bool             `json:"quote,omitempty"`
	Children []MessageSegment `json:"children,omitempty"`
}

func displayMessageSegments(record store.Record) map[string][]MessageSegment {
	raw := map[string]string{}
	for _, group := range []struct {
		source string
		items  []json.RawMessage
	}{{"thread", record.Raw.Threads}, {"comment", record.Raw.Comments}} {
		for _, item := range group.items {
			var value struct {
				ID      json.Number `json:"id"`
				Content string      `json:"content"`
				Type    string      `json:"contentType"`
			}
			if json.Unmarshal(item, &value) == nil && (strings.EqualFold(value.Type, "html") || strings.EqualFold(value.Type, "text/html")) {
				raw[group.source+":"+value.ID.String()] = value.Content
			}
		}
	}
	result := map[string][]MessageSegment{}
	for _, message := range record.Messages {
		key := message.Source + ":" + message.ID
		body := message.Body
		var parts []MessageSegment
		if source := raw[key]; source != "" && displayText(source) == body {
			if root, err := html.Parse(strings.NewReader(source)); err == nil {
				parts = splitHTMLQuotes(body, root)
			}
		}
		if len(parts) == 0 {
			parts = splitTextQuotes(body)
		}
		if hasQuote(parts) {
			result[key] = parts
		}
	}
	return result
}

// Reuse the business text conversion, rather than introducing a second HTML sanitizer.
func displayText(source string) string {
	message, _ := json.Marshal(map[string]string{"id": "display", "contentType": "html", "content": source})
	snapshot, err := ticket.Normalize(ticket.RawSnapshot{Ticket: json.RawMessage(`{"id":"display"}`), Threads: []json.RawMessage{message}})
	if err != nil || len(snapshot.Messages) != 1 {
		return ""
	}
	return snapshot.Messages[0].Body
}

func quoteElement(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	if node.Data == "blockquote" {
		return true
	}
	for _, attr := range node.Attr {
		if attr.Key != "class" && attr.Key != "id" {
			continue
		}
		for _, name := range strings.Fields(strings.ToLower(attr.Val)) {
			switch name {
			case "gmail_quote", "gmail_quote_container", "divrplyfwdmsg", "zmail_extra", "yahoo_quoted":
				return true
			}
		}
	}
	return false
}

func splitHTMLQuotes(body string, root *html.Node) []MessageSegment {
	var quotes []*html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if quoteElement(child) {
				quotes = append(quotes, child)
			} else {
				visit(child)
			}
		}
	}
	visit(root)
	var parts []MessageSegment
	rest := body
	for _, node := range quotes {
		var markup bytes.Buffer
		if html.Render(&markup, node) != nil {
			continue
		}
		text := displayText(markup.String())
		// Ambiguous repeated text must remain visible; never guess which occurrence is quoted.
		if text == "" || strings.Count(rest, text) != 1 {
			continue
		}
		at := strings.Index(rest, text)
		if at > 0 {
			parts = append(parts, MessageSegment{Text: rest[:at]})
		}
		nested := splitHTMLQuotes(text, node)
		if len(nested) == 0 {
			nested = []MessageSegment{{Text: text}}
		}
		parts = append(parts, MessageSegment{Quote: true, Children: nested})
		rest = rest[at+len(text):]
	}
	if len(parts) == 0 {
		return nil
	}
	if rest != "" {
		parts = append(parts, MessageSegment{Text: rest})
	}
	return parts
}

// Only explicit > prefixes are folded. Language-dependent headers alone do not
// prove that all following text is old; unprefixed inline replies stay visible.
func splitTextQuotes(body string) []MessageSegment {
	return splitQuotedLines(strings.SplitAfter(body, "\n"), 0)
}

func splitQuotedLines(lines []string, depth int) []MessageSegment {
	var parts []MessageSegment
	for i := 0; i < len(lines); {
		if quoteDepth(lines[i]) > depth {
			end := i + 1
			for end < len(lines) && quoteDepth(lines[end]) > depth {
				end++
			}
			parts = append(parts, MessageSegment{Quote: true, Children: splitQuotedLines(lines[i:end], depth+1)})
			i = end
		} else {
			if len(parts) > 0 && !parts[len(parts)-1].Quote {
				parts[len(parts)-1].Text += lines[i]
			} else {
				parts = append(parts, MessageSegment{Text: lines[i]})
			}
			i++
		}
	}
	return parts
}

func quoteDepth(line string) int {
	depth := 0
	for {
		line = strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(line, ">") {
			return depth
		}
		depth++
		line = line[1:]
	}
}

func hasQuote(parts []MessageSegment) bool {
	for _, part := range parts {
		if part.Quote {
			return true
		}
	}
	return false
}
