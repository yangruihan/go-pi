package session

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/yangruihan/go-pi/internal/config"
	"github.com/yangruihan/go-pi/internal/llm"
	"github.com/yangruihan/go-pi/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sequenceClient struct {
	mu       sync.Mutex
	handler  func(req *llm.ChatRequest) []llm.Event
	requests []*llm.ChatRequest
}

func (c *sequenceClient) Chat(_ context.Context, req *llm.ChatRequest) (<-chan llm.Event, error) {
	c.mu.Lock()
	copyReq := &llm.ChatRequest{Model: req.Model, Stream: req.Stream}
	copyReq.Tools = append(copyReq.Tools, req.Tools...)
	copyReq.Messages = append(copyReq.Messages, req.Messages...)
	c.requests = append(c.requests, copyReq)
	handler := c.handler
	c.mu.Unlock()

	events := handler(req)
	out := make(chan llm.Event, len(events))
	for _, event := range events {
		out <- event
	}
	close(out)
	return out, nil
}

func (c *sequenceClient) Requests() []*llm.ChatRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*llm.ChatRequest(nil), c.requests...)
}

type fakeIntegrationTool struct {
	called int
}

func (t *fakeIntegrationTool) Name() string { return "fake_tool" }

func (t *fakeIntegrationTool) Description() string { return "integration fake tool" }

func (t *fakeIntegrationTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"input": {Type: "string", Description: "tool input"},
		},
		Required: []string{"input"},
	}
}

func (t *fakeIntegrationTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	t.called++
	return "TOOL_RESULT_OK", nil
}

func TestIntegrationEndToEndPromptToolAndReply(t *testing.T) {
	root := t.TempDir()
	mgr := NewSessionManager(root)

	registry := tools.NewRegistry()
	tool := &fakeIntegrationTool{}
	registry.Register(tool)

	var call int
	client := &sequenceClient{handler: func(req *llm.ChatRequest) []llm.Event {
		call++
		if call == 1 {
			args, _ := json.Marshal(map[string]string{"input": "hello"})
			toolCall := llm.ToolCall{
				ID:   "tc-1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "fake_tool",
					Arguments: string(args),
				},
			}
			msg := &llm.Message{Role: "assistant", Content: "先调用工具", ToolCalls: []llm.ToolCall{toolCall}}
			return []llm.Event{
				{Type: llm.EventMessageDelta, Delta: "先调用工具"},
				{Type: llm.EventToolCallStart, Tool: &toolCall},
				{Type: llm.EventMessageEnd, Message: msg},
			}
		}

		require.GreaterOrEqual(t, len(req.Messages), 3)
		last := req.Messages[len(req.Messages)-1]
		require.Equal(t, "tool", last.Role)
		require.Contains(t, last.Content, "TOOL_RESULT_OK")

		msg := &llm.Message{Role: "assistant", Content: "工具执行完成，最终回复"}
		return []llm.Event{
			{Type: llm.EventMessageDelta, Delta: "工具执行完成，最终回复"},
			{Type: llm.EventMessageEnd, Message: msg},
		}
	}}

	cfg := config.Default()
	loaded, err := mgr.Create(mustGetwd(t), cfg.Ollama.Model)
	require.NoError(t, err)

	sess, err := NewAgentSession(cfg, client, registry, mgr, loaded, "")
	require.NoError(t, err)

	err = sess.Prompt("请帮我执行工具并回答")
	require.NoError(t, err)

	msgs := sess.Messages()
	require.GreaterOrEqual(t, len(msgs), 4)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Equal(t, "tool", msgs[2].Role)
	assert.Contains(t, msgs[2].Content, "TOOL_RESULT_OK")
	assert.Equal(t, "assistant", msgs[3].Role)
	assert.Contains(t, msgs[3].Content, "最终回复")
	assert.Equal(t, 1, tool.called)
}

