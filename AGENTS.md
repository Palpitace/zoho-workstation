一旦我所属的文件夹有所变化请更新我。

本目录负责轻量级 Zoho Desk 工单分析管理系统及其项目级约定。
模块关系：Zoho → Go 只读同步、规范化和 SQLite → 独立 Jev/Bub 分析 → Gin/Svelte 展示。
不负责修改 Zoho 工单、自动企业微信通知或通用 Agent 平台。

## 当前执行边界

用户已正式启动 Goal，当前进入实现与验证阶段。按 [GOAL.md](GOAL.md) 的完整交付要求自主执行，复用已验证的 Labelife 部门配置。
进度及阻塞统一记录在 [PROGRESS.md](PROGRESS.md)。本文件是工程约定，不重复复制完整 Goal。

## 固定决策

- 后端 Go、Gin、net/http、encoding/json、log/slog、database/sql、modernc.org/sqlite、原生 SQL；仅 Go 访问数据库。
- 前端 Svelte 5、TypeScript、Vite、shadcn-svelte、兼容的 Tailwind，按需 Lucide；生产静态资源由 Go 提供。
- Bub 使用核实过的稳定 SDK，通过固定 Python 入口及 stdin/stdout JSON 与 Go 交互；stderr 日志、超时、有限并发，不拼接 Shell。
- 运行需要 Go 服务及随附 Python 环境；Node 仅参与前端开发和构建。
- Jev 与 Bub 使用同一版本的完整脱敏会话。先尝试 Jev，再向 Bub 附带同版本的可用评估作补充参考；Jev 关闭、失败或超时不阻止 Bub 独立总结。模型评估不是工单事实，不替代消息证据；不传过期评估，Bub 实际输入版本覆盖附带参考。失败不能阻塞同步或查看，旧输入结果不能覆盖新输入结果。
- Jev 为 TypeSafe AI（https://typesafe.ai/），Go 直接调用其 HTTP API；Bub 为 https://bub.build/ 的 Python SDK，生成式模型供应商单独配置。
- 用户选定 Bub 使用 DeepSeek 官方模型服务。BUB_MODEL 采用 Republic 的 provider:model 格式，地址和 Key 显式传入；仅使用无工具的 Core SDK，不加载 App 插件或持久化会话记忆。
- Jev 按官方 Choice/Score/Noul 能力建模，不要求自由文本生成；客户诉求以明确标签及未知状态评估，具体内容保留原文。
- 依赖初始化时核实官方稳定版本并锁定；OAS 放在 docs/zoho-oas Submodule，固定提交，不修改第三方内容。
- 复用已有工具及 Skills，不重复安装，不引入额外运行时服务。

## 配置与数据

配置名称见 [.env.example](.env.example)。实际凭据只写根目录 `.env`，不覆盖已有值，不输出完整内容，不发给子代理。
只报告配置存在/缺失；禁用的 AI 能力不要求凭据。密钥不得进入 VITE_*、命令行、日志、报告或快照。
所有真实工单、原始响应、脱敏副本和 AI 输出存放在忽略的 `data/` 下；测试只能使用经检查的脱敏或合成数据。自动脱敏不等于完全匿名。
Zoho 地址使用实际数据中心的 origin，不含 `/api/v1` 或 `/oauth/v2/token`；模型地址按实际接口显式映射，不假定 SDK 自动读取这些变量。

## 自动执行边界（Goal 启动后）

- 部门是唯一业务筛选边界，不由模型丢弃工单；首次默认最多 50 张，未经进一步授权不得超过 100 张。
- 用户选定 Labelife 部门，实际 ID 从本地 .env 读取；不因名称相似扩大到其他部门。
- 采样限制按跨轮询和重启的累计唯一工单数计算；长期新增工单同步的起始范围在启用前明确，不默认回填历史。
- 只读业务接口；允许必要 OAuth 刷新。无通知订阅、Webhook 接收端或真实群消息。
- 不覆盖用户改动，不删除已有数据，不做破坏性迁移，不推送、合并或部署生产。
- 最多两个子代理加主协调者；Reviewer 默认只读，明确文件所有权。
- Goal 自动开发和应用持续运行分别验证，不声称会话结束后自动部署或持续同步。
- 用户已选定先在当前机器运行，之后再部署；本期先完成本机运行验证，未来生产部署不自动执行。

## 验证与完成标准

