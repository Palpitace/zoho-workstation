一旦我所属的文件夹有所变化请更新我。

负责模型输入最小化和独立结构化分析能力。
接收 ticket 快照，向 Go 编排返回 JSON；结果由 store 保存。
不访问数据库、不修改 Zoho、不发送企业微信消息。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 输入和外部分析边界 |
| input.go | 分析输入 | Bub 完整会话、Jev 总结输入及实际 Hash |
| input_test.go | 行为测试 | 保留否定事实、会话不变、无反向参考和脱敏 |
| jev.go | TypeSafe 接口 | 单次 14 个独立 Choice 问题、枚举校验及安全错误 |
| jev_test.go | 协议测试 | 正常、未知、非法枚举及失败响应 |
| bub.go | Python 边界 | 固定入口、最小代理环境、安全错误分类及 v6 三类事实/来源二次校验 |
| bub_test.go | 摘要契约测试 | 可空分类/角色来源校验、真实子进程模拟代理与凭据环境隔离 |

Bub 使用 PythonExecutable 选择固定本地解释器与 python/main.py，无 Shell 拼接。stdin 仅传完整脱敏会话、Bub 专用模型配置及 Schema；子进程环境保留固定 PATH/编码/不写缓存，并仅按名称继承非空 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY、NO_PROXY 及其小写形式，不继承 Zoho/Jev/Bub Key 或其他父进程变量。代理值可能含认证信息，禁止日志输出；大小写优先级和 NO_PROXY 行为由锁定的 SDK HTTP 客户端处理。90 秒上限、stdout 1 MiB 上限、stderr 不回显；失败不清已有结果。连接失败、超时、上游错误分别透传固定 model_connection_failed、model_timeout、model_upstream_failed，不输出异常原文。结构校验能拒绝未知引用、错角色或未发客服消息，不能证明模型自由文本事实正确。

代理回归使用本机 CONNECT 模拟代理和真实 Go→Python→Bub/Republic 路径，单次子进程验证全部 8 项白名单原值继承、实际 CONNECT 路由及父环境凭据隔离，不重复验证 SDK 的大小写优先级；需要先执行 make setup 安装固定 Python 环境，不读取 .env 或访问真实模型。错误分类另由 Python SDK Mock 验证；不新增模型预算、配置项或依赖。

Jev Schema 为 jev-assessment-v2。使用官方 POST systemone 的 model/state/questions、Bearer 认证及 model/answers/usage 响应；来源 https://docs.typesafe.ai/api/system-one 和 https://api.typesafe.ai/openapi.json（2026-11-04 核实）。全部问题独立提供 instructions，不依赖问题键被模型读取。每项显式 unknown；诉求为六个可同时成立的标签。保留供应商 confidence/probabilities，但不声称业务校准或构造加权分数。情绪仅依据总结明确反映的近期客户情绪；总结的中性措辞不等于客户中性，缺证据为 unknown，草稿及客服建议不等于客户执行。Jev state 暂限 24 KiB，这是保守工程限制而非精确 token 上限；超限显式失败，不截断。

Bub 先总结完整脱敏会话，Jev 后评估当前总结。会话 Hash 包含消息事实、角色、可见性、发送状态、时间及附件元数据，排除工单状态/优先级/编辑时间和部门。超过 512 KiB 拒绝发送，不静默截断。自动脱敏有局限，沿用 ticket/AGENTS.md；真实输入保存在 data/ 私有文件。

Bub Schema 为 bub-summary-v6，额外包含 conversation_hash，不再包含 jev_reference。Jev SummaryInput 只发送当前总结及其输入 Hash、会话版本、Schema/模型信息；不发送原始消息，也不因运行 ID 改变而失效。总结内容变化更新 Hash；选择当前成功总结由 app 负责。Jev v2 Prompt 明确模型总结非原始事实，未知不可用总结中性措辞补齐，客服建议不等于客户执行。旧结果保留但过期，不迁移或改写。
输入超限仍返回对应 Hash 以保存本地失败版本，但不返回可发送正文；本地失败不计为模型调用。

用户指定的新输出固定为 device_info、customer_feedback、support_responses 三个数组，设备最多 10 项、客户反馈和客服回复各最多 1 项，每项 `{text,message_ids}`。无依据则空数组，不填占位语，不再生成强制当前情况、卡点或下一步建议。Python Prompt 明确建议不等于执行，客户的执行/实际结果归入反馈并引用客户发言；客服分类只描述已经回复或给予的排查，不推断客户已照做。

