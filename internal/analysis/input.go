// input: 原始证据和规范化工单快照。
// output: 完整脱敏会话及 Bub 可选模型参考，各自实际输入的内容 Hash。
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
	digest := sha256.Sum256(body)
	if len(body) > 512*1024 {
		return nil, hex.EncodeToString(digest[:]), errors.New("conversation exceeds current model input size limit")
	}
	return body, hex.EncodeToString(digest[:]), nil
}

// JevReference is model interpretation, never a message or factual evidence.
type JevReference struct {
	Kind             string            `json:"kind"`
	RunID            int64             `json:"run_id"`
	ConversationHash string            `json:"conversation_hash"`
	SchemaVersion    string            `json:"schema_version"`
	RequestedModel   string            `json:"requested_model"`
	ReturnedModel    string            `json:"returned_model"`
	Choices          map[string]string `json:"choices"`
}

// BubInput retains every conversation field and versions the actual reference.
func BubInput(conversation json.RawMessage, hash string, reference *JevReference) (json.RawMessage, string, error) {
	if reference != nil && (reference.ConversationHash != hash || reference.Kind != "model_assessment") {
		return nil, "", errors.New("Bub reference does not match conversation")
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(conversation, &input) != nil || input == nil {
		return nil, "", errors.New("Bub conversation is invalid")
	}
	input["conversation_hash"], _ = json.Marshal(hash)
	input["jev_reference"], _ = json.Marshal(reference)
	body, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(body)
	return body, hex.EncodeToString(digest[:]), nil
}
