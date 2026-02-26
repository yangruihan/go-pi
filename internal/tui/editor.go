package tui

import (
	"fmt"
	"strings"
)

func renderEditor(input string, width int) string {
	if width < 20 {
		width = 20
	}
	value := input
	if strings.TrimSpace(value) == "" {
		value = ""
	}
	return fmt.Sprintf("Input (Enter发送, Shift+Enter换行, Ctrl+C中止, Ctrl+L清屏)\n%s", value)
}
