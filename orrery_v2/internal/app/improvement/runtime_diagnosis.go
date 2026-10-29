package improvement

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"orrery_v2/internal/platform/db"
)

const RuntimeDiagnosisProtocol = "orrery-runtime-diagnosis-v1"
const MaxRuntimeDiagnosisInputBytes = 192 << 10
const runtimeDiagnosisQueueTable = "ontology_improvement_runtime_diagnosis_queue"
const runtimeDiagnosisTable = "ontology_improvement_runtime_diagnoses"
const runtimeDiagnosisControlTable = "ontology_improvement_runtime_diagnosis_control"

// Input contains host-captured metadata, never raw prompts, outputs or secrets.
// These observations can expose evidence gaps, not prove business causality.
type RuntimeDiagnosisInput struct {
	Protocol      string                   `json:"protocol"`
	TenantID      string                   `json:"tenant_id"`
	CaseID        string                   `json:"case_id"`
	AttemptID     string                   `json:"attempt_id"`
	Observation   Observation              `json:"observation"`
	Request       RuntimeRequestEvidence   `json:"request"`
	Execution     RuntimeExecutionEvidence `json:"execution"`
	Terminal      RuntimeTerminalEvidence  `json:"terminal"`
	Review        RuntimeReview            `json:"review"`
	Business      []BusinessAssessment     `json:"business_assessments"`
	EvidenceIDs   []string                 `json:"evidence_ids"`
	ManagedRepair *ManagedRepairFeedback   `json:"managed_repair,omitempty"`
}

type RuntimeDiagnosisInference struct {
	Diagnosis    Diagnosis `json:"diagnosis"`
	ProfileID    string    `json:"profile_id"`
	Model        string    `json:"model"`
	PromptSHA256 string    `json:"prompt_sha256"`
}

type RuntimeDiagnoser interface {
	DiagnoseRuntime(context.Context, RuntimeDiagnosisInput) (RuntimeDiagnosisInference, error)
}

type RuntimeDiagnosisRecord struct {
	ID                string                      `json:"id"`
	Protocol          string                      `json:"protocol"`
	TenantID          string                      `json:"tenant_id"`
	CaseID            string                      `json:"case_id"`
	AttemptID         string                      `json:"attempt_id"`
	CaseRevision      int64                       `json:"case_revision"`
	State             string                      `json:"state"`
	RequestCapturedAt time.Time                   `json:"request_captured_at"`
	QueuedAt          time.Time                   `json:"queued_at"`
	StartedAt         time.Time                   `json:"started_at,omitempty"`
	CompletedAt       time.Time                   `json:"completed_at,omitempty"`
	Input             *RuntimeDiagnosisInput      `json:"input,omitempty"`
	InputSHA256       string                      `json:"input_sha256,omitempty"`
	Inference         *RuntimeDiagnosisInference  `json:"inference,omitempty"`
	ReceiptID         string                      `json:"receipt_id,omitempty"`
	Failure           string                      `json:"failure,omitempty"`
	Admission         *RuntimeExperimentAdmission `json:"experiment_admission,omitempty"`
}

func diagnosisKey(tenant, attempt string) string {
	return runtimeEvidenceKey(tenant, attempt, RuntimeDiagnosisProtocol)
}

// Activation survives restarts. Requests predating it never enter this queue,
// including old terminals recovered later from a feedback journal.
func (s *Store) EnableRuntimeDiagnoses(ctx context.Context) (time.Time, error) {
	if s.ready() != nil {
		return time.Time{}, ErrUnavailable
	}
	var activation struct {
		At time.Time `json:"at"`
	}
	err := s.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, runtimeDiagnosisControlTable, RuntimeDiagnosisProtocol); err != nil {
			return err
		}
		raw, exists, err := db.GetRecordTx(ctx, tx, runtimeDiagnosisControlTable, RuntimeDiagnosisProtocol)
		if err != nil {
			return err
		}
		if exists {
			activation, err = decode[struct {
				At time.Time `json:"at"`
			}](raw)
			return err
		}
		activation.At = s.now().UTC()
		return db.PutRecordTx(ctx, tx, runtimeDiagnosisControlTable, RuntimeDiagnosisProtocol, encode(activation))
	})
	if err == nil && activation.At.IsZero() {
		err = ErrInvalid
	}
	return activation.At, err
}

