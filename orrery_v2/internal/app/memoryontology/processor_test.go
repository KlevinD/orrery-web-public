package memoryontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type fixedMemoryReader struct {
	records []memcore.MemoryRecord
}

func (r fixedMemoryReader) FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error) {
	return append([]memcore.MemoryRecord(nil), r.records...), nil
}

type recordingExtractor struct {
	extraction Extraction
	calls      int
}

func (e *recordingExtractor) Extract(context.Context, memcore.MemoryRecord) (Extraction, error) {
	e.calls++
	return e.extraction, nil
}

func (e *recordingExtractor) ModelID() string { return "gpt-5.6-luna:max" }

type recordingMemoryRecordProjector struct {
	graph *contextgraph.MemoryStore
	calls int
}

type fixedResolutionScopes map[string][]string

func (s fixedResolutionScopes) ResolutionTenantIDs(_ context.Context, record memcore.MemoryRecord) ([]string, error) {
	return append([]string(nil), s[record.ID]...), nil
}

func (p *recordingMemoryRecordProjector) PublishMaterializedMemoryRecord(ctx context.Context, record memcore.MemoryRecord) error {
	p.calls++
	tenantID := TenantIDForRecord(record)
	return p.graph.SaveObject(ctx, contextgraph.Object{
		ID: record.ID, TenantID: tenantID, ObjectType: "memory_record", Label: record.Summary,
		OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: tenantID, TenantID: tenantID},
	})
}

type cancelingExtractor struct {
	cancel context.CancelFunc
}

func (e cancelingExtractor) Extract(context.Context, memcore.MemoryRecord) (Extraction, error) {
	e.cancel()
	return Extraction{}, context.DeadlineExceeded
}

func (cancelingExtractor) ModelID() string { return "gpt-5.6-luna:max" }

type recordingRunStore struct {
	saved          Run
	saves          []Run
	saveContextErr error
}

type countingMemoryOntologyGraphStore struct {
	*contextgraph.MemoryStore
	snapshotCalls int
}

func (s *countingMemoryOntologyGraphStore) Snapshot(ctx context.Context, tenantID string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error) {
	s.snapshotCalls++
	return s.MemoryStore.Snapshot(ctx, tenantID)
}

func (s *recordingRunStore) GetRun(_ context.Context, key RunKey) (Run, bool, error) {
	return s.saved, s.saved.RunKey == key, nil
}

func (s *recordingRunStore) ClaimRun(_ context.Context, key RunKey, modelID string, now time.Time) (Run, bool, error) {
	return Run{
		RunKey: key, Status: RunRunning, Attempts: 1,
		ModelID: modelID, StartedAt: now, UpdatedAt: now,
	}, true, nil
}

func (s *recordingRunStore) SaveRun(ctx context.Context, run Run) error {
	s.saved = run
	s.saves = append(s.saves, run)
	s.saveContextErr = ctx.Err()
	return s.saveContextErr
}

func TestProcessorResetsExhaustedSourceProjectionFailureWhenObjectAlreadyExists(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	record := memcore.MemoryRecord{
		ID: "memory-existing", Scope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "tenant-1"},
		Summary: "Existing source", UpdatedAt: now,
	}
	graph := contextgraph.NewMemoryStore()
	if err := graph.SaveObject(ctx, contextgraph.Object{
		ID: record.ID, TenantID: "tenant-1", ObjectType: "memory_record", Label: record.Summary,
		OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
	}); err != nil {
		t.Fatalf("SaveObject: %v", err)
	}
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	runs := &recordingRunStore{saved: Run{
		RunKey: RunKey{TenantID: "tenant-1", MemoryID: record.ID, ContentHash: ContentHash(record), ExtractorVersion: ExtractorVersion + ":" + EntityDictionaryVersion(nil)}, Status: RunFailed, Attempts: MaximumAttempts,
		LastError: "memory ontology source object memory-existing is not projected", UpdatedAt: now,
	}}
	extractor := &recordingExtractor{}
	processor := NewProcessor(fixedMemoryReader{}, runs, extractor, NewGraphApplier(graph, registry, func() time.Time { return now }), func() time.Time { return now })

	processed, _, err := processor.processRecord(ctx, record)
	if err != nil || !processed || extractor.calls != 1 {
		t.Fatalf("processRecord = processed %t, calls %d, err %v", processed, extractor.calls, err)
	}
	foundReset := false
	for _, saved := range runs.saves {
		if saved.Status == RunPending && saved.Attempts == 0 && saved.LastError == "" {
			foundReset = true
		}
	}
	if !foundReset {
		t.Fatalf("saved runs = %+v", runs.saves)
	}
}

func (*recordingRunStore) ReplaceMentions(context.Context, RunKey, []MentionProjection, time.Time) error {
	return nil
}

