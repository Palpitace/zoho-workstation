一旦我所属的文件夹有所变化请更新我。

负责单 Go 服务的同步/独立分析生命周期、开发采样及 HTTP 管理边界。
组合 config、store、syncer、analysis，向同源 Svelte 返回工单和状态。
不实现模型 SDK、不扩部门、不引入持久任务队列或通知。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 编排边界与命令契约 |
| knowledge.go | 知识编排 | 当前案例签名、有效来源检索、独立建议输入与自动案例保存 |
| knowledge_http.go | 知识 API | 本地文档导入、草稿修订、确认发布/停用和建议状态 |
| knowledge_test.go | 知识集成回归 | 自动任务、证据版本、Jev 隔离、草稿与文档导入 |
| analysis.go | 分析输入与开发入口 | 固定数据集、Bub 总结到 Jev 输入、实际输入版本及有限分析命令 |
| analysis_test.go | 恢复测试 | 互斥分析避免误恢复活动请求 |
| app.go | 生命周期 | 同步批次/退避、全部有效工单分析及完成唤醒 |
| recovery.go | 显式恢复 | 校验用户确认的运行 ID，向原调度器提供一次恢复机会 |
| display_messages.go | 展示派生 | 从保留 HTML/正文识别邮件引用分段，不改变证据和 Hash |
| display_messages_test.go | 引用验证 | 多段/嵌套、多语言文本、歧义回退及文本安全 |
| http_recovery_test.go | HTTP 回归 | invalid 排除恢复候选、引用分段仅供详情 |
| http.go | 同源 API | 工单号/链接、组合筛选、分类/独立分析状态及受保护操作 |
| list.go | 列表查询 | 字段/日期/表头筛选、来信时间与稳定计数 |
| list_test.go | 列表回归 | 旧快照真实身份、筛选选项稳定、分类和独立过期状态 |
| sync_test.go | 同步调度回归 | 首轮接续、失败/429 等待及关闭取消 |
| app_test.go | 小型集成验证 | 自动失败重试、查询不阻塞、操作防护及重启 |
| reference_test.go | 顺序与版本验证 | Bub 先总结、Jev 输入隔离、失败等待、替换总结/会话过期及重启 |

Start 必须在 HTTP 监听成功后调用，先持有同库 sync/analysis 进程锁再恢复 running。服务最多一个同步、一个 Jev、一个 Bub 同时工作。启动立即调度；一个同步协调者默认每批最多 9 张、最多 3 张工单并发 Fetch（SYNC_BATCH_SIZE/SYNC_CONCURRENCY），保存串行，有剩余候选则等待 1 秒继续，不再等待 SYNC_INTERVAL。失败批次退避从 5 秒倍增到 2 分钟；429 还需等待至少 Retry-After。全部候选处理后由 SYNC_INTERVAL 触发常规增量轮询。等待期间保持同步互斥且支持关闭取消；每批有 5 分钟 context 上限，日志只记录耗时和成功/失败/停止/剩余数量。同版本最多一次自动失败重试。没有外部队列，待分析输入由已保存快照与历史结果计算，重启后重新计算即可恢复。

同工单先 Bub 后 Jev；Bub 保留完整会话，不接收 Jev 参考。Jev 只有在当前会话/Schema/模型对应的成功总结存在时才调用；没有总结或只有过期总结时返回等待，不发送原始会话，不生成 invalid 记录或扣请求计数。已保存的当前成功总结可在 Bub 关闭或最近重试失败时继续使用。手动 Jev 缺当前总结返回 409；手动 Bub 完成后由原完成唤醒机制接续 Jev，不新建任务表或第二套调度器。

CLI、自动调用及 HTTP 状态共用 requestForRecord，避免调用和过期判断使用不同 Hash。Bub v6 输入为完整脱敏会话加 conversation_hash；Jev v2 输入只有 summary 与 ticket_id、conversation_hash、summary_input_hash、总结 Schema/请求和返回模型，无消息原文。输入 Hash 不包含 Bub 运行 ID；同内容同来源配置不重复评估，变化使旧评估过期。prepare 仍保存完整会话及固定研发清单，analyze 先 bub 后 jev；CLI 不隐式跨能力计费。

