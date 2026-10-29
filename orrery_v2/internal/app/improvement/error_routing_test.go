package improvement

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestFailedRuntimeWithIncompleteEvidenceIsExplicitlyBlocked(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, request := runtimeEvidenceFixture(t, "failed-without-execution")
	if err := s.RuntimeEvidence().Capture(ctx, request); err != nil {
		t.Fatal(err)
	}
	end := runtimeEvidenceTerminal()
	end.Status = agentcore.RuntimeStatusFailed
	c, err := s.RuntimeEvidence().Complete(ctx, request, end)
	if err != nil || c.State != Blocked || c.Reason != "runtime_evidence_incomplete" || c.Diagnosis != nil {
		t.Fatal("error classified as ordinary observation", c, err)
	}
	again, err := s.RuntimeEvidence().Complete(ctx, request, end)
	if err != nil || again.Revision != c.Revision || again.State != c.State {
		t.Fatal("terminal recovery changed error state", again, err)
	}
}

func TestLifecycleFailureRequiresCapturedEvidenceAndDoesNotClaimRecovery(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	o := testObservation("lifecycle-failure")
	o.Trigger = "runtime_failed"
	c, err := s.Observe(ctx, o)
	if err != nil || c.State != Blocked || c.Reason != "captured_runtime_evidence_required" || c.Diagnosis != nil {
		t.Fatal("failure hidden as observation", c, err)
	}
	page, err := s.core.ListRecords(ctx, runtimeDiagnosisQueueTable, "", 10)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("uncaptured execution sent for model guessing", page, err)
	}
}

func TestCapturedRuntimeErrorWithEvidenceEntersDiagnosis(t *testing.T) {
	s := diagnosisTestStore(t)
	c, _ := diagnosisTestCase(t, s, "captured-error", agentcore.RuntimeStatusFailed)
	if c.State != PendingDiagnosis {
		t.Fatal("captured error stayed observed", c)
	}
	if worked, err := s.DiagnoseNextRuntime(context.Background(), runtimeDiagnoserFunc(diagnosisTestModel), 4); !worked || err != nil {
		t.Fatal(worked, err)
	}
	after, err := s.Get(context.Background(), c.TenantID, c.ID)
	if err != nil || after.State != Diagnosed || after.Diagnosis == nil || after.Release != nil || after.Outcome != nil {
		t.Fatal("diagnosis confused with recovery", after, err)
	}
}
