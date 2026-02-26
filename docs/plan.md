# Gopi - 开发计划

> 目标：用最少的代码，实现响应最快、编码能力最强的本地 AI Agent

---

## 开发原则

1. **可运行优先**：每个阶段结束都必须是可运行的产品，不堆未完成功能
2. **最小依赖**：只引入必要的第三方库
3. **先跑通，再优化**：性能优化放在功能验证之后
4. **测试驱动核心逻辑**：Agent Loop、压缩算法必须有单元测试

---

## 总体里程碑

```
Phase 1 (基础骨架) ✅   →    Phase 2 (完整工具链)    →    Phase 3 (TUI + 体验)
  2026-02-26 完成              约 1-2 周                   约 1-2 周
  可用 CLI 交互               全工具链可用                 完整产品体验
        ↓
Phase 4 (稳定性)       →    Phase 5 (扩展能力)
  约 1 周                    持续迭代
  生产可用                   插件/自定义
```

---

## Phase 1 — 基础骨架 (MVP) ✅ 已完成

> **完成时间：** 2026-02-26  
> **状态：** 可运行，通过全部单元测试，二进制 ~10MB

**目标**：能在终端里和 Ollama 对话，有流式输出，有基础 bash 工具。

### 1.1 项目初始化 ✅

```bash
go mod init github.com/coderyrh/gopi

# 目录结构（已创建）
gopi/
├── cmd/gopi/main.go         # CLI 入口 ✅
├── internal/
│   ├── llm/                 # Ollama 客户端 ✅
│   ├── agent/               # Agent Loop ✅
│   ├── tools/               # 工具系统 ✅
│   └── config/              # 配置加载 ✅
├── build/gopi.exe           # 构建产物
├── Makefile                 # 构建脚本
├── go.mod / go.sum
└── docs/
```

**已安装依赖：**
- `github.com/ollama/ollama/api` — Ollama 官方 SDK
- `github.com/charmbracelet/bubbletea` — TUI 框架（Phase 3 使用）
- `github.com/charmbracelet/glamour` — Markdown 渲染
- `github.com/charmbracelet/lipgloss` — 样式
- `github.com/pkoukk/tiktoken-go` — Token 计算
- `gopkg.in/yaml.v3` — 配置文件解析
- `github.com/stretchr/testify` — 测试断言

---

### 1.2 Ollama 客户端 (`internal/llm/`) ✅

**已完成：**
- [x] `types.go`：`Message`, `Tool`, `ToolCall`, `Event` 类型定义
- [x] `client.go`：封装 Ollama API 连接，支持自定义 host，含 Ping/ListModels
- [x] `stream.go`：流式调用 `/api/chat`，输出 `<-chan Event`，支持 tool_call 解析
- [x] `tools.go`：工具 Schema 构建 & 参数解析

---

### 1.3 Agent Loop (`internal/agent/`) ✅

**已完成：**
- [x] `types.go`：`AgentEvent`, `AgentState`, `AgentLoopConfig`, `ToolExecutor` 接口
- [x] `loop.go`：`RunLoop(ctx, messages, config, client, executor) <-chan AgentEvent`
  - [x] 单轮 LLM 调用 + 流式转发
  - [x] Tool Call 识别与并发执行（goroutine pool）
  - [x] 多轮循环（有工具调用则继续）
  - [x] MaxTurns 限制防止死循环
- [x] `agent.go`：`Agent` 结构体，维护状态，`Prompt()` / `Abort()` / `SetModel()` API

**单元测试（全部通过 ✅）：**
- [x] `loop_test.go`：11 个测试，覆盖单轮/多轮/工具调用/取消/超限/LLM 错误
- [x] `agent_test.go`：Prompt/Abort/Model/Clear/SystemMessage 测试

---

### 1.4 核心工具 (`internal/tools/`) ✅

**已完成：**
- [x] `registry.go`：工具注册表，`Register` / `Get` / `All` / `ToLLMTools` / `Execute`
- [x] `bash.go`：bash 工具（支持 Windows cmd / Unix bash，超时 30s，输出截断 8KB）
- [x] `read.go`：读取文件（支持 `start_line`/`end_line`，最多 500 行，带行号显示）

---

### 1.5 最简 CLI 交互 (`cmd/gopi/main.go`) ✅