func enqueueRuntimeDiagnosisTx(ctx context.Context, tx db.Tx, c Case, request RuntimeRequestEvidence, execution *RuntimeExecutionEvidence, end RuntimeTerminalEvidence, now time.Time) error {
	if execution == nil || end.Dependencies == nil {
		return blockUndiagnosedCaseTx(ctx, tx, c, "runtime_evidence_incomplete", now)
	}
	if !awaitingDiagnosis(c.State) {
		return nil
	}
	raw, enabled, err := db.GetRecordTx(ctx, tx, runtimeDiagnosisControlTable, RuntimeDiagnosisProtocol)
	if err != nil {
		return err
	}
	if !enabled {
		return blockUndiagnosedCaseTx(ctx, tx, c, "diagnosis_disabled", now)
	}
	activation, err := decode[struct {
		At time.Time `json:"at"`
	}](raw)
	if err != nil || activation.At.IsZero() {
		return ErrInvalid
	}
	if request.CapturedAt.Before(activation.At) {
		return blockUndiagnosedCaseTx(ctx, tx, c, "historical_runtime_evidence", now)
	}
	key := diagnosisKey(c.TenantID, request.AttemptID)
	if _, exists, err := db.GetRecordTx(ctx, tx, runtimeDiagnosisTable, key); err != nil || exists {
		return err
	}
	job := RuntimeDiagnosisRecord{ID: "runtime-diagnosis-job:" + Digest([]string{c.TenantID, request.AttemptID}), Protocol: RuntimeDiagnosisProtocol, TenantID: c.TenantID, CaseID: c.ID, AttemptID: request.AttemptID, CaseRevision: c.Revision, State: "pending", RequestCapturedAt: request.CapturedAt, QueuedAt: now}
	_, err = db.InsertRecordTx(ctx, tx, runtimeDiagnosisQueueTable, key, encode(job))
	return err
}

