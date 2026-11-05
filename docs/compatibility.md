# 版本与接口依据

初始化核实日期：2026-11-04。Go 依赖固定在 go.mod/go.sum；`go get ...@latest` 解析稳定版本后锁定，不在每轮开发自动升级。

| 依赖 | 选择 | 依据与用途 |
| --- | --- | --- |
| Gin | v1.12.0 | https://gin-gonic.com/en/docs/quickstart/ ；本机查询 API |
| modernc.org/sqlite | v1.61.1 | https://pkg.go.dev/modernc.org/sqlite ；database/sql 驱动 |
| golang.org/x/net | v0.62.0 | Go 模块代理稳定版本；HTML 解析 |
| godotenv | v1.5.1 | https://github.com/joho/godotenv ；复用 .env 语法处理，避免手写 Shell 解析 |
| Zoho OAS | 1e77db20ed01b73c52b7b9bd0ade97be8acfa611 | https://github.com/zoho/zohodesk-oas ；Git Submodule 固定提交 |

当前工具链 Go 1.27.3 编译并测试。前端依赖与兼容性记录在 ../web/AGENTS.md，固定在 package.json/pnpm-lock.yaml；Python Bub/Republic 依据发布源码核实，固定版本与环境见 ../python/AGENTS.md。

## 工程 Skills 核对

