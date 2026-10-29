package lifecycle

import (
	"context"

	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/tracing"
)

type MetricSink interface {
	CaptureContext(ctx context.Context, name string, fields map[string]any) bool
}

type boundedMetricSink struct {
	sink *metrics.BoundedSink
}

func NewMetricSink(sink *metrics.BoundedSink) MetricSink {
	return boundedMetricSink{sink: sink}
}

func (s boundedMetricSink) CaptureContext(_ context.Context, name string, fields map[string]any) bool {
	if s.sink == nil {
		return false
	}
	return s.sink.Capture(metrics.Event{Name: name, Fields: fields})
}

func signalTelemetryFields(ctx context.Context, signal SignalEnvelope) map[string]any {
	fields := map[string]any{
		"signal_name":    string(signal.Name),
		"mission_id":     signal.MissionID.String(),
		"agent_id":       signal.AgentID.String(),
		"task_id":        signal.TaskID.String(),
		"run_id":         signal.RunID.String(),
		"correlation_id": string(signal.CorrelationID),
		"causation_id":   string(signal.CausationID),
		"attempt":        signal.Attempt,
	}
	if traceID := tracing.TraceID(ctx); traceID != "" {
		fields["trace_id"] = traceID
	}
	if spanID := tracing.SpanID(ctx); spanID != "" {
		fields["span_id"] = spanID
	}
	return fields
}
