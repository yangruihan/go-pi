package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 全局配置
type Config struct {
	Ollama  OllamaConfig     `yaml:"ollama"`
	LLM     LLMConfig        `yaml:"llm"`
	Context ContextConfig    `yaml:"context"`
	Tools   ToolsConfig      `yaml:"tools"`
	TUI     TUIConfig        `yaml:"tui"`
	Prompt  PromptConfig     `yaml:"prompt"`
	Ext     ExtensionsConfig `yaml:"extensions"`
}

// PromptConfig 系统提示词模板配置
type PromptConfig struct {
	TemplateFile string `yaml:"template_file"`
}

// LLMConfig 通用 LLM 配置（支持 OpenAI 兼容后端）
type LLMConfig struct {
	Provider string `yaml:"provider"` // ollama | openai
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
}

// OllamaConfig Ollama 连接配置
type OllamaConfig struct {
	Host        string        `yaml:"host"`
	Model       string        `yaml:"model"`
	Timeout     time.Duration `yaml:"timeout"`
	ToolCalling string        `yaml:"tool_calling"` // auto | native | react
}

// ContextConfig 上下文配置
type ContextConfig struct {
	MaxTokens           int     `yaml:"max_tokens"`
	CompactionThreshold float64 `yaml:"compaction_threshold"`
	KeepRecent          int     `yaml:"keep_recent"`
}

// ToolsConfig 工具配置
type ToolsConfig struct {
	BashTimeout    time.Duration `yaml:"bash_timeout"`
	BashMaxOutput  int           `yaml:"bash_max_output"`
	ReadMaxLines   int           `yaml:"read_max_lines"`
	GrepMaxMatches int           `yaml:"grep_max_matches"`
}

// TUIConfig TUI 配置
type TUIConfig struct {
	Theme          string `yaml:"theme"`
	ShowTokenCount bool   `yaml:"show_token_count"`
	QuietStartup   bool   `yaml:"quiet_startup"`
}

// ExtensionsConfig 扩展配置
type ExtensionsConfig struct {
	ToolFiles     []string `yaml:"tool_files"`
	BeforePrompt  string   `yaml:"before_prompt"`
	AfterResponse string   `yaml:"after_response"`
}

// LoadSources 记录配置加载来源
type LoadSources struct {
	ConfigPaths []string
	ModelPaths  []string
}

var projectAIDirs = []string{".gopi", ".claude", ".pi"}

// Default 返回默认配置
func Default() Config {
	return Config{
		Ollama: OllamaConfig{
			Host:        "http://localhost:11434",
			Model:       "qwen3:8b",
			Timeout:     120 * time.Second,
			ToolCalling: "auto",
		},
		LLM: LLMConfig{
			Provider: "ollama",
			BaseURL:  "",
			APIKey:   "",
		},
		Context: ContextConfig{
			MaxTokens:           32768,
			CompactionThreshold: 0.60,
			KeepRecent:          8,
		},
		Tools: ToolsConfig{
			BashTimeout:    30 * time.Second,
			BashMaxOutput:  8192,
			ReadMaxLines:   500,
			GrepMaxMatches: 50,
		},
		TUI: TUIConfig{
			Theme:          "dark",
			ShowTokenCount: true,
			QuietStartup:   false,
		},
		Prompt: PromptConfig{
			TemplateFile: "",
		},
		Ext: ExtensionsConfig{
			ToolFiles:     nil,
			BeforePrompt:  "",
			AfterResponse: "",
		},
	}
}

// ConfigDir 返回配置目录路径
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gopi"), nil
}

// ProjectConfigPath 返回项目级配置路径：<cwd>/.gopi/config.yaml
func ProjectConfigPath(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Join(cwd, ".gopi", "config.yaml")
}

// ProjectConfigPaths 返回项目级配置候选路径（按优先顺序）
func ProjectConfigPaths(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	paths := make([]string, 0, len(projectAIDirs))
	for _, dir := range projectAIDirs {
		paths = append(paths, filepath.Join(cwd, dir, "config.yaml"))
	}
	return paths
}

// Load 从默认路径加载配置文件
// 如果文件不存在，返回默认配置
func Load() (Config, error) {
	cwd, _ := os.Getwd()
	cfg, _, err := LoadWithSources(cwd)
	return cfg, err
}

// LoadWithSources 按顺序加载配置并返回来源：默认值 -> home -> project
func LoadWithSources(cwd string) (Config, LoadSources, error) {
	cfg := Default()
	sources := LoadSources{}

	if dir, err := ConfigDir(); err == nil {
		homeCfg := filepath.Join(dir, "config.yaml")
		if loaded, err := mergeConfigFile(&cfg, homeCfg); err != nil {
			return cfg, sources, err
		} else if loaded {
			sources.ConfigPaths = append(sources.ConfigPaths, homeCfg)
		}
	}

	for _, projectCfg := range ProjectConfigPaths(cwd) {
		if loaded, err := mergeConfigFile(&cfg, projectCfg); err != nil {
			return cfg, sources, err
		} else if loaded {
			sources.ConfigPaths = append(sources.ConfigPaths, projectCfg)
		}
	}

	return cfg, sources, nil
}

func mergeConfigFile(cfg *Config, path string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read config %s: %w", path, err)
	}
	data, err = normalizeYAMLBytes(data)
	if err != nil {
		return false, fmt.Errorf("decode config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return false, fmt.Errorf("parse config %s: %w", path, err)
	}
	return true, nil
}

// Save 保存配置到默认路径
func Save(cfg Config) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0644)
}
