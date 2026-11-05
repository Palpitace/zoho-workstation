一旦我所属的文件夹有所变化请更新我。

负责便携包构建、离线验收和解压后启动。
读取锁文件及构建资源，调用 Go/uv；仅构建阶段需要开发工具。
不读取用户 .env、不采集工单、不自动部署。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录约定 | 打包和验证边界 |
| package.py | 原生打包 | 携带 Python/依赖/前端及 Go，生成 SHA256 |
| smoke_package.py | 离线验收 | 异地解压后的 SDK/CLI/SQLite/日志检查 |
| start.cmd | Windows 入口 | 首用模板和检查后启动，失败停留窗口 |
| start.command | macOS/Linux 入口 | 工作目录定位、首用模板和服务启动 |

package.py 仅复制显式允许的文件和新建 runtime/packages，禁止整仓库打包；从 uv 管理的 CPython 3.12.14 复制独立发行版，用 uv.lock 导出带 Hash 的 requirements，安装对应平台二进制 wheels 到 python/packages。macOS 使用 tar.gz 保留可执行权限/链接，Windows ZIP；包内 BUILD.json 记录源提交和平台。无用户 .env/data/logs，前端必须已构建。Linux 同脚本用于本地离线验收，不列入本次下载矩阵。

解压包有自己的 Python，不包含不可移动的 .venv；测试将归档解到带空格的新目录，实际导入 SDK/原生 wheels，执行 Go check 验证配置/SQLite/操作锁/日志，不访问外部业务服务。uv 只在构建时使用，终端用户不用安装工具链。首次启动生成模板但不覆盖已存在的 .env。配置与运行数据保留在解压目录，需要写权限；升级说明见 docs/downloads.md。
