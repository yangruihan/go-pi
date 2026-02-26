package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coderyrh/gopi/internal/agent"
	"github.com/coderyrh/gopi/internal/config"
	"github.com/coderyrh/gopi/internal/session"
)

type agentEventMsg struct {
	event agent.AgentEvent
}

type promptDoneMsg struct {
	err error
}

type modalType int

const (
	modalNone modalType = iota
	modalSession
	modalModel
)

type AppModel struct {
	theme Theme

	sess    session.Session
	cfg     config.Config
	width   int
	height  int
	input   string
	history []string
	histPos int
	msgs    []chatMessage
	tools   []toolItem
	stream  bool
	tokens  int
	scroll  int
	expandTools bool
	lastErr string
	statusHint string
	compacting bool
	kittySupported bool

	modal modalType
	pickerIndex int
	sessionItems []session.SessionMeta
	modelItems []string

	eventCh chan tea.Msg

	unsubscribe func()
}

func NewAppModel(sess session.Session, cfg config.Config) AppModel {
	m := AppModel{
		theme:       DefaultTheme(),
		sess:        sess,
		cfg:         cfg,
		expandTools: true,
		kittySupported: detectKittySupport(),
		eventCh:     make(chan tea.Msg, 256),
	}
	m.unsubscribe = sess.Subscribe(func(ev agent.AgentEvent) {
		select {
		case m.eventCh <- agentEventMsg{event: ev}:
		default:
		}
	})
	for _, msg := range sess.Messages() {
		m.msgs = append(m.msgs, chatMessage{Role: msg.Role, Content: msg.Content})
	}
	m.tokens = estimateTokenLike(m.msgs)
	m.modelItems = buildModelItems(cfg, sess.Model())
	return m
}

