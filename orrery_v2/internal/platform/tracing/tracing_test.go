package tracing_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/tracing"
)

func TestTraceIDContextHelpers(t *testing.T) {
	ctx := tracing.WithTraceID(context.Background(), "trace-1")
	if got := tracing.TraceID(ctx); got != "trace-1" {
		t.Fatalf("TraceID = %q, want trace-1", got)
	}
}

func TestSpanIDContextHelpers(t *testing.T) {
	ctx := tracing.WithSpanID(context.Background(), "span-1")
	if got := tracing.SpanID(ctx); got != "span-1" {
		t.Fatalf("SpanID = %q, want span-1", got)
	}
}

func TestStartSpanAddsContextAndEmitsFinishedSpan(t *testing.T) {
	recorder := &spanRecorder{}
	now := fakeClock(
		time.Unix(100, 0),
		time.Unix(100, int64(25*time.Millisecond)),
	)
	ctx := tracing.WithTraceID(context.Background(), "trace-1")
	ctx = tracing.WithSpanID(ctx, "parent-1")

	spanCtx, finish := tracing.StartSpan(ctx, "queue.claim", recorder, now)
	if tracing.TraceID(spanCtx) != "trace-1" {
		t.Fatalf("TraceID(spanCtx) = %q, want trace-1", tracing.TraceID(spanCtx))
	}
	if tracing.SpanID(spanCtx) == "" || tracing.SpanID(spanCtx) == "parent-1" {
		t.Fatalf("SpanID(spanCtx) = %q, want generated child span", tracing.SpanID(spanCtx))
	}

	finish(errors.New("boom"))
	if len(recorder.spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(recorder.spans))
	}
	span := recorder.spans[0]
	if span.TraceID != "trace-1" || span.ParentSpanID != "parent-1" || span.SpanID != tracing.SpanID(spanCtx) {
		t.Fatalf("span identifiers = %+v", span)
	}
	if span.Name != "queue.claim" || span.Error != "boom" {
		t.Fatalf("span = %+v, want name queue.claim and error boom", span)
	}
	if span.Duration != 25*time.Millisecond {
		t.Fatalf("Duration = %s, want 25ms", span.Duration)
	}

	finish(nil)
	if len(recorder.spans) != 1 {
		t.Fatalf("finish emitted more than once: %d spans", len(recorder.spans))
	}
}

func TestStartSpanWithOptionsUsesInjectedClockAndIDGenerator(t *testing.T) {
	recorder := &spanRecorder{}
	start := time.Date(2026, 5, 31, 20, 0, 0, 0, time.UTC)
	clock := clock.NewFake(start)
	ids := idgen.NewDeterministic("trace")

	spanCtx, finish := tracing.StartSpanWithOptions(context.Background(), "runtime.trace", recorder, tracing.SpanOptions{
		Clock:       clock,
		IDGenerator: ids,
	})
	if tracing.TraceID(spanCtx) != "trace-000001" {
		t.Fatalf("TraceID = %q, want injected first ID", tracing.TraceID(spanCtx))
	}
	if tracing.SpanID(spanCtx) != "trace-000002" {
		t.Fatalf("SpanID = %q, want injected second ID", tracing.SpanID(spanCtx))
	}
	clock.Advance(25 * time.Millisecond)
	finish(nil)

	if len(recorder.spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(recorder.spans))
	}
	span := recorder.spans[0]
	if !span.StartedAt.Equal(start) || !span.EndedAt.Equal(start.Add(25*time.Millisecond)) || span.Duration != 25*time.Millisecond {
		t.Fatalf("span timing = %+v, want injected clock", span)
	}
}

func TestExtractTraceParentInjectsTraceSpanAndCorrelationID(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentID := "00f067aa0ba902b7"
	ctx, ok := tracing.ExtractTraceParent(context.Background(), "00-"+traceID+"-"+parentID+"-01")
	if !ok {
		t.Fatalf("ExtractTraceParent returned ok=false")
	}
	if tracing.TraceID(ctx) != traceID {
		t.Fatalf("TraceID = %q, want %q", tracing.TraceID(ctx), traceID)
	}
	if tracing.SpanID(ctx) != parentID {
		t.Fatalf("SpanID = %q, want %q", tracing.SpanID(ctx), parentID)
	}
	if logging.CorrelationID(ctx) != traceID {
		t.Fatalf("CorrelationID = %q, want trace id", logging.CorrelationID(ctx))
	}
}

