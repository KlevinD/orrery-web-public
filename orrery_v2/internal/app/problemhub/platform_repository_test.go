package problemhub

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProblemHubRoutesVerifiedResolutionIntoOneLessonCandidatePerCycle(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "problem-hub-lifecycle"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{problemHubMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC)
	lessons := &recordingLessonSink{}
	events := &recordingProblemEventSink{}
	service := NewService(NewPlatformRepository(store), WithClock(func() time.Time { return now }), WithLessonSink(lessons), WithEventSink(events))
	observation := Observation{
		ID: "observation-1", TenantID: "tenant-a", Fingerprint: "runtime:pi:timeout",
		SourceDomain: "reliability", Source: ObjectRef{Kind: "failure_incident", ID: "incident-1"},
		Category: "transient", Severity: "error", Title: "PI timeout",
		RedactedSummary: "request failed token=secret-value", EvidenceID: "evidence-observed-1",
		Actor: ActorRef{Kind: "worker", ID: "runtime-1"}, OccurredAt: now,
	}
	problem, err := service.Observe(ctx, observation)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if problem.Status != StatusOpen || problem.OccurrenceCount != 1 || problem.Version != 1 {
		t.Fatalf("initial problem = %+v", problem)
	}
	if problem.RedactedSummary != "request failed token=[REDACTED]" {
		t.Fatalf("redacted summary = %q", problem.RedactedSummary)
	}
	if _, err := service.Observe(ctx, observation); err != nil {
		t.Fatalf("Observe replay: %v", err)
	}
	replayed, err := service.Get(ctx, "tenant-a", problem.ID)
	if err != nil {
		t.Fatalf("Get after replay: %v", err)
	}
	if replayed.OccurrenceCount != 1 || replayed.Version != 1 {
		t.Fatalf("replayed problem = %+v", replayed)
	}

	routed, err := service.Route(ctx, RouteCommand{
		ID: "route-1", TenantID: "tenant-a", ProblemCaseID: problem.ID,
		Owner: ActorRef{Kind: "team", ID: "platform"}, Actor: ActorRef{Kind: "person", ID: "operator-1"}, OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if routed.Owner.Kind != "team" || routed.Owner.ID != "platform" {
		t.Fatalf("routed owner = %+v", routed.Owner)
	}
	if _, err := service.Route(ctx, RouteCommand{
		ID: "route-1", TenantID: "tenant-a", ProblemCaseID: problem.ID,
		Owner: ActorRef{Kind: "team", ID: "platform"}, Actor: ActorRef{Kind: "person", ID: "operator-1"}, OccurredAt: now,
	}); err != nil {
		t.Fatalf("Route replay: %v", err)
	}

	withOutcome, err := service.RecordOutcome(ctx, Outcome{
		ID: "outcome-1", TenantID: "tenant-a", Source: observation.Source, Success: true,
		Resolution:      ObjectRef{Kind: "repair_rule", ID: "rule-1"},
		RedactedSummary: "Verified retry repair succeeded.", EvidenceID: "evidence-verified-1",
		Actor: ActorRef{Kind: "service", ID: "repair-monitor"}, OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if withOutcome.LessonCandidateID != "" || len(lessons.proposals) != 0 {
		t.Fatalf("unresolved problem generated a lesson: problem=%+v proposals=%+v", withOutcome, lessons.proposals)
	}

	resolved, err := service.SyncSourceStatus(ctx, "tenant-a", observation.Source, StatusResolved, ActorRef{Kind: "service", ID: "repair-monitor"}, now)
	if err != nil {
		t.Fatalf("SyncSourceStatus resolved: %v", err)
	}
	if resolved.Status != StatusResolved || resolved.LessonCandidateID == "" || len(lessons.proposals) != 1 {
		t.Fatalf("resolved problem = %+v, proposals=%+v", resolved, lessons.proposals)
	}
	if len(resolved.EvidenceIDs) != 2 || lessons.proposals[0].ProblemCaseID != resolved.ID {
		t.Fatalf("resolved evidence/lesson = %+v / %+v", resolved.EvidenceIDs, lessons.proposals[0])
	}
	firstLessonID := resolved.LessonCandidateID
	if _, err := service.SyncSourceStatus(ctx, "tenant-a", observation.Source, StatusResolved, ActorRef{}, now); err != nil {
		t.Fatalf("SyncSourceStatus replay: %v", err)
	}
	if len(lessons.proposals) != 1 {
		t.Fatalf("resolved replay proposals = %d", len(lessons.proposals))
	}

	observation.ID = "observation-2"
	observation.EvidenceID = "evidence-observed-2"
	observation.OccurredAt = now.Add(time.Hour)
	reopened, err := service.Observe(ctx, observation)
	if err != nil {
		t.Fatalf("Observe recurrence: %v", err)
	}
	if reopened.Status != StatusOpen || reopened.OccurrenceCount != 2 || reopened.LessonCandidateID != "" || reopened.LatestOutcomeID != "" {
		t.Fatalf("reopened problem = %+v", reopened)
	}
	_, err = service.RecordOutcome(ctx, Outcome{
		ID: "outcome-2", TenantID: "tenant-a", Source: observation.Source, Success: true,
		Resolution:      ObjectRef{Kind: "repair_rule", ID: "rule-1"},
		RedactedSummary: "Verified retry repair succeeded again.", EvidenceID: "evidence-verified-2",
		Actor: ActorRef{Kind: "service", ID: "repair-monitor"}, OccurredAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("RecordOutcome recurrence: %v", err)
	}
	resolvedAgain, err := service.SyncSourceStatus(ctx, "tenant-a", observation.Source, StatusResolved, ActorRef{}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("resolve recurrence: %v", err)
	}
	if resolvedAgain.LessonCandidateID == firstLessonID || len(lessons.proposals) != 2 {
		t.Fatalf("second learning cycle = %+v proposals=%d", resolvedAgain, len(lessons.proposals))
	}
	if events.published[EventLessonCandidateProposed] != 2 || events.replayed[EventProblemCaseUpserted] == 0 {
		t.Fatalf("event counts published=%+v replayed=%+v", events.published, events.replayed)
	}
}

func TestProblemHubReplaysLessonProjectionAfterPartialFailure(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "problem-hub-lesson-replay"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{problemHubMigration()}).Up(ctx, store); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 6, 0, 0, 0, time.UTC)
	events := &recordingProblemEventSink{failOnce: EventLessonCandidateProposed}
	lessons := &recordingLessonSink{}
	service := NewService(NewPlatformRepository(store), WithClock(func() time.Time { return now }), WithLessonSink(lessons), WithEventSink(events))
	source := ObjectRef{Kind: "failure_incident", ID: "incident-replay"}
	problem, err := service.Observe(ctx, Observation{
		ID: "observation-replay", TenantID: "tenant-a", Fingerprint: "lesson-replay", SourceDomain: "reliability",
		Source: source, Category: "transient", Severity: "error", Title: "replay timeout", EvidenceID: "evidence-1", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordOutcome(ctx, Outcome{
		ID: "outcome-replay", TenantID: "tenant-a", ProblemCaseID: problem.ID, Source: source, Success: true,
		Resolution: ObjectRef{Kind: "repair_rule", ID: "rule-replay"}, RedactedSummary: "verified retry succeeded",
		EvidenceID: "evidence-2", OccurredAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SyncSourceStatus(ctx, "tenant-a", source, StatusResolved, ActorRef{Kind: "service", ID: "monitor"}, now); !errors.Is(err, errTestLessonProjection) {
		t.Fatalf("first resolution error=%v", err)
	}
	linked, err := service.Get(ctx, "tenant-a", problem.ID)
	if err != nil || linked.LessonCandidateID == "" {
		t.Fatalf("linked problem=%+v err=%v", linked, err)
	}
	if _, err := service.SyncSourceStatus(ctx, "tenant-a", source, StatusResolved, ActorRef{Kind: "service", ID: "monitor"}, now); err != nil {
		t.Fatalf("resolution replay: %v", err)
	}
	if events.replayed[EventLessonCandidateProposed] != 1 || len(lessons.proposals) != 1 {
		t.Fatalf("lesson replay events=%+v proposals=%d", events.replayed, len(lessons.proposals))
	}
}

func TestProblemHubKeepsTenantsAndSourceLookupsIsolated(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "problem-hub-tenants"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{problemHubMigration()}).Up(ctx, store); err != nil {
		t.Fatal(err)
	}
	service := NewService(NewPlatformRepository(store))
	observe := func(tenant, observationID, incidentID string) ProblemCase {
		problem, observeErr := service.Observe(ctx, Observation{
			ID: observationID, TenantID: tenant, Fingerprint: "shared", SourceDomain: "reliability",
			Source: ObjectRef{Kind: "failure_incident", ID: incidentID}, Category: "code", Severity: "error",
			Title: "build failure", EvidenceID: "evidence-" + tenant,
		})
		if observeErr != nil {
			t.Fatalf("Observe(%s): %v", tenant, observeErr)
		}
		return problem
	}
	a := observe("tenant-a", "observation-a", "incident-a")
	b := observe("tenant-b", "observation-b", "incident-b")
	if a.ID == b.ID {
		t.Fatalf("tenant-isolated problem ids matched: %s", a.ID)
	}
	loaded, err := NewPlatformRepository(store).GetBySource(ctx, "tenant-a", ObjectRef{Kind: "failure_incident", ID: "incident-a"})
	if err != nil || loaded.ID != a.ID {
		t.Fatalf("GetBySource tenant-a = %+v, %v", loaded, err)
	}
	listed, err := service.List(ctx, ListQuery{TenantID: "tenant-a", Status: StatusOpen})
	if err != nil || len(listed) != 1 || listed[0].ID != a.ID {
		t.Fatalf("List tenant-a = %+v, %v", listed, err)
	}
	searched, err := service.List(ctx, ListQuery{TenantID: "tenant-a", Search: "BUILD failure", Limit: 10})
	if err != nil || len(searched) != 1 || searched[0].ID != a.ID {
		t.Fatalf("Search tenant-a = %+v, %v", searched, err)
	}
	notMatched, err := service.List(ctx, ListQuery{TenantID: "tenant-a", Search: "other incident", Limit: 10})
	if err != nil || len(notMatched) != 0 {
		t.Fatalf("Search unmatched tenant-a = %+v, %v", notMatched, err)
	}
}

type recordingLessonSink struct {
	proposals []LessonProposal
}

func (s *recordingLessonSink) ProposeProblemLesson(_ context.Context, proposal LessonProposal) (string, error) {
	s.proposals = append(s.proposals, proposal)
	return proposal.ID, nil
}

type recordingProblemEventSink struct {
	published map[string]int
	replayed  map[string]int
	failOnce  string
}

var errTestLessonProjection = errors.New("lesson projection unavailable")

func (s *recordingProblemEventSink) PublishProblemHubEvent(_ context.Context, event Event) error {
	if s.published == nil {
		s.published = map[string]int{}
	}
	s.published[event.Type]++
	if s.failOnce == event.Type {
		s.failOnce = ""
		return errTestLessonProjection
	}
	return nil
}

func (s *recordingProblemEventSink) ReplayProblemHubEvent(_ context.Context, event Event) error {
	if s.replayed == nil {
		s.replayed = map[string]int{}
	}
	s.replayed[event.Type]++
	return nil
}
