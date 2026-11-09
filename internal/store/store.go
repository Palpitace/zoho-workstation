// input: SQLite 路径、固定部门和原始/规范化工单快照。
// output: 待确认缓存展示与可恢复滚动范围、工单、同步进度、可追溯知识版本及同库操作锁。
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
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
	"zoho-workstation/internal/ticket"
)

var ErrBusy = errors.New("operation is already running for this database")

type Store struct {
	recentOnly atomic.Bool
	db         *sql.DB
	department string
	path       string
}

type State struct {
	RecentConfirmed bool   `json:"recent_confirmed"`
	Complete        int    `json:"complete"`
	Stopped         int    `json:"stopped"`
	DiscoveryCycle  string `json:"discovery_cycle,omitempty"`
	DiscoveryStart  string `json:"discovery_start,omitempty"`
	DiscoveryOffset int    `json:"discovery_offset"`
	DiscoveryDone   bool   `json:"discovery_done"`
	DepartmentID    string `json:"department_id"`
	Selected        int    `json:"selected"`
	Ready           int    `json:"ready"`
	Failed          int    `json:"failed"`
	Watermark       string `json:"watermark"`
	BatchUpper      string `json:"batch_upper,omitempty"`
	LastAttempt     string `json:"last_attempt"`
	LastError       string `json:"last_error,omitempty"`
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
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(canonical)
	if filepath.VolumeName(canonical) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(1)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, department: department, path: canonical}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sync_state (
		id INTEGER PRIMARY KEY CHECK(id=1), department TEXT NOT NULL, frozen INTEGER NOT NULL DEFAULT 0,
		watermark TEXT NOT NULL DEFAULT '', last_attempt TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '',
		batch_upper TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS tickets (
		id TEXT PRIMARY KEY, selected_at TEXT NOT NULL,
		subject TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', modified_time TEXT NOT NULL DEFAULT '',
		raw_json TEXT, snapshot_json TEXT, sync_error TEXT NOT NULL DEFAULT '', checked_at TEXT NOT NULL DEFAULT '', sync_stopped INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS ai_runs (
		id INTEGER PRIMARY KEY, ticket_id TEXT NOT NULL REFERENCES tickets(id), kind TEXT NOT NULL,
		input_hash TEXT NOT NULL, schema_version TEXT NOT NULL, requested_model TEXT NOT NULL, round TEXT NOT NULL,
		status TEXT NOT NULL, returned_model TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL,
		completed_at TEXT NOT NULL DEFAULT '', result_json TEXT, error TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS recent_tickets (id TEXT PRIMARY KEY REFERENCES tickets(id));
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
	for name, definition := range map[string]string{"recent_confirmed": "INTEGER NOT NULL DEFAULT 0", "discovery_cycle": "TEXT NOT NULL DEFAULT ''", "discovery_start": "TEXT NOT NULL DEFAULT ''", "discovery_offset": "INTEGER NOT NULL DEFAULT 0", "discovery_done": "INTEGER NOT NULL DEFAULT 0"} {
		var exists int
		if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('sync_state') WHERE name=?", name).Scan(&exists); err != nil {
			db.Close()
			return nil, err
		}
		if exists == 0 {
			if _, err = db.Exec("ALTER TABLE sync_state ADD COLUMN " + name + " " + definition); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	var hasStopped int
	if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('tickets') WHERE name='sync_stopped'").Scan(&hasStopped); err != nil {
		db.Close()
		return nil, err
	}
	if hasStopped == 0 {
		if _, err = db.Exec("ALTER TABLE tickets ADD COLUMN sync_stopped INTEGER NOT NULL DEFAULT 0"); err != nil {
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
	// Preserve a previously confirmed nonempty scope when upgrading from the first rolling release.
	if _, err = db.Exec("UPDATE sync_state SET recent_confirmed=1 WHERE recent_confirmed=0 AND EXISTS(SELECT 1 FROM recent_tickets)"); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.initKnowledge(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Lock prevents concurrent writers of one operation on Linux, macOS, and Windows.
// Callers hold the returned release function until the operation ends. The
// kernel also releases the lock when the process exits, including after a crash.
func (s *Store) Lock(operation string) (func(), error) {
	if operation != "sync" && operation != "analysis" {
		return nil, errors.New("unsupported operation lock")
	}
	lock, err := os.OpenFile(s.path+"."+operation+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("operation lock unavailable")
	}
	if err = lockFile(lock); err != nil {
		lock.Close()
		if errors.Is(err, ErrBusy) {
			return nil, ErrBusy
		}
		return nil, errors.New("cannot acquire operation lock")
	}
	return func() {
		lock.Close()
	}, nil
}

// Reserve durably adds IDs before detail reads; existing records and history stay intact.
func (s *Store) Reserve(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if id == "" {
			return errors.New("ticket ID missing")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO tickets(id,selected_at) VALUES(?,?)", id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) IDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM tickets WHERE (?=0 OR id IN (SELECT id FROM recent_tickets)) ORDER BY selected_at,id", s.recentOnly.Load())
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
	result, err := s.db.ExecContext(ctx, `UPDATE tickets SET subject=?,status=?,modified_time=?,raw_json=?,snapshot_json=?,sync_error='',checked_at=? WHERE id=? AND sync_stopped=0`, snapshot.Subject, snapshot.Status, snapshot.ModifiedTime, rawJSON, viewJSON, time.Now().UTC().Format(time.RFC3339Nano), snapshot.ID)
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
	_, err := s.db.ExecContext(ctx, "UPDATE tickets SET sync_error=?,checked_at=? WHERE id=?", message, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	record := Record{Snapshot: ticket.Snapshot{ID: id}}
	var body, raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT snapshot_json,raw_json,sync_error,checked_at,sync_stopped,(? AND id NOT IN (SELECT id FROM recent_tickets)) FROM tickets WHERE id=?", s.recentOnly.Load(), id).Scan(&body, &raw, &record.SyncError, &record.CheckedAt, &record.SyncStopped, &record.OutsideScope)
	if err != nil {
		return record, err
	}
	if body == nil {
		record.Pending = true
		return record, nil
	}
	if err = json.Unmarshal(body, &record.Snapshot); err != nil {
		return record, err
	}
	err = json.Unmarshal(raw, &record.Raw)
	return record, err
}

type Record struct {
	ticket.Snapshot
	Raw          ticket.RawSnapshot `json:"-"`
	OutsideScope bool               `json:"outside_scope"`
	SyncStopped  bool               `json:"sync_stopped"`
	SyncError    string             `json:"sync_error,omitempty"`
	Pending      bool               `json:"pending"`
	CheckedAt    string             `json:"checked_at,omitempty"`
}

const visibleRecentIDs = `SELECT id FROM recent_tickets UNION ALL SELECT id FROM
 (SELECT id FROM tickets WHERE snapshot_json IS NOT NULL AND sync_stopped=0
 ORDER BY julianday(json_extract(snapshot_json,'$.created_time')) DESC,id DESC LIMIT 50)
 WHERE (SELECT recent_confirmed FROM sync_state WHERE id=1)=0`

func (s *Store) List(ctx context.Context, query, status string) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,snapshot_json,sync_error,checked_at,sync_stopped,(? AND id NOT IN (SELECT id FROM recent_tickets)) FROM tickets WHERE (?=0 OR id IN (`+visibleRecentIDs+`)) AND (?='' OR instr(lower(subject),lower(?))>0) AND (?='' OR status=?) ORDER BY modified_time DESC,id`, s.recentOnly.Load(), s.recentOnly.Load(), query, query, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		var r Record
		var body []byte
		if err = rows.Scan(&r.ID, &body, &r.SyncError, &r.CheckedAt, &r.SyncStopped, &r.OutsideScope); err != nil {
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
	err := s.db.QueryRowContext(ctx, `WITH scoped AS (SELECT * FROM tickets WHERE (?=0 OR id IN (`+visibleRecentIDs+`))) SELECT recent_confirmed,department,watermark,batch_upper,last_attempt,last_error,discovery_cycle,discovery_start,discovery_offset,discovery_done,
		(SELECT count(*) FROM scoped),
		(SELECT count(*) FROM scoped WHERE snapshot_json IS NOT NULL),
		(SELECT count(*) FROM scoped WHERE sync_error!='' AND sync_stopped=0),
		(SELECT count(*) FROM scoped WHERE json_extract(snapshot_json,'$.complete')=1 AND sync_stopped=0),
		(SELECT count(*) FROM scoped WHERE sync_stopped=1) FROM sync_state WHERE id=1`, s.recentOnly.Load()).Scan(&v.RecentConfirmed, &v.DepartmentID, &v.Watermark, &v.BatchUpper, &v.LastAttempt, &v.LastError, &v.DiscoveryCycle, &v.DiscoveryStart, &v.DiscoveryOffset, &v.DiscoveryDone, &v.Selected, &v.Ready, &v.Failed, &v.Complete, &v.Stopped)
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
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	if watermark == "" {
		_, err := s.db.ExecContext(ctx, "UPDATE sync_state SET last_error=? WHERE id=1", message)
		return err
	}
	_, err := s.db.ExecContext(ctx, "UPDATE sync_state SET watermark=?,last_error=?,batch_upper='' WHERE id=1", watermark, message)
	return err
}

// StopSync preserves the last in-scope snapshot and permanently excludes this
// reserved ID from network reads after a verified deletion or department change.
func (s *Store) StopSync(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE tickets SET sync_stopped=1,sync_error=?,checked_at=? WHERE id=?", reason, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// SaveDiscovery commits a validated page and cursor together; retrying a page is idempotent.
func (s *Store) SaveDiscovery(ctx context.Context, ids []string, cycle, start string, next int, done bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if id == "" {
			return errors.New("discovery ID missing")
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO tickets(id,selected_at) VALUES(?,?)", id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE sync_state SET discovery_cycle=?,discovery_start=?,discovery_offset=?,discovery_done=? WHERE id=1", cycle, start, next, done)
	if err != nil {
		return err
	}
	return tx.Commit()
}
