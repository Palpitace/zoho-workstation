# input: 已构建前端、Go/uv 工具链、固定 Python 版本和锁定依赖。
# output: 携带总结/知识/文档入口且不含配置和数据的本机便携包、构建元信息及 SHA256。
# pos: GitHub Actions 与本地共用的打包入口。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile

PYTHON_VERSION = "3.12.14"
ROOT = Path(__file__).resolve().parents[1]


def run(*args: str, **kwargs):
    return subprocess.run(args, cwd=ROOT, check=True, text=True, **kwargs)


def main():
    system = {"Windows": "windows", "Darwin": "macos", "Linux": "linux"}[platform.system()]
    arch = {"AMD64": "x64", "x86_64": "x64", "arm64": "arm64", "aarch64": "arm64"}[platform.machine()]
    name = f"zoho-workstation-{system}-{arch}"
    output = ROOT / ".cache" / "packages"
    output.mkdir(parents=True, exist_ok=True)
    cache = ROOT / ".cache" / "package-python"
    env = {**os.environ, "UV_PYTHON_INSTALL_DIR": str(cache)}
    run("uv", "python", "install", "--no-bin", PYTHON_VERSION, env=env)
    executable = Path(run("uv", "python", "find", "--managed-python", PYTHON_VERSION, env=env, capture_output=True).stdout.strip())
    home = executable.parent if system == "windows" else executable.parent.parent
    with tempfile.TemporaryDirectory(dir=ROOT / ".cache") as temporary:
        stage = Path(temporary) / name
        stage.mkdir()
        shutil.copytree(home, stage / "runtime" / "python", symlinks=system != "windows")
        python = stage / "runtime" / "python" / ("python.exe" if system == "windows" else "bin/python3")
        requirements = Path(temporary) / "requirements.txt"
        run("uv", "export", "--project", "python", "--locked", "--no-dev", "--no-emit-project", "--format", "requirements-txt", "--output-file", str(requirements), stdout=subprocess.DEVNULL)
        wheel_options = ["--only-binary", ":all:"]
        build_env = dict(os.environ)
        if system == "macos" and arch == "x64":
            # The locked cryptography release has no Intel macOS wheel. Build
            # its hash-verified source with static OpenSSL, never a cached
            # extension that could depend on the CI host's Homebrew libraries.
            wheel_options = ["--no-binary", "cryptography", "--no-cache"]
            build_env["OPENSSL_STATIC"] = "1"
            build_env["OPENSSL_DIR"] = run("brew", "--prefix", "openssl@3", capture_output=True).stdout.strip()
        run("uv", "pip", "install", "--python", str(python), "--target", str(stage / "python" / "packages"), "--require-hashes", "--link-mode", "copy", *wheel_options, "-r", str(requirements), env=build_env)
        for file in ("main.py", "knowledge.py", "documents.py", "pyproject.toml", "uv.lock"):
            shutil.copy2(ROOT / "python" / file, stage / "python" / file)
        shutil.copytree(ROOT / "web" / "dist", stage / "web" / "dist")
        shutil.copy2(ROOT / ".env.example", stage / ".env.example")
        shutil.copy2(ROOT / "docs" / "downloads.md", stage / "README.md")
        launcher = "start.cmd" if system == "windows" else "start.command"
        shutil.copy2(ROOT / "scripts" / launcher, stage / launcher)
        (stage / launcher).chmod(0o755)
        binary = "workstation.exe" if system == "windows" else "workstation"
        run("go", "build", "-trimpath", "-o", str(stage / binary), "./cmd/workstation", env={**os.environ, "CGO_ENABLED": "0"})
        commit = run("git", "rev-parse", "HEAD", capture_output=True).stdout.strip()
        (stage / "BUILD.json").write_text(json.dumps({"commit": commit, "source_dirty": bool(run("git", "status", "--porcelain", capture_output=True).stdout.strip()), "platform": system, "architecture": arch, "python": PYTHON_VERSION}, indent=2), encoding="utf-8")
        archive = Path(shutil.make_archive(str(output / name), "zip" if system == "windows" else "gztar", root_dir=temporary, base_dir=name))
    with archive.open("rb") as file:
        digest = hashlib.file_digest(file, "sha256").hexdigest()
    Path(str(archive) + ".sha256").write_text(f"{digest}  {archive.name}\n", encoding="ascii")
    print(archive)


if __name__ == "__main__":
    main()