API 每能力 AnalysisView 可含 waiting_for=bub，列表 AIStatus.status=waiting；仅表示缺少当前总结，仍保留成功历史和 stale 标记。waiting 计入待处理数量。

用户已授权业务自动分析全部已保存、完整且未停止的工单。服务不再读取/创建研发 manifest，不需要满 20 张才启动；CLI prepare 的开发/留出集合只用于研发对比。调用输入按 Hash 在 data/review 私有保存；本地输入校验失败记录 invalid、不计入请求次数。Python 环境缺失返回 503 并显示运行错误，不将输入永久标为 invalid；环境修复后可自动恢复。累计请求额度已关闭，自动任务不因历史调用数停止，不重置或自动切换统计轮次。

服务默认监听 loopback，容器转发时入口可按 APP_LISTEN_HOST 监听容器接口；API 仍校验本机 Host，POST 还需 GET /api/status 提供的临时 X-CSRF-Token 及允许的 Origin。Windows 经宿主回环转发访问 localhost:8080，不开放任意 IP/域名的 Host 或 Origin。开发 Vite 仅允许 localhost/127.0.0.1 的 5173 端口。无公网或生产部署授权。GET 状态返回进度/调用计数/能力数量；POST /api/sync 与 /api/tickets/:id/analysis/:kind 返回已受理，不宣称已完成。

自动分析每次从 SQLite 的完整有效快照选择候选，固定采样仍由 store 限制最多 50，不因分析范围变化扩大采集。CLI PrepareInputs/AnalyzeTickets 与服务共用输入和结果协议。网络尝试前持久记录次数；模型关闭不要求其配置。
CLI prepare 同样持有 analysis 锁；manifest/新模型输入通过 0600 临时文件与原子 rename 发布，进程中断不会截断已有清单。

GET /api/status 的 budget 保留 total/round 统计字段，移除 total_limit/round_limit；不再返回 budget_exhausted 或本地额度 HTTP 429。recordViews 不查询额度，Jev 是否等待仅根据当前成功总结判断。真实上游限流、模型开关、超时、有限重试和同输入互斥仍有效。

BubSchema 已升级为 bub-summary-v6，JevSchema 为 jev-assessment-v2；既有 Bub v1/v2/v3/v4/v5 与 Jev v1 成功结果按版本标为过期，仍保留历史；新自动/手动运行使用新结构，不把旧结果静默改写成新分类，Jev 在当前总结成功后更新。前端隐藏模型来源不影响数据库元信息与输入版本追踪。

状态 API runtime 返回 sync_batch_size/sync_concurrency；配置缺省为 9/3，上限 50/5，运行状态页展示实际值。同步批次互斥、AI 并发与固定样本集合不变。Bub v4 保留三段 JSON 形状，新增未知作者入站原文摘录约束；旧 v3 可继续展示但标记过期，v1/v2 保留旧结构提示。

## 全部有效工单自动分析与列表（2026-11-05）

App 移除 loadSelection/eligible 缓存及 selectionMu，直接从 Store.List 选非 Pending、非 SyncStopped、Complete 的工单。状态接口使用同一判断，runtime.ai_scope=all_synced、ai_scope_count 为有效数量。每个能力仍最多一个活动请求；完成后用容量 1 的 analysisWake 唤醒原协调循环，立即接续下一张，不依赖下一次 Zoho 轮询，不新建任务队列。Bub 独立运行，Jev 等待当前成功总结，已有成功复用、同版本失败最多 2 次、关闭取消和持久恢复不变。此前未纳入研发集的有效快照在部署重启后也会开始分析，真实模型费用按现有配置产生；本次开发仅 Mock。

GET /api/tickets 支持 q/status/product 组合，product=__missing__ 选择缺产品；返回 filters.statuses/products 为全部本地样本的去重排序选项，不被当前筛选收窄。既有 Store.List 的同步/CLI 消费者不改。列表仍最多当前固定 50 张，无部门外查询。AIStatus 新增 category，复用现有 Jev answers.category，仅展示当前未过期成功结果；无需改 Schema 或额外请求。

