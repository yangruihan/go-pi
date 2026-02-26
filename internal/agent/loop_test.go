package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yangruihan/go-pi/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock LLM Client ---

// mockLLMClient 模拟 LLM 客户端，用于测试
type mockLLMClient struct {
	responses []mockResponse
	callCount int
}

type mockResponse struct {
	events []llm.Event
	err    error
}

func (m *mockLLMClient) Chat(_ context.Context, _ *llm.ChatRequest) (<-chan llm.Event, error) {
	if m.callCount >= len(m.responses) {
		// 返回空响应
		ch := make(chan llm.Event, 1)
		msg := llm.Message{Role: "assistant", Content: "（无更多响应）"}
		ch <- llm.Event{Type: llm.EventMessageEnd, Message: &msg}
		close(ch)
		m.callCount++
		return ch, nil
	}

	resp := m.responses[m.callCount]
	m.callCount++

	if resp.err != nil {
		return nil, resp.err
	}

	ch := make(chan llm.Event, len(resp.events)+1)
	for _, e := range resp.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// buildTextResponse 构建一个纯文本响应
func buildTextResponse(text string) mockResponse {
	msg := llm.Message{Role: "assistant", Content: text}
	return mockResponse{
		events: []llm.Event{
			{Type: llm.EventMessageDelta, Delta: text},
			{Type: llm.EventMessageEnd, Message: &msg},
		},
	}
}

// buildToolCallResponse 构建一个包含工具调用的响应
func buildToolCallResponse(text string, toolName string, args map[string]string) mockResponse {
	argsJSON, _ := json.Marshal(args)
	toolCall := llm.ToolCall{
		ID:   "test-tc-1",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      toolName,
			Arguments: string(argsJSON),
		},
	}
	msg := llm.Message{
		Role:      "assistant",
		Content:   text,
		ToolCalls: []llm.ToolCall{toolCall},
	}
	return mockResponse{
		events: []llm.Event{
			{Type: llm.EventMessageDelta, Delta: text},
			{Type: llm.EventToolCallStart, Tool: &toolCall},
			{Type: llm.EventMessageEnd, Message: &msg},
		},
	}
}

// --- Mock Tool Executor ---

type mockExecutor struct {
	results map[string]string
	errors  map[string]error
	calls   []executorCall
}

type executorCall struct {
	name string
	args string
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{
		results: make(map[string]string),
		errors:  make(map[string]error),
	}
}

func (m *mockExecutor) Execute(_ context.Context, name string, args json.RawMessage) (string, error) {
	m.calls = append(m.calls, executorCall{name: name, args: string(args)})
	if err, ok := m.errors[name]; ok {
		return "", err
	}
	if result, ok := m.results[name]; ok {
		return result, nil
	}
	return "ok", nil
}

// --- Tests ---

// TestLoopSingleTurn 测试单轮对话（无工具调用）
func TestLoopSingleTurn(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			buildTextResponse("你好！我是 Gopi。"),
		},
	}

	messages := []llm.Message{
		{Role: "user", Content: "你好"},
	}
	config := DefaultLoopConfig("test-model")

	ch := RunLoop(context.Background(), messages, config, client, nil)

	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
	}

	// 验证事件序列
	eventTypes := make([]AgentEventType, 0, len(events))
	for _, e := range events {
		eventTypes = append(eventTypes, e.Type)
	}

	assert.Contains(t, eventTypes, AgentEventStart)
	assert.Contains(t, eventTypes, AgentEventTurnStart)
	assert.Contains(t, eventTypes, AgentEventDelta)
	assert.Contains(t, eventTypes, AgentEventEnd)
	assert.NotContains(t, eventTypes, AgentEventError)

	// 验证文本内容
	var fullText string
	for _, e := range events {
		if e.Type == AgentEventDelta {
			fullText += e.Delta
		}
	}
	assert.Equal(t, "你好！我是 Gopi。", fullText)
}

// TestLoopToolCall 测试单次工具调用
func TestLoopToolCall(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			// 第一轮：调用工具
			buildToolCallResponse("我来查看一下文件", "read_file", map[string]string{"path": "test.go"}),
			// 第二轮：返回最终答案
			buildTextResponse("文件内容是..."),
		},
	}

	executor := newMockExecutor()
	executor.results["read_file"] = "package main\n\nfunc main() {}"

	messages := []llm.Message{
		{Role: "user", Content: "帮我看看 test.go"},
	}
	config := DefaultLoopConfig("test-model")

	ch := RunLoop(context.Background(), messages, config, client, executor)

	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
	}

	// 验证没有错误
	for _, e := range events {
		require.NotEqual(t, AgentEventError, e.Type, "unexpected error event: %v", e.Err)
	}

	// 验证工具被调用
	assert.Len(t, executor.calls, 1)
	assert.Equal(t, "read_file", executor.calls[0].name)

	// 验证工具调用事件
	var toolCallEvents []AgentEvent
	var toolResultEvents []AgentEvent
	for _, e := range events {
		if e.Type == AgentEventToolCall {
			toolCallEvents = append(toolCallEvents, e)
		}
		if e.Type == AgentEventToolResult {
			toolResultEvents = append(toolResultEvents, e)
		}
	}
	assert.Len(t, toolCallEvents, 1)
	assert.Equal(t, "read_file", toolCallEvents[0].ToolName)
	assert.Len(t, toolResultEvents, 1)
	assert.Contains(t, toolResultEvents[0].ToolResult, "package main")
}

