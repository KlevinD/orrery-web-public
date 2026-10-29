package reliability

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryRepository struct {
	mu                sync.RWMutex
	incidents         map[string]FailureIncident
	byFingerprint     map[string]string
	observations      map[string]string
	repairTargets     map[string]RepairTarget
	diagnoses         map[string]DiagnosisCase
	diagnosisCommands map[string]diagnosisCommandRecord
	rules             map[string]RepairRule
	ruleCommands      map[string]string
	repairOutcomes    map[string]RepairOutcome
	preflights        map[string]RepairPreflight
	executions        map[string]RepairExecution
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		incidents:         make(map[string]FailureIncident),
		byFingerprint:     make(map[string]string),
		observations:      make(map[string]string),
		repairTargets:     make(map[string]RepairTarget),
		diagnoses:         make(map[string]DiagnosisCase),
		diagnosisCommands: make(map[string]diagnosisCommandRecord),
		rules:             make(map[string]RepairRule),
		ruleCommands:      make(map[string]string),
		repairOutcomes:    make(map[string]RepairOutcome),
		preflights:        make(map[string]RepairPreflight),
		executions:        make(map[string]RepairExecution),
	}
}

func (r *MemoryRepository) ObserveFailure(_ context.Context, observation FailureObservation, candidate FailureIncident) (FailureIncident, bool, error) {
	if r == nil {
		return FailureIncident{}, false, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	observationKey := reliabilityKey(observation.TenantID, observation.ID)
	if incidentID, exists := r.observations[observationKey]; exists {
		if incidentID != candidate.ID {
			return FailureIncident{}, false, fmt.Errorf("%w: observation id was reused for a different failure", ErrConflict)
		}
		incident, ok := r.incidents[reliabilityKey(observation.TenantID, incidentID)]
		if !ok {
			return FailureIncident{}, false, fmt.Errorf("%w: observation references missing incident", ErrConflict)
		}
		if err := r.validateObservationTargetReplay(observation, incidentID); err != nil {
			return FailureIncident{}, false, err
		}
		return cloneIncident(incident), false, nil
	}

	fingerprintKey := reliabilityKey(candidate.TenantID, candidate.Fingerprint)
	if incidentID, exists := r.byFingerprint[fingerprintKey]; exists {
		incidentKey := reliabilityKey(candidate.TenantID, incidentID)
		incident := r.incidents[incidentKey]
		isCurrent := !candidate.LastSeenAt.Before(incident.LastSeenAt)
		if candidate.FirstSeenAt.Before(incident.FirstSeenAt) {
			incident.FirstSeenAt = candidate.FirstSeenAt
		}
		if isCurrent {
			incident.LastSeenAt = candidate.LastSeenAt
		}
		incident.UpdatedAt = candidate.UpdatedAt
		incident.OccurrenceCount++
		incident.Version++
		if isCurrent && candidate.RedactedSummary != "" {
			incident.RedactedSummary = candidate.RedactedSummary
		}
		if severityRank(candidate.Severity) > severityRank(incident.Severity) {
			incident.Severity = candidate.Severity
		}
		incident.Retryable = incident.Retryable || candidate.Retryable
		if isCurrent && incident.Status == IncidentStatusResolved {
			incident.Status = IncidentStatusOpen
		}
		r.incidents[incidentKey] = incident
		r.observations[observationKey] = incident.ID
		r.saveObservationTarget(observation, incident.ID)
		return cloneIncident(incident), true, nil
	}

	r.incidents[reliabilityKey(candidate.TenantID, candidate.ID)] = candidate
	r.byFingerprint[fingerprintKey] = candidate.ID
	r.observations[observationKey] = candidate.ID
	r.saveObservationTarget(observation, candidate.ID)
	return cloneIncident(candidate), true, nil
}

func (r *MemoryRepository) saveObservationTarget(observation FailureObservation, incidentID string) {
	if observation.RepairTarget == nil {
		return
	}
	target := cloneRepairTarget(*observation.RepairTarget)
	target.TenantID = observation.TenantID
	target.ObservationID = observation.ID
	target.IncidentID = incidentID
	target.OccurredAt = observation.OccurredAt
	r.repairTargets[reliabilityKey(observation.TenantID, observation.ID)] = target
}

func (r *MemoryRepository) validateObservationTargetReplay(observation FailureObservation, incidentID string) error {
	if observation.RepairTarget == nil {
		return nil
	}
	existing, ok := r.repairTargets[reliabilityKey(observation.TenantID, observation.ID)]
	if !ok {
		return fmt.Errorf("%w: observation target is missing", ErrConflict)
	}
	candidate := cloneRepairTarget(*observation.RepairTarget)
	candidate.TenantID, candidate.ObservationID, candidate.IncidentID, candidate.OccurredAt = observation.TenantID, observation.ID, incidentID, observation.OccurredAt
	if !sameRepairTarget(existing, candidate) {
		return fmt.Errorf("%w: observation id was reused with a different repair target", ErrConflict)
	}
	return nil
}

func (r *MemoryRepository) GetLatestRepairTarget(_ context.Context, tenantID, incidentID string) (RepairTarget, error) {
	if r == nil {
		return RepairTarget{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest RepairTarget
	found := false
	for _, target := range r.repairTargets {
		if target.TenantID != tenantID || target.IncidentID != incidentID {
			continue
		}
		if !found || target.OccurredAt.After(latest.OccurredAt) || (target.OccurredAt.Equal(latest.OccurredAt) && target.ObservationID > latest.ObservationID) {
			latest, found = target, true
		}
	}
	if !found {
		return RepairTarget{}, fmt.Errorf("%w: repair target for failure incident %s", ErrNotFound, incidentID)
	}
	return cloneRepairTarget(latest), nil
}

func (r *MemoryRepository) GetIncident(_ context.Context, tenantID, incidentID string) (FailureIncident, error) {
	if r == nil {
		return FailureIncident{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	incident, ok := r.incidents[reliabilityKey(tenantID, incidentID)]
	if !ok {
		return FailureIncident{}, fmt.Errorf("%w: failure incident %s", ErrNotFound, incidentID)
	}
	return cloneIncident(incident), nil
}

func (r *MemoryRepository) UpdateIncidentStatus(_ context.Context, tenantID, incidentID string, expectedVersion int64, status IncidentStatus, updatedAt time.Time) (FailureIncident, error) {
	if r == nil {
		return FailureIncident{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := reliabilityKey(tenantID, incidentID)
	incident, ok := r.incidents[key]
	if !ok {
		return FailureIncident{}, fmt.Errorf("%w: failure incident %s", ErrNotFound, incidentID)
	}
	if incident.Version != expectedVersion {
		return FailureIncident{}, fmt.Errorf("%w: failure incident version changed", ErrConflict)
	}
	incident.Status = status
	incident.Version++
	incident.UpdatedAt = updatedAt.UTC()
	r.incidents[key] = incident
	return cloneIncident(incident), nil
}

func (r *MemoryRepository) UpsertRepairRule(_ context.Context, commandID string, candidate RepairRule) (RepairRule, bool, error) {
	if r == nil {
		return RepairRule{}, false, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	commandKey := reliabilityKey(candidate.TenantID, commandID)
	if ruleID, exists := r.ruleCommands[commandKey]; exists {
		if ruleID != candidate.ID {
			return RepairRule{}, false, fmt.Errorf("%w: command id was reused for a different repair rule", ErrConflict)
		}
		rule, ok := r.rules[reliabilityKey(candidate.TenantID, ruleID)]
		if !ok {
			return RepairRule{}, false, fmt.Errorf("%w: command references missing repair rule", ErrConflict)
		}
		return cloneRepairRule(rule), false, nil
	}
	key := reliabilityKey(candidate.TenantID, candidate.ID)
	if existing, exists := r.rules[key]; exists {
		if err := validateRepairRuleUpdate(existing, candidate); err != nil {
			return RepairRule{}, false, err
		}
		candidate.CreatedAt = existing.CreatedAt
		candidate.Version = existing.Version + 1
	} else {
		candidate.Version = 1
	}
	r.rules[key] = cloneRepairRule(candidate)
	r.ruleCommands[commandKey] = candidate.ID
	return cloneRepairRule(candidate), true, nil
}

func (r *MemoryRepository) GetRepairRule(_ context.Context, tenantID, ruleID string) (RepairRule, error) {
	if r == nil {
		return RepairRule{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[reliabilityKey(tenantID, ruleID)]
	if !ok {
		return RepairRule{}, fmt.Errorf("%w: repair rule %s", ErrNotFound, ruleID)
	}
	return cloneRepairRule(rule), nil
}

func (r *MemoryRepository) ListRepairRules(_ context.Context, tenantID, fingerprint string) ([]RepairRule, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var rules []RepairRule
	for _, rule := range r.rules {
		if rule.TenantID != tenantID || (fingerprint != "" && rule.SelectorFingerprint != fingerprint) {
			continue
		}
		rules = append(rules, cloneRepairRule(rule))
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	return rules, nil
}

func (r *MemoryRepository) RecordRepairOutcome(_ context.Context, outcome RepairOutcome, updatedAt time.Time) (RepairRule, bool, error) {
	if r == nil {
		return RepairRule{}, false, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	outcomeKey := reliabilityKey(outcome.TenantID, outcome.ID)
	if recorded, exists := r.repairOutcomes[outcomeKey]; exists {
		if recorded.RuleID != outcome.RuleID || recorded.IncidentID != outcome.IncidentID ||
			recorded.Success != outcome.Success || recorded.EvidenceID != outcome.EvidenceID {
			return RepairRule{}, false, fmt.Errorf("%w: outcome id was reused with different repair outcome data", ErrConflict)
		}
		rule, ok := r.rules[reliabilityKey(outcome.TenantID, recorded.RuleID)]
		if !ok {
			return RepairRule{}, false, fmt.Errorf("%w: outcome references missing repair rule", ErrConflict)
		}
		return cloneRepairRule(rule), false, nil
	}
	ruleKey := reliabilityKey(outcome.TenantID, outcome.RuleID)
	rule, ok := r.rules[ruleKey]
	if !ok {
		return RepairRule{}, false, fmt.Errorf("%w: repair rule %s", ErrNotFound, outcome.RuleID)
	}
	applyRepairOutcome(&rule, outcome, updatedAt)
	r.rules[ruleKey] = cloneRepairRule(rule)
	r.repairOutcomes[outcomeKey] = outcome
	return cloneRepairRule(rule), true, nil
}

func reliabilityKey(tenantID, id string) string {
	return strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(id)
}

func severityRank(severity FailureSeverity) int {
	switch severity {
	case FailureSeverityCritical:
		return 3
	case FailureSeverityError:
		return 2
	case FailureSeverityWarning:
		return 1
	default:
		return 0
	}
}

func cloneIncident(incident FailureIncident) FailureIncident {
	return incident
}

func cloneRepairRule(rule RepairRule) RepairRule {
	if rule.ActivatedAt != nil {
		value := *rule.ActivatedAt
		rule.ActivatedAt = &value
	}
	if rule.LastVerifiedAt != nil {
		value := *rule.LastVerifiedAt
		rule.LastVerifiedAt = &value
	}
	if rule.ExpiresAt != nil {
		value := *rule.ExpiresAt
		rule.ExpiresAt = &value
	}
	return rule
}

func applyRepairOutcome(rule *RepairRule, outcome RepairOutcome, updatedAt time.Time) {
	if outcome.Success {
		rule.SuccessCount++
	} else {
		rule.FailureCount++
		rule.Status = RepairRuleStatusQuarantined
		rule.AutoApply = false
	}
	total := rule.SuccessCount + rule.FailureCount
	rule.Confidence = float64(rule.SuccessCount+1) / float64(total+2)
	verifiedAt := outcome.OccurredAt.UTC()
	if verifiedAt.IsZero() {
		verifiedAt = updatedAt.UTC()
	}
	rule.LastVerifiedAt = &verifiedAt
	rule.UpdatedAt = updatedAt.UTC()
	rule.Version++
}
