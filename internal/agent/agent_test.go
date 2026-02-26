package agent

import (
	"context"
	"testing"
	"time"

	"github.com/coderyrh/gopi/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentPrompt 测试 Agent.Prompt 基本功能
func TestAgentPrompt(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			buildTextResponse("你好！"),
		},
	}
	executor := newMockExecutor()

	cfg := DefaultLoopConfig("test-model")
	a := NewAgent(client, executor, cfg)

	events, err := a.PromptSync(context.Background(), "你好")
	require.NoError(t, err)

	var fullText string
	for _, e := range events {
		if e.Type == AgentEventDelta {
			fullText += e.Delta
		}
	}
	assert.Equal(t, "你好！", fullText)
}

// TestAgentAbort 测试 Agent.Abort 中止功能
func TestAgentAbort(t *testing.T) {
	// 使用一个慢响应的 client
	slowClient := &slowLLMClient{delay: 200 * time.Millisecond}
	cfg := DefaultLoopConfig("test-model")
	a := NewAgent(slowClient, nil, cfg)

	ctx := context.Background()
	ch := a.Prompt(ctx, "slowly respond")

	// 稍等后中止
	go func() {
		time.Sleep(50 * time.Millisecond)
		a.Abort()
	}()

	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
	}

	// 验证 Agent 最终停止流式输出
	assert.False(t, a.IsStreaming())
}

// TestAgentModel 测试模型切换
func TestAgentModel(t *testing.T) {
	client := &mockLLMClient{}
	cfg := DefaultLoopConfig("initial-model")
	a := NewAgent(client, nil, cfg)

	assert.Equal(t, "initial-model", a.Model())

	a.SetModel("new-model")
	assert.Equal(t, "new-model", a.Model())
}

// TestAgentClearMessages 测试清空消息历史
func TestAgentClearMessages(t *testing.T) {
	client := &mockLLMClient{
		responses: []mockResponse{
			buildTextResponse("回复"),
		},
	}
	cfg := DefaultLoopConfig("test-model")
	a := NewAgent(client, nil, cfg)

	// 发送一条消息
	_, err := a.PromptSync(context.Background(), "测试")
	require.NoError(t, err)

	// 应该有用户消息在历史中
	msgs := a.Messages()
	assert.NotEmpty(t, msgs)

	// 清空
	a.ClearMessages()
	msgs = a.Messages()
	assert.Empty(t, msgs)
}

// TestAgentSetSystemMessage 测试系统消息设置
func TestAgentSetSystemMessage(t *testing.T) {
	var capturedReq *llm.ChatRequest
	capturingClient := &capturingLLMClient{
		onChat: func(req *llm.ChatRequest) {
			capturedReq = req
		},
		response: buildTextResponse("ok"),
	}

	cfg := DefaultLoopConfig("test-model")
	a := NewAgent(capturingClient, nil, cfg)
	a.SetSystemMessage("你是测试助手")

	_, err := a.PromptSync(context.Background(), "hello")
	require.NoError(t, err)

	require.NotNil(t, capturedReq)
	require.NotEmpty(t, capturedReq.Messages)
	assert.Equal(t, "system", capturedReq.Messages[0].Role)
	assert.Equal(t, "你是测试助手", capturedReq.Messages[0].Content)
}

// slowLLMClient 模拟慢响应的 LLM 客户端
type slowLLMClient struct {
	delay time.Duration
}

func (s *slowLLMClient) Chat(ctx context.Context, _ *llm.ChatRequest) (<-chan llm.Event, error) {
	ch := make(chan llm.Event, 2)
	go func() {
		defer close(ch)
		select {
		case <-time.After(s.delay):
			msg := llm.Message{Role: "assistant", Content: "slow response"}
			ch <- llm.Event{Type: llm.EventMessageDelta, Delta: "slow response"}
			ch <- llm.Event{Type: llm.EventMessageEnd, Message: &msg}
		case <-ctx.Done():
			ch <- llm.Event{Type: llm.EventError, Err: ctx.Err()}
		}
	}()
	return ch, nil
}

// capturingLLMClient 捕获请求的 mock 客户端
type capturingLLMClient struct {
	onChat   func(req *llm.ChatRequest)
	response mockResponse
}

func (c *capturingLLMClient) Chat(_ context.Context, req *llm.ChatRequest) (<-chan llm.Event, error) {
	if c.onChat != nil {
		c.onChat(req)
	}
	ch := make(chan llm.Event, len(c.response.events)+1)
	for _, e := range c.response.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}
