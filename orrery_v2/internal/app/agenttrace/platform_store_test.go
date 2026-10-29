package agenttrace

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

var traceStoreTestTime = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

func TestPlatformStoreUpsertsTraceLifecycleWithoutPayloadContent(t *testing.T) {
	store, sqlStore := newTracePlatformStore(t)
	record := TraceRecord{
		TenantID: "tenant-1", RunID: "run-1", TraceVersion: 2,
		TraceRef: "artifact://agent-runs/run-1/trace-v2/runtime-trace.json",
		Status:   TraceRunning, StartedAt: traceStoreTestTime,
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := store.Get(context.Background(), "tenant-1", "run-1", 2)
	if err != nil || !ok {
		t.Fatalf("stored=%+v ok=%v err=%v", stored, ok, err)
	}
	stored.Status = TraceHot
	stored.TerminalStatus = "failed"
	stored.EventCount = 7
	stored.ByteCount = 8192
	stored.CompletedAt = traceStoreTestTime.Add(time.Minute)
	if err := store.Save(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(context.Background(), "tenant-1", "run-1", 2)
	if err != nil || !ok || got.TerminalStatus != "failed" || got.EventCount != 7 || got.Revision != 2 {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}

	rows, err := sqlStore.Query(context.Background(), `PRAGMA table_info(agent_runtime_traces)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&index, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "payload" || name == "payload_json" || name == "prompt" || name == "event_json" {
			t.Fatalf("payload column must not exist: %s", name)
		}
	}
}

func TestPlatformStoreTraceLegalHoldIsTenantScoped(t *testing.T) {
	store, _ := newTracePlatformStore(t)
	seedTraceRecord(t, store, "tenant-a", "run-1")
	if err := store.ApplyLegalHold(context.Background(), "tenant-a", "run-1", 2, "operator-1", "incident review", traceStoreTestTime); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get(context.Background(), "tenant-b", "run-1", 2); err != nil || ok {
		t.Fatalf("cross-tenant read ok=%v err=%v", ok, err)
	}
	got, ok, err := store.Get(context.Background(), "tenant-a", "run-1", 2)
	if err != nil || !ok || got.LegalHoldActor != "operator-1" || got.LegalHoldReason != "incident review" {
		t.Fatalf("held trace=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestPlatformStoreRejectsStaleTraceRevision(t *testing.T) {
	store, _ := newTracePlatformStore(t)
	seedTraceRecord(t, store, "tenant-a", "run-1")
	first, ok, err := store.Get(context.Background(), "tenant-a", "run-1", 2)
	if err != nil || !ok {
		t.Fatal(first, ok, err)
	}
	stale := first
	first.EventCount = 1
	if err := store.Save(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	stale.EventCount = 2
	if err := store.Save(context.Background(), stale); !errors.Is(err, ErrTraceRevisionConflict) {
		t.Fatalf("stale save error=%v", err)
	}
}

func TestPlatformStoreTraceIndexSinkTransitionsRunningToHot(t *testing.T) {
	store, _ := newTracePlatformStore(t)
	ctx := context.Background()
	if err := store.SaveTraceIndex(ctx, traceIndexRecord("tenant-a", "run-1", "")); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTraceIndex(ctx, traceIndexRecord("tenant-a", "run-1", "failed")); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTraceIndex(ctx, traceIndexRecord("tenant-a", "run-1", "")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, "tenant-a", "run-1", 2)
	if err != nil || !ok || got.Status != TraceHot || got.TerminalStatus != "failed" || got.EventCount != 4 {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
}

func newTracePlatformStore(t *testing.T) (*PlatformStore, *db.Store) {
	t.Helper()
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "agent-trace-" + t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	if err := migration.NewRunner([]migration.Migration{agentRuntimeTracesMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatal(err)
	}
	store := NewPlatformStore(sqlStore)
	store.now = func() time.Time { return traceStoreTestTime }
	return store, sqlStore
}

func seedTraceRecord(t *testing.T, store *PlatformStore, tenantID, runID string) {
	t.Helper()
	if err := store.Save(context.Background(), TraceRecord{
		TenantID: tenantID, RunID: runID, TraceVersion: 2,
		TraceRef: "artifact://" + tenantID + "/" + runID,
		Status:   TraceRunning, StartedAt: traceStoreTestTime,
	}); err != nil {
		t.Fatal(err)
	}
}

func traceIndexRecord(tenantID, runID, terminalStatus string) agentcore.TraceIndexRecord {
	return agentcore.TraceIndexRecord{
		TenantID: tenantID, RunID: runID, TraceVersion: 2,
		TraceRef:       "artifact://" + tenantID + "/" + runID,
		TerminalStatus: terminalStatus, EventCount: 4, ByteCount: 1024,
		StartedAt: traceStoreTestTime, CompletedAt: traceStoreTestTime.Add(time.Minute),
	}
}
