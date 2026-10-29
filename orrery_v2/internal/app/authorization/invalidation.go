package authorization

import (
	"sync"
)

type InvalidationKind string

const (
	InvalidationAuthorization InvalidationKind = "authorization"
	InvalidationPolicy        InvalidationKind = "policy"
)

type InvalidationEvent struct {
	Kind     InvalidationKind
	Revision int64
}

type InvalidationSubscriber func(InvalidationEvent)

type InvalidationHub struct {
	mu          sync.RWMutex
	nextID      uint64
	subscribers map[uint64]InvalidationSubscriber
}

func NewInvalidationHub() *InvalidationHub {
	return &InvalidationHub{subscribers: map[uint64]InvalidationSubscriber{}}
}

func (h *InvalidationHub) Subscribe(subscriber InvalidationSubscriber) func() {
	if h == nil || subscriber == nil {
		return func() {}
	}
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	h.subscribers[id] = subscriber
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subscribers, id)
			h.mu.Unlock()
		})
	}
}

func (h *InvalidationHub) AuthorizationRevisionAdvanced(revision int64) {
	h.publish(InvalidationEvent{Kind: InvalidationAuthorization, Revision: revision})
}

func (h *InvalidationHub) PolicyRevisionAdvanced(revision int64) {
	h.publish(InvalidationEvent{Kind: InvalidationPolicy, Revision: revision})
}

func (h *InvalidationHub) publish(event InvalidationEvent) {
	if h == nil || event.Revision < 0 {
		return
	}
	h.mu.RLock()
	subscribers := make([]InvalidationSubscriber, 0, len(h.subscribers))
	for _, subscriber := range h.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	h.mu.RUnlock()
	for _, subscriber := range subscribers {
		callInvalidationSubscriber(subscriber, event)
	}
}

func callInvalidationSubscriber(subscriber InvalidationSubscriber, event InvalidationEvent) {
	defer func() {
		_ = recover()
	}()
	subscriber(event)
}
