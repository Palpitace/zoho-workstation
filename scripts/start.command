#!/bin/sh
# input: 解压后的应用目录和用户填写的 .env。
# output: 首次配置模板与前台服务，失败保留终端提示。
# pos: macOS/Linux 启动入口。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
cd "$(dirname "$0")" || exit 1
if [ ! -f .env ]; then
    cp -n .env.example .env || exit 1
    echo '已生成 .env，请填写凭据，保存后再次运行启动程序。'
    if [ "$(uname -s)" = Darwin ]; then open -e .env; fi
    exit 0
fi
./workstation check && ./workstation serve
result=$?
if [ "$result" -ne 0 ]; then
    echo '启动或运行失败，请查看 logs/workstation.log。按回车关闭。'
    read -r reply
fi
exit "$result"
