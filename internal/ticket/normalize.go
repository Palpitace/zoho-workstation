// input: Zoho 工单详情、完整线程、备注及可选联系人 JSON。
// output: 保留消息来源、角色、顺序和完整性信息的文本快照。
// pos: Zoho 原始证据与本地工单视图的转换边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package ticket

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type RawSnapshot struct {
	Ticket   json.RawMessage            `json:"ticket"`
	Threads  []json.RawMessage          `json:"threads"`
	Comments []json.RawMessage          `json:"comments"`
	Contact  json.RawMessage            `json:"contact,omitempty"`
	Complete bool                       `json:"complete"`
	Warnings []string                   `json:"warnings,omitempty"`
	Evidence map[string]json.RawMessage `json:"evidence,omitempty"`
}

type Snapshot struct {
	ID           string    `json:"id"`
	Subject      string    `json:"subject"`
	Description  string    `json:"description"`
	DepartmentID string    `json:"department_id"`
	Status       string    `json:"status"`
	Priority     string    `json:"priority"`
	Product      string    `json:"product"`
	CreatedTime  string    `json:"created_time"`
	ModifiedTime string    `json:"modified_time"`
	Messages     []Message `json:"messages"`
	Complete     bool      `json:"complete"`
	Warnings     []string  `json:"warnings,omitempty"`
}

type Message struct {
	ID                  string       `json:"id"`
	Source              string       `json:"source"`
	CreatedTime         string       `json:"created_time"`
	ModifiedTime        string       `json:"modified_time,omitempty"`
	Role                string       `json:"role"`
	MixedAuthors        bool         `json:"mixed_authors,omitempty"`
	Visibility          string       `json:"visibility"`
	Status              string       `json:"status,omitempty"`
	Direction           string       `json:"direction,omitempty"`
	IsDescriptionThread bool         `json:"is_description_thread"`
	Body                string       `json:"body"`
	Attachments         []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	URL         string `json:"url,omitempty"`
}

// Normalize never replaces missing content with the thread-list summary.
func Normalize(raw RawSnapshot) (Snapshot, error) {
	t, err := object(raw.Ticket)
	if err != nil {
		return Snapshot{}, errors.New("invalid ticket object")
	}
	s := Snapshot{
		ID: scalar(t["id"]), Subject: scalar(t["subject"]), Description: readableHTML(scalar(t["description"])),
		DepartmentID: scalar(t["departmentId"]), Status: scalar(t["status"]), Priority: scalar(t["priority"]),
		Product: scalar(t["productId"]), Complete: raw.Complete, Warnings: append([]string(nil), raw.Warnings...),
		Messages: []Message{},
	}
	if s.ID == "" {
		return Snapshot{}, errors.New("ticket ID is missing")
	}
	if product, ok := t["product"].(map[string]any); ok && scalar(product["productName"]) != "" {
		s.Product = scalar(product["productName"])
	}
	s.CreatedTime = s.normalizedTime(scalar(t["createdTime"]), "ticket_created_time")
	s.ModifiedTime = s.normalizedTime(scalar(t["modifiedTime"]), "ticket_modified_time")
	seen := map[string]int{}
	for _, group := range []struct {
		source   string
		messages []json.RawMessage
	}{{"thread", raw.Threads}, {"comment", raw.Comments}} {
		for _, body := range group.messages {
			obj, err := object(body)
			if err != nil {
				return Snapshot{}, fmt.Errorf("invalid %s object", group.source)
			}
			m := s.message(obj, group.source)
			key := m.Source + ":" + m.ID
			if index, ok := seen[key]; ok && m.ID != "" {
				s.Messages[index] = m
				continue
			}
			seen[key] = len(s.Messages)
			s.Messages = append(s.Messages, m)
		}
	}
	sort.SliceStable(s.Messages, func(i, j int) bool {
		a, b := s.Messages[i].CreatedTime, s.Messages[j].CreatedTime
		if a == "" || b == "" {
			return a != "" && b == ""
		}
		ta, _ := time.Parse(time.RFC3339Nano, a)
		tb, _ := time.Parse(time.RFC3339Nano, b)
		return ta.Before(tb)
	})
	return s, nil
}

