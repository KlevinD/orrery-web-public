package improvement

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

func testRepairFeedback(status string) ManagedRepairFeedback {
	return ManagedRepairFeedback{
		TenantID: "tenant-a", IncidentID: "failure-a", OccurrenceCount: 2, LastSeenNS: time.Unix(100, 0).UnixNano(),
		JobID: "repair-job-a", BatchID: "repair-batch-a", RepairRunID: "real-repair-run", Status: status, Verified: status == "resolved",
		SourceEvidence: []string{"verification-tool-call"}, OccurredAt: time.Unix(102, 0).UTC(),
		SourceAccessRef: graphprotocol.AccessRef{Kind: "failure_incident", ID: "failure-a", TenantID: "tenant-a"},
	}
}

func TestManagedRepairFeedbackRetainsVerdictWithoutPromotingAndReplays(t *testing.T) {
	for _, status := range []string{"needs_attention", "technical_recovered", "resolved"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			store := testStore(t)
			feedback := testRepairFeedback(status)
			c, err := store.ObserveManagedRepair(ctx, feedback)
			if err != nil {
				t.Fatal(err)
			}
			want := Observed
			if status == "needs_attention" {
				want = Blocked
			}
			if c.State != want || c.ChangeSet != nil || c.Diagnosis != nil || c.Evaluation != nil || c.Release != nil || c.Outcome != nil || len(c.Observation.Dependencies) != 0 {
				t.Fatalf("repair verdict fabricated an improvement: %+v", c)
			}
			if c.Observation.Trigger != "managed_repair_"+status || c.Observation.Task.ID != feedback.IncidentID || c.Observation.Run.ID != "agent-run:"+feedback.RepairRunID {
				t.Fatalf("repair lost its failure/run binding: %+v", c.Observation)
			}
			stored, err := store.GetManagedRepairFeedback(ctx, feedback.TenantID, c.Observation.EvidenceIDs[0])
			if err != nil || !reflect.DeepEqual(stored, feedback) {
				t.Fatalf("acceptance evidence missing: %+v %v", stored, err)
			}
			stored.SourceEvidence[0] = "caller mutation"
			again, err := store.ObserveManagedRepair(ctx, feedback)
			if err != nil || !reflect.DeepEqual(c, again) {
				t.Fatalf("replay changed case: %+v %v", again, err)
			}
			if _, err := store.GetManagedRepairFeedback(ctx, "other-tenant", c.Observation.EvidenceIDs[0]); !errors.Is(err, ErrNotFound) {
				t.Fatalf("feedback leaked across tenants: %v", err)
			}
			projection := &testProjectionSink{fail: true}
			if n, err := store.ProjectPending(ctx, projection, 10); err == nil || n != 0 {
				t.Fatal("failed graph projection acknowledged")
			}
			projection.fail = false
			wantProjections := 1
			if status == "needs_attention" {
				wantProjections = 2
			}
			if n, err := store.ProjectPending(ctx, projection, 10); err != nil || n != wantProjections {
				t.Fatalf("projection retry lost feedback: %d %v", n, err)
			}
		})
	}
}

func TestManagedRepairFeedbackRejectsUnverifiedResolutionAndMismatchedSource(t *testing.T) {
	for _, change := range []func(*ManagedRepairFeedback){
		func(f *ManagedRepairFeedback) { f.Verified = false },
		func(f *ManagedRepairFeedback) { f.SourceEvidence = nil },
		func(f *ManagedRepairFeedback) { f.RepairRunID = "" },
		func(f *ManagedRepairFeedback) { f.SourceAccessRef.TenantID = "other" },
		func(f *ManagedRepairFeedback) { f.SourceAccessRef.ID = "other-incident" },
		func(f *ManagedRepairFeedback) { f.Status = "running" },
	} {
		store := testStore(t)
		f := testRepairFeedback("resolved")
		change(&f)
		if _, err := store.ObserveManagedRepair(context.Background(), f); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid feedback accepted: %+v %v", f, err)
		}
	}
}

func TestManagedRepairFeedbackAndCaseRollbackWhenOutboxFails(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	f := testRepairFeedback("resolved")
	_, err := store.core.Exec(ctx, `CREATE TRIGGER fail_repair_feedback_outbox BEFORE INSERT ON platform_records
WHEN NEW.table_name = 'ontology_improvement_projection_outbox'
BEGIN SELECT RAISE(ABORT, 'simulated projection failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ObserveManagedRepair(ctx, f); err == nil {
		t.Fatal("projection failure ignored")
	}
	if _, err := store.GetManagedRepairFeedback(ctx, f.TenantID, repairFeedbackID(f)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan acceptance evidence: %v", err)
	}
	page, err := store.List(ctx, f.TenantID, "", 10)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("case survived rollback: %+v %v", page, err)
	}
	if _, err := store.core.Exec(ctx, "DROP TRIGGER fail_repair_feedback_outbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ObserveManagedRepair(ctx, f); err != nil {
		t.Fatalf("retry after atomic rollback: %v", err)
	}
}
