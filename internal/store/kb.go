// input: 具名知识库、部门授权集合、分类操作及已发布版本的本地向量。
// output: 可恢复的目录与索引、部门隔离后的有效知识；旧版本和引用保留。
// pos: SQLite 知识组织与向量持久化。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"zoho-workstation/internal/knowledge"
)

func (s *Store) initLibraries() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_libraries(id INTEGER PRIMARY KEY,name TEXT NOT NULL,department_ids TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS knowledge_categories(id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL REFERENCES knowledge_libraries(id),name TEXT NOT NULL,UNIQUE(library_id,name));
 CREATE TABLE IF NOT EXISTS knowledge_query_vectors(query_hash TEXT NOT NULL,model TEXT NOT NULL,values_json TEXT NOT NULL,created_at INTEGER NOT NULL,PRIMARY KEY(query_hash,model));
 CREATE TABLE IF NOT EXISTS knowledge_vectors(entry_id INTEGER NOT NULL REFERENCES knowledge_entries(id),block_id TEXT NOT NULL,source_hash TEXT NOT NULL,model TEXT NOT NULL,chunk_version TEXT NOT NULL,values_json TEXT NOT NULL,PRIMARY KEY(entry_id,block_id,model,chunk_version));`)
	if err != nil {
		return err
	}
	_, err = s.EnsureDefaultLibrary(context.Background(), s.department)
	return err
}

// EnsureDefaultLibrary assigns legacy unscoped entries only once. Existing
// bindings are never broadened merely because the application is restarted.
// If all libraries were unbound, a fresh default preserves automatic case saving.
func (s *Store) EnsureDefaultLibrary(ctx context.Context, departmentID string) (knowledge.Library, error) {
	if strings.TrimSpace(departmentID) == "" {
		return knowledge.Library{}, errors.New("department is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return knowledge.Library{}, err
	}
	defer tx.Rollback()
	var library knowledge.Library
	var departments string
	err = tx.QueryRowContext(ctx, "SELECT id,name,department_ids FROM knowledge_libraries l WHERE EXISTS(SELECT 1 FROM json_each(l.department_ids) WHERE value=?) ORDER BY id LIMIT 1", departmentID).Scan(&library.ID, &library.Name, &departments)
	if errors.Is(err, sql.ErrNoRows) {
		library.Name = "默认知识库"
		library.DepartmentIDs = []string{departmentID}
		encoded, _ := json.Marshal(library.DepartmentIDs)
		result, e := tx.ExecContext(ctx, "INSERT INTO knowledge_libraries(name,department_ids) VALUES(?,?)", library.Name, string(encoded))
		if e != nil {
			return library, e
		}
		library.ID, e = result.LastInsertId()
		if e != nil {
			return library, e
		}
	} else if err != nil {
		return library, err
	} else if err = json.Unmarshal([]byte(departments), &library.DepartmentIDs); err != nil {
		return library, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_entries SET library_id=? WHERE library_id=0", library.ID); err != nil {
		return library, err
	}
	return library, tx.Commit()
}

func (s *Store) ListLibraries(ctx context.Context) ([]knowledge.Library, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,department_ids FROM knowledge_libraries ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	libraries := []knowledge.Library{}
	for rows.Next() {
		var library knowledge.Library
		var departments string
		if err = rows.Scan(&library.ID, &library.Name, &departments); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(departments), &library.DepartmentIDs); err != nil {
			return nil, err
		}
		libraries = append(libraries, library)
	}
	return libraries, rows.Err()
}

func (s *Store) SaveLibrary(ctx context.Context, library knowledge.Library) (knowledge.Library, error) {
	library.Name = strings.TrimSpace(library.Name)
	if library.ID < 0 || library.Name == "" || len(library.Name) > 256 || len(library.DepartmentIDs) == 0 || len(library.DepartmentIDs) > 100 {
		return library, errors.New("invalid library")
	}
	for _, department := range library.DepartmentIDs {
		if strings.TrimSpace(department) == "" || len(department) > 128 {
			return library, errors.New("invalid department binding")
		}
	}
	library.DepartmentIDs = uniqueSourceTickets(library.DepartmentIDs)
	departments, _ := json.Marshal(library.DepartmentIDs)
	var result sql.Result
	var err error
	if library.ID == 0 {
		result, err = s.db.ExecContext(ctx, "INSERT INTO knowledge_libraries(name,department_ids) VALUES(?,?)", library.Name, string(departments))
		if err == nil {
			library.ID, err = result.LastInsertId()
		}
	} else {
		result, err = s.db.ExecContext(ctx, "UPDATE knowledge_libraries SET name=?,department_ids=? WHERE id=?", library.Name, string(departments), library.ID)
		if err == nil {
			n, e := result.RowsAffected()
			err = e
			if n == 0 && err == nil {
				err = sql.ErrNoRows
			}
		}
	}
	return library, err
}

func (s *Store) ListCategories(ctx context.Context, libraryID int64) ([]knowledge.Category, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,library_id,name FROM knowledge_categories WHERE library_id=? ORDER BY name,id", libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []knowledge.Category{}
	for rows.Next() {
		var category knowledge.Category
		if err = rows.Scan(&category.ID, &category.LibraryID, &category.Name); err != nil {
			return nil, err
		}
		result = append(result, category)
	}
	return result, rows.Err()
}

func (s *Store) SaveCategory(ctx context.Context, category knowledge.Category) (knowledge.Category, error) {
	category.Name = strings.TrimSpace(category.Name)
	if category.ID < 0 || category.LibraryID <= 0 || category.Name == "" || len(category.Name) > 256 {
		return category, errors.New("invalid category")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return category, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_libraries WHERE id=?", category.LibraryID).Scan(&exists); err != nil {
		return category, err
	}
	if exists != 1 {
		return category, sql.ErrNoRows
	}
	if category.ID == 0 {
		_, err = tx.ExecContext(ctx, "INSERT INTO knowledge_categories(library_id,name) VALUES(?,?) ON CONFLICT(library_id,name) DO NOTHING", category.LibraryID, category.Name)
		if err == nil {
			err = tx.QueryRowContext(ctx, "SELECT id FROM knowledge_categories WHERE library_id=? AND name=?", category.LibraryID, category.Name).Scan(&category.ID)
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, "UPDATE knowledge_categories SET name=? WHERE id=? AND library_id=?", category.Name, category.ID, category.LibraryID)
		if err == nil {
			n, e := result.RowsAffected()
			err = e
			if n == 0 && err == nil {
				err = sql.ErrNoRows
			}
		}
	}
	if err != nil {
		return category, err
	}
	return category, tx.Commit()
}

// MoveKnowledge changes navigation metadata only, never content or library
// visibility. Categories are required to belong to the entry's own library.
func (s *Store) MoveKnowledge(ctx context.Context, id, categoryID int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE knowledge_entries SET category_id=? WHERE id=? AND (?=0 OR EXISTS(SELECT 1 FROM knowledge_categories c WHERE c.id=? AND c.library_id=knowledge_entries.library_id))`, categoryID, id, categoryID, categoryID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

// ScopedKnowledge filters before retrieval. Blank department never means all
// departments; libraryID=0 means all libraries explicitly bound to this department.
func (s *Store) ScopedKnowledge(ctx context.Context, libraryID int64, departmentID, status string) ([]knowledge.Entry, error) {
	if strings.TrimSpace(departmentID) == "" {
		return nil, errors.New("department is required")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+knowledgeColumns+` FROM knowledge_entries k JOIN knowledge_libraries l ON l.id=k.library_id LEFT JOIN tickets t ON t.id=k.source_ticket_id WHERE (?=0 OR k.library_id=?) AND EXISTS(SELECT 1 FROM json_each(l.department_ids) WHERE value=?) AND (?='' OR k.status=?) AND (k.kind='sop' OR (t.sync_stopped=0 AND json_extract(t.snapshot_json,'$.complete')=1)) ORDER BY k.id DESC`, libraryID, libraryID, departmentID, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []knowledge.Entry{}
	for rows.Next() {
		entry, e := scanKnowledge(rows)
		if e != nil {
			return nil, e
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) SaveKnowledgeVector(ctx context.Context, vector knowledge.Vector) error {
	if vector.EntryID <= 0 || vector.Model == "" || len(vector.Model) > 512 || vector.ChunkVersion != knowledge.ChunkVersion || len(vector.Values) != 1024 {
		return errors.New("invalid knowledge vector")
	}
	if _, ok := knowledge.Cosine(vector.Values, vector.Values); !ok {
		return errors.New("invalid knowledge vector values")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entry, err := scanKnowledge(tx.QueryRowContext(ctx, "SELECT "+knowledgeColumns+" FROM knowledge_entries k WHERE k.id=?", vector.EntryID))
	if err != nil {
		return err
	}
	if entry.Status != "published" || entry.SourceHash != vector.SourceHash {
		return ErrKnowledgeConflict
	}
	found := false
	for _, block := range entry.Blocks {
		if block.ID == vector.BlockID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("unknown knowledge block")
	}
	values, _ := json.Marshal(vector.Values)
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_vectors(entry_id,block_id,source_hash,model,chunk_version,values_json) VALUES(?,?,?,?,?,?) ON CONFLICT(entry_id,block_id,model,chunk_version) DO UPDATE SET source_hash=excluded.source_hash,values_json=excluded.values_json`, vector.EntryID, vector.BlockID, vector.SourceHash, vector.Model, vector.ChunkVersion, string(values))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// KnowledgeVectors returns only exact current published content; history remains
// stored but cannot masquerade as an index of a replacement draft or new model.
func (s *Store) KnowledgeVectors(ctx context.Context, model, chunkVersion string) ([]knowledge.Vector, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.entry_id,v.block_id,v.source_hash,v.model,v.chunk_version,v.values_json FROM knowledge_vectors v JOIN knowledge_entries k ON k.id=v.entry_id WHERE k.status='published' AND k.source_hash=v.source_hash AND v.model=? AND v.chunk_version=?`, model, chunkVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	vectors := []knowledge.Vector{}
	for rows.Next() {
		var vector knowledge.Vector
		var values string
		if err = rows.Scan(&vector.EntryID, &vector.BlockID, &vector.SourceHash, &vector.Model, &vector.ChunkVersion, &values); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(values), &vector.Values); err != nil {
			return nil, err
		}
		vectors = append(vectors, vector)
	}
	return vectors, rows.Err()
}

// SaveKnowledgeQuery caches only local embeddings, not ticket text, and bounds
// total cached queries. Model identity prevents cross-version vector reuse.
func (s *Store) SaveKnowledgeQuery(ctx context.Context, hash, model string, values []float32) error {
	if hash == "" || len(hash) > 256 || model == "" || len(model) > 512 || len(values) != 1024 {
		return errors.New("invalid knowledge query vector")
	}
	if _, ok := knowledge.Cosine(values, values); !ok {
		return errors.New("invalid knowledge query vector values")
	}
	encoded, _ := json.Marshal(values)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_query_vectors(query_hash,model,values_json,created_at) VALUES(?,?,?,unixepoch()) ON CONFLICT(query_hash,model) DO UPDATE SET values_json=excluded.values_json,created_at=excluded.created_at`, hash, model, string(encoded))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM knowledge_query_vectors WHERE rowid IN (SELECT rowid FROM knowledge_query_vectors ORDER BY created_at DESC,rowid DESC LIMIT -1 OFFSET 1000)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) KnowledgeQuery(ctx context.Context, hash, model string) ([]float32, error) {
	var encoded string
	if err := s.db.QueryRowContext(ctx, "SELECT values_json FROM knowledge_query_vectors WHERE query_hash=? AND model=?", hash, model).Scan(&encoded); err != nil {
		return nil, err
	}
	var values []float32
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return nil, err
	}
	return values, nil
}
