package platform

import (
	"context"
	"fmt"

	"orrery_v2/internal/platform/tracing"
)

type Tracer struct {
	recorder *tracing.BoundedRecorder
	options  tracing.SpanOptions
}

func NewTracer(recorder *tracing.BoundedRecorder, options tracing.SpanOptions) *Tracer {
	return &Tracer{recorder: recorder, options: options}
}

func (t *Tracer) Start(ctx context.Context, name string) (context.Context, func(error)) {
	if t == nil || t.recorder == nil {
		return ctx, func(error) {}
	}
	return tracing.StartSpanWithOptions(ctx, name, t.recorder, t.options)
}

func (t *Tracer) Record(span tracing.Span) error {
	if t == nil || t.recorder == nil {
		return fmt.Errorf("platform tracer is not configured")
	}
	t.recorder.RecordSpan(span)
	return nil
}
