package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/coderyrh/gopi/internal/llm"
)

const GrepMaxMatches = 50

// GrepArgs 搜索参数
type GrepArgs struct {
	Pattern   string `json:"pattern"`
	Path      string `json:"path,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Literal   bool   `json:"literal,omitempty"`
}

// GrepTool 正则/字面量搜索
type GrepTool struct{}

func NewGrepTool() *GrepTool { return &GrepTool{} }

func (t *GrepTool) Name() string { return "grep_search" }

func (t *GrepTool) Description() string {
	return "在文件中进行正则或字面量搜索。支持递归目录搜索，输出文件名、行号和内容。"
}

func (t *GrepTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"pattern": {Type: "string", Description: "搜索模式（正则表达式或字面量）"},
			"path": {Type: "string", Description: "文件或目录路径，默认当前目录"},
			"recursive": {Type: "boolean", Description: "目录是否递归搜索，默认 true"},
			"literal": {Type: "boolean", Description: "是否按字面量匹配，默认 false（正则）"},
		},
		Required: []string{"pattern"},
	}
}

func (t *GrepTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a GrepArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse grep_search args: %w", err)
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", fmt.Errorf("pattern cannot be empty")
	}
	if a.Path == "" {
		a.Path = "."
	}
	if !a.Recursive {
		a.Recursive = true
	}

	matcher := func(s string) bool { return false }
	if a.Literal {
		matcher = func(s string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(a.Pattern)) }
	} else {
		re, err := regexp.Compile(a.Pattern)
		if err != nil {
			return "", fmt.Errorf("invalid regex: %w", err)
		}
		matcher = re.MatchString
	}

	var results []string
	scanFile := func(path string) {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if matcher(line) {
				results = append(results, fmt.Sprintf("%s:%d:%s", path, lineNo, line))
				if len(results) >= GrepMaxMatches {
					return
				}
			}
		}
	}

	info, err := os.Stat(a.Path)
	if err != nil {
		return "", fmt.Errorf("stat path: %w", err)
	}

	if !info.IsDir() {
		scanFile(a.Path)
	} else {
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
			if len(results) >= GrepMaxMatches {
				return fmt.Errorf("stop")
			}
			scanFile(path)
			return nil
		})
	}

	if len(results) == 0 {
		return "未找到匹配项", nil
	}
	return strings.Join(results, "\n"), nil
}