func TestFormatTraceParentUsesCurrentContext(t *testing.T) {
	ctx := tracing.WithTraceID(context.Background(), "4bf92f3577b34da6a3ce929d0e0e4736")
	ctx = tracing.WithSpanID(ctx, "00f067aa0ba902b7")
	got, ok := tracing.FormatTraceParent(ctx)
	if !ok {
		t.Fatalf("FormatTraceParent returned ok=false")
	}
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if got != want {
		t.Fatalf("traceparent = %q, want %q", got, want)
	}
}

func TestExtractTraceParentRejectsMalformedHeader(t *testing.T) {
	ctx, ok := tracing.ExtractTraceParent(context.Background(), "not-a-traceparent")
	if ok {
		t.Fatalf("ExtractTraceParent returned ok=true for malformed header")
	}
	if tracing.TraceID(ctx) != "" || tracing.SpanID(ctx) != "" {
		t.Fatalf("malformed traceparent modified context")
	}
}

func TestHTTPMiddlewareExtractsTraceParentAndRecordsSpan(t *testing.T) {
	recorder := &spanRecorder{}
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentID := "00f067aa0ba902b7"
	handler := tracing.HTTPMiddleware("http.request", recorder)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tracing.TraceID(r.Context()) != traceID {
			t.Fatalf("handler TraceID = %q, want %q", tracing.TraceID(r.Context()), traceID)
		}
		if tracing.SpanID(r.Context()) == "" || tracing.SpanID(r.Context()) == parentID {
			t.Fatalf("handler SpanID = %q, want generated child span", tracing.SpanID(r.Context()))
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/run", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-"+parentID+"-01")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusAccepted)
	}
	if got := res.Header().Get("traceparent"); !strings.HasPrefix(got, "00-"+traceID+"-") {
		t.Fatalf("response traceparent = %q, want trace id %q", got, traceID)
	}
	if len(recorder.spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(recorder.spans))
	}
	if recorder.spans[0].TraceID != traceID || recorder.spans[0].ParentSpanID != parentID || recorder.spans[0].Name != "http.request" {
		t.Fatalf("recorded span = %+v", recorder.spans[0])
	}
}

func TestHTTPMiddlewareWithOptionsUsesInjectedClockAndIDGenerator(t *testing.T) {
	recorder := &spanRecorder{}
	now := time.Date(2026, 5, 31, 20, 30, 0, 0, time.UTC)
	clock := clock.NewFake(now)
	ids := idgen.NewDeterministic("trace")
	handler := tracing.HTTPMiddlewareWithOptions("http.test", recorder, tracing.SpanOptions{
		Clock:       clock,
		IDGenerator: ids,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tracing.TraceID(r.Context()) != "trace-000001" {
			t.Fatalf("TraceID = %q, want injected trace ID", tracing.TraceID(r.Context()))
		}
		if tracing.SpanID(r.Context()) != "trace-000002" {
			t.Fatalf("SpanID = %q, want injected span ID", tracing.SpanID(r.Context()))
		}
		clock.Advance(10 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/trace", nil))

	if len(recorder.spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(recorder.spans))
	}
	span := recorder.spans[0]
	if span.TraceID != "trace-000001" || span.SpanID != "trace-000002" || span.Duration != 10*time.Millisecond {
		t.Fatalf("span = %+v, want injected ID and clock", span)
	}
}

func TestBoundedRecorderDoesNotBlockWhenFull(t *testing.T) {
	recorder := tracing.NewBoundedRecorder(1)
	recorder.RecordSpan(tracing.Span{Name: "first"})
	recorder.RecordSpan(tracing.Span{Name: "second"})

	spans := recorder.Drain()
	if len(spans) != 1 {
		t.Fatalf("drained %d spans, want 1", len(spans))
	}
	if spans[0].Name != "first" {
		t.Fatalf("span = %+v, want first span retained", spans[0])
	}
	if recorder.Dropped() != 1 {
		t.Fatalf("Dropped = %d, want 1", recorder.Dropped())
	}
}

func TestBoundedRecorderDrainClearsBufferedSpans(t *testing.T) {
	recorder := tracing.NewBoundedRecorder(10)
	recorder.RecordSpan(tracing.Span{Name: "one"})
	recorder.RecordSpan(tracing.Span{Name: "two"})

	if got := recorder.Drain(); len(got) != 2 {
		t.Fatalf("first drain returned %d spans, want 2", len(got))
	}
	if got := recorder.Drain(); len(got) != 0 {
		t.Fatalf("second drain returned %d spans, want 0", len(got))
	}
}

type spanRecorder struct {
	spans []tracing.Span
}

func (r *spanRecorder) RecordSpan(span tracing.Span) {
	r.spans = append(r.spans, span)
}

func fakeClock(times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		if i >= len(times) {
			return times[len(times)-1]
		}
		t := times[i]
		i++
		return t
	}
}
