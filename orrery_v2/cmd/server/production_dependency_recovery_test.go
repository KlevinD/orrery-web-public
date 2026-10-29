package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/reliability"
)

const recoveryPostgresError = "begin postgres transaction: failed to connect to `user=orrery database=orrery_jobs`: 127.0.0.1:5434 (127.0.0.1): server error: FATAL: the database system is not yet accepting connections (SQLSTATE 57P03)"

func TestDependencyRecoveryRequiresConsecutiveActualWritesAndFencesRecurrence(t *testing.T) {
	source := systemSourceFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	repo := reliability.NewPlatformRepository(source.core)
	control := reliability.NewService(repo, reliability.WithClock(func() time.Time { return now }))
	observation := reliability.FailureObservation{ID: "pg-first", TenantID: "tenant-a", Source: "server_log", Component: "queue_claim", ErrorCode: "queue_claim_failed", Message: recoveryPostgresError, Severity: reliability.FailureSeverityError, OccurredAt: now.Add(-3 * time.Minute)}
	i, err := control.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatal(err)
	}
	target := reliability.PostgresFailureTarget(i)
	if target != "postgres://127.0.0.1:5434/orrery_jobs" {
		t.Fatalf("original pgx error was not bound: %q", target)
	}
	failed := false
	calls := 0
	monitor := &productionDependencyRecovery{core: source.core, control: control, now: func() time.Time { return now }, postgres: map[string]func(context.Context) error{target: func(context.Context) error {
		calls++
		if failed {
			return errors.New("write still fails")
		}
		return nil
	}}, passes: map[string]dependencyRecoveryPass{}}
	inbox, err := reliability.NewCaptureInbox(t.TempDir(), control)
	if err != nil {
		t.Fatal(err)
	}
	monitor.inbox = inbox
	if err := inbox.Enqueue(ctx, reliability.CapturedFailure{Observation: reliability.FailureObservation{ID: "unrelated-pending-provider-error", TenantID: i.TenantID, Source: "server_log", Component: "outbox_relay_process", ErrorCode: "process_failed", Message: "gateway channel delivery attempt failed ret=-2"}}); err != nil {
		t.Fatal(err)
	}
	sweep := func() {
		t.Helper()
		if err := monitor.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sweep()
	now = now.Add(31 * time.Second)
	failed = true
	sweep()
	now = now.Add(31 * time.Second)
	failed = false
	sweep()
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Status == reliability.IncidentStatusResolved {
		t.Fatal("a failed write did not reset stabilization")
	}
	now = now.Add(31 * time.Second)
	sweep()
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Status != reliability.IncidentStatusResolved || calls != 4 {
		t.Fatalf("recovery not verified: %+v calls=%d", i, calls)
	}
	proof, err := repo.DependencyRecovery(ctx, i)
	if err != nil || proof == nil || !proof.Current(i) {
		t.Fatal("durable proof missing", proof, err)
	}
	receipt := reliability.AutomaticRepairReceipt{TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), Status: "needs_attention", UpdatedAt: now.Add(-time.Minute)}
	if receipt.CurrentResult(i) {
		t.Fatal("old needs-attention result masked verified recovery")
	}
	if err := control.ApplyAutomaticRepairReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := repo.GetIncident(ctx, i.TenantID, i.ID)
	if unchanged.Status != reliability.IncidentStatusResolved {
		t.Fatal("old receipt reopened recovery")
	}
	observation.ID = "pg-recurrence"
	observation.OccurredAt = now
	now = now.Add(time.Second)
	i, err = control.ObserveFailure(ctx, observation)
	if err != nil || i.Status != reliability.IncidentStatusOpen {
		t.Fatal("new error did not reopen", i, err)
	}
	if err := control.ApplyDependencyRecovery(ctx, *proof); err == nil {
		t.Fatal("old proof resolved a new occurrence")
	}
	if p, err := repo.DependencyRecovery(ctx, i); err != nil || p != nil {
		t.Fatal("old proof appeared current", p, err)
	}
}

