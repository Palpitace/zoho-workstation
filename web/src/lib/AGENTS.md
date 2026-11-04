一旦我所属的文件夹有所变化请更新我。

负责浏览器 API 类型、请求及页面组件。
由 App 传入查询结果和回调，复用 components/ui 基础组件。
不保存业务事实、不读取凭据，不访问数据库或第三方服务。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录说明 | API 和页面组件边界 |
| api.ts | 数据契约 | Go API 类型、只读请求和显示格式 |
| TicketList.svelte | 列表页面 | 搜索、状态筛选、同步标记和详情导航 |
| TicketDetail.svelte | 详情页面 | 工单字段、会话、消息状态及附件元数据 |
| JevAssessment.svelte | 评估展示 | Jev 枚举、失败、保留成功与过期状态 |
| BubSummary.svelte | 总结展示 | 事实总结、执行状态、独立失败及消息引用定位 |
| RunStatus.svelte | 运行概览 | 部门、同步进度、水位及能力开关 |
| utils.ts | 生成工具 | shadcn 官方 cn 及辅助类型 |
| components | 基础组件 | CLI 生成的最小 shadcn 组件集合 |

所有工单正文使用 Svelte 文本插值，禁止 {@html}。附件只显示名称、类型及大小，不使用其 URL 下载或预览。API 报错不回显原始服务响应。

source=comment 不自动等于内部：结合 visibility 标记备注。mixed_authors=true 显示混合发言者，不把整段聊天归为单一客户。消息 DRAFT/FAILED/PENDING 和 is_description_thread 保留可见标记。角色未知不按方向猜测。

Jev 只展示枚举及来源，不展示未校准的 confidence/probabilities；缺字段和 unknown 显示未知，no_explicit_clue 不等于低风险。当前情绪 unknown 或缺失时，情绪强度显示“待确认”，避免低强度暗示已知情绪；仅作展示，不改变模型原始结果。

Jev/Bub 分别展示 current、success 和 stale。当前失败保留已有成功结果，旧输入或旧配置结果明确标记过期；无成功结果显示待处理或失败，不生成假数据。Bub 根据 execution_status 区分建议、客户确认已执行、未知，根据 result_status 区分客户报告与结果未知；下一步 recommendation/hypothesis 分别标为模型建议/待验证假设。

Bub 每条事实、措施和建议保留 message_ids 引用。引用通过当前会话精确匹配唯一消息，点击后原地滚动并聚焦消息，不改变 hash 路由或重新请求数据。旧结果引用缺失、ID 重复时显示不能定位原因，不随意跳到第一条。消息正文仍使用文本插值。独立分析读取失败不影响工单正文，未提供重跑端点前不创建重跑按钮。
