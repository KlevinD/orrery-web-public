package memorydiary

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestAppendServiceAppendsToOneCanonicalTenantAgentDiary(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	service := NewDiaryService(DiaryServiceOptions{
		Repository: store,
		Now:        func() time.Time { return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC) },
	})

	first, err := service.Append(ctx, AppendRequest{
		TenantID:        "tenant-a",
		AgentID:         "agent-1",
		Date:            "2026-07-17",
		Path:            "memory/2026-07-17.md",
		Content:         "## Morning\n- Finished the search index.",
		SessionID:       "session-1",
		ContextRevision: "revision-1",
		Trigger:         TriggerRunTerminal,
		IdempotencyKey:  "append-1",
		Sensitivity:     memcore.SensitivityPersonal,
	})
	if err != nil {
		t.Fatalf("Append(first): %v", err)
	}
	second, err := service.Append(ctx, AppendRequest{
		TenantID:        "tenant-a",
		AgentID:         "agent-1",
		Date:            "2026-07-17",
		Path:            "memory/2026-07-17.md",
		Content:         "## Afternoon\n- Fixed the review state.",
		SessionID:       "session-1",
		ContextRevision: "revision-2",
		Trigger:         TriggerRunTerminal,
		IdempotencyKey:  "append-2",
	})
	if err != nil {
		t.Fatalf("Append(second): %v", err)
	}
	if first.RecordID != second.RecordID || first.Path != "memory/2026-07-17.md" {
		t.Fatalf("receipts = first:%+v second:%+v", first, second)
	}
	if !strings.Contains(second.Content, "Finished the search index.") || !strings.Contains(second.Content, "Fixed the review state.") {
		t.Fatalf("appended content = %q", second.Content)
	}
	if strings.Index(second.Content, "Finished") > strings.Index(second.Content, "Fixed") {
		t.Fatalf("append order changed: %q", second.Content)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{
		ProfileIDs: []agentcore.MemoryProfileID{"memory-agent-1"},
		Topics:     []string{"memory/2026-07-17.md"},
	})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 1 || records[0].Sensitivity != memcore.SensitivityPersonal {
		t.Fatalf("records = %+v", records)
	}
}

func TestAppendServiceCarriesServerClassifiedSensitivity(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	service := NewDiaryService(DiaryServiceOptions{
		Repository: store,
		ClassifySensitivity: func(string) memcore.Sensitivity {
			return memcore.SensitivityCompensation
		},
	})

	_, err := service.Append(ctx, AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- Recorded the approved salary adjustment.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerRunTerminal, IdempotencyKey: "classified-append",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{
		ProfileIDs: []agentcore.MemoryProfileID{"memory-agent-1"},
		Topics:     []string{"memory/2026-07-17.md"},
	})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 1 || records[0].Sensitivity != memcore.SensitivityCompensation {
		t.Fatalf("records = %+v", records)
	}
}

func TestDiaryServiceAppendPublishesAuditMetadataAndSourceProvenance(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	audit := &recordingDiaryAudit{}
	metadata := &recordingDiaryMetadata{}
	service := NewDiaryService(DiaryServiceOptions{
		Repository: store,
		Audit:      audit,
		Metadata:   metadata,
		Now:        func() time.Time { return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC) },
	})

	receipt, err := service.Append(ctx, AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- Published a durable redacted outcome.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerRunTerminal, IdempotencyKey: "provenance-append", MissionID: "mission-1", TaskID: "task-1", RunID: "run-1",
		EvidenceRefs: []string{"evidence:test-log-1"}, LineageRefs: []string{"memory:digest-1"},
		SourceSensitivity: memcore.SensitivitySecret,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	record, err := store.GetRecord(ctx, receipt.RecordID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if record.Sensitivity != memcore.SensitivitySecret || !containsString(record.Tags, "mission:mission-1") ||
		!containsString(record.Tags, "task:task-1") || !containsString(record.Tags, "run:run-1") ||
		!containsString(record.SourceEvidenceRefs, "evidence:test-log-1") || !containsString(record.LineageRefs, "memory:digest-1") {
		t.Fatalf("record provenance = %+v", record)
	}
	candidate, err := store.GetCandidate(ctx, receipt.CandidateID)
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if candidate.SourceType != memcore.SourceAgentRunDiary {
		t.Fatalf("candidate source type = %q", candidate.SourceType)
	}
	if len(audit.events) != 2 || audit.events[0].Type != memaudit.EventMemoryCandidateCreated || audit.events[1].Type != memaudit.EventMemoryRecordCreated {
		t.Fatalf("audit events = %+v", audit.events)
	}
	if len(metadata.events) != 1 || metadata.events[0].TenantID != "tenant-a" || metadata.events[0].StreamID != receipt.RecordID ||
		metadata.events[0].Type != "memory.record.accepted" || !strings.Contains(metadata.events[0].ID, receipt.CandidateID) {
		t.Fatalf("metadata events = %+v", metadata.events)
	}
}

func TestDiaryServiceAppendReplayKeepsRecordCreatedAuditType(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	audit := &failFirstRecordCreatedAudit{fail: true}
	service := NewDiaryService(DiaryServiceOptions{Repository: store, Audit: audit})
	req := AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- Shipped the durable diary path.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerRunTerminal, IdempotencyKey: "append-1", MissionID: "mission-1",
	}
	if _, err := service.Append(ctx, req); err == nil {
		t.Fatal("first Append error = nil, want injected record-created audit failure")
	}
	if _, err := service.Append(ctx, req); err != nil {
		t.Fatalf("replayed Append: %v", err)
	}
	var recordEvents []memaudit.Event
	for _, event := range audit.events {
		if event.Type == memaudit.EventMemoryRecordCreated || event.Type == memaudit.EventMemoryRecordUpdated {
			recordEvents = append(recordEvents, event)
		}
	}
	if len(recordEvents) != 1 || recordEvents[0].Type != memaudit.EventMemoryRecordCreated {
		t.Fatalf("record audit events = %+v, want one stable record-created event", recordEvents)
	}
}

