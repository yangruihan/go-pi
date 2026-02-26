# Gopi - 设计文档

> Go + Ollama 本地 AI 编程助手框架，目标：**响应快、编码能力强**

---

## 1. 项目目标

| 目标 | 具体指标 |
|---|---|
| 启动时间 | < 50ms（不含 Ollama 冷启动） |
| 首 Token 延迟 | < 1s（局域网 Ollama） |
| 内存占用 | < 30MB（不含模型） |
| 默认模型 | `qwen2.5-coder:7b` / `qwen3:8b` |
| 分发形式 | 单一静态二进制，无依赖 |

---

## 2. 整体架构

```
┌──────────────────────────────────────────────────────────┐
│                     CLI 入口 (main.go)                    │
│           解析参数 / 选择运行模式 / 初始化配置             │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│               TUI Layer  (Bubbletea)                      │
│   输入框 │ Markdown 流式渲染 │ 工具执行进度 │ 快捷键        │
└────────────────────────┬─────────────────────────────────┘
                         │ tea.Msg / channel
┌────────────────────────▼─────────────────────────────────┐
│                  AgentSession  (业务枢纽)                  │
│  • session.prompt()   • 事件分发 (EventBus)               │
│  • 上下文压缩          • 会话持久化 (JSONL)                │
│  • 重试逻辑            • 扩展/Slash 命令                   │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│                  Agent Loop (goroutine)                   │
│  • 流式调用 Ollama      • Tool Call 解析 & 分发            │
│  • steer channel        • followUp channel                │
└──────────┬─────────────────────────────┬─────────────────┘
           │ stream                      │ tool calls
┌──────────▼──────────┐    ┌─────────────▼───────────────┐
│   Ollama Client      │    │     Tool Executor            │
│  /api/chat (stream)  │    │  goroutine pool，并发执行    │
│  支持 tool calling   │    │  bash / read / write / edit  │
│  + ReAct fallback    │    │  grep / find / ls            │
└──────────────────────┘    └─────────────────────────────┘
```

---

## 3. 模块详细设计

### 3.1 Ollama 客户端 (`internal/llm/`)

```
internal/llm/
├── client.go        # Ollama HTTP 客户端封装
├── stream.go        # 流式响应解析，输出 Event channel
├── tools.go         # Tool schema 构建 & Tool Call 响应解析
└── react.go         # ReAct fallback（当模型不支持原生 Tool Calling 时）
```

**关键设计：双模式 Tool Calling**

```
模型支持原生 Function Calling  →  使用 Ollama /api/chat tools 字段
         ↓
模型不支持（如部分 gguf 量化）  →  ReAct 模式
         系统提示中定义工具格式：
         Thought: xxx
         Action: tool_name
         Action Input: {"key": "value"}
         Observation: [tool result]
```

优先使用原生模式，在 `config.yaml` 中可强制指定。

**事件流（Event）定义：**

```go
type Event struct {
    Type    EventType
    // message_start | message_delta | message_end
    // tool_call_start | tool_call_end
    // agent_start | agent_end | error
    Delta   string        // 流式文本增量
    Message *Message      // 完整消息（message_end 时）
    Tool    *ToolCall     // 工具调用信息
    Err     error
}
```

---

### 3.2 Agent Loop (`internal/agent/`)

```
internal/agent/
├── loop.go          # 核心 Agent Loop
├── agent.go         # Agent 结构体，状态管理
└── types.go         # AgentMessage, AgentState, Tool 接口定义
```

**核心 Loop 逻辑（伪代码）：**

```
func runLoop(ctx, messages, tools) <-chan Event:
  emit agent_start
  while true:
    emit turn_start
    
    // 1. 流式调用 LLM
    response = streamLLM(messages)
    for event in response:
      emit event (转发给上层)
    
    // 2. 有工具调用 → 并发执行
    if response.hasToolCalls():
      results = concurrentExecTools(response.toolCalls)  // goroutine pool
      messages.append(results)
      continue  // 继续下一轮
    
    // 3. 无工具调用 → 检查 steer/followUp 队列
    if steerChan has message:
      messages.append(steerMsg)
      continue
    if followUpChan has message:
      messages.append(followUpMsg)
      continue
    
    break  // 真正结束
  
  emit agent_end
```