func TestPendingRecurrenceFencesOnlyMatchingIncidentBinding(t *testing.T) {
	source := systemSourceFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	control := reliability.NewService(reliability.NewPlatformRepository(source.core))
	inbox, err := reliability.NewCaptureInbox(t.TempDir(), control)
	if err != nil {
		t.Fatal(err)
	}
	i := reliability.FailureIncident{TenantID: "tenant-a", Source: "server_log", Component: "queue_claim", ErrorCode: "failed", LastSeenAt: now}
	for _, o := range []reliability.FailureObservation{
		{ID: "provider", TenantID: "tenant-a", Source: "server_log", Component: "outbox_relay_process", ErrorCode: "failed", Message: "ret=-2", OccurredAt: now},
		{ID: "old", TenantID: "tenant-a", Source: i.Source, Component: i.Component, ErrorCode: i.ErrorCode, Message: "old failure", OccurredAt: now.Add(-time.Second)},
		{ID: "other-tenant", TenantID: "tenant-b", Source: i.Source, Component: i.Component, ErrorCode: i.ErrorCode, Message: "other failure", OccurredAt: now},
	} {
		if err := inbox.Enqueue(ctx, reliability.CapturedFailure{Observation: o}); err != nil {
			t.Fatal(err)
		}
	}
	if pending, err := inbox.HasPendingRecurrence(i); err != nil || pending {
		t.Fatal("unrelated pending failure blocked recovery", pending, err)
	}
	if err := inbox.Enqueue(ctx, reliability.CapturedFailure{Observation: reliability.FailureObservation{ID: "recurrence", TenantID: i.TenantID, Source: i.Source, Component: i.Component, ErrorCode: i.ErrorCode, Message: recoveryPostgresError, OccurredAt: now.Add(time.Second)}}); err != nil {
		t.Fatal(err)
	}
	if pending, err := inbox.HasPendingRecurrence(i); err != nil || !pending {
		t.Fatal("pending recurrence did not fence recovery", pending, err)
	}
}

