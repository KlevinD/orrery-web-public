package metrics_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/metrics"
)

func TestBoundedSinkDropsWhenFullWithoutBlocking(t *testing.T) {
	sink := metrics.NewBoundedSink(1)

	if ok := sink.Capture(metrics.Event{Name: "one"}); !ok {
		t.Fatalf("first capture dropped")
	}
	if ok := sink.Capture(metrics.Event{Name: "two"}); ok {
		t.Fatalf("second capture should drop when full")
	}
	if sink.Dropped() != 1 {
		t.Fatalf("Dropped = %d, want 1", sink.Dropped())
	}
}

func TestSnapshotReportsQueueDepthAndDrops(t *testing.T) {
	sink := metrics.NewBoundedSink(1)
	sink.Capture(metrics.Event{Name: "one"})
	sink.Capture(metrics.Event{Name: "two"})

	snapshot := sink.Snapshot()
	if snapshot.Buffered != 1 || snapshot.Dropped != 1 {
		t.Fatalf("snapshot = %+v, want buffered=1 dropped=1", snapshot)
	}
}

func TestDrainReturnsBufferedEventsAndTracksEmittedCount(t *testing.T) {
	sink := metrics.NewBoundedSink(3)
	sink.Capture(metrics.Event{Name: "one"})
	sink.Capture(metrics.Event{Name: "two"})

	events := sink.Drain(10)
	if len(events) != 2 {
		t.Fatalf("Drain returned %d events, want 2", len(events))
	}
	if events[0].Name != "one" || events[1].Name != "two" {
		t.Fatalf("Drain events = %+v, want one,two", events)
	}
	snapshot := sink.Snapshot()
	if snapshot.Buffered != 0 || snapshot.Emitted != 2 {
		t.Fatalf("snapshot = %+v, want buffered=0 emitted=2", snapshot)
	}
}

func TestWarnIfDroppedLogsPeriodicSummary(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewTextLogger(&out, logging.LevelDebug)
	sink := metrics.NewBoundedSinkWithWarnInterval(1, time.Minute)
	sink.Capture(metrics.Event{Name: "one"})
	sink.Capture(metrics.Event{Name: "two"})

	now := time.Unix(100, 0)
	if !sink.WarnIfDropped(context.Background(), logger, now) {
		t.Fatalf("first WarnIfDropped returned false, want true")
	}
	if sink.WarnIfDropped(context.Background(), logger, now.Add(30*time.Second)) {
		t.Fatalf("second WarnIfDropped before interval returned true")
	}
	if !sink.WarnIfDropped(context.Background(), logger, now.Add(time.Minute)) {
		t.Fatalf("third WarnIfDropped after interval returned false")
	}

	logged := out.String()
	if strings.Count(logged, "metrics events dropped") != 2 {
		t.Fatalf("log output = %q, want two drop summaries", logged)
	}
	if !strings.Contains(logged, "dropped=1") {
		t.Fatalf("log output = %q, want dropped count", logged)
	}
}