func TestIntegrationSessionPersistenceRestoreConsistency(t *testing.T) {
	root := t.TempDir()
	mgr := NewSessionManager(root)
	registry := tools.NewRegistry()

	client := &sequenceClient{handler: func(req *llm.ChatRequest) []llm.Event {
		last := req.Messages[len(req.Messages)-1]
		content := "收到: " + strings.TrimSpace(last.Content)
		msg := &llm.Message{Role: "assistant", Content: content}
		return []llm.Event{
			{Type: llm.EventMessageDelta, Delta: content},
			{Type: llm.EventMessageEnd, Message: msg},
		}
	}}

	cfg := config.Default()
	loaded, err := mgr.Create(mustGetwd(t), cfg.Ollama.Model)
	require.NoError(t, err)

	sess, err := NewAgentSession(cfg, client, registry, mgr, loaded, "")
	require.NoError(t, err)

	require.NoError(t, sess.Prompt("第一条消息"))
	require.NoError(t, sess.Prompt("第二条消息"))
	require.NoError(t, sess.Save())

	current := sess.Messages()
	require.Len(t, current, 4)

	reloaded, err := mgr.LoadByID(mustGetwd(t), sess.SessionID())
	require.NoError(t, err)
	require.Len(t, reloaded.Messages, len(current))

	assert.Equal(t, current, reloaded.Messages)
}

func TestIntegrationCompactionMaintainsContinuity(t *testing.T) {
	root := t.TempDir()
	mgr := NewSessionManager(root)
	registry := tools.NewRegistry()

	client := &sequenceClient{handler: func(req *llm.ChatRequest) []llm.Event {
		if len(req.Messages) == 1 && strings.Contains(req.Messages[0].Content, "会话历史压缩助手") {
			summary := "当前任务：修复认证流程。\n已完成操作：定位 token 刷新问题。"
			msg := &llm.Message{Role: "assistant", Content: summary}
			return []llm.Event{{Type: llm.EventMessageDelta, Delta: summary}, {Type: llm.EventMessageEnd, Message: msg}}
		}

		reply := "继续执行：已基于已有上下文推进。"
		msg := &llm.Message{Role: "assistant", Content: reply}
		return []llm.Event{{Type: llm.EventMessageDelta, Delta: reply}, {Type: llm.EventMessageEnd, Message: msg}}
	}}

	cfg := config.Default()
	cfg.Context.MaxTokens = 40
	cfg.Context.CompactionThreshold = 0.2
	cfg.Context.KeepRecent = 2

	loaded, err := mgr.Create(mustGetwd(t), cfg.Ollama.Model)
	require.NoError(t, err)

	sess, err := NewAgentSession(cfg, client, registry, mgr, loaded, "")
	require.NoError(t, err)

	require.NoError(t, sess.Prompt("请分析认证模块并输出详细步骤，包含失败重试和审计日志建议"))
	require.NoError(t, sess.Prompt("继续补充边界条件与回滚策略"))

	compressed := sess.Messages()
	require.NotEmpty(t, compressed)
	assert.Equal(t, "system", compressed[0].Role)
	assert.Contains(t, compressed[0].Content, "历史摘要")
	assert.Contains(t, compressed[0].Content, "修复认证流程")

	require.NoError(t, sess.Prompt("基于刚才上下文继续给出迁移步骤"))

	requests := client.Requests()
	followupHasSummary := false
	for _, req := range requests {
		containsFollowup := false
		hasSummary := false
		for _, msg := range req.Messages {
			if msg.Role == "user" && strings.Contains(msg.Content, "基于刚才上下文继续给出迁移步骤") {
				containsFollowup = true
			}
			if msg.Role == "system" && strings.Contains(msg.Content, "历史摘要（自动压缩）") {
				hasSummary = true
			}
		}
		if containsFollowup && hasSummary {
			followupHasSummary = true
			break
		}
	}
	assert.True(t, followupHasSummary)
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	return cwd
}
