// input: 用户明确上传的文档名称及有界文件字节。
// output: 固定 Python 提取器的带位置正文，拒绝不完整或不可读文档。
// pos: 本地文档解析边界，不调用模型。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"zoho-workstation/internal/knowledge"
)

func ExtractDocument(ctx context.Context, name string, content []byte) ([]knowledge.Block, error) {
	if len(content) == 0 || len(content) > 10*1024*1024 {
		return nil, errors.New("文档不能为空且不能超过 10 MiB")
	}
	if _, err := PythonExecutable(); err != nil {
		return nil, errors.New("文档解析环境不可用，请恢复完整下载包或安装锁定的 Python 环境")
	}
	if info, err := os.Stat("python/documents.py"); err != nil || info.IsDir() {
		return nil, errors.New("缺少文档解析入口，请恢复完整下载包")
	}
	body, _ := json.Marshal(map[string]string{"filename": name, "content_base64": base64.StdEncoding.EncodeToString(content)})
	var result struct {
		OK         bool              `json:"ok"`
		Blocks     []knowledge.Block `json:"blocks"`
		Incomplete bool              `json:"incomplete"`
		Error      string            `json:"error"`
	}
	err := pythonJSON(ctx, "python/documents.py", body, &result)
	if result.Incomplete {
		return nil, errors.New("文档包含无法提取的页面，请提供完整文本版后导入")
	}
	if err != nil || !result.OK || len(result.Blocks) == 0 {
		return nil, errors.New("文档提取失败：请使用 Markdown、TXT、DOCX 或包含可提取正文的 PDF；不支持扫描件和旧版 DOC")
	}
	size := 0
	seen := map[string]bool{}
	for _, block := range result.Blocks {
		size += len(block.Text)
		if block.ID == "" || seen[block.ID] || !usefulText(block.Text) || len(block.Text) > 8*1024 {
			return nil, errors.New("文档位置或正文超出限制")
		}
		seen[block.ID] = true
	}
	if size > 256*1024 {
		return nil, errors.New("文档正文超过 256 KiB，请按章节拆分后导入")
	}
	return result.Blocks, nil
}
