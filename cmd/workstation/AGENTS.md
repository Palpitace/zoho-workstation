一旦我所属的文件夹有所变化请更新我。

负责启动采样、脱敏、分析命令和 Gin 查询 API。
调用 config、zoho、syncer、store、analysis 并处理退出信号。
不包含模型提示词或 Zoho 写入操作。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | CLI 职责和命令 |
| main.go | 启动入口 | 命令分派、本机查询路由及前端静态文件 |
| analysis.go | 分析编排 | prepare/analyze、版本记录及开发调用额度 |
| analysis_test.go | 生命周期验证 | 并发命令不误恢复活动请求，退出后允许恢复 |

当前本机平台为 Linux。分析命令先获取同数据库路径的内核文件锁，再恢复中断状态；第二个分析命令立即报 busy，进程退出/崩溃自动释放锁。查询 API 可以同时读取；后续后台分析也必须遵守同一所有权。

从项目根运行 `go run ./cmd/workstation sample -size 50 -max 3`，先固定最多 50 个 ID，本轮最多补齐 3 张；后续运行复用相同集合。
`prepare -max 20` 在忽略的 data/review 写脱敏副本与固定开发/留出清单，已有清单保留 ID 和分组，仅更新内容 Hash；不是人工标注。`analyze -kind jev -max 1` 或 `-kind bub` 调用独立能力，默认 development-1 轮次跨重启累计最多 20 次、总计最多 60 次；没有隐式无限重试。`-force` 仍受额度限制。不要并发启动两个分析命令，恢复逻辑只由唯一执行者调用；当前查询服务不启动模型请求。
`serve` 提供同源 API 与 web/dist 静态文件，开发前端由 Vite 代理 /api。分析查询为 GET /api/tickets/:id/analysis，分别返回 jev/bub 的 current/success/stale。Bub 需要 python/.venv，结果增加 summary/usage/request_id。自动定时与手动变更端点尚未实现。
