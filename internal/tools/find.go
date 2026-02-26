package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yangruihan/go-pi/internal/llm"
)

// FindArgs 文件查找参数
type FindArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

// FindTool 按 glob 查找文件
type FindTool struct{}

func NewFindTool() *FindTool { return &FindTool{} }

func (t *FindTool) Name() string { return "find_files" }

func (t *FindTool) Description() string {
	return "按 glob 模式查找文件（例如 **/*.go, cmd/*/*.go）。自动跳过 .git 与常见依赖目录。"
}

func (t *FindTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"pattern": {Type: "string", Description: "glob 模式，如 **/*.go"},
			"path": {Type: "string", Description: "根目录，默认当前目录"},
		},
		Required: []string{"pattern"},
	}
}

func (t *FindTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a FindArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse find_files args: %w", err)
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", fmt.Errorf("pattern cannot be empty")
	}
	if a.Path == "" {
		a.Path = "."
	}

	pattern := strings.ReplaceAll(a.Pattern, "\\", "/")
	var matches []string
	_ = filepath.Walk(a.Path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || strings.HasPrefix(name, ".") {
				if path != a.Path {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, relErr := filepath.Rel(a.Path, path)
		if relErr != nil {
			rel = path
		}
		rel = strings.ReplaceAll(rel, "\\", "/")
		ok, _ := filepath.Match(pattern, rel)
		if ok || strings.Contains(pattern, "**") && simpleDoubleStarMatch(pattern, rel) {
			matches = append(matches, path)
		}
		return nil
	})

	if len(matches) == 0 {
		return "未找到匹配文件", nil
	}
	if len(matches) > 200 {
		matches = matches[:200]
		matches = append(matches, "... 结果过多，已截断为 200 条")
	}
	return strings.Join(matches, "\n"), nil
}

func simpleDoubleStarMatch(pattern, rel string) bool {
	parts := strings.Split(pattern, "**")
	idx := 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		n := strings.Index(rel[idx:], strings.Trim(p, "/"))
		if n < 0 {
			return false
		}
		idx += n + len(strings.Trim(p, "/"))
	}
	return true
}
