一旦我所属的文件夹有所变化请更新我。

负责模型输入最小化和独立结构化分析能力。
接收 ticket 快照，向 Go 编排返回 JSON；结果由 store 保存。
不访问数据库、不修改 Zoho、不发送企业微信消息。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 输入和外部分析边界 |
| input.go | 共享输入 | 脱敏、排除展示字段、计算内容 Hash |
| input_test.go | 行为测试 | 保留否定事实、脱敏和语义失效 |
| jev.go | TypeSafe 接口 | 单次 14 个独立 Choice 问题、枚举校验及安全错误 |
| jev_test.go | 协议测试 | 正常、未知、非法枚举及失败响应 |
| bub.go | Python 边界 | 固定入口、最小环境、超时及结构/引用二次校验 |
| bub_test.go | 摘要契约测试 | 建议与执行字段、非法引用及缺字段拒绝 |

Bub 使用 python/.venv/bin/python 与 python/main.py 固定路径，无 Shell 拼接。stdin 仅传共享脱敏输入、Bub 专用模型配置及 Schema；子进程环境仅 PATH/编码/不写缓存，不继承 Zoho/Jev 凭据。90 秒上限、stdout 1 MiB 上限、stderr 不回显；失败不清已有结果。结构校验能拒绝未知引用与矛盾执行状态，不能证明模型文字事实正确。

Jev Schema 为 jev-assessment-v1。使用官方 POST systemone 的 model/state/questions、Bearer 认证及 model/answers/usage 响应；来源 https://docs.typesafe.ai/api/system-one 和 https://api.typesafe.ai/openapi.json（2026-11-04 核实）。全部问题独立提供 instructions，不依赖问题键被模型读取。每项显式 unknown；诉求为六个可同时成立的标签。保留供应商 confidence/probabilities，但不声称业务校准或构造加权分数。情绪仅依据近期客户消息，草稿及客服建议不等于客户执行。Jev state 暂限 24 KiB，这是保守工程限制而非精确 token 上限；超限显式失败，不截断。

Jev 与 Bub 接收同一份 JSON，不以总结代替评估输入。Hash 包含消息事实、角色、可见性、发送状态、时间及附件元数据，排除工单状态/优先级/编辑时间和部门。超过 512 KiB 拒绝发送，不静默截断。自动脱敏有局限，沿用 ticket/AGENTS.md；真实发送前在本地 data/ 留副本并检查。