func Run(sess session.Session, cfg config.Config) error {
	m := NewAppModel(sess, cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if fm, ok := finalModel.(AppModel); ok {
		if fm.unsubscribe != nil {
			fm.unsubscribe()
		}
	}
	return err
}

func (m AppModel) Init() tea.Cmd {
	return waitForEvent(m.eventCh)
}

func waitForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func runPrompt(sess session.Session, text string, images []string) tea.Cmd {
	return func() tea.Msg {
		if len(images) > 0 {
			err := sess.Prompt(text, session.WithImages(images))
			return promptDoneMsg{err: err}
		}
		err := sess.Prompt(text)
		return promptDoneMsg{err: err}
	}
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		return m, nil

	case agentEventMsg:
		ev := v.event
		switch ev.Type {
		case agent.AgentEventDelta:
			m.stream = true
			if len(m.msgs) == 0 || m.msgs[len(m.msgs)-1].Role != "assistant" {
				m.msgs = append(m.msgs, chatMessage{Role: "assistant", Content: ""})
			}
			m.msgs[len(m.msgs)-1].Content += ev.Delta
		case agent.AgentEventToolCall:
			if ev.ToolName == "context_compaction" {
				m.compacting = true
				m.statusHint = "[正在压缩上下文，请稍候...]"
			} else {
				m.tools = append(m.tools, toolItem{Name: ev.ToolName, Args: ev.ToolArgs})
			}
		case agent.AgentEventToolResult:
			if ev.ToolName == "context_compaction" {
				m.compacting = false
				m.statusHint = ev.ToolResult
			} else if len(m.tools) > 0 {
				m.tools[len(m.tools)-1].Output = ev.ToolResult
			}
		case agent.AgentEventError:
			if ev.Err != nil {
				m.lastErr = ev.Err.Error()
			}
			m.compacting = false
		case agent.AgentEventEnd:
			m.stream = false
		}
		m.tokens = estimateTokenLike(m.msgs)
		return m, waitForEvent(m.eventCh)

	case promptDoneMsg:
		m.stream = false
		if v.err != nil && v.err.Error() != "context canceled" {
			m.lastErr = v.err.Error()
		}
		m.tokens = estimateTokenLike(m.msgs)
		return m, nil

	case tea.KeyMsg:
		s := v.String()

		if m.modal != modalNone {
			switch s {
			case "esc":
				m.modal = modalNone
				return m, nil
			case "up":
				if m.pickerIndex > 0 {
					m.pickerIndex--
				}
				return m, nil
			case "down":
				max := 0
				if m.modal == modalSession {
					max = len(m.sessionItems)
				} else if m.modal == modalModel {
					max = len(m.modelItems)
				}
				if max > 0 && m.pickerIndex < max-1 {
					m.pickerIndex++
				}
				return m, nil
			case "enter":
				if m.modal == modalSession && len(m.sessionItems) > 0 {
					id := m.sessionItems[m.pickerIndex].ID
					if err := m.sess.SwitchSession(id); err != nil {
						m.lastErr = err.Error()
					} else {
						m.statusHint = "已切换会话: " + id
						m.msgs = nil
						for _, msg := range m.sess.Messages() {
							m.msgs = append(m.msgs, chatMessage{Role: msg.Role, Content: msg.Content})
						}
						m.tokens = estimateTokenLike(m.msgs)
					}
				}
				if m.modal == modalModel && len(m.modelItems) > 0 {
					model := m.modelItems[m.pickerIndex]
					if err := m.sess.SetModel(model); err != nil {
						m.lastErr = err.Error()
					} else {
						m.statusHint = "已切换模型: " + model
					}
				}
				m.modal = modalNone
				return m, nil
			}
			return m, nil
		}

		switch s {
		case "ctrl+c":
			if m.stream {
				m.sess.Abort()
				m.stream = false
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+l":
			m.scroll = 0
			m.lastErr = ""
			return m, nil
		case "ctrl+t":
			m.expandTools = !m.expandTools
			return m, nil
		case "ctrl+r":
			items, err := m.sess.ListSessions()
			if err != nil {
				m.lastErr = err.Error()
				return m, nil
			}
			m.sessionItems = items
			m.modal = modalSession
			m.pickerIndex = 0
			return m, nil
		case "ctrl+p":
			m.modelItems = buildModelItems(m.cfg, m.sess.Model())
			m.modal = modalModel
			m.pickerIndex = 0
			for i, model := range m.modelItems {
				if model == m.sess.Model() {
					m.pickerIndex = i
					break
				}
			}
			return m, nil
		case "enter":
			if m.stream {
				return m, nil
			}
			raw := strings.TrimSpace(m.input)
			text, images, missing := parseImageMentions(raw)
			if len(missing) > 0 {
				m.statusHint = "部分图片不存在: " + strings.Join(missing, ", ")
			}
			if len(images) > 0 {
				if m.kittySupported {
					m.statusHint = "已附带图片: " + strings.Join(images, ", ")
				} else {
					m.statusHint = "终端不支持 Kitty 图片协议，已降级为路径文本 + 图片附件"
				}
			}
			if text == "" {
				return m, nil
			}
			m.history = append(m.history, text)
			m.histPos = len(m.history)
			m.msgs = append(m.msgs, chatMessage{Role: "user", Content: text})
			m.input = ""
			m.stream = true
			return m, runPrompt(m.sess, text, images)
		case "shift+enter":
			m.input += "\n"
			return m, nil
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			return m, nil
		case "pgup":
			if m.scroll > 0 {
				m.scroll -= 10
				if m.scroll < 0 {
					m.scroll = 0
				}
			}
			return m, nil
		case "pgdown":
			m.scroll += 10
			return m, nil
		case "up":
			if len(m.history) == 0 {
				return m, nil
			}
			if m.histPos > 0 {
				m.histPos--
			}
			if m.histPos >= 0 && m.histPos < len(m.history) {
				m.input = m.history[m.histPos]
			}
			return m, nil
		case "down":
			if len(m.history) == 0 {
				return m, nil
			}
			if m.histPos < len(m.history)-1 {
				m.histPos++
				m.input = m.history[m.histPos]
			} else {
				m.histPos = len(m.history)
				m.input = ""
			}
			return m, nil
		default:
			if len(v.Runes) > 0 {
				m.input += string(v.Runes)
				return m, nil
			}
		}
	}
	return m, nil
}

func (m AppModel) View() string {
	if m.width == 0 {
		m.width = 120
	}
	if m.height == 0 {
		m.height = 36
	}

	msgView := renderMessages(m.msgs, m.width-4, m.scroll)
	toolView := renderToolPanel(m.tools, m.expandTools)
	editorView := renderEditor(m.input, m.width-4)
	footerView := renderFooter(m.sess.Model(), m.tokens, m.stream, m.sess.SessionID())
	if m.compacting {
		footerView += "\n" + m.theme.Hint.Render("[正在压缩上下文，请稍候...]")
	}
	if strings.TrimSpace(m.statusHint) != "" {
		footerView += "\n" + m.theme.Hint.Render(m.statusHint)
	}

	if m.lastErr != "" {
		footerView += "\n" + m.theme.Error.Render("错误: "+m.lastErr)
	}

	bodyHeight := m.height - 10
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	msgPane := m.theme.Border.Width(m.width - 4).Height(bodyHeight).Render(msgView)
	toolPane := m.theme.Border.Width(m.width - 4).Height(6).Render(toolView)
	editorPane := m.theme.Border.Width(m.width - 4).Height(4).Render(editorView)
	footerPane := lipgloss.NewStyle().Width(m.width - 2).Render(m.theme.Footer.Render(footerView))

	header := m.theme.Hint.Render(fmt.Sprintf("Gopi TUI | %s", m.sess.Model()))
	base := lipgloss.JoinVertical(lipgloss.Left, header, msgPane, toolPane, editorPane, footerPane)
	if m.modal != modalNone {
		modal := m.renderModal()
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	}
	return base
}

func (m AppModel) renderModal() string {
	title := ""
	items := []string{}
	if m.modal == modalSession {
		title = "会话选择器（Enter 切换, Esc 关闭）"
		for _, s := range m.sessionItems {
			items = append(items, fmt.Sprintf("%s  (%s)", s.ID, s.UpdatedAt.Format("01-02 15:04")))
		}
	}
	if m.modal == modalModel {
		title = "模型选择器（Enter 切换, Esc 关闭）"
		items = append(items, m.modelItems...)
	}
	if len(items) == 0 {
		items = []string{"(无可选项)"}
	}
	var lines []string
	for i, it := range items {
		if i == m.pickerIndex {
			lines = append(lines, "> "+it)
		} else {
			lines = append(lines, "  "+it)
		}
	}
	body := title + "\n\n" + strings.Join(lines, "\n")
	return m.theme.Border.Width(min(80, m.width-6)).Render(body)
}

func buildModelItems(cfg config.Config, current string) []string {
	base := []string{"qwen2.5-coder:7b", "qwen3:8b", cfg.Ollama.Model, current}
	set := map[string]bool{}
	out := make([]string, 0, len(base))
	for _, m := range base {
		m = strings.TrimSpace(m)
		if m == "" || set[m] {
			continue
		}
		set[m] = true
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func detectKittySupport() bool {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	return strings.Contains(term, "kitty")
}

func parseImageMentions(text string) (string, []string, []string) {
	parts := strings.Fields(text)
	images := make([]string, 0)
	missing := make([]string, 0)
	for _, p := range parts {
		if !strings.HasPrefix(p, "@") || len(p) <= 1 {
			continue
		}
		cand := strings.TrimPrefix(p, "@")
		ext := strings.ToLower(filepath.Ext(cand))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
			continue
		}
		if _, err := os.Stat(cand); err == nil {
			images = append(images, cand)
		} else {
			missing = append(missing, cand)
		}
	}
	return text, images, missing
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func estimateTokenLike(msgs []chatMessage) int {
	total := 0
	for _, m := range msgs {
		total += len([]rune(m.Content))/3 + 4
	}
	if total < 0 {
		return 0
	}
	return total
}