func (s *Store) runtimeDiagnosisInput(ctx context.Context, job RuntimeDiagnosisRecord) (RuntimeDiagnosisInput, error) {
	var input RuntimeDiagnosisInput
	c, err := s.Get(ctx, job.TenantID, job.CaseID)
	if err != nil {
		return input, err
	}
	if !awaitingDiagnosis(c.State) || c.Revision != job.CaseRevision {
		return input, ErrConflict
	}
	if c.Observation.Trigger == "managed_repair_needs_attention" {
		if job.AttemptID != managedRepairAttempt(c) || len(c.Observation.EvidenceIDs) < 1 {
			return input, ErrInvalid
		}
		f, err := s.GetManagedRepairFeedback(ctx, c.TenantID, c.Observation.EvidenceIDs[0])
		if err != nil {
			return input, err
		}
		if f.TenantID != c.TenantID || f.IncidentID != c.Observation.Task.ID || "agent-run:"+f.RepairRunID != c.Observation.Run.ID || f.Status != "needs_attention" || f.SourceAccessRef != c.Observation.SourceAccessRef {
			return input, ErrInvalid
		}
		input = RuntimeDiagnosisInput{Protocol: RuntimeDiagnosisProtocol, TenantID: c.TenantID, CaseID: c.ID, AttemptID: job.AttemptID, Observation: c.Observation, ManagedRepair: &f, EvidenceIDs: append([]string(nil), c.Observation.EvidenceIDs...)}
		return input, nil
	}
	review, err := s.RuntimeReviewForCase(ctx, c)
	if err != nil {
		return input, err
	}
	if review.AttemptID != job.AttemptID {
		return input, ErrInvalid
	}
	evidence := s.RuntimeEvidence()
	request, err := evidence.GetRequest(ctx, c.TenantID, job.AttemptID)
	if err != nil {
		return input, err
	}
	execution, err := evidence.GetExecution(ctx, c.TenantID, job.AttemptID)
	if err != nil {
		return input, err
	}
	terminal, err := evidence.GetTerminal(ctx, c.TenantID, job.AttemptID)
	if err != nil {
		return input, err
	}
	if terminal.Dependencies == nil || review.ExecutionID != execution.ID || terminal.Dependencies.ExecutionID != execution.ID {
		return input, ErrInvalid
	}
	business, err := evidence.BusinessAssessments(ctx, c.TenantID, job.AttemptID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return input, err
	}
	if len(business) == 0 && s.now().Sub(job.QueuedAt) < time.Minute {
		binding, bindingErr := evidence.GetBusinessBinding(ctx, c.TenantID, job.AttemptID)
		if bindingErr != nil && !errors.Is(bindingErr, ErrNotFound) {
			return input, bindingErr
		}
		if bindingErr == nil && binding.ContractSHA256 != "" {
			return input, ErrUnavailable
		}
	}
	input = RuntimeDiagnosisInput{Protocol: RuntimeDiagnosisProtocol, TenantID: c.TenantID, CaseID: c.ID, AttemptID: job.AttemptID, Observation: c.Observation, Request: request, Execution: execution, Terminal: terminal, Review: review, Business: business,
		EvidenceIDs: []string{request.ID, execution.ID, terminal.ID, review.ID, terminal.Dependencies.ID}}
	for _, a := range business {
		input.EvidenceIDs = append(input.EvidenceIDs, a.ID)
	}
	if !idsOK(append([]string{"runtime-diagnosis-receipt:reserved"}, input.EvidenceIDs...)) {
		return RuntimeDiagnosisInput{}, ErrInvalid
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > MaxRuntimeDiagnosisInputBytes {
		return RuntimeDiagnosisInput{}, ErrInvalid
	}
	return input, nil
}

func validateRuntimeDiagnosis(input RuntimeDiagnosisInput, result RuntimeDiagnosisInference) error {
	d := result.Diagnosis
	if !textOK(result.ProfileID, 256) || !textOK(result.Model, 256) || !hashOK(result.PromptSHA256) || !textOK(d.Summary, 1000) || !idsOK(d.EvidenceIDs) || d.DiagnosisCaseID != "" || len(d.AffectedKinds) < 1 || len(d.AffectedKinds) > len(TargetKinds()) {
		return ErrInvalid
	}
	allowed := map[string]bool{}
	for _, id := range input.EvidenceIDs {
		allowed[id] = true
	}
	for _, id := range d.EvidenceIDs {
		if !allowed[id] {
			return ErrInvalid
		}
	}
	seen := map[TargetKind]bool{}
	for _, kind := range d.AffectedKinds {
		if !kindOK(kind) || seen[kind] {
			return ErrInvalid
		}
		seen[kind] = true
	}
	return nil
}

// Sweep reserves a durable paid-call slot before inference. Running/unknown
// calls are not reissued. Generated answers can finish applying after restart.
func (s *Store) DiagnoseNextRuntime(ctx context.Context, model RuntimeDiagnoser, maxCallsPerHour int) (bool, error) {
	if s.ready() != nil || model == nil {
		return false, ErrUnavailable
	}
	if maxCallsPerHour < 1 || maxCallsPerHour > 100 {
		return false, ErrInvalid
	}
	page, err := s.core.ListRecords(ctx, runtimeDiagnosisQueueTable, "", 1)
	if err != nil || len(page.Items) == 0 {
		return false, err
	}
	entry := page.Items[0]
	job, err := decode[RuntimeDiagnosisRecord](entry.Record)
	if err != nil || job.Protocol != RuntimeDiagnosisProtocol || entry.Key != diagnosisKey(job.TenantID, job.AttemptID) {
		return false, ErrInvalid
	}
	if job.State == "generated" {
		return true, s.applyRuntimeDiagnosis(ctx, job)
	}
	if job.State != "pending" {
		return false, ErrInvalid
	}
	input, inputErr := s.runtimeDiagnosisInput(ctx, job)
	now := s.now().UTC()
	if inputErr != nil && now.Sub(job.RequestCapturedAt) <= 24*time.Hour && !errors.Is(inputErr, ErrInvalid) && !errors.Is(inputErr, ErrConflict) && !errors.Is(inputErr, ErrNotFound) {
		if errors.Is(inputErr, ErrUnavailable) {
			return false, nil
		}
		return false, inputErr
	}
	if inputErr != nil || now.Sub(job.RequestCapturedAt) > 24*time.Hour {
		job.State, job.Failure, job.CompletedAt = "failed", "evidence_unavailable_or_expired", now
		return true, s.finishRuntimeDiagnosis(ctx, entry.Record, job)
	}
	job.Input, job.InputSHA256, job.StartedAt, job.State = &input, Digest(input), now, "running"
	reserved := false
	err = s.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, runtimeDiagnosisQueueTable, entry.Key); err != nil {
			return err
		}
		raw, exists, err := db.GetRecordTx(ctx, tx, runtimeDiagnosisQueueTable, entry.Key)
		if err != nil || !exists {
			return err
		}
		if !reflect.DeepEqual(raw, entry.Record) {
			return nil
		}
		budgetKey := "hour/" + now.Format("2006010215")
		if err := db.LockRecordKeyTx(ctx, tx, runtimeDiagnosisControlTable, budgetKey); err != nil {
			return err
		}
		budget, exists, err := db.GetRecordTx(ctx, tx, runtimeDiagnosisControlTable, budgetKey)
		if err != nil {
			return err
		}
		var usage struct {
			Calls int `json:"calls"`
		}
		if exists {
			usage, err = decode[struct {
				Calls int `json:"calls"`
			}](budget)
			if err != nil || usage.Calls < 0 {
				return ErrInvalid
			}
		}
		if usage.Calls >= maxCallsPerHour {
			return nil
		}
		usage.Calls++
		if err := db.PutRecordTx(ctx, tx, runtimeDiagnosisControlTable, budgetKey, encode(usage)); err != nil {
			return err
		}
		created, err := db.InsertRecordTx(ctx, tx, runtimeDiagnosisTable, entry.Key, encode(job))
		if err != nil {
			return err
		}
		if !created {
			return ErrConflict
		}
		if err := db.DeleteRecordTx(ctx, tx, runtimeDiagnosisQueueTable, entry.Key); err != nil {
			return err
		}
		reserved = true
		return nil
	})
	if err != nil || !reserved {
		return false, err
	}
	modelCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	result, modelErr := model.DiagnoseRuntime(modelCtx, privateCopy(input))
	if modelErr == nil {
		modelErr = modelCtx.Err()
	}
	cancel()
	if modelErr == nil {
		modelErr = validateRuntimeDiagnosis(input, result)
	}
	job.CompletedAt = s.now().UTC()
	job.State, job.Failure = "failed", "model_unavailable"
	if errors.Is(modelErr, ErrInvalid) {
		job.Failure = "invalid_model_diagnosis"
	}
	if modelErr == nil {
		job.State, job.Failure, job.Inference = "generated", "", &result
		job.ReceiptID = "runtime-diagnosis-receipt:" + Digest(struct {
			InputSHA256 string
			Inference   RuntimeDiagnosisInference
		}{job.InputSHA256, result})
	}
	writeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	if err := s.saveRuntimeDiagnosisResult(writeCtx, job); err != nil {
		return true, err
	}
	if job.State == "generated" {
		return true, s.applyRuntimeDiagnosis(writeCtx, job)
	}
	return true, nil
}

