package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

type traceIDKey struct{}
type spanIDKey struct{}

type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	StartedAt    time.Time
	EndedAt      time.Time
	Duration     time.Duration
	Error        string
}

type SpanRecorder interface {
	RecordSpan(span Span)
}

type SpanOptions struct {
	Clock       clock.Clock
	IDGenerator idgen.ErrorGenerator
}

func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, id)
}

func TraceID(ctx context.Context) string {
	value, _ := ctx.Value(traceIDKey{}).(string)
	return value
}

func WithSpanID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, spanIDKey{}, id)
}

func SpanID(ctx context.Context) string {
	value, _ := ctx.Value(spanIDKey{}).(string)
	return value
}

func StartSpan(ctx context.Context, name string, recorder SpanRecorder, now func() time.Time) (context.Context, func(error)) {
	return startSpan(ctx, name, recorder, spanDependencies{now: now})
}

func StartSpanWithOptions(ctx context.Context, name string, recorder SpanRecorder, opts SpanOptions) (context.Context, func(error)) {
	now := func() time.Time {
		if opts.Clock == nil {
			return time.Now()
		}
		return opts.Clock.Now()
	}
	return startSpan(ctx, name, recorder, spanDependencies{now: now, idGenerator: opts.IDGenerator})
}

type spanDependencies struct {
	now         func() time.Time
	idGenerator idgen.ErrorGenerator
}

func startSpan(ctx context.Context, name string, recorder SpanRecorder, deps spanDependencies) (context.Context, func(error)) {
	if deps.now == nil {
		deps.now = time.Now
	}
	traceID := TraceID(ctx)
	if traceID == "" {
		traceID = newTraceID(deps.idGenerator)
		ctx = WithTraceID(ctx, traceID)
	}
	parentSpanID := SpanID(ctx)
	spanID := newSpanID(deps.idGenerator)
	startedAt := deps.now()
	spanCtx := WithSpanID(ctx, spanID)

	var once sync.Once
	finish := func(err error) {
		once.Do(func() {
			endedAt := deps.now()
			span := Span{
				TraceID:      traceID,
				SpanID:       spanID,
				ParentSpanID: parentSpanID,
				Name:         name,
				StartedAt:    startedAt,
				EndedAt:      endedAt,
				Duration:     endedAt.Sub(startedAt),
			}
			if err != nil {
				span.Error = err.Error()
			}
			if recorder != nil {
				recorder.RecordSpan(span)
			}
		})
	}
	return spanCtx, finish
}

func newTraceID(generator idgen.ErrorGenerator) string {
	if generator != nil {
		if id, err := generator.NewIDContext(); err == nil && id != "" {
			return id
		}
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(bytes[:])
}

func newSpanID(generator idgen.ErrorGenerator) string {
	if generator != nil {
		if id, err := generator.NewIDContext(); err == nil && id != "" {
			return id
		}
	}
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		fallback := hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		if len(fallback) >= 16 {
			return fallback[:16]
		}
		return fallback
	}
	return hex.EncodeToString(bytes[:])
}
