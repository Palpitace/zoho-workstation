// input: 隔离数据库、已运行的分析记录及另一个进程持有的文件锁。
// output: 并发 CLI 不误恢复活动请求，锁释放后可恢复中断记录的证据。
// pos: 分析命令生命周期回归测试。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"zoho-workstation/internal/config"
	"zoho-workstation/internal/store"
)

func TestConcurrentAnalysisDoesNotRecoverActiveRequest(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	path, _ := filepath.Abs("app.db")
	db, err := store.Open(path, "7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Reserve(ctx, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	req := store.AnalysisRequest{TicketID: "1", Kind: "jev", InputHash: "synthetic", SchemaVersion: "v1", RequestedModel: "synthetic", Round: DevelopmentRound}
	if _, _, err = db.StartAnalysis(ctx, req, false); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(path+".analysis.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DatabasePath: path, Jev: config.Model{Enabled: true, Name: "synthetic", URL: "https://example.invalid", Key: "synthetic"}}
	err = AnalyzeTickets(ctx, db, cfg, 1, "jev", DevelopmentRound, false)
	view, _ := db.Analysis(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "already running") || view.Current.Status != "running" {
		t.Fatalf("active request changed: %v status=%s", err, view.Current.Status)
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = AnalyzeTickets(ctx, db, cfg, 1, "jev", DevelopmentRound, false)
	view, _ = db.Analysis(ctx, req)
	if view.Current.Status != "failed" {
		t.Fatal("interrupted attempt not recovered after lock released")
	}
}
