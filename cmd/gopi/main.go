package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/coderyrh/gopi/internal/agent"
	"github.com/coderyrh/gopi/internal/config"
	"github.com/coderyrh/gopi/internal/llm"
	"github.com/coderyrh/gopi/internal/tools"
)

const version = "0.1.0"

func main() {
	// 解析命令行参数
	var (
		model    = flag.String("m", "", "指定模型（默认使用配置文件中的模型）")
		host     = flag.String("host", "", "Ollama 主机地址（默认 http://localhost:11434）")
		noTools  = flag.Bool("no-tools", false, "禁用工具，纯对话模式")
		printVer = flag.Bool("version", false, "显示版本信息")
		printMode = flag.Bool("print", false, "非交互模式，从 stdin 读取，输出到 stdout")
	)
	flag.Parse()

	if *printVer {
		fmt.Printf("gopi v%s\n", version)
		return
	}

	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告: 加载配置失败: %v，使用默认配置\n", err)
	}

	// 命令行参数覆盖配置
	if *model != "" {
		cfg.Ollama.Model = *model
	}
	if *host != "" {
		cfg.Ollama.Host = *host
	}

	// 创建 LLM 客户端
	client, err := llm.NewClient(cfg.Ollama.Host)
	if err != nil {
		fatal("创建 LLM 客户端失败: %v", err)
	}

	// 检测 Ollama 连接
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		fatal("无法连接到 Ollama (%s): %v\n提示: 请确认 Ollama 已启动，或使用 --host 指定正确的地址", cfg.Ollama.Host, err)
	}

	// 工具注册
	registry := tools.NewRegistry()
	if !*noTools {
		registry.Register(tools.NewBashTool())
		registry.Register(tools.NewReadTool())
	}

	// 构建 LLM 工具列表
	var llmTools []llm.Tool
	if !*noTools {
		llmTools, err = registry.ToLLMTools()
		if err != nil {
			fatal("构建工具定义失败: %v", err)
		}
	}

	// 构建 Agent 配置
	loopCfg := agent.AgentLoopConfig{
		Model:    cfg.Ollama.Model,
		Tools:    llmTools,
		MaxTurns: 20,
		SystemMsg: buildSystemMessage(),
	}

	// 创建 Agent
	a := agent.NewAgent(client, registry, loopCfg)

	if *printMode {
		runPrintMode(ctx, a)
		return
	}

	// 交互式模式
	runInteractive(ctx, a, cfg)
}

// buildSystemMessage 构建系统提示词
func buildSystemMessage() string {
	cwd, _ := os.Getwd()
	return fmt.Sprintf(`你是 Gopi，一个运行在本地的 AI 编程助手。

当前工作目录: %s
操作系统: %s

你有以下工具可以使用:
- bash: 执行 shell 命令，支持文件操作、代码执行等
- read_file: 读取文件内容（支持指定行范围）

使用原则:
1. 优先阅读文件再回答，避免猜测
2. 执行危险操作前先确认
3. 回答简洁、准确，代码有注释
4. 中文回答（除非用户要求英文）`, cwd, getOS())
}

func getOS() string {
	if os.Getenv("OS") == "Windows_NT" {
		return "Windows"
	}
	// 简单检测
	if _, err := os.Stat("/etc/os-release"); err == nil {
		return "Linux"
	}
	return "系统"
}

