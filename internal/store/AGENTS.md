一旦我所属的文件夹有所变化请更新我。

负责 Go 独占的 SQLite 访问、样本/调用额度及同库操作锁。
保存原始证据、规范化快照与分析状态，向编排及查询 API 提供数据。
不发网络请求、不创建通知或通用任务队列。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 数据职责与索引 |
| store.go | 存储边界 | 冻结样本、幂等快照、尝试时间、同步水位及操作锁 |
| store_test.go | 集成测试 | 重启恢复、重复保存、失败不清成功及部门边界 |
| analysis.go | 分析记录 | 输入版本、本地校验失败、成功结果与调用额度 |
| analysis_test.go | AI 存储测试 | 版本隔离、invalid 不计费、恢复、预算及操作锁 |

ai_runs 每次网络尝试先占用预算再发送，按工单、能力、输入 Hash、Schema 和模型读取当前结果；旧输入完成不会覆盖新输入。失败保留已有成功行，结果与当前输入不符时标记过期。
AnalysisView.HasFinishedAttempt 由同输入/Schema/模型的非 running 历史派生，不保存新字段、不进入 API JSON；供 Bub 判断首次 Jev 尝试是否已经终结，后续重试不能再次阻止总结。旧会话历史不算当前尝试。
RecordAnalysisFailure 仅用于发送前的本地校验失败，调用方只传不含正文或凭据的安全错误；它按同一工单/能力/Hash/Schema/模型最多追加一条 `invalid`，同时记录起止时间，保留旧成功结果。
`invalid` 不属于模型请求，不占预算，也不参与失败重试次数；StartAnalysis 非 force 时跳过同版本 invalid。force 仍受预算限制，调用方必须先完成本地校验，不能用 force 强行发送无效输入。
AnalysisBudget 与 StartAnalysis 只统计 `status!='invalid'` 的真实或保守预留尝试，原有记录计数不变，无需数据库迁移。按 round 最多 20 次、总计 60 次，耗尽返回可用 errors.Is 判断的 ErrAnalysisBudget；同一输入默认最多两次真实失败。
中断的 running 请求保守计入额度。调用方必须在取得 analysis 锁后恢复 running，并持锁完成分析；单独调用旧 API 不会隐式获取操作锁。

Store.Lock 仅允许 `sync`、`analysis`，使用 Open 解析过符号链接的绝对数据库路径创建 `<数据库>.<操作>.lock`。当前目标为 Linux，以非阻塞 Flock 拒绝同库同类并行写者，忙时返回 ErrBusy，其他失败只返回安全泛化说明。
调用方持有返回的 release 到整个操作结束；release 关闭锁文件或进程退出时内核释放文件锁，Store.Close 本身不代替 release。不使用租约表或后台任务；不同操作的锁独立。锁文件保留以免删除文件导致不同 inode 绕过排他性，并随数据库放在忽略的运行数据目录中。

Get 单次查询同时读取原始/规范化快照，避免并发同步时混用两份版本来脱敏；Record.Raw 明确 json:"-"，不会进入前端。详情同时返回 sync_error/checked_at，列表与详情使用相同同步状态。
已保留但未保存快照的 ID 返回 pending=true，不编造正文；分析编排必须拒绝 pending 输入。样本外 ID 仍返回 sql.ErrNoRows。

初次 Reserve 在事务中固定最多 50 个唯一 ID；失败和重启不释放名额。原始与规范化 JSON 分列保存，同一 UPDATE 原子提交。sync_state.batch_upper 固定可恢复批次时间，tickets.checked_at 记录最近一次成功或失败尝试的完成时间，供同步按最久未尝试顺序公平调度。Failure 同时保存错误和尝试时间，保留旧快照；checked_at 本身不表示成功，完成判断还必须要求完整快照及无 sync_error。仅完整批次推进水位并清除批次时间。复用已有字段，无需为公平重试迁移数据库。已有采样数据库的批次上界使用非破坏性新增列迁移；数据文件和 WAL 由根 data/ 忽略规则覆盖。
