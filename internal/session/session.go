package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yangruihan/go-pi/internal/agent"
	"github.com/yangruihan/go-pi/internal/config"
	"github.com/yangruihan/go-pi/internal/extensions"
	"github.com/yangruihan/go-pi/internal/llm"
	"github.com/yangruihan/go-pi/internal/tools"
)

// Session 对外会话接口
type Session interface {
	Prompt(text string, opts ...PromptOpt) error
	Steer(text string) error
	FollowUp(text string) error
	Abort()
	ClearMessages()
	Subscribe(fn EventListener) func()

	Model() string
	SetModel(model string) error
	AppendSystemPrompt(text string) error
	IsStreaming() bool
	Messages() []llm.Message

	Save() error
	SessionFile() string
	SessionID() string
	ListSessions() ([]SessionMeta, error)
	ListEntries(limit int) ([]SessionEntryMeta, error)
	SwitchSession(id string) error
	Checkout(entryID string) (string, error)
}

type PromptOpt func(*promptOptions)
type promptOptions struct {
	images []string
}

func WithImages(paths []string) PromptOpt {
	return func(o *promptOptions) {
		if o == nil {
			return
		}
		o.images = append(o.images, paths...)
	}
}

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
	pendingJSONLLines [][]byte
	beforePromptHook string
	afterResponseHook string
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
		beforePromptHook: strings.TrimSpace(cfg.Ext.BeforePrompt),
		afterResponseHook: strings.TrimSpace(cfg.Ext.AfterResponse),
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

