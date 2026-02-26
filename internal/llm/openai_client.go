package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewOpenAIClient(baseURL, apiKey string) (*OpenAIClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("openai compatible base url is required")
	}
	return &OpenAIClient{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(apiKey),
		http:    &http.Client{Timeout: 0},
	}, nil
}

func (c *OpenAIClient) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("openai ping failed: %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *OpenAIClient) PingWithRetry(ctx context.Context, maxRetries int) error {
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

func (c *OpenAIClient) Chat(ctx context.Context, req *ChatRequest) (<-chan Event, error) {
	type oaTool struct {
		Type     string `json:"type"`
		Function struct {
			Name        string         `json:"name"`
			Description string         `json:"description,omitempty"`
			Parameters  map[string]any `json:"parameters,omitempty"`
		} `json:"function"`
	}
	type oaReqMessage struct {
		Role       string `json:"role"`
		Content    string `json:"content,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
	}
	type oaRequest struct {
		Model    string         `json:"model"`
		Messages []oaReqMessage `json:"messages"`
		Tools    []oaTool       `json:"tools,omitempty"`
		Stream   bool           `json:"stream"`
	}
	type oaResp struct {
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	ch := make(chan Event, 32)

	body := oaRequest{Model: req.Model, Stream: false}
	body.Messages = make([]oaReqMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, oaReqMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID})
	}
	if len(req.Tools) > 0 {
		body.Tools = make([]oaTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			ot := oaTool{Type: t.Type}
			ot.Function.Name = t.Function.Name
			ot.Function.Description = t.Function.Description
			if len(t.Function.Parameters) > 0 {
				_ = json.Unmarshal(t.Function.Parameters, &ot.Function.Parameters)
			}
			body.Tools = append(body.Tools, ot)
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	go func() {
		defer close(ch)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
		if err != nil {
			ch <- Event{Type: EventError, Err: err}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		resp, err := c.http.Do(httpReq)
		if err != nil {
			ch <- Event{Type: EventError, Err: err}
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			ch <- Event{Type: EventError, Err: fmt.Errorf("openai request failed: %s (%s)", resp.Status, strings.TrimSpace(string(data)))}
			return
		}

		data, err := io.ReadAll(bufio.NewReader(resp.Body))
		if err != nil {
			ch <- Event{Type: EventError, Err: err}
			return
		}
		var parsed oaResp
		if err := json.Unmarshal(data, &parsed); err != nil {
			ch <- Event{Type: EventError, Err: fmt.Errorf("parse openai response: %w", err)}
			return
		}
		if len(parsed.Choices) == 0 {
			ch <- Event{Type: EventError, Err: fmt.Errorf("openai response has no choices")}
			return
		}

		msg := parsed.Choices[0].Message
		toolCalls := make([]ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			call := ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
			toolCalls = append(toolCalls, call)
			ch <- Event{Type: EventToolCallStart, Tool: &call}
		}

		if msg.Content != "" {
			ch <- Event{Type: EventMessageDelta, Delta: msg.Content}
		}
		ch <- Event{Type: EventMessageEnd, Message: &Message{Role: "assistant", Content: msg.Content, ToolCalls: toolCalls}}
	}()

	return ch, nil
}
