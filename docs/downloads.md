# 下载与运行

Windows 10/11 x64 下载 `zoho-workstation-windows-x64.zip`；Intel Mac 下载 `zoho-workstation-macos-x64.tar.gz`，Apple Silicon Mac 下载 `zoho-workstation-macos-arm64.tar.gz`。

GitHub 仓库 → Actions → **Build desktop packages** → 最新成功运行 → Artifacts。登录有仓库访问权的 GitHub 账户后下载对应平台的产物；Actions 外层 ZIP 解压后，继续解压其中的应用归档。校验和文件供核对完整性。自动构建保留下载文件 30 天，也可手动 Run workflow 重新构建。

## Windows

1. 将整个包解压到自己有写权限的本地目录，例如 `D:\ZohoWorkstation`；不要在 ZIP 内直接运行。
2. 双击 `start.cmd`。首次会生成 `.env` 并打开记事本；填入 Zoho 部门及凭据、Jev 和 Bub/DeepSeek 配置后保存。文件名必须是 `.env`，不是 `.env.txt`。已有 `.env` 不会覆盖。
3. 再次双击 `start.cmd`。检查通过后启动服务，浏览器访问日志中显示的 `local_url`（默认 `http://127.0.0.1:8080`）。
4. 保持窗口开启；停止时按 Ctrl+C。无需安装 Go、Node、Python、WSL 或 Docker。包未做代码签名，Windows 可能提示确认来源，请核对仓库后再运行。应用仍需要网络访问你配置的服务。

包内含 Go 程序、前端和 Python/Bub 环境，不是单个 EXE。不要单独移动或删除 runtime、python、web 目录。Windows ARM 原生包本期不提供。

## macOS

解压适配 CPU 的包，运行 `start.command`，首次填写同目录 `.env`，再次运行后访问终端显示的地址。也可在终端进入目录执行 `./workstation check`、`./workstation serve`。macOS 包未经过 Apple 签名/公证；若系统拦截，核对仓库来源后通过系统设置的“隐私与安全性”允许本次应用，不需要关闭系统安全功能。不要修改未知程序的隔离属性。

## 日志与排障

日志在 `logs/workstation.log`，同时输出到启动窗口；包含启动、同步批次、AI 成功/失败和安全错误。单文件最大 10 MiB，最多保留 3 个轮转备份，备份最长 7 天；默认 info，`.env` 的 APP_LOG_LEVEL 可调整。启动配置错误也会写入；目录不可写时会在窗口显示原因。

Windows 可用记事本打开日志，或在应用目录运行 PowerShell：`Get-Content .\logs\workstation.log -Tail 100 -Wait`。日志不记录密钥或工单正文；提交问题时只分享必要错误片段，不上传 `.env`、数据库或 data 目录。Python 原始 stderr 不直接写入应用日志。

`workstation.exe check` 是离线配置/资源/SQLite/锁及 Python SDK 导入检查，不调用 Zoho 或模型。认证或模型连通性仍须启动后的实际请求验证。已有进程运行时检查会提示锁被占用。

## 更新

先 Ctrl+C 停止旧服务并备份 `.env` 与 `data`，将新包解压到一个新目录，再复制自己的 `.env` 和整个 `data` 目录过去；需要留存历史日志时一起复制 logs。不要用下载包覆盖正在使用的数据库。包不包含用户配置或数据，也不会自动升级、开机启动或修改 Zoho 工单。首次启动仍遵守固定采样范围，启用模型会按配置自动分析。

## SOCKS 代理与 Bub 环境错误

下载包包含 SOCKS 支持，可继承 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY、NO_PROXY 及小写形式。启动检查会离线构造 HTTP 客户端，但不会验证代理网络连通性。

- `proxy_dependency_missing`：重新下载完整新版包；源码部署执行 `uv sync --project python --locked --no-dev`。
- `proxy_configuration_invalid`：检查代理环境变量的协议/地址，修正后重启；不要将含认证信息的代理地址贴入问题报告。
- `python_environment_invalid`：检查本地证书与 Python 环境设置。

自动分析在环境检查失败时暂停 Bub，不增加每张工单的模型尝试数，同步和查看不受影响。升级不会删除此前失败记录；已达到两次自动尝试上限的旧工单，确认启动检查通过后点击“重新总结”。
