一旦我所属的文件夹有所变化请更新我。

负责单 Go 服务的同步/独立分析生命周期、开发采样及 HTTP 管理边界。
组合 config、store、syncer、analysis，向同源 Svelte 返回工单和状态。
不实现模型 SDK、不扩部门、不引入持久任务队列或通知。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 编排边界与命令契约 |
| analysis.go | 分析输入与开发入口 | 固定数据集、同版 Jev 参考、实际输入版本及有限分析命令 |
| analysis_test.go | 恢复测试 | 互斥分析避免误恢复活动请求 |
| app.go | 生命周期 | 短间隔批次/退避、常规轮询、分析及关闭 |
| http.go | 同源 API | 工单/分析状态、预算及受保护操作 |
| sync_test.go | 同步调度回归 | 首轮接续、失败/429 等待及关闭取消 |
| app_test.go | 小型集成验证 | 自动失败重试、查询不阻塞、操作防护及重启 |
| reference_test.go | 参考流程验证 | 完整会话、Jev 首次尝试等待及失败/过期参考隔离 |

Start 必须在 HTTP 监听成功后调用，先持有同库 sync/analysis 进程锁再恢复 running。服务最多一个同步、一个 Jev、一个 Bub 同时工作。启动立即调度；一个同步 worker 每批最多 3 张，有剩余候选则等待 1 秒继续，不再等待 SYNC_INTERVAL。失败批次退避从 5 秒倍增到 2 分钟；429 还需等待至少 Retry-After。全部候选处理后由 SYNC_INTERVAL 触发常规增量轮询。等待期间保持同步互斥且支持关闭取消；每批有 5 分钟 context 上限，日志只记录耗时和成功/失败/停止/剩余数量。同版本最多一次自动失败重试。没有外部队列，待分析输入由已保存快照与历史结果计算，重启后重新计算即可恢复。

同工单自动流程先尝试 Jev，首次尝试终结即尝试启动 Bub；成功时附带当前会话版本的评估作补充参考，关闭/失败/超时则使用完整会话独立总结。等待有 90 秒调用上限，不阻塞其他工单、同步或查看。匹配历史中已有终结尝试时，Jev 重试不能再次阻挡 Bub；状态从现有结果计算，无新任务表。手动 Bub 只使用已有参考，不隐式调用 Jev；手动 Jev 不立即追加 Bub 计费请求，自动周期会处理参考变化造成的过期。

CLI、自动调用及 HTTP 状态共用 requestForRecord，避免调用和过期判断使用不同 Hash。Jev 使用完整会话 Hash；Bub v2 保留完整会话并添加 conversation_hash、可空 jev_reference，实际输入包含来源运行 ID/版本/模型和评估标签，按内容 Hash 私有保存。只附同版本的成功结果，未知/失败/过期结果不伪装成当前评估。

开发自动分析仅 manifest 中 development ID，首次至少保存 20 张时自动建立 17/3 开发与留出集合；已有集合不扩展。所有 50 张以内的工单仍展示，可在预算内手动分析。调用输入按 Hash 在 data/review 私有保存；本地输入校验失败记录 invalid、不占请求额度。Python 环境缺失返回 503 并显示运行错误，不将输入永久标为 invalid；环境修复后可自动恢复。额度耗尽继续同步/展示，不自动换轮次。

服务默认监听 loopback，容器转发时入口可按 APP_LISTEN_HOST 监听容器接口；API 仍校验本机 Host，POST 还需 GET /api/status 提供的临时 X-CSRF-Token 及允许的 Origin。Windows 经宿主回环转发访问 localhost:8080，不开放任意 IP/域名的 Host 或 Origin。开发 Vite 仅允许 localhost/127.0.0.1 的 5173 端口。无公网或生产部署授权。GET 状态返回进度/预算/能力数量；POST /api/sync 与 /api/tickets/:id/analysis/:kind 返回已受理，不宣称已完成。

自动开发分析复用 data/review/manifest.json 的 development ID，排除 holdout；固定采样仍由 store 限制最多 50。CLI PrepareInputs/AnalyzeTickets 与服务共用输入和结果协议。网络尝试前持久占预算；模型关闭不要求其配置。
CLI prepare 同样持有 analysis 锁，服务内部初始化已持锁且加进程内互斥；manifest/新模型输入通过 0600 临时文件与原子 rename 发布，进程中断不会截断已有清单。
