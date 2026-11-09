一旦我所属的文件夹有所变化请更新我。

负责 Windows/macOS 原生构建与产物上传。
调用项目测试及 scripts/package.py、smoke_package.py。
不调用真实 Zoho/模型、不发布生产或自动创建 Release。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | CI 触发与固定工具链 |
| build.yml | 自动构建 | main/PR/手动触发三平台验证和下载包 |

固定 Actions 提交 SHA；Go 版本读取 go.mod，pnpm 12.17.0 与 web/package.json 一致，Node 26.10.0、uv 0.13.7、CPython 3.12.14 固定。Windows Server 2025 x64、macOS 15 Intel/ARM 原生测试，运行完整小型测试、构建、异地解压验收，再上传产物保留 30 天。仅 contents:read，不使用业务凭据。CI 成功才视为相应操作系统验证通过。

Intel Mac 在安装依赖前确认 runner 的 Rust、Clang 与 OpenSSL 静态库；cryptography 50.0.2 没有 Intel wheel，打包阶段从锁定源码静态链接 OpenSSL，不升级/降级运行时依赖。macOS smoke 拒绝指向构建机的 SDK 动态库依赖。

测试阶段同时运行 scripts/test_*.py 的标准库打包回归，覆盖 universal2 多架构标题解析与任一架构的外部绝对依赖拒绝；随后仍对实际下载包执行原生解压验收。

下载构建新增固定CPU llama.cpp、实际模型初始化/无外部HTTP重启/检索回归。Windows静态MSVC运行库并用dumpbin拒绝VC redistributable依赖；macOS/Linux无OpenSSL/OpenMP/GPU运行依赖。矩阵超时60分钟，仍无生产部署。

原生矩阵对实际解压包运行现有图文编辑/批量审核/引用浏览器回归，Playwright1.64.1固定，合成API不读取真实配置。
