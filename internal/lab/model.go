// input: 脱敏历史工单、固定知识版本与人工反馈。
// output: 离线批次、节点回复/评估、修订建议及不可覆盖的调用记录契约。
// pos: 离线 FAQ 实验的数据边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/ticket"
)

const Schema = "bub-lab-v1"
const MaxTickets = 50
const MaxRewrites = 2

type Sample struct {
	TicketID     string          `json:"ticket_id"`
	TicketNumber string          `json:"ticket_number"`
	Subject      string          `json:"subject"`
	Group        string          `json:"group"`
	Split        string          `json:"split"`
	Snapshot     ticket.Snapshot `json:"snapshot"`
	Nodes        []Node          `json:"nodes"`
}

type Node struct {
	ID         string          `json:"id"`
	CutoffID   string          `json:"cutoff_id"`
	CutoffTime string          `json:"cutoff_time"`
	Label      string          `json:"label"`
	Ticket     json.RawMessage `json:"ticket"`
}

type Spec struct {
	Schema       string            `json:"schema"`
	Model        string            `json:"model"`
	ModelURLHash string            `json:"model_url_hash"`
	StartTime    string            `json:"start_time"`
	EndTime      string            `json:"end_time"`
	ParentID     int64             `json:"parent_id,omitempty"`
	Samples      []Sample          `json:"samples"`
	Knowledge    []knowledge.Entry `json:"knowledge"`
	Feedback     []Feedback        `json:"feedback"`
}

type Batch struct {
	ID        int64  `json:"id"`
	Revision  int64  `json:"revision"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Spec      Spec   `json:"spec"`
	State     State  `json:"state"`
}

type State struct {
	Status   string           `json:"status"`
	Phase    string           `json:"phase"`
	Error    string           `json:"error,omitempty"`
	Calls    []Call           `json:"calls"`
	Feedback []Feedback       `json:"feedback"`
	Drafts   map[string]int64 `json:"drafts"`
}

type Call struct {
	ID            int             `json:"id"`
	Key           string          `json:"key"`
	Task          string          `json:"task"`
	NodeID        string          `json:"node_id"`
	Variant       string          `json:"variant"`
	Revision      int             `json:"revision"`
	InputHash     string          `json:"input_hash"`
	Input         json.RawMessage `json:"input"`
	Status        string          `json:"status"`
	StartedAt     string          `json:"started_at"`
	CompletedAt   string          `json:"completed_at,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	ReturnedModel string          `json:"returned_model,omitempty"`
	Usage         json.RawMessage `json:"usage,omitempty"`
	RequestID     string          `json:"request_id,omitempty"`
	Error         string          `json:"error,omitempty"`
}

type Feedback struct {
	RatedReply  *Reply   `json:"rated_reply,omitempty"`
	BatchID     int64    `json:"batch_id"`
	ID          int      `json:"id"`
	CallID      int      `json:"call_id"`
	NodeID      string   `json:"node_id"`
	Score       int      `json:"score"`
	Tags        []string `json:"tags"`
	Note        string   `json:"note"`
	EditedReply string   `json:"edited_reply"`
	CreatedAt   string   `json:"created_at"`
}

// Input carries a historical prefix, never the full ticket for reply/evaluate.
// Propose can use a learning ticket's full public conversation, but no holdout.
type Input struct {
	Ticket     json.RawMessage    `json:"ticket"`
	Sources    []knowledge.Source `json:"sources"`
	Reply      *Reply             `json:"reply,omitempty"`
	Evaluation *Evaluation        `json:"evaluation,omitempty"`
	Feedback   []Feedback         `json:"feedback"`
}

type Reply struct {
	Text               string   `json:"text"`
	Language           string   `json:"language"`
	Rationale          string   `json:"rationale"`
	MissingInformation []string `json:"missing_information"`
	SourceIDs          []string `json:"source_ids"`
	MessageIDs         []string `json:"message_ids"`
}

type Finding struct {
	Code       string   `json:"code"`
	Severity   string   `json:"severity"`
	Reason     string   `json:"reason"`
	MessageIDs []string `json:"message_ids"`
	SourceIDs  []string `json:"source_ids"`
}

type Evaluation struct {
	Verdict     string    `json:"verdict"`
	Score       int       `json:"score"`
	Findings    []Finding `json:"findings"`
	Suggestions []string  `json:"suggestions"`
}

type Proposal struct {
	Title      string   `json:"title"`
	Product    string   `json:"product"`
	Question   string   `json:"question"`
	Answer     string   `json:"answer"`
	Conditions string   `json:"conditions"`
	Reason     string   `json:"reason"`
	Scope      string   `json:"scope"`
	ReplacesID int64    `json:"replaces_id"`
	MessageIDs []string `json:"message_ids"`
	SourceIDs  []string `json:"source_ids"`
}

type Proposals struct {
	Items []Proposal `json:"items"`
}

func Hash(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
