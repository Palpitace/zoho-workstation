// input: 临时 SQLite 数据库、合成文档版本和已保存工单。
// output: 草稿隔离、显式发布、版本竞争、来源兼容性及重启持久性的行为验证。
// pos: 知识生命周期集成测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"zoho-workstation/internal/knowledge"
	"zoho-workstation/internal/store"
	"zoho-workstation/internal/ticket"
)

func TestKnowledgePublicationAndRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "knowledge.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	draft := knowledge.Entry{Kind: "sop", Status: "published", Title: "WiFi 连接排查", Product: "D30", SourceHash: "document-v1", SourceName: "wifi.md", Blocks: []knowledge.Block{{ID: "section-1", Text: "仅当 WiFi 连接失败时检查路由器设置。"}}}
	first, err := db.SaveKnowledge(ctx, draft)
	if err != nil || first.Status != "draft" {
		t.Fatalf("import published without review: %+v %v", first, err)
	}
	if sources, err := db.KnowledgeSources(ctx, "", "D30", "WiFi 连接失败", 5); err != nil || len(sources) != 0 {
		t.Fatalf("draft entered advice: %v %v", sources, err)
	}
	duplicate, err := db.SaveKnowledge(ctx, draft)
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("duplicate import: %+v %v", duplicate, err)
	}
	if err := db.PublishKnowledge(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if sources, err := db.KnowledgeSources(ctx, "", "D30", "WiFi 连接失败", 5); err != nil || len(sources) != 1 || sources[0].EntryID != first.ID || sources[0].BlockID != "section-1" {
		t.Fatalf("published reference missing: %v %v", sources, err)
	}
	for _, product := range []string{"D50", "", "unknown", "others"} {
		if sources, err := db.KnowledgeSources(ctx, "", product, "WiFi", 5); err != nil || len(sources) != 0 {
			t.Fatalf("wrong/unknown model %q borrowed D30 SOP: %v %v", product, sources, err)
		}
	}
	if sources, err := db.KnowledgeSources(ctx, "", "D30", "battery charging", 5); err != nil || len(sources) != 0 {
		t.Fatalf("model match admitted unrelated evidence: %v %v", sources, err)
	}
	draft.ReplacesID, draft.SourceHash = first.ID, "document-v2"
	second, err := db.SaveKnowledge(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	draft.SourceHash = "document-v2-sibling"
	sibling, err := db.SaveKnowledge(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if sources, err := db.KnowledgeSources(ctx, "", "D30", "WiFi", 5); err != nil || len(sources) != 1 || sources[0].EntryID != first.ID {
		t.Fatalf("draft prematurely withdrew previous SOP: %v %v", sources, err)
	}
	if err := db.PublishKnowledge(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.PublishKnowledge(ctx, sibling.ID); !errors.Is(err, store.ErrKnowledgeConflict) {
		t.Fatalf("stale sibling publication accepted: %v", err)
	}
	if old, err := db.Knowledge(ctx, first.ID); err != nil || old.Status != "archived" || old.Blocks[0].Text != first.Blocks[0].Text {
		t.Fatalf("prior content lost: %+v %v", old, err)
	}
	db.Close()
	db, err = store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	if sources, err := db.KnowledgeSources(ctx, "", "D30", "WiFi", 5); err != nil || len(sources) != 1 || sources[0].EntryID != second.ID {
		t.Fatalf("published version did not survive restart: %v %v", sources, err)
	}
	if err := db.ArchiveKnowledge(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if sources, err := db.KnowledgeSources(ctx, "", "D30", "WiFi", 5); err != nil || len(sources) != 0 {
		t.Fatalf("archived source still eligible: %v %v", sources, err)
	}
	draft.SourceHash, draft.ReplacesID = "document-v1", 0
	archived, err := db.SaveKnowledge(ctx, draft)
	if err != nil || archived.ID != first.ID || archived.Status != "archived" {
		t.Fatalf("repeat import resurrected archived source: %+v %v", archived, err)
	}
	if entries, err := db.ListKnowledge(ctx, "路由器", ""); err != nil || len(entries) != 3 {
		t.Fatalf("retained history not searchable: %+v %v", entries, err)
	}
}

func TestKnowledgeCaseSources(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "cases.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Reserve(ctx, []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	snapshot := ticket.Snapshot{ID: "1", DepartmentID: "7", Product: "D30", Complete: true, Messages: []ticket.Message{{ID: "m1", Role: "customer", Body: "WiFi still fails after reset"}}}
	if err := db.Save(ctx, ticket.RawSnapshot{}, snapshot); err != nil {
		t.Fatal(err)
	}
	entry := knowledge.Entry{Kind: "case", Title: "WiFi reset did not resolve", Product: "D30", SourceTicketID: "1", SourceHash: "case-v1", MessageIDs: []string{"m1"}, Blocks: []knowledge.Block{{ID: "case", Text: "Customer confirmed reset; WiFi issue remains unresolved."}}}
	first, err := db.SaveKnowledge(ctx, entry)
	if err != nil || first.Status != "published" {
		t.Fatalf("case was not automatically available: %+v %v", first, err)
	}
	duplicate, err := db.SaveKnowledge(ctx, entry)
	if err != nil || duplicate.ID != first.ID {
		t.Fatal("duplicate case appended")
	}
	if sources, err := db.KnowledgeSources(ctx, "2", "D30", "WiFi reset", 5); err != nil || len(sources) != 1 || sources[0].TicketID != "1" || sources[0].SourceHash != "case-v1" || sources[0].MessageIDs[0] != "m1" {
		t.Fatalf("case lineage missing: %v %v", sources, err)
	}
	if sources, err := db.KnowledgeSources(ctx, "1", "D30", "WiFi", 5); err != nil || len(sources) != 0 {
		t.Fatalf("ticket matched its own case: %v %v", sources, err)
	}
	entry.SourceHash = "case-v2"
	second, err := db.SaveKnowledge(ctx, entry)
	if err != nil {
		t.Fatal(err)
	}
	if sources, err := db.KnowledgeSources(ctx, "2", "D30", "WiFi", 5); err != nil || len(sources) != 1 || sources[0].EntryID != second.ID {
		t.Fatalf("previous case version remains active: %v %v", sources, err)
	}
	snapshot.Complete = false
	if err := db.Save(ctx, ticket.RawSnapshot{}, snapshot); err != nil {
		t.Fatal(err)
	}
	if sources, err := db.KnowledgeSources(ctx, "2", "D30", "WiFi", 5); err != nil || len(sources) != 0 {
		t.Fatalf("incomplete case source remains eligible: %v %v", sources, err)
	}
	snapshot.Complete = true
	if err := db.Save(ctx, ticket.RawSnapshot{}, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := db.StopSync(ctx, "1", "ticket confirmed merged in Zoho"); err != nil {
		t.Fatal(err)
	}
	if sources, err := db.KnowledgeSources(ctx, "2", "D30", "WiFi", 5); err != nil || len(sources) != 0 {
		t.Fatalf("stopped case source remains eligible: %v %v", sources, err)
	}
	entry.SourceTicketID, entry.SourceHash = "2", "pending-ticket"
	if _, err := db.SaveKnowledge(ctx, entry); err == nil {
		t.Fatal("pending source created a published case")
	}
	generic := knowledge.Entry{Kind: "sop", Title: "WiFi general", SourceHash: "generic", Blocks: []knowledge.Block{{ID: "section-1", Text: "Check WiFi availability; do not reset any device without applicable instructions."}}}
	generic, err = db.SaveKnowledge(ctx, generic)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PublishKnowledge(ctx, generic.ID); err != nil {
		t.Fatal(err)
	}
	if sources, err := db.KnowledgeSources(ctx, "2", "", "WiFi", 5); err != nil || len(sources) != 1 || sources[0].EntryID != generic.ID {
		t.Fatalf("unknown model cannot use generic SOP: %v %v", sources, err)
	}
}

func TestKnowledgeEditedDraftKeepsEffectivePredecessor(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "revisions.db"), "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entry := knowledge.Entry{Kind: "sop", Title: "WiFi", SourceHash: "original", Blocks: []knowledge.Block{{ID: "section-1", Text: "Check WiFi."}}}
	published, err := db.SaveKnowledge(ctx, entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PublishKnowledge(ctx, published.ID); err != nil {
		t.Fatal(err)
	}
	entry.ReplacesID, entry.SourceHash = published.ID, "draft"
	draft, err := db.SaveKnowledge(ctx, entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.ReplacesID, entry.SourceHash = draft.ID, "edited-draft"
	edited, err := db.SaveKnowledge(ctx, entry)
	if err != nil || edited.ReplacesID != published.ID {
		t.Fatalf("draft editing lost effective predecessor: %+v %v", edited, err)
	}
	if err := db.PublishKnowledge(ctx, edited.ID); err != nil {
		t.Fatal(err)
	}
	if entries, err := db.ListKnowledge(ctx, "", "published"); err != nil || len(entries) != 1 || entries[0].ID != edited.ID {
		t.Fatalf("edited draft left old SOP effective: %+v %v", entries, err)
	}
	if err := db.PublishKnowledge(ctx, draft.ID); !errors.Is(err, store.ErrKnowledgeConflict) {
		t.Fatalf("earlier draft superseded reviewed edit: %v", err)
	}
}