func TestProcessorSweepReusesTenantGraphSnapshot(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	records := []memcore.MemoryRecord{
		{ID: "memory-1", Scope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "tenant-1"}, Summary: "First memory", UpdatedAt: now},
		{ID: "memory-2", Scope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "tenant-1"}, Summary: "Second memory", UpdatedAt: now},
	}
	graph := &countingMemoryOntologyGraphStore{MemoryStore: contextgraph.NewMemoryStore()}
	for _, record := range records {
		if err := graph.SaveObject(ctx, contextgraph.Object{
			ID: record.ID, TenantID: "tenant-1", ObjectType: "memory_record", Label: record.Summary,
			OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	processor := NewProcessor(
		fixedMemoryReader{records: records}, &recordingRunStore{}, &recordingExtractor{},
		NewGraphApplier(graph, registry, func() time.Time { return now }),
		func() time.Time { return now },
	)

	report, err := processor.Sweep(ctx, 2)
	if err != nil || report.Succeeded != 2 {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	if graph.snapshotCalls != 1 {
		t.Fatalf("snapshot calls = %d, want 1", graph.snapshotCalls)
	}
}

func TestProcessorResolvesAuthorizedParentTenantObjectAndMaterializesReference(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC)
	record := memcore.MemoryRecord{
		ID: "memory-agent-1", Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "A000001"},
		Summary: "丁皮 owns Minvia.", UpdatedAt: now,
	}
	graph := contextgraph.NewMemoryStore()
	memoryAccess := contextgraph.AccessRef{Kind: "agent", ID: "A000001", TenantID: "minvia"}
	entityAccess := contextgraph.AccessRef{Kind: "organization", ID: "minvia", TenantID: "minvia"}
	if err := graph.SaveObject(ctx, contextgraph.Object{
		ID: record.ID, TenantID: "agent:A000001", ObjectType: "memory_record", Label: record.Summary, OwnerAccessRef: memoryAccess,
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.SaveObject(ctx, contextgraph.Object{
		ID: "person:H000001", TenantID: "minvia", ObjectType: "person", CanonicalKey: "person:H000001",
		Label: "丁皮", OwnerAccessRef: entityAccess,
	}); err != nil {
		t.Fatal(err)
	}
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	runs := &recordingRunStore{}
	processor := NewProcessor(
		fixedMemoryReader{records: []memcore.MemoryRecord{record}}, runs,
		&recordingExtractor{extraction: Extraction{Mentions: []Mention{{
			Surface: "丁皮", ObjectType: "person", ObjectHint: "丁皮", Confidence: 0.99, Anchor: "summary",
		}}}}, NewGraphApplier(graph, registry, func() time.Time { return now }), func() time.Time { return now },
	).WithResolutionScopes(fixedResolutionScopes{record.ID: {"minvia"}})

	report, err := processor.Sweep(ctx, 1)
	if err != nil || report.Succeeded != 1 || report.Linked != 1 {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	objects, links, _, _, err := graph.Snapshot(ctx, "agent:A000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 2 || len(links) != 1 || links[0].ToObjectID != "person:H000001" {
		t.Fatalf("objects = %+v, links = %+v", objects, links)
	}
	var reference contextgraph.Object
	for _, object := range objects {
		if object.ID == "person:H000001" {
			reference = object
		}
	}
	if reference.OwnerAccessRef != entityAccess || reference.Properties["_orrery_reference_source_tenant"] != "minvia" {
		t.Fatalf("reference = %+v", reference)
	}
}

func TestProcessorDoesNotResolveObjectOutsideAuthorizedScopes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC)
	record := memcore.MemoryRecord{ID: "memory-agent-2", Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "A000002"}, UpdatedAt: now}
	graph := contextgraph.NewMemoryStore()
	if err := graph.SaveObject(ctx, contextgraph.Object{ID: record.ID, TenantID: "agent:A000002", ObjectType: "memory_record"}); err != nil {
		t.Fatal(err)
	}
	if err := graph.SaveObject(ctx, contextgraph.Object{ID: "person:secret", TenantID: "other-company", ObjectType: "person", Label: "Secret Person"}); err != nil {
		t.Fatal(err)
	}
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	processor := NewProcessor(
		fixedMemoryReader{records: []memcore.MemoryRecord{record}}, &recordingRunStore{},
		&recordingExtractor{extraction: Extraction{Mentions: []Mention{{Surface: "Secret Person", ObjectType: "person", Confidence: 0.99}}}},
		NewGraphApplier(graph, registry, func() time.Time { return now }), func() time.Time { return now },
	).WithResolutionScopes(fixedResolutionScopes{record.ID: {"minvia"}})

	report, err := processor.Sweep(ctx, 1)
	if err != nil || report.Succeeded != 1 || report.Linked != 0 {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	objects, links, _, _, _ := graph.Snapshot(ctx, "agent:A000002")
	if len(objects) != 1 || len(links) != 0 {
		t.Fatalf("objects = %+v, links = %+v", objects, links)
	}
}

func TestProcessorPersistsFailureAfterExtractionContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runs := &recordingRunStore{}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	processor := NewProcessor(
		fixedMemoryReader{}, runs, cancelingExtractor{cancel: cancel},
		&GraphApplier{graph: contextgraph.NewMemoryStore()}, func() time.Time { return now },
	)

	processed, linked, err := processor.processRecord(ctx, memcore.MemoryRecord{ID: "memory-canceled"})
	if !processed || linked != 0 || err == nil {
		t.Fatalf("processRecord = processed %t, linked %d, err %v", processed, linked, err)
	}
	if runs.saveContextErr != nil {
		t.Fatalf("SaveRun context error = %v", runs.saveContextErr)
	}
	if runs.saved.Status != RunFailed || runs.saved.LastError != context.DeadlineExceeded.Error() {
		t.Fatalf("saved run = %+v", runs.saved)
	}
	if want := now.Add(time.Minute); !runs.saved.NextRetryAt.Equal(want) {
		t.Fatalf("next retry = %s, want %s", runs.saved.NextRetryAt, want)
	}
}

func TestProcessorSweepsMemoryIntoOntologyGraphOnce(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-ontology-processor"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("install ontology: %v", err)
	}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	record := memcore.MemoryRecord{
		ID: "memory-1", ProfileID: "profile-1",
		Scope:   memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "tenant-1"},
		Summary: "Orrery is ready for release.", UpdatedAt: now,
	}
	graph := contextgraph.NewMemoryStore()
	access := contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}
	for _, object := range []contextgraph.Object{
		{ID: "project-1", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery", OwnerAccessRef: access},
	} {
		if err := graph.SaveObject(ctx, object); err != nil {
			t.Fatalf("save object: %v", err)
		}
	}
	extractor := &recordingExtractor{extraction: Extraction{Mentions: []Mention{{
		Surface: "Orrery", ObjectType: "project", ObjectHint: "Orrery", Confidence: 0.99, Anchor: "summary",
	}}}}
	projector := &recordingMemoryRecordProjector{graph: graph}
	runStore := NewPlatformStore(database)
	objectsBeforeRepair, _, _, _, err := graph.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("snapshot before repair: %v", err)
	}
	key := RunKey{
		TenantID: "tenant-1", MemoryID: record.ID, ContentHash: ContentHash(record),
		ExtractorVersion: ExtractorVersion + ":" + EntityDictionaryVersion(objectsBeforeRepair),
	}
	if err := runStore.SaveRun(ctx, Run{
		RunKey: key, Status: RunFailed, Attempts: MaximumAttempts,
		LastError: "memory ontology source object memory-1 is not projected", UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save exhausted source projection run: %v", err)
	}
	processor := NewProcessor(
		fixedMemoryReader{records: []memcore.MemoryRecord{record}},
		runStore, extractor,
		NewGraphApplier(graph, registry, func() time.Time { return now }),
		func() time.Time { return now }, projector,
	)

	report, err := processor.Sweep(ctx, 10)
	if err != nil || report.Succeeded != 1 || report.Linked != 1 || extractor.calls != 1 || projector.calls != 1 {
		t.Fatalf("first sweep report=%+v calls=%d err=%v", report, extractor.calls, err)
	}
	report, err = processor.Sweep(ctx, 10)
	if err != nil || report.Processed != 0 || report.Skipped != 1 || extractor.calls != 1 {
		t.Fatalf("second sweep report=%+v calls=%d err=%v", report, extractor.calls, err)
	}
	if err := graph.SaveObject(ctx, contextgraph.Object{
		ID: "tool-1", TenantID: "tenant-1", ObjectType: "tool", Label: "PageIndex",
		Properties: map[string]any{"aliases": []any{"PI"}}, OwnerAccessRef: access,
	}); err != nil {
		t.Fatalf("save ontology dictionary change: %v", err)
	}
	report, err = processor.ReprojectExisting(ctx, 10)
	if err != nil || report.Processed != 1 || report.Succeeded != 1 || extractor.calls != 1 {
		t.Fatalf("dictionary-change reprojection report=%+v calls=%d err=%v", report, extractor.calls, err)
	}
	_, links, _, evidence, err := graph.Snapshot(ctx, "tenant-1")
	if err != nil || len(links) != 1 || len(evidence) != 1 || links[0].LinkType != "memory_mentions_project" {
		t.Fatalf("graph links=%+v evidence=%+v err=%v", links, evidence, err)
	}
}
