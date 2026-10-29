package session

import (
	"sync"

	agentcore "orrery_v2/internal/agent/core"
)

type boundedBus[T any] struct {
	mu       sync.Mutex
	nextID   uint64
	closed   bool
	capacity int
	overflow func(uint64, T) T
	subs     map[uint64]*boundedSubscription[T]
}

type boundedSubscription[T any] struct {
	mu       sync.Mutex
	queue    []T
	capacity int
	overflow func(uint64, T) T
	notify   chan struct{}
	out      chan T
	done     chan struct{}
	once     sync.Once
	onClose  func()
}

func newBoundedBus[T any](capacity int, overflow func(uint64, T) T) *boundedBus[T] {
	if capacity < 2 {
		capacity = 2
	}
	return &boundedBus[T]{capacity: capacity, overflow: overflow, subs: make(map[uint64]*boundedSubscription[T])}
}

func (b *boundedBus[T]) subscribe() *boundedSubscription[T] {
	b.mu.Lock()
	defer b.mu.Unlock()
	sub := &boundedSubscription[T]{capacity: b.capacity, overflow: b.overflow, notify: make(chan struct{}, 1), out: make(chan T), done: make(chan struct{})}
	if b.closed {
		sub.once.Do(func() {
			close(sub.done)
			close(sub.out)
		})
		return sub
	}
	id := b.nextID
	b.nextID++
	sub.onClose = func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
	b.subs[id] = sub
	go sub.run()
	return sub
}

func (b *boundedBus[T]) publish(value T) {
	b.mu.Lock()
	subs := make([]*boundedSubscription[T], 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.Unlock()
	for _, sub := range subs {
		sub.enqueue(value)
	}
}

func (b *boundedBus[T]) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*boundedSubscription[T], 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	b.subs = make(map[uint64]*boundedSubscription[T])
	b.mu.Unlock()
	for _, sub := range subs {
		sub.Close()
	}
}

func (s *boundedSubscription[T]) enqueue(value T) {
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return
	default:
	}
	if len(s.queue) >= s.capacity {
		skipped := uint64(len(s.queue) + 1)
		s.queue = s.queue[:0]
		s.queue = append(s.queue, s.overflow(skipped, value))
	} else {
		s.queue = append(s.queue, value)
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *boundedSubscription[T]) run() {
	defer close(s.out)
	for {
		select {
		case <-s.done:
			return
		case <-s.notify:
			for {
				s.mu.Lock()
				if len(s.queue) == 0 {
					s.mu.Unlock()
					break
				}
				value := s.queue[0]
				var zero T
				s.queue[0] = zero
				s.queue = s.queue[1:]
				s.mu.Unlock()
				select {
				case s.out <- value:
				case <-s.done:
					return
				}
			}
		}
	}
}

func (s *boundedSubscription[T]) Close() {
	s.once.Do(func() {
		if s.onClose != nil {
			s.onClose()
		}
		close(s.done)
	})
}

type EventSubscription struct {
	inner *boundedSubscription[agentcore.SessionEnvelope]
}

func (s *EventSubscription) Events() <-chan agentcore.SessionEnvelope { return s.inner.out }
func (s *EventSubscription) Close()                                   { s.inner.Close() }

type SnapshotUpdate struct {
	Snapshot Snapshot
	Lagged   uint64
}

type SnapshotSubscription struct {
	inner *boundedSubscription[SnapshotUpdate]
}

func (s *SnapshotSubscription) Updates() <-chan SnapshotUpdate { return s.inner.out }
func (s *SnapshotSubscription) Close()                         { s.inner.Close() }
