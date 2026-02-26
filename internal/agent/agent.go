package agent

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/coderyrh/gopi/internal/llm"
)

// Agent 维护会话状态，提供对话 API
type Agent struct {
	mu       sync.Mutex
	client   LLMClient
	executor ToolExecutor
	config   AgentLoopConfig
	messages []llm.Message

	// 当前是否正在流式输出
	streaming atomic.Bool

	// 用于中止当前生成
	cancelFn    context.CancelFunc
	cancelID    uint64 // 唯一标识当前生成
	nextCancelID uint64
}

// NewAgent 创建一个新的 Agent
func NewAgent(client LLMClient, executor ToolExecutor, config AgentLoopConfig) *Agent {
	return &Agent{
		client:   client,
		executor: executor,
		config:   config,
		messages: make([]llm.Message, 0),
	}
}

// SetSystemMessage 设置系统消息
func (a *Agent) SetSystemMessage(msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.SystemMsg = msg
}

// AddMessage 向历史消息中追加一条消息
func (a *Agent) AddMessage(msg llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, msg)
}

// Messages 返回当前消息历史（快照）
func (a *Agent) Messages() []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]llm.Message, len(a.messages))
	copy(out, a.messages)
	return out
}

// ClearMessages 清空消息历史
func (a *Agent) ClearMessages() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = make([]llm.Message, 0)
}

// IsStreaming 返回是否正在流式输出
func (a *Agent) IsStreaming() bool {
	return a.streaming.Load()
}

// Abort 中止当前正在进行的生成
func (a *Agent) Abort() {
	a.mu.Lock()
	cancel := a.cancelFn
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// Prompt 发送用户消息并开始生成，返回事件 channel
// 调用者负责消费 channel 直到关闭
func (a *Agent) Prompt(parentCtx context.Context, userMsg string) <-chan AgentEvent {
	a.mu.Lock()

	// 如果已有正在进行的生成，先中止
	if a.cancelFn != nil {
		a.cancelFn()
	}

	ctx, cancel := context.WithCancel(parentCtx)
	a.nextCancelID++
	myID := a.nextCancelID
	a.cancelFn = cancel
	a.cancelID = myID

	// 追加用户消息
	a.messages = append(a.messages, llm.Message{
		Role:    "user",
		Content: userMsg,
	})

	// 复制当前消息用于本次 loop
	msgs := make([]llm.Message, len(a.messages))
	copy(msgs, a.messages)
	config := a.config

	a.mu.Unlock()

	a.streaming.Store(true)

	outCh := make(chan AgentEvent, 64)

	go func() {
		defer func() {
			a.streaming.Store(false)
			a.mu.Lock()
			if a.cancelID == myID {
				a.cancelFn = nil
			}
			a.mu.Unlock()
		}()
		defer close(outCh)

		innerCh := RunLoop(ctx, msgs, config, a.client, a.executor)

		for event := range innerCh {
			outCh <- event
		}
	}()

	return outCh
}

// PromptSync 同步版本的 Prompt，收集所有事件后返回
// 主要用于测试
func (a *Agent) PromptSync(ctx context.Context, userMsg string) ([]AgentEvent, error) {
	ch := a.Prompt(ctx, userMsg)
	var events []AgentEvent
	for e := range ch {
		events = append(events, e)
		if e.Type == AgentEventError && e.Err != nil {
			return events, e.Err
		}
	}
	return events, nil
}

// Model 返回当前使用的模型名
func (a *Agent) Model() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config.Model
}

// SetModel 设置模型
func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.Model = model
}
