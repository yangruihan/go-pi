package llm

import (
	"encoding/json"
	"fmt"
)

// ToolSchema 用于构建工具的 JSON Schema
type ToolSchema struct {
	Name        string
	Description string
	Parameters  ToolParameters
}

// ToolParameters 工具参数的 JSON Schema
type ToolParameters struct {
	Type       string                     `json:"type"`
	Properties map[string]ToolProperty    `json:"properties"`
	Required   []string                   `json:"required,omitempty"`
}

// ToolProperty 单个参数属性
type ToolProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

// BuildTool 将 ToolSchema 转换为 LLM 可用的 Tool 定义
func BuildTool(schema ToolSchema) (Tool, error) {
	paramBytes, err := json.Marshal(schema.Parameters)
	if err != nil {
		return Tool{}, fmt.Errorf("marshal tool parameters: %w", err)
	}

	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        schema.Name,
			Description: schema.Description,
			Parameters:  json.RawMessage(paramBytes),
		},
	}, nil
}

// ParseToolCallArgs 解析工具调用的参数 JSON
func ParseToolCallArgs(args string, v any) error {
	if err := json.Unmarshal([]byte(args), v); err != nil {
		return fmt.Errorf("parse tool args: %w", err)
	}
	return nil
}
