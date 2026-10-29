package observability

import "time"

type MetricSink interface {
	Capture(name string, fields map[string]any) bool
}

type Metrics struct {
	sink MetricSink
}

func NewMetrics(sink MetricSink) *Metrics {
	return &Metrics{sink: sink}
}

func (m *Metrics) RecordIngestJob(status string) {
	m.capture("knowledge_ingest_jobs_total", map[string]any{"status": status})
}

func (m *Metrics) RecordEmbeddingBatch(size int) {
	m.capture("knowledge_embedding_batch_size", map[string]any{"size": size})
}

func (m *Metrics) RecordSearch(duration time.Duration, results int) {
	m.capture("knowledge_search_duration_ms", map[string]any{"duration_ms": duration.Milliseconds()})
	m.capture("knowledge_search_results_total", map[string]any{"results": results})
}

func (m *Metrics) RecordVectorUpsert(duration time.Duration, chunks int) {
	m.capture("knowledge_vector_upsert_duration_ms", map[string]any{"duration_ms": duration.Milliseconds(), "chunks": chunks})
}

func (m *Metrics) capture(name string, fields map[string]any) {
	if m == nil || m.sink == nil {
		return
	}
	m.sink.Capture(name, fields)
}