列表/详情通过 displayTicket 从已保存 raw.Ticket 提取 ticketNumber（数字/数字字符串）与 webUrl，输出 ticket_number/web_url；内部 id 仍用于查询/引用。只接受无用户凭据的 HTTP(S) URL，缺失不猜工单号/路径。旧快照无需数据库迁移或重新同步；身份字段不进模型输入/Hash。

验证边界为现有 App Start/HTTP、Store 的公开接口和外部 HTTP/Python 入口 Mock。缺 manifest/holdout 的自动分析在旧实现观察失败后修复；一小时同步间隔下连续处理两票，排除停止/不完整，失败等待、总结替换/新会话过期与重启复用通过。隔离移除完成唤醒时第二票停滞测试失败，因此保留唤醒，不另加轮询线程。

Python 环境预检和实际执行共用 analysis.PythonExecutable，支持便携包和 Windows venv；后台模型完成/失败写结构化日志（能力、工单 ID、安全错误），不记录输入或输出正文。CLI 互斥回归改用 Store.Lock 公共入口，避免测试依赖 Unix syscall。

Issue #2：每轮自动分析对启用的 Bub 做一次 CheckPython 离线预检，失败写运行错误并跳过该能力，不进入 StartAnalysis、不增加每票失败次数；下轮重检，可恢复。手动 Bub 和研发 CLI 同样先检查，HTTP 返回 503 及安全原因码。同步和已有结果查看不受影响，不增加持久状态或重试表。历史 summary_failed 不清空，已用尽两次自动尝试的旧输入由用户点击重新总结，或使用下述显式批量恢复。公共 App/Store 边界验证环境失败零请求、手动重复失败不计次、环境恢复可继续。

## 组合搜索与五列表头筛选

GET /api/tickets 的 field 支持 all/subject/ticket_number/id/product；q 默认跨字段匹配，标题/产品忽略大小写包含匹配，号码/ID 去除前导 # 后精确匹配。日期字段 date_field 为 first_incoming/created/updated；from 包含起点、until 不包含终点，均为 RFC3339，非法字段/日期/反向范围返回 400。浏览器把当地日期的次日零点作为 until，可正确覆盖结束当天和夏令时。
first_incoming_time 仅从完整快照最早的公开入站 thread 计算，排除 agent 以及 DRAFT/FAILED/PENDING，不修改原作者角色；无法确认时为空，不用创建时间替代。展示字段不改变模型输入 Hash。
status/product/emotion/priority/sync_status 五列条件与搜索/日期取交集；未知 AI 字段归 unknown，缺产品/状态为 __missing__。sync_status 与展示共用同一派生，区分 synced/pending/failed/incomplete/merged/deleted/stopped。filters 返回同名数组 [{value,count}]，计数基于历史开关限定的本地集合，不随当前查询缩窄，列表上限仍为原固定样本；不扩采样，不改 Store.List 的同步消费者。
show_history=false 时服务端排除 sync_stopped；前端始终明确传值，无参数的既有接口调用仍包含历史。列表组件不再重复过滤，清除条件恢复隐藏历史。

## Issue #3：历史失败与受控恢复

AnalysisView / AIStatus 返回 retry_exhausted、last_failed_at；仅当前签名最新运行失败且累计 failed≥2 才标耗尽，成功或新输入不继承该标志。HTTP 用 runtime.started_at 区分 failure_before_start；历史失败不冒充本次运行失败。Jev waiting_reason 指明 Bub 失败/耗尽/关闭，仍不发送过期总结。
GET /api/status 返回 retry_candidates（按能力、有效工单的 ticket_id/run_id）及耗尽数量，runtime.recovery_pending 表示未开始的已确认项。POST /api/analysis/:kind/retry 必须同源/CSRF，JSON candidates 限 1–50 项，全部校验当前有效快照/输入版本/失败运行 ID 后原子接受；失效确认返回 409，不扩大候选集合。Bub 先离线预检，失败返回 503、不增加次数。
确认只进入 App 内存映射，由既有 scheduleAnalysis/startAnalysis 和 analysisWake 消费；每能力仍一个请求，不另建队列/表/调度器。实际开始前再次验证运行 ID/输入，改变、停止或无效的候选撤销授权，正常新输入分析仍遵守原规则。每个确认项只 force 一次，再失败仍受历史两次失败限制，不自动循环；复用当前成功、保留全部尝试历史。重启丢弃未开始的确认项，用户重新确认；已有 running 仍按原中断恢复规则处理。单票手动重跑保持可用。
reference_test 通过公共 HTTP/App/Store 与合成 Python 进程验证：历史两次失败→重启显示耗尽→本地环境失败零计数→确认补一次、重复/旧确认拒绝→再次失败不循环→修复后重确认成功并接续 Jev→重启不重复成功。

