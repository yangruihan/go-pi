package llm

import "encoding/json"

// EventType 表示流式事件的类型
type EventType string

const (
	EventMessageStart  EventType = "message_start"
	EventMessageDelta  EventType = "message_delta"
	EventMessageEnd    EventType = "message_end"
	EventToolCallStart EventType = "tool_call_start"
	EventToolCallEnd   EventType = "tool_call_end"
	EventError         EventType = "error"
)

// Message 表示一条对话消息
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Images     []string   `json:"images,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Tool 表示可调用的工具定义
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction 工具函数的定义
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall 表示一次工具调用
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction 工具调用的函数部分
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Event 表示一个流式事件
type Event struct {
	Type    EventType
	Delta   string    // 文本增量（message_delta 时使用）
	Message *Message  // 完整消息（message_end 时使用）
	Tool    *ToolCall // 工具调用（tool_call_* 时使用）
	Err     error
}

// ChatRequest 表示一次聊天请求
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
}
