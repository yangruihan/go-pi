package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coderyrh/gopi/internal/agent"
	"github.com/coderyrh/gopi/internal/config"
	"github.com/coderyrh/gopi/internal/llm"
	"github.com/coderyrh/gopi/internal/tools"
)

// Session 对外会话接口
type Session interface {
	Prompt(text string, opts ...PromptOpt) error
	Steer(text string) error
	FollowUp(text string) error
	Abort()
	Subscribe(fn EventListener) func()

	Model() string
	SetModel(model string) error
	IsStreaming() bool
	Messages() []llm.Message

	Save() error
	SessionFile() string
	SessionID() string
}

type PromptOpt func(*promptOptions)
type promptOptions struct{}

type AgentSession struct {
	mu         sync.Mutex
	cwd        string
	model      string
	systemMsg  string
	client     agent.LLMClient
	registry   *tools.Registry
	cfg        config.Config
	manager    *SessionManager
	sessionID  string
	sessionFile string
	messages   []llm.Message
	bus        *EventBus
	estimator  *TokenEstimator

	streaming bool
	cancelFn  context.CancelFunc
}

func NewAgentSession(
	cfg config.Config,
	client agent.LLMClient,
	registry *tools.Registry,
	manager *SessionManager,
	loaded *LoadedSession,
	systemMsg string,
) (*AgentSession, error) {
	cwd, _ := os.Getwd()
	s := &AgentSession{
		cwd:       cwd,
		model:     cfg.Ollama.Model,
		systemMsg: systemMsg,
		client:    client,
		registry:  registry,
		cfg:       cfg,
		manager:   manager,
		bus:       NewEventBus(),
		estimator: NewTokenEstimator(),
	}

	if loaded == nil {
		created, err := manager.Create(cwd, s.model)
		if err != nil {
			return nil, err
		}
		s.sessionID = created.ID
		s.sessionFile = created.FilePath
	} else {
		s.sessionID = loaded.ID
		s.sessionFile = loaded.FilePath
		s.messages = append(s.messages, loaded.Messages...)
		if strings.TrimSpace(loaded.Model) != "" {
			s.model = loaded.Model
		}
	}

	return s, nil
}

func (s *AgentSession) Prompt(text string, _ ...PromptOpt) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("prompt cannot be empty")
	}

	s.mu.Lock()
	if s.streaming {
		s.mu.Unlock()
		return fmt.Errorf("agent is already streaming")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFn = cancel
	s.streaming = true

	working := make([]llm.Message, len(s.messages), len(s.messages)+4)
	copy(working, s.messages)
	userMsg := llm.Message{Role: "user", Content: text}
	working = append(working, userMsg)
	_ = appendJSONL(s.sessionFile, messageEntry{Type: entryMessage, Role: userMsg.Role, Content: userMsg.Content, Timestamp: time.Now().UTC().Format(time.RFC3339)})

	model := s.model
	s.mu.Unlock()

	llmTools, err := s.registry.ToLLMTools()
	if err != nil {
		s.finishStreaming()
		return err
	}

	loopCfg := agent.AgentLoopConfig{
		Model: model,
		Tools: llmTools,
		MaxTurns: 30,
		SystemMsg: s.systemMsg,
	}

	eventCh := agent.RunLoop(ctx, working, loopCfg, s.client, s.registry)
	var turnBuilder strings.Builder
	var finalErr error

	for ev := range eventCh {
		s.bus.Publish(ev)
		switch ev.Type {
		case agent.AgentEventDelta:
			turnBuilder.WriteString(ev.Delta)
		case agent.AgentEventToolResult:
			toolMsg := llm.Message{Role: "tool", Content: ev.ToolResult}
			working = append(working, toolMsg)
			_ = appendJSONL(s.sessionFile, messageEntry{Type: entryMessage, Role: toolMsg.Role, Content: toolMsg.Content, Timestamp: time.Now().UTC().Format(time.RFC3339)})
		case agent.AgentEventTurnEnd:
			assistantText := strings.TrimSpace(turnBuilder.String())
			if assistantText != "" {
				assistant := llm.Message{Role: "assistant", Content: assistantText}
				working = append(working, assistant)
				_ = appendJSONL(s.sessionFile, messageEntry{Type: entryMessage, Role: assistant.Role, Content: assistant.Content, Timestamp: time.Now().UTC().Format(time.RFC3339)})
			}
			turnBuilder.Reset()
		case agent.AgentEventError:
			if ev.Err != nil && ev.Err != context.Canceled {
				finalErr = ev.Err
			}
		}
	}

	s.mu.Lock()
	s.messages = working
	s.mu.Unlock()

	_ = s.tryCompact()
	s.finishStreaming()
	return finalErr
}

func (s *AgentSession) Steer(text string) error {
	if s.IsStreaming() {
		s.Abort()
	}
	return s.Prompt("[Steer] " + text)
}

func (s *AgentSession) FollowUp(text string) error {
	return s.Prompt(text)
}

func (s *AgentSession) Abort() {
	s.mu.Lock()
	cancel := s.cancelFn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *AgentSession) Subscribe(fn EventListener) func() { return s.bus.Subscribe(fn) }

func (s *AgentSession) Model() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}

func (s *AgentSession) SetModel(model string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("model cannot be empty")
	}
	s.mu.Lock()
	s.model = model
	file := s.sessionFile
	s.mu.Unlock()
	return appendJSONL(file, modelChangeEntry{Type: entryModelChange, Model: model, Timestamp: time.Now().UTC().Format(time.RFC3339)})
}

func (s *AgentSession) IsStreaming() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streaming
}

func (s *AgentSession) Messages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]llm.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

func (s *AgentSession) Save() error { return nil }

func (s *AgentSession) SessionFile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionFile
}

func (s *AgentSession) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *AgentSession) finishStreaming() {
	s.mu.Lock()
	s.streaming = false
	s.cancelFn = nil
	s.mu.Unlock()
}

func (s *AgentSession) tryCompact() error {
	s.mu.Lock()
	messages := make([]llm.Message, len(s.messages))
	copy(messages, s.messages)
	model := s.model
	maxTokens := s.cfg.Context.MaxTokens
	threshold := s.cfg.Context.CompactionThreshold
	keepRecent := s.cfg.Context.KeepRecent
	s.mu.Unlock()

	if !ShouldCompact(messages, s.estimator, maxTokens, threshold) {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := CompactMessages(ctx, s.client, model, messages, keepRecent, s.estimator)
	if err != nil || res == nil {
		return err
	}

	s.mu.Lock()
	s.messages = res.Messages
	file := s.sessionFile
	s.mu.Unlock()

	return appendJSONL(file, compactionEntry{
		Type: entryCompaction, Summary: res.Summary, TokenBefore: res.TokenBefore, TokenAfter: res.TokenAfter, Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}