Issue #7：ticketView.message_segments 以 source:id 为键，仅详情展示消费，原 Message.body/RawSnapshot/持久化和 AI Hash 不变，老数据库无需迁移。HTML 优先识别 blockquote、gmail_quote/gmail_quote_container、divRplyFwdMsg、zmail_extra、yahoo_quoted；复用 ticket.Normalize 转纯文本后在已有正文中精确匹配，重复歧义或不一致保持原样，嵌套结构保留。原始 HTML、脚本、事件属性和链接均不返回/执行。纯文本只折叠明确 > 前缀（支持嵌套）；多语言写道和转发头不独自证明余下均属引用，无法确认的无前缀内容保持可见。分段文本拼接与保存 body 完全一致。

## 当前执行规则：Issue #4–#7（替代固定样本/耗尽规则）

服务传入 Config.SyncStartTime，默认 2026-11-05T00:00:00+08:00，按创建时间持续发现同部门工单；不再冻结 50 个 ID，已有数据保留。CLI sample 的显式单次采样仍可用。runtime 返回起点、下次定时调度、最近批次保存/失败/停止/剩余数与停止总数；State 包含持久发现分页进度。
自动分析独立每 30 秒检查到期任务，并复用完成唤醒；移除累计两次失败的永久停止，按 Store.next_retry_at 退避，保留单能力并发、超时、缓存、invalid 输入拒绝及历史。人工批量入口改为“立即重试失败的”，确认固定运行 ID，可提前一次尝试；再次失败回到退避，不需要人工恢复才能自动继续，不受 50 个候选限制（HTTP 体积有界）。
AIStatus/AnalysisView 返回 next_retry_at，统计 retry_waiting；不再返回 retry_exhausted。Jev 无当前 Bub 成功总结仍等待，失败原因不伪装为未知低风险。reference_test 用人工提前尝试生成第二次历史失败，不等待真实冷却时间。

## 知识与内部建议（2026-11-09）

用户确认历史案例自动积累、SOP 人工确认发布、内部建议自动生成。本增量不扩大同步起点或部门，使用已同步的完整有效会话；`bub` 事实总结仍先于 Jev。新增 `case` 和 `advice` 使用现有 ai_runs 与持续退避，不增加任务队列。Bub 总结/案例/建议共享一个并发名额并轮流选择任务；Jev 保留自己的名额。每轮一次 Python 环境预检，缺少知识入口不影响既有总结/评估。
案例当前成功结果自动保存为可参考条目；签名覆盖工单/总结输入、Schema/模型及规范化案例内容。同输入重提取有语义内容变化也更新知识；相同内容不因运行 ID/usage 重建。迟到的旧输入不发布。数据库恢复后从成功记录补齐知识，归档原件不删除。人工停用的相同案例不会自动复活。
建议检索当前发布的 SOP 和源工单当前成功案例，排除当前工单本身、失效/不完整/停止来源及不相容机型。产品缺失时可采用当前案例中经字面证据校验的型号；仍未知时只选通用 SOP。按词面匹配而非语义向量检索，最多五块、每块有固定来源位置；无来源则等待，不计模型请求。只有被选来源变化才令建议失效；不会把跨票经验送给 Jev。部分匹配不保证适用性，模型须核对原文前提。
GET `/api/knowledge?q=&status=`、`/api/knowledge/:id`；POST `/api/knowledge/import` 上传有界 multipart 文件（file/title/product/replaces_id），只解析保存草稿；POST `/api/knowledge/draft` 创建不可变修订（title/product/text/source_entry_id/replaces_id）；POST `/:id/publish` 明确发布、`/:id/archive` 停用。导入最多一个解析进程；原件在忽略的 data/knowledge/，解析正文与标题/产品经现有脱敏器。相同文件去重；相同内容更改导入元数据显式 409，指导通过修订草稿操作，避免默默丢弃修改。
GET `/api/tickets/:id/knowledge` 独立返回 case/advice 状态和来源快照，POST 原分析路径的 `/advice` 可重跑。所有操作复用本机 Host/Origin/CSRF。SOP 草稿不影响当前建议，发布替代/停用后相应建议立即标过期，随后自动更新；事实总结、Jev 和同步不受知识失败影响。知识正文仅作数据，不增加运行时工具或自主回复。

