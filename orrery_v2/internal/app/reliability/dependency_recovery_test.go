package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestDependencyRecoveryRejectsUnstableStaleAndQuarantinedProofs(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{reliabilityMigration(), reliabilityRepairExecutionMigration(), reliabilityDiagnosisMigration(), reliabilityRuleActivationMigration()}).Up(ctx, store); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	repo := NewPlatformRepository(store)
	s := NewService(repo, WithClock(func() time.Time { return now }))
	i, err := s.ObserveFailure(ctx, FailureObservation{ID: "pg", TenantID: "tenant-a", Source: "server_log", Component: "queue_claim", ErrorCode: "failed", Message: "failed to connect to user=orrery database=orrery_jobs: 127.0.0.1:5434 FATAL database system not yet accepting connections (SQLSTATE 57P03)", OccurredAt: now.Add(-5 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	p := DependencyRecovery{TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), Dependency: "postgres", Targets: []string{PostgresFailureTarget(i)}, FirstPassedAt: now.Add(-time.Minute), VerifiedAt: now}
	for _, test := range []struct {
		name   string
		modify func(*DependencyRecovery)
	}{
		{"one probe", func(p *DependencyRecovery) { p.FirstPassedAt = p.VerifiedAt }},
		{"expired probe", func(p *DependencyRecovery) {
			p.FirstPassedAt = now.Add(-4 * time.Minute)
			p.VerifiedAt = now.Add(-3 * time.Minute)
		}},
		{"before quiet period", func(p *DependencyRecovery) { p.FirstPassedAt = i.LastSeenAt.Add(time.Second) }},
		{"future probe", func(p *DependencyRecovery) { p.VerifiedAt = now.Add(time.Second) }},
		{"wrong target", func(p *DependencyRecovery) { p.Targets = []string{"postgres://other:5434/orrery_jobs"} }},
		{"old occurrence", func(p *DependencyRecovery) { p.OccurrenceCount++ }},
		{"wrong tenant", func(p *DependencyRecovery) { p.TenantID = "tenant-b" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := p
			test.modify(&copy)
			if err := s.ApplyDependencyRecovery(ctx, copy); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
	// A concurrent observation must roll back both the status update and proof.
	if _, err := s.ObserveFailure(ctx, FailureObservation{ID: "next", TenantID: i.TenantID, Source: i.Source, Component: i.Component, ErrorCode: i.ErrorCode, Message: i.RedactedSummary, OccurredAt: i.LastSeenAt.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.acceptDependencyRecovery(ctx, i, p, IncidentStatusResolved, now); !errors.Is(err, ErrConflict) {
		t.Fatal("stale compare-and-swap accepted", err)
	}
	if proof, err := repo.DependencyRecovery(ctx, i); err != nil || proof != nil {
		t.Fatal("rejected transition retained a proof", proof, err)
	}
	i, err = repo.GetIncident(ctx, i.TenantID, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionIncident(ctx, i.TenantID, i.ID, IncidentStatusQuarantined, ActorRef{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	p.OccurrenceCount = i.OccurrenceCount
	p.LastSeenNS = i.LastSeenAt.UnixNano()
	if err = s.ApplyDependencyRecovery(ctx, p); err == nil {
		t.Fatal("quarantine bypassed by healthy database")
	}
}