**并发工具执行：**

```go
// 一次 LLM 返回多个工具调用时，真正并发执行
func (e *Executor) ConcurrentExec(calls []ToolCall) []ToolResult {
    results := make([]ToolResult, len(calls))
    var wg sync.WaitGroup
    for i, call := range calls {
        wg.Add(1)
        go func(i int, call ToolCall) {
            defer wg.Done()
            results[i] = e.exec(call)
        }(i, call)
    }
    wg.Wait()
    return results
}
```

---

### 3.3 AgentSession (`internal/session/`)

```
internal/session/
├── session.go       # AgentSession 结构体，对外 API
├── prompt.go        # prompt() / steer() / followUp() 实现
├── compaction.go    # 上下文压缩逻辑
├── persistence.go   # JSONL 会话文件读写
└── eventbus.go      # 内部事件总线
```

**对外 API（极简）：**

```go
type Session interface {
    Prompt(text string, opts ...PromptOpt) error  // 发送消息
    Steer(text string) error                       // 流中干预
    FollowUp(text string) error                    // 完成后追加
    Abort()                                        // 中止当前生成
    Subscribe(fn EventListener) func()             // 订阅事件
    
    // 状态
    Model() string
    IsStreaming() bool
    Messages() []Message
    
    // 会话管理
    Save() error
    SessionFile() string
}
```

---

### 3.4 上下文压缩 (`internal/session/compaction.go`)

**触发条件（更激进的阈值）：**

```
Token 估算 > 60% 模型上限  →  触发压缩
```

| 模型 | 上限 | 触发阈值 |
|---|---|---|
| qwen2.5-coder:7b | 32K | 19K |
| qwen3:8b | 32K | 19K |
| 自定义 | 配置 | 配置 × 0.6 |

**压缩流程：**

```
1. 保留最近 8 条消息（含工具调用/结果）为"热消息"
2. 将其余历史 + 历史工具调用结果 发给 LLM 摘要
3. 摘要 Prompt 提取：
   - 当前任务状态
   - 已完成/放弃的操作
   - 关键发现（修改过的文件、找到的错误等）
   - 重要决策
4. 用摘要消息替换旧历史，写入 CompactionEntry
5. 保留热消息继续对话
```

**工具输出截断（响应快的关键）：**

```go
const (
    BashOutputMaxBytes  = 8192   // bash 输出超过 8KB 截断
    FileReadMaxLines    = 500    // read 工具最多读 500 行
    GrepMaxMatches      = 50     // grep 最多返回 50 条匹配
)
```

---

### 3.5 工具系统 (`internal/tools/`)

```
internal/tools/
├── registry.go      # 工具注册表
├── bash.go          # bash 工具（持久化 Shell 进程）
├── read.go          # 读取文件（支持行范围）
├── write.go         # 写入文件
├── edit.go          # 字符串替换编辑（精确定位）
├── grep.go          # 正则/字面量搜索
├── find.go          # 按文件名 glob 查找
└── ls.go            # 列出目录
```

**Bash 工具（持久化 Shell）：**

```go
// 维护一个持久化的 bash 进程，保留工作目录和环境变量
type BashExecutor struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout *bufio.Scanner
    mu     sync.Mutex       // 串行执行，防止并发混乱
}
// 输出流式回传给 TUI，超时 30s 强制中断
```

**工具接口：**

```go
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage   // JSON Schema for parameters
    Execute(ctx context.Context, args json.RawMessage) (string, error)
}
```

---

### 3.6 会话持久化 (`internal/session/persistence.go`)

**文件格式（JSONL，兼容 pi 的格式思路但简化）：**

```jsonl
{"type":"header","id":"a1b2c3d4","cwd":"/home/user/project","timestamp":"2026-02-26T10:00:00Z"}
{"type":"model_change","model":"qwen2.5-coder:7b","timestamp":"2026-02-26T10:00:01Z"}
{"type":"message","role":"user","content":"帮我写一个快排","timestamp":"..."}
{"type":"message","role":"assistant","content":"...","timestamp":"..."}
{"type":"compaction","summary":"...","token_before":18500,"timestamp":"..."}
```

**存储路径：**
```
~/.gopi/sessions/<cwd-hash>/<session-id>.jsonl
~/.gopi/config.yaml
~/.gopi/models.yaml     # 自定义模型配置
```

