// input: 自有目录 AGENTS.md、直接文件索引和代码文件头。
// output: 缺失目录文档、索引或三行头部的轻量检查结果。
// pos: 文档结构验证命令。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func excluded(path string) bool {
	if filepath.ToSlash(path) == "docs/zoho-oas" {
		return true
	}
	switch filepath.Base(path) {
	case ".git", "data", "logs", "raw", "samples", "node_modules", ".venv", "__pycache__", ".cache", ".pytest_cache", ".ruff_cache", "dist", "bin":
		return true
	}
	return false
}

func main() {
	problems := []string{}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if excluded(path) {
				return filepath.SkipDir
			}
			docPath := filepath.Join(path, "AGENTS.md")
			body, err := os.ReadFile(docPath)
			if err != nil {
				problems = append(problems, docPath+": missing")
				return nil
			}
			doc := string(body)
			if !strings.HasPrefix(doc, "一旦我所属的文件夹有所变化请更新我。") {
				problems = append(problems, docPath+": opening declaration missing")
			}
			children, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			for _, child := range children {
				if excluded(filepath.Join(path, child.Name())) && child.Name() != "zoho-oas" {
					continue
				}
				if !strings.Contains(doc, "| "+child.Name()+" |") {
					problems = append(problems, docPath+": index missing "+child.Name())
				}
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".py", ".ts", ".js", ".svelte", ".css", ".sh":
		default:
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(body), "\n")
		if len(lines) > 0 && strings.HasPrefix(lines[0], "#!") {
			lines = lines[1:]
		}
		for len(lines) > 0 && (strings.HasPrefix(lines[0], "//go:build") || strings.HasPrefix(lines[0], "// +build") || lines[0] == "") {
			lines = lines[1:]
		}
		for i, label := range []string{"input:", "output:", "pos:"} {
			if len(lines) <= i || !strings.Contains(lines[i], label) {
				problems = append(problems, path+": missing "+label)
			}
		}
		if len(lines) < 3 || !strings.Contains(lines[2], "一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。") {
			problems = append(problems, path+": update notice missing")
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "document traversal failed")
		os.Exit(1)
	}
	for _, problem := range problems {
		fmt.Fprintln(os.Stderr, problem)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Println("document structure: PASS")
}
