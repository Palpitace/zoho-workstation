一旦我所属的文件夹有所变化请更新我。

本目录负责固定 Python stdin/stdout 总结入口与锁定的 Bub Core 环境。
模块关系：Go 发送完整脱敏会话，本目录返回校验后的 JSON，Go 再次校验并持久化。
不负责数据库、Zoho、工具执行、插件、持久会话记忆、Web 服务或企业微信。

## 协议和运行

输入只有 `{input:<完整脱敏会话>,config:{api_base,api_key,model},schema_version:"bub-summary-v6"}`。
input 保持 messages、主题、描述等字段平面结构，必有 conversation_hash。不再接收 Jev 参考，完整会话独立传入无工具 SDK；总结完成后由 Go 再提供给 Jev。
Prompt 保留客户明确诉求、风险报告及近期情绪的明确表达，但不自行评估情绪标签、优先级或风险等级；总结仍需消息引用支撑，建议不等于执行。
配置不从 `.env` 或其他文件加载；模型必须是 `deepseek:模型名`，地址与 Key 显式传入，拒绝空值以避免 SDK 环境回退。
成功输出 `{ok:true,result,returned_model,request_id,usage}`；失败输出 `{ok:false,error:<固定错误码>,diagnostics?:[{loc,type,code}]}` 并以非零状态退出。
已经取得供应商 Response 时，即使本地结构、引用或截断校验失败，错误输出也保留 `returned_model`、`request_id` 和 `usage`，不包含原文；认证/网络失败尚无 Response 时这些信息未知，不用请求模型补造。
stdout 只输出一份 JSON；stderr 仅输出不含正文和凭据的错误码。Go 必须使用固定解释器/入口、stdin 和外层进程超时，不拼接 Shell。
Go 子进程允许继承 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY、NO_PROXY 的大小写形式；本入口不读取其他父进程配置，也不记录代理值。锁定 Republic 的 APITimeoutError 对应 model_timeout，APIConnectionError 对应 model_connection_failed，APIStatusError/APIResponseError 对应 model_upstream_failed；其他 RepublicError 仍为 model_request_failed。HTTP 状态、响应正文、异常 cause/repr 和代理认证信息均不输出。SDK Mock 用连接异常、连接超时和 HTTP 503 覆盖分类及单次请求约束；真实子进程模拟代理检查位于 internal/analysis/bub_test.go。
诊断最多 16 项，每项 loc 最多 8 层，只保留已知结构字段及有界索引；任意额外字段名统一为 `unknown_field`。
type/code 都来自本地白名单，不输出 Pydantic input、msg、ctx、模型正文、HTTP 响应或异常 repr；可区分未知引用、缺少引用、客户/客服来源不符、截断、空白和结构错误。
过去只返回 `invalid_summary` 且原文已丢弃的请求不能追溯具体原因；新增诊断只用于后续获准请求，不隐式重试。

从仓库根目录执行：

```sh
uv sync --project python --locked --no-dev
python/.venv/bin/python python/main.py
uv run --project python --no-sync python -m unittest discover -s python -p 'test_*.py'
```

入口等待 stdin JSON；不要把真实凭据写在命令行。真实运行需要 Go 服务和本目录随附 Python 环境。
Python 本身设置 75 秒总超时，Republic HTTP 超时 60 秒。并发、调用计数和有限失败重试由 Go 控制，累计请求额度已由用户关闭。

## SDK 与结果边界

