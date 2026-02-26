package agent

import (
	"context"
	"encoding/json"

	"github.com/coderyrh/gopi/internal/llm"
)

// AgentEventType Agent 事件类型
type AgentEventType string

const (
	AgentEventStart      AgentEventType = "agent_start"
	AgentEventEnd        AgentEventType = "agent_end"
	AgentEventTurnStart  AgentEventType = "turn_start"
	AgentEventTurnEnd    AgentEventType = "turn_end"
	AgentEventDelta      AgentEventType = "delta"       // 文本增量
	AgentEventToolCall   AgentEventType = "tool_call"   // 工具调用开始
	AgentEventToolResult AgentEventType = "tool_result" // 工具调用结果
	AgentEventError      AgentEventType = "error"
)

// AgentEvent Agent 输出的事件
type AgentEvent struct {
	Type       AgentEventType
	Delta      string         // 文本增量
	ToolName   string         // 工具名称
	ToolArgs   string         // 工具参数（JSON 字符串）
	ToolResult string         // 工具执行结果
	Message    *llm.Message   // 完整消息
	Err        error
}

// AgentState Agent 当前状态
type AgentState int

const (
	AgentStateIdle      AgentState = iota
	AgentStateStreaming             // 正在流式输出
	AgentStateToolExec             // 正在执行工具
)

// ToolExecutor 工具执行接口
type ToolExecutor interface {
	Execute(ctx context.Context, name string, args json.RawMessage) (string, error)
}

// AgentLoopConfig Agent Loop 配置
type AgentLoopConfig struct {
	Model      string
	Tools      []llm.Tool
	MaxTurns   int // 最大轮次，0 表示不限制
	SystemMsg  string
}

// DefaultLoopConfig 返回默认配置
func DefaultLoopConfig(model string) AgentLoopConfig {
	return AgentLoopConfig{
		Model:    model,
		MaxTurns: 20,
	}
}
