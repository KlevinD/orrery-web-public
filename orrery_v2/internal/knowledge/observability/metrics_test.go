package observability_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/observability"
)

func TestMetricsRecorderCapturesKnowledgeEvents(t *testing.T) {
	sink := &fakeMetricSink{}
	metrics := observability.NewMetrics(sink)

	metrics.RecordIngestJob("completed")
	metrics.RecordEmbeddingBatch(12)
	metrics.RecordSearch(time.Second, 3)
	metrics.RecordVectorUpsert(1500*time.Millisecond, 4)

	names := []string{}
	for _, event := range sink.events {
		names = append(names, event.name)
	}
	assertContainsMetric(t, names, "knowledge_ingest_jobs_total")
	assertContainsMetric(t, names, "knowledge_embedding_batch_size")
	assertContainsMetric(t, names, "knowledge_search_duration_ms")
	assertContainsMetric(t, names, "knowledge_search_results_total")
	assertContainsMetric(t, names, "knowledge_vector_upsert_duration_ms")
}

func TestStatusProviderConvertsTypedKnowledgeCounts(t *testing.T) {
	provider := observability.NewStatusProvider(observability.StatusDeps{
		KnowledgeBases: func(context.Context) (map[core.KnowledgeStatus]int64, error) {
			return map[core.KnowledgeStatus]int64{core.KnowledgeActive: 2, core.KnowledgeDeleting: 1}, nil
		},
		Documents: func(context.Context) (map[core.DocumentStatus]int64, error) {
			return map[core.DocumentStatus]int64{core.DocumentIndexed: 3, core.DocumentFailed: 1}, nil
		},
		Jobs: func(context.Context) (map[core.JobStatus]int64, error) {
			return map[core.JobStatus]int64{core.JobQueued: 4, core.JobFailed: 2}, nil
		},
	})

	bases, err := provider.KnowledgeBaseStatusCounts(context.Background())
	if err != nil {
		t.Fatalf("base status counts: %v", err)
	}
	documents, err := provider.DocumentStatusCounts(context.Background())
	if err != nil {
		t.Fatalf("document status counts: %v", err)
	}
	jobs, err := provider.JobStatusCounts(context.Background())
	if err != nil {
		t.Fatalf("job status counts: %v", err)
	}
	if bases["active"] != 2 || bases["deleting"] != 1 {
		t.Fatalf("base counts mismatch: %#v", bases)
	}
	if documents["indexed"] != 3 || documents["failed"] != 1 {
		t.Fatalf("document counts mismatch: %#v", documents)
	}
	if jobs["queued"] != 4 || jobs["failed"] != 2 {
		t.Fatalf("job counts mismatch: %#v", jobs)
	}
}

type fakeMetricSink struct {
	events []metricEvent
}

type metricEvent struct {
	name   string
	fields map[string]any
}

func (s *fakeMetricSink) Capture(name string, fields map[string]any) bool {
	s.events = append(s.events, metricEvent{name: name, fields: fields})
	return true
}

func assertContainsMetric(t *testing.T, names []string, want string) {
	t.Helper()
	for _, name := range names {
		if name == want {
			return
		}
	}
	t.Fatalf("metrics %v missing %s", names, want)
}
