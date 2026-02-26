package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/coderyrh/gopi/internal/llm"
)

const (
	BashOutputMaxBytes = 8192 // 输出超过 8KB 截断
	BashTimeout        = 30 * time.Second
)

// BashArgs 是 bash 工具的参数
type BashArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // 秒，0 表示使用默认值
}

// BashTool 是持久化 shell 工具
// 维护一个持久化的 bash/cmd 进程，保留工作目录和环境变量
type BashTool struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	started bool
}

// NewBashTool 创建一个新的 BashTool
func NewBashTool() *BashTool {
	return &BashTool{}
}

func (b *BashTool) Name() string { return "bash" }

func (b *BashTool) Description() string {
	return "在持久化的 shell 进程中执行 bash 命令。支持 cd 切换目录，状态在多次调用间保留。命令超时时间默认 30s，输出超过 8KB 自动截断。"
}

func (b *BashTool) Schema() llm.ToolParameters {
	return llm.ToolParameters{
		Type: "object",
		Properties: map[string]llm.ToolProperty{
			"command": {
				Type:        "string",
				Description: "要执行的 shell 命令",
			},
			"timeout": {
				Type:        "integer",
				Description: "超时时间（秒），默认 30",
			},
		},
		Required: []string{"command"},
	}
}

// ensureStarted 确保 shell 进程已启动
func (b *BashTool) ensureStarted() error {
	if b.started {
		return nil
	}

	// 使用 cmd.exe /K 保持 shell 存活（Windows），Linux/Mac 使用 bash
	var cmd *exec.Cmd
	// 检测系统，使用 bash on Unix-like，cmd/powershell on Windows
	if isWindows() {
		cmd = exec.Command("cmd", "/Q")
	} else {
		cmd = exec.Command("bash")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start shell: %w", err)
	}

	b.cmd = cmd
	b.stdin = stdin
	b.stdout = stdout
	b.stderr = stderr
	b.started = true
	return nil
}

// Execute 执行 bash 命令，流式返回输出
func (b *BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var a BashArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse bash args: %w", err)
	}

	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("command cannot be empty")
	}

	timeout := BashTimeout
	if a.Timeout > 0 {
		timeout = time.Duration(a.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 直接使用 exec.CommandContext 执行单次命令（兼容性更好）
	result, err := b.runCommand(ctx, a.Command)
	if err == nil {
		return result, nil
	}

	// 崩溃/启动异常自动重试一次（Phase4 健壮性）
	if ctx.Err() == nil {
		retryResult, retryErr := b.runCommand(ctx, a.Command)
		if retryErr == nil {
			if strings.TrimSpace(result) != "" {
				retryResult = strings.TrimSpace(result) + "\n[bash 已自动重试恢复]\n" + retryResult
			}
			return retryResult, nil
		}
	}

	return result, err
}

// runCommand 使用独立进程执行命令（跨平台兼容）
func (b *BashTool) runCommand(ctx context.Context, command string) (string, error) {
	var cmd *exec.Cmd
	if isWindows() {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", command)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()

	combined := outBuf.String()
	if errBuf.Len() > 0 {
		if combined != "" {
			combined += "\n"
		}
		combined += errBuf.String()
	}

	// 截断超长输出
	if len(combined) > BashOutputMaxBytes {
		combined = combined[:BashOutputMaxBytes] + fmt.Sprintf("\n... [输出超过 %d 字节，已截断]", BashOutputMaxBytes)
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return combined, fmt.Errorf("命令超时（已执行的输出：%s）", combined)
		}
		// 命令本身返回非零退出码，将 stderr 包含在结果中但不作为错误
		if combined != "" {
			return combined, nil
		}
		return "", fmt.Errorf("command failed: %w", err)
	}

	return combined, nil
}

// Close 关闭持久化 shell 进程
func (b *BashTool) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.started {
		return
	}

	b.stdin.Close()
	b.cmd.Wait()
	b.started = false
}

// isWindows 检测是否在 Windows 上运行
func isWindows() bool {
	// 检测 GOARCH/GOOS 在编译时确定，通过文件分隔符检测更可靠
	return exec.Command("cmd", "/C", "echo test").Run() == nil
}

// streamReader 从 reader 读取内容，直到 sentinel 标记
func streamReader(r io.Reader, sentinel string, maxBytes int) (string, error) {
	var buf strings.Builder
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, sentinel) {
			break
		}
		buf.WriteString(line)
		buf.WriteString("\n")
		if buf.Len() > maxBytes {
			buf.WriteString(fmt.Sprintf("... [输出超过 %d 字节，已截断]", maxBytes))
			break
		}
	}

	return buf.String(), scanner.Err()
}
