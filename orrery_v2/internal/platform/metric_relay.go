package platform

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/platform/metrics"
)

type MetricEvent struct {
	Name   string
	Fields map[string]any
}

type MetricEventWriter interface {
	WriteMetricEvents(context.Context, []MetricEvent) error
}

type MetricEventWriterFunc func(context.Context, []MetricEvent) error

func (fn MetricEventWriterFunc) WriteMetricEvents(ctx context.Context, events []MetricEvent) error {
	if fn == nil {
		return fmt.Errorf("metric event writer is nil")
	}
	return fn(ctx, events)
}

type MetricsRelayRuntimeConfig struct {
	BatchSize  int
	FlushEvery time.Duration
}

type metricWriterAdapter struct {
	writer MetricEventWriter
}

func (a metricWriterAdapter) WriteEvents(ctx context.Context, events []metrics.Event) error {
	batch := make([]MetricEvent, 0, len(events))
	for _, event := range events {
		batch = append(batch, metricEventFromMetrics(event))
	}
	return a.writer.WriteMetricEvents(ctx, batch)
}

func metricEventFromMetrics(event metrics.Event) MetricEvent {
	return MetricEvent{Name: event.Name, Fields: event.Fields}
}

func multiMetricWriter(writers []registeredMetricWriter) metrics.EventWriter {
	return metrics.EventWriterFunc(func(ctx context.Context, events []metrics.Event) error {
		for _, registered := range writers {
			if err := (metricWriterAdapter{writer: registered.writer}).WriteEvents(ctx, events); err != nil {
				return fmt.Errorf("metric writer %q: %w", registered.name, err)
			}
		}
		return nil
	})
}

type registeredMetricWriter struct {
	name   string
	writer MetricEventWriter
}
