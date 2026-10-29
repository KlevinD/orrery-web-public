package discussionsession

import (
	"sync"
)

type conversationStreamHub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan<- StreamEventDTO]struct{}
}

func newConversationStreamHub() *conversationStreamHub {
	return &conversationStreamHub{subscribers: map[string]map[chan<- StreamEventDTO]struct{}{}}
}

func (h *conversationStreamHub) subscribe(conversationID string, ch chan<- StreamEventDTO) {
	if h == nil || ch == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers[conversationID] == nil {
		h.subscribers[conversationID] = map[chan<- StreamEventDTO]struct{}{}
	}
	h.subscribers[conversationID][ch] = struct{}{}
}

func (h *conversationStreamHub) unsubscribe(conversationID string, ch chan<- StreamEventDTO) {
	if h == nil || ch == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	subscribers := h.subscribers[conversationID]
	if len(subscribers) == 0 {
		return
	}
	delete(subscribers, ch)
	if len(subscribers) == 0 {
		delete(h.subscribers, conversationID)
	}
}

func (h *conversationStreamHub) publish(conversationID string, event StreamEventDTO) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers[conversationID] {
		select {
		case ch <- event:
		default:
		}
	}
}