优先小型高价值测试：分页、重复同步、恢复、更新、清洗事实及角色、AI 独立失败与结果版本、重启持久性和列表到详情主链路。
研发真实模型请求每轮最多 20 次、整个 Goal 最多 60 次，重试计数，每次失败最多自动重试一次；与正式运行额度分开，耗尽后使用 Mock 或复用结果。
每项有意义改动检查一个有价值的简化候选；区分设计检查和实际改动并运行测试的消融实验，无合理候选不强行删除。
只把实际验证过的接入标为完成。停止条件以用户 Goal 为准，不制造无收益优化任务。

## 文档同步协议

任何功能、架构、接口、依赖和写法约定更新，都必须在工作结束前同步到相关目录文档。文档同步是完成标准的一部分。
自有目录使用大写 AGENTS.md，开头使用本文件首句，三行内说明职责、关系和边界，索引仅列直接子项。
每个自有代码文件（含测试）保留适合语言的三行 input/output/pos 头；pos 包含“一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。”
遵守 shebang、build tags 等位置约束。第三方、自动生成内容、构建产物、数据和缓存豁免；JSON、锁文件不强加注释。
修改后检查头部、所在目录索引、父目录影响；仅系统级约定变化才更新根文档。结构检查只核对缺失、索引和头部，不递归制造修改。

## 启动、构建与验证

从项目根执行：`pnpm --dir web install --frozen-lockfile` 安装前端固定依赖；`make test` 运行 Go 合成测试、前端类型及文档结构检查；`make build` 构建前端静态文件与 Go 程序；`make sample` 固定最多 50 个样本 ID 并补齐本轮最多 3 张；`make run` 构建并启动本机服务。前端开发用 `pnpm --dir web dev` 代理本机 Go API。
首次可用 `make setup` 依次安装锁定的 Go、pnpm 和 uv 依赖；`make test` 同时执行 Python 合成契约检查。依赖版本只在需要兼容性变化时更新。
Go 在 127.0.0.1:8080 提供 web/dist 和 `/api/status`、`/api/tickets`、`/api/tickets/:id`、`/api/tickets/:id/analysis`。生产运行需保留 web/dist 和 Python 环境，不需要 Node 服务。服务启动后按 SYNC_INTERVAL 自动同步（每批最多 3 张）并独立调度 Jev/Bub；样本集合冻结后复用，不重建数据库绕过采样上限。
页面提供 POST `/api/sync` 与 `/api/tickets/:id/analysis/:kind`，校验本机 Host、允许 Origin 与临时会话令牌。serve 持有同库同步/分析操作锁，运行期间使用页面操作；CLI sample/analyze 会拒绝并发写者。后台自动评估只选择固定开发集，留出样本不自动调用；模型预算耗尽继续同步和展示。详细状态/关闭约定见 internal/app/AGENTS.md。
`./bin/workstation prepare -max 20` 在 data/review 保存脱敏副本和固定开发/留出样本清单；`analyze -kind jev -max 1` 或 `-kind bub` 调用所选能力。二者会话版本一致，Bub 实际输入 Hash 还覆盖可选评估参考。研发默认 development-1 轮次的 20 次与全 Goal 60 次预算存于 SQLite，重启不清零。Python 安装见 python/AGENTS.md。

## 直接文件索引

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 项目约定 | 范围、技术、配置和文档协议 |
| GOAL.md | 完整交付目标 | 用户要求、已确认选择、授权边界及验收标准 |
| .env.example | 可提交模板 | 配置字段及无密钥默认值 |
| .env | 本地配置，Git 忽略 | 凭据、已定位的部门及功能启用开关 |
| .gitignore | 提交边界 | 排除凭据、运行数据和产物 |
| PROGRESS.md | 唯一进度记录 | 当前阶段、验证、阻塞及下一步 |
| .gitmodules | 第三方固定来源 | Zoho OAS Submodule |
| go.mod | Go 依赖契约 | 工具链要求与依赖版本 |
| go.sum | 依赖校验 | 固定下载校验值 |
| Makefile | 命令入口 | 构建、采样、运行与验证 |
| cmd | 可执行入口 | 主程序与文档检查 |
| internal | 内部能力 | 配置、Zoho、清洗、存储和同步 |
| docs | 接口依据 | OAS 与兼容性说明 |
| web | 前端应用 | Svelte 列表、详情、分析及运行状态 |
| python | Bub 入口 | 固定 stdin/stdout JSON 总结进程及锁定环境 |