func TestAppendServiceReplaysIdempotencyWithoutDuplicateContent(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	service := NewDiaryService(DiaryServiceOptions{Repository: store})
	req := AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- Durable outcome.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerPreCompaction, IdempotencyKey: "same-append",
	}
	first, err := service.Append(ctx, req)
	if err != nil {
		t.Fatalf("Append(first): %v", err)
	}
	second, err := service.Append(ctx, req)
	if err != nil {
		t.Fatalf("Append(replay): %v", err)
	}
	if !second.Replayed || first.CandidateID != second.CandidateID {
		t.Fatalf("replay receipts = first:%+v second:%+v", first, second)
	}
	if strings.Count(second.Content, "Durable outcome") != 1 {
		t.Fatalf("duplicate append content = %q", second.Content)
	}
}

func TestDiaryServiceAppendRecoversAfterRecordPersistenceFailure(t *testing.T) {
	ctx := context.Background()
	store := &failFirstRecordStore{FakeStore: memtestkit.NewFakeStore(), fail: true}
	seedTenantMemoryProfile(t, ctx, store.FakeStore, "tenant-a", "agent-1")
	service := NewDiaryService(DiaryServiceOptions{Repository: store})
	req := AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- Durable after retry.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerRunTerminal, IdempotencyKey: "recoverable-append",
	}

	if _, err := service.Append(ctx, req); err == nil {
		t.Fatal("Append(first) error = nil, want injected record persistence failure")
	}
	receipt, err := service.Append(ctx, req)
	if err != nil {
		t.Fatalf("Append(retry): %v", err)
	}
	if receipt.RecordID == "" || strings.Count(receipt.Content, "Durable after retry") != 1 {
		t.Fatalf("retry receipt = %+v", receipt)
	}
}

func TestDiaryServiceAppendRecoversExistingRecordAfterNonTransactionalRecordFailure(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	baseService := NewDiaryService(DiaryServiceOptions{Repository: store})
	base := AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- First entry.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerRunTerminal, IdempotencyKey: "append-1",
	}
	if _, err := baseService.Append(ctx, base); err != nil {
		t.Fatalf("seed Append: %v", err)
	}

	failing := &nonTransactionalFailRecordStore{Repository: store, fail: true}
	service := NewDiaryService(DiaryServiceOptions{Repository: failing})
	second := base
	second.Content = "- Second entry."
	second.ContextRevision = "revision-2"
	second.IdempotencyKey = "append-2"
	if _, err := service.Append(ctx, second); err == nil {
		t.Fatal("Append(first attempt) error = nil, want injected record persistence failure")
	}
	receipt, err := service.Append(ctx, second)
	if err != nil {
		t.Fatalf("Append(recovery): %v", err)
	}
	if !receipt.Replayed || !strings.Contains(receipt.Content, "First entry.") || !strings.Contains(receipt.Content, "Second entry.") {
		t.Fatalf("recovered receipt = %+v", receipt)
	}
	record, err := store.GetRecord(ctx, receipt.RecordID)
	if err != nil || !strings.Contains(record.Content, "Second entry.") {
		t.Fatalf("recovered record = %+v err=%v", record, err)
	}
}

