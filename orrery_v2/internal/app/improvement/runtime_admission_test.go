package improvement

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeAdmissionCapturedWithDiagnosisAndOntologyEvidence(t *testing.T) {
	ctx := context.Background()
	s := diagnosisTestStore(t)
	original, request := diagnosisTestCase(t, s, "admission")
	if _, err := s.DiagnoseNextRuntime(ctx, runtimeDiagnoserFunc(diagnosisTestModel), 4); err != nil {
		t.Fatal(err)
	}
	a, saved, err := s.RuntimeExperimentAdmission(ctx, original.TenantID, request.AttemptID)
	if err != nil || !saved || a.State != "blocked" || len(a.Domains) != 9 || len(a.Blockers) < 4 {
		t.Fatal(a, saved, err)
	}
	job, _ := s.RuntimeDiagnosis(ctx, original.TenantID, request.AttemptID)
	c, _ := s.Get(ctx, original.TenantID, original.ID)
	if job.Admission == nil || a.DiagnosisReceiptID != job.ReceiptID || a.InputSHA256 != job.InputSHA256 ||
		c.State != Diagnosed || c.ChangeSet != nil || c.Experiment != nil || !reflect.DeepEqual(c.Observation, original.Observation) {
		t.Fatal("admission changed task state or lost provenance")
	}
	events := OntologyEvents(c)
	ids := events[0].Payload["evidence_ids"].([]string)
	if !containsAdmissionString(ids, a.ID) || events[0].SourceAccessRef != original.Observation.SourceAccessRef || len(events) != 1 {
		t.Fatal("admission missing from original scoped ontology evidence")
	}
	raw := encode(a)["payload"].(string)
	if strings.Contains(raw, job.Inference.Diagnosis.Summary) || strings.Contains(raw, "private task content") || strings.Contains(raw, "private credential") {
		t.Fatal("private prose leaked into admission")
	}
	before := Digest(c)
	restored := NewStore(s.core)
	if again, persisted, err := restored.RuntimeExperimentAdmission(ctx, c.TenantID, request.AttemptID); err != nil || !persisted || !reflect.DeepEqual(a, again) {
		t.Fatal("admission changed after restart", err)
	}
	if _, err := restored.DiagnoseNextRuntime(ctx, runtimeDiagnoserFunc(func(context.Context, RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error) {
		t.Fatal("admission inspection repeated inference")
		return RuntimeDiagnosisInference{}, nil
	}), 4); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(ctx, c.TenantID, c.ID)
	if before != Digest(current) {
		t.Fatal("read-only inspection rewrote case")
	}
	if _, _, err := s.RuntimeExperimentAdmission(ctx, "foreign", request.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant admission read", err)
	}
}

func containsAdmissionString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRuntimeAdmissionLegacyReadAndUpgradeRecoveryPreserveHistory(t *testing.T) {
	ctx := context.Background()
	s := diagnosisTestStore(t)
	original, request := diagnosisTestCase(t, s, "legacy-admission")
	if _, err := s.DiagnoseNextRuntime(ctx, runtimeDiagnoserFunc(diagnosisTestModel), 4); err != nil {
		t.Fatal(err)
	}
	job, _ := s.RuntimeDiagnosis(ctx, original.TenantID, request.AttemptID)
	c, _ := s.Get(ctx, original.TenantID, original.ID)
	// Simulate an old binary committing its case then dying before finalizing
	// the diagnosis record. A new binary may finish it, without adding history.
	job.Admission, job.State = nil, "generated"
	c.Diagnosis.EvidenceIDs = append([]string{job.ReceiptID}, job.Input.EvidenceIDs...)
	if err := s.core.PutRecord(ctx, caseTable, caseKey(c.TenantID, c.ID), encode(c)); err != nil {
		t.Fatal(err)
	}
	key := diagnosisKey(job.TenantID, job.AttemptID)
	if err := s.core.PutRecord(ctx, runtimeDiagnosisTable, key, encode(job)); err != nil {
		t.Fatal(err)
	}
	if err := s.core.PutRecord(ctx, runtimeDiagnosisQueueTable, key, encode(job)); err != nil {
		t.Fatal(err)
	}
	before := Digest(c)
	if err := s.applyRuntimeDiagnosis(ctx, job); err != nil {
		t.Fatal(err)
	}
	a, saved, err := s.RuntimeExperimentAdmission(ctx, c.TenantID, request.AttemptID)
	if err != nil || saved || a.State != "blocked" {
		t.Fatal(a, saved, err)
	}
	current, _ := s.Get(ctx, c.TenantID, c.ID)
	if Digest(current) != before {
		t.Fatal("upgrade rewrote committed history")
	}
	stored, _ := s.RuntimeDiagnosis(ctx, c.TenantID, request.AttemptID)
	if stored.State != "completed" || stored.Admission != nil {
		t.Fatal("legacy admission was backfilled")
	}
}

func TestRuntimeAdmissionRejectsForgedFrozenFactsAndReceipts(t *testing.T) {
	ctx := context.Background()
	s := diagnosisTestStore(t)
	c, request := diagnosisTestCase(t, s, "forged-admission")
	if _, err := s.DiagnoseNextRuntime(ctx, runtimeDiagnoserFunc(diagnosisTestModel), 4); err != nil {
		t.Fatal(err)
	}
	original, _ := s.RuntimeDiagnosis(ctx, c.TenantID, request.AttemptID)
	for _, name := range []string{"ready-flag", "missing-domain", "wrong-case", "wrong-receipt", "wrong-input", "wrong-attempt", "missing-dependencies", "wrong-execution", "wrong-observation", "saved-ready"} {
		t.Run(name, func(t *testing.T) {
			job := privateCopy(original)
			switch name {
			case "ready-flag":
				job.Input.Review.BaselineReady = true
			case "missing-domain":
				job.Input.Review.Domains = job.Input.Review.Domains[:8]
			case "wrong-case":
				job.CaseID = "another-case"
			case "wrong-receipt":
				job.ReceiptID = "invented-receipt"
			case "wrong-input":
				job.InputSHA256 = Digest("invented-input")
			case "wrong-attempt":
				job.Input.Terminal.AttemptID = "another-attempt"
			case "missing-dependencies":
				job.Input.Terminal.Dependencies = nil
			case "wrong-execution":
				job.Input.Terminal.Dependencies.ExecutionID = "another-execution"
			case "wrong-observation":
				job.Input.Observation.Summary = "different observation"
			case "saved-ready":
				job.Admission.State = "ready"
			}
			// Rehash tampered content too: structural checks must reject forged
			// completeness even when simple content checksums are consistent.
			if name != "wrong-input" && name != "wrong-receipt" {
				job.InputSHA256 = Digest(job.Input)
				job.ReceiptID = runtimeDiagnosisReceipt(job.InputSHA256, *job.Inference)
			}
			if err := s.core.PutRecord(ctx, runtimeDiagnosisTable, diagnosisKey(c.TenantID, request.AttemptID), encode(job)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.RuntimeExperimentAdmission(ctx, c.TenantID, request.AttemptID); !errors.Is(err, ErrInvalid) {
				t.Fatal("forged admission accepted", err)
			}
		})
	}
}
