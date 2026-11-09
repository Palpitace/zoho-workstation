// input: 已脱敏案例、分类文档、明确发布/停用/恢复操作与检索条件。
// output: 不可覆盖的知识版本及来源链、SOP 草稿发布/恢复和有效来源查询。
// pos: SQLite 知识生命周期边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"zoho-workstation/internal/knowledge"
)

var ErrKnowledgeConflict = errors.New("knowledge version is no longer publishable")

const knowledgeColumns = `k.id,k.replaces_id,k.kind,k.status,k.title,k.product,k.source_name,k.source_hash,k.source_ticket_id,k.message_ids,k.blocks,k.created_at,k.published_at,k.scope,k.source_tickets,k.lab_batch_id`

func (s *Store) initKnowledge() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_entries (
		id INTEGER PRIMARY KEY, replaces_id INTEGER NOT NULL DEFAULT 0,
		kind TEXT NOT NULL CHECK(kind IN ('case','sop')),
		status TEXT NOT NULL CHECK(status IN ('draft','published','archived')),
		title TEXT NOT NULL, product TEXT NOT NULL DEFAULT '', source_name TEXT NOT NULL DEFAULT '',
		source_hash TEXT NOT NULL, source_ticket_id TEXT NOT NULL DEFAULT '',
		message_ids TEXT NOT NULL, blocks TEXT NOT NULL, created_at TEXT NOT NULL, published_at TEXT NOT NULL DEFAULT '',
		UNIQUE(kind,source_ticket_id,source_hash)
	);
	CREATE INDEX IF NOT EXISTS knowledge_status ON knowledge_entries(status,kind);`)
	if err != nil {
		return err
	}
	for name, definition := range map[string]string{"scope": "TEXT NOT NULL DEFAULT 'reference'", "source_tickets": "TEXT NOT NULL DEFAULT '[]'", "lab_batch_id": "INTEGER NOT NULL DEFAULT 0"} {
		var exists int
		if err = s.db.QueryRow("SELECT count(*) FROM pragma_table_info('knowledge_entries') WHERE name=?", name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err = s.db.Exec("ALTER TABLE knowledge_entries ADD COLUMN " + name + " " + definition); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateKnowledge(entry knowledge.Entry) error {
	if err := validateKnowledgeProvenance(entry); err != nil {
		return err
	}
	if (entry.Kind != "case" && entry.Kind != "sop") || strings.TrimSpace(entry.Title) == "" || strings.TrimSpace(entry.SourceHash) == "" || len(entry.Title) > 1024 || len(entry.SourceHash) > 256 || len(entry.Product) > 256 || len(entry.SourceName) > 1024 || entry.ReplacesID < 0 {
		return errors.New("invalid knowledge metadata")
	}
	if (entry.Kind == "case" && entry.SourceTicketID == "") || (entry.Kind == "case" && entry.ReplacesID != 0) {
		return errors.New("invalid knowledge source")
	}
	if len(entry.Blocks) == 0 || len(entry.Blocks) > 4096 || len(entry.MessageIDs) > 1000 {
		return errors.New("knowledge content exceeds supported limits")
	}
	seen, size := map[string]bool{}, 0
	for _, block := range entry.Blocks {
		if !knowledgeBlockID.MatchString(block.ID) || seen[block.ID] || strings.TrimSpace(block.Text) == "" || len(block.Text) > 16*1024 {
			return errors.New("invalid or oversized knowledge block")
		}
		seen[block.ID] = true
		size += len(block.Text)
	}
	if size > 512*1024 {
		return errors.New("knowledge content exceeds supported limits")
	}
	return nil
}

var knowledgeBlockID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)

// SaveKnowledge appends immutable content. Duplicate sources return the original
// version, including archived ones, and never silently publish a SOP.
func (s *Store) SaveKnowledge(ctx context.Context, entry knowledge.Entry) (knowledge.Entry, error) {
	if err := validateKnowledge(entry); err != nil {
		return knowledge.Entry{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return knowledge.Entry{}, err
	}
	defer tx.Rollback()
	previous, err := scanKnowledge(tx.QueryRowContext(ctx, "SELECT "+knowledgeColumns+" FROM knowledge_entries k WHERE k.kind=? AND k.source_ticket_id=? AND k.source_hash=?", entry.Kind, entry.SourceTicketID, entry.SourceHash))
	if err == nil {
		return previous, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return knowledge.Entry{}, err
	}
	if err = bindKnowledgeProvenance(ctx, tx, &entry); err != nil {
		return knowledge.Entry{}, err
	}
	entry.ID = 0
	entry.Status = "draft"
	entry.PublishedAt = ""
	entry.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if entry.Kind == "case" {
		var complete bool
		if err = tx.QueryRowContext(ctx, "SELECT coalesce(json_extract(snapshot_json,'$.complete'),0)=1 AND sync_stopped=0 FROM tickets WHERE id=?", entry.SourceTicketID).Scan(&complete); err != nil {
			return knowledge.Entry{}, err
		}
		if !complete {
			return knowledge.Entry{}, errors.New("case source ticket is unavailable or incomplete")
		}
		entry.Status, entry.PublishedAt = "published", entry.CreatedAt
	}
	if entry.MessageIDs == nil {
		entry.MessageIDs = []string{}
	}
	messages, _ := json.Marshal(entry.MessageIDs)
	blocks, _ := json.Marshal(entry.Blocks)
	sourceTickets, _ := json.Marshal(entry.SourceTickets)
	result, err := tx.ExecContext(ctx, `INSERT INTO knowledge_entries(replaces_id,kind,status,title,product,source_name,source_hash,source_ticket_id,message_ids,blocks,created_at,published_at,scope,source_tickets,lab_batch_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, entry.ReplacesID, entry.Kind, entry.Status, entry.Title, entry.Product, entry.SourceName, entry.SourceHash, entry.SourceTicketID, string(messages), string(blocks), entry.CreatedAt, entry.PublishedAt, entry.Scope, string(sourceTickets), entry.LabBatchID)
	if err != nil {
		return knowledge.Entry{}, err
	}
	if entry.ID, err = result.LastInsertId(); err != nil {
		return knowledge.Entry{}, err
	}
	if entry.Kind == "case" {
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_entries SET status='archived' WHERE kind='case' AND source_ticket_id=? AND id!=? AND status='published'", entry.SourceTicketID, entry.ID); err != nil {
			return knowledge.Entry{}, err
		}
	}
	return entry, tx.Commit()
}

