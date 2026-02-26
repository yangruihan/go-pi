package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yangruihan/go-pi/internal/agent"
	"github.com/yangruihan/go-pi/internal/config"
	"github.com/yangruihan/go-pi/internal/extensions"
	"github.com/yangruihan/go-pi/internal/llm"
	"github.com/yangruihan/go-pi/internal/perf"
	"github.com/yangruihan/go-pi/internal/prompt"
	"github.com/yangruihan/go-pi/internal/session"
	"github.com/yangruihan/go-pi/internal/skills"
	"github.com/yangruihan/go-pi/internal/tools"
	gotui "github.com/yangruihan/go-pi/internal/tui"
)

const version = "0.1.0"

var modelProfiles []config.ModelProfile

func main() {
	// 解析命令行参数
	var (
		model     = flag.String("m", "", "指定模型（默认使用配置文件中的模型）")
		modelLong = flag.String("model", "", "指定模型（默认使用配置文件中的模型）")
		host      = flag.String("host", "", "Ollama 主机地址（默认 http://localhost:11434）")
		provider  = flag.String("provider", "", "LLM 后端：ollama|openai")
		apiBase   = flag.String("api-base", "", "OpenAI 兼容后端 base url（如 https://api.deepseek.com）")
		apiKey    = flag.String("api-key", "", "OpenAI 兼容后端 API Key")
		noTools   = flag.Bool("no-tools", false, "禁用工具，纯对话模式")
		cont      = flag.Bool("c", false, "继续最近一次会话")
		contLong  = flag.Bool("continue", false, "继续最近一次会话")
		sessionID = flag.String("s", "", "打开指定会话 ID")
		sessionLong = flag.String("session", "", "打开指定会话 ID")
		printVer  = flag.Bool("version", false, "显示版本信息")
		printMode = flag.Bool("print", false, "非交互模式，从 stdin 读取，输出到 stdout")
		tuiMode   = flag.Bool("tui", false, "启用 TUI 模式")
		perfMode  = flag.Bool("perf", false, "运行 Phase4.2 性能测量")
	)
	flag.Parse()

	if *printVer {
		fmt.Printf("gopi v%s\n", version)
		return
	}

	cwd, _ := os.Getwd()

	// 加载配置（home + project）
	cfg, loadSources, err := config.LoadWithSources(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告: 加载配置失败: %v，使用默认配置\n", err)
	}
	profiles, modelSources, perr := config.LoadModelProfilesWithSources("", cwd)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "警告: 读取 models.yaml 失败: %v\n", perr)
	}
	loadSources.ModelPaths = modelSources
	modelProfiles = profiles

	// 命令行参数覆盖配置
	selectedModel := *model
	if *modelLong != "" {
		selectedModel = *modelLong
	}
	if selectedModel != "" {
		cfg.Ollama.Model = selectedModel
	}
	if p, ok := config.ResolveModelProfile(selectedModel, profiles); ok {
		cfg.Ollama.Model = p.Model
		if p.Provider != "" {
			cfg.LLM.Provider = p.Provider
		}
		if p.BaseURL != "" {
			cfg.LLM.BaseURL = p.BaseURL
		}
		if k, err := p.ResolveAPIKey(); err == nil && strings.TrimSpace(k) != "" {
			cfg.LLM.APIKey = k
		}
	}
	if *host != "" {
		cfg.Ollama.Host = *host
	}
	if *provider != "" {
		cfg.LLM.Provider = strings.ToLower(strings.TrimSpace(*provider))
	}
	if *apiBase != "" {
		cfg.LLM.BaseURL = *apiBase
	}
	if *apiKey != "" {
		cfg.LLM.APIKey = *apiKey
	}
	if strings.TrimSpace(cfg.LLM.Provider) == "" {
		cfg.LLM.Provider = "ollama"
	}

	// 创建 LLM 客户端
	var (
		chatClient agent.LLMClient
		pingErr    error
		ollamaClient *llm.Client
	)
	ctx := context.Background()

	switch cfg.LLM.Provider {
	case "openai":
		base := strings.TrimSpace(cfg.LLM.BaseURL)
		if base == "" {
			base = strings.TrimSpace(cfg.Ollama.Host)
		}
		if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" && strings.TrimSpace(cfg.LLM.APIKey) == "" {
			cfg.LLM.APIKey = key
		}
		oai, e := llm.NewOpenAIClient(base, cfg.LLM.APIKey)
		if e != nil {
			fatal("创建 OpenAI 兼容客户端失败: %v", e)
		}
		chatClient = oai
		pingErr = oai.PingWithRetry(ctx, 3)
	default:
		client, e := llm.NewClient(cfg.Ollama.Host)
		if e != nil {
			fatal("创建 Ollama 客户端失败: %v", e)
		}
		chatClient = client
		ollamaClient = client
		pingErr = client.PingWithRetry(ctx, 3)
	}

	if pingErr != nil {
		if !*perfMode {
			fatal("无法连接到 LLM 后端(provider=%s): %v", cfg.LLM.Provider, pingErr)
		}
		fmt.Fprintf(os.Stderr, "警告: LLM 后端不可用，--perf 将跳过首 token 测量: %v\n", pingErr)
		ollamaClient = nil
	}

	if *perfMode {
		report := perf.Run(ctx, ollamaClient, cfg)
		printPerfReport(report)
		return
	}

	// 工具注册
	registry := tools.NewRegistry()
	var bashTool *tools.BashTool
	if !*noTools {
		bashTool = tools.NewBashTool()
		registry.Register(bashTool)
		registry.Register(tools.NewReadTool())
		registry.Register(tools.NewWriteTool())
		registry.Register(tools.NewEditTool())
		registry.Register(tools.NewGrepTool())
		registry.Register(tools.NewFindTool())
		registry.Register(tools.NewLSTool())

		toolFiles := append([]string{}, cfg.Ext.ToolFiles...)
		if len(toolFiles) == 0 {
			if dir, err := config.ConfigDir(); err == nil {
				defaultToolFile := filepath.Join(dir, "tools.yaml")
				if _, statErr := os.Stat(defaultToolFile); statErr == nil {
					toolFiles = append(toolFiles, defaultToolFile)
				}
			}
		}
		for _, tf := range toolFiles {
			tf = expandUserPath(tf)
			loadedTools, err := tools.LoadCustomToolsFromYAML(tf)
			if err != nil {
				fmt.Fprintf(os.Stderr, "警告: 加载扩展工具失败(%s): %v\n", tf, err)
				continue
			}
			for _, tool := range loadedTools {
				registry.Register(tool)
			}
		}
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

	runMode := "cli"
	if *printMode {
		runMode = "print"
	}
	if *tuiMode {
		runMode = "tui"
	}

	sess, err := session.NewAgentSession(cfg, chatClient, registry, manager, loaded, buildSystemMessage(cfg, runMode))
	if err != nil {
		fatal("创建会话失败: %v", err)
	}

	defer cleanupResources(sess, bashTool)

	if *printMode {
		runPrintMode(ctx, sess)
		return
	}

	if *tuiMode {
		if err := gotui.Run(sess, cfg); err != nil {
			fatal("启动 TUI 失败: %v", err)
		}
		return
	}

	// 交互式模式
	runInteractive(ctx, sess, cfg, manager, bashTool, loadSources)
}

