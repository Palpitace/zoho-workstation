// input: 临时 SQLite、合成 Markdown、具名知识库与固定维数的人工向量。
// output: 部门/分类隔离、不可变版本、过期向量隔离及旧来源兼容验证。
// pos: 知识目录及本地索引的小型集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/store"
)

func TestLibraryScopeMarkdownAndIndexVersions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "knowledge.db")
	db, err := store.Open(path, "labelife")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	libraries, err := db.ListLibraries(ctx)
	if err != nil || len(libraries) != 1 || len(libraries[0].DepartmentIDs) != 1 || libraries[0].DepartmentIDs[0] != "labelife" {
		t.Fatalf("default library: %+v %v", libraries, err)
	}
	own := libraries[0]
	foreign, err := db.SaveLibrary(ctx, knowledge.Library{Name: "Other", DepartmentIDs: []string{"other"}})
	if err != nil {
		t.Fatal(err)
	}
	category, err := db.SaveCategory(ctx, knowledge.Category{LibraryID: own.ID, Name: "故障排查"})
	if err != nil {
		t.Fatal(err)
	}
	otherCategory, err := db.SaveCategory(ctx, knowledge.Category{LibraryID: foreign.ID, Name: "排查"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := db.SaveKnowledge(ctx, knowledge.Entry{LibraryID: own.ID, CategoryID: category.ID, Kind: "sop", Title: "Print checks", Scope: "technical", SourceHash: "one", Markdown: "# Printer checks\n\nOnly for supported firmware.\n\n## Blank prints\nCheck media.\n\n## Offset prints\nCalibrate media."})
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Blocks) != 2 {
		t.Fatalf("semantic sections: %+v", draft.Blocks)
	}
	if err = db.PublishKnowledge(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	hidden, err := db.SaveKnowledge(ctx, knowledge.Entry{LibraryID: foreign.ID, Kind: "sop", Title: "Print checks other", SourceHash: "two", Markdown: "Check media using the other department's private process."})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PublishKnowledge(ctx, hidden.ID); err != nil {
		t.Fatal(err)
	}
	sources, err := db.KnowledgeSources(ctx, "", "", "media", 10)
	if err != nil || len(sources) != 2 {
		t.Fatalf("foreign library leaked: %+v %v", sources, err)
	}
	for _, source := range sources {
		if source.EntryID != draft.ID {
			t.Fatal("foreign library was retrieved")
		}
	}
	if err = db.MoveKnowledge(ctx, draft.ID, otherCategory.ID); err == nil {
		t.Fatal("cross-library category accepted")
	}
	if _, err = db.ScopedKnowledge(ctx, 0, "", "published"); err == nil {
		t.Fatal("blank department widened access")
	}
	vector := knowledge.Vector{EntryID: draft.ID, BlockID: draft.Blocks[0].ID, SourceHash: draft.SourceHash, Model: "pinned-model-sha256", ChunkVersion: knowledge.ChunkVersion, Values: make([]float32, 1024)}
	vector.Values[0] = 1
	if err = db.SaveKnowledgeQuery(ctx, "query-signature", vector.Model, vector.Values); err != nil {
		t.Fatal(err)
	}
	query, err := db.KnowledgeQuery(ctx, "query-signature", vector.Model)
	if err != nil || len(query) != 1024 || query[0] != 1 {
		t.Fatalf("query cache: %v %v", len(query), err)
	}
	if _, err = db.KnowledgeQuery(ctx, "query-signature", "other-model"); err == nil {
		t.Fatal("query cache mixed model versions")
	}
	if err = db.SaveKnowledgeVector(ctx, vector); err != nil {
		t.Fatal(err)
	}
	vectors, err := db.KnowledgeVectors(ctx, vector.Model, vector.ChunkVersion)
	if err != nil || len(vectors) != 1 {
		t.Fatalf("saved vector: %v %v", vectors, err)
	}
	if vectors, err = db.KnowledgeVectors(ctx, "other-model", vector.ChunkVersion); err != nil || len(vectors) != 0 {
		t.Fatal("wrong model reused old vector")
	}
	revised := draft
	revised.ID = 0
	revised.LibraryID = 0
	revised.ReplacesID = draft.ID
	revised.SourceHash = "three"
	revised.Markdown = "# Printer checks\nCheck the media size before calibration."
	next, err := db.SaveKnowledge(ctx, revised)
	if err != nil || next.LibraryID != own.ID {
		t.Fatalf("revision placement: %+v %v", next, err)
	}
	if err = db.PublishKnowledge(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if vectors, err = db.KnowledgeVectors(ctx, vector.Model, vector.ChunkVersion); err != nil || len(vectors) != 0 {
		t.Fatal("archived vectors remained current")
	}
	if err = db.SaveKnowledgeVector(ctx, vector); err == nil {
		t.Fatal("index write to archived version accepted")
	}
	restored, err := db.RestoreKnowledge(ctx, draft.ID)
	if err != nil || restored.Status != "draft" || restored.Markdown != draft.Markdown {
		t.Fatalf("Markdown restoration: %+v %v", restored, err)
	}
	db.Close()
	db, err = store.Open(path, "labelife")
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.Knowledge(ctx, draft.ID)
	if err != nil || old.Markdown != draft.Markdown || old.Blocks[0].ID != draft.Blocks[0].ID {
		t.Fatalf("immutable version lost: %+v %v", old, err)
	}
}

func TestLegacyMarkdownAndCategoryRename(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "legacy.db"), "one")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy, err := db.SaveKnowledge(ctx, knowledge.Entry{Kind: "sop", Title: "Old paragraph import", SourceHash: "legacy", Blocks: []knowledge.Block{{ID: "page-3", Text: "Keep the exact old citation."}}})
	if err != nil {
		t.Fatal(err)
	}
	reread, err := db.Knowledge(ctx, legacy.ID)
	if err != nil || !reread.MarkdownMigrated || reread.Markdown != "Keep the exact old citation." || reread.Blocks[0].ID != "page-3" {
		t.Fatalf("legacy conversion destroyed provenance: %+v %v", reread, err)
	}
	category, err := db.SaveCategory(ctx, knowledge.Category{LibraryID: reread.LibraryID, Name: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.MoveKnowledge(ctx, legacy.ID, category.ID); err != nil {
		t.Fatal(err)
	}
	category.Name = "Renamed"
	if _, err = db.SaveCategory(ctx, category); err != nil {
		t.Fatal(err)
	}
	categories, err := db.ListCategories(ctx, reread.LibraryID)
	if err != nil || len(categories) != 1 || categories[0].Name != "Renamed" {
		t.Fatalf("rename: %+v %v", categories, err)
	}
}

func TestAutomaticDefaultNeverBroadensLibraryBinding(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "default.db"), "one")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	libraries, err := db.ListLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := libraries[0]
	old.DepartmentIDs = []string{"other"}
	if _, err = db.SaveLibrary(ctx, old); err != nil {
		t.Fatal(err)
	}
	library, err := db.EnsureDefaultLibrary(ctx, "one")
	if err != nil || library.ID == old.ID {
		t.Fatalf("unbound default reused: %+v %v", library, err)
	}
	libraries, err = db.ListLibraries(ctx)
	if err != nil || len(libraries) != 2 || len(libraries[0].DepartmentIDs) != 1 || libraries[0].DepartmentIDs[0] != "other" {
		t.Fatalf("old binding silently broadened: %+v %v", libraries, err)
	}
	entry, err := db.SaveKnowledge(ctx, knowledge.Entry{Kind: "sop", Title: "Imported default", SourceHash: "default", Markdown: "Check media."})
	if err != nil || entry.LibraryID != library.ID {
		t.Fatalf("new default not used: %+v %v", entry, err)
	}
}

