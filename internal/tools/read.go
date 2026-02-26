package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/coderyrh/gopi/internal/llm"
)

const FileReadMaxLines = 500

// ReadArgs 是 read 工具的参数
type ReadArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"` // 1-based，0 表示从头开始
	EndLine   int    `json:"end_line,omitempty"`   // 1-based，0 表示到文件末尾
}

// ReadTool 读取文件内容
type ReadTool struct{}

func NewReadTool() *ReadTool { return &ReadTool{} }

func (t *ReadTool) Name() string { return "read_file" }

func (t *ReadTool) Description() string {
	return "读取文件内容。可以指定起始行和结束行（1-based）。最多读取 500 行，超出部分需要分段读取。"
}

func (t *ReadTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"path": {
				Type:        "string",
				Description: "文件路径（绝对路径或相对于当前工作目录的路径）",
			},
			"start_line": {
				Type:        "integer",
				Description: "起始行号（1-based），默认从第 1 行开始",
			},
			"end_line": {
				Type:        "integer",
				Description: "结束行号（1-based），默认读到文件末尾或最多 500 行",
			},
		},
		Required: []string{"path"},
	}
}

func (t *ReadTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a ReadArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse read_file args: %w", err)
	}

	if a.Path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	f, err := os.Open(a.Path)
	if err != nil {
		return "", fmt.Errorf("open file %q: %w", a.Path, err)
	}
	defer f.Close()

	// 获取文件信息
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file %q: %w", a.Path, err)
	}

	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory, use ls tool instead", a.Path)
	}

	startLine := a.StartLine
	if startLine <= 0 {
		startLine = 1
	}

	endLine := a.EndLine
	if endLine <= 0 || endLine-startLine+1 > FileReadMaxLines {
		endLine = startLine + FileReadMaxLines - 1
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	lineNum := 0
	readCount := 0
	totalLines := 0

	// 先统计总行数（用于显示）
	// 实际读取
	for scanner.Scan() {
		lineNum++
		if lineNum < startLine {
			continue
		}
		if lineNum > endLine {
			// 继续扫描以统计总行数
			for scanner.Scan() {
				lineNum++
			}
			totalLines = lineNum
			break
		}
		// 输出带行号的内容
		sb.WriteString(fmt.Sprintf("%4s │ %s\n", strconv.Itoa(lineNum), scanner.Text()))
		readCount++
		totalLines = lineNum
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	result := sb.String()
	if result == "" {
		return fmt.Sprintf("文件 %q 为空或指定范围（%d-%d）无内容", a.Path, startLine, endLine), nil
	}

	header := fmt.Sprintf("文件: %s | 显示行: %d-%d", a.Path, startLine, startLine+readCount-1)
	if totalLines > endLine {
		header += fmt.Sprintf(" | 共 %d 行（还有更多，请指定 start_line=%d 继续读取）", totalLines, endLine+1)
	}
	header += "\n" + strings.Repeat("─", 60) + "\n"

	return header + result, nil
}