func awaitingDiagnosis(state State) bool { return state == Observed || state == PendingDiagnosis }

// Failed inference is an explicit unresolved condition, never ordinary observation.
// Do not steal a concurrently claimed case or retry an uncertain paid call.
func (s *Store) blockRuntimeDiagnosisFailureTx(ctx context.Context, tx db.Tx, job RuntimeDiagnosisRecord) error {
	key := caseKey(job.TenantID, job.CaseID)
	if err := db.LockRecordKeyTx(ctx, tx, caseTable, key); err != nil {
		return err
	}
	raw, exists, err := db.GetRecordTx(ctx, tx, caseTable, key)
	if err != nil || !exists {
		return err
	}
	c, err := decode[Case](raw)
	if err != nil {
		return err
	}
	ownedClaim := c.Revision == job.CaseRevision+1 && c.Lease != nil && c.Lease.Owner == job.ID
	unchanged := c.Revision == job.CaseRevision && (c.Lease == nil || !c.Lease.ExpiresAt.After(s.now()))
	if c.State != PendingDiagnosis || !ownedClaim && !unchanged {
		return nil
	}
	if ownedClaim {
		c.Lease = nil
	}
	return blockUndiagnosedCaseTx(ctx, tx, c, job.Failure, s.now().UTC())
}