**已完成：**
- [x] 流式打印 LLM 输出（delta 增量）
- [x] 显示工具调用状态（`[执行工具: bash]` + 参数预览）
- [x] `Ctrl+C` 中止当前生成，`Ctrl+D` 退出
- [x] Slash 命令：`/help`, `/model`, `/clear`, `/exit`
- [x] `--model` / `--host` / `--no-tools` / `--print` / `--version` 参数
- [x] `--print` 非交互管道模式
- [x] 配置文件 `~/.gopi/config.yaml` 自动加载

**运行方式：**
```bash
# 构建
go build -o build/gopi.exe ./cmd/gopi/

# 运行
./build/gopi.exe                     # 交互模式（需 Ollama 启动）
./build/gopi.exe --model qwen3:8b    # 指定模型
./build/gopi.exe --continue          # 继续最近会话
./build/gopi.exe --session <id>      # 打开指定会话
./build/gopi.exe --no-tools          # 纯对话模式
echo "写一个冒泡排序" | ./build/gopi.exe --print  # 管道模式
```

**Phase 1 完成标志：** ✅ 能在终端与 Ollama 进行多轮对话，能执行 bash 命令并将结果返回给 LLM，全部 11 个单元测试通过。

---

## Phase 2 — 完整工具链 + 会话持久化 ✅ 已完成（初版）

**目标**：完整的文件操作工具 + 会话可以保存/恢复。

### 2.1 完整工具实现 (`internal/tools/`)

**任务清单：**
- [x] `write.go`：写文件（覆盖或追加）
- [x] `edit.go`：精确字符串替换（`old_string` → `new_string`，需匹配上下文）
- [x] `grep.go`：正则/字面量搜索（`-r` 递归，输出文件名+行号+内容）
- [x] `find.go`：Glob 文件查找（过滤 .gitignore）
- [x] `ls.go`：目录列表（树状或平铺）

**edit 工具的精确替换算法（重要）：**
```
1. 找到 old_string 在文件中的所有出现位置
2. 如果出现 0 次 → 返回错误，提示 LLM 检查字符串
3. 如果出现 >1 次 → 返回错误，提示 LLM 提供更多上下文
4. 恰好 1 次 → 执行替换，返回成功
```

---

### 2.2 会话持久化 (`internal/session/persistence.go`)

**任务清单：**
- [x] JSONL 会话文件写入（Append-only）
- [x] 从文件加载历史会话
- [x] 会话文件路径管理（按 cwd hash 分目录）
- [x] `SessionManager.List(cwd)` — 列出当前项目的会话
- [x] `SessionManager.Continue(cwd)` — 继续最近一次会话

---

### 2.3 AgentSession 业务层 (`internal/session/session.go`)

**任务清单：**
- [x] `Session` 接口完整实现
- [x] 事件总线：`Subscribe(fn)` → 返回取消函数
- [x] `Prompt()` 串联：扩展输入处理 → 构建消息 → 调用 Agent → 持久化
- [x] Slash 命令基础框架：`/help`, `/session`, `/model`, `/clear`

---

### 2.4 上下文压缩 (`internal/session/compaction.go`)

**任务清单：**
- [x] Token 估算（tiktoken-go，cl100k_base 编码）
- [x] 压缩触发检测（每次 agent_end 后检查）
- [x] 压缩流程：提取热消息 → 调用 LLM 摘要 → 替换历史 → 持久化
- [x] 摘要 Prompt 设计（专门针对编程任务优化）
- [x] 单元测试：验证压缩后消息数量、Token 数减少正确

**摘要 Prompt 模板（针对编码任务）：**
```
你是一个会话历史压缩助手。请将以下对话历史提炼成简洁的摘要。

要求：
1. 当前任务：用一句话说明用户的核心需求
2. 已完成操作：列出已执行的关键操作（修改了哪些文件、发现了什么）
3. 当前状态：当前代码/任务处于什么状态
4. 重要发现：记录关键的技术细节、错误信息、决策依据
5. 待续事项：还未完成的工作

对话历史：
{{history}}
```

---

### 2.5 CLI 增强

**任务清单：**
- [x] `-m / --model` 指定模型
- [x] `-c / --continue` 继续上次会话
- [x] `-s / --session <id>` 打开指定会话
- [x] `--print` 非交互模式（接受 stdin 输入，输出到 stdout，适合管道）
- [x] `--no-tools` 纯对话模式

**Phase 2 完成标志：** ✅ 已达成（初版）：已支持完整文件工具链、会话 JSONL 持久化与恢复、`/session` 命令、上下文压缩与单元测试。

---