// buildSystemMessage 构建系统提示词
func buildSystemMessage(cfg config.Config, runMode string) string {
	cwd, _ := os.Getwd()
	base := prompt.BuildBase(cwd, getOS(), cfg.LLM.Provider, runMode)
	agentMD := strings.TrimSpace(skills.LoadAgentMarkdown(cwd))
	return prompt.BuildWithTemplate(cfg.Prompt.TemplateFile, base, agentMD)
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
func runInteractive(ctx context.Context, sess session.Session, cfg config.Config, manager *session.SessionManager, bashTool *tools.BashTool, sources config.LoadSources) {
	// 设置信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	var exitOnce sync.Once
	cleanupAndExit := func(code int) {
		exitOnce.Do(func() {
			cleanupResources(sess, bashTool)
			os.Exit(code)
		})
	}

	go func() {
		for sig := range sigCh {
			if sig == syscall.SIGINT {
				if sess.IsStreaming() {
					fmt.Println("\n\n[已中止生成]")
					sess.Abort()
				} else {
					fmt.Println("\n再见！")
					cleanupAndExit(0)
				}
			} else {
				cleanupAndExit(0)
			}
		}
	}()

	// 欢迎信息
	if !cfg.TUI.QuietStartup {
		fmt.Printf("Gopi v%s — 本地 AI 编程助手\n", version)
		fmt.Printf("模型: %s | provider: %s\n", cfg.Ollama.Model, cfg.LLM.Provider)
		fmt.Printf("配置路径: %s\n", formatFinalPathOrDefault(sources.ConfigPaths, "(default 内置配置)"))
		fmt.Printf("模型配置路径: %s\n", formatFinalPathOrDefault(sources.ModelPaths, "(none)"))
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

func cleanupResources(sess session.Session, bashTool *tools.BashTool) {
	if sess != nil {
		_ = sess.Save()
	}
	if bashTool != nil {
		bashTool.Close()
	}
}

func printPerfReport(r perf.Report) {
	fmt.Println("=== Gopi Perf (Phase 4.2) ===")
	if r.FirstTokenError != "" {
		fmt.Printf("首 Token 延迟: N/A (%s)\n", r.FirstTokenError)
	} else {
		fmt.Printf("首 Token 延迟: %v\n", r.FirstTokenLatency)
	}
	fmt.Printf("TUI 帧耗时: avg=%v, max=%v\n", r.TUIFrameAvg, r.TUIFrameMax)
	if r.SessionLoad1000 > 0 {
		fmt.Printf("1000 条会话加载: %v\n", r.SessionLoad1000)
	} else {
		fmt.Println("1000 条会话加载: N/A")
	}

	fmt.Printf("瓶颈识别: %s\n", r.Bottleneck)

	if r.FirstTokenError == "" {
		if r.FirstTokenLatency < time.Second {
			fmt.Println("目标检查: 首 token < 1s ✅")
		} else {
			fmt.Println("目标检查: 首 token < 1s ❌")
		}
	}
	if r.TUIFrameMax < 16*time.Millisecond {
		fmt.Println("目标检查: TUI 单帧 < 16ms ✅")
	} else {
		fmt.Println("目标检查: TUI 单帧 < 16ms ❌")
	}
}

// runAgentTurn 执行一次 Agent 对话轮次
func runAgentTurn(_ context.Context, sess session.Session, userMsg string) {
	renderer := &cliOutputRenderer{}
	indicator := newThinkingIndicator()
	unsubscribe := sess.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.AgentEventStart, agent.AgentEventTurnStart:
		default:
			indicator.Stop()
		}
		handleOutputEvent(event, renderer)
	})
	defer unsubscribe()

	fmt.Println()
	if err := sess.Prompt(userMsg); err != nil {
		if err != context.Canceled {
			fmt.Fprintf(os.Stderr, "\n[错误]: %v\n", err)
		}
	}
	indicator.StopAndClear()
	renderer.flush()
	fmt.Println()
}

