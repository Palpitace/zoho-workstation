// input: SQLite 路径、固定部门和原始/规范化工单快照。
// output: 持久化样本名额、工单快照与同步进度，支持幂等恢复。
// pos: Go 独占数据库边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
	"zoho-workstation/internal/ticket"
)

type Store struct {
	db         *sql.DB
	department string
}

type State struct {
	DepartmentID string `json:"department_id"`
	Selected     int    `json:"selected"`
	Ready        int    `json:"ready"`
	Failed       int    `json:"failed"`
	Watermark    string `json:"watermark"`
	BatchUpper   string `json:"batch_upper,omitempty"`
	LastAttempt  string `json:"last_attempt"`
	LastError    string `json:"last_error,omitempty"`
}

func Open(path, department string) (*Store, error) {
	if department == "" {
		return nil, errors.New("department is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	u := url.URL{Scheme: "file", Path: abs}
	q := url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(1)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, department: department}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sync_state (
		id INTEGER PRIMARY KEY CHECK(id=1), department TEXT NOT NULL, frozen INTEGER NOT NULL DEFAULT 0,
		watermark TEXT NOT NULL DEFAULT '', last_attempt TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '',
		batch_upper TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS tickets (
		id TEXT PRIMARY KEY, selected_at TEXT NOT NULL,
		subject TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', modified_time TEXT NOT NULL DEFAULT '',
		raw_json TEXT, snapshot_json TEXT, sync_error TEXT NOT NULL DEFAULT '', checked_at TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS ai_runs (
		id INTEGER PRIMARY KEY, ticket_id TEXT NOT NULL REFERENCES tickets(id), kind TEXT NOT NULL,
		input_hash TEXT NOT NULL, schema_version TEXT NOT NULL, requested_model TEXT NOT NULL, round TEXT NOT NULL,
		status TEXT NOT NULL, returned_model TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL,
		completed_at TEXT NOT NULL DEFAULT '', result_json TEXT, error TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS ai_runs_ticket ON ai_runs(ticket_id,kind,id);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	// Additive migration for databases created by the initial sampling milestone.
	var hasBatch int
	if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('sync_state') WHERE name='batch_upper'").Scan(&hasBatch); err != nil {
		db.Close()
		return nil, err
	}
	if hasBatch == 0 {
		if _, err = db.Exec("ALTER TABLE sync_state ADD COLUMN batch_upper TEXT NOT NULL DEFAULT ''"); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err = db.Exec("INSERT OR IGNORE INTO sync_state(id,department) VALUES(1,?)", department); err != nil {
		db.Close()
		return nil, err
	}
	var bound string
	if err = db.QueryRow("SELECT department FROM sync_state WHERE id=1").Scan(&bound); err != nil {
		db.Close()
		return nil, err
	}
	if bound != department {
		db.Close()
		return nil, errors.New("database belongs to another department; use a separate database")
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Reserve freezes the sample before any detail reads; retries cannot replace failed IDs.
func (s *Store) Reserve(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	unique := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			return errors.New("sample ID missing")
		}
		unique[id] = true
	}
	if len(unique) > 50 {
		return errors.New("sample exceeds cumulative limit of 50")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var frozen bool
	if err = tx.QueryRowContext(ctx, "SELECT frozen FROM sync_state WHERE id=1").Scan(&frozen); err != nil {
		return err
	}
	if frozen {
		for id := range unique {
			var found int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tickets WHERE id=?", id).Scan(&found); err != nil {
				return err
			}
			if found == 0 {
				return errors.New("sample is frozen; existing IDs must be reused")
			}
		}
		return nil
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO tickets(id,selected_at) VALUES(?,?)", id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE sync_state SET frozen=1 WHERE id=1"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM tickets ORDER BY selected_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) Save(ctx context.Context, raw ticket.RawSnapshot, snapshot ticket.Snapshot) error {
	if snapshot.DepartmentID != s.department {
		return errors.New("snapshot department mismatch")
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	viewJSON, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tickets SET subject=?,status=?,modified_time=?,raw_json=?,snapshot_json=?,sync_error='',checked_at=? WHERE id=?`, snapshot.Subject, snapshot.Status, snapshot.ModifiedTime, rawJSON, viewJSON, time.Now().UTC().Format(time.RFC3339Nano), snapshot.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("ticket is outside frozen sample")
	}
	return err
}

func (s *Store) Failure(ctx context.Context, id, message string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE tickets SET sync_error=? WHERE id=?", message, id)
	return err
}

func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	var record Record
	var body, raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT snapshot_json,raw_json,sync_error,checked_at FROM tickets WHERE id=? AND snapshot_json IS NOT NULL", id).Scan(&body, &raw, &record.SyncError, &record.CheckedAt)
	if err != nil {
		return record, err
	}
	if err = json.Unmarshal(body, &record.Snapshot); err != nil {
		return record, err
	}
	err = json.Unmarshal(raw, &record.Raw)
	return record, err
}

type Record struct {
	ticket.Snapshot
	Raw       ticket.RawSnapshot `json:"-"`
	SyncError string             `json:"sync_error,omitempty"`
	Pending   bool               `json:"pending"`
	CheckedAt string             `json:"checked_at,omitempty"`
}

func (s *Store) List(ctx context.Context, query, status string) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,snapshot_json,sync_error,checked_at FROM tickets WHERE (?='' OR instr(lower(subject),lower(?))>0) AND (?='' OR status=?) ORDER BY modified_time DESC,id LIMIT 50`, query, query, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		var r Record
		var body []byte
		if err = rows.Scan(&r.ID, &body, &r.SyncError, &r.CheckedAt); err != nil {
			return nil, err
		}
		r.Pending = body == nil
		if body != nil {
			if err = json.Unmarshal(body, &r.Snapshot); err != nil {
				return nil, err
			}
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func (s *Store) State(ctx context.Context) (State, error) {
	var v State
	err := s.db.QueryRowContext(ctx, `SELECT department,watermark,batch_upper,last_attempt,last_error,
		(SELECT count(*) FROM tickets),
		(SELECT count(*) FROM tickets WHERE snapshot_json IS NOT NULL),
		(SELECT count(*) FROM tickets WHERE sync_error!='') FROM sync_state WHERE id=1`).Scan(&v.DepartmentID, &v.Watermark, &v.BatchUpper, &v.LastAttempt, &v.LastError, &v.Selected, &v.Ready, &v.Failed)
	return v, err
}

func (s *Store) Start(ctx context.Context) (time.Time, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var upper string
	err := s.db.QueryRowContext(ctx, `UPDATE sync_state SET last_attempt=?,last_error='',batch_upper=CASE WHEN batch_upper='' THEN ? ELSE batch_upper END WHERE id=1 RETURNING batch_upper`, now, now).Scan(&upper)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, upper)
}

func (s *Store) Finish(ctx context.Context, watermark string, runErr error) error {
	if runErr != nil {
		_, err := s.db.ExecContext(ctx, "UPDATE sync_state SET last_error=? WHERE id=1", runErr.Error())
		return err
	}
	_, err := s.db.ExecContext(ctx, "UPDATE sync_state SET watermark=?,last_error='',batch_upper='' WHERE id=1", watermark)
	return err
}
