package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/yangruihan/go-pi/internal/llm"
)

// Tool 定义工具接口
type Tool interface {
	Name() string
	Description() string
	Schema() llm.ToolParameters
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry 工具注册表
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry 创建新的工具注册表
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register 注册一个工具
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get 获取工具，不存在则返回 false
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All 返回所有已注册的工具
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// ToLLMTools 将所有工具转换为 LLM 可用的 Tool 定义列表
func (r *Registry) ToLLMTools() ([]llm.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tool, err := llm.BuildTool(llm.ToolSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
		if err != nil {
			return nil, fmt.Errorf("build tool %q: %w", t.Name(), err)
		}
		out = append(out, tool)
	}
	return out, nil
}

// Execute 执行指定工具
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("tool %q not found", name)
	}
	return t.Execute(ctx, args)
}
