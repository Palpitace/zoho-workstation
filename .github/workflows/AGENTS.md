一旦我所属的文件夹有所变化请更新我。

负责 Windows/macOS 原生构建与产物上传。
调用项目测试及 scripts/package.py、smoke_package.py。
不调用真实 Zoho/模型、不发布生产或自动创建 Release。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | CI 触发与固定工具链 |
| build.yml | 自动构建 | main/PR/手动触发三平台验证和下载包 |

固定 Actions 提交 SHA；Go 版本读取 go.mod，pnpm 12.17.0 与 web/package.json 一致，Node 26.10.0、uv 0.13.7、CPython 3.12.14 固定。Windows Server 2025 x64、macOS 15 Intel/ARM 原生测试，运行完整小型测试、构建、异地解压验收，再上传产物保留 30 天。仅 contents:read，不使用业务凭据。CI 成功才视为相应操作系统验证通过。
