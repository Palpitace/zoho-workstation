// input: 合成 Markdown、机型限制、正式/草稿知识和人工方向向量。
// output: 父上下文保留、排除机型、RRF 召回及无效索引隔离验证。
// pos: 无模型调用的知识整理与召回行为测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package knowledge_test

import (
	"math"
	"strings"
	"testing"
	"zoho-workstation/internal/knowledge"
)

func TestMarkdownSemanticParents(t *testing.T) {
	body, flags := knowledge.CleanMarkdown("# Instructions\n<font color='red'>Only D30; never D50.</font>\n<!-- OCR: verify voltage -->\n## Reset\n### Before reset\nDisconnect power.\n### Reset steps\nHold the button.\n## Print test\nDouble click.")
	if strings.Contains(body, "<font") || !strings.Contains(body, "verify voltage") || len(flags) != 1 {
		t.Fatalf("export cleanup dropped evidence: %q %+v", body, flags)
	}
	blocks, err := knowledge.MarkdownBlocks(body)
	if err != nil || len(blocks) != 2 {
		t.Fatalf("split failed: %+v %v", blocks, err)
	}
	for _, block := range blocks {
		if !strings.Contains(block.Text, "Only D30; never D50.") {
			t.Fatal("parent applicability missing")
		}
	}
	if !strings.Contains(blocks[0].Text, "Disconnect power.") || !strings.Contains(blocks[0].Text, "Hold the button.") {
		t.Fatal("procedure split away prerequisites")
	}
	recovered, flags := knowledge.CleanMarkdown("### <font>  \n</font><font>模板同步功能</font>\n### Template sync\nKeep accounts separate.")
	if !strings.Contains(recovered, "### 模板同步功能") || len(flags) != 1 {
		t.Fatalf("wrapped heading lost: %q %+v", recovered, flags)
	}
	if _, err = knowledge.MarkdownBlocks(strings.Repeat("x", 16*1024+1)); err == nil {
		t.Fatal("oversized semantic context silently truncated")
	}
}

func TestHybridVersionAndApplicability(t *testing.T) {
	entries := []knowledge.Entry{
		{ID: 1, Kind: "sop", Status: "published", Title: "Label positioning", SourceHash: "a", ExcludedProducts: []string{"D50"}, Blocks: []knowledge.Block{{ID: "faq", Text: "Calibrate when paper alignment is offset."}}},
		{ID: 2, Kind: "sop", Status: "published", Title: "WiFi", Product: "D50", SourceHash: "b", Blocks: []knowledge.Block{{ID: "faq", Text: "Check wireless router."}}},
		{ID: 3, Kind: "sop", Status: "draft", Title: "Unreviewed", SourceHash: "c", Blocks: []knowledge.Block{{ID: "faq", Text: "Do not send unpublished steps."}}},
	}
	vectors := []knowledge.Vector{
		{EntryID: 1, BlockID: "faq", SourceHash: "a", Model: "test", ChunkVersion: knowledge.ChunkVersion, Values: []float32{1, 0}},
		{EntryID: 2, BlockID: "faq", SourceHash: "b", Model: "test", ChunkVersion: knowledge.ChunkVersion, Values: []float32{1, 0}},
		{EntryID: 3, BlockID: "faq", SourceHash: "c", Model: "test", ChunkVersion: knowledge.ChunkVersion, Values: []float32{1, 0}},
	}
	result := knowledge.HybridSources(entries, vectors, []float32{1, 0}, "", "D30", "中文同义问题", 8)
	if len(result) != 1 || result[0].EntryID != 1 {
		t.Fatalf("hybrid did not enforce scope: %+v", result)
	}
	for _, model := range []string{"", "D50"} {
		r := knowledge.HybridSources(entries[:1], vectors, []float32{1, 0}, "", model, "alignment", 8)
		if len(r) != 0 {
			t.Fatalf("excluded/unknown model admitted: %s %+v", model, r)
		}
	}
	entries[0].ApplicableProducts = []string{"D30", "D31"}
	if result = knowledge.HybridSources(entries[:1], vectors, []float32{1, 0}, "", "D32", "paper alignment", 8); len(result) != 0 {
		t.Fatal("model outside whitelist admitted")
	}
	if result = knowledge.HybridSources(entries[:1], vectors, []float32{1, 0}, "", "D31", "中文同义问题", 8); len(result) != 1 {
		t.Fatal("compatible whitelist model rejected")
	}
	vectors[0].SourceHash = "old"
	if result = knowledge.HybridSources(entries[:1], vectors, []float32{1, 0}, "", "D30", "中文同义问题", 8); len(result) != 0 {
		t.Fatal("stale input vector reused")
	}
	if result = knowledge.HybridSources(entries[:1], nil, nil, "", "D30", "paper alignment", 8); len(result) != 1 {
		t.Fatal("keyword fallback unavailable")
	}
	if _, ok := knowledge.Cosine([]float32{float32(math.NaN())}, []float32{1}); ok {
		t.Fatal("nonfinite vector admitted")
	}
}

