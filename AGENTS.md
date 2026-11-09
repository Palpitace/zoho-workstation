一旦我所属的文件夹有所变化请更新我。

> 2026-11-05 用户最新范围覆盖旧采样/重试限制：仅发现指定部门从 2026-11-05T00:00:00+08:00 创建的工单，持续分页发现新增，不回填更早历史；移除固定 50 样本总量。Jev/Bub 普通失败取消累计次数上限，持续指数退避（1 至 30 分钟，上限后每 30 分钟），保留超时、有限并发、去重、证据校验和历史。开发仍只调用合成 Mock，不自动启动真实采集。

本目录负责轻量级 Zoho Desk 工单分析管理系统及其项目级约定。
模块关系：Zoho → Go 只读同步、规范化和 SQLite → 独立 Jev/Bub 分析 → Gin/Svelte 展示。
不负责修改 Zoho 工单、自动企业微信通知或通用 Agent 平台。

## 当前执行边界

用户已正式启动 Goal，当前进入实现与验证阶段。按 [GOAL.md](GOAL.md) 的完整交付要求自主执行，复用已验证的 Labelife 部门配置。
进度及阻塞统一记录在 [PROGRESS.md](PROGRESS.md)。本文件是工程约定，不重复复制完整 Goal。
2026-11-05 用户明确授权首次源码上传，并在 Issue #1 修复后授权提交、推送合并到 https://github.com/Palpitace/zoho-workstation 的 main 分支；随后授权关闭模型请求额度并重新提交推送。此授权不包含上传凭据/运行数据或部署生产。

## 固定决策

- 后端 Go、Gin、net/http、encoding/json、log/slog、database/sql、modernc.org/sqlite、原生 SQL；仅 Go 访问数据库。
- 前端 Svelte 5、TypeScript、Vite、shadcn-svelte、兼容的 Tailwind，按需 Lucide；生产静态资源由 Go 提供。
- 列表按情绪、优先级、问题分类分列显示 Jev，支持五列表头 Combobox、顶部字段/日期组合搜索和清除筛选，以及真实工单号跳转。详情采用紧凑工单信息 → Jev → Bub → 默认折叠的倒序沟通记录；模型结果来源在界面隐藏但继续保存。Bub v6 按整张工单综合归纳，只输出有依据的设备信息、客户反馈、客服回复/排查，客户反馈/客服处理各至多一份摘要、空段不展示；事实引用可展开并定位消息，旧格式结果保留并提示重新总结。未知作者的公开入站 SUCCESS 线程仅允许逐字摘录反馈并标注作者未确认，不据方向推断作者身份。
- Bub 使用核实过的稳定 SDK，通过固定 Python 入口及 stdin/stdout JSON 与 Go 交互；stderr 日志、超时、有限并发，不拼接 Shell。
- 运行需要 Go 服务及随附 Python 环境；Node 仅参与前端开发和构建。
- Bub v6 先读取完整脱敏会话并总结；Jev v2 只基于当前版本的成功 Bub 总结评估。缺少当前总结时 Jev 等待，不回退原始会话、不使用过期总结。总结及评估均是模型输出，缺失事实保持未知；失败不影响同步和查看，旧输入结果不能覆盖新输入结果。
- Jev 为 TypeSafe AI（https://typesafe.ai/），Go 直接调用其 HTTP API；Bub 为 https://bub.build/ 的 Python SDK，生成式模型供应商单独配置。
- 用户选定 Bub 使用 DeepSeek 官方模型服务。BUB_MODEL 采用 Republic 的 provider:model 格式，地址和 Key 显式传入；仅使用无工具的 Core SDK，不加载 App 插件或持久化会话记忆。
- Jev 按官方 Choice/Score/Noul 能力建模，不要求自由文本生成；客户诉求以明确标签及未知状态评估，具体内容保留原文。
- 依赖初始化时核实官方稳定版本并锁定；OAS 放在 docs/zoho-oas Submodule，固定提交，不修改第三方内容。
- 复用已有工具及 Skills，不重复安装，不引入额外运行时服务。

## 知识扩展（2026-11-09 用户确认）

在保留既有事实总结/Jev 顺序的基础上，用户授权：自动从已同步历史会话与总结积累可追溯案例，导入独立 DOCX/PDF/Markdown/TXT 文档，自动给新工单内部处理建议。案例可自动成为有证据和效果标记的参考；新 SOP/修订先存草稿，由用户明确发布。该扩展替代原先仅限单工单总结的范围，但不包含自动回复、Zoho 写入、自修改规则或扩大历史同步时间范围。
Bub 总结、案例提取和建议使用同一配置并共享一个并发名额；Jev 只读取事实总结。知识与建议独立保存，选中来源/内容版本变化才更新建议。知识库采用现有 SQLite 和固定无工具 Python 入口，不新增服务或模型供应商。文档先本地解析并预览，原件在忽略的 data/knowledge/；详见 [知识库使用与限制](docs/knowledge.md)。

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
- 交付后用户确认从同机 Windows 访问 WSL 的 wslc 容器。APP_LISTEN_HOST 默认 127.0.0.1，容器可显式设 0.0.0.0；宿主侧使用回环端口转发，浏览器仍访问 localhost:8080。此请求不包含局域网或公网开放。

