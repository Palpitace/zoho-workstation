# input: 固定 llama.cpp 源归档、原生 CMake/C++ 工具链。
# output: runtime/llama 下可随包复制的 CPU 推理程序和许可证。
# pos: 本地与三平台 CI 共用推理运行时构建。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
from __future__ import annotations

import hashlib
import os
import re
from pathlib import Path
import shutil
import subprocess
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
COMMIT = "cd959bae99ff4bed22bef2604f45241c09bbb8c8"  # official stable v0.9.0
SHA256 = "fbe74146a3cdc00f706773eb233bc4aedb7d8c3c7e2122440e492a99c1ec5a76"


def main():
    cache = ROOT / ".cache" / "llama-build"
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / "source.tar.gz"
    if not archive.exists():
        urllib.request.urlretrieve(f"https://codeload.github.com/ggml-org/llama.cpp/tar.gz/{COMMIT}", archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != SHA256:
        raise RuntimeError("llama.cpp source checksum mismatch")
    source = cache / f"llama.cpp-{COMMIT}"
    if not source.exists():
        with tarfile.open(archive) as tar:
            tar.extractall(cache, filter="data")
    build = cache / "build"
    subprocess.run(["cmake", "-S", str(source), "-B", str(build), "-DCMAKE_BUILD_TYPE=Release", "-DBUILD_SHARED_LIBS=OFF", "-DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded", "-DGGML_NATIVE=OFF", "-DGGML_METAL=OFF", "-DGGML_CUDA=OFF", "-DGGML_OPENMP=OFF", "-DLLAMA_OPENSSL=OFF", "-DLLAMA_BUILD_TESTS=OFF", "-DLLAMA_BUILD_EXAMPLES=OFF", "-DLLAMA_BUILD_SERVER=ON"], check=True)
    subprocess.run(["cmake", "--build", str(build), "--config", "Release", "--target", "llama-server", "-j", str(min(os.cpu_count() or 2, 4))], check=True)
    name = "llama-server.exe" if os.name == "nt" else "llama-server"
    candidates = list((build / "bin").rglob(name))
    if len(candidates) != 1:
        raise RuntimeError("missing native inference executable")
    if os.name == "nt":
        # Runner-installed redistributables must not hide a user-machine dependency.
        probes = list((Path(os.environ.get("ProgramFiles(x86)", "C:/Program Files (x86)")) / "Microsoft Visual Studio").glob("*/BuildTools/VC/Tools/MSVC/*/bin/Hostx64/x64/dumpbin.exe"))
        probes += list((Path(os.environ.get("ProgramFiles", "C:/Program Files")) / "Microsoft Visual Studio").glob("*/*/VC/Tools/MSVC/*/bin/Hostx64/x64/dumpbin.exe"))
        dumpbin = shutil.which("dumpbin") or (str(sorted(probes)[-1]) if probes else None)
        if not dumpbin:
            raise RuntimeError("cannot verify Windows inference runtime dependencies")
        dependencies = subprocess.check_output([dumpbin, "/dependents", str(candidates[0])], text=True)
        if re.search(r"(?i)\b(?:vcruntime|msvcp|concrt)[^\s]*\.dll", dependencies):
            raise RuntimeError("inference executable requires external VC redistributables")
    output = ROOT / "runtime" / "llama"
    output.mkdir(parents=True, exist_ok=True)
    shutil.copy2(candidates[0], output / name)
    shutil.copy2(source / "LICENSE", output / "LICENSE")
    (output / "VERSION").write_text(f"llama.cpp v0.9.0\n{COMMIT}\n{SHA256}\n", encoding="utf-8")


if __name__ == "__main__":
    main()
