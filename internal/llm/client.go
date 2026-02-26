package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"
)

// Client 封装 Ollama API 连接
type Client struct {
	api  *ollamaapi.Client
	host string
}

// NewClient 创建新的 Ollama 客户端
// host 例如: "http://localhost:11434"
func NewClient(host string) (*Client, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama host %q: %w", host, err)
	}

	httpClient := &http.Client{Timeout: 0} // streaming，不设超时
	api := ollamaapi.NewClient(u, httpClient)

	return &Client{
		api:  api,
		host: host,
	}, nil
}

// Ping 检测 Ollama 是否可连接
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.api.Heartbeat(ctx)
}

// PingWithRetry 带指数退避的连接检测
func (c *Client) PingWithRetry(ctx context.Context, maxRetries int) error {
	if maxRetries <= 0 {
		maxRetries = 1
	}
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := c.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i == maxRetries-1 {
			break
		}
		backoff := time.Duration(1<<i) * 200 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return lastErr
}

// EnhanceModelError 为模型相关错误添加友好提示
func EnhanceModelError(err error, model string) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "model") && (strings.Contains(msg, "not found") || strings.Contains(msg, "no such") || strings.Contains(msg, "does not exist")) {
		if strings.TrimSpace(model) != "" {
			return fmt.Errorf("%w\n提示: 目标模型可能未拉取，请执行: ollama pull %s", err, model)
		}
		return fmt.Errorf("%w\n提示: 目标模型可能未拉取，请先执行: ollama list / ollama pull <model>", err)
	}
	return err
}

// ListModels 列出可用模型
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	resp, err := c.api.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	names := make([]string, 0, len(resp.Models))
	for _, m := range resp.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// Host 返回当前配置的 Ollama 主机
func (c *Client) Host() string {
	return c.host
}

// OllamaAPI 返回底层 ollamaapi.Client（供 stream.go 使用）
func (c *Client) OllamaAPI() *ollamaapi.Client {
	return c.api
}
