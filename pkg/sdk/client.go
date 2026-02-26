package sdk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/coderyrh/gopi/internal/agent"
	"github.com/coderyrh/gopi/internal/config"
	"github.com/coderyrh/gopi/internal/llm"
	"github.com/coderyrh/gopi/internal/prompt"
	"github.com/coderyrh/gopi/internal/session"
	"github.com/coderyrh/gopi/internal/skills"
	"github.com/coderyrh/gopi/internal/tools"
)

type Options struct {
	CWD            string
	Model          string
	Provider       string
	Host           string
	APIBase        string
	APIKey         string
	NoTools        bool
	ContinueLatest bool
	SessionID      string
}

type Client struct {
	sess     session.Session
	bashTool *tools.BashTool
	mu       sync.Mutex
}

func New(opts Options) (*Client, error) {
	cwd := strings.TrimSpace(opts.CWD)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	cfg, _, err := config.LoadWithSources(cwd)
	if err != nil {
		cfg = config.Default()
	}

	if v := strings.TrimSpace(opts.Model); v != "" {
		cfg.Ollama.Model = v
	}
	if v := strings.TrimSpace(opts.Provider); v != "" {
		cfg.LLM.Provider = strings.ToLower(v)
	}
	if v := strings.TrimSpace(opts.Host); v != "" {
		cfg.Ollama.Host = v
	}
	if v := strings.TrimSpace(opts.APIBase); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := strings.TrimSpace(opts.APIKey); v != "" {
		cfg.LLM.APIKey = v
	}
	if strings.TrimSpace(cfg.LLM.Provider) == "" {
		cfg.LLM.Provider = "ollama"
	}

	var chatClient agent.LLMClient
	switch cfg.LLM.Provider {
	case "openai":
		base := strings.TrimSpace(cfg.LLM.BaseURL)
		if base == "" {
			base = strings.TrimSpace(cfg.Ollama.Host)
		}
		oai, e := llm.NewOpenAIClient(base, cfg.LLM.APIKey)
		if e != nil {
			return nil, e
		}
		chatClient = oai
	default:
		client, e := llm.NewClient(cfg.Ollama.Host)
		if e != nil {
			return nil, e
		}
		chatClient = client
	}

	registry := tools.NewRegistry()
	var bashTool *tools.BashTool
	if !opts.NoTools {
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
			if dir, e := config.ConfigDir(); e == nil {
				defaultToolFile := filepath.Join(dir, "tools.yaml")
				if _, statErr := os.Stat(defaultToolFile); statErr == nil {
					toolFiles = append(toolFiles, defaultToolFile)
				}
			}
		}
		for _, tf := range toolFiles {
			loadedTools, e := tools.LoadCustomToolsFromYAML(tf)
			if e != nil {
				continue
			}
			for _, tool := range loadedTools {
				registry.Register(tool)
			}
		}
	}

	sessionsRoot, err := session.DefaultSessionsRoot()
	if err != nil {
		if bashTool != nil {
			bashTool.Close()
		}
		return nil, err
	}
	manager := session.NewSessionManager(sessionsRoot)

	var loaded *session.LoadedSession
	if sid := strings.TrimSpace(opts.SessionID); sid != "" {
		loaded, err = manager.LoadByID(cwd, sid)
		if err != nil {
			if bashTool != nil {
				bashTool.Close()
			}
			return nil, err
		}
	} else if opts.ContinueLatest {
		loaded, err = manager.Continue(cwd)
		if err != nil && !os.IsNotExist(err) {
			if bashTool != nil {
				bashTool.Close()
			}
			return nil, err
		}
	}

	newCwd, _ := os.Getwd()
	if strings.TrimSpace(cwd) != "" {
		_ = os.Chdir(cwd)
	}
	systemMsg := buildSystemMessage(cfg, "print")
	if newCwd != "" {
		_ = os.Chdir(newCwd)
	}

	sess, err := session.NewAgentSession(cfg, chatClient, registry, manager, loaded, systemMsg)
	if err != nil {
		if bashTool != nil {
			bashTool.Close()
		}
		return nil, err
	}

	return &Client{sess: sess, bashTool: bashTool}, nil
}

func (c *Client) Ask(ctx context.Context, promptText string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if strings.TrimSpace(promptText) == "" {
		return "", fmt.Errorf("prompt cannot be empty")
	}

	var b strings.Builder
	var finalErr error
	unsubscribe := c.sess.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.AgentEventDelta:
			b.WriteString(event.Delta)
		case agent.AgentEventError:
			if event.Err != nil {
				finalErr = event.Err
			}
		}
	})
	defer unsubscribe()

	done := make(chan error, 1)
	go func() {
		done <- c.sess.Prompt(promptText)
	}()

	select {
	case err := <-done:
		if err != nil {
			return "", err
		}
		if finalErr != nil {
			return "", finalErr
		}
		return strings.TrimSpace(b.String()), nil
	case <-ctx.Done():
		c.sess.Abort()
		<-done
		return "", ctx.Err()
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != nil {
		_ = c.sess.Save()
	}
	if c.bashTool != nil {
		c.bashTool.Close()
	}
	return nil
}

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
	if _, err := os.Stat("/etc/os-release"); err == nil {
		return "Linux"
	}
	return "系统"
}
