package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

type systemRepairReaderStub struct {
	receipt *reliability.AutomaticRepairReceipt
	calls   int
}

func (s *systemRepairReaderStub) AutomaticRepair(string, string) (*reliability.AutomaticRepairReceipt, error) {
	s.calls++
	return s.receipt, nil
}

func systemSourceFixture(t *testing.T) productionPersonalAssistantSystemSource {
	t.Helper()
	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: {Driver: db.DriverMemory, Name: t.Name()}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	migrations := migration.NewCoordinator(registry)
	if err := reliability.RegisterMigrations(migrations); err != nil {
		t.Fatal(err)
	}
	if err := migrations.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatal(err)
	}
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	return productionPersonalAssistantSystemSource{core: core}
}

func TestSystemSourceOriginalIncidentsRedactionAndRepairEvidence(t *testing.T) {
	s := systemSourceFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	service := reliability.NewService(reliability.NewPlatformRepository(s.core), reliability.WithClock(func() time.Time { return now }))
	i, err := service.ObserveFailure(ctx, reliability.FailureObservation{ID: "event", TenantID: "tenant-a", Source: "server_log", Component: "scheduler", ErrorCode: "timeout", Message: "data unavailable password=secret-value", OccurredAt: now, Severity: reliability.FailureSeverityError})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(ctx, "tenant-a", "failure", personalAssistantSystemCursor{At: now.Add(-time.Second).UnixNano()})
	if err != nil || len(rows) != 1 {
		t.Fatalf("canonical incident query: %+v %v", rows, err)
	}
	u, found, err := s.Current(ctx, "tenant-a", "failure", i.ID, true)
	if err != nil || !found || u.Category != "system_error" {
		t.Fatal(u, found, err)
	}
	material, _ := json.Marshal(u.Facts)
	if strings.Contains(string(material), "secret-value") {
		t.Fatal("private diagnostic leaked")
	}
	reader := &systemRepairReaderStub{receipt: &reliability.AutomaticRepairReceipt{TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), Status: "resolved", UpdatedAt: now.Add(time.Second)}}
	s.repairs = reader
	u, _, err = s.Current(ctx, "tenant-a", "failure", i.ID, true)
	if err != nil || u.Category != "system_error" {
		t.Fatal("unverified receipt reported resolution", err)
	}
	reader.receipt.Verified = true
	reader.receipt.Evidence = []string{"verified-evidence"}
	reader.receipt.RepairRunID = "actual-run"
	u, _, err = s.Current(ctx, "tenant-a", "failure", i.ID, true)
	if err != nil || u.State != "resolved" || u.Category != "automatic_repair" {
		t.Fatal("verified host result lost", err)
	}
	before := reader.calls
	u, found, err = s.Current(ctx, "tenant-a", "failure", i.ID, false)
	if err != nil || !found || u.Category != "system_error" || !u.DiagnosticsHidden || reader.calls != before {
		t.Fatal("read-only view loaded protected host receipt", u, err)
	}
	if _, present := u.Facts["repair_summary"]; present {
		t.Fatal("host receipt leaked to read-only material")
	}
	reader.receipt.OccurrenceCount++
	u, _, err = s.Current(ctx, "tenant-a", "failure", i.ID, true)
	if err != nil || u.Category != "system_error" {
		t.Fatal("stale receipt crossed occurrence fence", err)
	}
	for _, component := range []string{"personal-assistant-notification-heartbeat-failed", "notification/weixin/bot/human", "agent-notice-test"} {
		incident, err := service.ObserveFailure(ctx, reliability.FailureObservation{ID: component, TenantID: "tenant-a", Source: "server_log", Component: component, ErrorCode: "failed", Message: "notification failure", OccurredAt: now, Severity: reliability.FailureSeverityError})
		if err != nil {
			t.Fatal(err)
		}
		if _, visible, err := s.Current(ctx, "tenant-a", "failure", incident.ID, true); err != nil || visible {
			t.Fatalf("notification failure recursively notified: %s %+v %v", component, incident, err)
		}
	}
	if _, visible, err := s.Current(ctx, "tenant-b", "failure", i.ID, true); err != nil || visible {
		t.Fatal("source crossed tenant", err)
	}
	notice, err := service.ObserveFailure(ctx, reliability.FailureObservation{ID: "runtime:agent-notice-123:failed:timeout", TenantID: "tenant-a", Source: "agent_runtime", Component: "agentcore", ErrorCode: "timeout", Message: "provider unavailable", OccurredAt: now, Severity: reliability.FailureSeverityError})
	if err != nil {
		t.Fatal(err)
	}
	if _, visible, err := s.Current(ctx, "tenant-a", "failure", notice.ID, true); err != nil || visible {
		t.Fatal("runtime notice failure recursively notified", err)
	}
}

func TestSystemSourceRSIMilestonesUseCanonicalCaseAndOriginalAccess(t *testing.T) {
	s := systemSourceFixture(t)
	ctx := context.Background()
	store := improvement.NewStore(s.core)
	o := improvement.Observation{ID: "observation", TenantID: "tenant-a", Task: improvement.ObjectRef{Kind: "task", ID: "task"}, Run: improvement.ObjectRef{Kind: "agent_run", ID: "run"}, Trigger: "quality_feedback", Summary: "减少返工", EvidenceIDs: []string{"evidence"}, OccurredAt: time.Now().UTC(), SourceAccessRef: graphprotocol.AccessRef{Kind: "task", ID: "task", TenantID: "tenant-a"}}
	c, err := store.Observe(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, visible, err := s.Current(ctx, c.TenantID, "rsi", c.ID, false); err != nil || visible {
		t.Fatal("raw observation treated as RSI progress", err)
	}
	c, err = store.Claim(ctx, c.TenantID, c.ID, "learner", c.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c, err = store.Advance(ctx, improvement.Transition{ID: "diagnose", TenantID: c.TenantID, CaseID: c.ID, ExpectedRevision: c.Revision, LeaseToken: c.Lease.Token, Actor: improvement.ObjectRef{Kind: "learner", ID: "learner"}, To: improvement.Diagnosed, Diagnosis: &improvement.Diagnosis{Summary: "证据已定位", EvidenceIDs: o.EvidenceIDs, AffectedKinds: []improvement.TargetKind{improvement.TargetSkill}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(ctx, c.TenantID, "rsi", personalAssistantSystemCursor{})
	if err != nil || len(rows) != 1 || rows[0].ID != c.ID {
		t.Fatalf("canonical RSI query: %+v %v", rows, err)
	}
	if next, err := s.List(ctx, c.TenantID, "rsi", rows[0].Position); err != nil || len(next) != 0 {
		t.Fatal("RSI keyset repeated final item", err)
	}
	u, visible, err := s.Current(ctx, c.TenantID, "rsi", c.ID, false)
	if err != nil || !visible || u.State != "diagnosed" || u.Access != o.SourceAccessRef || u.Facts["has_activation_receipt"] != false {
		t.Fatal("RSI evidence/access lost", u, err)
	}
	key := improvement.Digest(c.TenantID) + "/" + improvement.Digest(c.ID)
	if err := s.core.PutRecord(ctx, "ontology_improvement_cases", key, db.Record{"payload": `{"id":"forged"}`, "sha256": "wrong"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Current(ctx, c.TenantID, "rsi", c.ID, false); err == nil {
		t.Fatal("damaged case bypassed canonical integrity check")
	}
}