func (s *AgentSession) Prompt(text string, opts ...PromptOpt) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("prompt cannot be empty")
	}
	if strings.TrimSpace(s.beforePromptHook) != "" {
		if out, err := extensions.RunHook(s.beforePromptHook, text, 10*time.Second); err != nil {
			s.bus.Publish(agent.AgentEvent{Type: agent.AgentEventError, Err: err})
		} else if strings.TrimSpace(out) != "" {
			text = out
		}
	}

	po := &promptOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(po)
		}
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
	userMsg := llm.Message{EntryID: newEntryID(), Role: "user", Content: text, Images: po.images}
	working = append(working, userMsg)
	model := s.model
	s.mu.Unlock()

	if err := s.persistEntry(messageEntry{Type: entryMessage, ID: userMsg.EntryID, Role: userMsg.Role, Content: userMsg.Content, Images: userMsg.Images, Timestamp: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		s.bus.Publish(agent.AgentEvent{Type: agent.AgentEventError, Err: fmt.Errorf("会话写入失败（已缓冲，稍后重试）: %w", err)})
	}

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
	var lastAssistant string

	for ev := range eventCh {
		s.bus.Publish(ev)
		switch ev.Type {
		case agent.AgentEventDelta:
			turnBuilder.WriteString(ev.Delta)
		case agent.AgentEventToolResult:
			toolMsg := llm.Message{EntryID: newEntryID(), Role: "tool", Content: ev.ToolResult}
			working = append(working, toolMsg)
			if err := s.persistEntry(messageEntry{Type: entryMessage, ID: toolMsg.EntryID, Role: toolMsg.Role, Content: toolMsg.Content, Images: toolMsg.Images, Timestamp: time.Now().UTC().Format(time.RFC3339)}); err != nil {
				s.bus.Publish(agent.AgentEvent{Type: agent.AgentEventError, Err: fmt.Errorf("会话写入失败（已缓冲，稍后重试）: %w", err)})
			}
		case agent.AgentEventTurnEnd:
			assistantText := strings.TrimSpace(turnBuilder.String())
			if assistantText != "" {
				assistant := llm.Message{EntryID: newEntryID(), Role: "assistant", Content: assistantText}
				working = append(working, assistant)
				if err := s.persistEntry(messageEntry{Type: entryMessage, ID: assistant.EntryID, Role: assistant.Role, Content: assistant.Content, Images: assistant.Images, Timestamp: time.Now().UTC().Format(time.RFC3339)}); err != nil {
					s.bus.Publish(agent.AgentEvent{Type: agent.AgentEventError, Err: fmt.Errorf("会话写入失败（已缓冲，稍后重试）: %w", err)})
				}
				lastAssistant = assistantText
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
	if strings.TrimSpace(s.afterResponseHook) != "" && strings.TrimSpace(lastAssistant) != "" {
		if _, err := extensions.RunHook(s.afterResponseHook, lastAssistant, 10*time.Second); err != nil {
			s.bus.Publish(agent.AgentEvent{Type: agent.AgentEventError, Err: err})
		}
	}
	s.finishStreaming()
	return llm.EnhanceModelError(finalErr, model)
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

func (s *AgentSession) ClearMessages() {
	s.mu.Lock()
	s.messages = nil
	file := s.sessionFile
	s.mu.Unlock()
	_ = appendJSONL(file, messageEntry{Type: entryMessage, Role: "system", Content: "[会话已清空]", Timestamp: time.Now().UTC().Format(time.RFC3339)})
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

func (s *AgentSession) AppendSystemPrompt(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("system prompt addon cannot be empty")
	}
	s.mu.Lock()
	if s.systemMsg != "" {
		s.systemMsg += "\n\n"
	}
	s.systemMsg += text
	s.mu.Unlock()
	return nil
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

func (s *AgentSession) Save() error {
	s.mu.Lock()
	pending := append([][]byte(nil), s.pendingJSONLLines...)
	file := s.sessionFile
	s.mu.Unlock()
	for _, line := range pending {
		if err := appendJSONLLine(file, line); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.pendingJSONLLines = nil
	s.mu.Unlock()
	return nil
}

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

func (s *AgentSession) ListSessions() ([]SessionMeta, error) {
	return s.manager.List(s.cwd)
}

func (s *AgentSession) ListEntries(limit int) ([]SessionEntryMeta, error) {
	s.mu.Lock()
	file := s.sessionFile
	s.mu.Unlock()
	return s.manager.ListEntries(file, limit)
}

func (s *AgentSession) SwitchSession(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("session id cannot be empty")
	}
	loaded, err := s.manager.LoadByID(s.cwd, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streaming {
		return fmt.Errorf("cannot switch session while streaming")
	}
	s.sessionID = loaded.ID
	s.sessionFile = loaded.FilePath
	s.messages = append([]llm.Message{}, loaded.Messages...)
	if strings.TrimSpace(loaded.Model) != "" {
		s.model = loaded.Model
	}
	return nil
}

func (s *AgentSession) Checkout(entryID string) (string, error) {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return "", fmt.Errorf("entry id cannot be empty")
	}

	s.mu.Lock()
	if s.streaming {
		s.mu.Unlock()
		return "", fmt.Errorf("cannot checkout while streaming")
	}
	currentID := s.sessionID
	currentFile := s.sessionFile
	model := s.model
	s.mu.Unlock()

	loaded, err := s.manager.CheckoutFromEntry(s.cwd, currentID, currentFile, entryID, model)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.sessionID = loaded.ID
	s.sessionFile = loaded.FilePath
	s.messages = append([]llm.Message{}, loaded.Messages...)
	if strings.TrimSpace(loaded.Model) != "" {
		s.model = loaded.Model
	}
	s.mu.Unlock()
	return loaded.ID, nil
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

	s.bus.Publish(agent.AgentEvent{Type: agent.AgentEventToolCall, ToolName: "context_compaction", ToolArgs: fmt.Sprintf("{" + "\"before\":%d" + "}", s.estimator.EstimateMessages(messages))})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := CompactMessages(ctx, s.client, model, messages, keepRecent, s.estimator)
	if err != nil || res == nil {
		s.bus.Publish(agent.AgentEvent{Type: agent.AgentEventError, Err: err})
		return err
	}

	s.mu.Lock()
	s.messages = res.Messages
	s.mu.Unlock()

	if err := s.persistEntry(compactionEntry{
		Type: entryCompaction, Summary: res.Summary, TokenBefore: res.TokenBefore, TokenAfter: res.TokenAfter, Timestamp: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return err
	}
	s.bus.Publish(agent.AgentEvent{Type: agent.AgentEventToolResult, ToolName: "context_compaction", ToolResult: fmt.Sprintf("压缩完成：%d -> %d tokens", res.TokenBefore, res.TokenAfter)})
	return nil
}

func (s *AgentSession) persistEntry(entry any) error {
	line, err := marshalJSONLLine(entry)
	if err != nil {
		return err
	}

	s.mu.Lock()
	file := s.sessionFile
	pending := append([][]byte(nil), s.pendingJSONLLines...)
	s.mu.Unlock()

	for _, p := range pending {
		if err := appendJSONLLine(file, p); err != nil {
			s.mu.Lock()
			s.pendingJSONLLines = append(pending, line)
			s.mu.Unlock()
			return err
		}
	}
	if err := appendJSONLLine(file, line); err != nil {
		s.mu.Lock()
		s.pendingJSONLLines = append(pending, line)
		s.mu.Unlock()
		return err
	}

	s.mu.Lock()
	s.pendingJSONLLines = nil
	s.mu.Unlock()
	return nil
}
