一旦我所属的文件夹有所变化请更新我。

负责 Go 独占的 SQLite 访问、样本/调用计数及同库操作锁。
保存原始证据、规范化快照与分析状态，向编排及查询 API 提供数据。
不发网络请求、不创建通知或通用任务队列。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 数据职责与索引 |
| store.go | 存储边界 | 冻结样本、幂等快照、独立失败/停止状态、同步水位及操作锁 |
| store_test.go | 集成测试 | 重启/迁移保留快照和预算、重复保存及部门边界 |
| analysis.go | 分析记录 | 输入版本、本地校验失败、成功结果与调用计数 |
| analysis_test.go | AI 存储测试 | 版本隔离、invalid 不计费、恢复、不限额计数及操作锁 |

ai_runs 每次网络尝试先持久记录再发送，按工单、能力、输入 Hash、Schema 和模型读取当前结果；旧输入完成不会覆盖新输入。失败保留已有成功行，结果与当前输入不符时标记过期。
AnalysisView.HasFinishedAttempt 由同输入/Schema/模型的非 running 历史派生，不保存新字段、不进入 API JSON；供 Bub 判断首次 Jev 尝试是否已经终结，后续重试不能再次阻止总结。旧会话历史不算当前尝试。
RecordAnalysisFailure 仅用于发送前的本地校验失败，调用方只传不含正文或凭据的安全错误；它按同一工单/能力/Hash/Schema/模型最多追加一条 `invalid`，同时记录起止时间，保留旧成功结果。
`invalid` 不属于模型请求，不计入请求次数，也不参与失败重试次数；StartAnalysis 非 force 时跳过同版本 invalid。force 仍不能覆盖正在运行的同输入，调用方必须先完成本地校验，不能用 force 强行发送无效输入。
AnalysisBudget 只统计 `status!='invalid'` 的真实或保守预留尝试，原有记录计数不变，无需数据库迁移。用户已关闭单轮及累计额度；StartAnalysis 不查累计次数或抛额度错误。同一输入仍最多两次自动失败尝试，成功/invalid 缓存和 running 互斥不变；force 可手动重跑，不能绕过活动请求互斥。
中断的 running 请求保守计入调用次数。调用方必须在取得 analysis 锁后恢复 running，并持锁完成分析；单独调用旧 API 不会隐式获取操作锁。

Store.Lock 仅允许 `sync`、`analysis`，使用 Open 解析过符号链接的绝对数据库路径创建 `<数据库>.<操作>.lock`。当前目标为 Linux，以非阻塞 Flock 拒绝同库同类并行写者，忙时返回 ErrBusy，其他失败只返回安全泛化说明。
调用方持有返回的 release 到整个操作结束；release 关闭锁文件或进程退出时内核释放文件锁，Store.Close 本身不代替 release。不使用租约表或后台任务；不同操作的锁独立。锁文件保留以免删除文件导致不同 inode 绕过排他性，并随数据库放在忽略的运行数据目录中。

Get 单次查询同时读取原始/规范化快照，避免并发同步时混用两份版本来脱敏；Record.Raw 明确 json:"-"，不会进入前端。详情同时返回 sync_error/checked_at/sync_stopped，列表与详情使用相同同步状态。
已保留但未保存快照的 ID 返回 pending=true，不编造正文；分析编排必须拒绝 pending 输入。样本外 ID 仍返回 sql.ErrNoRows。

初次 Reserve 在事务中固定最多 50 个唯一 ID；失败、停止和重启不释放名额。原始与规范化 JSON 分列原子保存。sync_state.batch_upper 固定可恢复批次时间，tickets.checked_at 保存最近尝试时间；Failure 保留旧快照，syncer 据此独立冷却重试。

仅成功确认匹配 ID 的工单移出部门才调用 StopSync：sync_stopped=true、固定安全说明和 checked_at 原子写入，保留快照与 AI 历史，Save 拒绝覆盖已停止记录。Get/List 同时返回停止状态；State.Failed 只统计可重试错误，停止原因仍在列表/详情可见。

Finish 的 watermark 非空表示候选已全部处理（允许存在独立持久失败），更新水位并清空批次；空值只更新错误，不推进或清空窗口。全局错误/取消由调用方传空值。重试义务不依赖水位。老数据库仅非破坏性增加 batch_upper、sync_stopped 默认值列，不改已有快照、AI 记录或预算。
