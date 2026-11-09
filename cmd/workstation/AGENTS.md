一旦我所属的文件夹有所变化请更新我。

负责命令选择、配置指定的 IP 监听和服务退出信号。
调用 config/store/zoho/syncer，委托 app 运行服务及开发分析。
不包含模型提示词或 Zoho 写入操作。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | CLI 职责和命令 |
| main_test.go | CLI 回归 | 启动失败留存安全日志 |
| main.go | 启动入口 | 命令分派、数据库锁、显式 IP 监听及优雅关闭 |

支持 Linux、macOS 与 Windows。锁由 store 统一提供，sample 获取 sync 锁，prepare/analyze 获取 analysis 锁，serve 在生命周期内持有两把锁；服务运行时使用页面操作，不能再并发执行 CLI 同步/分析或改写开发清单。进程退出/崩溃自动释放锁。

从项目根运行 `go run ./cmd/workstation sample -size 50 -max 3`，先固定最多 50 个 ID，本轮最多补齐 3 张；后续运行复用相同集合，单次并发由 SYNC_CONCURRENCY 控制（默认 3，上限 5），-max 继续限制本轮工单数。
`prepare -max 20` 在忽略的 data/review 写脱敏副本与固定开发/留出清单，已有清单保留 ID 和分组，仅更新会话 Hash；不是人工标注。`analyze -kind jev -max 1` 或 `-kind bub` 调用所选能力，默认 development-1 仅作为跨重启调用统计轮次，用户已关闭单轮及累计请求额度；没有隐式无限重试。Bub 先独立总结，Jev 仅基于当前成功总结评估；没有当前总结时拒绝 Jev，不隐式追加 Bub 请求。完整实际输入另按 Hash 保存。`-force` 可手动重跑已终结输入，不能绕过正在运行的同输入互斥。不要并发启动两个分析命令，恢复逻辑只由唯一执行者调用。
`serve` 提供同源 API、web/dist、自动同步及独立分析。具体周期、开发评估范围、调用计数和 POST 防护见 ../../internal/app/AGENTS.md。Bub 需要 python/.venv，不是纯 Go 单文件运行。

`check` 只校验配置、SQLite/操作锁、前端及启用的 Python SDK 导入及代理 transport 实际构造（与 Bub 共用最小进程环境），不调用网络。main 在配置读取之前初始化终端+文件日志，记录安全错误和正常退出，10 MiB、3 个备份、7 天保留；路径固定 logs/workstation.log。不可写时直接报告终端错误，不假称日志已保存。

滚动测试版所有命令在打开数据库后启用 SYNC_RECENT_LIMIT 范围，CLI sample 同样刷新最新50张；该模式优先于-size（仅0模式继续使用显式采样数量），-max仍限制本轮详情数量。prepare/analyze 不会绕过范围读取或发送旧工单；旧研发清单含范围外ID时保留清单并明确报不可用，不自动删除。
