package tracing

import "sync"

type BoundedRecorder struct {
	mu      sync.Mutex
	limit   int
	spans   []Span
	dropped int64
}

func NewBoundedRecorder(limit int) *BoundedRecorder {
	if limit <= 0 {
		limit = 1024
	}
	return &BoundedRecorder{limit: limit}
}

func (r *BoundedRecorder) RecordSpan(span Span) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.spans) >= r.limit {
		r.dropped++
		return
	}
	r.spans = append(r.spans, span)
}

func (r *BoundedRecorder) Drain() []Span {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	spans := append([]Span(nil), r.spans...)
	r.spans = r.spans[:0]
	return spans
}

func (r *BoundedRecorder) Dropped() int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}