func TestOldKnowledgeSchemaRetainsCitationOnUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE knowledge_entries(id INTEGER PRIMARY KEY,replaces_id INTEGER NOT NULL DEFAULT 0,kind TEXT NOT NULL,status TEXT NOT NULL,title TEXT NOT NULL,product TEXT NOT NULL DEFAULT '',source_name TEXT NOT NULL DEFAULT '',source_hash TEXT NOT NULL,source_ticket_id TEXT NOT NULL DEFAULT '',message_ids TEXT NOT NULL,blocks TEXT NOT NULL,created_at TEXT NOT NULL,published_at TEXT NOT NULL DEFAULT '',UNIQUE(kind,source_ticket_id,source_hash));
 INSERT INTO knowledge_entries(id,kind,status,title,source_hash,message_ids,blocks,created_at,published_at) VALUES(55,'sop','published','Old FAQ','original','[]','[{"id":"page-7","text":"Original printed instructions."}]','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z');`)
	if err != nil {
		old.Close()
		t.Fatal(err)
	}
	old.Close()
	db, err := store.Open(path, "one")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrated, err := db.Knowledge(ctx, 55)
	if err != nil || migrated.ID != 55 || migrated.SourceHash != "original" || migrated.Blocks[0].ID != "page-7" || !migrated.MarkdownMigrated || migrated.Markdown != "Original printed instructions." || migrated.LibraryID == 0 {
		t.Fatalf("upgrade changed evidence: %+v %v", migrated, err)
	}
	sources, err := db.KnowledgeSources(ctx, "", "", "printed instructions", 2)
	if err != nil || len(sources) != 1 || sources[0].ID != "knowledge:55:page-7" {
		t.Fatalf("old citation inaccessible: %+v %v", sources, err)
	}
}

func TestPublishRetiresLegacyDraftChainAndRejectsSibling(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chain.db")
	db, err := store.Open(path, "one")
	if err != nil {
		t.Fatal(err)
	}
	create := func(hash string, parent int64) knowledge.Entry {
		t.Helper()
		entry, err := db.SaveKnowledge(ctx, knowledge.Entry{Kind: "sop", Title: "Media", SourceHash: hash, ReplacesID: parent, Markdown: "Check media."})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	first := create("first", 0)
	if err = db.PublishKnowledge(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	draft := create("draft", first.ID)
	last := create("last", draft.ID)
	sibling := create("sibling", draft.ID)
	// Historical/imported databases can retain immediate draft parent pointers;
	// current SaveKnowledge normally collapses these to the published predecessor.
	db.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec("UPDATE knowledge_entries SET replaces_id=? WHERE id IN (?,?)", draft.ID, last.ID, sibling.ID); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()
	db, err = store.Open(path, "one")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.PublishKnowledge(ctx, last.ID); err != nil {
		t.Fatal(err)
	}
	active, err := db.ListKnowledge(ctx, "", "published")
	if err != nil || len(active) != 1 || active[0].ID != last.ID {
		t.Fatalf("old published ancestor remained active: %+v %v", active, err)
	}
	if err = db.PublishKnowledge(ctx, sibling.ID); !errors.Is(err, store.ErrKnowledgeConflict) {
		t.Fatalf("stale sibling accepted: %v", err)
	}
	for _, id := range []int64{first.ID, draft.ID} {
		entry, err := db.Knowledge(ctx, id)
		if err != nil || entry.Status != "archived" || entry.Markdown != "Check media." {
			t.Fatalf("ancestor evidence lost: %+v %v", entry, err)
		}
	}
}