2026-11-04 收尾时重新检查本机已安装 Skills，再读取 [Matt Pocock Skills 当前说明](https://github.com/mattpocock/skills)，确认不应重复安装来源。仓库当前提交为 `4ae2317ddb22b046e26455e4f9c9b3aaaf62212d`；本机现有 `tdd` 的 SKILL.md 与该提交 `skills/engineering/tdd/SKILL.md` 逐字相同，本项目实际复用其小步 RED→GREEN 工作流，没有重复安装或全量安装。

同时查阅该提交的 code-review 与 codebase-design，按本项目范围核对规范/需求和接口简化。最小职责选择复用已有 tdd（小步实现及测试）、think（必要设计）、check（审查）、hunt（故障定位）、reclaim-code-entropy（简化）；安装存在不代表每个技能都已执行。项目明确的 GOAL.md、单一 PROGRESS.md、两子代理上限和预算优先，不新增工单平台、重复规则文档或运行时依赖。此记录证明本次核对，不伪称缺失的早期安装记录。

## Zoho API

官方网页：https://desk.zoho.com/DeskAPIDocument 。本地依据为 zoho-oas/v1.0 下的 Ticket.json、Search.json、Thread.json、TicketComment.json 及引用文件。

- 普通列表使用 `departmentId`、`sortBy=-createdTime`、`from=0` 和受剩余样本额度限制的 `limit`。不能使用缺乏依据的 `sortBy=modifiedTime`。
- 增量优先使用 `/tickets/search`，包含固定 `departmentId`、已冻结样本的 `id` 集合以及 `modifiedTimeRange`。需要 `Desk.search.READ`；当前真实服务返回 403 FORBIDDEN，未据此推定具体权限原因。已验证仅在固定 ID 内 GET 详情并检查 modifiedTime 的回退，不扩大扫描范围；单票失败独立持久保留并冷却重试，不再阻塞其他候选的水位；全局查询错误/取消仍不提交批次。搜索成功但缺失的固定 ID 也用详情复核，缺失本身不认定移出。请求代价详见 ../internal/zoho/AGENTS.md。
- 搜索 from 上限 OAS 为 999，网页为 4999；当前实现保守使用 999。本项目最多 50 个固定 ID，不应触及该上限。
- 普通列表排序字段 OAS 与网页不同，共同支持 createdTime；采用其交集，不修改第三方文件。
- 线程和评论 from 均从 0 开始。本地每页 50；线程每项再取详情。详情 `isContentTruncated=true` 时按已知同源路径调用 fullContent，不能直接信任返回 URL。
- fullContent 与线程详情采用相同 JSON 形状。仍截断或缺少 content 视为读取失败，不标成完整。
- 会话获取后再次读取工单更新时间，若期间发生改变则标记不完整并独立待重试；水位表示批次处理进度，不代表每张会话完整。确认同一 ID 部门不同才停止后续读取并保留历史，不凭 403/404 或计数不一致推断移出。
- 未知动态字段保留于原始响应，不使用拒绝未知字段的严格解码。联系人/产品使用工单 include 展开，不批量抓取联系人或产品库。

## AI 接口

Jev 按 https://api.typesafe.ai/openapi.json 与 https://docs.typesafe.ai/api/system-one 实现，使用 jev-1.14.0（2026-11-04 核实）。Choice 的 criteria 是对象，问题名称不会传给模型，判断要求显式写 instructions。14 项独立问题共用 state，一次请求；所有选项包含 unknown。不展示为已校准业务概率，不增加加权评分。保存实际响应 model 与 usage。

Bub/Republic 使用当前稳定 0.6.1 的 Core SDK 发布源码核实，Python >=3.12。DeepSeek 官方目前提供 JSON object 模式，不宣称严格 schema 输出；Python和Go校验结果结构、枚举和消息引用。默认 deepseek:deepseek-flash 是可变别名，保存服务实际返回模型信息，不伪造不可变版本。来源 https://api-docs.deepseek.com/guides/json_mode/ 和 https://api-docs.deepseek.com/api/create-chat-completion/ 。

真实 ONLINE_CHAT 线程包含混合发言者聊天导出及访客信息，与普通独立邮件不同。保留原始全文，规范化 Role=unknown/MixedAuthors=true；模型副本遮盖访客 IP/位置、聊天 ID 和设备唯一标识。不能把整条转录归给一个客户，也不把客服推断当已确认根因。

## 运行边界

Go 默认监听 127.0.0.1，容器可通过 APP_LISTEN_HOST 显式监听 0.0.0.0，提供查询、受保护的同步/重跑入口和自动周期。POST 需同源临时令牌及允许的 Origin，Host 限本机；开发 Vite 代理重写 Host 并保留 Origin。同库同步/分析互斥使用当前 Linux 环境的 flock，移植到其他系统前须验证该边界。未来向其他机器开放时需明确管理接口访问控制，生产部署另行授权。
真实工单、原始响应、脱敏副本、AI 输出只留本地 data/，不提交 Git。自动脱敏有已知限制，详见 internal/ticket/AGENTS.md。
企业微信通知暂缓；将来只在单独授权后接入，不提前增加模块或表。

## 同机 Windows → WSL → wslc 容器

用户使用 Paseo 连接 WSL2 内的 wslc 容器。容器本地 localhost 通并不能证明 Windows 已通。当前 .env 已追加 APP_LISTEN_HOST=0.0.0.0，原有值保留；应用接受转发后的 localhost:8080 Host，直接用容器 IP 作 URL 仍返回 403。

在 **Windows PowerShell** 中检查宿主端口映射（不要直接执行 Windows 未安装的 docker）：

```powershell
wsl.exe -- docker port wslc 8080/tcp
```

若 Docker 所在 WSL 不是默认发行版，先用 `wsl.exe --list --verbose` 查看，再用 `wsl.exe -d <发行版名称> -- docker port wslc 8080/tcp`。目标是宿主回环 127.0.0.1:8080 转发到容器 8080，然后在 Windows 打开 http://localhost:8080。现有容器映射需在宿主确认；不能为了添加映射直接删除/重建含用户数据的容器。

依据：[Microsoft WSL 网络说明](https://learn.microsoft.com/en-us/windows/wsl/networking)、[Docker 端口发布说明](https://docs.docker.com/engine/network/port-publishing/)。Paseo 的客户端连接本身不证明项目 8080 已发布；其连接机制见 [Paseo connectivity](https://paseo.sh/docs/connectivity)。Windows 端实测尚待宿主映射确认，不将容器内验证标成 Windows 已可访问。

## Issue #1 运行调整（2026-11-05）

首轮冻结采样仍最多 50 张、每批最多 3 张；有剩余时正常间隔 1 秒接续，失败退避 5 秒起、最多 2 分钟，429 另遵守 Retry-After。完成后恢复 SYNC_INTERVAL 的常规轮询。单票失败每至少 5 分钟重试一次，重启不会丢失；旧 SQLite 自动新增一个 sync_stopped 列，已有快照、分析记录和额度不变。

Bub 从启动 Go 的环境中显式继承大小写 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY；需要代理时先在本机启动环境配置可达地址，不把代理认证写入命令行或提交到仓库。仍不继承 Zoho/Jev/其他环境凭据；模型 Key 通过原有 stdin 传入。连接失败、超时和上游响应错误有不同固定错误码。修改代理后须重新启动 Go 服务才能继承新环境；该次代理修复不重置历史或重发真实模型请求。后续用户已关闭累计请求额度，见下方更新。

2026-11-05 后续更新：用户关闭单轮 20 次／累计 60 次请求额度，应用不再因已用次数停止自动或手动分析。API budget 保留 total/round 统计，移除上限字段；前端改为“模型调用统计”。不新增环境变量、不清空数据库、不改变自动样本或失败自动重试上限。更新源码并重启服务后生效。

## 详情与 Bub v3（2026-11-05）

Bub 输出升级到 bub-summary-v3：device_info、customer_feedback、support_responses 均为可空数组，元素保留 text/message_ids。前端只展示非空段；旧版成功结果仍在数据库/API 中，标记过期并提示重新总结，不能从旧混合字段机械映射客户/客服事实。无需数据库迁移，Jev Schema 未改变。本次只验证合成 SDK/接口和页面，不自动批量调用真实模型生成新版。

界面隐藏 Jev/Bub 模型来源面板但保留消息证据引用；工单信息用现有 Card 的 sm 尺寸，沟通记录默认折叠并在副本上倒序显示，未知时间置后。引用跳转会展开原文，后台会话排序和输入 Hash 不受展示顺序影响。

## 同步并发与单封来信修正（2026-11-05）

默认服务批次从 3 调整为 9，Fetch 并发默认 3；SYNC_BATCH_SIZE=1..50、SYNC_CONCURRENCY=1..5，旧 .env 缺字段使用默认值，修改后重启生效。状态页显示实际配置。仅工单间并发，单工单分页/消息详情保持顺序；SQLite 由协调者串行保存，429 取消同组并保留剩余工作，正常批次间隔/退避/固定采样不变。

真实只读复核发现：两张指定工单已 isDeleted=true，线程返回 404；另一张 threadCount=2 实取 3 条完整线程。明确删除现在保留本地历史并停止同步，不再当普通失败重试；403/404 本身不能推断删除。计数用作缺失下界，实取少于计数仍失败，多于计数可通过，前后版本/分页完整性不放宽。停止项不自动重新启用；恢复已删除工单后的恢复同步需后续处理。

Bub v4 保留三数组结构，只为未知作者、公开入站、SUCCESS 线程提供单消息逐字摘录的反馈路径，明确标注作者未确认；不靠邮件方向猜身份，不允许改写或客服消息冒充客户。无客服回复输出空数组。旧 v3 成功仍可展示但过期，旧失败不会消耗 v4 的尝试次数，无数据库迁移。一次真实模型请求使用合成单封来信，通过 Go/Python 双重校验；不能据此保证所有模型结果正确。
