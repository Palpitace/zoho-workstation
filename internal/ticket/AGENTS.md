一旦我所属的文件夹有所变化请更新我。

本目录把 Zoho 原始响应转换为可展示的工单快照，并生成供 AI 使用的脱敏副本。
同步模块提供 RawSnapshot、保留原始 Evidence；持久化/API/AI 消费 Snapshot，模型输入 Hash 由调用方按语义字段计算。
不负责网络调用、数据库访问、授权范围、AI 推断或保证完全匿名。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 数据边界、清洗及脱敏限制 |
| normalize.go | 转换入口和数据模型 | Normalize、RawSnapshot、Snapshot、Message、Attachment |
| normalize_test.go | 合成边界验证 | 精确 ID、消息顺序/去重、事实与角色、草稿状态、不完整证据 |
| redact.go | 模型输入转换 | RedactedSnapshot，替换个人信息并移除附件 URL |
| redact_test.go | 合成脱敏验证 | 正文/签名/引用脱敏、保留事实、原快照不变 |

## 调用契约

- `Normalize(RawSnapshot) (Snapshot, error)` 使用 `json.Number` 接受 OAS 中的字符串或整数 ID，不通过 float64 转换。未知字段兼容保留在调用方的原始证据中，不强制匹配整份第三方 Schema。
- RawSnapshot.Threads 应为线程详情，Comments 应为备注详情；采集者把已验证的分页完整性传入 Complete。线程摘要不是正文，不用 summary 补 content。原始 GET 响应存放在 RawSnapshot.Evidence，由采集者维护；本包不修改或解析 Evidence。
- 时间转换为 UTC RFC3339Nano。缺失/非法时间转为空值并标记不完整，原时间仍可在原始证据中追溯。已知时间按真实时间稳定排序，未知时间排最后。同一 source+ID 去重，重复项采用后出现的对象；缺失 ID 不去重并标记不完整。
- Message.Source 为 thread/comment；Role 仅依据 author.type/commenter.type 的 END_USER/AGENT 映射为 customer/agent，其余 unknown。不以邮件方向或备注私密性推断作者。
- 实测 ONLINE_CHAT 线程可包含整段双方聊天及访客信息；不把整段归于线程 author，标记 MixedAuthors=true、Role=unknown，保留全文和原始证据。不猜测聊天内的发言者，也不因此声称内容缺页。
- 可见性为 public/private/unknown。保留线程 Status、Direction、IsDescriptionThread 及可选 ModifiedTime，DRAFT/FAILED/PENDING 不能被下游当作已发送；描述线程不能被当成第二份独立事实。
- HTML 标签转可读文本，保留段落、换行、引文、签名、否定词及图片 alt 文本，不执行脚本、不读取链接、不下载附件。附件仅保留元数据。截断、缺正文、缺 ID、缺时间、附件元数据缺失会显式标记不完整。

## 脱敏方法与限制

在线聊天导出中的单独行字段 Name、电话/地址、IP、Latitude/Longitude、City、State-Province-Region、Chat ID 的下一行值也会遮盖；保留问题内容和设备/系统版本。此规则不推断自由文本地址。
模型副本额外解码一层 HTML 实体，遮盖导出 JSON 中 sn/serial/serialNumber/mac/imei 值；产品型号与固件版本保留。原始和规范化证据不因此改变。

`RedactedSnapshot` 返回独立副本，不修改规范化原件。识别联系人、客服和作者结构中的姓名、邮件、电话、街道/地址，以及邮件头中的显示姓名；按完整词边界替换已知信息，尽量避免删掉产品名中的相同字母。再检查正文、签名和引用中的邮箱、带标识或格式明确的电话号码、明确标识的姓名/地址、邮箱旁的显示姓名。附件文件名同样脱敏，附件 URL 从模型副本移除。消息 ID 和作者角色保留用于事实引用。

裸露的未知姓名、没有标识的地址、非常见电话号码、别名/拼写变体和编码内容可能漏检；标签识别也可能误遮盖字段值。短名字与产品/故障术语重合时不能完全消除误判。自动脱敏不保证完全匿名，真实样本应在本地抽查，禁止把原始包作为模型输入。根目录约定的脱敏副本只存 `data/`，不得提交真实测试样本。

测试使用合成内容，通过 `go test ./internal/ticket` 验证。测试只观察公开 Normalize/RedactedSnapshot 边界，不重复验证第三方 HTML 库内部行为。设计简化检查：只保留两个业务转换入口及必要的数据类型，不增加清洗策略接口、可配置规则引擎或重复传输模型。

字段依据：固定 Submodule 的 `docs/zoho-oas/v1.0/Ticket.json`、`Thread.json`、`TicketComment.json`、`Contact.json`。动态字段差异由采集阶段记录，不修改 Submodule。