## 验证与完成标准

优先小型高价值测试：分页、重复同步、恢复、更新、清洗事实及角色、AI 独立失败与结果版本、重启持久性和列表到详情主链路。
2026-11-05 用户明确关闭模型累计请求额度：不再按单轮 20 次或总计 60 次拦截，现有历史和次数继续保存。每个输入失败最多自动重试一次，成功结果复用、并发和超时保护保留；本地开发验证仍优先 Mock，不为了验证额度而调用真实模型。
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
首次可用 `make setup` 依次安装锁定的 Go、pnpm 和 uv 依赖；`make test` 同时执行 Python 合成契约和打包脚本回归检查。依赖版本只在需要兼容性变化时更新。
Go 默认在 127.0.0.1:8080 提供 web/dist 和 `/api/status`、`/api/tickets`、`/api/tickets/:id`、`/api/tickets/:id/analysis`，TCP 监听 IP 可由 APP_LISTEN_HOST 配置。Windows/WSL/容器访问步骤见 docs/compatibility.md。生产运行需保留 web/dist 和 Python 环境，不需要 Node 服务。服务启动后接续处理固定样本待同步批次（默认每批 9 张、最多 3 张并发读取、正常批次间隔 1 秒、失败退避），处理完后按 SYNC_INTERVAL 常规增量轮询；同步独立冷却失败工单，确认 Zoho 已合并、删除或移出部门则保留历史并停止读取，默认列表隐藏、可勾选查看历史，水位不再等待单票恢复。继续调度 Bub 总结后的 Jev 评估；样本集合冻结后复用，不重建数据库绕过采样上限。
页面提供 POST `/api/sync` 与 `/api/tickets/:id/analysis/:kind`，校验本机 Host、允许 Origin 与临时会话令牌。serve 持有同库同步/分析操作锁，运行期间使用页面操作；CLI sample/analyze 会拒绝并发写者。后台自动分析覆盖所有已完整同步且未停止的工单，研发/留出清单不限制业务调度；完成后立即接续下一张，累计请求次数仅作统计，不阻止模型处理。SYNC_BATCH_SIZE（默认 9，1..50）与 SYNC_CONCURRENCY（默认 3，1..5）分别控制批次和并发，修改后重启生效；SQLite 保存仍串行。详细状态/关闭约定见 internal/app/AGENTS.md。
`./bin/workstation prepare -max 20` 在 data/review 保存脱敏副本和固定开发/留出样本清单；`analyze -kind jev -max 1` 或 `-kind bub` 调用所选能力。prepare 仅保存会话；首次研发调用先选 bub，再选 jev。Jev 实际输入 Hash 覆盖当前会话及总结内容、Schema 和模型信息，相同内容的重复总结不因运行 ID 变化重复评估。默认 development-1 轮次和累计请求次数存于 SQLite，重启不清零；请求额度已关闭。Python 安装见 python/AGENTS.md。

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
| .github | 自动构建 | Windows/macOS 原生验证和下载产物 |
| scripts | 打包和启动 | 便携包、异地解压验收、双击入口 |
| python | Bub 入口 | 固定 stdin/stdout JSON 总结进程及锁定环境 |

## 下载版构建与运行

用户授权增加 Windows x64、macOS Intel/Apple Silicon 自动构建并提交远程；这不包含生产部署。GitHub Actions 构建包含 Go、前端及独立 Python/Bub 环境的包，下载者无需安装开发工具链；操作见 [docs/downloads.md](docs/downloads.md)。源代码仍支持原 .venv 开发方式。
`python scripts/package.py` 在前端构建后原生打包，`python scripts/smoke_package.py <归档>` 异地解压离线验证。`workstation check` 检查配置、资源、数据库锁及启用的 Python SDK/代理 transport，不调用外部服务。Bub 的 SOCKS 依赖随锁文件和下载包提供；自动/手动/CLI 分析在请求计数前离线预检，环境错误不产生逐票重试风暴，详情见 python/AGENTS.md。
日志固定在 logs/workstation.log，slog 同时输出终端；lumberjack v2.2.1 实现 10 MiB/3 备份/7 天轮转，启动错误也留存。密钥、正文和 Python 原始 stderr 不写日志。Windows 使用 LockFileEx，Unix 使用 Flock；进程退出释放操作锁。

历史失败恢复：自动失败上限不因升级/重启重置。运行状态可确认固定失败版本，每票补执行一次；仍使用原调度器和持久历史，未开始的确认不跨重启保留。接口与约束见 internal/app/AGENTS.md，用户操作见 docs/downloads.md。

Issue #9：运行状态页提供Bub总结/案例/建议的任务积压、活动工单及持久重试诊断；列表和详情共用任务状态。上游HTTP和响应格式异常分别诊断，禁止日志记录供应商响应正文；细节见 internal/app/AGENTS.md。
