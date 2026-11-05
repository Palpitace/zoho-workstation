一旦我所属的文件夹有所变化请更新我。

负责模型输入最小化和独立结构化分析能力。
接收 ticket 快照，向 Go 编排返回 JSON；结果由 store 保存。
不访问数据库、不修改 Zoho、不发送企业微信消息。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 输入和外部分析边界 |
| input.go | 分析输入 | 完整脱敏会话、可选 Jev 参考及实际输入 Hash |
| input_test.go | 行为测试 | 保留否定事实、会话不变、参考版本和脱敏 |
| jev.go | TypeSafe 接口 | 单次 14 个独立 Choice 问题、枚举校验及安全错误 |
| jev_test.go | 协议测试 | 正常、未知、非法枚举及失败响应 |
| bub.go | Python 边界 | 固定入口、最小代理环境、安全错误分类及 v3 三类事实/来源二次校验 |
| bub_test.go | 摘要契约测试 | 可空分类/角色来源校验、真实子进程模拟代理与凭据环境隔离 |

Bub 使用 python/.venv/bin/python 与 python/main.py 固定路径，无 Shell 拼接。stdin 仅传完整脱敏会话、可选 Jev 参考、Bub 专用模型配置及 Schema；子进程环境保留固定 PATH/编码/不写缓存，并仅按名称继承非空 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY、NO_PROXY 及其小写形式，不继承 Zoho/Jev/Bub Key 或其他父进程变量。代理值可能含认证信息，禁止日志输出；大小写优先级和 NO_PROXY 行为由锁定的 SDK HTTP 客户端处理。90 秒上限、stdout 1 MiB 上限、stderr 不回显；失败不清已有结果。连接失败、超时、上游错误分别透传固定 model_connection_failed、model_timeout、model_upstream_failed，不输出异常原文。结构校验能拒绝未知引用、错角色或未发客服消息，不能证明模型自由文本事实正确。

代理回归使用本机 CONNECT 模拟代理和真实 Go→Python→Bub/Republic 路径，单次子进程验证全部 8 项白名单原值继承、实际 CONNECT 路由及父环境凭据隔离，不重复验证 SDK 的大小写优先级；需要先执行 make setup 安装固定 Python 环境，不读取 .env 或访问真实模型。错误分类另由 Python SDK Mock 验证；不新增模型预算、配置项或依赖。

Jev Schema 为 jev-assessment-v1。使用官方 POST systemone 的 model/state/questions、Bearer 认证及 model/answers/usage 响应；来源 https://docs.typesafe.ai/api/system-one 和 https://api.typesafe.ai/openapi.json（2026-11-04 核实）。全部问题独立提供 instructions，不依赖问题键被模型读取。每项显式 unknown；诉求为六个可同时成立的标签。保留供应商 confidence/probabilities，但不声称业务校准或构造加权分数。情绪仅依据近期客户消息，草稿及客服建议不等于客户执行。Jev state 暂限 24 KiB，这是保守工程限制而非精确 token 上限；超限显式失败，不截断。

Jev 与 Bub 使用同一完整脱敏会话，不以总结代替评估输入。会话 Hash 包含消息事实、角色、可见性、发送状态、时间及附件元数据，排除工单状态/优先级/编辑时间和部门。超过 512 KiB 拒绝发送，不静默截断。自动脱敏有局限，沿用 ticket/AGENTS.md；真实发送前在本地 data/ 留副本并检查。

Bub Schema 为 bub-summary-v3；完整会话额外包含 conversation_hash 与 jev_reference（无参考时为 null）。参考仅传模型评估标签、来源运行 ID、会话 Hash、Schema 和请求/返回模型标识，不传凭据或概率。评估不充当原始消息或事实引用；参考会话版本不匹配直接拒绝。Bub Hash 覆盖完整实际输入，参考增删或替换会使旧总结过期；旧 v1/v2 总结保留为过期历史，不按新分类自动改写。来源选择由 app 读取当前会话版本的成功 Jev 结果完成。
输入超限仍返回对应 Hash 以保存本地失败版本，但不返回可发送正文；本地失败不计为模型调用。

用户指定的新输出固定为 device_info、customer_feedback、support_responses 三个数组，每类最多 10 项，每项 `{text,message_ids}`。无依据则空数组，不填占位语，不再生成强制当前情况、卡点或下一步建议。Python Prompt 明确建议不等于执行，客户的执行/实际结果归入反馈并引用客户发言；客服分类只描述已经回复或给予的排查，不推断客户已照做。

通常必须引用真实消息。客户反馈至少包含 customer/mixed_authors 证据；客服回复至少包含 agent/mixed_authors 证据，引用中不能含 private 或 DRAFT/FAILED/PENDING 消息。混合转录的逐句归属仍由模型根据正文判断。device_info 唯一允许无消息引用的例外：text 必须逐字等于工单 product 或为 description 中逐字片段，前端可显示“工单字段”；Prompt 禁止拼接或省略否定/条件改变含义。此机械校验不证明摘录必然是设备信息或所有模型文本正确。

简化：三类统一复用 fact，删除旧 Measure/NextStep 及只为旧字段服务的枚举校验。v3 引用与可空分类已在旧实现观察失败后通过；代理和 SDK Mock 保持，不新增依赖、自动迁移旧结果或真实调用。