func (s *Snapshot) message(obj map[string]any, source string) Message {
	m := Message{ID: scalar(obj["id"]), Source: source, Role: "unknown", Visibility: "unknown"}
	if m.ID == "" {
		s.incomplete("message_id_missing")
	}
	dateKey, authorKey := "createdTime", "author"
	if source == "comment" {
		dateKey, authorKey = "commentedTime", "commenter"
	}
	m.CreatedTime = s.normalizedTime(scalar(obj[dateKey]), "message_time")
	if modified := scalar(obj["modifiedTime"]); modified != "" {
		m.ModifiedTime = s.normalizedTime(modified, "message_modified_time")
	}
	m.Status, m.Direction = scalar(obj["status"]), scalar(obj["direction"])
	m.IsDescriptionThread, _ = obj["isDescriptionThread"].(bool)
	if author, ok := obj[authorKey].(map[string]any); ok {
		switch scalar(author["type"]) {
		case "END_USER":
			m.Role = "customer"
		case "AGENT":
			m.Role = "agent"
		}
	}
	if source == "comment" {
		if public, ok := obj["isPublic"].(bool); ok {
			m.Visibility = "private"
			if public {
				m.Visibility = "public"
			}
		}
	} else if visibility := scalar(obj["visibility"]); visibility == "public" || visibility == "private" {
		m.Visibility = visibility
	}
	// ONLINE_CHAT can embed both speakers; its thread author is not attribution
	// for every line of the exported conversation.
	if strings.EqualFold(scalar(obj["channel"]), "ONLINE_CHAT") {
		m.Role, m.MixedAuthors = "unknown", true
	}
	content, hasContent := obj["content"].(string)
	if !hasContent {
		s.incomplete("message_content_missing")
	}
	if truncated, _ := obj["isContentTruncated"].(bool); truncated {
		s.incomplete("message_content_truncated")
	}
	switch strings.ToLower(scalar(obj["contentType"])) {
	case "html", "text/html":
		m.Body = readableHTML(content)
	default:
		m.Body = strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	}
	if values, ok := obj["attachments"].([]any); ok {
		for _, value := range values {
			item, ok := value.(map[string]any)
			if !ok {
				s.incomplete("attachment_metadata_invalid")
				continue
			}
			size, _ := strconv.ParseInt(scalar(item["size"]), 10, 64)
			m.Attachments = append(m.Attachments, Attachment{ID: scalar(item["id"]), Name: scalar(item["name"]), ContentType: scalar(item["contentType"]), Size: size, URL: scalar(item["href"])})
		}
	}
	if count, err := strconv.Atoi(scalar(obj["attachmentCount"])); err == nil && count > len(m.Attachments) {
		s.incomplete("attachment_metadata_incomplete")
	}
	return m
}

func (s *Snapshot) normalizedTime(value, field string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		s.incomplete(field + "_missing_or_invalid")
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *Snapshot) incomplete(reason string) {
	s.Complete = false
	for _, existing := range s.Warnings {
		if existing == reason {
			return
		}
	}
	s.Warnings = append(s.Warnings, reason)
}

func object(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var obj map[string]any
	if err := decoder.Decode(&obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("object missing")
	}
	return obj, nil
}

func scalar(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	default:
		return ""
	}
}

// HTML text remains in source order; signatures and quoted replies are retained.
func readableHTML(input string) string {
	z := html.NewTokenizer(strings.NewReader(input))
	var out strings.Builder
	skip := ""
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return strings.TrimSpace(out.String())
			}
			lines := strings.Split(strings.ReplaceAll(out.String(), "\r", ""), "\n")
			clean := make([]string, 0, len(lines))
			for _, line := range lines {
				if value := strings.Join(strings.Fields(line), " "); value != "" {
					clean = append(clean, value)
				}
			}
			return strings.Join(clean, "\n")
		case html.TextToken:
			if skip == "" {
				out.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			token := z.Token()
			if token.Data == "script" || token.Data == "style" {
				if token.Type == html.EndTagToken {
					skip = ""
				} else {
					skip = token.Data
				}
				continue
			}
			if skip != "" {
				continue
			}
			switch token.Data {
			case "br", "p", "div", "li", "tr", "blockquote", "pre", "h1", "h2", "h3", "h4", "h5", "h6":
				out.WriteByte('\n')
			case "td", "th":
				out.WriteByte('\t')
			case "img":
				if token.Type != html.EndTagToken {
					for _, a := range token.Attr {
						if a.Key == "alt" && a.Val != "" {
							out.WriteString("[" + a.Val + "]")
						}
					}
				}
			}
		}
	}
}
