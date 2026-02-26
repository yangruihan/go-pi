package tui

import (
	"fmt"
	"strings"
)

type toolItem struct {
	Name   string
	Args   string
	Output string
}

func renderToolPanel(items []toolItem, expanded bool) string {
	if !expanded {
		return "[工具面板已折叠，按 Ctrl+T 展开]"
	}
	if len(items) == 0 {
		return "工具面板：暂无工具调用"
	}
	if len(items) > 6 {
		items = items[len(items)-6:]
	}
	var lines []string
	lines = append(lines, "工具面板：")
	for _, it := range items {
		line := fmt.Sprintf("- %s", it.Name)
		if strings.TrimSpace(it.Args) != "" {
			line += " args=" + trimText(it.Args, 80)
		}
		if strings.TrimSpace(it.Output) != "" {
			line += "\n  -> " + trimText(strings.ReplaceAll(it.Output, "\n", " | "), 120)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func trimText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