func TestDiaryServiceAppendConcurrentWritesRetainBothEntries(t *testing.T) {
	ctx := context.Background()
	store := newCoordinatedAppendStore()
	seedTenantMemoryProfile(t, ctx, store.FakeStore, "tenant-a", "agent-1")
	service := NewDiaryService(DiaryServiceOptions{Repository: store})

	results := make(chan AppendReceipt, 2)
	errs := make(chan error, 2)
	for _, item := range []struct {
		session string
		content string
	}{
		{session: "session-a", content: "- Concurrent entry A."},
		{session: "session-b", content: "- Concurrent entry B."},
	} {
		item := item
		go func() {
			receipt, err := service.Append(ctx, AppendRequest{
				TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
				Content: item.content, SessionID: item.session, ContextRevision: "revision-1",
				Trigger: TriggerRunTerminal, IdempotencyKey: "append-" + item.session,
			})
			results <- receipt
			errs <- err
		}()
	}

	<-store.candidateReady
	<-store.candidateReady
	close(store.releaseCandidates)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Append: %v", err)
		}
		<-results
	}
	read, err := service.Read(ctx, ReadRequest{TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(read.Content, "Concurrent entry A") || !strings.Contains(read.Content, "Concurrent entry B") {
		t.Fatalf("concurrent content = %q", read.Content)
	}
}

func TestAppendServiceRejectsPathTenantAgentAndNoReplyOverrides(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	seedTenantMemoryProfile(t, ctx, store, "tenant-b", "agent-2")
	service := NewDiaryService(DiaryServiceOptions{Repository: store})
	base := AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- Safe entry.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerRunTerminal, IdempotencyKey: "append",
	}
	tests := []struct {
		name string
		edit func(*AppendRequest)
	}{
		{name: "different path", edit: func(req *AppendRequest) { req.Path = "memory/2026-07-17-1200.md" }},
		{name: "parent path", edit: func(req *AppendRequest) { req.Path = "../MEMORY.md" }},
		{name: "tenant mismatch", edit: func(req *AppendRequest) { req.TenantID = "tenant-b" }},
		{name: "cross agent", edit: func(req *AppendRequest) { req.AgentID = "agent-2" }},
		{name: "no reply", edit: func(req *AppendRequest) { req.Content = "NO_REPLY" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := base
			test.edit(&req)
			_, err := service.Append(ctx, req)
			if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Append error = %v, want forbidden or invalid", err)
			}
		})
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("denied appends mutated records: %+v", records)
	}
}

