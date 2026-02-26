package session

import (
	"sync"

	"github.com/yangruihan/go-pi/internal/agent"
)

// EventListener 会话事件监听函数
type EventListener func(event agent.AgentEvent)

// EventBus 内部事件总线
type EventBus struct {
	mu        sync.RWMutex
	nextID    int
	listeners map[int]EventListener
}

func NewEventBus() *EventBus {
	return &EventBus{listeners: make(map[int]EventListener)}
}

func (b *EventBus) Subscribe(fn EventListener) func() {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.listeners[id] = fn
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		delete(b.listeners, id)
		b.mu.Unlock()
	}
}

func (b *EventBus) Publish(event agent.AgentEvent) {
	b.mu.RLock()
	listeners := make([]EventListener, 0, len(b.listeners))
	for _, fn := range b.listeners {
		listeners = append(listeners, fn)
	}
	b.mu.RUnlock()

	for _, fn := range listeners {
		fn(event)
	}
}
