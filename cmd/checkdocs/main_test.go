// input: 含 LF/CRLF 与 build tag 的合成文档目录及检查器子进程。
// output: 跨平台换行不影响头部检查，缺少头部仍被拒绝的证据。
// pos: 文档命令跨平台回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("CHECKDOCS_TEST_CHILD") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}
func TestHeaderCheckHandlesPlatformNewlines(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, newline string
		valid         bool
	}{{"LF", "\n", true}, {"CRLF", "\r\n", true}, {"missing header", "\r\n", false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			doc := "一旦我所属的文件夹有所变化请更新我。\n| AGENTS.md | 约定 | 合成 |\n| sample.go | 代码 | 合成 |\n"
			code := "//go:build !windows\n\n// input: synthetic\n// output: synthetic\n// pos: synthetic。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。\npackage example\n"
			if !tc.valid {
				code = strings.ReplaceAll(code, "// input: synthetic\n", "")
			}
			for name, body := range map[string]string{"AGENTS.md": doc, "sample.go": code} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(body, "\n", tc.newline)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(binary)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "CHECKDOCS_TEST_CHILD=1")
			if err := cmd.Run(); (err == nil) != tc.valid {
				t.Fatalf("accepted=%t want=%t", err == nil, tc.valid)
			}
		})
	}
}
