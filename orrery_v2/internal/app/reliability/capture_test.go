package reliability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type unreliableCaptureObserver struct {
	service   *Service
	fail      bool
	committed bool
}

func (o *unreliableCaptureObserver) ObserveFailure(ctx context.Context, observation FailureObservation) (FailureIncident, error) {
	if o.fail && !o.committed {
		return FailureIncident{}, errors.New("database temporarily unavailable")
	}
	incident, err := o.service.ObserveFailure(ctx, observation)
	if err == nil && o.fail {
		return incident, errors.New("acknowledgement lost after commit")
	}
	return incident, err
}

func TestCaptureInboxSurvivesDatabaseFailureRestartAndLostAcknowledgement(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service := NewService(NewMemoryRepository())
	observer := &unreliableCaptureObserver{service: service, fail: true}
	q, err := NewCaptureInbox(dir, observer)
	if err != nil {
		t.Fatal(err)
	}
	record := CapturedFailure{Observation: FailureObservation{ID: "one", TenantID: "tenant-a", Source: "web_client", Component: "javascript:/holdings", ErrorCode: "TypeError", Message: "holdings timeout token=secret"}, Diagnostics: map[string]string{"cause": "database timeout password=secret", "stack": "at https://host/app.js?token=hidden", "body": "must never be saved"}}
	if err = q.Enqueue(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = q.Sweep(ctx); err == nil || q.Status().Pending != 1 {
		t.Fatalf("failed delivery lost record: %+v %v", q.Status(), err)
	}
	// A new process opens the persisted record; the first retry commits but loses its acknowledgement.
	observer.committed = true
	q, err = NewCaptureInbox(dir, observer)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Sweep(ctx); err == nil || q.Status().Pending != 1 {
		t.Fatalf("lost acknowledgement was accepted: %+v %v", q.Status(), err)
	}
	observer.fail = false
	if err = q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if q.Status().Pending != 0 {
		t.Fatal(q.Status())
	}
	// A browser also retries the acknowledged ID after its HTTP response was lost.
	if err = q.Enqueue(ctx, record); err != nil {
		t.Fatal(err)
	}
	if q.Status().Pending != 0 {
		t.Fatal("already delivered ID queued twice")
	}
	incident, err := service.ObserveFailure(ctx, record.Observation)
	if err != nil {
		t.Fatal(err)
	}
	if incident.OccurrenceCount != 1 {
		t.Fatalf("retry counted twice: %+v", incident)
	}
	records, err := q.Diagnostics("tenant-a", incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("history = %+v", records)
	}
	// A crash after writing history but before removing pending replays safely on Windows and Linux.
	key := captureKey("tenant-a", "one")
	data, err := os.ReadFile(filepath.Join(dir, "history", key))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "pending", key), data, 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewCaptureInbox(dir, observer)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Sweep(ctx); err != nil {
		t.Fatalf("crash replay failed: %v", err)
	}
	if records[0].Diagnostics["body"] != "" || strings.Contains(records[0].Diagnostics["cause"], "secret") || strings.Contains(records[0].Diagnostics["stack"], "hidden") {
		t.Fatal("private data persisted", records)
	}
	other, err := q.Diagnostics("tenant-b", incident.ID)
	if err != nil || len(other) != 0 {
		t.Fatalf("tenant leak: %+v %v", other, err)
	}
}

func TestCaptureInboxQuarantinesCorruptionAndContinues(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	q, err := NewCaptureInbox(dir, NewService(NewMemoryRepository()))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "pending", "000-invalid.json"), []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	q, err = NewCaptureInbox(dir, q.observer)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Enqueue(ctx, CapturedFailure{Observation: FailureObservation{ID: "good", TenantID: "tenant-a", Source: "test", Component: "worker", ErrorCode: "timeout", Message: "request timeout"}}); err != nil {
		t.Fatal(err)
	}
	if err = q.Sweep(ctx); err == nil {
		t.Fatal("corruption was hidden")
	}
	if q.Status().Pending != 0 || q.Status().Quarantined != 1 || q.Status().Delivered != 1 {
		t.Fatal(q.Status())
	}
	if _, err = os.Stat(filepath.Join(dir, "quarantine", "000-invalid.json")); err != nil {
		t.Fatal(err)
	}
}