func TestExactErrorCodeAbstentionAndEmbeddingView(t *testing.T) {
	entry := knowledge.Entry{ID: 1, Kind: "sop", Status: "published", Title: "Driver diagnostics", SourceHash: "a", Blocks: []knowledge.Block{{ID: "faq", Text: "错误码ZXQ-917：verify the documented USB condition. Error code ZXQ-917 requires this exact check."}}}
	vectors := []knowledge.Vector{{EntryID: 1, BlockID: "faq", SourceHash: "a", ChunkVersion: knowledge.ChunkVersion, Values: []float32{1, 0}}}
	for _, query := range []string{"What does error code ZXQ-91 mean?", "错误码ZXQ-918是什么"} {
		if got := knowledge.HybridSources([]knowledge.Entry{entry}, vectors, []float32{1, 0}, "", "", query, 3); len(got) != 0 {
			t.Fatalf("unknown code borrowed nearby procedure: %+v", got)
		}
	}
	if got := knowledge.HybridSources([]knowledge.Entry{entry}, vectors, []float32{1, 0}, "", "", "错误码ZXQ-917", 3); len(got) != 1 {
		t.Fatal("exact documented code rejected")
	}
	unrelated := knowledge.Entry{ID: 2, Kind: "sop", Status: "published", Title: "Home printing", Blocks: []knowledge.Block{{ID: "faq", Text: "How to open the home page."}}}
	if got := knowledge.SelectSources([]knowledge.Entry{unrelated}, "", "", "How do I bake sourdough bread at home?", 3); len(got) != 0 {
		t.Fatal("one generic question/home term became relevant evidence")
	}
	original := "Only model D30. ![button diagram](https://images.example.test/opaque-id.png) [Windows installer](https://downloads.example.test/file)"
	view := knowledge.EmbeddingText("Printer setup", original)
	if strings.Contains(view, "https:") || !strings.Contains(view, "Only model D30") || !strings.Contains(view, "button diagram") || !strings.Contains(view, "Windows installer") {
		t.Fatalf("index view lost visible conditions: %q", view)
	}
	if !strings.Contains(original, "https://") {
		t.Fatal("immutable evidence mutated")
	}
}

func TestSemanticConfidenceKeepsRelevantNeighbours(t *testing.T) {
	entries := []knowledge.Entry{
		{ID: 1, Kind: "sop", Status: "published", Title: "Generic connection", SourceHash: "a", Blocks: []knowledge.Block{{ID: "one", Text: "Generic documented connection guidance."}}},
		{ID: 2, Kind: "sop", Status: "published", Title: "Application pairing", SourceHash: "b", Blocks: []knowledge.Block{{ID: "one", Text: "Connect the printer from within the application."}}},
	}
	vectors := []knowledge.Vector{
		{EntryID: 1, BlockID: "one", SourceHash: "a", ChunkVersion: knowledge.ChunkVersion, Values: []float32{.5, .8660254}},
		{EntryID: 2, BlockID: "one", SourceHash: "b", ChunkVersion: knowledge.ChunkVersion, Values: []float32{.38, .9249865}},
	}
	sources := knowledge.HybridSources(entries, vectors, []float32{1, 0}, "", "", "中文连接问题", 3)
	if len(sources) != 2 {
		t.Fatalf("confidence cutoff discarded useful lower-ranked context: %+v", sources)
	}
	if sources = knowledge.HybridSources(entries[1:], vectors[1:], []float32{1, 0}, "", "", "没有明确匹配", 3); len(sources) != 0 {
		t.Fatal("weak semantic channel did not abstain")
	}
}

func TestExactModelRetainsSpecificSingleTermEvidence(t *testing.T) {
	entry := knowledge.Entry{ID: 1, Kind: "sop", Status: "published", Product: "P1", Title: "P1 calibration", SourceHash: "a", Blocks: []knowledge.Block{{ID: "faq", Text: "Verify media before calibration."}}}
	if got := knowledge.SelectSources([]knowledge.Entry{entry}, "", "P1", "P1 printer calibration offset still failing", 3); len(got) != 1 {
		t.Fatal("exact-model calibration source dropped by multi-term threshold")
	}
	if got := knowledge.SelectSources([]knowledge.Entry{entry}, "", "P1", "P1 battery charging broken", 3); len(got) != 0 {
		t.Fatal("model alone turned unrelated evidence into a hit")
	}
}