func TestReadServiceReturnsOnlyOwnedCanonicalDiary(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	service := NewDiaryService(DiaryServiceOptions{Repository: store})
	_, err := service.Append(ctx, AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- Shipped canonical diary.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerRunTerminal, IdempotencyKey: "append-1",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := service.Read(ctx, ReadRequest{TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.Exists || got.Path != "memory/2026-07-17.md" || !strings.Contains(got.Content, "Shipped canonical diary") {
		t.Fatalf("Read result = %+v", got)
	}
	if _, err := service.Read(ctx, ReadRequest{TenantID: "tenant-b", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant Read error = %v, want ErrForbidden", err)
	}
}

func TestAppendServiceAdvancesSharedFlushCursor(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	state := NewMemoryFlushStateStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	service := NewDiaryService(DiaryServiceOptions{Repository: store, State: state})
	_, err := service.Append(ctx, AppendRequest{
		TenantID: "tenant-a", AgentID: "agent-1", Date: "2026-07-17", Path: "memory/2026-07-17.md",
		Content: "- Flushed before compaction.", SessionID: "session-1", ContextRevision: "revision-1",
		Trigger: TriggerPreCompaction, IdempotencyKey: "append-1",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	cursor, ok, err := state.LoadFlushState(ctx, "tenant-a", "agent-1", "session-1")
	if err != nil || !ok {
		t.Fatalf("LoadFlushState = %+v, %t, %v", cursor, ok, err)
	}
	if cursor.LastSuccessfulRevision != "revision-1" || cursor.LastSuccessfulDate != "2026-07-17" || cursor.AttemptTrigger != TriggerPreCompaction {
		t.Fatalf("cursor = %+v", cursor)
	}
}

type failFirstRecordStore struct {
	*memtestkit.FakeStore
	mu   sync.Mutex
	fail bool
}

type nonTransactionalFailRecordStore struct {
	Repository
	fail bool
}

func (s *nonTransactionalFailRecordStore) SaveRecord(ctx context.Context, record memcore.MemoryRecord) error {
	if s.fail {
		s.fail = false
		return errors.New("injected record persistence failure")
	}
	return s.Repository.SaveRecord(ctx, record)
}

func (s *failFirstRecordStore) SaveRecord(ctx context.Context, record memcore.MemoryRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		s.fail = false
		return errors.New("injected record persistence failure")
	}
	return s.FakeStore.SaveRecord(ctx, record)
}

func (s *failFirstRecordStore) WithRecordWriteTx(ctx context.Context, recordKey string, fn func(memstore.RecordWriteTx) error) error {
	return s.FakeStore.WithRecordWriteTx(ctx, recordKey, func(tx memstore.RecordWriteTx) error {
		return fn(failFirstRecordTx{RecordWriteTx: tx, store: s})
	})
}

type failFirstRecordTx struct {
	memstore.RecordWriteTx
	store *failFirstRecordStore
}

func (t failFirstRecordTx) SaveRecord(ctx context.Context, record memcore.MemoryRecord) error {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	if t.store.fail {
		t.store.fail = false
		return errors.New("injected record persistence failure")
	}
	return t.RecordWriteTx.SaveRecord(ctx, record)
}

type coordinatedAppendStore struct {
	*memtestkit.FakeStore
	candidateReady    chan struct{}
	releaseCandidates chan struct{}
}

type recordingDiaryAudit struct {
	events []memaudit.Event
}

func (r *recordingDiaryAudit) RecordMemoryAudit(_ context.Context, event memaudit.Event) error {
	r.events = append(r.events, event)
	return nil
}

type failFirstRecordCreatedAudit struct {
	events []memaudit.Event
	fail   bool
}

func (r *failFirstRecordCreatedAudit) RecordMemoryAudit(_ context.Context, event memaudit.Event) error {
	if r.fail && event.Type == memaudit.EventMemoryRecordCreated {
		r.fail = false
		return errors.New("injected record-created audit failure")
	}
	r.events = append(r.events, event)
	return nil
}

func TestDiaryServiceResolvesPersonalMemoryTenantByAgentOwnership(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedTenantMemoryProfile(t, ctx, store, "tenant-a", "agent-1")
	service := NewDiaryService(DiaryServiceOptions{Repository: store})

	tenantID, err := service.ResolvePersonalMemoryTenant(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if tenantID != "tenant-a" {
		t.Fatalf("tenant id = %q, want tenant-a", tenantID)
	}
}

type recordingDiaryMetadata struct {
	events []graphprotocol.DomainEvent
}

func (r *recordingDiaryMetadata) PublishMemoryRecordMetadata(_ context.Context, event graphprotocol.DomainEvent) error {
	r.events = append(r.events, event)
	return nil
}

func newCoordinatedAppendStore() *coordinatedAppendStore {
	return &coordinatedAppendStore{
		FakeStore:         memtestkit.NewFakeStore(),
		candidateReady:    make(chan struct{}, 2),
		releaseCandidates: make(chan struct{}),
	}
}

func (s *coordinatedAppendStore) SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error {
	if strings.HasPrefix(candidate.ID, "candidate.agent_daily_diary_append.") {
		s.candidateReady <- struct{}{}
		select {
		case <-s.releaseCandidates:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.FakeStore.SaveCandidate(ctx, candidate)
}

func (s *coordinatedAppendStore) WithRecordWriteTx(ctx context.Context, recordKey string, fn func(memstore.RecordWriteTx) error) error {
	s.candidateReady <- struct{}{}
	select {
	case <-s.releaseCandidates:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.FakeStore.WithRecordWriteTx(ctx, recordKey, fn)
}

func seedTenantMemoryProfile(t *testing.T, ctx context.Context, store *memtestkit.FakeStore, tenantID, agentID string) {
	t.Helper()
	if err := store.SaveProfile(ctx, memcore.MemoryProfile{
		ID:         agentcore.MemoryProfileID("memory-" + agentID),
		OwnerRef:   memcore.OwnerRef{AgentID: agentcore.AgentID(agentID), OrganizationID: tenantID},
		MemoryKind: agentcore.MemoryKindPersonal,
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeAgent,
			ID:   agentID,
		},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
}
