// input: 原始证据和规范化工单快照。
// output: Jev/Bub 共用的最小脱敏 JSON 和内容 Hash。
// pos: 分析输入数据最小化边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"zoho-workstation/internal/ticket"
)

// Input deliberately excludes ticket status, priority, department, and edit
// timestamps. Message delivery status remains evidence, especially for drafts.
func Input(snapshot ticket.Snapshot, raw ticket.RawSnapshot) (json.RawMessage, string, error) {
	redacted := ticket.RedactedSnapshot(snapshot, raw)
	for i := range redacted.Messages {
		redacted.Messages[i].ModifiedTime = ""
	}
	input := struct {
		TicketID    string           `json:"ticket_id"`
		Subject     string           `json:"subject"`
		Description string           `json:"description"`
		Product     string           `json:"product"`
		Complete    bool             `json:"complete"`
		Messages    []ticket.Message `json:"messages"`
	}{redacted.ID, redacted.Subject, redacted.Description, redacted.Product, redacted.Complete, redacted.Messages}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	// Never silently truncate a conversation to fit a model request.
	if len(body) > 512*1024 {
		return nil, "", errors.New("conversation exceeds current model input size limit")
	}
	digest := sha256.Sum256(body)
	return body, hex.EncodeToString(digest[:]), nil
}
