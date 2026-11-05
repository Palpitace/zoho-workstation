一旦我所属的文件夹有所变化请更新我。

负责单 Go 服务的同步/独立分析生命周期、开发采样及 HTTP 管理边界。
组合 config、store、syncer、analysis，向同源 Svelte 返回工单和状态。
不实现模型 SDK、不扩部门、不引入持久任务队列或通知。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 编排边界与命令契约 |
| analysis.go | 分析输入与开发入口 | 固定数据集、同版 Jev 参考、实际输入版本及有限分析命令 |
| analysis_test.go | 恢复测试 | 互斥分析避免误恢复活动请求 |
| app.go | 生命周期 | 同步批次/退避、全部有效工单分析及完成唤醒 |
| http.go | 同源 API | 工单号/链接、组合筛选、分类/独立分析状态及受保护操作 |
| list_test.go | 列表回归 | 旧快照真实身份、筛选选项稳定、分类和独立过期状态 |
| sync_test.go | 同步调度回归 | 首轮接续、失败/429 等待及关闭取消 |
| app_test.go | 小型集成验证 | 自动失败重试、查询不阻塞、操作防护及重启 |
| reference_test.go | 参考流程验证 | 三段输出、完整会话、Jev 首次尝试等待及失败/过期参考隔离 |

Start 必须在 HTTP 监听成功后调用，先持有同库 sync/analysis 进程锁再恢复 running。服务最多一个同步、一个 Jev、一个 Bub 同时工作。启动立即调度；一个同步协调者默认每批最多 9 张、最多 3 张工单并发 Fetch（SYNC_BATCH_SIZE/SYNC_CONCURRENCY），保存串行，有剩余候选则等待 1 秒继续，不再等待 SYNC_INTERVAL。失败批次退避从 5 秒倍增到 2 分钟；429 还需等待至少 Retry-After。全部候选处理后由 SYNC_INTERVAL 触发常规增量轮询。等待期间保持同步互斥且支持关闭取消；每批有 5 分钟 context 上限，日志只记录耗时和成功/失败/停止/剩余数量。同版本最多一次自动失败重试。没有外部队列，待分析输入由已保存快照与历史结果计算，重启后重新计算即可恢复。

同工单自动流程先尝试 Jev，首次尝试终结即尝试启动 Bub；成功时附带当前会话版本的评估作补充参考，关闭/失败/超时则使用完整会话独立总结。等待有 90 秒调用上限，不阻塞其他工单、同步或查看。匹配历史中已有终结尝试时，Jev 重试不能再次阻挡 Bub；状态从现有结果计算，无新任务表。手动 Bub 只使用已有参考，不隐式调用 Jev；手动 Jev 不立即追加 Bub 计费请求，自动周期会处理参考变化造成的过期。

CLI、自动调用及 HTTP 状态共用 requestForRecord，避免调用和过期判断使用不同 Hash。Jev 使用完整会话 Hash；Bub v5 保留完整会话并添加 conversation_hash、可空 jev_reference，实际输入包含来源运行 ID/版本/模型和评估标签，按内容 Hash 私有保存。只附同版本的成功结果，未知/失败/过期结果不伪装成当前评估。

用户已授权业务自动分析全部已保存、完整且未停止的工单。服务不再读取/创建研发 manifest，不需要满 20 张才启动；CLI prepare 的开发/留出集合只用于研发对比。调用输入按 Hash 在 data/review 私有保存；本地输入校验失败记录 invalid、不计入请求次数。Python 环境缺失返回 503 并显示运行错误，不将输入永久标为 invalid；环境修复后可自动恢复。累计请求额度已关闭，自动任务不因历史调用数停止，不重置或自动切换统计轮次。

服务默认监听 loopback，容器转发时入口可按 APP_LISTEN_HOST 监听容器接口；API 仍校验本机 Host，POST 还需 GET /api/status 提供的临时 X-CSRF-Token 及允许的 Origin。Windows 经宿主回环转发访问 localhost:8080，不开放任意 IP/域名的 Host 或 Origin。开发 Vite 仅允许 localhost/127.0.0.1 的 5173 端口。无公网或生产部署授权。GET 状态返回进度/调用计数/能力数量；POST /api/sync 与 /api/tickets/:id/analysis/:kind 返回已受理，不宣称已完成。

自动分析每次从 SQLite 的完整有效快照选择候选，固定采样仍由 store 限制最多 50，不因分析范围变化扩大采集。CLI PrepareInputs/AnalyzeTickets 与服务共用输入和结果协议。网络尝试前持久记录次数；模型关闭不要求其配置。
CLI prepare 同样持有 analysis 锁；manifest/新模型输入通过 0600 临时文件与原子 rename 发布，进程中断不会截断已有清单。

GET /api/status 的 budget 保留 total/round 统计字段，移除 total_limit/round_limit；不再返回 budget_exhausted 或本地额度 HTTP 429。recordViews 不查询额度，Jev 首次尝试等待只根据已有运行状态判断。真实上游限流、模型开关、超时、有限重试和同输入互斥仍有效。

BubSchema 已升级为 bub-summary-v5，既有 v1/v2/v3/v4 成功结果按版本标为过期，仍保留历史；新自动/手动运行使用新结构，不把旧结果静默改写成新分类，不触发 Jev 无谓重跑。前端隐藏模型来源不影响数据库元信息与输入版本追踪。

状态 API runtime 返回 sync_batch_size/sync_concurrency；配置缺省为 9/3，上限 50/5，运行状态页展示实际值。同步批次互斥、AI 并发与固定样本集合不变。Bub v4 保留三段 JSON 形状，新增未知作者入站原文摘录约束；旧 v3 可继续展示但标记过期，v1/v2 保留旧结构提示。

## 全部有效工单自动分析与列表（2026-11-05）

App 移除 loadSelection/eligible 缓存及 selectionMu，直接从 Store.List 选非 Pending、非 SyncStopped、Complete 的工单。状态接口使用同一判断，runtime.ai_scope=all_synced、ai_scope_count 为有效数量。每个能力仍最多一个活动请求；完成后用容量 1 的 analysisWake 唤醒原协调循环，立即接续下一张，不依赖下一次 Zoho 轮询，不新建任务队列。首次 Jev 失败仍释放 Bub，已有成功复用、同版本失败最多 2 次、关闭取消和持久恢复不变。此前未纳入研发集的有效快照在部署重启后也会开始分析，真实模型费用按现有配置产生；本次开发仅 Mock。

GET /api/tickets 支持 q/status/product 组合，product=__missing__ 选择缺产品；返回 filters.statuses/products 为全部本地样本的去重排序选项，不被当前筛选收窄。既有 Store.List 的同步/CLI 消费者不改。列表仍最多当前固定 50 张，无部门外查询。AIStatus 新增 category，复用现有 Jev answers.category，仅展示当前未过期成功结果；无需改 Schema 或额外请求。

列表/详情通过 displayTicket 从已保存 raw.Ticket 提取 ticketNumber（数字/数字字符串）与 webUrl，输出 ticket_number/web_url；内部 id 仍用于查询/引用。只接受无用户凭据的 HTTP(S) URL，缺失不猜工单号/路径。旧快照无需数据库迁移或重新同步；身份字段不进模型输入/Hash。

验证边界为现有 App Start/HTTP、Store 的公开接口和外部 HTTP/Python 入口 Mock。缺 manifest/holdout 的自动分析在旧实现观察失败后修复；一小时同步间隔下连续处理两票，排除停止/不完整，Jev 失败 Bub 独立与重启复用通过。隔离移除完成唤醒时第二票停滞测试失败，因此保留唤醒，不另加轮询线程。
