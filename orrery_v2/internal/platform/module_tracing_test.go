package platform_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/tracing"
)

func TestBusinessModuleStartsSpanThroughRegistrarTracer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	now := time.Date(2026, 5, 31, 23, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	ids := idgen.NewDeterministic("span")
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		spanCtx, finish := registry.Tracer().Start(ctx, "business.tracer")
		if tracing.TraceID(spanCtx) != "span-000001" || tracing.SpanID(spanCtx) != "span-000002" {
			t.Fatalf("span IDs = trace %q span %q", tracing.TraceID(spanCtx), tracing.SpanID(spanCtx))
		}
		fakeClock.Advance(20 * time.Millisecond)
		finish(errors.New("boom"))
		return nil
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{
		Clock:       fakeClock,
		IDGenerator: ids,
	}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	spans := rt.Traces.Drain()
	if len(spans) != 1 {
		t.Fatalf("spans = %+v, want one span", spans)
	}
	span := spans[0]
	if span.Name != "business.tracer" || span.Duration != 20*time.Millisecond || !strings.Contains(span.Error, "boom") {
		t.Fatalf("span = %+v, want tracer facade span", span)
	}
}

func TestBusinessModuleRecordsTracesThroughRegistrar(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		registry.Traces().RecordSpan(tracing.Span{Name: "business.register"})
		return nil
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	spans := rt.Traces.Drain()
	if len(spans) != 1 || spans[0].Name != "business.register" {
		t.Fatalf("recorded spans = %+v, want business.register", spans)
	}
}

func TestBusinessModuleStartsSpansThroughRegistrarTracingOptions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		opts := registry.TracingOptions()
		if opts.Clock == nil {
			t.Fatalf("registrar tracing options missing platform clock")
		}
		if opts.IDGenerator == nil {
			t.Fatalf("registrar tracing options missing platform ID generator")
		}
		spanCtx, finish := tracing.StartSpanWithOptions(ctx, "business.register.span", registry.Traces(), opts)
		defer finish(nil)
		if tracing.TraceID(spanCtx) == "" {
			t.Fatalf("span context missing trace id")
		}
		if tracing.SpanID(spanCtx) == "" {
			t.Fatalf("span context missing span id")
		}
		return nil
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	spans := rt.Traces.Drain()
	if len(spans) != 1 || spans[0].Name != "business.register.span" {
		t.Fatalf("recorded spans = %+v, want business.register.span", spans)
	}
	if !strings.HasPrefix(spans[0].TraceID, "orrery-") || !strings.HasPrefix(spans[0].SpanID, "orrery-") {
		t.Fatalf("span IDs = trace %q span %q, want platform-generated orrery IDs", spans[0].TraceID, spans[0].SpanID)
	}
}
