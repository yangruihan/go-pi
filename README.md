# gopi

本地 AI 编程助手（Go 实现），支持 CLI / TUI、工具调用、会话持久化、上下文压缩、模型别名与 OpenAI 兼容后端。

## 核心能力

- 本地或兼容 API 对话：`ollama` / `openai`
- 工具调用：`bash`、文件读写编辑、grep/find/ls、自定义 YAML 工具
- 会话系统：持久化、继续会话、会话分支与 `/checkout`
- TUI 交互：模型选择、会话切换、工具面板、滚动显示
- 提示词系统：内置规则 + `AGENT.md` + 外置模板

## 快速开始

### 1) 构建

```bash
go build -o build/gopi.exe ./cmd/gopi/
```

### 2) 运行

```bash
# 交互 CLI
./build/gopi.exe

# TUI 模式
./build/gopi.exe --tui

# 非交互（stdin -> stdout）
echo "写一个快速排序" | ./build/gopi.exe --print
```

### 3) Windows 一键脚本（推荐）

```powershell
.\make.ps1 build
.\make.ps1 run
.\make.ps1 test-short
```

## 常用参数

- `--model` / `-m`：指定模型或模型别名
- `--provider`：`ollama` 或 `openai`
- `--api-base`：OpenAI 兼容后端地址
- `--api-key`：OpenAI 兼容后端 key
- `--continue` / `-c`：继续最近会话
- `--session` / `-s`：打开指定会话
- `--tui`：启用 TUI
- `--print`：非交互模式
- `--perf`：运行性能测量

## Slash 命令

- `/help`
- `/session`
- `/session entries`
- `/model <name>`
- `/checkout <entry-id>`
- `/skill:<name>`
- `/clear`
- `/exit`

## 配置

gopi 读取用户目录配置：

- `~/.gopi/config.yaml`
- `~/.gopi/models.yaml`
- `~/.gopi/tools.yaml`
- `~/.gopi/prompt.md`

项目目录规则文件：

- `<project>/AGENT.md`

仓库已提供模板示例，见 [config/README.md](config/README.md)。

## 提示词拼装逻辑

系统提示词由三部分组合：

1. 内置基础提示（随 provider 和运行模式动态变化）
2. 外置模板（`prompt.template_file`，可选）
3. 项目 `AGENT.md`（存在时追加）

模板占位符：

- `{{BASE_PROMPT}}`
- `{{AGENT_MD}}`

## 开发命令

```bash
# Makefile（若安装了 make）
make build
make test

# PowerShell 一键脚本（Windows）
.\make.ps1 build
.\make.ps1 test
.\make.ps1 clean
```

## 目录概览

- `cmd/gopi/`：CLI 入口
- `internal/agent/`：Agent loop
- `internal/llm/`：LLM 客户端（Ollama/OpenAI compatible）
- `internal/session/`：会话、持久化、压缩、分支
- `internal/tui/`：TUI 组件
- `internal/tools/`：内置与自定义工具
- `internal/prompt/`：系统提示词构建
- `config/`：配置模板示例
