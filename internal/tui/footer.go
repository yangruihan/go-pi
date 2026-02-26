package tui

import "fmt"

func renderFooter(model string, tokenCount int, streaming bool, sessionID string) string {
	state := "idle"
	if streaming {
		state = "streaming"
	}
	return fmt.Sprintf("model: %s | tokens~%d | state: %s | session: %s", model, tokenCount, state, sessionID)
}
