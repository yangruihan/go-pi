package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coderyrh/gopi/internal/agent"
	"github.com/coderyrh/gopi/internal/config"
	"github.com/coderyrh/gopi/internal/session"
	"github.com/coderyrh/gopi/internal/skills"
	"golang.org/x/term"
)

type agentEventMsg struct {
	event agent.AgentEvent
}

type promptDoneMsg struct {
	err error
}

type resizePollMsg struct {
	width  int
	height int
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
	return tea.Batch(waitForEvent(m.eventCh), pollWindowSizeCmd())
}

func pollWindowSizeCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || w <= 0 || h <= 0 {
			return nil
		}
		return resizePollMsg{width: w, height: h}
	})
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
		return m, pollWindowSizeCmd()

	case resizePollMsg:
		if v.width > 0 && v.height > 0 {
			m.width, m.height = v.width, v.height
		}
		return m, pollWindowSizeCmd()

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
			if strings.HasPrefix(raw, "/skill:") {
				name := strings.TrimPrefix(raw, "/skill:")
				cwd, _ := os.Getwd()
				content, err := skills.LoadProjectSkill(cwd, name)
				if err != nil {
					m.lastErr = err.Error()
				} else if err := m.sess.AppendSystemPrompt("技能[" + name + "]:\n" + content); err != nil {
					m.lastErr = err.Error()
				} else {
					m.statusHint = "已加载技能: " + name
					m.input = ""
				}
				return m, nil
			}
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
			m.scroll += 10
			return m, nil
		case "pgdown":
			m.scroll -= 10
			if m.scroll < 0 {
				m.scroll = 0
			}
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
	if m.height < 8 {
		return m.theme.Hint.Render("窗口高度过小，请增大终端高度（建议 >= 10 行）")
	}

	innerWidth := maxInt(20, m.width-2)

	toolView := renderToolPanel(m.tools, m.expandTools)
	editorView := renderEditor(m.input, innerWidth)
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

	headerH := 1
	footerH := clampInt(strings.Count(footerView, "\n")+1, 1, 3)
	editorH := 4
	toolH := 0
	if m.expandTools {
		toolH = 6
	}

	// 优先保障消息区可见：空间不足时依次裁剪 tool -> footer -> editor
	minMsgH := 5
	need := headerH + msgHWithBorder(minMsgH) + editorH + footerH + toolH
	if need > m.height {
		over := need - m.height
		toolH, over = shrinkSection(toolH, 0, over)
		footerH, over = shrinkSection(footerH, 1, over)
		editorH, over = shrinkSection(editorH, 3, over)
		if over > 0 {
			minMsgH = maxInt(3, minMsgH-over)
		}
	}

	msgH := m.height - headerH - editorH - footerH - toolH
	if msgH < msgHWithBorder(3) {
		msgH = msgHWithBorder(3)
	}

	msgContentH := maxInt(1, msgH-2)
	msgView := renderMessages(m.msgs, innerWidth, m.scroll, msgContentH)

	msgPane := m.theme.Border.Width(innerWidth).Height(msgH).Render(msgView)
	toolPane := ""
	if toolH > 0 {
		toolPane = m.theme.Border.Width(innerWidth).Height(toolH).Render(toolView)
	}
	editorPane := m.theme.Border.Width(innerWidth).Height(editorH).Render(editorView)
	footerPane := lipgloss.NewStyle().Width(m.width).Height(footerH).Render(m.theme.Footer.Render(limitLines(footerView, footerH)))

	header := lipgloss.NewStyle().Width(m.width).Render(m.theme.Hint.Render(fmt.Sprintf("Gopi TUI | %s", m.sess.Model())))
	parts := []string{header, msgPane}
	if toolPane != "" {
		parts = append(parts, toolPane)
	}
	parts = append(parts, editorPane, footerPane)
	base := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if m.modal != modalNone {
		modal := m.renderModal()
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	}
	// 强制裁剪到终端高度，避免底部块把顶部挤出可视区域
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, base)
}

func (m AppModel) renderModal() string {
	title := ""
	items := []string{}
	if m.modal == modalSession {
		title = "会话选择器（Enter 切换, Esc 关闭）"
		labels := buildSessionTreeLabels(m.sessionItems)
		for i, s := range m.sessionItems {
			items = append(items, fmt.Sprintf("%s  (%s)", labels[i], s.UpdatedAt.Format("01-02 15:04")))
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
	return m.theme.Border.Width(min(80, m.width-4)).Render(body)
}

func buildSessionTreeLabels(items []session.SessionMeta) []string {
	depth := map[string]int{}
	index := map[string]session.SessionMeta{}
	for _, it := range items {
		index[it.ID] = it
	}
	var calcDepth func(id string) int
	calcDepth = func(id string) int {
		if d, ok := depth[id]; ok {
			return d
		}
		it, ok := index[id]
		if !ok || strings.TrimSpace(it.ParentID) == "" {
			depth[id] = 0
			return 0
		}
		d := calcDepth(it.ParentID) + 1
		depth[id] = d
		return d
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		d := calcDepth(it.ID)
		prefix := strings.Repeat("  ", d)
		if d > 0 {
			prefix += "└─ "
		}
		out = append(out, prefix+it.ID)
	}
	return out
}

func buildModelItems(cfg config.Config, current string) []string {
	base := []string{"qwen2.5-coder:7b", "qwen3:8b", cfg.Ollama.Model, current}
	if profiles, err := config.LoadModelProfiles(""); err == nil {
		for _, p := range profiles {
			if strings.TrimSpace(p.Model) != "" {
				base = append(base, p.Model)
			}
		}
	}
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func shrinkSection(current, minValue, over int) (int, int) {
	if over <= 0 {
		return current, 0
	}
	can := current - minValue
	if can <= 0 {
		return current, over
	}
	if can >= over {
		return current - over, 0
	}
	return minValue, over - can
}

func limitLines(text string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	return strings.Join(lines[:maxLines], "\n")
}

func msgHWithBorder(contentH int) int {
	return contentH + 2
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
