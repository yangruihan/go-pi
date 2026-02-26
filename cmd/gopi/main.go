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
	"github.com/coderyrh/gopi/internal/session"
	"github.com/coderyrh/gopi/internal/tools"
)

const version = "0.1.0"

func main() {
	// 解析命令行参数
	var (
		model     = flag.String("m", "", "指定模型（默认使用配置文件中的模型）")
		modelLong = flag.String("model", "", "指定模型（默认使用配置文件中的模型）")
		host      = flag.String("host", "", "Ollama 主机地址（默认 http://localhost:11434）")
		noTools   = flag.Bool("no-tools", false, "禁用工具，纯对话模式")
		cont      = flag.Bool("c", false, "继续最近一次会话")
		contLong  = flag.Bool("continue", false, "继续最近一次会话")
		sessionID = flag.String("s", "", "打开指定会话 ID")
		sessionLong = flag.String("session", "", "打开指定会话 ID")
		printVer  = flag.Bool("version", false, "显示版本信息")
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
	selectedModel := *model
	if *modelLong != "" {
		selectedModel = *modelLong
	}
	if selectedModel != "" {
		cfg.Ollama.Model = selectedModel
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
		registry.Register(tools.NewWriteTool())
		registry.Register(tools.NewEditTool())
		registry.Register(tools.NewGrepTool())
		registry.Register(tools.NewFindTool())
		registry.Register(tools.NewLSTool())
	}

	// 初始化会话管理
	sessionsRoot, err := session.DefaultSessionsRoot()
	if err != nil {
		fatal("初始化会话目录失败: %v", err)
	}
	manager := session.NewSessionManager(sessionsRoot)

	var loaded *session.LoadedSession
	selectedSession := *sessionID
	if *sessionLong != "" {
		selectedSession = *sessionLong
	}
	shouldContinue := *cont || *contLong

	cwd, _ := os.Getwd()
	if selectedSession != "" {
		loaded, err = manager.LoadByID(cwd, selectedSession)
		if err != nil {
			fatal("加载指定会话失败: %v", err)
		}
	} else if shouldContinue {
		loaded, err = manager.Continue(cwd)
		if err != nil && !os.IsNotExist(err) {
			fatal("继续会话失败: %v", err)
		}
	}

	sess, err := session.NewAgentSession(cfg, client, registry, manager, loaded, buildSystemMessage())
	if err != nil {
		fatal("创建会话失败: %v", err)
	}

	if *printMode {
		runPrintMode(ctx, sess)
		return
	}

	// 交互式模式
	runInteractive(ctx, sess, cfg, manager)
}

// buildSystemMessage 构建系统提示词
func buildSystemMessage() string {
	cwd, _ := os.Getwd()
	return fmt.Sprintf(`你是 Gopi，一个运行在本地的 AI 编程助手。

当前工作目录: %s
操作系统: %s

你有以下工具可以使用:
- bash: 执行 shell 命令
- read_file / write_file / edit_file: 读写与精确编辑文件
- grep_search / find_files / list_dir: 搜索与文件遍历

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
func runInteractive(ctx context.Context, sess session.Session, cfg config.Config, manager *session.SessionManager) {
	// 设置信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigCh {
			if sig == syscall.SIGINT {
				if sess.IsStreaming() {
					fmt.Println("\n\n[已中止生成]")
					sess.Abort()
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
		if handled := handleSlashCommand(input, sess, cfg, manager); handled {
			continue
		}

		// 发送给 Agent
		runAgentTurn(ctx, sess, input)
	}
}

// runAgentTurn 执行一次 Agent 对话轮次
func runAgentTurn(_ context.Context, sess session.Session, userMsg string) {
	unsubscribe := sess.Subscribe(func(event agent.AgentEvent) {
		handleOutputEvent(event)
	})
	defer unsubscribe()

	fmt.Println()
	if err := sess.Prompt(userMsg); err != nil {
		if err != context.Canceled {
			fmt.Fprintf(os.Stderr, "\n[错误]: %v\n", err)
		}
	}
	fmt.Println()
}

// handleSlashCommand 处理 slash 命令，返回是否已处理
func handleSlashCommand(input string, sess session.Session, cfg config.Config, manager *session.SessionManager) bool {
	if !strings.HasPrefix(input, "/") {
		return false
	}

	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/help":
		fmt.Println(`可用命令:
  /help          显示帮助
  /session       查看当前会话与历史
  /model <name>  切换模型
  /clear         清空对话历史
  /exit, /quit   退出`)
		return true

	case "/session":
		cwd, _ := os.Getwd()
		list, err := manager.List(cwd)
		if err != nil {
			fmt.Printf("读取会话列表失败: %v\n", err)
			return true
		}
		fmt.Printf("当前会话: %s\n", sess.SessionID())
		if len(list) == 0 {
			fmt.Println("暂无历史会话")
			return true
		}
		fmt.Println("最近会话:")
		max := 10
		if len(list) < max {
			max = len(list)
		}
		for i := 0; i < max; i++ {
			fmt.Printf("  - %s (%s)\n", list[i].ID, list[i].UpdatedAt.Format("2006-01-02 15:04:05"))
		}
		return true

	case "/model":
		if len(parts) < 2 {
			fmt.Printf("当前模型: %s\n", sess.Model())
		} else {
			newModel := parts[1]
			if err := sess.SetModel(newModel); err != nil {
				fmt.Printf("切换模型失败: %v\n", err)
			} else {
				fmt.Printf("已切换到模型: %s\n", newModel)
			}
		}
		return true

	case "/clear":
		sess.ClearMessages()
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

func handleOutputEvent(event agent.AgentEvent) {
	switch event.Type {
	case agent.AgentEventDelta:
		fmt.Print(event.Delta)
	case agent.AgentEventToolCall:
		fmt.Printf("\n[执行工具: %s]\n", event.ToolName)
		if event.ToolArgs != "" && event.ToolArgs != "{}" {
			fmt.Printf("  参数: %s\n", truncate(event.ToolArgs, 200))
		}
	case agent.AgentEventToolResult:
		result := strings.TrimSpace(event.ToolResult)
		if result != "" {
			lines := strings.Split(result, "\n")
			if len(lines) > 10 {
				preview := strings.Join(lines[:10], "\n")
				fmt.Printf("[工具结果]\n%s\n... (%d 行)\n", preview, len(lines))
			} else {
				fmt.Printf("[工具结果]\n%s\n", result)
			}
		}
		fmt.Println()
	}
}

// runPrintMode 非交互模式：从 stdin 读取，处理后输出到 stdout
func runPrintMode(_ context.Context, sess session.Session) {
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

	unsubscribe := sess.Subscribe(func(event agent.AgentEvent) {
		if event.Type == agent.AgentEventDelta {
			fmt.Print(event.Delta)
		}
	})
	defer unsubscribe()

	if err := sess.Prompt(input); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
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
