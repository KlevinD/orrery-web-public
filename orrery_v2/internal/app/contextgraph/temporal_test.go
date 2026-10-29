package contextgraph

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

func TestTemporalHistoryPolicy(t *testing.T) {
	for _, test := range []struct {
		mode string
		want bool
	}{
		{mode: "", want: false},
		{mode: "static", want: false},
		{mode: "STATIC", want: false},
		{mode: "state_interval", want: true},
		{mode: "event", want: true},
		{mode: "measurement", want: true},
	} {
		t.Run(test.mode, func(t *testing.T) {
			if got := keepsTemporalHistory(TemporalMetadata{Mode: test.mode}); got != test.want {
				t.Fatalf("keepsTemporalHistory(%q) = %v, want %v", test.mode, got, test.want)
			}
		})
	}
}

func TestTemporalResourceVersionIdentityIgnoresIngestionTimeForStableSourceVersion(t *testing.T) {
	resource := temporalVersionTestObject(
		"Original",
		7,
		temporalTestTime(2026, time.January, 1),
		nil,
		temporalTestTime(2026, time.January, 2),
	)
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 3)
	accessRef := AccessRef{Kind: "mission", ID: "mission-1", TenantID: "tenant-1"}

	firstID, firstPayload, err := temporalResourceVersionIdentity(objectTemporalVersion(resource), accessRef, AccessStateResolved)
	if err != nil {
		t.Fatalf("first identity: %v", err)
	}
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 4)
	secondID, secondPayload, err := temporalResourceVersionIdentity(objectTemporalVersion(resource), accessRef, AccessStateResolved)
	if err != nil {
		t.Fatalf("second identity: %v", err)
	}
	if firstID != secondID {
		t.Fatalf("stable source version identity changed with ingestion time: %q != %q", firstID, secondID)
	}
	if firstPayload == secondPayload {
		t.Fatal("stored payload did not retain the actual ingestion time")
	}

	changed := resource
	changed.Label = "Corrected"
	changedID, _, err := temporalResourceVersionIdentity(objectTemporalVersion(changed), accessRef, AccessStateResolved)
	if err != nil {
		t.Fatalf("changed payload identity: %v", err)
	}
	if changedID == secondID {
		t.Fatal("semantic payload change reused the previous identity")
	}

	changed = resource
	changed.SourceVersion++
	changedID, _, err = temporalResourceVersionIdentity(objectTemporalVersion(changed), accessRef, AccessStateResolved)
	if err != nil {
		t.Fatalf("changed source version identity: %v", err)
	}
	if changedID == secondID {
		t.Fatal("source version change reused the previous identity")
	}

	changedID, _, err = temporalResourceVersionIdentity(
		objectTemporalVersion(resource),
		AccessRef{Kind: "mission", ID: "mission-2", TenantID: "tenant-1"},
		AccessStateResolved,
	)
	if err != nil {
		t.Fatalf("changed access identity: %v", err)
	}
	if changedID == secondID {
		t.Fatal("access reference change reused the previous identity")
	}
}

func TestTemporalResourceVersionIdentityKeepsIngestionTimeWithoutSourceVersion(t *testing.T) {
	resource := temporalVersionTestObject(
		"Unversioned",
		0,
		temporalTestTime(2026, time.January, 1),
		nil,
		temporalTestTime(2026, time.January, 2),
	)
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 3)
	firstID, _, err := temporalResourceVersionIdentity(objectTemporalVersion(resource), AccessRef{}, AccessStateUnresolved)
	if err != nil {
		t.Fatalf("first identity: %v", err)
	}
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 4)
	secondID, _, err := temporalResourceVersionIdentity(objectTemporalVersion(resource), AccessRef{}, AccessStateUnresolved)
	if err != nil {
		t.Fatalf("second identity: %v", err)
	}
	if firstID == secondID {
		t.Fatal("unversioned temporal resource discarded ingestion time from identity")
	}
}

