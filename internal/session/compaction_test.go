package session

import (
	"context"
	"testing"

	"github.com/coderyrh/gopi/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLLMClient struct{}

func (f *fakeLLMClient) Chat(_ context.Context, _ *llm.ChatRequest) (<-chan llm.Event, error) {
	ch := make(chan llm.Event, 3)
	ch <- llm.Event{Type: llm.EventMessageDelta, Delta: "任务摘要：继续实现 Phase 2。"}
	msg := &llm.Message{Role: "assistant", Content: "任务摘要：继续实现 Phase 2。"}
	ch <- llm.Event{Type: llm.EventMessageEnd, Message: msg}
	close(ch)
	return ch, nil
}

func TestShouldCompact(t *testing.T) {
	est := NewTokenEstimator()
	msgs := []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	assert.False(t, ShouldCompact(msgs, est, 10000, 0.6))
	assert.True(t, ShouldCompact(msgs, est, 1, 0.6))
}

func TestCompactMessages(t *testing.T) {
	est := NewTokenEstimator()
	messages := []llm.Message{
		{Role: "user", Content: "需求 A"},
		{Role: "assistant", Content: "处理 A"},
		{Role: "user", Content: "需求 B"},
		{Role: "assistant", Content: "处理 B"},
		{Role: "user", Content: "需求 C"},
	}

	res, err := CompactMessages(context.Background(), &fakeLLMClient{}, "test-model", messages, 2, est)
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Contains(t, res.Summary, "任务摘要")
	assert.Less(t, len(res.Messages), len(messages)+1)
	assert.Equal(t, "system", res.Messages[0].Role)
	assert.Contains(t, res.Messages[0].Content, "历史摘要")
}
