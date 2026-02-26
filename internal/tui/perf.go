package tui

import "time"

// MeasureFrameRenderTime 粗略测量 TUI 一帧渲染耗时（不依赖终端 I/O）。
func MeasureFrameRenderTime(iterations, width, height int) (avg time.Duration, max time.Duration) {
	if iterations <= 0 {
		iterations = 60
	}
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 40
	}

	msgs := make([]chatMessage, 0, 40)
	for i := 0; i < 20; i++ {
		msgs = append(msgs,
			chatMessage{Role: "user", Content: "请帮我优化这个函数的性能，重点关注内存分配和循环开销。"},
			chatMessage{Role: "assistant", Content: "可以先通过 pprof 定位热点，再减少临时对象与重复字符串拼接。"},
		)
	}
	tools := []toolItem{
		{Name: "read_file", Args: "{\"path\":\"main.go\"}", Output: "..."},
		{Name: "grep_search", Args: "{\"pattern\":\"TODO\"}", Output: "..."},
	}

	var total time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_ = renderMessages(msgs, width-2, i%10, maxInt(1, height-14))
		_ = renderToolPanel(tools, true)
		_ = renderEditor("正在输入一段较长的问题，观察布局与换行效果...", width-2)
		_ = renderFooter("qwen3:8b", 1234+i, i%2 == 0, "bench-session")
		elapsed := time.Since(start)
		total += elapsed
		if elapsed > max {
			max = elapsed
		}
	}
	avg = time.Duration(int64(total) / int64(iterations))
	return avg, max
}
