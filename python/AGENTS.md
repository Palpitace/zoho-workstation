一旦我所属的文件夹有所变化请更新我。

本目录负责固定 Python stdin/stdout 总结入口与锁定的 Bub Core 环境。
模块关系：Go 发送完整脱敏会话和可选同版本 Jev 参考，本目录返回校验后的 JSON，Go 再次校验并持久化。
不负责数据库、Zoho、工具执行、插件、持久会话记忆、Web 服务或企业微信。

## 协议和运行

输入只有 `{input:<完整脱敏会话及可选参考>,config:{api_base,api_key,model},schema_version:"bub-summary-v2"}`。
input 保持 messages、主题、描述等字段平面结构，必有 `conversation_hash`（附参考之前的会话版本）和 `jev_reference`；无可用评估时参考显式为 null，不阻止独立总结。
非空参考只允许 `{kind:"model_assessment",run_id,conversation_hash,schema_version,requested_model,returned_model,choices:{字段:标签字符串}}`，不接收额外自由文本或概率字段。参考 Hash 必须与 input.conversation_hash 相同，不一致返回安全诊断 `reference_version_mismatch`；Go 负责筛选当前有效评估。
完整会话与独立参考一同传入无工具 SDK，参考不变成消息或事实，不进入可引用消息 ID 集。Prompt 要求原文优先、缺少证据时承认未知，不能因 Jev 参考推定客户已执行或缺陷已确认，也不让 Bub 重新执行 Jev 评估。
配置不从 `.env` 或其他文件加载；模型必须是 `deepseek:模型名`，地址与 Key 显式传入，拒绝空值以避免 SDK 环境回退。
成功输出 `{ok:true,result,returned_model,request_id,usage}`；失败输出 `{ok:false,error:<固定错误码>,diagnostics?:[{loc,type,code}]}` 并以非零状态退出。
已经取得供应商 Response 时，即使本地结构、引用或截断校验失败，错误输出也保留 `returned_model`、`request_id` 和 `usage`，不包含原文；认证/网络失败尚无 Response 时这些信息未知，不用请求模型补造。
stdout 只输出一份 JSON；stderr 仅输出不含正文和凭据的错误码。Go 必须使用固定解释器/入口、stdin 和外层进程超时，不拼接 Shell。
Go 子进程允许继承 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY、NO_PROXY 的大小写形式；本入口不读取其他父进程配置，也不记录代理值。锁定 Republic 的 APITimeoutError 对应 model_timeout，APIConnectionError 对应 model_connection_failed，APIStatusError/APIResponseError 对应 model_upstream_failed；其他 RepublicError 仍为 model_request_failed。HTTP 状态、响应正文、异常 cause/repr 和代理认证信息均不输出。SDK Mock 用连接异常、连接超时和 HTTP 503 覆盖分类及单次请求约束；真实子进程模拟代理检查位于 internal/analysis/bub_test.go。
诊断最多 16 项，每项 loc 最多 8 层，只保留已知结构字段及有界索引；任意额外字段名统一为 `unknown_field`。
type/code 都来自本地白名单，不输出 Pydantic input、msg、ctx、模型正文、HTTP 响应或异常 repr；可区分未知引用、缺少引用、执行/结果冲突、截断、空白和结构错误。
过去只返回 `invalid_summary` 且原文已丢弃的请求不能追溯具体原因；新增诊断只用于后续获准请求，不自动重试或扩大预算。

从仓库根目录执行：

```sh
uv sync --project python --locked --no-dev
python/.venv/bin/python python/main.py
uv run --project python --no-sync python -m unittest discover -s python -p 'test_*.py'
```

入口等待 stdin JSON；不要把真实凭据写在命令行。真实运行需要 Go 服务和本目录随附 Python 环境。
Python 本身设置 75 秒总超时，Republic HTTP 超时 60 秒。并发与研发调用预算由 Go 控制。

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
固定结构区分建议、客户确认执行和客户报告结果；客户结果必须对应已确认执行。混合作者转录的真实归属仍由模型依据正文判断。
本地验证只证明枚举、引用和部分角色约束，不证明模型没有事实错误。没有真实模型验证时不能声称真实接入已完成。

已执行验证：合成契约测试通过；仅 PATH 的最小环境下 SDK 导入和构造成功，不依赖 HOME 或额外 CA 环境变量；非法 stdin 只返回固定错误码。
使用安装后的 Bub/Republic 与本地 HTTPX2 MockTransport 完成一次流式验证：只请求一次、没有 tools、使用 JSON 模式，并保留返回模型、request_id 和用量；未调用外部模型。
安全诊断验证覆盖非法引用、客户执行证据缺失、执行/结果冲突，以及任意额外字段名和输入值不回显；另以 SDK Mock 流确认截断只产生固定 `output_truncated` 码。
第二个合成 SDK 边界测试覆盖结构、引用及截断失败后仍保留实际返回模型、请求 ID 和用量，同时排除原文和 Key；使用 MockTransport，不调用外部模型。
v2 增量先观察旧 schema 拒绝新请求，再观察旧输入模型错误接受缺失参考/基础 Hash、不同会话版本、额外自由文本和概率对象；实现后全部通过。第三个合成测试在真实 Bub/Republic + MockTransport 边界验证有/无参考均可总结、全部会话字段及独立参考完整传递、无工具，以及把参考 run_id 当 message_ids 会失败。结构验证和固定 Mock 输出不能证明真实模型文字正确，未因此增加真实模型调用。
简化实验：移除仅转发超时调用的包装函数，直接使用 `asyncio.wait_for`，合成测试和 SDK Mock 流验证通过。当前保留 Pydantic 模型是为了共享严格字段与引用约束。
v2 设计简化检查：继续复用已有消息引用校验和单份 input 序列化，只增加参考结构和版本校验；不建立另一套证据集合、摘要输入转换层或 SDK 包装器。本次为设计检查，没有宣称新的实际消融实验。

## 直接文件索引

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 协议、运行、版本与能力边界 |
| main.py | 固定入口 | 完整会话与可选参考、SDK、消息引用校验及网络错误安全分类 |
| test_main.py | 合成契约验证 | 会话完整性、参考隔离、状态、安全诊断与网络失败分类 |
| pyproject.toml | 依赖声明 | 稳定版本和 Python 兼容范围 |
| uv.lock | 自动生成锁文件 | 固定全部解析依赖及校验信息 |
| .venv/ | 忽略的安装产物 | 本机 Python 解释器与依赖 |
| __pycache__/ | 忽略的缓存 | Python 导入与测试缓存 |
