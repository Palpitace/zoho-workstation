// input: 规范化快照及仅用于识别已知个人信息的原始响应。
// output: 保留工单事实和引用 ID 的独立脱敏副本。
// pos: 发送模型前的本地数据最小化边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package ticket

import (
	"encoding/json"
	"html"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	deviceIdentifierPattern   = regexp.MustCompile(`(?i)("(?:sn|serial|serialNumber|mac|imei)"\s*:\s*")[^"]*(")`)
	emailPattern              = regexp.MustCompile(`[\pL\pN._%+'-]+@[\pL\pN.-]+\.[\pL]{2,}`)
	phoneLabelPattern         = regexp.MustCompile(`(?im)((?:phone|telephone|tel|mobile|电话|手机)\s*[:：]\s*)[+()0-9][0-9 ()+.-]{6,}[0-9]`)
	internationalPhonePattern = regexp.MustCompile(`\+[1-9][0-9](?:[ .()-]?[0-9]){6,13}`)
	formattedPhonePattern     = regexp.MustCompile(`\b[0-9]{3}[- .][0-9]{3}[- .][0-9]{4}\b`)
	nameLabelPattern          = regexp.MustCompile(`(?im)^(\s*(?:name|customer name|姓名|联系人)\s*[:：]\s*)[^\n]+`)
	addressLabelPattern       = regexp.MustCompile(`(?im)^(\s*(?:address|shipping address|billing address|地址|收货地址|联系地址)\s*[:：]\s*)[^\n]+`)
	mailDisplayNamePattern    = regexp.MustCompile(`[\pL][\pL .'’-]{1,80}\s*<[\pL\pN._%+'-]+@[\pL\pN.-]+\.[\pL]{2,}>`)
	visitorFieldPattern       = regexp.MustCompile(`(?im)^([ \t]*(?:name|customer name|phone|telephone|mobile|address|shipping address|姓名|联系人|地址|收货地址|IP|IP address|Latitude|Longitude|City|State-Province-Region|Chat ID)[ \t]*[:：]?[ \t]*)\r?\n[^\r\n]+`)
)

// RedactedSnapshot is best-effort redaction, not a guarantee of anonymity.
// Evidence, author contact objects, and attachment URLs never enter this copy.
func RedactedSnapshot(s Snapshot, raw RawSnapshot) Snapshot {
	redact := redactor(raw)
	out := s
	out.Subject, out.Description, out.Product = redact(s.Subject), redact(s.Description), redact(s.Product)
	out.Warnings = append([]string(nil), s.Warnings...)
	out.Messages = make([]Message, len(s.Messages))
	for i, m := range s.Messages {
		out.Messages[i] = m
		out.Messages[i].Body = redact(m.Body)
		out.Messages[i].Attachments = make([]Attachment, len(m.Attachments))
		for j, a := range m.Attachments {
			out.Messages[i].Attachments[j] = a
			out.Messages[i].Attachments[j].Name = redact(a.Name)
			out.Messages[i].Attachments[j].URL = ""
		}
	}
	return out
}

func redactor(raw RawSnapshot) func(string) string {
	values := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if utf8.RuneCountInString(value) >= 2 {
			values[value] = true
		}
	}
	person := func(obj map[string]any) {
		for _, key := range []string{"name", "firstName", "lastName", "email", "secondaryEmail", "phone", "mobile", "street", "address"} {
			add(scalar(obj[key]))
		}
		first, last := scalar(obj["firstName"]), scalar(obj["lastName"])
		if first != "" && last != "" {
			add(first + " " + last)
		}
	}
	if obj, err := object(raw.Contact); err == nil {
		person(obj)
	}
	if obj, err := object(raw.Ticket); err == nil {
		for _, key := range []string{"email", "phone"} {
			add(scalar(obj[key]))
		}
		for _, key := range []string{"contact", "assignee"} {
			if p, ok := obj[key].(map[string]any); ok {
				person(p)
			}
		}
	}
	for _, group := range [][]json.RawMessage{raw.Threads, raw.Comments} {
		for _, body := range group {
			obj, err := object(body)
			if err != nil {
				continue
			}
			for _, key := range []string{"author", "commenter", "impersonatedUser"} {
				if p, ok := obj[key].(map[string]any); ok {
					person(p)
				}
			}
			for _, key := range []string{"fromEmailAddress", "to", "cc", "bcc", "replyTo"} {
				if addresses, err := mail.ParseAddressList(scalar(obj[key])); err == nil {
					for _, address := range addresses {
						add(address.Name)
						add(address.Address)
					}
				}
			}
		}
	}
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) == len(ordered[j]) {
			return ordered[i] < ordered[j]
		}
		return len(ordered[i]) > len(ordered[j])
	})
	patterns := make([]*regexp.Regexp, 0, len(ordered))
	for _, value := range ordered {
		patterns = append(patterns, regexp.MustCompile(`(?i)`+regexp.QuoteMeta(value)))
	}
	return func(text string) string {
		text = html.UnescapeString(text)
		text = deviceIdentifierPattern.ReplaceAllString(text, "${1}[DEVICE_ID]${2}")
		text = visitorFieldPattern.ReplaceAllString(text, "${1}\n[PERSONAL]")
		text = mailDisplayNamePattern.ReplaceAllString(text, "[PERSON] <[EMAIL]>")
		text = nameLabelPattern.ReplaceAllString(text, "${1}[PERSON]")
		text = addressLabelPattern.ReplaceAllString(text, "${1}[ADDRESS]")
		text = phoneLabelPattern.ReplaceAllString(text, "${1}[PHONE]")
		text = emailPattern.ReplaceAllString(text, "[EMAIL]")
		text = internationalPhonePattern.ReplaceAllString(text, "[PHONE]")
		text = formattedPhonePattern.ReplaceAllString(text, "[PHONE]")
		for _, pattern := range patterns {
			text = replaceKnown(text, pattern)
		}
		return text
	}
}

// A known short name must not erase the same letters inside a product or word.
func replaceKnown(text string, pattern *regexp.Regexp) string {
	var out strings.Builder
	start := 0
	for _, span := range pattern.FindAllStringIndex(text, -1) {
		before, after := rune(0), rune(0)
		if span[0] > 0 {
			before, _ = utf8.DecodeLastRuneInString(text[:span[0]])
		}
		if span[1] < len(text) {
			after, _ = utf8.DecodeRuneInString(text[span[1]:])
		}
		matched := text[span[0]:span[1]]
		first, _ := utf8.DecodeRuneInString(matched)
		last, _ := utf8.DecodeLastRuneInString(matched)
		if !unicode.Is(unicode.Han, first) && wordRune(before) && wordRune(first) {
			continue
		}
		if !unicode.Is(unicode.Han, last) && wordRune(after) && wordRune(last) {
			continue
		}
		out.WriteString(text[start:span[0]])
		out.WriteString("[PERSONAL]")
		start = span[1]
	}
	if start == 0 {
		return text
	}
	out.WriteString(text[start:])
	return out.String()
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }
