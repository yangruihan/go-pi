package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
