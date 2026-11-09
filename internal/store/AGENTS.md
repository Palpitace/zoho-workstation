一旦我所属的文件夹有所变化请更新我。

负责 Go 独占的 SQLite 访问、工单/知识版本/发现游标/调用计数及同库操作锁。
保存原始证据、规范化快照与分析状态，向编排及查询 API 提供数据。
不发网络请求、不创建通知或通用任务队列。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 数据职责与索引 |
| recent.go | 滚动范围 | 原子替换最新50张成员，历史保留 |
| store.go | 存储边界 | 冻结样本、幂等快照、独立失败/停止状态、同步水位及操作锁 |
| store_test.go | 集成测试 | 重启/迁移保留快照和预算、重复保存及部门边界 |
| lock_unix.go | 平台锁 | Linux/macOS 非阻塞 Flock |
| lock_windows.go | 平台锁 | Windows 非阻塞 LockFileEx |
| knowledge.go | 知识存储 | 幂等案例、SOP 草稿/发布/停用、不可变来源与检索 |
| knowledge_test.go | 知识集成验证 | 发布隔离、版本竞争、重启、来源有效性及机型边界 |
| analysis.go | 分析记录 | 输入版本、本地校验失败、成功结果与调用计数 |
| retry_test.go | 持续重试验证 | 持久冷却、无累计上限、重启和输入隔离 |
| analysis_test.go | AI 存储测试 | 版本隔离、invalid 不计费、恢复、不限额计数及操作锁 |

ai_runs 每次网络尝试先持久记录再发送，按工单、能力、输入 Hash、Schema 和模型读取当前结果；旧输入完成不会覆盖新输入。失败保留已有成功行，结果与当前输入不符时标记过期。
RecordAnalysisFailure 仅用于发送前的本地校验失败，调用方只传不含正文或凭据的安全错误；它按同一工单/能力/Hash/Schema/模型最多追加一条 `invalid`，同时记录起止时间，保留旧成功结果。
`invalid` 不属于模型请求，不计入请求次数，也不参与失败重试次数；StartAnalysis 非 force 时跳过同版本 invalid。force 仍不能覆盖正在运行的同输入，调用方必须先完成本地校验，不能用 force 强行发送无效输入。
AnalysisBudget 只统计 `status!='invalid'` 的真实或保守预留尝试，原有记录计数不变，无需数据库迁移。用户已关闭单轮及累计额度；StartAnalysis 不查累计次数或抛额度错误。同一输入仍最多两次自动失败尝试，成功/invalid 缓存和 running 互斥不变；force 可手动重跑，不能绕过活动请求互斥。
中断的 running 请求保守计入调用次数。调用方必须在取得 analysis 锁后恢复 running，并持锁完成分析；单独调用旧 API 不会隐式获取操作锁。

Store.Lock 仅允许 `sync`、`analysis`，使用 Open 解析过符号链接的绝对数据库路径创建 `<数据库>.<操作>.lock`。Linux/macOS 使用非阻塞 Flock，Windows 使用 LockFileEx 拒绝同库同类并行写者，忙时返回 ErrBusy，其他失败只返回安全泛化说明。
调用方持有返回的 release 到整个操作结束；release 关闭锁文件或进程退出时内核释放文件锁，Store.Close 本身不代替 release。不使用租约表或后台任务；不同操作的锁独立。锁文件保留以免删除文件导致不同 inode 绕过排他性，并随数据库放在忽略的运行数据目录中。

Get 单次查询同时读取原始/规范化快照，避免并发同步时混用两份版本来脱敏；Record.Raw 明确 json:"-"，不会进入前端。详情同时返回 sync_error/checked_at/sync_stopped，列表与详情使用相同同步状态。
已保留但未保存快照的 ID 返回 pending=true，不编造正文；分析编排必须拒绝 pending 输入。样本外 ID 仍返回 sql.ErrNoRows。

Reserve 在事务中幂等追加工单ID，不限累计数量；旧 frozen 字段仅为旧数据库兼容保留，已不再读写该字段决定业务范围。原始与规范化 JSON 分列原子保存。sync_state.batch_upper 固定可恢复批次时间，tickets.checked_at 保存最近尝试时间；Failure 保留旧快照，syncer 据此独立冷却重试。

仅成功确认工单已合并、删除或移出部门才调用 StopSync(ctx,id,reason)：sync_stopped=true、固定安全说明和 checked_at 原子写入，保留快照与 AI 历史，Save 拒绝覆盖已停止记录。Get/List 同时返回停止状态；State.Failed 只统计可重试错误，停止原因仍在列表/详情可见。

Finish 的 watermark 非空表示候选已全部处理（允许存在独立持久失败），更新水位并清空批次；空值只更新错误，不推进或清空窗口。全局错误/取消由调用方传空值。重试义务不依赖水位。老数据库仅非破坏性增加 batch_upper、sync_stopped 默认值列，不改已有快照、AI 记录或预算。

