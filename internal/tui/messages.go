package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

type chatMessage struct {
	Role    string
	Content string
}

func renderMessages(messages []chatMessage, width int, scrollOffset int, viewportHeight int) string {
	if len(messages) == 0 {
		return "暂无消息，输入内容后按 Enter 发送。"
	}

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width-6),
	)

	var blocks []string
	for _, m := range messages {
		prefix := "[assistant]"
		if m.Role == "user" {
			prefix = "[user]"
		} else if m.Role == "system" {
			prefix = "[system]"
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		if out, err := renderer.Render(content); err == nil {
			blocks = append(blocks, prefix+"\n"+strings.TrimRight(out, "\n"))
		} else {
			blocks = append(blocks, prefix+"\n"+content)
		}
	}

	all := strings.Join(blocks, "\n\n")
	lines := strings.Split(all, "\n")
	if viewportHeight <= 0 {
		return ""
	}
	if len(lines) <= viewportHeight {
		return strings.Join(lines, "\n")
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	maxOffset := len(lines) - viewportHeight
	if scrollOffset > maxOffset {
		scrollOffset = maxOffset
	}

	start := maxOffset - scrollOffset
	end := start + viewportHeight
	return strings.Join(lines[start:end], "\n")
}
