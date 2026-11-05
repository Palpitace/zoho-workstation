一旦我所属的文件夹有所变化请更新我。

负责文档存在性、直接索引和三行文件头格式检查。
由 Makefile 调用，语义仍由 Reviewer 抽查。
不读取 .env 内容，不扫描第三方安装、Submodule 或运行数据。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 检查范围 |
| main_test.go | 跨平台回归 | LF/CRLF、build tag 与缺失头部的公共命令验证 |
| main.go | 检查入口 | 轻量文档结构检查 |

头部检查先统一 CRLF 为 LF，再跳过 shebang/build tag/空行，避免 Windows checkout 换行误报；缺失头部仍拒绝。
