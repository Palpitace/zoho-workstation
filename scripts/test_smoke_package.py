# input: 合成 otool 单/双架构输出和临时原生扩展，不访问真实服务。
# output: 架构标题/库自身 ID 不误报，任何架构的构建机绝对依赖仍被拒绝。
# pos: 便携包原生依赖验收回归。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from smoke_package import check_native_libraries


class NativeLibraryTests(unittest.TestCase):
    def test_architecture_headers_and_external_dependencies(self):
        with tempfile.TemporaryDirectory(prefix="native library space ") as temporary:
            packages = Path(temporary)
            library = packages / "builder.cpython-312-darwin.so"
            library.touch()
            for architectures, external_at in ((1, None), (2, None), (1, 0), (2, 0), (2, 1)):
                with self.subTest(architectures=architectures, external_at=external_at):
                    sections = []
                    for index, arch in enumerate(("x86_64", "arm64")[:architectures]):
                        header = f"{library}:" if architectures == 1 else f"{library} (architecture {arch}):"
                        dependency = "/opt/homebrew/lib/libssl.dylib" if index == external_at else "/usr/lib/libSystem.B.dylib"
                        sections.append(f"{header}\nLoad command 0\n          cmd LC_ID_DYLIB\n         name /DLC/PIL/.dylibs/libbrotli.dylib (offset 24)\nLoad command 1\n          cmd LC_LOAD_DYLIB\n         name {dependency} (offset 24)\n")
                    with patch("smoke_package.subprocess.check_output", return_value="".join(sections)):
                        if external_at is None:
                            check_native_libraries(packages)
                        else:
                            with self.assertRaisesRegex(AssertionError, "/opt/homebrew/lib/libssl.dylib"):
                                check_native_libraries(packages)


if __name__ == "__main__":
    unittest.main()
