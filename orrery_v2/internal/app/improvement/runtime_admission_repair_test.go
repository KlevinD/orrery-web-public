package improvement

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRuntimeAdmissionPreservesManagedRepairDiagnosisAndUpgradeRecovery(t *testing.T) {
	ctx := context.Background()
	s := repairDiagnosisStore(t)
	c, err := s.ObserveManagedRepair(ctx, testRepairFeedback("needs_attention"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	model := runtimeDiagnoserFunc(func(_ context.Context, input RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		calls++
		return RuntimeDiagnosisInference{Diagnosis: Diagnosis{Summary: "Repair evidence still requires verification", EvidenceIDs: []string{input.EvidenceIDs[0]}, AffectedKinds: []TargetKind{TargetTool}}, ProfileID: "profile", Model: "model", PromptSHA256: Digest("prompt")}, nil
	})
	if worked, err := s.DiagnoseNextRuntime(ctx, model, 4); !worked || err != nil {
		t.Fatal(worked, err)
	}
	after, err := s.Get(ctx, c.TenantID, c.ID)
	if err != nil || after.State != Blocked || after.Reason != "managed_repair_needs_attention" || after.Diagnosis == nil || after.Experiment != nil || after.Release != nil {
		t.Fatal("repair review lost its unresolved state", after, err)
	}
	job, err := s.RuntimeDiagnosis(ctx, c.TenantID, managedRepairAttempt(c))
	if err != nil || job.State != "completed" || job.Admission != nil || job.Input.ManagedRepair == nil {
		t.Fatal("repair diagnosis requires invented runtime baseline", job, err)
	}
	if _, saved, err := s.RuntimeExperimentAdmission(ctx, c.TenantID, job.AttemptID); saved || !errors.Is(err, ErrUnavailable) {
		t.Fatal("repair review claimed native runtime admission", saved, err)
	}
	// An older repair binary can commit the blocked case before acknowledging
	// its generated diagnosis. Upgrading must finish without new history/calls.
	job.State = "generated"
	key := diagnosisKey(job.TenantID, job.AttemptID)
	for _, table := range []string{runtimeDiagnosisTable, runtimeDiagnosisQueueTable} {
		if err := s.core.PutRecord(ctx, table, key, encode(job)); err != nil {
			t.Fatal(err)
		}
	}
	restored := NewStore(s.core)
	if worked, err := restored.DiagnoseNextRuntime(ctx, model, 4); !worked || err != nil || calls != 1 {
		t.Fatal("repair recovery repeated inference", worked, err, calls)
	}
	current, err := restored.Get(ctx, c.TenantID, c.ID)
	if err != nil || !reflect.DeepEqual(current, after) {
		t.Fatal("upgrade rewrote committed repair history", current, err)
	}
	stored, err := restored.RuntimeDiagnosis(ctx, c.TenantID, job.AttemptID)
	if err != nil || stored.State != "completed" || stored.Admission != nil {
		t.Fatal("repair recovery did not acknowledge diagnosis", stored, err)
	}
}
