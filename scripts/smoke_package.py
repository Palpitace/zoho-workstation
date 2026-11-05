# input: 自有构建产物归档，不读取用户凭据。
# output: 异地解压后 Python/SDK、真实代理 transport、Go/SQLite/锁与安全日志的离线验证。
# pos: 下载包验收边界，供三个原生 CI 平台调用。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

archive = Path(sys.argv[1]).resolve()
expected = Path(str(archive) + ".sha256").read_text().split()[0]
with archive.open("rb") as file:
    assert hashlib.file_digest(file, "sha256").hexdigest() == expected
with tempfile.TemporaryDirectory(prefix="workstation portable space ") as temporary:
    shutil.unpack_archive(str(archive), temporary)
    root = next(Path(temporary).iterdir())
    assert not (root / ".env").exists() and not (root / "data").exists() and not (root / "logs").exists()
    assert (root / "web/dist/index.html").is_file()
    python = root / "runtime/python" / ("python.exe" if os.name == "nt" else "bin/python3")
    env = {**os.environ, "PYTHONPATH": str(root / "python/packages"), "PYTHONNOUSERSITE": "1", "PYTHONDONTWRITEBYTECODE": "1"}
    # No SDK request: actual imports and JSON contract validation exercise native wheels.
    subprocess.run([str(python), "-c", "import sys; sys.path.insert(0, 'python'); import main; main.Snapshot.model_validate({'conversation_hash':'synthetic','messages':[]}); print('bundled SDK import: PASS')"], cwd=root, env=env, check=True)
    if sys.platform == "darwin":
        for library in (root / "python/packages").rglob("*"):
            if library.suffix not in (".so", ".dylib"):
                continue
            linked = subprocess.check_output(["otool", "-L", str(library)], text=True)
            for line in linked.splitlines()[1:]:
                dependency = line.strip().split(" (", 1)[0]
                if dependency.startswith("/") and not dependency.startswith(("/usr/lib/", "/System/Library/")):
                    raise AssertionError(f"SDK depends on build-host library: {dependency}")
        print("SDK native libraries are independent of Homebrew/build-host paths: PASS")
    binary = root / ("workstation.exe" if os.name == "nt" else "workstation")
    invalid = subprocess.run([str(binary), "check"], cwd=root, capture_output=True)
    assert invalid.returncode != 0
    logfile = root / "logs/workstation.log"
    assert "cannot read .env" in logfile.read_text(encoding="utf-8")
    config = (root / ".env.example").read_text(encoding="utf-8")
    values = {"ZOHO_BASE_URL":"https://desk.example.invalid", "ZOHO_ACCOUNTS_URL":"https://accounts.example.invalid", "ZOHO_CLIENT_ID":"synthetic-private", "ZOHO_CLIENT_SECRET":"synthetic-private", "ZOHO_REFRESH_TOKEN":"synthetic-private", "ZOHO_ORG_ID":"7", "ZOHO_DEPARTMENT_ID":"7", "JEV_API_KEY":"synthetic-private", "BUB_API_KEY":"synthetic-private"}
    config = "\n".join(f"{line.split('=',1)[0]}={values[line.split('=',1)[0]]}" if '=' in line and line.split('=',1)[0] in values else line for line in config.splitlines())
    (root / ".env").write_text(config, encoding="utf-8")
    # Exercise the real Go -> isolated Python -> SDK transport path without sending requests.
    clean_env = {k: v for k, v in os.environ.items() if k.lower() not in ("http_proxy", "https_proxy", "all_proxy", "no_proxy")}
    subprocess.run([str(binary), "check"], cwd=root, env=clean_env, check=True)
    for key in ("ALL_PROXY", "all_proxy"):
        for mixed in (False, True):
            proxy_env = {**clean_env, key: "socks5h://synthetic-private-proxy:secret@127.0.0.1:7890"}
            if mixed:
                proxy_env.update(HTTP_PROXY="http://127.0.0.1:7891", HTTPS_PROXY="http://127.0.0.1:7891")
            subprocess.run([str(binary), "check"], cwd=root, env=proxy_env, check=True)
    socks = root / "python/packages/socksio"
    missing = root / "python/packages/socksio-unavailable"
    socks.rename(missing)
    try:
        failure = subprocess.run([str(binary), "check"], cwd=root, env=proxy_env, capture_output=True, text=True)
        assert failure.returncode != 0 and "proxy_dependency_missing" in failure.stderr
        assert "synthetic-private-proxy" not in failure.stdout + failure.stderr
    finally:
        missing.rename(socks)
    subprocess.run([str(binary), "check"], cwd=root, env=proxy_env, check=True)
    log = logfile.read_text(encoding="utf-8")
    assert "local runtime verified" in log and "synthetic-private" not in log
    assert (root / "data/app.db").is_file()
    print("relocated package, SQLite/locks, startup diagnostics and SDK: PASS")
