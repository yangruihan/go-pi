package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/coderyrh/gopi/internal/agent"
	"github.com/coderyrh/gopi/internal/llm"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

// CompactionResult 压缩结果
type CompactionResult struct {
	Summary     string
	TokenBefore int
	TokenAfter  int
	Messages    []llm.Message
}

// TokenEstimator token 估算器
type TokenEstimator struct {
	enc *tiktoken.Tiktoken
}

func NewTokenEstimator() *TokenEstimator {
	enc, _ := tiktoken.GetEncoding("cl100k_base")
	return &TokenEstimator{enc: enc}
}

func (e *TokenEstimator) EstimateText(text string) int {
	if e == nil || e.enc == nil {
		return len([]rune(text)) / 3
	}
	return len(e.enc.Encode(text, nil, nil))
}

func (e *TokenEstimator) EstimateMessages(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += 4
		total += e.EstimateText(m.Role)
		total += e.EstimateText(m.Content)
	}
	return total
}

func ShouldCompact(messages []llm.Message, estimator *TokenEstimator, maxTokens int, threshold float64) bool {
	if maxTokens <= 0 || threshold <= 0 {
		return false
	}
	return float64(estimator.EstimateMessages(messages)) > float64(maxTokens)*threshold
}

func CompactMessages(
	ctx context.Context,
	client agent.LLMClient,
	model string,
	messages []llm.Message,
	keepRecent int,
	estimator *TokenEstimator,
) (*CompactionResult, error) {
	if keepRecent <= 0 {
		keepRecent = 8
	}
	if len(messages) <= keepRecent+1 {
		return nil, nil
	}

	tokenBefore := estimator.EstimateMessages(messages)
	split := len(messages) - keepRecent
	cold := messages[:split]
	hot := messages[split:]

	historyText := buildHistoryText(cold)
	summary, err := summarizeHistory(ctx, client, model, historyText)
	if err != nil {
		summary = fallbackSummary(cold)
	}

	compacted := make([]llm.Message, 0, len(hot)+1)
	compacted = append(compacted, llm.Message{Role: "system", Content: "历史摘要（自动压缩）:\n" + summary})
	compacted = append(compacted, hot...)

	return &CompactionResult{
		Summary:     summary,
		TokenBefore: tokenBefore,
		TokenAfter:  estimator.EstimateMessages(compacted),
		Messages:    compacted,
	}, nil
}

func summarizeHistory(ctx context.Context, client agent.LLMClient, model, history string) (string, error) {
	prompt := "你是一个会话历史压缩助手。请将以下对话历史提炼成简洁的摘要。\n\n要求：\n1. 当前任务：用一句话说明用户的核心需求\n2. 已完成操作：列出已执行的关键操作（修改了哪些文件、发现了什么）\n3. 当前状态：当前代码/任务处于什么状态\n4. 重要发现：记录关键的技术细节、错误信息、决策依据\n5. 待续事项：还未完成的工作\n\n对话历史：\n" + history
	req := &llm.ChatRequest{Model: model, Messages: []llm.Message{{Role: "user", Content: prompt}}, Stream: true}
	events, err := client.Chat(ctx, req)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for ev := range events {
		if ev.Type == llm.EventMessageDelta {
			b.WriteString(ev.Delta)
		}
		if ev.Type == llm.EventError && ev.Err != nil {
			return "", ev.Err
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", fmt.Errorf("empty summary")
	}
	return b.String(), nil
}

func buildHistoryText(messages []llm.Message) string {
	var b strings.Builder
	for _, m := range messages {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		b.WriteString("[")
		b.WriteString(m.Role)
		b.WriteString("] ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func fallbackSummary(messages []llm.Message) string {
	if len(messages) == 0 {
		return "无历史内容"
	}
	last := messages[len(messages)-1]
	return "当前任务：延续之前的编码任务。\n已完成操作：已执行多轮对话与工具调用。\n当前状态：会话已压缩，保留最近上下文。\n重要发现：" + trim(last.Content, 300) + "\n待续事项：继续根据最新用户请求执行。"
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