通常必须引用真实消息。客户反馈至少包含 customer/mixed_authors 证据；客服回复至少包含 agent/mixed_authors 证据，引用中不能含 private 或 DRAFT/FAILED/PENDING 消息。混合转录的逐句归属仍由模型根据正文判断。device_info 唯一允许无消息引用的例外：text 必须逐字等于工单 product 或为 description 中逐字片段，前端可显示“工单字段”；Prompt 禁止拼接或省略否定/条件改变含义。此机械校验不证明摘录必然是设备信息或所有模型文本正确。

简化：三类统一复用 fact，删除旧 Measure/NextStep 及只为旧字段服务的枚举校验。v3 引用与可空分类已在旧实现观察失败后通过；代理和 SDK Mock 保持，不新增依赖、自动迁移旧结果或真实调用。

v4 客户反馈例外：只有单条引用，role=unknown、source=thread、direction=in、visibility=public、status=SUCCESS 且 text 为 body 连续逐字片段时允许保存。不是将 role 改成 customer，也不是任意未知作者或客服转述豁免；页面标注来信原文/作者未确认。Prompt 要求完整保留否定/条件，不翻译改写，客服回复缺失时 support_responses=[]。Go/Python 同步校验；Schema 升级使旧失败版本不再挡住新版本重试，旧结果继续保留。单封正常客户来信也不要求有客服回复。测试先在旧校验拒绝未知作者原文，再在新校验通过，同时拒绝改写、客服、内部、出站和草稿证据。实际只调用 1 次配置的 Bub 模型，使用合成单封未知作者来信，返回 1 条原文反馈、0 条客服回复；不代表所有实际模型输出都能通过。

2026-11-05 整单汇总 v5：仍一次发送完整会话。device_info 最多 10 项；customer_feedback/support_responses 各最多 1 项（无内容为空），text 综合同一工单的主要问题、实际处理进展与当前状态，message_ids 可覆盖多条证据。按主题合并重复内容，禁止逐线程罗列，保持建议/执行/结果区分，不新增评估或下一步建议。Python/Go 双重校验数量，结构形状不变，无新表或二次总结调用。未知作者例外仍限单条逐字引用，不为了归纳而推断身份。

历史 v5 升级时使 v4 旧结果过期；当前业务自动分析覆盖完整有效快照，没有批量手动重跑或修改既有结果。合成 SDK 检查一次请求包含全部四条消息、综合段落保留多条引用；另检查两段消息摘要被拒绝。机械校验仅约束结构及引用，不能证明语义归纳必然正确。

便携包由 PythonExecutable 优先选择 runtime/python 下的平台解释器，源代码回退 python/.venv/bin/python 或 Windows Scripts/python.exe。PYTHONPATH 仅指向本地 python/packages，禁用用户 site-packages；Windows 另白名单继承 SystemRoot/WINDIR/TEMP/TMP，PATH 仅解释器目录和系统目录。不继承父 PYTHONPATH/PYTHONHOME 或凭据。测试仍通过真实子进程校验代理和环境隔离。

CheckPython 与 Bub 共用最小进程环境，离线导入真实入口及 SDK，配置密钥不传给该检查；失败返回安全错误，30 秒超时，不回显 Python traceback。

Issue #2：CheckPython 调用固定入口 `--check`，通过实际 HTTPX2 transport 构造检查 SOCKS/代理依赖，解析有界 JSON，仅映射已知错误码到安全恢复提示，不读取凭据或发送请求。Bub 输出白名单增加 proxy_dependency_missing/proxy_configuration_invalid/python_environment_invalid；原始异常仍不回显。离线预检使用与总结相同的代理环境。

Issue #4：引用校验失败返回白名单 section、从 0 开始的 item 索引及 message_ids 路径，不含正文/实际消息 ID。Python model_validator 以 ContractIssue 携带本地路径；Go 仅接受三个已知栏目、0..9 整数及固定字段，非法路径省略。界面已有错误文本可显示安全路径，真实性校验不放宽。Prompt 明确 JSON object 不保证引用，无法给出有效来源的事实必须省略，不编造 ID、不自动补引用；无新增内部模型纠错调用、依赖、Schema 升级或旧结果失效。合成测试覆盖缺/未知/角色错误引用、字段证据豁免、准确路径与恶意路径不泄漏。
