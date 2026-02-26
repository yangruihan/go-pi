package perf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/coderyrh/gopi/internal/config"
	"github.com/coderyrh/gopi/internal/llm"
	"github.com/coderyrh/gopi/internal/session"
	"github.com/coderyrh/gopi/internal/tui"
)

type Report struct {
	FirstTokenLatency time.Duration
	FirstTokenError   string
	TUIFrameAvg       time.Duration
	TUIFrameMax       time.Duration
	SessionLoad1000   time.Duration
	Bottleneck        string
}

func Run(ctx context.Context, client *llm.Client, cfg config.Config) Report {
	report := Report{}

	if client != nil {
		latency, err := measureFirstTokenLatency(ctx, client, cfg.Ollama.Model)
		report.FirstTokenLatency = latency
		if err != nil {
			report.FirstTokenError = err.Error()
		}
	} else {
		report.FirstTokenError = "llm client unavailable"
	}

	report.TUIFrameAvg, report.TUIFrameMax = tui.MeasureFrameRenderTime(120, 120, 40)

	load, err := measureSessionLoad1000()
	if err != nil {
		report.SessionLoad1000 = -1
		if report.FirstTokenError == "" {
			report.FirstTokenError = "session load benchmark failed: " + err.Error()
		}
	} else {
		report.SessionLoad1000 = load
	}

	report.Bottleneck = findBottleneck(report)
	return report
}

func measureFirstTokenLatency(parent context.Context, client *llm.Client, model string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()

	start := time.Now()
	events, err := client.Chat(ctx, &llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{{
			Role:    "user",
			Content: "请回复一个字",
		}},
		Stream: true,
	})
	if err != nil {
		return 0, llm.EnhanceModelError(err, model)
	}

	for ev := range events {
		if ev.Type == llm.EventError && ev.Err != nil {
			return 0, llm.EnhanceModelError(ev.Err, model)
		}
		if ev.Type == llm.EventMessageDelta && ev.Delta != "" {
			return time.Since(start), nil
		}
	}
	return 0, fmt.Errorf("no delta received")
}

func measureSessionLoad1000() (time.Duration, error) {
	root, err := os.MkdirTemp("", "gopi-perf-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(root)

	mgr := session.NewSessionManager(filepath.Join(root, "sessions"))
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return 0, err
	}
	created, err := mgr.Create(cwd, "qwen3:8b")
	if err != nil {
		return 0, err
	}

	f, err := os.OpenFile(created.FilePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	for i := 0; i < 1000; i++ {
		line := map[string]any{
			"type":      "message",
			"role":      []string{"user", "assistant"}[i%2],
			"content":   fmt.Sprintf("message-%d", i),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
		b, _ := json.Marshal(line)
		if _, err := f.Write(append(b, '\n')); err != nil {
			return 0, err
		}
	}

	start := time.Now()
	_, err = mgr.Load(created.FilePath)
	if err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

func findBottleneck(r Report) string {
	type item struct {
		name string
		cost time.Duration
	}
	items := []item{{"tui_frame_max", r.TUIFrameMax}}
	if r.FirstTokenError == "" {
		items = append(items, item{"first_token", r.FirstTokenLatency})
	}
	if r.SessionLoad1000 > 0 {
		items = append(items, item{"session_load_1000", r.SessionLoad1000})
	}
	if len(items) == 0 {
		return "unknown"
	}
	max := items[0]
	for _, it := range items[1:] {
		if it.cost > max.cost {
			max = it
		}
	}
	return max.name
}