## Phase 3 — TUI 完整实现 ✅ 已完成

**目标**：好看、好用的终端界面。

### 3.1 Bubbletea 主框架 (`internal/tui/app.go`)

**任务清单：**
- [x] `AppModel` 主 Model（Bubbletea 架构）
- [x] 消息列表组件 `messages.go`（滚动、自动滚到底部）
- [x] 多行输入框 `editor.go`（Shift+Enter 换行，Enter 发送，↑↓ 历史）
- [x] 状态栏 `footer.go`（模型名、Token 计数、流式状态指示）

### 3.2 渲染组件

**任务清单：**
- [x] Markdown 渲染（glamour）：代码块高亮、表格、列表
- [x] 工具执行面板：显示工具名 + 参数 + 实时输出（折叠/展开）
- [x] 流式 delta 渲染：增量更新，不全量重绘
- [x] 压缩状态提示：`[正在压缩上下文，请稍候...]`
- [x] 错误消息渲染（红色，有重试提示）

### 3.3 快捷键设计

| 快捷键 | 功能 |
|---|---|
| `Enter` | 发送消息 |
| `Shift+Enter` | 输入框换行 |
| `Ctrl+C` | 中止当前生成 |
| `Ctrl+L` | 清屏（不清历史） |
| `Ctrl+R` | 打开会话选择器 |
| `Ctrl+P` | 切换模型 |
| `Esc` | 关闭弹窗/选择器 |
| `↑ / ↓` | 输入历史翻页（无选择器时） |
| `PgUp / PgDn` | 消息列表滚动 |

### 3.4 图片支持

- [x] 检测终端是否支持 Kitty 图片协议
- [x] 支持文件路径 `@image.png` 语法附带图片（需要多模态模型）
- [x] 优雅降级：不支持时显示路径文本

**Phase 3 完成标志：** ✅ 已达成：TUI 主框架、渲染组件、快捷键、图片附带与降级能力均已可用。

---

## Phase 4 — 稳定性与性能优化 ✅ 已完成

**目标**：生产可用，无明显 Bug。

### 4.1 健壮性

**任务清单：**
- [x] Ollama 连接重试（指数退避，最多 3 次）
- [x] 模型未加载时的友好提示（`ollama pull qwen2.5-coder:7b`）
- [x] Bash 工具进程崩溃自动重启
- [x] 会话文件写入失败的错误处理（不丢消息）
- [x] `Ctrl+C` 正确退出（清理子进程、保存会话）

### 4.2 性能测试

**任务清单：**
- [x] 测量：从启动到第一个 Token 的延迟（目标 < 1s）
- [x] 测量：TUI 一帧渲染时间（目标 < 16ms）
- [x] 测量：大会话（1000 条消息）的加载时间
- [x] 识别并优化瓶颈

**测量入口：**
```bash
./build/gopi.exe --perf
```

**当前样例结果（2026-02-26）：**
- 首 Token 延迟：`8.2292482s`（未达标，当前主要瓶颈）
- TUI 帧耗时：`avg=5.207174ms`，`max=36.6387ms`
- 1000 条会话加载：`2.585ms`
- 瓶颈识别：`first_token`

### 4.3 集成测试

**任务清单：**
- [x] 端到端测试：启动 → 发送消息 → 工具调用 → 收到回复
- [x] 会话持久化测试：保存 → 退出 → 恢复 → 验证消息一致
- [x] 压缩测试：人工构造大会话，触发压缩，验证对话仍连贯

**完成说明（2026-02-26）：**
- 新增 `internal/session/integration_test.go`，覆盖 Phase 4.3 三类集成场景。
- 测试通过后发现并修复 `Prompt()` 中持锁调用 `persistEntry()` 导致的死锁风险。
- 已执行 `go test ./internal/session -v` 与 `go test ./...`，当前全部通过。

---

## Phase 5 — 扩展能力（持续迭代）

**可选功能，按需开发：**

### 5.1 多模型支持
- [x] 在运行时切换模型（`/model qwen3:8b`）
- [x] 支持非 Ollama 后端（OpenAI 兼容 API，如 LM Studio）
- [x] 模型配置文件 `~/.gopi/models.yaml`

### 5.2 扩展系统（简化版）
- [x] Slash 命令注册 API（Go 函数，不是 npm 包）
- [x] 自定义工具通过 YAML 定义 Shell 脚本工具
- [x] `before_prompt` / `after_response` 钩子