func (s *Store) saveRuntimeDiagnosisResult(ctx context.Context, job RuntimeDiagnosisRecord) error {
	key := diagnosisKey(job.TenantID, job.AttemptID)
	return s.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, runtimeDiagnosisTable, key); err != nil {
			return err
		}
		raw, exists, err := db.GetRecordTx(ctx, tx, runtimeDiagnosisTable, key)
		if err != nil {
			return err
		}
		old, err := decode[RuntimeDiagnosisRecord](raw)
		if !exists || err != nil || old.State != "running" || old.InputSHA256 != job.InputSHA256 {
			return ErrConflict
		}
		if err := db.PutRecordTx(ctx, tx, runtimeDiagnosisTable, key, encode(job)); err != nil {
			return err
		}
		if job.State == "generated" {
			_, err = db.InsertRecordTx(ctx, tx, runtimeDiagnosisQueueTable, key, encode(job))
		} else if job.State == "failed" {
			err = s.blockRuntimeDiagnosisFailureTx(ctx, tx, job)
		}
		return err
	})
}

func (s *Store) finishRuntimeDiagnosis(ctx context.Context, expected db.Record, job RuntimeDiagnosisRecord) error {
	key := diagnosisKey(job.TenantID, job.AttemptID)
	return s.core.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, runtimeDiagnosisQueueTable, key); err != nil {
			return err
		}
		raw, exists, err := db.GetRecordTx(ctx, tx, runtimeDiagnosisQueueTable, key)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if !reflect.DeepEqual(raw, expected) {
			return ErrConflict
		}
		if err := db.PutRecordTx(ctx, tx, runtimeDiagnosisTable, key, encode(job)); err != nil {
			return err
		}
		if job.State == "failed" {
			if err := s.blockRuntimeDiagnosisFailureTx(ctx, tx, job); err != nil {
				return err
			}
		}
		return db.DeleteRecordTx(ctx, tx, runtimeDiagnosisQueueTable, key)
	})
}