// runInteractive 运行交互式 CLI
func runInteractive(ctx context.Context, a *agent.Agent, cfg config.Config) {
	// 设置信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigCh {
			if sig == syscall.SIGINT {
				if a.IsStreaming() {
					fmt.Println("\n\n[已中止生成]")
					a.Abort()
				} else {
					fmt.Println("\n再见！")
					os.Exit(0)
				}
			} else {
				os.Exit(0)
			}
		}
	}()

	// 欢迎信息
	if !cfg.TUI.QuietStartup {
		fmt.Printf("Gopi v%s — 本地 AI 编程助手\n", version)
		fmt.Printf("模型: %s | Ollama: %s\n", cfg.Ollama.Model, cfg.Ollama.Host)
		fmt.Println("输入消息后按 Enter 发送，Ctrl+C 中止生成，Ctrl+D 退出")
		fmt.Println(strings.Repeat("─", 60))
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 支持大输入

	for {
		fmt.Print("\n> ")

		if !scanner.Scan() {
			// EOF (Ctrl+D)
			fmt.Println("\n再见！")
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// 处理内置命令
		if handled := handleSlashCommand(input, a, cfg); handled {
			continue
		}

		// 发送给 Agent
		runAgentTurn(ctx, a, input)
	}
}

// runAgentTurn 执行一次 Agent 对话轮次
func runAgentTurn(ctx context.Context, a *agent.Agent, userMsg string) {
	ch := a.Prompt(ctx, userMsg)

	fmt.Println()
	var assistantBuf strings.Builder
	inToolCall := false

	for event := range ch {
		switch event.Type {
		case agent.AgentEventDelta:
			fmt.Print(event.Delta)
			assistantBuf.WriteString(event.Delta)

		case agent.AgentEventToolCall:
			if !inToolCall {
				if assistantBuf.Len() > 0 {
					fmt.Println()
				}
				inToolCall = true
			}
			fmt.Printf("\n[执行工具: %s]\n", event.ToolName)
			if event.ToolArgs != "" && event.ToolArgs != "{}" {
				// 只显示关键参数
				fmt.Printf("  参数: %s\n", truncate(event.ToolArgs, 200))
			}

		case agent.AgentEventToolResult:
			inToolCall = false
			result := strings.TrimSpace(event.ToolResult)
			if result != "" {
				lines := strings.Split(result, "\n")
				if len(lines) > 10 {
					// 只显示前 10 行
					preview := strings.Join(lines[:10], "\n")
					fmt.Printf("[工具结果]\n%s\n... (%d 行)\n", preview, len(lines))
				} else {
					fmt.Printf("[工具结果]\n%s\n", result)
				}
			}
			fmt.Println()

		case agent.AgentEventEnd:
			if assistantBuf.Len() > 0 {
				fmt.Println()
			}

		case agent.AgentEventError:
			if event.Err != nil && event.Err != context.Canceled {
				fmt.Fprintf(os.Stderr, "\n[错误]: %v\n", event.Err)
			}
		}
	}
}

// handleSlashCommand 处理 slash 命令，返回是否已处理
func handleSlashCommand(input string, a *agent.Agent, cfg config.Config) bool {
	if !strings.HasPrefix(input, "/") {
		return false
	}

	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/help":
		fmt.Println(`可用命令:
  /help          显示帮助
  /model <name>  切换模型
  /clear         清空对话历史
  /exit, /quit   退出`)
		return true

	case "/model":
		if len(parts) < 2 {
			fmt.Printf("当前模型: %s\n", a.Model())
		} else {
			newModel := parts[1]
			a.SetModel(newModel)
			fmt.Printf("已切换到模型: %s\n", newModel)
		}
		return true

	case "/clear":
		a.ClearMessages()
		fmt.Println("对话历史已清空")
		return true

	case "/exit", "/quit":
		fmt.Println("再见！")
		os.Exit(0)
		return true

	default:
		fmt.Printf("未知命令: %s（输入 /help 查看帮助）\n", cmd)
		return true
	}
}

// runPrintMode 非交互模式：从 stdin 读取，处理后输出到 stdout
func runPrintMode(ctx context.Context, a *agent.Agent) {
	scanner := bufio.NewScanner(os.Stdin)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	input := strings.Join(lines, "\n")

	if strings.TrimSpace(input) == "" {
		fmt.Fprintln(os.Stderr, "错误: stdin 为空")
		os.Exit(1)
	}

	ch := a.Prompt(ctx, input)
	for event := range ch {
		switch event.Type {
		case agent.AgentEventDelta:
			fmt.Print(event.Delta)
		case agent.AgentEventError:
			if event.Err != nil {
				fmt.Fprintf(os.Stderr, "错误: %v\n", event.Err)
				os.Exit(1)
			}
		}
	}
	fmt.Println()
}

// fatal 打印错误并退出
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
