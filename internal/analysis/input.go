// input: 原始证据和规范化工单快照。
// output: Bub 完整脱敏会话及 Jev 总结输入，各自实际输入的内容 Hash。
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

// BubInput retains the complete conversation without downstream assessments.
func BubInput(conversation json.RawMessage, hash string) (json.RawMessage, string, error) {
	var input map[string]json.RawMessage
	if json.Unmarshal(conversation, &input) != nil || input == nil {
		return nil, "", errors.New("Bub conversation is invalid")
	}
	input["conversation_hash"], _ = json.Marshal(hash)
	body, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(body)
	return body, hex.EncodeToString(digest[:]), nil
}

// SummaryInput versions Jev's actual source, excluding run IDs so an identical
// successful summary can be reused without charging for another assessment.
func SummaryInput(ticketID, conversationHash, inputHash, schema, model, returnedModel string, result json.RawMessage) (json.RawMessage, string, error) {
	var saved struct {
		Summary json.RawMessage `json:"summary"`
	}
	if json.Unmarshal(result, &saved) != nil || len(saved.Summary) == 0 || string(saved.Summary) == "null" {
		return nil, "", errors.New("saved Bub summary is invalid")
	}
	body, err := json.Marshal(struct {
		TicketID         string          `json:"ticket_id"`
		ConversationHash string          `json:"conversation_hash"`
		SummaryInputHash string          `json:"summary_input_hash"`
		SummarySchema    string          `json:"summary_schema"`
		RequestedModel   string          `json:"summary_requested_model"`
		ReturnedModel    string          `json:"summary_returned_model"`
		Summary          json.RawMessage `json:"summary"`
	}{ticketID, conversationHash, inputHash, schema, model, returnedModel, saved.Summary})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(body)
	return body, hex.EncodeToString(digest[:]), nil
}