func (s *Store) applyRuntimeDiagnosis(ctx context.Context, job RuntimeDiagnosisRecord) error {
	expected := encode(job)
	if job.Input == nil || job.Inference == nil || job.InputSHA256 != Digest(job.Input) || validateRuntimeDiagnosis(*job.Input, *job.Inference) != nil || job.Input.TenantID != job.TenantID || job.Input.CaseID != job.CaseID || job.Input.AttemptID != job.AttemptID || job.ReceiptID != "runtime-diagnosis-receipt:"+Digest(struct {
		InputSHA256 string
		Inference   RuntimeDiagnosisInference
	}{job.InputSHA256, *job.Inference}) {
		job.State, job.Failure = "failed", "invalid_saved_diagnosis"
		return s.finishRuntimeDiagnosis(ctx, expected, job)
	}
	diagnosis := privateCopy(job.Inference.Diagnosis)
	diagnosis.EvidenceIDs = []string{job.ReceiptID}
	if job.Input.ManagedRepair == nil {
		admission, admissionErr := buildRuntimeExperimentAdmission(job)
		if admissionErr != nil || (job.Admission != nil && !reflect.DeepEqual(*job.Admission, admission)) {
			job.State, job.Failure = "failed", "invalid_experiment_admission"
			return s.finishRuntimeDiagnosis(ctx, expected, job)
		}
		job.Admission = &admission
		diagnosis.EvidenceIDs = append(diagnosis.EvidenceIDs, admission.ID)
	} else if job.Admission != nil {
		// Repair reviews have no captured native runtime baseline. They retain
		// their diagnosis receipt without claiming a runtime experiment admission.
		job.State, job.Failure = "failed", "invalid_experiment_admission"
		return s.finishRuntimeDiagnosis(ctx, expected, job)
	}
	diagnosis.EvidenceIDs = append(diagnosis.EvidenceIDs, job.Input.EvidenceIDs...)
	if len(diagnosis.EvidenceIDs) > 64 {
		// The immutable input/admission retains the full inventory.
		diagnosis.EvidenceIDs = diagnosis.EvidenceIDs[:64]
	}
	c, err := s.Get(ctx, job.TenantID, job.CaseID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(c.Observation, job.Input.Observation) {
		job.State, job.Failure = "failed", "case_observation_changed"
		return s.finishRuntimeDiagnosis(ctx, expected, job)
	}
	target, reason := Diagnosed, ""
	if job.Input.ManagedRepair != nil {
		// A model review cannot clear missing repair evidence or approval.
		target, reason = Blocked, "managed_repair_needs_attention"
	}
	legacy := privateCopy(job.Inference.Diagnosis)
	legacy.EvidenceIDs = append([]string{job.ReceiptID}, job.Input.EvidenceIDs...)
	if c.State == target && c.Diagnosis != nil && reflect.DeepEqual(*c.Diagnosis, legacy) {
		// Recovery across upgrade must not rewrite an already committed case.
		job.State, job.Admission = "completed", nil
		return s.finishRuntimeDiagnosis(ctx, expected, job)
	}
	if c.State == target && c.Diagnosis != nil && reflect.DeepEqual(*c.Diagnosis, diagnosis) {
		job.State = "completed"
		return s.finishRuntimeDiagnosis(ctx, expected, job)
	}
	if awaitingDiagnosis(c.State) && c.Revision == job.CaseRevision {
		c, err = s.Claim(ctx, c.TenantID, c.ID, job.ID, c.Revision, 5*time.Minute)
		if err != nil {
			return err
		}
	}
	if !awaitingDiagnosis(c.State) || c.Revision != job.CaseRevision+1 || c.Lease == nil || c.Lease.Owner != job.ID || !c.Lease.ExpiresAt.After(s.now()) {
		job.State, job.Failure = "failed", "case_changed_or_lease_expired"
		return s.finishRuntimeDiagnosis(ctx, expected, job)
	}
	_, err = s.Advance(ctx, Transition{ID: job.ID + ":apply", TenantID: c.TenantID, CaseID: c.ID, ExpectedRevision: c.Revision, LeaseToken: c.Lease.Token, Actor: ObjectRef{Kind: "system", ID: job.ID}, To: target, Diagnosis: &diagnosis, Reason: reason}, nil)
	if err != nil {
		return err
	}
	job.State = "completed"
	return s.finishRuntimeDiagnosis(ctx, expected, job)
}

func (s *Store) RuntimeDiagnosis(ctx context.Context, tenant, attempt string) (RuntimeDiagnosisRecord, error) {
	if s.ready() != nil {
		return RuntimeDiagnosisRecord{}, ErrUnavailable
	}
	if !validScope(tenant, attempt) {
		return RuntimeDiagnosisRecord{}, ErrInvalid
	}
	raw, exists, err := s.core.GetRecord(ctx, runtimeDiagnosisTable, diagnosisKey(tenant, attempt))
	if err != nil {
		return RuntimeDiagnosisRecord{}, err
	}
	if !exists {
		return RuntimeDiagnosisRecord{}, ErrNotFound
	}
	job, err := decode[RuntimeDiagnosisRecord](raw)
	if err == nil && (job.TenantID != tenant || job.AttemptID != attempt) {
		err = ErrInvalid
	}
	return job, err
}
