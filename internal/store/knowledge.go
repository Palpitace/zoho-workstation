// input: 已脱敏案例、文档块、明确发布/停用操作与检索条件。
// output: 不可覆盖的知识版本、SOP 草稿发布及有效来源查询。
// pos: SQLite 知识生命周期边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"zoho-workstation/internal/knowledge"
)

var ErrKnowledgeConflict = errors.New("knowledge version is no longer publishable")

const knowledgeColumns = `k.id,k.replaces_id,k.kind,k.status,k.title,k.product,k.source_name,k.source_hash,k.source_ticket_id,k.message_ids,k.blocks,k.created_at,k.published_at`

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
	return err
}

func validateKnowledge(entry knowledge.Entry) error {
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
	if entry.ReplacesID != 0 {
		var parentKind, parentStatus string
		var parentReplaces int64
		if err = tx.QueryRowContext(ctx, "SELECT kind,status,replaces_id FROM knowledge_entries WHERE id=?", entry.ReplacesID).Scan(&parentKind, &parentStatus, &parentReplaces); err != nil {
			return knowledge.Entry{}, err
		}
		if parentKind != "sop" || parentStatus == "archived" {
			return knowledge.Entry{}, ErrKnowledgeConflict
		}
		// Editing a pending revision still replaces its effective ancestor,
		// not only the intermediate draft. Publication then withdraws that SOP.
		if parentStatus == "draft" && parentReplaces != 0 {
			entry.ReplacesID = parentReplaces
			if err = tx.QueryRowContext(ctx, "SELECT status FROM knowledge_entries WHERE id=?", parentReplaces).Scan(&parentStatus); err != nil {
				return knowledge.Entry{}, err
			}
			if parentStatus == "archived" {
				return knowledge.Entry{}, ErrKnowledgeConflict
			}
		}
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
	result, err := tx.ExecContext(ctx, `INSERT INTO knowledge_entries(replaces_id,kind,status,title,product,source_name,source_hash,source_ticket_id,message_ids,blocks,created_at,published_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, entry.ReplacesID, entry.Kind, entry.Status, entry.Title, entry.Product, entry.SourceName, entry.SourceHash, entry.SourceTicketID, string(messages), string(blocks), entry.CreatedAt, entry.PublishedAt)
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
	var messages, blocks string
	err := row.Scan(&entry.ID, &entry.ReplacesID, &entry.Kind, &entry.Status, &entry.Title, &entry.Product, &entry.SourceName, &entry.SourceHash, &entry.SourceTicketID, &messages, &blocks, &entry.CreatedAt, &entry.PublishedAt)
	if err == nil {
		err = json.Unmarshal([]byte(messages), &entry.MessageIDs)
	}
	if err == nil {
		err = json.Unmarshal([]byte(blocks), &entry.Blocks)
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
