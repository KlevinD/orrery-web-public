package daemon

import (
	"strings"
	"sync"
)

type Limiter struct {
	ch chan struct{}
}

func NewLimiter(max int) *Limiter {
	if max <= 0 {
		max = 1
	}
	return &Limiter{ch: make(chan struct{}, max)}
}

func (l *Limiter) TryAcquire() bool {
	select {
	case l.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *Limiter) Release() {
	select {
	case <-l.ch:
	default:
	}
}

type KeyedLimiter struct {
	mu     sync.Mutex
	max    int
	lanes  map[string]*Limiter
	active map[string]int
}

func NewKeyedLimiter(maxPerKey int) *KeyedLimiter {
	if maxPerKey <= 0 {
		maxPerKey = 1
	}
	return &KeyedLimiter{
		max:    maxPerKey,
		lanes:  map[string]*Limiter{},
		active: map[string]int{},
	}
}

func (l *KeyedLimiter) TryAcquire(key string) bool {
	if l == nil {
		return true
	}
	normalized := normalizeLimiterKey(key)
	l.mu.Lock()
	limiter := l.lanes[normalized]
	if limiter == nil {
		limiter = NewLimiter(l.max)
		l.lanes[normalized] = limiter
	}
	l.mu.Unlock()
	if !limiter.TryAcquire() {
		return false
	}
	l.mu.Lock()
	l.active[normalized]++
	l.mu.Unlock()
	return true
}

func (l *KeyedLimiter) Release(key string) {
	if l == nil {
		return
	}
	normalized := normalizeLimiterKey(key)
	l.mu.Lock()
	limiter := l.lanes[normalized]
	if limiter != nil {
		limiter.Release()
	}
	if l.active[normalized] > 1 {
		l.active[normalized]--
		l.mu.Unlock()
		return
	}
	delete(l.active, normalized)
	delete(l.lanes, normalized)
	l.mu.Unlock()
}

func normalizeLimiterKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "default"
	}
	return key
}
