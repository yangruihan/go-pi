package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yangruihan/go-pi/internal/llm"
)

// LSArgs 列目录参数
type LSArgs struct {
	Path string `json:"path,omitempty"`
	Tree bool   `json:"tree,omitempty"`
}

// LSTool 列出目录
type LSTool struct{}

func NewLSTool() *LSTool { return &LSTool{} }

func (t *LSTool) Name() string { return "list_dir" }

func (t *LSTool) Description() string {
	return "列出目录内容，支持平铺或树状输出。"
}

func (t *LSTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"path": {Type: "string", Description: "目录路径，默认当前目录"},
			"tree": {Type: "boolean", Description: "是否树状输出，默认 false"},
		},
	}
}

func (t *LSTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a LSArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("parse list_dir args: %w", err)
		}
	}
	if a.Path == "" {
		a.Path = "."
	}

	if a.Tree {
		return renderTree(a.Path)
	}

	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return "", fmt.Errorf("read dir: %w", err)
	}
	items := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		items = append(items, name)
	}
	sort.Strings(items)
	return strings.Join(items, "\n"), nil
}

func renderTree(root string) (string, error) {
	var lines []string
	lines = append(lines, root)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := strings.Count(rel, string(filepath.Separator))
		indent := strings.Repeat("  ", depth)
		name := info.Name()
		if info.IsDir() {
			name += "/"
			if strings.HasPrefix(name, ".") || name == "node_modules/" {
				if info.Name() != "." {
					return filepath.SkipDir
				}
			}
		}
		lines = append(lines, indent+"- "+name)
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}
