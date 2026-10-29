package metrics

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"orrery_v2/internal/platform/logging"
)

type Event struct {
	Name   string
	Fields map[string]any
}

type BoundedSink struct {
	events       chan Event
	notify       chan struct{}
	dropped      atomic.Int64
	emitted      atomic.Int64
	warnInterval time.Duration
	warnMu       sync.Mutex
	lastWarn     time.Time
}

type Snapshot struct {
	Buffered int
	Dropped  int64
	Emitted  int64
}

func NewBoundedSink(size int) *BoundedSink {
	return NewBoundedSinkWithWarnInterval(size, time.Minute)
}

func NewBoundedSinkWithWarnInterval(size int, warnInterval time.Duration) *BoundedSink {
	if size <= 0 {
		size = 1
	}
	if warnInterval <= 0 {
		warnInterval = time.Minute
	}
	return &BoundedSink{
		events:       make(chan Event, size),
		notify:       make(chan struct{}, 1),
		warnInterval: warnInterval,
	}
}

func (s *BoundedSink) Capture(event Event) bool {
	select {
	case s.events <- event:
		s.signal()
		return true
	default:
		s.dropped.Add(1)
		return false
	}
}

func (s *BoundedSink) Dropped() int64 {
	return s.dropped.Load()
}

func (s *BoundedSink) Drain(max int) []Event {
	if max <= 0 {
		return nil
	}
	events := make([]Event, 0, max)
	for len(events) < max {
		select {
		case event := <-s.events:
			events = append(events, event)
		default:
			s.emitted.Add(int64(len(events)))
			return events
		}
	}
	s.emitted.Add(int64(len(events)))
	return events
}

func (s *BoundedSink) Snapshot() Snapshot {
	return Snapshot{Buffered: len(s.events), Dropped: s.Dropped(), Emitted: s.emitted.Load()}
}

func (s *BoundedSink) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *BoundedSink) WarnIfDropped(ctx context.Context, logger logging.Logger, now time.Time) bool {
	if logger == nil || s.Dropped() == 0 {
		return false
	}
	s.warnMu.Lock()
	defer s.warnMu.Unlock()
	if !s.lastWarn.IsZero() && now.Sub(s.lastWarn) < s.warnInterval {
		return false
	}
	s.lastWarn = now
	snapshot := s.Snapshot()
	logger.Warn(ctx, "metrics events dropped",
		logging.Int("buffered", snapshot.Buffered),
		logging.Any("dropped", snapshot.Dropped),
		logging.Any("emitted", snapshot.Emitted),
	)
	return true
}
