一旦我所属的文件夹有所变化请更新我。

负责 Svelte 工单列表、详情和运行状态界面及静态构建。
通过同源 Go API 获取数据；Vite 仅用于开发及构建，dist 交给 Go 服务。
不读取数据库、凭据或原始 HTML，不直接调用 Zoho、模型或企业微信。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 前端约定 | 依赖依据、API、验证及界面约定 |
| package.json | 依赖入口 | 固定版本及 pnpm 命令 |
| pnpm-lock.yaml | 锁定文件 | 依赖树及完整性校验 |
| pnpm-workspace.yaml | pnpm 策略 | 锁定新近发布依赖的精确版本例外 |
| components.json | shadcn 配置 | Vega、neutral、Lucide 及别名 |
| tsconfig.json | 类型配置 | Svelte 与 TypeScript 严格检查 |
| vite.config.ts | 构建配置 | 开发代理、别名及 dist 输出 |
| index.html | HTML 入口 | 中文页面及应用容器 |
| src | 自有源代码 | 页面、API 类型和基础组件 |

## 初始化版本依据

2026-11-04 查询官方文档与 npm registry 的稳定 latest 标签：Svelte 5.57.3、Vite 8.3.5、@sveltejs/vite-plugin-svelte 7.3.1、Tailwind CSS 与 @tailwindcss/vite 4.3.4、shadcn-svelte 1.8.0、svelte-check 4.7.8、@tsconfig/svelte 5.0.8、@lucide/svelte 1.68.0。使用现有 Node 26.10.0 与 pnpm 12.17.0。

TypeScript latest 为 7.0.2，但 svelte-check 4.7.8 的 peerDependencies 仅接受 ^5 或 ^6，因此锁定兼容范围内最新稳定 6.0.3。Svelte Vite 插件要求 Svelte ^5.46.4、Vite ^8；Tailwind Vite 插件支持 Vite ^8。未使用预发布版本。

通过固定 CLI 选择官方经典 Vega preset，仅生成 button、badge、card、table、input。CLI 的组件依赖声明 tailwind-variants ^3.3.0，选定该兼容范围最新稳定 3.3.1；未强升至 4.0.1。cn 为稳定 0.5.2。生成源文件随仓库保存，不在运行时从 registry 获取。pnpm 12 对刚发布的两个直接依赖记录了精确版本 release-age 例外，未关闭其他检查。

来源：[Svelte 文档](https://svelte.dev/docs/svelte/overview)、[Vite 官方安装要求](https://vite.dev/guide/)、[shadcn-svelte Vite 安装](https://www.shadcn-svelte.com/docs/installation/vite)、[Tailwind Vite 集成](https://tailwindcss.com/docs/installation/using-vite)、各包 [npm registry](https://registry.npmjs.org/)。具体补丁版本以 package.json 与 pnpm-lock.yaml 为准，不在后续轮次自动升级。

## 界面方向

使用标准 shadcn 灰阶变量和圆角、系统字体；语义颜色只用于错误提示。布局以浅色侧栏、紧凑工具栏、工单表格和顺序会话为主。Tailwind 为唯一样式策略，不引入自定义设计系统或第二 UI 库。

内容先显示同步概况，再提供查询、列表和详情；无营销区域、装饰图形或动效。按钮采用即时加载/禁用状态反馈。桌面保留侧栏，小屏折为顶部导航；长正文换行，表格仅在自身容器滚动。正文可复制，附件仅展示元数据。

## 命令与边界

在本目录执行 `pnpm install --frozen-lockfile`、`pnpm check`、`pnpm build`。`pnpm dev` 监听 127.0.0.1:5173，/api 代理到 127.0.0.1:8080；Go 端口不同须同步调整代理。生产输出为 web/dist，不需要生产 Node 服务。

## API 契约

- GET /api/status：sync（department_id、selected、ready、failed、watermark、batch_upper、last_attempt、last_error）及 jev_enabled/bub_enabled/wecom_enabled。ready 指已有快照，不表示完整成功，失败数量独立展示。
- GET /api/tickets?q=&status=：返回 {data: TicketRecord[]}，列表使用 pending、sync_error、complete 显示保存和失败状态。
- GET /api/tickets/:id：返回 TicketRecord（快照字段及 sync_error、checked_at、pending），详情直接读取自己的同步状态，不依赖当前筛选列表，因此收藏链接或筛选后进入仍显示同步错误；不接收 Raw 原始响应。
- GET /api/tickets/:id/analysis：返回 {jev:{current,success,stale},bub:{current,success,stale}}，分别展示真实结构化结果。Jev 的 success.result 含 model/answers，Bub 的 success.result 含 summary/usage/request_id。Bub summary 结构对应 Go bub-summary-v1；引用跳到当前会话的唯一消息并聚焦，缺失或重复引用不猜测。未增加逐列表行请求，避免 N+1。无结果、读取失败和过期分别展示。

所有请求同源，无 VITE_* 密钥，无前端数据库访问。刷新按钮只重新查询已有本地数据。当前无可用手动同步/重跑端点，因此不提供假操作。企业微信无页面或操作。

## 简化与验证

复用组件，采用 hash 导航与本地页面状态，不增加路由器、状态管理、表格引擎或请求库。实际消融移除了生成 preset 附带的 Inter 字体和未使用的动画样式包：构建中约 218 KB 的字体文件消失，CSS 从 38.44 KB 减为 34.45 KB；功能与类型检查保留。组件的 transition-all 收窄为颜色变化，按钮删除按压位移，未改造标准布局或语义变量。

已执行 `pnpm check`（0 错误、0 警告）、`pnpm build` 以及本目录文档结构检查。通过已有 Chromium 对隔离合成 HTTP 服务做桌面 1366×900 与窄屏 390×844 检查：列表到详情、搜索空结果、状态筛选、返回导航、运行状态、草稿/内部备注/混合发言者、不完整提示、正文转义、Jev 失败保留成功和过期标记、未知情绪强度待确认；页面无横向溢出。浏览器检查未读取真实服务或数据；本机真实 Go 接入由主协调者验收。

临时合成验证脚本、截图和浏览器配置位于被忽略的 .cache，非运行依赖，不新增测试框架或长期快照套件。生产代码不包含 Mock 数据。

Bub 增量的设计简化检查：共用 AnalysisRun/AnalysisView 元数据类型，结果结构仍各自明确；引用作为单个组件内 snippet 复用，不新增引用路由、全局状态或弹窗系统。保留独立展示组件以防评估与总结职责混合。

Bub 增量已通过同一合成浏览器主链路：建议/客户确认执行/执行未知、客户报告结果/结果未知、模型建议/待验证假设、失败保留成功及过期标记、无结果待总结、失败无成功结果、旧消息引用缺失，以及引用点击后滚动并聚焦且 hash 路由不变。补充验证筛选列表不含目标票时，详情仍显示自身同步错误。桌面与窄屏无溢出，最终 `pnpm check` 为 0 错误 0 警告，`pnpm build` 与文档结构检查通过。未调用真实模型或读取真实工单。