### 5.3 分支与历史
- [x] 会话树形结构（类似 pi 的分支支持）
- [x] `/checkout <entry-id>` 回退到历史节点
- [x] TUI 中的树状会话选择器

### 5.4 技能文件 (Skills)
- [x] 读取项目根目录 `AGENT.md` 作为系统提示词追加
- [x] 支持 `/skill:name` 语法加载技能文件

**完成说明（2026-02-26）：**
- 5.1：新增 OpenAI 兼容客户端与 CLI `--provider/--api-base/--api-key`，并支持 `~/.gopi/models.yaml` 模型别名映射。
- 5.2：新增扩展 Slash 命令注册 API、YAML 自定义 Shell 工具加载、`before_prompt`/`after_response` Hook 执行。
- 5.3：会话持久化新增父会话关系与 entry-id，支持 `/session entries` + `/checkout <entry-id>` 分支回退；TUI 会话选择器按树形前缀展示。
- 5.4：启动自动读取项目根目录 `AGENT.md` 追加到系统提示；CLI/TUI 支持 `/skill:<name>` 从 `.gopi/skills/<name>.md` 或 `~/.gopi/skills/<name>.md` 加载技能。

### 5.5 分发与安装
- [ ] `Makefile` 一键构建多平台二进制
- [ ] GitHub Actions CI/CD
- [ ] `brew install` / 直接下载的安装脚本

---

## 文件结构（完成后）

```
gopi/
├── cmd/
│   └── gopi/
│       └── main.go
├── internal/
│   ├── agent/
│   │   ├── agent.go
│   │   ├── agent_test.go
│   │   ├── loop.go
│   │   ├── loop_test.go
│   │   └── types.go
│   ├── config/
│   │   ├── config.go
│   │   └── defaults.go
│   ├── llm/
│   │   ├── client.go
│   │   ├── stream.go
│   │   ├── tools.go
│   │   ├── react.go
│   │   └── types.go
│   ├── session/
│   │   ├── session.go
│   │   ├── prompt.go
│   │   ├── compaction.go
│   │   ├── compaction_test.go
│   │   ├── persistence.go
│   │   └── eventbus.go
│   ├── tools/
│   │   ├── registry.go
│   │   ├── bash.go
│   │   ├── bash_test.go
│   │   ├── read.go
│   │   ├── write.go
│   │   ├── edit.go
│   │   ├── edit_test.go
│   │   ├── grep.go
│   │   ├── find.go
│   │   └── ls.go
│   └── tui/
│       ├── app.go
│       ├── messages.go
│       ├── editor.go
│       ├── footer.go
│       ├── tool_panel.go
│       └── theme.go
├── docs/
│   ├── design.md
│   └── plan.md
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## 开发顺序建议

开发时严格按以下顺序，每步都要能运行：

```
Step 1: go mod init + 目录结构                    ✅ 完成
Step 2: internal/llm/ → 能调用 Ollama 流式接口    ✅ 完成
Step 3: internal/tools/registry.go + bash.go      ✅ 完成
Step 4: internal/agent/types.go + loop.go          ✅ 完成（含单元测试）
Step 5: cmd/gopi/main.go → 最简命令行能对话        ✅ 完成
          ⬆ 这里是第一个可演示节点（Phase 1 完成）
Step 6: internal/tools/ 其余工具（read/write/edit/grep/find/ls） ✅ 完成
Step 7: internal/session/ → 持久化 + 压缩                 ✅ 完成
Step 8: internal/session/ → AgentSession 业务层 + Slash 命令 ✅ 完成
          ⬆ Phase 2 完成，完整 CLI 可用
Step 9: internal/tui/ → Bubbletea TUI                      ✅ 完成
Step 10: 稳定性、性能优化
```

---

## 关键风险与应对

| 风险 | 可能性 | 应对方案 |
|---|---|---|
| qwen2.5-coder:7b Tool Calling 不稳定 | 中 | 实现 ReAct fallback，自动检测并切换 |
| 上下文窗口不够用导致频繁压缩 | 高 | 激进的工具输出截断 + 早触发压缩 |
| edit 工具字符串匹配失败 | 中 | 返回详细错误，让 LLM 重新提供精确匹配字符串 |
| bash 工具输出过长卡住 TUI | 低 | 硬限制 8KB，超出自动截断并提示 |
| Bubbletea 与 bash 子进程 I/O 冲突 | 低 | bash 输出通过 channel 转发到 TUI，不直接写 stdout |
