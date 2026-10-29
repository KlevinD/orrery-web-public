package platform

import (
	"context"
	"fmt"

	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
)

type MetricRecorder struct {
	sink   *metrics.BoundedSink
	logger logging.Logger
}

func NewMetricRecorder(sink *metrics.BoundedSink, logger logging.Logger) *MetricRecorder {
	return &MetricRecorder{sink: sink, logger: logger}
}

func (r *MetricRecorder) Capture(name string, fields map[string]any) bool {
	if r == nil || r.sink == nil {
		return false
	}
	return r.sink.Capture(metrics.Event{Name: name, Fields: fields})
}

func (r *MetricRecorder) CaptureContext(ctx context.Context, name string, fields map[string]any) bool {
	ok := r.Capture(name, fields)
	if !ok && r.logger != nil {
		r.logger.Warn(ctx, "metric event dropped",
			logging.String("operation", "metrics.capture"),
			logging.String("metric", name),
			logging.String("error_code", "metrics_buffer_full"),
		)
	}
	return ok
}

func (r *MetricRecorder) Drain(max int) ([]metrics.Event, error) {
	if r == nil || r.sink == nil {
		return nil, fmt.Errorf("platform metric recorder is not configured")
	}
	return r.sink.Drain(max), nil
}
