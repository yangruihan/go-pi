package extensions

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type SlashHandler func(args []string) (string, error)

type SlashCommand struct {
	Name        string
	Description string
	Handler     SlashHandler
}

var (
	slashMu       sync.RWMutex
	slashCommands = map[string]SlashCommand{}
)

func RegisterSlashCommand(name, description string, handler SlashHandler) error {
	name = normalizeName(name)
	if name == "" {
		return fmt.Errorf("slash command name cannot be empty")
	}
	if handler == nil {
		return fmt.Errorf("slash command handler cannot be nil")
	}
	slashMu.Lock()
	defer slashMu.Unlock()
	slashCommands[name] = SlashCommand{Name: name, Description: strings.TrimSpace(description), Handler: handler}
	return nil
}

func ListSlashCommands() []SlashCommand {
	slashMu.RLock()
	defer slashMu.RUnlock()
	out := make([]SlashCommand, 0, len(slashCommands))
	for _, c := range slashCommands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func ExecuteSlashCommand(name string, args []string) (string, bool, error) {
	name = normalizeName(name)
	slashMu.RLock()
	cmd, ok := slashCommands[name]
	slashMu.RUnlock()
	if !ok {
		return "", false, nil
	}
	out, err := cmd.Handler(args)
	return out, true, err
}

func normalizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	return strings.ToLower(s)
}