func TestBatchRelayDrainsSinkOnClose(t *testing.T) {
	sink := metrics.NewBoundedSink(4)
	var (
		mu      sync.Mutex
		batches [][]metrics.Event
	)
	relay, err := metrics.NewBatchRelay(sink, metrics.BatchRelayConfig{
		BatchSize:  2,
		FlushEvery: time.Hour,
		Writer: metrics.EventWriterFunc(func(ctx context.Context, events []metrics.Event) error {
			mu.Lock()
			defer mu.Unlock()
			batches = append(batches, append([]metrics.Event(nil), events...))
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("NewBatchRelay returned error: %v", err)
	}
	relay.Start()

	sink.Capture(metrics.Event{Name: "one"})
	sink.Capture(metrics.Event{Name: "two"})
	sink.Capture(metrics.Event{Name: "three"})
	if err := relay.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, batch := range batches {
		total += len(batch)
	}
	if total != 3 {
		t.Fatalf("writer received %d events in %d batches, want 3", total, len(batches))
	}
	stats := relay.Stats()
	if stats.Sent != 3 || stats.Failed != 0 {
		t.Fatalf("relay stats = %+v, want sent=3 failed=0", stats)
	}
}

func TestBatchRelayFlushesWhenBatchSizeReached(t *testing.T) {
	sink := metrics.NewBoundedSink(4)
	flushed := make(chan []metrics.Event, 1)
	relay, err := metrics.NewBatchRelay(sink, metrics.BatchRelayConfig{
		BatchSize:  2,
		FlushEvery: time.Hour,
		Writer: metrics.EventWriterFunc(func(ctx context.Context, events []metrics.Event) error {
			flushed <- append([]metrics.Event(nil), events...)
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("NewBatchRelay returned error: %v", err)
	}
	relay.Start()
	defer relay.Close(context.Background())

	sink.Capture(metrics.Event{Name: "one"})
	sink.Capture(metrics.Event{Name: "two"})

	select {
	case events := <-flushed:
		if len(events) != 2 || events[0].Name != "one" || events[1].Name != "two" {
			t.Fatalf("flushed events = %+v, want one,two", events)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("relay did not flush when batch size was reached")
	}
}

func TestBatchRelayCountsWriterFailuresWithoutLosingSinkDropStats(t *testing.T) {
	sink := metrics.NewBoundedSink(1)
	sink.Capture(metrics.Event{Name: "one"})
	sink.Capture(metrics.Event{Name: "dropped"})

	relay, err := metrics.NewBatchRelay(sink, metrics.BatchRelayConfig{
		BatchSize:  10,
		FlushEvery: time.Hour,
		Writer: metrics.EventWriterFunc(func(ctx context.Context, events []metrics.Event) error {
			return errors.New("downstream unavailable")
		}),
	})
	if err != nil {
		t.Fatalf("NewBatchRelay returned error: %v", err)
	}
	relay.Start()
	if err := relay.Close(context.Background()); err == nil {
		t.Fatalf("Close returned nil error, want writer failure")
	}

	stats := relay.Stats()
	if stats.Sent != 0 || stats.Failed != 1 || stats.Dropped != 1 {
		t.Fatalf("relay stats = %+v, want sent=0 failed=1 dropped=1", stats)
	}
}

func TestRegistryHandlerExposesBuildAndHTTPMetrics(t *testing.T) {
	registry := metrics.NewRegistry(metrics.RegistryOptions{Version: "v-test", Commit: "abc123"})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})
	handler := registry.HTTP.Middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/items/secret-id?token=secret", nil)
	req = metrics.WithRoutePattern(req, "/api/items/{id}")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}

	metricsRec := httptest.NewRecorder()
	metrics.NewHandler(registry.Gatherer).ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()
	for _, want := range []string{
		`orrery_build_info{commit="abc123",version="v-test"} 1`,
		`orrery_http_requests_total{method="GET",route="/api/items/{id}",status="201"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q\n%s", want, body)
		}
	}
	for _, leaked := range []string{"secret-id", "secret"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("metrics body leaked %q\n%s", leaked, body)
		}
	}
}

func TestHTTPMetricsMiddlewareUsesInjectedClock(t *testing.T) {
	now := time.Date(2026, 5, 31, 21, 30, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	registry := metrics.NewRegistry(metrics.RegistryOptions{
		Version: "v-test",
		HTTP: metrics.HTTPOptions{
			Clock: fakeClock,
		},
	})

	handler := registry.HTTP.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fakeClock.Advance(250 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/clock", nil)
	req = metrics.WithRoutePattern(req, "/api/clock")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	rec := httptest.NewRecorder()
	metrics.NewHandler(registry.Gatherer).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `orrery_http_request_duration_seconds_sum{method="GET",route="/api/clock",status="202"} 0.25`) {
		t.Fatalf("metrics body missing injected clock duration:\n%s", body)
	}
}

func TestMetricsHandlerOnlyServesMetricsPath(t *testing.T) {
	registry := metrics.NewRegistry(metrics.RegistryOptions{})
	handler := metrics.NewHandler(registry.Gatherer)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "orrery_build_info") {
		t.Fatalf("/metrics body missing build info: %s", body)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/health status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRegistryExposesDatabaseStatsPerRole(t *testing.T) {
	registry := metrics.NewRegistry(metrics.RegistryOptions{
		Databases: map[string]metrics.DBStatsProvider{
			"core": fakeDBStats{stats: sql.DBStats{OpenConnections: 1, InUse: 1, Idle: 0}},
			"jobs": fakeDBStats{stats: sql.DBStats{OpenConnections: 2, InUse: 0, Idle: 2}},
		},
	})

	rec := httptest.NewRecorder()
	metrics.NewHandler(registry.Gatherer).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`orrery_db_open_connections{role="core"} 1`,
		`orrery_db_in_use_connections{role="core"} 1`,
		`orrery_db_idle_connections{role="jobs"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q\n%s", want, body)
		}
	}
}

func TestRegistryExposesStatusProviderCounts(t *testing.T) {
	registry := metrics.NewRegistry(metrics.RegistryOptions{
		StatusGroups: []metrics.StatusGroup{
			{
				Subsystem: "outbox",
				Name:      "jobs",
				Help:      "Outbox jobs by status.",
				Providers: map[string]metrics.StatusCountsProvider{
					"jobs": fakeStatusCounts{counts: map[string]int64{"pending": 2, "running": 1}},
				},
			},
			{
				Subsystem: "execution",
				Name:      "jobs",
				Help:      "Execution jobs by status.",
				Providers: map[string]metrics.StatusCountsProvider{
					"jobs": fakeStatusCounts{counts: map[string]int64{"queued": 3, "failed": 1}},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	metrics.NewHandler(registry.Gatherer).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`orrery_outbox_jobs{role="jobs",status="pending"} 2`,
		`orrery_outbox_jobs{role="jobs",status="running"} 1`,
		`orrery_execution_jobs{role="jobs",status="queued"} 3`,
		`orrery_execution_jobs{role="jobs",status="failed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q\n%s", want, body)
		}
	}
}

type fakeDBStats struct {
	stats sql.DBStats
}

func (f fakeDBStats) Stats() sql.DBStats {
	return f.stats
}

type fakeStatusCounts struct {
	counts map[string]int64
}

func (f fakeStatusCounts) StatusCounts(context.Context) (map[string]int64, error) {
	return f.counts, nil
}
