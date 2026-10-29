package agenttrace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestArchiveIsDeterministicAndVerifiedBeforeHotDeletion(t *testing.T) {
	entries := []ArchiveEntry{
		{Kind: "manifest", Ref: "runtime-trace.json", Data: []byte(`{"trace_version":2}`)},
		{Kind: "event", Ref: "events/0001.json", Data: []byte(`{"ordinal":1}`)},
	}
	first, err := BuildArchive(entries)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildArchive(entries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("archive is not deterministic")
	}
	if err := VerifyArchive(first, len(entries)); err != nil {
		t.Fatal(err)
	}
	restored, err := ParseArchive(first)
	if err != nil || len(restored) != len(entries) || restored[1].Ref != entries[1].Ref {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}

	fixture := newRetentionFixture()
	fixture.objects.failReadback = true
	fixture.seedHot("completed", fixture.now.Add(-31*24*time.Hour))
	if _, err := fixture.service.Run(context.Background(), 100); err == nil {
		t.Fatal("expected verification failure")
	}
	if fixture.objects.hotDeleteCalls != 0 {
		t.Fatal("hot trace deleted before archive verification")
	}
}

func TestRetentionArchivesCompletedAtThirtyDaysAndFailureAtNinety(t *testing.T) {
	fixture := newRetentionFixture()
	fixture.seedHot("completed", fixture.now.Add(-30*24*time.Hour))
	fixture.seedHot("failed", fixture.now.Add(-89*24*time.Hour))
	report, err := fixture.service.Run(context.Background(), 100)
	if err != nil || report.Archived != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	fixture.now = fixture.now.Add(24 * time.Hour)
	fixture.service.now = func() time.Time { return fixture.now }
	report, err = fixture.service.Run(context.Background(), 100)
	if err != nil || report.Archived != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRetentionPurgesArchiveAt365DaysUnlessHeld(t *testing.T) {
	fixture := newRetentionFixture()
	held := fixture.seedArchive(fixture.now.Add(-365*24*time.Hour), true)
	expired := fixture.seedArchive(fixture.now.Add(-365*24*time.Hour), false)
	report, err := fixture.service.Run(context.Background(), 100)
	if err != nil || report.Purged != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if fixture.objects.exists(expired.ArchiveRef) || !fixture.objects.exists(held.ArchiveRef) {
		t.Fatal("legal hold boundary violated")
	}
}

func TestRetentionBatchIsBoundedToOneHundred(t *testing.T) {
	fixture := newRetentionFixture()
	for index := 0; index < 101; index++ {
		fixture.seedHot("completed", fixture.now.Add(-31*24*time.Hour))
	}
	report, err := fixture.service.Run(context.Background(), 100)
	if err != nil || report.Archived != 100 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRetentionAdvancesOptimisticRevisionBetweenArchiveTransitions(t *testing.T) {
	store, _ := newTracePlatformStore(t)
	now := traceStoreTestTime
	record := TraceRecord{
		TenantID: "tenant-a", RunID: "run-revision", TraceVersion: 2, TraceRef: "hot://run-revision",
		Status: TraceHot, TerminalStatus: "completed", StartedAt: now.Add(-31 * 24 * time.Hour),
		CompletedAt: now.Add(-31 * 24 * time.Hour), RetainUntil: now.Add(-time.Hour),
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	objects := &memoryTraceObjects{data: map[string][]byte{"hot://run-revision": []byte(`{"trace_version":2}`)}}
	service := NewRetentionService(store, objects, func() time.Time { return now })
	if report, err := service.Run(context.Background(), 100); err != nil || report.Archived != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	got, ok, err := store.Get(context.Background(), "tenant-a", "run-revision", 2)
	if err != nil || !ok || got.HotDeletedAt.IsZero() || got.Revision != 3 {
		t.Fatalf("record=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestReconcileMarksStaleRunningTraceAborted(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repo := &memoryTraceRepository{records: []TraceRecord{{
		TenantID: "tenant-1", RunID: "run-stale", TraceVersion: 2, TraceRef: "hot://run-stale",
		Status: TraceRunning, StartedAt: now.Add(-20 * time.Minute), LatestEventAt: now.Add(-11 * time.Minute), Revision: 1,
	}}}
	objects := &memoryTraceObjects{data: map[string][]byte{"hot://run-stale": []byte(`{"event_refs":["event-1","event-2"]}`)}}
	reconciler := NewReconciler(repo, objects, staticRunActivity(false), func() time.Time { return now })
	report, err := reconciler.Run(context.Background(), 100)
	if err != nil || report.Aborted != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if repo.records[0].TerminalStatus != "aborted" || repo.records[0].Status != TraceHot {
		t.Fatalf("record=%+v", repo.records[0])
	}
}

type retentionFixture struct {
	now     time.Time
	repo    *memoryTraceRepository
	objects *memoryTraceObjects
	service *RetentionService
	next    int
}

func newRetentionFixture() *retentionFixture {
	fixture := &retentionFixture{
		now:     time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		repo:    &memoryTraceRepository{},
		objects: &memoryTraceObjects{data: map[string][]byte{}},
	}
	fixture.service = NewRetentionService(fixture.repo, fixture.objects, func() time.Time { return fixture.now })
	return fixture
}

func (f *retentionFixture) seedHot(status string, completedAt time.Time) TraceRecord {
	f.next++
	runID := fmt.Sprintf("run-%03d", f.next)
	ref := "hot://" + runID
	f.objects.data[ref] = []byte(`{"trace_version":2}`)
	record := TraceRecord{TenantID: "tenant-1", RunID: runID, TraceVersion: 2, TraceRef: ref, Status: TraceHot, TerminalStatus: status, CompletedAt: completedAt, RetainUntil: completedAt.Add(hotRetentionForStatus(status)), Revision: 1}
	f.repo.records = append(f.repo.records, record)
	return record
}

func (f *retentionFixture) seedArchive(purgeAfter time.Time, held bool) TraceRecord {
	f.next++
	runID := fmt.Sprintf("archive-%03d", f.next)
	ref := "archive://" + runID
	f.objects.data[ref] = []byte("archive")
	record := TraceRecord{TenantID: "tenant-1", RunID: runID, TraceVersion: 2, TraceRef: "hot://" + runID, Status: TraceArchived, TerminalStatus: "completed", ArchiveRef: ref, HotDeletedAt: purgeAfter.Add(-time.Hour), PurgeAfter: purgeAfter, Revision: 1}
	if held {
		record.LegalHoldActor = "operator"
		record.LegalHoldAt = f.now
	}
	f.repo.records = append(f.repo.records, record)
	return record
}

type memoryTraceRepository struct{ records []TraceRecord }

func (r *memoryTraceRepository) ListInterruptedCandidates(_ context.Context, before time.Time, limit int) ([]TraceRecord, error) {
	out := make([]TraceRecord, 0, limit)
	for _, record := range r.records {
		if record.Status == TraceRunning && !record.LatestEventAt.After(before) && len(out) < limit {
			out = append(out, record)
		}
	}
	return out, nil
}

func (r *memoryTraceRepository) ListRetentionCandidates(_ context.Context, now time.Time, limit int) ([]TraceRecord, error) {
	out := make([]TraceRecord, 0, limit)
	for _, record := range r.records {
		due := record.Status == TraceHot && !record.RetainUntil.After(now) || record.Status == TraceArchived && record.LegalHoldAt.IsZero() && !record.PurgeAfter.After(now)
		if due && len(out) < limit {
			out = append(out, record)
		}
	}
	return out, nil
}

func (r *memoryTraceRepository) Save(_ context.Context, record TraceRecord) error {
	for index := range r.records {
		if r.records[index].TenantID == record.TenantID && r.records[index].RunID == record.RunID && r.records[index].TraceVersion == record.TraceVersion {
			record.Revision++
			r.records[index] = record
			return nil
		}
	}
	return ErrTraceNotFound
}

type memoryTraceObjects struct {
	data           map[string][]byte
	failReadback   bool
	hotDeleteCalls int
}

func (s *memoryTraceObjects) ReadTrace(_ context.Context, ref string) ([]ArchiveEntry, error) {
	data, ok := s.data[ref]
	if !ok {
		return nil, errors.New("trace missing")
	}
	return []ArchiveEntry{{Kind: "manifest", Ref: ref, Data: append([]byte(nil), data...)}}, nil
}

func (s *memoryTraceObjects) WriteArchive(_ context.Context, record TraceRecord, data []byte) (string, error) {
	ref := "archive://" + record.RunID
	s.data[ref] = append([]byte(nil), data...)
	return ref, nil
}

func (s *memoryTraceObjects) ReadObject(_ context.Context, ref string) ([]byte, error) {
	data := append([]byte(nil), s.data[ref]...)
	if s.failReadback && len(data) > 0 {
		data[len(data)-1] ^= 0xff
	}
	return data, nil
}

func (s *memoryTraceObjects) DeleteHot(_ context.Context, ref string) error {
	s.hotDeleteCalls++
	delete(s.data, ref)
	return nil
}

func (s *memoryTraceObjects) DeleteArchive(_ context.Context, ref string) error {
	delete(s.data, ref)
	return nil
}

func (s *memoryTraceObjects) AbortTrace(_ context.Context, record TraceRecord, at time.Time) error {
	s.data[record.TraceRef] = []byte(fmt.Sprintf(`{"status":"aborted","error_code":"host_process_interrupted","completed_at":%q,"event_refs":["event-1","event-2"]}`, at.UTC().Format(time.RFC3339Nano)))
	return nil
}

func (s *memoryTraceObjects) exists(ref string) bool { _, ok := s.data[ref]; return ok }

type staticRunActivity bool

func (active staticRunActivity) RunActive(context.Context, string, string) (bool, error) {
	return bool(active), nil
}
