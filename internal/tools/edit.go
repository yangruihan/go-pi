package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/coderyrh/gopi/internal/llm"
)

// EditArgs 精确替换参数
type EditArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// EditTool 精确字符串替换工具
type EditTool struct{}

func NewEditTool() *EditTool { return &EditTool{} }

func (t *EditTool) Name() string { return "edit_file" }

func (t *EditTool) Description() string {
	return "精确字符串替换：old_string 在文件中必须恰好出现 1 次才会替换。"
}

func (t *EditTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"path": {Type: "string", Description: "文件路径"},
			"old_string": {Type: "string", Description: "待替换的原字符串（必须精确匹配）"},
			"new_string": {Type: "string", Description: "新字符串"},
		},
		Required: []string{"path", "old_string", "new_string"},
	}
}

func (t *EditTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a EditArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse edit_file args: %w", err)
	}
	if a.Path == "" || a.OldString == "" {
		return "", fmt.Errorf("path and old_string are required")
	}

	contentBytes, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	content := string(contentBytes)

	count := strings.Count(content, a.OldString)
	switch {
	case count == 0:
		return "", fmt.Errorf("old_string 未在文件中找到，请检查是否精确匹配")
	case count > 1:
		return "", fmt.Errorf("old_string 出现 %d 次，需提供更多上下文以确保唯一定位", count)
	}

	updated := strings.Replace(content, a.OldString, a.NewString, 1)
	if err := os.WriteFile(a.Path, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("已更新 %s（成功替换 1 处）", a.Path), nil
}