---

### 3.7 TUI 层 (`internal/tui/`)

```
internal/tui/
├── app.go           # Bubbletea 主 Model
├── messages.go      # 消息列表渲染组件
├── editor.go        # 输入框组件（支持多行、历史记录）
├── footer.go        # 状态栏（模型名、Token 数、状态）
├── tool_panel.go    # 工具执行面板（实时输出）
└── theme.go         # 颜色主题
```

**渲染策略：**
- 使用 `glamour` 渲染 Markdown，支持代码高亮
- 流式 delta 直接 append 到当前消息块，避免全量重绘
- Terminal 宽度自适应

---

### 3.8 配置系统 (`internal/config/`)

**`~/.gopi/config.yaml`（极简设计）：**

```yaml
# Ollama 配置
ollama:
  host: "http://localhost:11434"
  model: "qwen2.5-coder:7b"    # 默认模型
  timeout: 120s
  tool_calling: auto            # auto | native | react

# 上下文配置  
context:
  max_tokens: 32768             # 0 = 从模型自动获取
  compaction_threshold: 0.60   # 60% 触发压缩
  keep_recent: 8               # 压缩时保留最近 N 条消息

# 工具配置
tools:
  bash_timeout: 30s
  bash_max_output: 8192
  read_max_lines: 500
  grep_max_matches: 50

# TUI 配置
tui:
  theme: "dark"                 # dark | light | system
  show_token_count: true
  quiet_startup: false
```

---

## 4. 数据流：一次完整对话

```
用户输入 "帮我重构 main.go"
    │
    ▼
TUI Editor → session.Prompt("帮我重构 main.go")
    │
    ▼
AgentSession:
  1. 检查 Token 数，判断是否需要先压缩
  2. 构建消息列表（历史 + 新输入）
  3. 设置系统提示词（含工具说明、cwd 等）
  4. 调用 agent.Loop(messages) → 返回 Event channel
    │
    ▼
Agent Loop (goroutine):
  → 调用 Ollama /api/chat（stream=true）
  → 收到 text delta → emit message_delta → TUI 流式渲染
  → 收到 tool_call:read("main.go") → emit tool_call_start
    │
    ▼
Tool Executor:
  → 读取 main.go 内容
  → emit tool_call_end(content)
    │
    ▼
Agent Loop 继续:
  → 将工具结果加入 messages
  → 再次调用 Ollama（携带工具结果）
  → LLM 生成重构后的代码 → text delta → TUI 渲染
  → LLM 调用 write("main.go", newContent)
  → 工具执行写文件
  → LLM 生成最终回答 → emit agent_end
    │
    ▼
AgentSession:
  → 持久化所有消息到 JSONL
  → 更新 Token 计数
    │
    ▼
TUI 显示完整响应 + "✓ 已写入 main.go"
```

---

## 5. 扩展设计（简化版）

不实现复杂的 npm-style 插件包管理，改用**本地脚本扩展**：

```yaml
# ~/.gopi/config.yaml
extensions:
  - path: "~/.gopi/extensions/auto-commit.go"   # Go plugin
  - path: "~/.gopi/extensions/web-search.sh"    # Shell script tool
```

扩展可以：
- 注册新的工具（实现 `Tool` 接口）
- 注册 Slash 命令（`/mycommand`）
- 监听会话事件

---

## 6. 技术选型汇总

| 组件 | 选型 | 理由 |
|---|---|---|
| 语言 | Go 1.22+ | 启动快、并发好、单二进制 |
| Ollama 客户端 | `github.com/ollama/ollama/api` | 官方 SDK |
| TUI 框架 | `github.com/charmbracelet/bubbletea` | Go TUI 事实标准 |
| Markdown 渲染 | `github.com/charmbracelet/glamour` | 支持代码高亮 |
| Token 估算 | `github.com/pkoukk/tiktoken-go` | 不调用 API，本地计算 |
| 配置文件 | `github.com/BurntSushi/toml` 或 `gopkg.in/yaml.v3` | 简单易读 |
| 日志 | `log/slog`（标准库） | Go 1.21+ 内置 |
| 测试 | `testing` 标准库 + `testify` | 够用 |
