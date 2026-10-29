package reliability

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type repairFeedbackTestSink struct {
	recordingEventSink
	receipts []AutomaticRepairReceipt
	fail     bool
}

func (s *repairFeedbackTestSink) ObserveAutomaticRepair(_ context.Context, r AutomaticRepairReceipt) error {
	if s.fail {
		return errors.New("feedback persistence unavailable")
	}
	s.receipts = append(s.receipts, r)
	return nil
}

func TestAutomaticRepairFeedbackRetriesAcceptedVerdictAndRejectsStaleReceipts(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	sink := &repairFeedbackTestSink{fail: true}
	s := NewService(repo, WithEventSink(sink))
	observation := FailureObservation{ID: "first", TenantID: "tenant-a", Source: "server_log", Component: "api", ErrorCode: "timeout", Message: "timeout", OccurredAt: time.Now().UTC()}
	i, err := s.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatal(err)
	}
	r := AutomaticRepairReceipt{Version: 1, TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount,
		LastSeenNS: i.LastSeenAt.UnixNano(), JobID: "job", BatchID: "batch", RepairRunID: "real-run", Status: "resolved", Evidence: []string{"receipt"}, UpdatedAt: time.Now().UTC()}
	if err := s.ApplyAutomaticRepairReceipt(ctx, r); err == nil || len(sink.receipts) != 0 {
		t.Fatal("unverified result reached feedback")
	}
	r.Verified = true
	if err := s.ApplyAutomaticRepairReceipt(ctx, r); err == nil {
		t.Fatal("feedback failure was discarded")
	}
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Status != IncidentStatusResolved {
		t.Fatal("feedback failure discarded accepted recovery")
	}
	version := i.Version
	sink.fail = false
	if err := s.ApplyAutomaticRepairReceipt(ctx, r); err != nil || len(sink.receipts) != 1 {
		t.Fatalf("accepted receipt not retried: %v", err)
	}
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Version != version {
		t.Fatal("feedback retry changed incident")
	}
	observation.ID = "second"
	observation.OccurredAt = observation.OccurredAt.Add(time.Second)
	if _, err := s.ObserveFailure(ctx, observation); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAutomaticRepairReceipt(ctx, r); err != nil || len(sink.receipts) != 1 {
		t.Fatal("stale receipt became current feedback")
	}
}

func TestCaptureAutomaticRepairProgressAndVerifiedResolution(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	service := NewService(repo)
	q, err := NewCaptureInbox(t.TempDir(), service)
	if err != nil {
		t.Fatal(err)
	}
	observation := FailureObservation{ID: "first", TenantID: "system", Source: "server_log", Component: "api", ErrorCode: "timeout", Message: "timeout", OccurredAt: time.Now().UTC()}
	i, err := service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatal(err)
	}
	r := AutomaticRepairReceipt{Version: 1, TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), JobID: "repair-one", Status: "running", Summary: "password=private", UpdatedAt: time.Now().UTC()}
	write := func() {
		t.Helper()
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(q.dir, "repairs", captureKey(i.TenantID, i.ID)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if err := q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Status != IncidentStatusRepairing {
		t.Fatal(i.Status)
	}
	r.Status = "resolved"
	write()
	if err := q.Sweep(ctx); err == nil {
		t.Fatal("unverified resolution accepted")
	}
	r.Verified = true
	r.RepairRunID = "actual-run"
	r.Evidence = []string{"test-receipt"}
	write()
	if err := q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Status != IncidentStatusResolved {
		t.Fatal(i.Status)
	}
	version := i.Version
	if err := q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Version != version {
		t.Fatal("replay changed incident version")
	}
	receipt, err := q.AutomaticRepair(i.TenantID, i.ID)
	if err != nil || receipt == nil || strings.Contains(receipt.Summary, "private") {
		t.Fatalf("unsafe receipt: %+v %v", receipt, err)
	}
	other, err := q.AutomaticRepair("other-tenant", i.ID)
	if err != nil || other != nil {
		t.Fatal("receipt leaked across tenants")
	}
	observation.ID = "second"
	observation.OccurredAt = observation.OccurredAt.Add(time.Second)
	i, err = service.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatal(err)
	}
	before := i.Status
	if err := q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Status != before || receipt.Current(i) {
		t.Fatal("stale result closed a new occurrence")
	}
}

func TestCaptureAutomaticRepairDoesNotOverrideQuarantine(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	sink := &repairFeedbackTestSink{}
	s := NewService(repo, WithEventSink(sink))
	i, err := s.ObserveFailure(ctx, FailureObservation{ID: "one", TenantID: "system", Source: "web_client", Component: "ui", ErrorCode: "error", Message: "error"})
	if err != nil {
		t.Fatal(err)
	}
	i, err = s.TransitionIncident(ctx, i.TenantID, i.ID, IncidentStatusQuarantined, ActorRef{Kind: "human", ID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.ApplyAutomaticRepairReceipt(ctx, AutomaticRepairReceipt{TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	i, _ = repo.GetIncident(ctx, i.TenantID, i.ID)
	if i.Status != IncidentStatusQuarantined {
		t.Fatal("operator quarantine overridden")
	}
	for _, status := range []string{"needs_attention", "technical_recovered", "resolved"} {
		err := s.ApplyAutomaticRepairReceipt(ctx, AutomaticRepairReceipt{Version: 1, TenantID: i.TenantID, IncidentID: i.ID,
			OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), Status: status, Verified: status == "resolved",
			JobID: "job", BatchID: "batch", RepairRunID: "run", Evidence: []string{"verification"}, UpdatedAt: time.Now().UTC()})
		if err != nil || len(sink.receipts) != 0 {
			t.Fatalf("quarantined incident reached feedback: %v", err)
		}
	}
}
