package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coderyrh/gopi/internal/llm"
)

// LLMClient LLM 客户端接口（方便测试时 mock）
type LLMClient interface {
	Chat(ctx context.Context, req *llm.ChatRequest) (<-chan llm.Event, error)
}

// RunLoop 执行 Agent Loop，返回事件 channel
// messages 是初始消息列表（包含 system + user 消息）
// config 是 Agent 配置
// executor 是工具执行器
func RunLoop(
	ctx context.Context,
	messages []llm.Message,
	config AgentLoopConfig,
	client LLMClient,
	executor ToolExecutor,
) <-chan AgentEvent {
	ch := make(chan AgentEvent, 64)

	go func() {
		defer close(ch)

		// 复制消息切片，避免修改原始数据
		msgs := make([]llm.Message, len(messages))
		copy(msgs, messages)

		// 添加系统消息（如果有）
		if config.SystemMsg != "" {
			sysMsg := llm.Message{Role: "system", Content: config.SystemMsg}
			msgs = append([]llm.Message{sysMsg}, msgs...)
		}

		ch <- AgentEvent{Type: AgentEventStart}

		turns := 0
		for {
			// 检查上下文是否已取消
			select {
			case <-ctx.Done():
				ch <- AgentEvent{Type: AgentEventError, Err: ctx.Err()}
				return
			default:
			}

			// 检查最大轮次限制
			if config.MaxTurns > 0 && turns >= config.MaxTurns {
				ch <- AgentEvent{
					Type: AgentEventError,
					Err:  fmt.Errorf("reached max turns limit (%d)", config.MaxTurns),
				}
				return
			}

			turns++
			ch <- AgentEvent{Type: AgentEventTurnStart}

			// 调用 LLM
			req := &llm.ChatRequest{
				Model:    config.Model,
				Messages: msgs,
				Tools:    config.Tools,
				Stream:   true,
			}

			events, err := client.Chat(ctx, req)
			if err != nil {
				ch <- AgentEvent{Type: AgentEventError, Err: fmt.Errorf("chat: %w", err)}
				return
			}

			// 收集本轮 LLM 响应
			var fullMsg *llm.Message
			var toolCalls []llm.ToolCall

			for event := range events {
				switch event.Type {
				case llm.EventMessageDelta:
					ch <- AgentEvent{Type: AgentEventDelta, Delta: event.Delta}

				case llm.EventMessageEnd:
					fullMsg = event.Message
					if fullMsg != nil {
						toolCalls = fullMsg.ToolCalls
					}

				case llm.EventToolCallStart:
					if event.Tool != nil {
						ch <- AgentEvent{
							Type:     AgentEventToolCall,
							ToolName: event.Tool.Function.Name,
							ToolArgs: event.Tool.Function.Arguments,
						}
					}

				case llm.EventError:
					ch <- AgentEvent{Type: AgentEventError, Err: event.Err}
					return
				}
			}

			// 将 assistant 消息加入历史
			if fullMsg != nil {
				msgs = append(msgs, *fullMsg)
			}

			ch <- AgentEvent{Type: AgentEventTurnEnd}

			// 无工具调用则结束
			if len(toolCalls) == 0 {
				break
			}

			// 并发执行所有工具调用
			if executor != nil {
				results := execToolsConcurrent(ctx, toolCalls, executor)
				for _, res := range results {
					ch <- AgentEvent{
						Type:       AgentEventToolResult,
						ToolName:   res.name,
						ToolResult: res.result,
					}
					// 将工具结果加入消息历史
					msgs = append(msgs, llm.Message{
						Role:       "tool",
						Content:    res.result,
						ToolCallID: res.toolCallID,
					})
				}
			} else {
				// 没有工具执行器，结束循环
				break
			}
		}

		ch <- AgentEvent{Type: AgentEventEnd}
	}()

	return ch
}

// toolExecResult 工具执行结果
type toolExecResult struct {
	toolCallID string
	name       string
	result     string
	err        error
}

// execToolsConcurrent 并发执行所有工具调用
func execToolsConcurrent(ctx context.Context, calls []llm.ToolCall, executor ToolExecutor) []toolExecResult {
	results := make([]toolExecResult, len(calls))
	var wg sync.WaitGroup

	for i, call := range calls {
		wg.Add(1)
		go func(i int, call llm.ToolCall) {
			defer wg.Done()

			results[i].toolCallID = call.ID
			results[i].name = call.Function.Name

			var argsRaw json.RawMessage
			if call.Function.Arguments != "" {
				argsRaw = json.RawMessage(call.Function.Arguments)
			} else {
				argsRaw = json.RawMessage("{}")
			}

			result, err := executor.Execute(ctx, call.Function.Name, argsRaw)
			if err != nil {
				results[i].result = fmt.Sprintf("错误: %s", err.Error())
				results[i].err = err
			} else {
				results[i].result = result
			}
		}(i, call)
	}

	wg.Wait()
	return results
}
