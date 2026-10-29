package reliability

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type diagnosisCommandRecord struct {
	DiagnosisCaseID string
	Operation       string
	Decision        DiagnosisDecision
	Reason          string
	Actor           ActorRef
}

func (r *MemoryRepository) CreateDiagnosisCase(ctx context.Context, commandID string, candidate DiagnosisCase) (DiagnosisCase, bool, error) {
	if r == nil {
		return DiagnosisCase{}, false, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := validateDiagnosisCase(candidate); err != nil {
		return DiagnosisCase{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return DiagnosisCase{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	commandKey := reliabilityKey(candidate.TenantID, commandID)
	if recorded, ok := r.diagnosisCommands[commandKey]; ok {
		if recorded.Operation != "create" || recorded.DiagnosisCaseID != candidate.ID {
			return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis command id was reused", ErrConflict)
		}
		existing, exists := r.diagnoses[reliabilityKey(candidate.TenantID, recorded.DiagnosisCaseID)]
		if !exists || !sameDiagnosisProposal(existing, candidate) {
			return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis command id was reused with different data", ErrConflict)
		}
		return cloneDiagnosisCase(existing), false, nil
	}
	incident, ok := r.incidents[reliabilityKey(candidate.TenantID, candidate.IncidentID)]
	if !ok {
		return DiagnosisCase{}, false, fmt.Errorf("%w: failure incident %s", ErrNotFound, candidate.IncidentID)
	}
	if incident.Version != candidate.IncidentVersion {
		return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis incident version changed", ErrConflict)
	}
	key := reliabilityKey(candidate.TenantID, candidate.ID)
	if _, exists := r.diagnoses[key]; exists {
		return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis case already exists", ErrConflict)
	}
	candidate.Version = 1
	r.diagnoses[key] = cloneDiagnosisCase(candidate)
	r.diagnosisCommands[commandKey] = diagnosisCommandRecord{
		DiagnosisCaseID: candidate.ID, Operation: "create", Actor: candidate.ProposedBy,
	}
	return cloneDiagnosisCase(candidate), true, nil
}

func (r *MemoryRepository) GetDiagnosisCase(ctx context.Context, tenantID, diagnosisID string) (DiagnosisCase, error) {
	if r == nil {
		return DiagnosisCase{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return DiagnosisCase{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	diagnosis, ok := r.diagnoses[reliabilityKey(tenantID, diagnosisID)]
	if !ok {
		return DiagnosisCase{}, fmt.Errorf("%w: diagnosis case %s", ErrNotFound, diagnosisID)
	}
	return cloneDiagnosisCase(diagnosis), nil
}

func (r *MemoryRepository) ListDiagnosisCasesByIncident(ctx context.Context, tenantID, incidentID string, limit int) ([]DiagnosisCase, error) {
	if r == nil || tenantID == "" || incidentID == "" {
		return nil, fmt.Errorf("%w: repository, tenant_id, and incident_id are required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]DiagnosisCase, 0, limit)
	for _, diagnosis := range r.diagnoses {
		if diagnosis.TenantID == tenantID && diagnosis.IncidentID == incidentID {
			items = append(items, cloneDiagnosisCase(diagnosis))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r *MemoryRepository) DecideDiagnosisCase(ctx context.Context, command DiagnosisDecisionCommand, updatedAt time.Time) (DiagnosisCase, bool, error) {
	if r == nil {
		return DiagnosisCase{}, false, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := validateDiagnosisDecisionCommand(command); err != nil {
		return DiagnosisCase{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return DiagnosisCase{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	commandKey := reliabilityKey(command.TenantID, command.ID)
	if recorded, ok := r.diagnosisCommands[commandKey]; ok {
		if recorded.Operation != "decision" || recorded.DiagnosisCaseID != command.DiagnosisCaseID || recorded.Decision != command.Decision ||
			recorded.Reason != command.Reason || recorded.Actor != command.Actor {
			return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis command id was reused with different data", ErrConflict)
		}
		existing, exists := r.diagnoses[reliabilityKey(command.TenantID, recorded.DiagnosisCaseID)]
		if !exists {
			return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis command references missing case", ErrConflict)
		}
		return cloneDiagnosisCase(existing), false, nil
	}
	key := reliabilityKey(command.TenantID, command.DiagnosisCaseID)
	diagnosis, ok := r.diagnoses[key]
	if !ok {
		return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis case %s", ErrNotFound, command.DiagnosisCaseID)
	}
	if diagnosis.Status != DiagnosisCaseStatusProposed {
		return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis case is already decided", ErrConflict)
	}
	if command.Decision == DiagnosisDecisionConfirm {
		incident, exists := r.incidents[reliabilityKey(command.TenantID, diagnosis.IncidentID)]
		if !exists {
			return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis incident is missing", ErrConflict)
		}
		if incident.Version != diagnosis.IncidentVersion {
			return DiagnosisCase{}, false, fmt.Errorf("%w: diagnosis no longer matches the current incident version", ErrConflict)
		}
		diagnosis.Status = DiagnosisCaseStatusConfirmed
	} else {
		diagnosis.Status = DiagnosisCaseStatusRejected
	}
	diagnosis.ReviewedBy = command.Actor
	diagnosis.ReviewReason = command.Reason
	diagnosis.Version++
	diagnosis.UpdatedAt = updatedAt.UTC()
	r.diagnoses[key] = cloneDiagnosisCase(diagnosis)
	r.diagnosisCommands[commandKey] = diagnosisCommandRecord{
		DiagnosisCaseID: command.DiagnosisCaseID, Operation: "decision", Decision: command.Decision,
		Reason: command.Reason, Actor: command.Actor,
	}
	return cloneDiagnosisCase(diagnosis), true, nil
}

func cloneDiagnosisCase(diagnosis DiagnosisCase) DiagnosisCase {
	diagnosis.EvidenceIDs = append([]string(nil), diagnosis.EvidenceIDs...)
	return diagnosis
}

func sameDiagnosisProposal(existing, candidate DiagnosisCase) bool {
	if existing.ID != candidate.ID || existing.TenantID != candidate.TenantID || existing.IncidentID != candidate.IncidentID ||
		existing.RootCauseCategory != candidate.RootCauseCategory || existing.RedactedSummary != candidate.RedactedSummary ||
		existing.Confidence != candidate.Confidence || existing.ProposedBy != candidate.ProposedBy || len(existing.EvidenceIDs) != len(candidate.EvidenceIDs) {
		return false
	}
	for index := range existing.EvidenceIDs {
		if existing.EvidenceIDs[index] != candidate.EvidenceIDs[index] {
			return false
		}
	}
	return true
}
