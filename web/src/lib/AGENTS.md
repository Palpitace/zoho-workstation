一旦我所属的文件夹有所变化请更新我。

负责浏览器 API 类型、请求及页面组件。
由 App 传入查询结果和回调，复用 components/ui 基础组件。
不保存业务事实、不读取凭据，不访问数据库或第三方服务。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录说明 | API 和页面组件边界 |
| api.ts | 数据契约 | Go 类型、查询/受保护操作、AI 状态及显示格式 |
| TicketList.svelte | 列表页面 | 筛选、同步标记、Jev 建议和双 AI 状态 |
| TicketDetail.svelte | 详情页面 | 工单字段、会话、消息状态、部门停止原因及附件元数据 |
| JevAssessment.svelte | 评估展示 | Jev 枚举、失败、保留成功与过期状态 |
| BubSummary.svelte | 总结展示 | 事实总结、执行状态、独立失败及消息引用定位 |
| RunStatus.svelte | 运行概览 | 部门、水位、自动任务、分析数量及调用预算 |
| utils.ts | 生成工具 | shadcn 官方 cn 及辅助类型 |
| components | 基础组件 | CLI 生成的最小 shadcn 组件集合 |

所有工单正文使用 Svelte 文本插值，禁止 {@html}。附件只显示名称、类型及大小，不使用其 URL 下载或预览。API 报错不回显原始响应；POST 仅展示服务约定的安全 error 字段。

source=comment 不自动等于内部：结合 visibility 标记备注。mixed_authors=true 显示混合发言者，不把整段聊天归为单一客户。消息 DRAFT/FAILED/PENDING 和 is_description_thread 保留可见标记。角色未知不按方向猜测。

Jev 只展示枚举及来源，不展示未校准的 confidence/probabilities；缺字段和 unknown 显示未知，no_explicit_clue 不等于低风险。当前情绪 unknown 或缺失时，情绪强度显示“待确认”，避免低强度暗示已知情绪；仅作展示，不改变模型原始结果。

Jev/Bub 分别展示 current、success 和 stale。当前失败保留已有成功结果，旧输入或旧配置结果明确标记过期；无成功结果显示待处理或失败，不生成假数据。Bub 根据 execution_status 区分建议、客户确认已执行、未知，根据 result_status 区分客户报告与结果未知；下一步 recommendation/hypothesis 分别标为模型建议/待验证假设。

current.status=invalid 是本地输入无法处理，显示“无法评估/总结”和具体错误，不显示为待处理，也不假称已发模型请求；仍保留已有成功结果。Record.ai.status 已由后端将 invalid 统一为 failed。

Bub 每条事实、措施和建议保留 message_ids 引用。引用通过当前会话精确匹配唯一消息，点击后原地滚动并聚焦消息，不改变 hash 路由或重新请求数据。旧结果引用缺失、ID 重复时显示不能定位原因，不随意跳到第一条。消息正文仍使用文本插值。独立分析读取失败不影响工单正文。

评估和总结各提供一个重跑入口，操作回调由 App 处理；组件只显示受理、错误或禁用原因。列表使用后端内嵌 AI 状态，不增加 N+1 查询；priority/emotion 缺失或未知显示未知，automatic_eligible=false 的 pending 标明未纳入自动分析，不暗示马上会处理。运行页显示后端持久化预算与各能力数量，前端不维护第二份额度。

sync_stopped 优先显示“已停止同步”，详情明确已确认移出配置部门、仅保留历史快照，不当成临时失败或当前事实。无历史快照也显示停止原因。普通 sync_error 显示等待重试及至少 5 分钟间隔；快照不完整单独提示。
