package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coderyrh/gopi/internal/llm"
	"gopkg.in/yaml.v3"
)

type yamlToolsFile struct {
	Tools []yamlToolSpec `yaml:"tools"`
}

type yamlToolSpec struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Command     string `yaml:"command"`
	TimeoutSec  int    `yaml:"timeout_sec"`
}

type yamlShellTool struct {
	name        string
	description string
	command     string
	timeout     time.Duration
}

func (t *yamlShellTool) Name() string { return t.name }

func (t *yamlShellTool) Description() string { return t.description }

func (t *yamlShellTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"input": {Type: "string", Description: "传给脚本的文本参数"},
		},
	}
}

func (t *yamlShellTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var payload map[string]any
	_ = json.Unmarshal(args, &payload)
	input := ""
	if v, ok := payload["input"]; ok {
		input = fmt.Sprint(v)
	}
	cmdline := strings.ReplaceAll(t.command, "{{input}}", input)

	if t.timeout <= 0 {
		t.timeout = 15 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var cmd *exec.Cmd
	if isWindowsTool() {
		cmd = exec.CommandContext(cmdCtx, "cmd", "/C", cmdline)
	} else {
		cmd = exec.CommandContext(cmdCtx, "bash", "-c", cmdline)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return strings.TrimSpace(stdout.String()), fmt.Errorf("custom tool %s failed: %s", t.name, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("custom tool %s failed: %w", t.name, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func LoadCustomToolsFromYAML(path string) ([]Tool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg yamlToolsFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	tools := make([]Tool, 0, len(cfg.Tools))
	for _, spec := range cfg.Tools {
		name := strings.TrimSpace(spec.Name)
		cmd := strings.TrimSpace(spec.Command)
		if name == "" || cmd == "" {
			continue
		}
		timeout := 15 * time.Second
		if spec.TimeoutSec > 0 {
			timeout = time.Duration(spec.TimeoutSec) * time.Second
		}
		tools = append(tools, &yamlShellTool{
			name:        name,
			description: strings.TrimSpace(spec.Description),
			command:     cmd,
			timeout:     timeout,
		})
	}
	return tools, nil
}

func isWindowsTool() bool {
	return exec.Command("cmd", "/C", "echo ok").Run() == nil
}
