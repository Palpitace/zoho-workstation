# input: Go、pnpm、uv/Python 工具链与根 .env；测试仅使用合成数据。
# output: 构建、本机运行、受控采样及业务/打包合成验证命令。
# pos: 项目命令入口。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
.PHONY: setup test build run sample docs-check

setup:
	go mod download
	pnpm --dir web install --frozen-lockfile
	uv sync --project python --locked --no-dev

test:
	go test ./...
	pnpm --dir web check
	python/.venv/bin/python -m unittest discover -s python -p 'test_*.py'
	python/.venv/bin/python -m unittest discover -s scripts -p 'test_*.py'
	go run ./cmd/checkdocs

build:
	pnpm --dir web build
	go build -o bin/workstation ./cmd/workstation

run: build
	./bin/workstation serve

sample:
	go run ./cmd/workstation sample -size 50 -max 3

docs-check:
	go run ./cmd/checkdocs
