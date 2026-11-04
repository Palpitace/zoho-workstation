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

## Zoho API

官方网页：https://desk.zoho.com/DeskAPIDocument 。本地依据为 zoho-oas/v1.0 下的 Ticket.json、Search.json、Thread.json、TicketComment.json 及引用文件。

- 普通列表使用 `departmentId`、`sortBy=-createdTime`、`from=0` 和受剩余样本额度限制的 `limit`。不能使用缺乏依据的 `sortBy=modifiedTime`。
- 增量优先使用 `/tickets/search`，包含固定 `departmentId`、已冻结样本的 `id` 集合以及 `modifiedTimeRange`。需要 `Desk.search.READ`；当前真实服务返回 403 FORBIDDEN，未据此推定具体权限原因。已验证仅在固定 ID 内 GET 详情并检查 modifiedTime 的回退，不扩大扫描范围；任一详情失败仍不推进水位。请求代价详见 ../internal/zoho/AGENTS.md。
- 搜索 from 上限 OAS 为 999，网页为 4999；当前实现保守使用 999。本项目最多 50 个固定 ID，不应触及该上限。
- 普通列表排序字段 OAS 与网页不同，共同支持 createdTime；采用其交集，不修改第三方文件。
- 线程和评论 from 均从 0 开始。本地每页 50；线程每项再取详情。详情 `isContentTruncated=true` 时按已知同源路径调用 fullContent，不能直接信任返回 URL。
- fullContent 与线程详情采用相同 JSON 形状。仍截断或缺少 content 视为读取失败，不标成完整。
- 会话获取后再次读取工单更新时间，若期间发生改变则标记不完整并待重试，不推进成功水位。
- 未知动态字段保留于原始响应，不使用拒绝未知字段的严格解码。联系人/产品使用工单 include 展开，不批量抓取联系人或产品库。

## AI 接口

Jev 按 https://api.typesafe.ai/openapi.json 与 https://docs.typesafe.ai/api/system-one 实现，使用 jev-1.14.0（2026-11-04 核实）。Choice 的 criteria 是对象，问题名称不会传给模型，判断要求显式写 instructions。14 项独立问题共用 state，一次请求；所有选项包含 unknown。不展示为已校准业务概率，不增加加权评分。保存实际响应 model 与 usage。

Bub/Republic 使用当前稳定 0.6.1 的 Core SDK 发布源码核实，Python >=3.12。DeepSeek 官方目前提供 JSON object 模式，不宣称严格 schema 输出；Python和Go校验结果结构、枚举和消息引用。默认 deepseek:deepseek-flash 是可变别名，保存服务实际返回模型信息，不伪造不可变版本。来源 https://api-docs.deepseek.com/guides/json_mode/ 和 https://api-docs.deepseek.com/api/create-chat-completion/ 。

真实 ONLINE_CHAT 线程包含混合发言者聊天导出及访客信息，与普通独立邮件不同。保留原始全文，规范化 Role=unknown/MixedAuthors=true；模型副本遮盖访客 IP/位置、聊天 ID 和设备唯一标识。不能把整条转录归给一个客户，也不把客服推断当已确认根因。

## 运行边界

先在本机 127.0.0.1 提供查询服务。未来向其他机器开放时需明确管理接口访问控制，生产部署另行授权。
真实工单、原始响应、脱敏副本、AI 输出只留本地 data/，不提交 Git。自动脱敏有已知限制，详见 internal/ticket/AGENTS.md。
企业微信通知暂缓；将来只在单独授权后接入，不提前增加模块或表。
