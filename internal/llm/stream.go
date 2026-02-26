package llm

import (
	"context"
	"encoding/json"

	ollamaapi "github.com/ollama/ollama/api"
)

// Chat 发起流式聊天请求，返回 Event channel
// 调用方负责消费 channel 直到关闭
func (c *Client) Chat(ctx context.Context, req *ChatRequest) (<-chan Event, error) {
	ch := make(chan Event, 32)

	ollamaReq := &ollamaapi.ChatRequest{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
		Stream:   boolPtr(req.Stream),
	}

	go func() {
		defer close(ch)

		// 收集完整的 assistant 消息
		var fullContent string
		var toolCalls []ToolCall

		respFn := func(resp ollamaapi.ChatResponse) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if resp.Message.Content != "" {
				delta := resp.Message.Content
				fullContent += delta
				ch <- Event{
					Type:  EventMessageDelta,
					Delta: delta,
				}
			}

			// 处理工具调用
			for _, tc := range resp.Message.ToolCalls {
				argBytes, _ := json.Marshal(tc.Function.Arguments)
				call := ToolCall{
					ID:   tc.Function.Name,
					Type: "function",
					Function: ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: string(argBytes),
					},
				}
				toolCalls = append(toolCalls, call)
				ch <- Event{
					Type: EventToolCallStart,
					Tool: &call,
				}
			}

			if resp.Done {
				msg := &Message{
					Role:      "assistant",
					Content:   fullContent,
					ToolCalls: toolCalls,
				}
				ch <- Event{
					Type:    EventMessageEnd,
					Message: msg,
				}
			}

			return nil
		}

		if err := c.api.Chat(ctx, ollamaReq, respFn); err != nil {
			if ctx.Err() == nil {
				ch <- Event{Type: EventError, Err: err}
			}
		}
	}()

	return ch, nil
}

// convertMessages 将内部 Message 格式转换为 ollama API 格式
func convertMessages(msgs []Message) []ollamaapi.Message {
	out := make([]ollamaapi.Message, 0, len(msgs))
	for _, m := range msgs {
		om := ollamaapi.Message{
			Role:    m.Role,
			Content: m.Content,
		}
		// 将工具调用结果（role=tool）作为内容传递
		out = append(out, om)
	}
	return out
}

// convertTools 将内部 Tool 格式转换为 ollama API 格式
func convertTools(tools []Tool) ollamaapi.Tools {
	if len(tools) == 0 {
		return nil
	}
	out := make(ollamaapi.Tools, 0, len(tools))
	for _, t := range tools {
		var params ollamaapi.ToolFunctionParameters
		if len(t.Function.Parameters) > 0 {
			// 将 JSON Schema 直接反序列化到 Ollama 参数类型
			_ = json.Unmarshal(t.Function.Parameters, &params)
		}
		if params.Properties == nil {
			params.Properties = ollamaapi.NewToolPropertiesMap()
		}
		if params.Type == "" {
			params.Type = "object"
		}

		ot := ollamaapi.Tool{
			Type: t.Type,
			Function: ollamaapi.ToolFunction{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  params,
			},
		}
		out = append(out, ot)
	}
	return out
}

func boolPtr(b bool) *bool { return &b }