type thinkingIndicator struct {
	stopCh  chan struct{}
	doneCh  chan struct{}
	stopped atomic.Bool
}

func newThinkingIndicator() *thinkingIndicator {
	ti := &thinkingIndicator{
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go func() {
		defer close(ti.doneCh)
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ti.stopCh:
				return
			case <-ticker.C:
				fmt.Printf("\r[%s] 思考中...", frames[i%len(frames)])
				i++
			}
		}
	}()
	return ti
}

func (ti *thinkingIndicator) StopAndClear() {
	if ti == nil {
		return
	}
	ti.Stop()
	select {
	case <-ti.doneCh:
	default:
		<-ti.doneCh
	}
	fmt.Print("\r                \r")
}

func (ti *thinkingIndicator) Stop() {
	if ti == nil {
		return
	}
	if !ti.stopped.CompareAndSwap(false, true) {
		return
	}
	close(ti.stopCh)
}

// handleSlashCommand 处理 slash 命令，返回是否已处理
func handleSlashCommand(input string, sess session.Session, cfg config.Config, manager *session.SessionManager) bool {
	_ = cfg
	if !strings.HasPrefix(input, "/") {
		return false
	}

	parts := strings.Fields(input)
	cmd := parts[0]
	if strings.HasPrefix(cmd, "/skill:") {
		name := strings.TrimPrefix(cmd, "/skill:")
		cwd, _ := os.Getwd()
		content, err := skills.LoadProjectSkill(cwd, name)
		if err != nil {
			fmt.Printf("加载技能失败: %v\n", err)
			return true
		}
		if err := sess.AppendSystemPrompt("技能[" + name + "]:\n" + content); err != nil {
			fmt.Printf("应用技能失败: %v\n", err)
		} else {
			fmt.Printf("已加载技能: %s\n", name)
		}
		return true
	}

	switch cmd {
	case "/help":
		fmt.Println(`可用命令:
  /help          显示帮助
  /session       查看当前会话与历史
  /session entries 查看当前会话最近条目
  /model <name>  切换模型
  /checkout <entry-id> 从历史条目创建分支会话
  /skill:<name>  加载技能文件（.gopi/skills/<name>.md）
  /clear         清空对话历史
  /exit, /quit   退出`)
		extra := extensions.ListSlashCommands()
		if len(extra) > 0 {
			fmt.Println("扩展命令:")
			for _, c := range extra {
				fmt.Printf("  /%s  %s\n", c.Name, c.Description)
			}
		}
		return true

	case "/session":
		if len(parts) >= 2 && parts[1] == "entries" {
			entries, err := sess.ListEntries(20)
			if err != nil {
				fmt.Printf("读取会话条目失败: %v\n", err)
				return true
			}
			if len(entries) == 0 {
				fmt.Println("当前会话暂无可 checkout 条目")
				return true
			}
			fmt.Println("最近条目（可用于 /checkout <entry-id>）:")
			for _, e := range entries {
				fmt.Printf("  - %s [%s] %s\n", e.ID, e.Role, e.Preview)
			}
			return true
		}
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
			prefix := ""
			if list[i].ParentID != "" {
				prefix = "└─ "
			}
			fmt.Printf("  - %s%s (%s)\n", prefix, list[i].ID, list[i].UpdatedAt.Format("2006-01-02 15:04:05"))
		}
		return true

	case "/model":
		if len(parts) < 2 {
			fmt.Printf("当前模型: %s\n", sess.Model())
		} else {
			newModel := parts[1]
			if p, ok := config.ResolveModelProfile(newModel, modelProfiles); ok {
				newModel = p.Model
			}
			if err := sess.SetModel(newModel); err != nil {
				fmt.Printf("切换模型失败: %v\n", err)
			} else {
				fmt.Printf("已切换到模型: %s\n", newModel)
			}
		}
		return true

	case "/checkout":
		if len(parts) < 2 {
			fmt.Println("用法: /checkout <entry-id>")
			return true
		}
		newID, err := sess.Checkout(parts[1])
		if err != nil {
			fmt.Printf("checkout 失败: %v\n", err)
		} else {
			fmt.Printf("已创建并切换到分支会话: %s\n", newID)
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
		name := strings.TrimPrefix(cmd, "/")
		if out, ok, err := extensions.ExecuteSlashCommand(name, parts[1:]); ok {
			if err != nil {
				fmt.Printf("扩展命令执行失败: %v\n", err)
			} else if strings.TrimSpace(out) != "" {
				fmt.Println(out)
			}
			return true
		}
		fmt.Printf("未知命令: %s（输入 /help 查看帮助）\n", cmd)
		return true
	}
}