2026-11-04 核实 [Bub 0.6.1](https://pypi.org/project/bub/0.6.1/)、[Republic 0.6.1](https://pypi.org/project/republic/0.6.1/) 发布 wheel 源码；Python 需要 3.12+，当前验证解释器为 3.13.5。
Pydantic 2.14.2 复用 SDK 已使用的依赖，并作为本入口直接依赖声明；所有传递依赖以 `uv.lock` 为准，不默认升级。
仅 `bub.core.Agent`：`tools=()`、`sidecars=()`、`command_prefix=None`，不加载 App 配置、插件或遥测 exporter。
默认内存 Tape 是本次处理的临时状态；每个请求新建 Agent、`merge_back=False`、Republic `history=None`，不跨工单复用或落盘。
`max_steps=1` 限制 Bub 上下文溢出 handoff 不能再调用模型；`max_retries=0` 关闭 Republic HTTP 重试。Go 统一控制重试并计费计数。
Republic 的公开 middleware 读取 `Completed.response`，保留供应商实际返回的 model、request_id 和 token_usage；缺失 model 不用请求模型冒充。
接口依据：[Republic 配置](https://getrepublic.org/reference/configuration/)、[Provider API](https://getrepublic.org/reference/provider-api/)。

[DeepSeek JSON 模式](https://api-docs.deepseek.com/guides/json_mode/)使用 `response_format.type=json_object`，只保证 JSON 格式，不保证字段或事实正确。
Republic `output_schema` 在 Chat 格式下会生成 `json_schema`，当前 DeepSeek Chat 文档未声明支持，故本入口使用 JSON 模式及 Pydantic 本地校验。
空白、截断、拒绝、工具输出、结构错误和不存在的引用均失败，不保存为成功总结；不因失败删除 Go 中的旧结果。
v5 结果只有 device_info、customer_feedback、support_responses 三个必有数组，设备信息最多 10 项、客户反馈和客服回复各最多 1 项，统一 `{text,message_ids}`。无证据则数组为空，不强制“未知”等占位，不新增模型建议。设备信息、客户实际反馈、客服已回复/给予排查分别归类；Prompt 明确建议不能当已执行，客户确认及实际结果必须基于客户原话。
客户反馈至少有 customer 或 mixed_authors 消息引用；客服回复至少有 agent 或 mixed_authors，且其引用不允许 private 或 DRAFT/FAILED/PENDING 消息。模型仍负责混合转录逐句归属，不根据外层角色猜测执行者。
设备信息可引用消息；仅当 text 逐字等于 input.product 或为 input.description 的逐字片段时，允许 message_ids=[] 表示工单字段证据。Prompt 禁止设备推断、截断否定/条件或拼接改变含义；没有消息或明确设备字段时三个数组全空。
本地验证只证明结构、引用、字面字段及部分角色约束，不能证明设备分类、建议与执行等自由文本事实准确。旧 v1/v2 结果保留历史，不自动映射新分类；新版 Schema 使旧结果过期。没有真实模型验证时不能声称本次新格式已真实验证。

验证：`python/.venv/bin/python -m unittest discover -s python -p 'test_*.py'` 使用合成契约及真实 Bub/Republic+HTTPX2 MockTransport。v3 可空分类、字段证据、角色/公开已发约束先在旧代码失败，再验证通过；保留无工具、一次请求、完整会话、未知引用及安全诊断、截断失败仍保留元信息、网络错误分类等既有边界。固定 Mock 文字不代表真实模型业务准确率。Go 另进行同契约二次验证并验证真实本地代理子进程，不访问外部模型。

简化：所有新分类共用 Fact，删除旧 Measure/NextStep 模型和仅服务于旧输出的强制建议/执行枚举逻辑。保留 Pydantic 与固定入口、既有字段/错误白名单，无新 SDK、工具或迁移模块。

## 直接文件索引

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 协议、运行、版本与能力边界 |
| main.py | 固定入口 | 完整会话、SDK、三类可空事实与来源校验及网络错误分类 |
| test_main.py | 合成契约验证 | 可空分类、角色/字段证据、完整会话及安全诊断 |
| pyproject.toml | 依赖声明 | 稳定版本和 Python 兼容范围 |
| uv.lock | 自动生成锁文件 | 固定全部解析依赖及校验信息 |
| .venv/ | 忽略的安装产物 | 本机 Python 解释器与依赖 |
| __pycache__/ | 忽略的缓存 | Python 导入与测试缓存 |

v4 客户反馈例外：只有单条引用，role=unknown、source=thread、direction=in、visibility=public、status=SUCCESS 且 text 为 body 连续逐字片段时允许保存。不是将 role 改成 customer，也不是任意未知作者或客服转述豁免；页面标注来信原文/作者未确认。Prompt 要求完整保留否定/条件，不翻译改写，客服回复缺失时 support_responses=[]。Go/Python 同步校验；Schema 升级使旧失败版本不再挡住新版本重试，旧结果继续保留。单封正常客户来信也不要求有客服回复。测试先在旧校验拒绝未知作者原文，再在新校验通过，同时拒绝改写、客服、内部、出站和草稿证据。实际只调用 1 次配置的 Bub 模型，使用合成单封未知作者来信，返回 1 条原文反馈、0 条客服回复；不代表所有实际模型输出都能通过。

2026-11-05 整单汇总 v5：仍一次发送完整会话。device_info 最多 10 项；customer_feedback/support_responses 各最多 1 项（无内容为空），text 综合同一工单的主要问题、实际处理进展与当前状态，message_ids 可覆盖多条证据。按主题合并重复内容，禁止逐线程罗列，保持建议/执行/结果区分，不新增评估或下一步建议。Python/Go 双重校验数量，结构形状不变，无新表或二次总结调用。未知作者例外仍限单条逐字引用，不为了归纳而推断身份。

Schema 升级使 v4 旧结果过期，新自动运行仍限原开发集；没有批量手动重跑或修改既有结果。合成 SDK 检查一次请求包含全部四条消息、综合段落保留多条引用；另检查两段消息摘要被拒绝。机械校验仅约束结构及引用，不能证明语义归纳必然正确。

便携包由 scripts/package.py 将独立 CPython 及 uv.lock 依赖装入 runtime/python 和 python/packages，无需用户安装 Python/uv。Go 固定 PYTHONPATH 并禁止用户 site-packages；Windows 仅增加运行所需系统/临时目录环境，不继承密钥。开发仍使用 .venv。

## SOCKS 与离线预检（Issue #2）

显式锁定 httpx2[socks]==2.16.0，沿用 SDK 的同一 HTTP 客户端，新增 socksio==1.0.0，不升级其他依赖。依据已安装 wheel 的 Provides-Extra/Requires-Dist 核实 socks extra；uv.lock 固定 Hash。`main.py --check` 不读 stdin/凭据、不发送网络请求，实际构造并关闭 AsyncClient，覆盖继承代理的 transports。正常总结也先检查环境；缺 SOCKS 依赖、非法代理配置、本地证书/环境问题分别返回 proxy_dependency_missing、proxy_configuration_invalid、python_environment_invalid，不打印异常/代理 URL。测试覆盖大小写 ALL_PROXY、仅 SOCKS、与 HTTP(S) 共存、socks5/socks5h、缺模块以及 CLI 安全错误。

Issue #4：引用校验失败返回白名单 section、从 0 开始的 item 索引及 message_ids 路径，不含正文/实际消息 ID。Python model_validator 以 ContractIssue 携带本地路径；Go 仅接受三个已知栏目、0..9 整数及固定字段，非法路径省略。界面已有错误文本可显示安全路径，真实性校验不放宽。Prompt 明确 JSON object 不保证引用，无法给出有效来源的事实必须省略，不编造 ID、不自动补引用；无新增内部模型纠错调用、依赖、Schema 升级或旧结果失效。合成测试覆盖缺/未知/角色错误引用、字段证据豁免、准确路径与恶意路径不泄漏。

设备信息可选（2026-11-05 用户修正）：缺 device_info 字段默认空数组；设备条目无有效消息引用且不是产品/描述原文，或引用未知 ID 时，仅省略该设备条目，继续校验并返回客户反馈/客服处理。保留有依据设备条目；不补猜 ID、不增加模型请求。客户反馈/客服处理的缺引用、角色等规则仍严格。最终输出始终包含 device_info 数组，Go 对规范化后的输出继续严格复核。无 Schema 升级、旧结果不强制失效、未修改原会话。
