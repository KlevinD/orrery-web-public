package improvement

import (
	"context"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

const repairFeedbackTable = "ontology_improvement_repair_feedback"
const repairCaseTable = "ontology_improvement_repair_cases"

// ManagedRepairFeedback is supplied only after the host accepts a current repair
// receipt. It records that verdict, not an independent quality or causal review.
// SourceEvidence contains references; diagnostic prose and public replies stay
// with their original owners.
type ManagedRepairFeedback struct {
	TenantID        string                  `json:"tenant_id"`
	IncidentID      string                  `json:"incident_id"`
	OccurrenceCount int64                   `json:"occurrence_count"`
	LastSeenNS      int64                   `json:"last_seen_ns"`
	JobID           string                  `json:"job_id"`
	BatchID         string                  `json:"batch_id"`
	RepairRunID     string                  `json:"repair_run_id"`
	Status          string                  `json:"status"`
	Verified        bool                    `json:"verified"`
	SourceEvidence  []string                `json:"source_evidence"`
	OccurredAt      time.Time               `json:"occurred_at"`
	SourceAccessRef graphprotocol.AccessRef `json:"source_access_ref"`
	// Private, redacted conclusion from the original repair Agent. This is not
	// independent verification and is never copied into graph display text.
	Summary string `json:"summary,omitempty"`
}

func validateRepairFeedback(f ManagedRepairFeedback) error {
	if !textOK(f.TenantID, 256) || !textOK(f.IncidentID, 256) || !textOK(f.JobID, 256) || !textOK(f.BatchID, 256) ||
		!textOK(f.RepairRunID, 256) || f.OccurrenceCount < 1 || f.LastSeenNS < 1 || f.OccurredAt.IsZero() || len(f.SourceEvidence) > 20 ||
		f.SourceAccessRef.TenantID != f.TenantID || f.SourceAccessRef.Kind != "failure_incident" || f.SourceAccessRef.ID != f.IncidentID || f.Summary != "" && !textOK(f.Summary, 16384) {
		return ErrInvalid
	}
	for _, ref := range f.SourceEvidence {
		// The capture inbox bounds references to 512 Unicode code points.
		if !textOK(ref, 2048) {
			return ErrInvalid
		}
	}
	switch f.Status {
	case "resolved":
		if !f.Verified || len(f.SourceEvidence) == 0 {
			return ErrInvalid
		}
	case "technical_recovered", "needs_attention":
		if f.Verified {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func repairFeedbackID(f ManagedRepairFeedback) string { return "managed-repair-feedback:" + Digest(f) }

// Occurrence counters and receipt refresh clocks are not new repair attempts.
// A new run or verdict gets a new case, while every receipt stays immutable.
func repairCaseKey(f ManagedRepairFeedback) string {
	return caseKey(f.TenantID, Digest([]string{f.TenantID, f.IncidentID, f.JobID, f.BatchID, f.RepairRunID, f.Status}))
}

func repairFeedbackObservation(f ManagedRepairFeedback) Observation {
	summary := "托管修复仍需处理，保留执行与验收证据供改进分析"
	if f.Status == "resolved" {
		summary = "宿主已验证本次故障恢复，改进归因与复用仍待独立评估"
	} else if f.Status == "technical_recovered" {
		summary = "技术修复已验证，原业务验收仍未完成"
	}
	return Observation{
		ID: repairFeedbackID(f), TenantID: f.TenantID,
		Task:    ObjectRef{Kind: "failure_incident", ID: f.IncidentID},
		Run:     ObjectRef{Kind: "agent_run", ID: "agent-run:" + f.RepairRunID},
		Trigger: "managed_repair_" + f.Status, Summary: summary,
		EvidenceIDs: []string{repairFeedbackID(f), f.RepairRunID},
		OccurredAt:  f.OccurredAt, SourceAccessRef: f.SourceAccessRef,
	}
}

// ObserveManagedRepair atomically retains immutable acceptance evidence, a case
// and its existing projection outbox. Replays do not create another case or
// advance it to a diagnosis, experiment, activation or business-success verdict.
func (s *Store) ObserveManagedRepair(ctx context.Context, feedback ManagedRepairFeedback) (Case, error) {
	if err := s.ready(); err != nil {
		return Case{}, err
	}
	f := privateCopy(feedback)
	f.OccurredAt = f.OccurredAt.UTC()
	if err := validateRepairFeedback(f); err != nil {
		return Case{}, err
	}
	var result Case
	err := s.core.WithTx(ctx, func(tx db.Tx) error {
		key := caseKey(f.TenantID, repairFeedbackID(f))
		created, err := db.InsertRecordTx(ctx, tx, repairFeedbackTable, key, encode(f))
		if err != nil {
			return err
		}
		if !created {
			record, exists, err := db.GetRecordTx(ctx, tx, repairFeedbackTable, key)
			if err != nil {
				return err
			}
			if !exists || record["sha256"] != Digest(f) || record["payload"] != encode(f)["payload"] {
				return ErrConflict
			}
		}
		indexKey := repairCaseKey(f)
		if err := db.LockRecordKeyTx(ctx, tx, repairCaseTable, indexKey); err != nil {
			return err
		}
		index, exists, err := db.GetRecordTx(ctx, tx, repairCaseTable, indexKey)
		if err != nil {
			return err
		}
		if exists {
			binding, err := decode[repairCaseBinding](index)
			if err != nil {
				return err
			}
			raw, found, err := db.GetRecordTx(ctx, tx, caseTable, caseKey(f.TenantID, binding.CaseID))
			if err != nil {
				return err
			}
			if !found {
				return ErrConflict
			}
			result, err = decode[Case](raw)
			if err == nil && (result.TenantID != f.TenantID || result.Observation.Task.ID != f.IncidentID || result.Observation.Run.ID != "agent-run:"+f.RepairRunID || result.Observation.Trigger != "managed_repair_"+f.Status || result.Observation.SourceAccessRef != f.SourceAccessRef) {
				return ErrConflict
			}
			return err
		}
		result, err = s.observeTx(ctx, tx, repairFeedbackObservation(f), s.now().UTC())
		if err != nil {
			return err
		}
		if f.Status == "needs_attention" && result.State == Observed {
			if result.Lease != nil && result.Lease.ExpiresAt.After(s.now()) {
				return ErrLease
			}
			result.State, result.Lease, result.Reason = PendingDiagnosis, nil, ""
			result.Revision++
			result.UpdatedAt = s.now().UTC()
			if err := persistCase(ctx, tx, result, true); err != nil {
				return err
			}
		}
		if err := db.PutRecordTx(ctx, tx, repairCaseTable, indexKey, encode(repairCaseBinding{CaseID: result.ID})); err != nil {
			return err
		}
		if result.State == PendingDiagnosis {
			if err := enqueueManagedRepairDiagnosisTx(ctx, tx, result, f, s.now().UTC()); err != nil {
				return err
			}
			raw, _, err := db.GetRecordTx(ctx, tx, caseTable, caseKey(f.TenantID, result.ID))
			if err != nil {
				return err
			}
			result, err = decode[Case](raw)
			return err
		}
		return nil
	})
	return result, err
}

type repairCaseBinding struct {
	CaseID string `json:"case_id"`
}

func managedRepairAttempt(c Case) string {
	return "managed-repair-diagnosis:" + Digest([]string{c.TenantID, c.ID})
}

func enqueueManagedRepairDiagnosisTx(ctx context.Context, tx db.Tx, c Case, f ManagedRepairFeedback, now time.Time) error {
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
	// Read current accepted receipts only; never rerun old business or repairs.
	if f.OccurredAt.Before(activation.At) || now.Sub(f.OccurredAt) > 24*time.Hour {
		return blockUndiagnosedCaseTx(ctx, tx, c, "historical_repair_evidence", now)
	}
	attempt := managedRepairAttempt(c)
	job := RuntimeDiagnosisRecord{ID: "runtime-diagnosis-job:" + Digest([]string{c.TenantID, attempt}), Protocol: RuntimeDiagnosisProtocol, TenantID: c.TenantID, CaseID: c.ID, AttemptID: attempt, CaseRevision: c.Revision, State: "pending", RequestCapturedAt: now, QueuedAt: now}
	_, err = db.InsertRecordTx(ctx, tx, runtimeDiagnosisQueueTable, diagnosisKey(c.TenantID, attempt), encode(job))
	return err
}

func blockUndiagnosedCaseTx(ctx context.Context, tx db.Tx, c Case, reason string, now time.Time) error {
	if c.State != PendingDiagnosis {
		return nil
	}
	c.State, c.Reason, c.UpdatedAt = Blocked, reason, now
	c.Revision++
	return persistCase(ctx, tx, c, true)
}

func (s *Store) GetManagedRepairFeedback(ctx context.Context, tenant, id string) (ManagedRepairFeedback, error) {
	if err := s.ready(); err != nil {
		return ManagedRepairFeedback{}, err
	}
	if !textOK(tenant, 256) || !strings.HasPrefix(id, "managed-repair-feedback:") || !textOK(id, 256) {
		return ManagedRepairFeedback{}, ErrInvalid
	}
	record, exists, err := s.core.GetRecord(ctx, repairFeedbackTable, caseKey(tenant, id))
	if err != nil {
		return ManagedRepairFeedback{}, err
	}
	if !exists {
		return ManagedRepairFeedback{}, ErrNotFound
	}
	f, err := decode[ManagedRepairFeedback](record)
	if err == nil && (f.TenantID != tenant || repairFeedbackID(f) != id || validateRepairFeedback(f) != nil) {
		err = ErrInvalid
	}
	return f, err
}
