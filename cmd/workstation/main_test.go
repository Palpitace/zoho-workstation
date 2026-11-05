// input: 隔离工作目录、无效合成配置和真实 CLI 子进程。
// output: 启动失败留存日志且不泄露配置值的行为证据。
// pos: 下载版启动诊断回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("WORKSTATION_TEST_CHILD") == "1" {
		os.Args = []string{"workstation", "serve"}
		main()
		return
	}
	os.Exit(m.Run())
}
func TestStartupFailureIsLoggedWithoutConfiguration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_PORT=synthetic-private-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary)
	command.Dir = dir
	command.Env = append(os.Environ(), "WORKSTATION_TEST_CHILD=1")
	if err = command.Run(); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	body, err := os.ReadFile(filepath.Join(dir, "logs", "workstation.log"))
	if err != nil {
		t.Fatal("startup failure has no persistent log")
	}
	if !strings.Contains(string(body), "invalid APP_PORT") || strings.Contains(string(body), "synthetic-private-value") {
		t.Fatal("startup log missing safe diagnosis or leaked config")
	}
}
