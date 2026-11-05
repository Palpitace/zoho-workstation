一旦我所属的文件夹有所变化请更新我。

本目录负责固定 Python stdin/stdout 总结入口与锁定的 Bub Core 环境。
模块关系：Go 发送完整脱敏会话和可选同版本 Jev 参考，本目录返回校验后的 JSON，Go 再次校验并持久化。
不负责数据库、Zoho、工具执行、插件、持久会话记忆、Web 服务或企业微信。

## 协议和运行

输入只有 `{input:<完整脱敏会话及可选参考>,config:{api_base,api_key,model},schema_version:"bub-summary-v5"}`。
input 保持 messages、主题、描述等字段平面结构，必有 `conversation_hash`（附参考之前的会话版本）和 `jev_reference`；无可用评估时参考显式为 null，不阻止独立总结。
非空参考只允许 `{kind:"model_assessment",run_id,conversation_hash,schema_version,requested_model,returned_model,choices:{字段:标签字符串}}`，不接收额外自由文本或概率字段。参考 Hash 必须与 input.conversation_hash 相同，不一致返回安全诊断 `reference_version_mismatch`；Go 负责筛选当前有效评估。
完整会话与独立参考一同传入无工具 SDK，参考不变成消息或事实，不进入可引用消息 ID 集。Prompt 要求原文优先、缺少证据时承认未知，不能因 Jev 参考推定客户已执行或缺陷已确认，也不让 Bub 重新执行 Jev 评估。
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

验证：`python/.venv/bin/python -m unittest discover -s python -p 'test_*.py'` 使用合成契约及真实 Bub/Republic+HTTPX2 MockTransport。v3 可空分类、字段证据、角色/公开已发约束先在旧代码失败，再验证通过；保留无工具、一次请求、完整会话/可选参考、未知引用及安全诊断、截断失败仍保留元信息、网络错误分类等既有边界。固定 Mock 文字不代表真实模型业务准确率。Go 另进行同契约二次验证并验证真实本地代理子进程，不访问外部模型。

简化：所有新分类共用 Fact，删除旧 Measure/NextStep 模型和仅服务于旧输出的强制建议/执行枚举逻辑。保留 Pydantic 与固定入口、既有字段/错误白名单，无新 SDK、工具或迁移模块。

## 直接文件索引

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 协议、运行、版本与能力边界 |
| main.py | 固定入口 | 完整会话与可选参考、SDK、三类可空事实与来源校验及网络错误分类 |
| test_main.py | 合成契约验证 | 可空分类、角色/字段证据、完整会话、参考及安全诊断 |
| pyproject.toml | 依赖声明 | 稳定版本和 Python 兼容范围 |
| uv.lock | 自动生成锁文件 | 固定全部解析依赖及校验信息 |
| .venv/ | 忽略的安装产物 | 本机 Python 解释器与依赖 |
| __pycache__/ | 忽略的缓存 | Python 导入与测试缓存 |

v4 客户反馈例外：只有单条引用，role=unknown、source=thread、direction=in、visibility=public、status=SUCCESS 且 text 为 body 连续逐字片段时允许保存。不是将 role 改成 customer，也不是任意未知作者或客服转述豁免；页面标注来信原文/作者未确认。Prompt 要求完整保留否定/条件，不翻译改写，客服回复缺失时 support_responses=[]。Go/Python 同步校验；Schema 升级使旧失败版本不再挡住新版本重试，旧结果继续保留。单封正常客户来信也不要求有客服回复。测试先在旧校验拒绝未知作者原文，再在新校验通过，同时拒绝改写、客服、内部、出站和草稿证据。实际只调用 1 次配置的 Bub 模型，使用合成单封未知作者来信，返回 1 条原文反馈、0 条客服回复；不代表所有实际模型输出都能通过。

2026-11-05 整单汇总 v5：仍一次发送完整会话。device_info 最多 10 项；customer_feedback/support_responses 各最多 1 项（无内容为空），text 综合同一工单的主要问题、实际处理进展与当前状态，message_ids 可覆盖多条证据。按主题合并重复内容，禁止逐线程罗列，保持建议/执行/结果区分，不新增评估或下一步建议。Python/Go 双重校验数量，结构形状不变，无新表或二次总结调用。未知作者例外仍限单条逐字引用，不为了归纳而推断身份。

Schema 升级使 v4 旧结果过期，新自动运行仍限原开发集；没有批量手动重跑或修改既有结果。合成 SDK 检查一次请求包含全部四条消息、综合段落保留多条引用；另检查两段消息摘要被拒绝。机械校验仅约束结构及引用，不能证明语义归纳必然正确。