// TestLoopMultiTurn 测试多轮工具调用
func TestLoopMultiTurn(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			buildToolCallResponse("先读取文件", "read_file", map[string]string{"path": "a.go"}),
			buildToolCallResponse("再执行命令", "bash", map[string]string{"command": "ls"}),
			buildTextResponse("完成了！"),
		},
	}

	executor := newMockExecutor()
	executor.results["read_file"] = "内容A"
	executor.results["bash"] = "a.go\nb.go"

	messages := []llm.Message{
		{Role: "user", Content: "帮我分析项目"},
	}
	config := DefaultLoopConfig("test-model")

	ch := RunLoop(context.Background(), messages, config, client, executor)

	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
	}

	// 验证工具被调用 2 次
	assert.Len(t, executor.calls, 2)

	assert.Equal(t, AgentEventEnd, events[len(events)-1].Type)
}

// TestLoopReActFallback 测试最小 ReAct fallback（无原生 tool call 事件）
func TestLoopReActFallback(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			buildTextResponse("Thought: 先读取文件\nAction: read_file\nAction Input: {\"path\":\"test.go\"}"),
			buildTextResponse("读取完成"),
		},
	}

	executor := newMockExecutor()
	executor.results["read_file"] = "package main"

	messages := []llm.Message{{Role: "user", Content: "读取 test.go"}}
	config := DefaultLoopConfig("test-model")

	ch := RunLoop(context.Background(), messages, config, client, executor)

	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
	}

	assert.Len(t, executor.calls, 1)
	assert.Equal(t, "read_file", executor.calls[0].name)

	hasToolCall := false
	hasToolResult := false
	for _, e := range events {
		if e.Type == AgentEventToolCall && e.ToolName == "read_file" {
			hasToolCall = true
		}
		if e.Type == AgentEventToolResult && e.ToolName == "read_file" {
			hasToolResult = true
		}
	}

	assert.True(t, hasToolCall)
	assert.True(t, hasToolResult)
}

func TestLoopReActFallbackSingleQuoteJSON(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			buildTextResponse("Thought: 先读取文件\nAction: read_file\nAction Input: {'path':'test.go',}"),
			buildTextResponse("读取完成"),
		},
	}

	executor := newMockExecutor()
	executor.results["read_file"] = "ok"

	ch := RunLoop(context.Background(), []llm.Message{{Role: "user", Content: "读取 test.go"}}, DefaultLoopConfig("test-model"), client, executor)
	for range ch {
	}

	assert.Len(t, executor.calls, 1)
	assert.Equal(t, "read_file", executor.calls[0].name)
	assert.JSONEq(t, `{"path":"test.go"}`, executor.calls[0].args)
}

func TestLoopReActFallbackFencedJSON(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			buildTextResponse("Thought: 先读取文件\nAction: read_file\nAction Input:\n```json\n{\n  \"path\": \"test.go\"\n}\n```"),
			buildTextResponse("读取完成"),
		},
	}

	executor := newMockExecutor()
	executor.results["read_file"] = "ok"

	ch := RunLoop(context.Background(), []llm.Message{{Role: "user", Content: "读取 test.go"}}, DefaultLoopConfig("test-model"), client, executor)
	for range ch {
	}

	assert.Len(t, executor.calls, 1)
	assert.Equal(t, "read_file", executor.calls[0].name)
	assert.JSONEq(t, `{"path":"test.go"}`, executor.calls[0].args)
}

// TestLoopContextCancellation 测试上下文取消
func TestLoopContextCancellation(t *testing.T) {
	// 创建一个 mock，Chat 会阻塞
	blockingClient := &blockingLLMClient{}

	ctx, cancel := context.WithCancel(context.Background())

	messages := []llm.Message{{Role: "user", Content: "hello"}}
	config := DefaultLoopConfig("test-model")

	ch := RunLoop(ctx, messages, config, blockingClient, nil)

	// 立即取消
	cancel()

	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
	}

	// 应该有错误事件
	hasError := false
	for _, e := range events {
		if e.Type == AgentEventError {
			hasError = true
			break
		}
	}
	assert.True(t, hasError, "expected error event after context cancellation")
}

// TestLoopMaxTurns 测试最大轮次限制
func TestLoopMaxTurns(t *testing.T) {
	// 每次都返回工具调用，会无限循环，应该被 MaxTurns 限制
	responses := make([]mockResponse, 10)
	for i := range responses {
		responses[i] = buildToolCallResponse("继续", "bash", map[string]string{"command": "echo hi"})
	}

	client := &mockLLMClient{responses: responses}
	executor := newMockExecutor()
	executor.results["bash"] = "hi"

	messages := []llm.Message{{Role: "user", Content: "无限循环测试"}}
	config := DefaultLoopConfig("test-model")
	config.MaxTurns = 3

	ch := RunLoop(context.Background(), messages, config, client, executor)

	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
	}

	// 应该在 MaxTurns 后出现错误
	hasError := false
	for _, e := range events {
		if e.Type == AgentEventError {
			hasError = true
			break
		}
	}
	assert.True(t, hasError, "expected error event when max turns reached")
}

// TestLoopLLMError 测试 LLM 返回错误
func TestLoopLLMError(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			{err: errors.New("model not found")},
		},
	}

	messages := []llm.Message{{Role: "user", Content: "hello"}}
	config := DefaultLoopConfig("test-model")

	ch := RunLoop(context.Background(), messages, config, client, nil)

	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
	}

	hasError := false
	for _, e := range events {
		if e.Type == AgentEventError && e.Err != nil {
			hasError = true
		}
	}
	assert.True(t, hasError)
}

// blockingLLMClient 一个会等待上下文取消的 mock client
type blockingLLMClient struct{}

func (b *blockingLLMClient) Chat(ctx context.Context, _ *llm.ChatRequest) (<-chan llm.Event, error) {
	ch := make(chan llm.Event, 1)
	go func() {
		defer close(ch)
		<-ctx.Done()
		ch <- llm.Event{Type: llm.EventError, Err: ctx.Err()}
	}()
	return ch, nil
}