func TestDependencyRecoveryRejectsWrongHostBusinessFailureAndUnstableProof(t *testing.T) {
	for _, test := range []struct{ name, component, message string }{
		{"wrong-host", "queue_claim", strings.ReplaceAll(recoveryPostgresError, "127.0.0.1:5434", "127.0.0.2:5434")},
		{"delivery-provider", "outbox_relay_process", "gateway channel delivery attempt failed ret=-2"},
		{"business", "agentcore", recoveryPostgresError},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := systemSourceFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			repo := reliability.NewPlatformRepository(source.core)
			control := reliability.NewService(repo, reliability.WithClock(func() time.Time { return now }))
			i, err := control.ObserveFailure(ctx, reliability.FailureObservation{ID: test.name, TenantID: "tenant-a", Source: "server_log", Component: test.component, ErrorCode: "failed", Message: test.message, OccurredAt: now.Add(-3 * time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			monitor := &productionDependencyRecovery{core: source.core, control: control, now: func() time.Time { return now }, postgres: map[string]func(context.Context) error{"postgres://127.0.0.1:5434/orrery_jobs": func(context.Context) error { t.Fatal("probed another dependency"); return nil }}, passes: map[string]dependencyRecoveryPass{}}
			if err := monitor.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			if err := monitor.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			loaded, _ := repo.GetIncident(ctx, i.TenantID, i.ID)
			if loaded.Status == reliability.IncidentStatusResolved {
				t.Fatal("unrelated fault auto-resolved")
			}
		})
	}
}

func TestDependencyRecoveryQueriesEveryOriginallyFailedFolder(t *testing.T) {
	source := systemSourceFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	repo := reliability.NewPlatformRepository(source.core)
	control := reliability.NewService(repo, reliability.WithClock(func() time.Time { return now }))
	inbox, err := reliability.NewCaptureInbox(t.TempDir(), control)
	if err != nil {
		t.Fatal(err)
	}
	cause := "pageindex GET /folder/folder_first/documents returned 500: Internal Server Error\npageindex GET /folder/folder_second/documents returned 500: Internal Server Error"
	err = inbox.Enqueue(ctx, reliability.CapturedFailure{Observation: reliability.FailureObservation{ID: "folders", TenantID: "global", Source: "server_log", Component: "knowledge_catalog_reconcile", ErrorCode: "logged_error", Message: cause, OccurredAt: now.Add(-3 * time.Minute)}, Diagnostics: map[string]string{"cause": cause}})
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := source.core.QueryRow(ctx, `SELECT id FROM reliability_failure_incidents WHERE component='knowledge_catalog_reconcile'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	secondFails := true
	calls := map[string]int{}
	monitor := &productionDependencyRecovery{core: source.core, control: control, inbox: inbox, now: func() time.Time { return now }, passes: map[string]dependencyRecoveryPass{}, folders: func(_ context.Context, folder string) error {
		calls[folder]++
		if folder == "folder_second" && secondFails {
			return errors.New("HTTP 500")
		}
		return nil
	}}
	for round := 0; round < 2; round++ {
		if err := monitor.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(31 * time.Second)
	}
	i, _ := repo.GetIncident(ctx, "global", id)
	if i.Status == reliability.IncidentStatusResolved {
		t.Fatal("only first folder was checked")
	}
	secondFails = false
	for round := 0; round < 2; round++ {
		if err := monitor.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(31 * time.Second)
	}
	i, _ = repo.GetIncident(ctx, "global", id)
	proof, err := repo.DependencyRecovery(ctx, i)
	if err != nil || i.Status != reliability.IncidentStatusResolved || proof == nil || len(proof.Targets) != 2 || calls["folder_second"] != 4 {
		t.Fatal("exact failed folders not verified", i, proof, calls, err)
	}
}

func TestDependencyNotificationsGroupOneDatabaseOutageWithoutLosingCursorOrTenant(t *testing.T) {
	s := systemSourceFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	control := reliability.NewService(reliability.NewPlatformRepository(s.core), reliability.WithClock(func() time.Time { return now }))
	var incidents []reliability.FailureIncident
	for index, component := range []string{"queue_claim", "execution_runner", "outbox_relay_run"} {
		i, err := control.ObserveFailure(ctx, reliability.FailureObservation{ID: component, TenantID: "tenant-a", Source: "server_log", Component: component, ErrorCode: "failed", Message: recoveryPostgresError, Severity: reliability.FailureSeverityError, OccurredAt: now.Add(time.Duration(index) * time.Millisecond)})
		if err != nil {
			t.Fatal(err)
		}
		incidents = append(incidents, i)
	}
	rows, err := s.List(ctx, "tenant-a", "failure", personalAssistantSystemCursor{})
	if err != nil || len(rows) != 3 {
		t.Fatal(rows, err)
	}
	group := rows[0].ID
	for _, row := range rows {
		if row.ID != group || row.Position.ID == group {
			t.Fatal("group lost original keyset cursor", rows)
		}
	}
	if next, err := s.List(ctx, "tenant-a", "failure", rows[len(rows)-1].Position); err != nil || len(next) != 0 {
		t.Fatal("cursor repeated", next, err)
	}
	u, visible, err := s.Current(ctx, "tenant-a", "failure", group, true)
	if err != nil || !visible || u.Facts["status"] != "unavailable" || len(u.Facts["members"].([]map[string]any)) != 3 {
		t.Fatal(u, visible, err)
	}
	if _, visible, err := s.Current(ctx, "tenant-a", "failure", incidents[0].ID, true); err != nil || visible {
		t.Fatal("individual pending notice was not fenced", err)
	}
	if _, visible, err := s.Current(ctx, "tenant-b", "failure", group, true); err != nil || visible {
		t.Fatal("group crossed tenant", err)
	}
	i, err := control.ObserveFailure(ctx, reliability.FailureObservation{ID: "provider-error", TenantID: "tenant-a", Source: "server_log", Component: "outbox_relay_process", ErrorCode: "failed", Message: "gateway channel delivery attempt failed ret=-2", Severity: reliability.FailureSeverityError})
	if err != nil {
		t.Fatal(err)
	}
	if _, visible, err := s.Current(ctx, "tenant-a", "failure", i.ID, true); err != nil || !visible {
		t.Fatal("provider rejection hidden by database group", err)
	}
	for _, i := range incidents {
		if _, err := control.TransitionIncident(ctx, i.TenantID, i.ID, reliability.IncidentStatusResolved, reliability.ActorRef{Kind: "human", ID: "operator"}); err != nil {
			t.Fatal(err)
		}
	}
	u, _, err = s.Current(ctx, "tenant-a", "failure", group, false)
	if err != nil || u.State != "postgres:resolved" {
		t.Fatal("group recovery missing", u, err)
	}
	if _, present := u.Facts["host_verified"]; present {
		t.Fatal("read-only notification loaded host proof")
	}
}
