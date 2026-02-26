package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yangruihan/go-pi/internal/llm"
)

// WriteArgs 写文件参数
type WriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Append  bool   `json:"append,omitempty"`
}

// WriteTool 写入文件（覆盖或追加）
type WriteTool struct{}

func NewWriteTool() *WriteTool { return &WriteTool{} }

func (t *WriteTool) Name() string { return "write_file" }

func (t *WriteTool) Description() string {
	return "写入文件内容。默认覆盖写入，可通过 append=true 追加写入。"
}

func (t *WriteTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"path": {Type: "string", Description: "文件路径"},
			"content": {Type: "string", Description: "写入内容"},
			"append": {Type: "boolean", Description: "是否追加写入，默认 false（覆盖）"},
		},
		Required: []string{"path", "content"},
	}
}

func (t *WriteTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a WriteArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse write_file args: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
		return "", fmt.Errorf("create parent dir: %w", err)
	}

	flag := os.O_CREATE | os.O_WRONLY
	if a.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	f, err := os.OpenFile(a.Path, flag, 0o644)
	if err != nil {
		return "", fmt.Errorf("open file for write: %w", err)
	}
	defer f.Close()

	n, err := f.WriteString(a.Content)
	if err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	mode := "覆盖"
	if a.Append {
		mode = "追加"
	}
	return fmt.Sprintf("已%s写入 %s（%d 字节）", mode, a.Path, n), nil
}
