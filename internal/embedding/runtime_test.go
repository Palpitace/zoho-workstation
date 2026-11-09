// input: 合成向量及可选的已打包运行时和固定模型下载授权。
// output: 数值校验、真实本机多语言召回与进程关闭的验收。
// pos: 嵌入运行时的定向验证。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package embedding

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
	"zoho-workstation/internal/knowledge"
)

type loopbackOnly struct{ base http.RoundTripper }

func (tr loopbackOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() != "127.0.0.1" {
		return nil, errors.New("network disabled for offline verification")
	}
	return tr.base.RoundTrip(req)
}

func TestUnavailableFallsBack(t *testing.T) {
	r := New(t.TempDir())
	defer r.Close()
	if _, e := r.Embed(context.Background(), "printer", true); e == nil {
		t.Fatal("unavailable model accepted")
	}
	if !r.Status().Degraded {
		t.Fatal("unavailable status")
	}
	if validFile(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("missing model accepted")
	}
}
func TestNativeRetrieval(t *testing.T) {
	if os.Getenv("VERIFY_LOCAL_EMBEDDING") != "1" {
		t.Skip("explicit native model validation only")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	r := New(filepath.Join(".cache", "embedding-model"))
	defer r.Close()
	if err = r.Enable(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(30 * time.Minute)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for r.Status().State != "ready" {
		select {
		case <-deadline:
			t.Fatal("model initialization timed out")
		case <-ticker.C:
			if r.Status().State == "failed" {
				t.Fatal(r.Status().Error)
			}
		}
	}
	docs := []knowledge.Entry{
		{ID: 1, Kind: "sop", Status: "published", Title: "Bluetooth pairing", SourceHash: "1", Blocks: []knowledge.Block{{ID: "one", Text: "Open the Labelife application to select and pair the Bluetooth printer. Pairing in the phone system settings alone does not connect the application."}}},
		{ID: 2, Kind: "sop", Status: "published", Title: "Paper calibration", SourceHash: "2", Blocks: []knowledge.Block{{ID: "one", Text: "When printing crosses label boundaries, check the label dimensions and calibrate the media sensor. Preserve the model-specific button instructions."}}},
		{ID: 3, Kind: "sop", Status: "published", Title: "Windows USB driver", SourceHash: "3", Blocks: []knowledge.Block{{ID: "one", Text: "Install the correct Windows printer driver, connect the USB cable, and select the matching printer device."}}},
		{ID: 4, Kind: "sop", Status: "published", Title: "Restricted reset", SourceHash: "4", ApplicableProducts: []string{"D520"}, Blocks: []knowledge.Block{{ID: "one", Text: "D520 factory reset procedure. Follow the approved button sequence for this exact model."}}},
	}
	vectors := []knowledge.Vector{}
	for _, doc := range docs {
		v, e := r.Embed(context.Background(), knowledge.EmbeddingText(doc.Title, doc.Blocks[0].Text), false)
		if e != nil {
			t.Fatal(e)
		}
		vectors = append(vectors, knowledge.Vector{EntryID: doc.ID, BlockID: "one", SourceHash: doc.SourceHash, Model: ModelID, ChunkVersion: knowledge.ChunkVersion, Values: v})
	}
	cases := []struct {
		query, product string
		want           int64
	}{{"蓝牙在手机设置中连接了，但应用搜不到打印机", "", 1}, {"打印内容跨到下一张标签，边界偏移", "", 2}, {"电脑通过数据线找不到打印机，怎样安装驱动？", "", 3}, {"phone paired but app cannot see the device", "", 1}, {"printing overlaps the next sticker", "", 2}, {"D520 factory reset", "D520", 4}}
	lexical, hybrid, recalled := 0, 0, 0
	for _, tc := range cases {
		v, e := r.Embed(context.Background(), tc.query, true)
		if e != nil {
			t.Fatal(e)
		}
		old := knowledge.SelectSources(docs, "", tc.product, tc.query, 3)
		got := knowledge.HybridSources(docs, vectors, v, "", tc.product, tc.query, 3)
		if len(old) > 0 && old[0].EntryID == tc.want {
			lexical++
		}
		if len(got) > 0 && got[0].EntryID == tc.want {
			hybrid++
		}
		found := false
		for _, source := range got {
			if source.EntryID == tc.want {
				found = true
				break
			}
		}
		if found {
			recalled++
		} else {
			t.Errorf("retrieval case %q missing expected source %d in top3", tc.query, tc.want)
		}
	}
	t.Logf("top1 keyword=%d/%d hybrid=%d/%d; hybrid recall@3=%d/%d", lexical, len(cases), hybrid, len(cases), recalled, len(cases))
	if hybrid <= lexical {
		t.Error("hybrid did not improve this fixed bilingual sample")
	}
	v, e := r.Embed(context.Background(), "factory reset", true)
	if e != nil {
		t.Fatal(e)
	}
	for _, source := range knowledge.HybridSources(docs, vectors, v, "", "D530", "factory reset", 8) {
		if source.EntryID == 4 {
			t.Fatal("cross-model procedure leaked")
		}
	}
	r.Close()
	if !r.Status().Degraded {
		t.Fatal("process remained ready after close")
	}
	previous := http.DefaultTransport
	http.DefaultTransport = loopbackOnly{previous}
	defer func() { http.DefaultTransport = previous }()
	offline := New(filepath.Join(".cache", "embedding-model"))
	defer offline.Close()
	if err := offline.Enable(); err != nil {
		t.Fatal(err)
	}
	offlineDeadline := time.After(2 * time.Minute)
	for offline.Status().State != "ready" {
		select {
		case <-offlineDeadline:
			t.Fatal("offline initialization timed out")
		case <-ticker.C:
			if offline.Status().State == "failed" {
				t.Fatal(offline.Status().Error)
			}
		}
	}
	if _, err := offline.Embed(context.Background(), "蓝牙连接", true); err != nil {
		t.Fatal(err)
	}
	t.Log("cached model initializes and embeds with all external HTTP blocked; child exits on Close")
}