type cliOutputRenderer struct {
	lastToolCallSig string
	lastToolCallCnt int

	lastToolResultSig string
	lastToolResultCnt int
}

func (r *cliOutputRenderer) flushToolCallDup() {
	if r.lastToolCallCnt > 1 {
		fmt.Printf("[重复工具调用已省略] %s x%d\n", r.lastToolCallSig, r.lastToolCallCnt)
	}
	r.lastToolCallCnt = 0
	r.lastToolCallSig = ""
}

func (r *cliOutputRenderer) flushToolResultDup() {
	if r.lastToolResultCnt > 1 {
		fmt.Printf("[重复工具结果已省略] x%d\n", r.lastToolResultCnt)
	}
	r.lastToolResultCnt = 0
	r.lastToolResultSig = ""
}

func (r *cliOutputRenderer) flush() {
	r.flushToolCallDup()
	r.flushToolResultDup()
}

func handleOutputEvent(event agent.AgentEvent, renderer *cliOutputRenderer) {
	switch event.Type {
	case agent.AgentEventDelta:
		renderer.flushToolCallDup()
		renderer.flushToolResultDup()
		fmt.Print(event.Delta)
	case agent.AgentEventToolCall:
		sig := event.ToolName + "|" + strings.TrimSpace(event.ToolArgs)
		if sig == renderer.lastToolCallSig {
			renderer.lastToolCallCnt++
			return
		}
		renderer.flushToolCallDup()
		renderer.lastToolCallSig = sig
		renderer.lastToolCallCnt = 1

		fmt.Printf("\n[执行工具: %s]\n", event.ToolName)
		if event.ToolArgs != "" && event.ToolArgs != "{}" {
			fmt.Printf("  参数: %s\n", truncate(event.ToolArgs, 200))
		}
	case agent.AgentEventToolResult:
		renderer.flushToolCallDup()
		result := strings.TrimSpace(event.ToolResult)
		if result == renderer.lastToolResultSig {
			renderer.lastToolResultCnt++
			return
		}
		renderer.flushToolResultDup()
		renderer.lastToolResultSig = result
		renderer.lastToolResultCnt = 1

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
	case agent.AgentEventTurnEnd, agent.AgentEventEnd, agent.AgentEventError:
		renderer.flush()
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

func expandUserPath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
				return filepath.Join(home, p[2:])
			}
		}
	}
	return p
}

func formatFinalPathOrDefault(paths []string, fallback string) string {
	if len(paths) == 0 {
		return fallback
	}
	return paths[len(paths)-1]
}
