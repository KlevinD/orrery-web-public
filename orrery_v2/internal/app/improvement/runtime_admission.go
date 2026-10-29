package improvement

import (
	"context"
	"reflect"
	"time"
)

const RuntimeExperimentAdmissionProtocol = "orrery-runtime-experiment-admission-v1"

// Admission is a host assessment of a frozen diagnosis, not model permission
// to run an experiment. V1 has no trusted native baseline/sandbox integration;
// it records that boundary explicitly and never grants execution authority.
type RuntimeExperimentAdmission struct {
	ID                 string                  `json:"id"`
	Protocol           string                  `json:"protocol"`
	TenantID           string                  `json:"tenant_id"`
	CaseID             string                  `json:"case_id"`
	AttemptID          string                  `json:"attempt_id"`
	DiagnosisReceiptID string                  `json:"diagnosis_receipt_id"`
	InputSHA256        string                  `json:"input_sha256"`
	State              string                  `json:"state"`
	Domains            []RuntimeDomainCoverage `json:"domains"`
	AffectedKinds      []TargetKind            `json:"affected_kinds"`
	EvidenceIDs        []string                `json:"evidence_ids"`
	Blockers           []string                `json:"blockers"`
	AssessedAt         time.Time               `json:"assessed_at"`
}

func runtimeDiagnosisReceipt(inputHash string, result RuntimeDiagnosisInference) string {
	return "runtime-diagnosis-receipt:" + Digest(struct {
		InputSHA256 string
		Inference   RuntimeDiagnosisInference
	}{inputHash, result})
}

func buildRuntimeExperimentAdmission(job RuntimeDiagnosisRecord) (RuntimeExperimentAdmission, error) {
	if job.Protocol != RuntimeDiagnosisProtocol || job.Input == nil || job.Inference == nil || job.InputSHA256 != Digest(job.Input) ||
		job.ReceiptID != runtimeDiagnosisReceipt(job.InputSHA256, *job.Inference) || validateRuntimeDiagnosis(*job.Input, *job.Inference) != nil {
		return RuntimeExperimentAdmission{}, ErrInvalid
	}
	i := job.Input
	if i.Protocol != RuntimeDiagnosisProtocol || i.TenantID != job.TenantID || i.CaseID != job.CaseID || i.AttemptID != job.AttemptID || i.Request.Scope.TenantID != i.TenantID || i.Observation.TenantID != i.TenantID ||
		i.Request.AttemptID != job.AttemptID || i.Execution.AttemptID != job.AttemptID || i.Terminal.AttemptID != job.AttemptID ||
		i.Terminal.Dependencies == nil || i.Terminal.Dependencies.ExecutionID != i.Execution.ID {
		return RuntimeExperimentAdmission{}, ErrInvalid
	}
	// Reconstruct from immutable host facts. A saved coverage/ready flag, or a
	// model's assertion that an experiment is safe, cannot supply missing proof.
	review, err := BuildRuntimeReviewWithExecution(i.Request, &i.Terminal, &i.Execution)
	if err != nil || !reflect.DeepEqual(review, i.Review) {
		return RuntimeExperimentAdmission{}, ErrInvalid
	}
	observation, err := capturedRuntimeObservation(i.Request, i.Terminal)
	if err != nil || !reflect.DeepEqual(observation, i.Observation) || caseID(observation) != i.CaseID {
		return RuntimeExperimentAdmission{}, ErrInvalid
	}
	a := RuntimeExperimentAdmission{
		Protocol: RuntimeExperimentAdmissionProtocol, TenantID: i.TenantID, CaseID: i.CaseID, AttemptID: i.AttemptID,
		DiagnosisReceiptID: job.ReceiptID, InputSHA256: job.InputSHA256, State: "blocked",
		Domains: privateCopy(review.Domains), AffectedKinds: privateCopy(job.Inference.Diagnosis.AffectedKinds),
		EvidenceIDs: append([]string{job.ReceiptID}, i.EvidenceIDs...), AssessedAt: job.CompletedAt,
		Blockers: append(append([]string(nil), review.Blockers...),
			"trusted_runtime_baseline_verifier_unavailable", "candidate_scope_unadmitted", "isolated_task_dataset_unadmitted", "sandbox_policy_unadmitted"),
	}
	if a.AssessedAt.IsZero() || len(a.Domains) != len(TargetKinds()) || !idsOK(a.EvidenceIDs) {
		return RuntimeExperimentAdmission{}, ErrInvalid
	}
	if i.Terminal.Dependencies.Dropped > 0 {
		a.Blockers = append(a.Blockers, "dispatch_dependencies_truncated")
	}
	if i.Terminal.Dependencies.Invalid > 0 {
		a.Blockers = append(a.Blockers, "dispatch_dependencies_invalid")
	}
	if !i.Terminal.Dependencies.RunnerReturned {
		a.Blockers = append(a.Blockers, "runtime_runner_not_returned")
	}
	seen := map[TargetKind]bool{}
	for _, d := range a.Domains {
		if !kindOK(d.Kind) || seen[d.Kind] {
			return RuntimeExperimentAdmission{}, ErrInvalid
		}
		seen[d.Kind] = true
	}
	a.ID = "runtime-experiment-admission:" + Digest(a)
	return a, nil
}

// Read-only compatibility for diagnoses completed before admission capture.
// Derived receipts are identified as unpersisted and never rewrite history.
func (s *Store) RuntimeExperimentAdmission(ctx context.Context, tenant, attempt string) (RuntimeExperimentAdmission, bool, error) {
	job, err := s.RuntimeDiagnosis(ctx, tenant, attempt)
	if err != nil {
		return RuntimeExperimentAdmission{}, false, err
	}
	if job.State != "completed" {
		return RuntimeExperimentAdmission{}, false, ErrUnavailable
	}
	if job.Input != nil && job.Input.ManagedRepair != nil {
		return RuntimeExperimentAdmission{}, false, ErrUnavailable
	}
	a, err := buildRuntimeExperimentAdmission(job)
	if err != nil {
		return RuntimeExperimentAdmission{}, false, err
	}
	if job.Admission != nil && !reflect.DeepEqual(*job.Admission, a) {
		return RuntimeExperimentAdmission{}, false, ErrInvalid
	}
	return a, job.Admission != nil, nil
}