func TestMemoryStoreKeepsStaticResourcesOutOfHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	updatedAt := temporalTestTime(2026, time.January, 2)
	if err := store.SaveObject(ctx, Object{
		ID: "object-1", TenantID: "tenant-1", ObjectType: "task", UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveObject: %v", err)
	}
	if err := store.SaveLink(ctx, Link{
		ID: "link-1", TenantID: "tenant-1", LinkType: "depends_on", FromObjectID: "object-1", ToObjectID: "object-2", UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveLink: %v", err)
	}
	if err := store.SaveFact(ctx, Fact{
		ID: "fact-1", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "object-1"}, Predicate: "status", Value: "active", UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveFact: %v", err)
	}
	objects, links, facts, _, err := store.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || len(links) != 1 || len(facts) != 1 {
		t.Fatalf("current snapshot objects=%d links=%d facts=%d", len(objects), len(links), len(facts))
	}
	if len(store.history) != 0 {
		t.Fatalf("static history rows = %d, want 0", len(store.history))
	}
}

func TestMemoryStoreKeepsAllNonStaticResourceModesInHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	updatedAt := temporalTestTime(2026, time.January, 2)
	if err := store.SaveObject(ctx, Object{
		ID: "object-1", TenantID: "tenant-1", ObjectType: "task", SourceStreamID: "object-stream", SourceVersion: 1,
		Temporal: TemporalMetadata{Mode: "state_interval", BusinessValidFrom: updatedAt, SourceRecordedAt: updatedAt}, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveObject: %v", err)
	}
	if err := store.SaveLink(ctx, Link{
		ID: "link-1", TenantID: "tenant-1", LinkType: "depends_on", FromObjectID: "object-1", ToObjectID: "object-2",
		SourceStreamID: "link-stream", SourceVersion: 1, Temporal: TemporalMetadata{Mode: "event", SourceRecordedAt: updatedAt}, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveLink: %v", err)
	}
	if err := store.SaveFact(ctx, Fact{
		ID: "fact-1", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "object-1"}, Predicate: "progress", Value: 0.5,
		SourceStreamID: "fact-stream", SourceVersion: 1, Temporal: TemporalMetadata{Mode: "measurement", SourceRecordedAt: updatedAt}, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("SaveFact: %v", err)
	}
	if len(store.history) != 3 {
		t.Fatalf("non-static history rows = %d, want 3", len(store.history))
	}
}

func TestMemoryStoreDeduplicatesStableTemporalReplay(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	resource := temporalVersionTestObject(
		"Original", 1, temporalTestTime(2026, time.January, 1), nil, temporalTestTime(2026, time.January, 2),
	)
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 3)
	if err := store.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject first: %v", err)
	}
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 4)
	if err := store.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject replay: %v", err)
	}
	if len(store.history) != 1 {
		t.Fatalf("replayed history rows = %d, want 1", len(store.history))
	}
	resource.Label = "Corrected"
	if err := store.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject semantic change: %v", err)
	}
	if len(store.history) != 2 {
		t.Fatalf("semantic history rows = %d, want 2", len(store.history))
	}
}

func TestMemoryStoreKeepsFullIdentityForUnversionedTemporalResources(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	resource := temporalVersionTestObject(
		"Unversioned", 0, temporalTestTime(2026, time.January, 1), nil, temporalTestTime(2026, time.January, 2),
	)
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 3)
	if err := store.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject first: %v", err)
	}
	resource.Temporal.OrreryIngestedAt = temporalTestTime(2026, time.January, 4)
	if err := store.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject replay: %v", err)
	}
	if len(store.history) != 2 {
		t.Fatalf("unversioned history rows = %d, want 2", len(store.history))
	}
}

func TestMemoryStoreDoesNotUpdateCurrentStateWhenTemporalIdentityFails(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	resource := temporalVersionTestObject(
		"Original", 1, temporalTestTime(2026, time.January, 1), nil, temporalTestTime(2026, time.January, 2),
	)
	if err := store.SaveObject(ctx, resource); err != nil {
		t.Fatalf("SaveObject first: %v", err)
	}
	invalid := resource
	invalid.Label = "Invalid"
	invalid.SourceVersion = 2
	invalid.Properties = map[string]any{"unsupported": make(chan struct{})}
	if err := store.SaveObject(ctx, invalid); err == nil {
		t.Fatal("SaveObject invalid identity error = nil")
	}
	objects, _, _, _, err := store.Snapshot(ctx, resource.TenantID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].Label != "Original" {
		t.Fatalf("current object changed after rejected temporal identity: %+v", objects)
	}
	if len(store.history) != 1 {
		t.Fatalf("history rows after rejected temporal identity = %d, want 1", len(store.history))
	}
}

