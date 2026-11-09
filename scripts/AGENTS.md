一旦我所属的文件夹有所变化请更新我。

负责便携包构建、离线/浏览器验收和解压后启动。
读取锁文件及构建资源，调用 Go/uv；仅构建阶段需要开发工具。
不读取用户 .env、不采集工单、不自动部署。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 打包和验证边界 |
| package.py | 原生打包 | 携带 Python 总结/知识/文档/离线实验入口、依赖、前端及 Go，生成 SHA256 |
| check_ai_ui.py | 浏览器回归 | 合成 API 验证逐票轮询、隐藏/竞争及恢复确认 |
| check_knowledge_ui.py | 知识回归 | 具名库/分类、批量审核、Markdown 图文编辑、版本隔离、建议引用与移动端 |
| check_detail_ui.py | 详情回归 | 引用折叠/键盘/安全与静默、延迟、失败、断网、导航竞争 |
| smoke_package.py | 离线验收 | 异地解压后的 SDK/CLI/SQLite/日志检查 |
| test_smoke_package.py | 打包回归 | 单/双架构标题及各架构外部依赖校验 |
| start.cmd | Windows 入口 | 首用模板和检查后启动，失败停留窗口 |
| start.command | macOS/Linux 入口 | 工作目录定位、首用模板和服务启动 |

package.py 仅复制显式允许的文件和新建 runtime/packages，禁止整仓库打包；从 uv 管理的 CPython 3.12.14 复制独立发行版，用 uv.lock 导出带 Hash 的 requirements，安装对应平台依赖到 python/packages（优先二进制 wheels）。macOS 使用 tar.gz 保留可执行权限/链接，Windows ZIP；包内 BUILD.json 记录源提交和平台。无用户 .env/data/logs，前端必须已构建。Linux 同脚本用于本地离线验收，不列入本次下载矩阵。

知识功能同时显式复制 python/main.py、knowledge.py、documents.py 及依赖/锁文件；新增 pypdf/python-docx/lxml 由原有锁文件导出流程带入，不把用户导入文档或知识数据打包。总结与知识任务共用一个随附 Python 环境，不要求下载用户安装 Office 或解析工具。

解压包有自己的 Python，不包含不可移动的 .venv；测试将归档解到带空格的新目录，实际导入 SDK/原生 wheels，执行 Go check 验证配置/SQLite/操作锁/日志，不访问外部业务服务。uv 只在构建时使用，终端用户不用安装工具链。首次启动生成模板但不覆盖已存在的 .env。配置与运行数据保留在解压目录，需要写权限；升级说明见 docs/downloads.md。

锁定 cryptography 50.0.2 无 Intel macOS wheel；该平台从已校验 Hash 的源码构建，强制 OPENSSL_STATIC=1 且不复用缓存扩展。Rust/Clang/Homebrew OpenSSL 仅在 CI 构建机需要，包不依赖用户 Homebrew。macOS smoke 用 otool 检查 SDK 原生扩展，不允许非系统绝对动态库路径，随后实际 SDK 导入验证。

macOS 原生库检查显式遍历全部架构，只解析 otool 缩进的依赖行，跳过每段架构标题；不能把 universal2 的第二段文件路径当成依赖，也不能仅检查第一架构。标准库合成回归覆盖单架构、双架构及任一架构存在 Homebrew 绝对依赖，纳入 make test 和三平台 CI；实际库仍由原生解压验收检查。

Issue #2：便携包验收通过 Go check 的真实隔离 Python 检查大小写 ALL_PROXY、仅 SOCKS 与 HTTP(S) 共存；临时移开解压副本的 socksio 验证安全失败，再恢复验证成功。所有代理地址为本机合成地址，不发送请求；不只依赖 SDK import 成功。所有平台共用 uv.lock 导出，因此都会携带 SOCKS 可选依赖。

check_ai_ui.py 自行启动仅含 web/dist 的临时静态服务器，拦截全部 API 使用合成数据，不读取配置或 SQLite、不调用 Zoho/模型。先 pnpm --dir web build，再 uv run --no-project --with playwright python scripts/check_ai_ui.py；需已有 Playwright Chromium（可用 PLAYWRIGHT_CHROMIUM_EXECUTABLE 指定本机路径）。AI_UI_SCREENSHOT 可选写截图到测试临时位置。验证全局统计不变时逐票状态更新、草稿/已应用筛选/滚动保持、隐藏暂停与恢复、慢请求不重叠/不覆盖新查询、恢复取消零 POST 和确认固定运行 ID。

check_detail_ui.py 同样只访问临时静态资源和合成 API，不读取配置/数据；运行方式与 check_ai_ui.py 相同，可通过 DETAIL_UI_SCREENSHOT 保存截图。验证嵌套引用默认折叠、键盘展开、消息证据跳转展开全部引用、刷新保留展开；手动/静默刷新高度稳定，延迟请求覆盖完整加载反馈、重复点击不重发，500/断网保留成功内容及导航旧响应不覆盖新票。

check_knowledge_ui.py 使用相同的临时静态服务与合成 API，覆盖知识库重命名/多部门绑定、分类新增、批量文件预览、风险默认不选与全选后仍需确认、保存草稿、带版本引用的混合检索、Markdown 表格/图片渲染和危险 HTML/链接过滤、受保护图片上传、可视/源码切换与新草稿保留当前正式版。保留建议跳到来源块/工单消息、返回建议和过期隐藏断言，并明确点击发布才替代正式版。桌面和 390px 验证无溢出/脚本错误，所有浏览器请求仅限临时服务。运行前先构建 web/dist，再执行 `uv run --no-project --with playwright python scripts/check_knowledge_ui.py`；可用 PLAYWRIGHT_CHROMIUM_EXECUTABLE 指定已有浏览器。截图写忽略的 .cache/knowledge-ui，不读取真实配置、工单或知识资料。沿用一份脚本，没有新增测试框架或运行时依赖。

知识增量：smoke_package 在异地解压目录实际调用 documents.py 解析合成 Markdown，并导入 knowledge.py 校验无工具建议 Schema，不访问模型；同时继续验证 native SDK 库、代理和本机启动。

离线 FAQ 增量：打包白名单包含 `python/lab.py`，复用原有锁定依赖；异地解压验收实际导入 lab 并校验空修订草稿 Schema，零模型调用。不携带批次、评分、候选回复或任何用户知识资料。

| check_lab_ui.py | 合成浏览器验收 | 批次创建、手动启动、评分轮询保留、草稿与移动端 |

| build_embedding.py | 原生推理构建 | 固定稳定 llama.cpp 源码和 SHA256，CPU 静态构建 |

本地检索：build_embedding.py按固定SHA256验证官方稳定llama.cpp v0.9.0源码，只构建CPU静态llama-server（禁GPU/OpenSSL/OpenMP），复制许可证和版本；runtime为忽略的第三方构建产物。package.py复制runtime/llama，不带模型权重、知识或用户配置。模型首次使用在应用内显式下载。CMake和C++仅构建机需要。

包级界面验收：`VERIFY_PACKAGE_UI=1`使smoke在解压后的web/dist上运行既有浏览器脚本（通过KNOWLEDGE_UI_DIST），不只验证源码构建目录。CI用PyPI核实的Playwright1.64.1及其固定Chromium，属于测试工具、不进入运行包。
