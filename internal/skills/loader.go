package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LoadProjectSkill(cwd, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("skill name cannot be empty")
	}
	candidates := []string{
		filepath.Join(cwd, ".gopi", "skills", name+".md"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".gopi", "skills", name+".md"))
	}
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil {
			text := strings.TrimSpace(string(data))
			if text != "" {
				return text, nil
			}
		}
	}
	return "", fmt.Errorf("skill not found: %s", name)
}

func LoadGOPIMarkdown(cwd string) string {
	path := filepath.Join(cwd, "GOPI.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