func TestMemoryStoreSnapshotAsOfUsesKnowledgeTimeForCorrections(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	firstRecordedAt := validFrom.Add(time.Hour)
	correctedAt := validFrom.Add(48 * time.Hour)
	supersededAt := correctedAt

	first := temporalTestObject("Original", validFrom, firstRecordedAt)
	first.Temporal.SourceSupersededAt = &supersededAt
	if err := store.SaveObject(ctx, first); err != nil {
		t.Fatal(err)
	}
	corrected := temporalTestObject("Corrected", validFrom, correctedAt)
	corrected.SourceVersion = 2
	if err := store.SaveObject(ctx, corrected); err != nil {
		t.Fatal(err)
	}

	beforeCorrection := snapshotTemporalTestObject(t, store, validFrom.Add(24*time.Hour), correctedAt.Add(-time.Second))
	if beforeCorrection.Label != "Original" {
		t.Fatalf("object before correction = %+v", beforeCorrection)
	}
	afterCorrection := snapshotTemporalTestObject(t, store, validFrom.Add(24*time.Hour), correctedAt.Add(time.Second))
	if afterCorrection.Label != "Corrected" {
		t.Fatalf("object after correction = %+v", afterCorrection)
	}
}

func TestMemoryStoreSnapshotAsOfUsesBusinessIntervalsAndFiltersFutureEvidence(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	january := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	february := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	knownAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	first := temporalTestObject("January", january, january.Add(time.Hour))
	first.Temporal.BusinessValidTo = &february
	if err := store.SaveObject(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := temporalTestObject("February", february, january.Add(24*time.Hour))
	second.SourceVersion = 2
	if err := store.SaveObject(ctx, second); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []Evidence{
		{ID: "known", TenantID: "tenant-1", ObservedAt: knownAt.Add(-time.Hour)},
		{ID: "future", TenantID: "tenant-1", ObservedAt: knownAt.Add(time.Hour)},
	} {
		if err := store.SaveEvidence(ctx, evidence); err != nil {
			t.Fatal(err)
		}
	}

	inJanuary := snapshotTemporalTestObject(t, store, january.Add(15*24*time.Hour), knownAt)
	if inJanuary.Label != "January" {
		t.Fatalf("January snapshot = %+v", inJanuary)
	}
	objects, _, _, evidence, err := store.SnapshotAsOf(ctx, "tenant-1", february.Add(24*time.Hour), knownAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 || objects[0].Label != "February" {
		t.Fatalf("February snapshot = %+v", objects)
	}
	if len(evidence) != 1 || evidence[0].ID != "known" {
		t.Fatalf("evidence as known at query time = %+v", evidence)
	}
}

func temporalTestObject(label string, validFrom, recordedAt time.Time) Object {
	return Object{
		ID: "subject-1", TenantID: "tenant-1", ObjectType: "workforce_subject", Label: label,
		SourceStreamID: "people:subject-1", SourceVersion: 1, UpdatedAt: recordedAt,
		Temporal: TemporalMetadata{
			Mode: "state_interval", BusinessValidFrom: validFrom, SourceRecordedAt: recordedAt,
		},
	}
}

func snapshotTemporalTestObject(t *testing.T, store *MemoryStore, businessAsOf, knownAt time.Time) Object {
	t.Helper()
	objects, _, _, _, err := store.SnapshotAsOf(context.Background(), "tenant-1", businessAsOf, knownAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 {
		t.Fatalf("snapshot objects = %+v", objects)
	}
	return objects[0]
}

func TestMemoryStoreSnapshotAsOfDoesNotResurfaceEndedCorrection(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	start := temporalTestTime(2026, time.January, 1)
	originalRecorded := temporalTestTime(2026, time.January, 2)
	correctionRecorded := temporalTestTime(2026, time.February, 2)
	futureStart := temporalTestTime(2026, time.April, 1)
	futureRecorded := temporalTestTime(2026, time.March, 1)
	end := temporalTestTime(2026, time.June, 1)
	endRecorded := temporalTestTime(2026, time.May, 1)

	for _, version := range []Object{
		temporalVersionTestObject("Original", 1, start, nil, originalRecorded),
		temporalVersionTestObject("Corrected", 2, start, nil, correctionRecorded),
		temporalVersionTestObject("Future structure", 3, futureStart, nil, futureRecorded),
		temporalVersionTestObject("Future structure", 4, futureStart, &end, endRecorded),
	} {
		if err := store.SaveObject(ctx, version); err != nil {
			t.Fatalf("SaveObject(%s v%d): %v", version.Label, version.SourceVersion, err)
		}
	}

	assertTemporalObjectLabel(t, store, temporalTestTime(2026, time.February, 1), temporalTestTime(2026, time.January, 15), "Original")
	assertTemporalObjectLabel(t, store, temporalTestTime(2026, time.February, 1), temporalTestTime(2026, time.March, 15), "Corrected")
	assertTemporalObjectLabel(t, store, temporalTestTime(2026, time.May, 1), temporalTestTime(2026, time.April, 1), "Future structure")
	assertTemporalObjectLabel(t, store, temporalTestTime(2026, time.July, 1), temporalTestTime(2026, time.April, 1), "Future structure")

	objects, _, _, _, err := store.SnapshotAsOf(ctx, "tenant-1", temporalTestTime(2026, time.July, 1), temporalTestTime(2026, time.June, 2))
	if err != nil {
		t.Fatalf("SnapshotAsOf ended interval: %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("ended organization version resurfaced: %+v", objects)
	}
}

func TestProjectionLinkIdentityKeepsOneTemporalRelationshipSlot(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	firstStart := temporalTestTime(2026, time.January, 1)
	secondStart := temporalTestTime(2026, time.April, 1)
	for index, value := range []struct {
		eventID string
		manager string
		start   time.Time
	}{
		{eventID: "reporting-1", manager: "manager-a", start: firstStart},
		{eventID: "reporting-2", manager: "manager-b", start: secondStart},
	} {
		recordedAt := value.start.Add(time.Hour)
		result := graphprotocol.ProjectionResult{
			Assertions: []graphprotocol.Assertion{
				graphprotocol.LinkAssertion{
					Identity: "reporting-slot-1", LinkType: "reporting_relationship_to_manager",
					From: graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "reporting_relationship", ID: "reporting-slot-1"},
					To:   graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "workforce_subject", ID: value.manager},
					Provenance: graphprotocol.ProvenanceRef{
						SourceEventID: value.eventID, SourceEventType: "people.reporting_relationship.v2.changed",
						SourceSystem: "people", Actor: graphprotocol.ActorRef{Kind: "service", ID: "people"},
						ObservedAt: recordedAt, SourceVersion: int64(index + 1),
						Temporal: graphprotocol.TemporalContext{
							Mode: "state_interval", BusinessValidFrom: value.start,
							SourceRecordedAt: recordedAt, OrreryIngestedAt: recordedAt,
						},
					},
				},
			},
		}
		if err := ApplyProjectionResult(ctx, store, result); err != nil {
			t.Fatalf("ApplyProjectionResult: %v", err)
		}
	}
	_, current, _, _, err := store.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(current) != 1 || current[0].ID != "reporting_relationship_to_manager:reporting-slot-1" || current[0].ToObjectID != "manager-b" {
		t.Fatalf("current reporting slot = %+v", current)
	}
	_, historical, _, _, err := store.SnapshotAsOf(ctx, "tenant-1", temporalTestTime(2026, time.February, 1), temporalTestTime(2026, time.May, 1))
	if err != nil {
		t.Fatalf("SnapshotAsOf: %v", err)
	}
	if len(historical) != 1 || historical[0].ToObjectID != "manager-a" {
		t.Fatalf("historical reporting slot = %+v", historical)
	}
}

func temporalVersionTestObject(label string, version int64, validFrom time.Time, validTo *time.Time, recordedAt time.Time) Object {
	return Object{
		ID: "org-1", TenantID: "tenant-1", ObjectType: "organization_unit",
		CanonicalKey: "org-1", Label: label, SourceStreamID: "org_unit:org-1",
		SourceVersion: version, UpdatedAt: recordedAt,
		Temporal: TemporalMetadata{
			Mode: "state_interval", BusinessValidFrom: validFrom, BusinessValidTo: validTo,
			SourceRecordedAt: recordedAt, OrreryIngestedAt: recordedAt,
		},
	}
}

func assertTemporalObjectLabel(t *testing.T, store TemporalStore, businessAsOf, knownAt time.Time, want string) {
	t.Helper()
	objects, _, _, _, err := store.SnapshotAsOf(context.Background(), "tenant-1", businessAsOf, knownAt)
	if err != nil {
		t.Fatalf("SnapshotAsOf(%s, %s): %v", businessAsOf, knownAt, err)
	}
	if len(objects) != 1 || objects[0].Label != want {
		t.Fatalf("SnapshotAsOf(%s, %s) objects = %+v, want label %q", businessAsOf, knownAt, objects, want)
	}
}

func temporalTestTime(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