// PublishKnowledge makes a reviewed SOP effective and supersedes its exact
// predecessor in the same transaction. A sibling draft cannot replace it later.
func (s *Store) PublishKnowledge(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entry, err := scanKnowledge(tx.QueryRowContext(ctx, "SELECT "+knowledgeColumns+" FROM knowledge_entries k WHERE k.id=?", id))
	if err != nil {
		return err
	}
	if entry.Kind != "sop" || entry.Status != "draft" {
		return ErrKnowledgeConflict
	}
	if entry.ReplacesID != 0 {
		result, err := tx.ExecContext(ctx, "UPDATE knowledge_entries SET status='archived' WHERE id=? AND kind='sop' AND status IN ('draft','published')", entry.ReplacesID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrKnowledgeConflict
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_entries SET status='published',published_at=? WHERE id=?", time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return err
	}
	return tx.Commit()
}

// ArchiveKnowledge withdraws evidence without deleting its content or history.
func (s *Store) ArchiveKnowledge(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "UPDATE knowledge_entries SET status='archived' WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) Knowledge(ctx context.Context, id int64) (knowledge.Entry, error) {
	return scanKnowledge(s.db.QueryRowContext(ctx, "SELECT "+knowledgeColumns+" FROM knowledge_entries k WHERE k.id=?", id))
}

func scanKnowledge(row interface{ Scan(...any) error }) (knowledge.Entry, error) {
	var entry knowledge.Entry
	var messages, blocks, sourceTickets string
	err := row.Scan(&entry.ID, &entry.ReplacesID, &entry.Kind, &entry.Status, &entry.Title, &entry.Product, &entry.SourceName, &entry.SourceHash, &entry.SourceTicketID, &messages, &blocks, &entry.CreatedAt, &entry.PublishedAt, &entry.Scope, &sourceTickets, &entry.LabBatchID)
	if err == nil {
		err = json.Unmarshal([]byte(messages), &entry.MessageIDs)
	}
	if err == nil {
		err = json.Unmarshal([]byte(blocks), &entry.Blocks)
	}
	if err == nil {
		err = json.Unmarshal([]byte(sourceTickets), &entry.SourceTickets)
	}
	return entry, err
}

func (s *Store) ListKnowledge(ctx context.Context, query, status string) ([]knowledge.Entry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+knowledgeColumns+` FROM knowledge_entries k WHERE (?='' OR k.status=?) AND (?='' OR instr(lower(k.title||' '||k.product||' '||k.blocks),lower(?))>0) ORDER BY k.id DESC`, status, status, query, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []knowledge.Entry{}
	for rows.Next() {
		entry, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// KnowledgeSources excludes stopped/incomplete case sources. The app additionally
// checks SourceHash against the current analysis request before sending a case.
func (s *Store) KnowledgeSources(ctx context.Context, excludeTicketID, product, query string, limit int) ([]knowledge.Source, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+knowledgeColumns+` FROM knowledge_entries k LEFT JOIN tickets t ON t.id=k.source_ticket_id WHERE k.status='published' AND (k.kind='sop' OR (t.sync_stopped=0 AND json_extract(t.snapshot_json,'$.complete')=1)) ORDER BY k.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []knowledge.Entry{}
	for rows.Next() {
		entry, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return knowledge.SelectSources(entries, excludeTicketID, product, query, limit), nil
}

func uniqueSourceTickets(ids []string) []string {
	unique := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			unique[id] = true
		}
	}
	result := make([]string, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// KnowledgeHistory includes sibling drafts as well as the effective lineage.
// Content stays immutable; status and publication timestamps describe lifecycle.
func (s *Store) KnowledgeHistory(ctx context.Context, id int64) ([]knowledge.Entry, error) {
	entry, err := s.Knowledge(ctx, id)
	if err != nil {
		return nil, err
	}
	if entry.Kind != "sop" {
		return []knowledge.Entry{entry}, nil
	}
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE family(id) AS (
 SELECT ? UNION SELECT k.id FROM knowledge_entries k JOIN family f ON k.replaces_id=f.id
 UNION SELECT k.replaces_id FROM knowledge_entries k JOIN family f ON k.id=f.id WHERE k.replaces_id<>0
 ) SELECT `+knowledgeColumns+` FROM knowledge_entries k JOIN family f ON f.id=k.id ORDER BY k.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []knowledge.Entry{}
	for rows.Next() {
		entry, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// RestoreKnowledge copies an earlier immutable version to a new review draft.
// The currently published version remains active until explicit publication.
func (s *Store) RestoreKnowledge(ctx context.Context, id int64) (knowledge.Entry, error) {
	target, err := s.Knowledge(ctx, id)
	if err != nil {
		return knowledge.Entry{}, err
	}
	if target.Kind != "sop" {
		return knowledge.Entry{}, ErrKnowledgeConflict
	}
	history, err := s.KnowledgeHistory(ctx, id)
	if err != nil {
		return knowledge.Entry{}, err
	}
	var current knowledge.Entry
	for _, entry := range history {
		if entry.Status == "published" {
			current = entry
		}
	}
	if current.ID == 0 || current.ID == target.ID {
		return knowledge.Entry{}, ErrKnowledgeConflict
	}
	target.ID = 0
	target.ReplacesID = current.ID
	target.SourceHash = fmt.Sprintf("restore:%d:%d:%d", id, current.ID, time.Now().UnixNano())
	target.SourceName = fmt.Sprintf("restore version %d", id)
	return s.SaveKnowledge(ctx, target)
}

func validateKnowledgeProvenance(entry knowledge.Entry) error {
	if entry.Scope != "" && entry.Scope != "technical" && entry.Scope != "policy" && entry.Scope != "reference" {
		return errors.New("invalid knowledge scope")
	}
	if entry.LabBatchID < 0 || len(entry.SourceTickets) > 1000 {
		return errors.New("invalid knowledge provenance")
	}
	for _, id := range entry.SourceTickets {
		if id == "" || len(id) > 128 {
			return errors.New("invalid knowledge source ticket")
		}
	}
	return nil
}

// bindKnowledgeProvenance resolves the effective predecessor and retains all
// known source tickets before the immutable revision is inserted.
func bindKnowledgeProvenance(ctx context.Context, tx *sql.Tx, entry *knowledge.Entry) error {
	var err error
	if entry.ReplacesID != 0 {
		parent, err := scanKnowledge(tx.QueryRowContext(ctx, "SELECT "+knowledgeColumns+" FROM knowledge_entries k WHERE k.id=?", entry.ReplacesID))
		if err != nil {
			return err
		}
		if parent.Kind != "sop" || parent.Status == "archived" {
			return ErrKnowledgeConflict
		}
		if entry.LabBatchID != 0 && parent.Scope != "technical" {
			return errors.New("automatic revisions require technical knowledge")
		}
		if entry.Scope == "" {
			entry.Scope = parent.Scope
		}
		if parent.Scope == "policy" && entry.Scope != "policy" {
			return errors.New("policy scope cannot be reclassified")
		}
		entry.SourceTickets = append(entry.SourceTickets, parent.SourceTickets...)
		if parent.SourceTicketID != "" {
			entry.SourceTickets = append(entry.SourceTickets, parent.SourceTicketID)
		}
		if entry.LabBatchID == 0 {
			entry.LabBatchID = parent.LabBatchID
		}
		// A revision of a pending draft still supersedes its effective ancestor.
		if parent.Status == "draft" && parent.ReplacesID != 0 {
			entry.ReplacesID = parent.ReplacesID
			var status, scope string
			if err = tx.QueryRowContext(ctx, "SELECT status,scope FROM knowledge_entries WHERE id=?", parent.ReplacesID).Scan(&status, &scope); err != nil {
				return err
			}
			if status == "archived" {
				return ErrKnowledgeConflict
			}
			if entry.LabBatchID != 0 && scope != "technical" {
				return errors.New("automatic revisions require a technical effective predecessor")
			}
		}
	}
	if entry.Scope == "" {
		entry.Scope = "reference"
	}
	if entry.LabBatchID != 0 && entry.Scope != "technical" {
		return errors.New("automatic proposals must be technical")
	}
	if entry.SourceTicketID != "" {
		entry.SourceTickets = append(entry.SourceTickets, entry.SourceTicketID)
	}
	entry.SourceTickets = uniqueSourceTickets(entry.SourceTickets)
	if err = validateKnowledgeProvenance(*entry); err != nil {
		return err
	}
	return nil
}