合并原因使用固定 ticket confirmed merged in Zoho，删除原因使用固定 ticket confirmed deleted in Zoho，部门变化使用固定安全原因；都复用 sync_error/sync_stopped，未增表或迁移。已停止项不自动恢复读取；Zoho 恢复已删除工单后的重新启用不在本次实现中。

Bub→Jev 调度仅使用当前成功结果；已移除无业务消费者的 HasFinishedAttempt 派生字段，不新增持久化结构。

SQLite file URI 使用正斜杠与 Windows 盘符前缀斜杠，实际锁文件仍使用规范化原生路径，支持解压目录中的空格。

Analysis 在原有历史扫描中计算当前签名的失败次数与最近失败时间，不额外查表。retry_exhausted 只对最新 failed 且失败累计至少两次返回 true；最新 running/success 或输入/Schema/模型改变均不显示旧耗尽。last_failed_at 保留便于追溯，不修改历史，不引入部署版本或自动重置重试计数。

## 当前自动重试规则（替代上文累计两次后停止的旧规则）

用户取消 Jev/Bub 累计失败上限。StartAnalysis 在同签名 running/success/invalid 时仍去重；普通失败按持久 completed_at 和失败次数计算 1、2、4、8、16、30 分钟冷却，之后每 30 分钟继续，重启不清零。Analysis 返回 next_retry_at，不再返回 retry_exhausted；有当前成功缓存的手动失败不重复自动调用。手动 force 可提前重试但不能重叠。retry_test.go 以合成数据库时间前移验证超过两次后继续、退避上限和重启/新输入隔离。

Issue #5：List 移除固定 LIMIT 50，Reserve 移除冻结及累计上限。新增 sync_state.discovery_cycle/start/offset/done 为幂等非破坏性列迁移；SaveDiscovery 原子保存已校验的页ID和分页位置，不删除工单或AI历史。State 公开发现位置/完成标记以及完整有效数量complete、停止数量stopped，ready仍为已保存快照总数。创建日期与部门验证在Zoho层完成，存储仅负责原子性。新增周期重扫、失败不推进以及60张跨重启验证在syncer公开集成测试覆盖，不另重复实现数据库专用测试套件。

## 知识来源与发布

knowledge_entries 通过独立 CREATE TABLE/INDEX 做增量初始化，不修改已有工单或 AI 历史。内容不可就地修改；同类/同工单/同 SourceHash 幂等返回原条目，已停用条目不会因为重复导入复活。案例只从完整且未停止的工单保存，可自动发布，保存新版本后旧案例归档。SourceHash 由 app 对照当前案例输入，存储查询仅校验工单仍完整且未停止。

SOP 导入始终为 draft，只有 PublishKnowledge 显式操作生效。ReplacesID 指定旧版，草稿期间不影响旧版；发布事务原子归档旧版并发布新版，同源兄弟草稿随后发布会返回 ErrKnowledgeConflict，防止旧审阅覆盖已生效修订。ArchiveKnowledge 仅停用不删除，历史及引用仍可查看。

检索先筛选 published 和当前有效案例，再按机型及英文/中文词面匹配。Source 返回稳定条目/块定位和原工单消息 ID；无相关证据时不回填任意条目。每块限 16 KiB、每份资料 512 KiB、256 块，超限显式拒绝不截断，检索进一步限制返回总量。词面检索不保证跨语言召回。数据库仅 Go 读写，上传原文件由 app 保存在忽略的 data/knowledge 下。

简化检查：只新增一张正在使用的知识版本表；不增加向量服务、发布队列、抽象仓储或重复全文副本。版本发布与查询由现有 SQLite 事务及 Go 词面排序完成。

知识派生 SOP 可保留 source_ticket_id/message_ids 以回到原案例证据，人工发布后独立生效。来源签名由 app 包含当前输入/Schema/模型及案例内容；存储不把调用 ID 当成知识版本。知识块最多 4096 个，总文本仍有界。ai_runs 的 kind 允许 case/advice，复用原缓存、重试和重启恢复，所有真实请求继续计入持久计数。

Issue #9：AnalysisView 增加 task_state/attempts，尝试次数按当前输入/Schema/模型计算且不包含本地 invalid；冷却期 retry_waiting，到期 pending，实际 running 才显示执行中，invalid 为 manual。Active 仅内部使用，来自同次历史查询，保留旧输入仍在执行的记录。继续使用 ai_runs，无迁移；安全HTTP错误文本随失败保存，重启保留，旧成功不清空。

滚动测试版：EnableRecentScope 启用当前 Store 的范围过滤，IDs 只查询已确认成员；首次远端未确认时 List/State 展示按创建时间排序的本地最新50张缓存，OutsideScope=true，不参与同步或AI。ReplaceRecent 在事务中替换完整集合并 Reserve 新ID，失败不改变旧集合；Get 保留历史读取并标记 OutsideScope。新增 recent_confirmed 列区分未确认和已确认空集合，旧非空成员自动迁移为已确认；不删除工单/AI/知识历史，不复用表示已合并删除的 sync_stopped。重启后成员集合保留，配置0可重新查询全部历史。