知识列表 API 只返回条目元信息，blocks/message_ids 置为空数组，详情端点按需读取完整证据，避免每次轮询发送全部文档正文；存储/检索内容保持不变。

## 离线 FAQ 实验（2026-11-09）

| lab.go | 批次执行 | 共享 Bub 槽、先保存调用、有限流程与暂停恢复 |
| lab_http.go | 人工操作 | 选择/创建批次、启动暂停、评分和修订草稿 |
| lab_test.go | 集成验证 | 真实 Python 替身进程与 SQLite 的模拟到审核链路 |

GET /api/lab/candidates?from=&until= 只读取授权起点以后最近最多50张本地完整有效工单；缺少可靠历史节点时解释原因。POST /api/lab/batches 接受 ticket_ids/knowledge_ids/groups/from/until 或 parent_id；新批固定完整脱敏快照、知识、分组及模型签名。已暂停或完成批次可明确创建下一轮，开发反馈含被评回复，留出不学习。创建不调用模型。GET /api/lab/batches 和 /:id 查询，详情 revision 相同时仅返回 unchanged，避免反复传完整会话。

POST /:id/start、/:id/pause、/:id/feedback、/:id/drafts 复用 Host/Origin/CSRF 边界。最多一个运行批次，与总结/案例/建议轮转共享名额。每个步骤先写 running，调用失败保存并最多重试一次；无活动 worker 的 running 视为结果不确定，暂停后人工恢复。停机暂停，不擅自重发不确定调用。

评分与模型完成分别在 SQLite 事务读取最新状态后追加，不覆盖并发反馈。只有全部留出原版/候选评估成功返回，且候选未被新修订取代，才能晋升草稿；明确发布仍检查原批验证完整和当前正式前驱。模型评分不作真值。技术候选继承所有学习/开发与固定知识来源工单，原文不写日志。已有事实总结→Jev、同步和工单查看不依赖离线实验成功。

| knowledge_embedding.go | 检索编排 | 增量向量索引、查询缓存、混合召回与降级状态 |
| knowledge_import.go | 批量解析 | ZIP/多文档、原件保留、素材下载和审核预览 |
| knowledge_library.go | 知识管理 API | 具名库/分类、批量确认重试、图片和 Markdown 导出 |
| knowledge_library_test.go | 小型集成验证 | 分类去重、风险确认、图片重试、部门与降级 |

知识管理API新增 libraries/categories、import-batch预览/确认/重试、assets上传读取、export与embedding/retrieve。原件、素材和批次清单放私有data/knowledge，图像下载只允许校验后的公网HTTPS直连目标，重定向同样检查；不爬取链接站点。批次保存草稿不要求风险确认，正式发布需确认且素材完整。批次锁独立于全局调度锁。后台索引仅发布版本，每5秒增量建立文档与当前成功Bub查询向量，状态读取不启动推理。管理页可查看所有自有库，检索及实验创建必须明确部门；新实验/后续批次不能绕过当前部门绑定，历史快照不改写。

批量导入未知资料默认reference，清单明确scope可保留；引用式图片规范化，无法支持的图片语法/缺图阻止发布。已保存批次草稿修改时关联原EntryID，内容不变复用版本。批次确认不覆盖用户已有正式知识。
