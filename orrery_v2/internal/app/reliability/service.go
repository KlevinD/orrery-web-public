package reliability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type EventSink interface {
	PublishReliabilityEvent(context.Context, Event) error
}

type EventReplaySink interface {
	ReplayReliabilityEvent(context.Context, Event) error
}

type RepairRuleActivationAuthorizer interface {
	AuthorizeRepairRuleActivation(context.Context, RepairRuleCommand) error
}

type Service struct {
	repository Repository
	sink       EventSink
	authorizer RepairRuleActivationAuthorizer
	now        func() time.Time
}

type Option func(*Service)

func WithEventSink(sink EventSink) Option {
	return func(service *Service) { service.sink = sink }
}

func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

func WithRepairRuleActivationAuthorizer(authorizer RepairRuleActivationAuthorizer) Option {
	return func(service *Service) { service.authorizer = authorizer }
}

func NewService(repository Repository, options ...Option) *Service {
	service := &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) ObserveFailure(ctx context.Context, observation FailureObservation) (FailureIncident, error) {
	if s == nil || s.repository == nil {
		return FailureIncident{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	observation = normalizeObservation(observation, s.now())
	if err := validateObservation(observation); err != nil {
		return FailureIncident{}, err
	}
	category := observation.Category
	if category == "" {
		category = classifyFailure(observation.ErrorCode, observation.Message)
	}
	severity := observation.Severity
	if severity == "" {
		severity = classifySeverity(observation.ErrorCode, observation.Message)
	}
	retryable := category == FailureCategoryTransient
	if observation.Retryable != nil {
		retryable = *observation.Retryable
	}
	fingerprint := failureFingerprint(observation.Source, observation.Component, observation.ErrorCode, category)
	now := s.now().UTC()
	incident := FailureIncident{
		ID:              stableID("failure-incident", observation.TenantID, fingerprint),
		TenantID:        observation.TenantID,
		Fingerprint:     fingerprint,
		Source:          observation.Source,
		Component:       observation.Component,
		Category:        category,
		Severity:        severity,
		Status:          IncidentStatusOpen,
		ErrorCode:       observation.ErrorCode,
		RedactedSummary: redactFailureSummary(observation.Message),
		Retryable:       retryable,
		FirstSeenAt:     observation.OccurredAt,
		LastSeenAt:      observation.OccurredAt,
		OccurrenceCount: 1,
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	incident, changed, err := s.repository.ObserveFailure(ctx, observation, incident)
	if err != nil {
		return FailureIncident{}, err
	}
	if err := s.publishEvent(ctx, failureObservedEvent(observation, incident), changed); err != nil {
		return incident, err
	}
	return incident, nil
}

func (s *Service) TransitionIncident(ctx context.Context, tenantID, incidentID string, next IncidentStatus, actor ActorRef) (FailureIncident, error) {
	if s == nil || s.repository == nil {
		return FailureIncident{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	tenantID = strings.TrimSpace(tenantID)
	incidentID = strings.TrimSpace(incidentID)
	if tenantID == "" || incidentID == "" || !validIncidentStatus(next) {
		return FailureIncident{}, fmt.Errorf("%w: tenant_id, incident_id, and valid status are required", ErrInvalidRequest)
	}
	incident, err := s.repository.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		return FailureIncident{}, err
	}
	if incident.Status == next {
		if err := s.publishEvent(ctx, failureStatusEvent(incident, actor), false); err != nil {
			return incident, err
		}
		return incident, nil
	}
	if !validIncidentTransition(incident.Status, next) {
		return FailureIncident{}, fmt.Errorf("%w: incident transition %s -> %s is not allowed", ErrInvalidRequest, incident.Status, next)
	}
	updatedAt := s.now().UTC()
	incident, err = s.repository.UpdateIncidentStatus(ctx, tenantID, incidentID, incident.Version, next, updatedAt)
	if err != nil {
		return FailureIncident{}, err
	}
	if err := s.publishEvent(ctx, failureStatusEvent(incident, actor), true); err != nil {
		return incident, err
	}
	return incident, nil
}

func (s *Service) CreateDiagnosisCase(ctx context.Context, command DiagnosisCaseCommand) (DiagnosisCase, error) {
	if s == nil || s.repository == nil {
		return DiagnosisCase{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	command.ID = strings.TrimSpace(command.ID)
	diagnosis := command.Diagnosis
	diagnosis.TenantID = strings.TrimSpace(diagnosis.TenantID)
	diagnosis.IncidentID = strings.TrimSpace(diagnosis.IncidentID)
	diagnosis.RootCauseCategory = FailureCategory(strings.TrimSpace(string(diagnosis.RootCauseCategory)))
	diagnosis.RedactedSummary = redactFailureSummary(diagnosis.RedactedSummary)
	diagnosis.EvidenceIDs = normalizeEvidenceIDs(diagnosis.EvidenceIDs)
	command.Actor.Kind = normalizeToken(command.Actor.Kind)
	command.Actor.ID = strings.TrimSpace(command.Actor.ID)
	if command.ID == "" || diagnosis.TenantID == "" || diagnosis.IncidentID == "" || command.Actor.Kind == "" || command.Actor.ID == "" {
		return DiagnosisCase{}, fmt.Errorf("%w: command, tenant, incident, and attributed actor are required", ErrInvalidRequest)
	}
	incident, err := s.repository.GetIncident(ctx, diagnosis.TenantID, diagnosis.IncidentID)
	if err != nil {
		return DiagnosisCase{}, err
	}
	if diagnosis.RootCauseCategory == "" {
		diagnosis.RootCauseCategory = incident.Category
	}
	diagnosis.ID = stableID("diagnosis-case", diagnosis.TenantID, diagnosis.IncidentID, command.ID)
	diagnosis.IncidentVersion = incident.Version
	diagnosis.Status = DiagnosisCaseStatusProposed
	diagnosis.ProposedBy = command.Actor
	diagnosis.ReviewedBy = ActorRef{}
	diagnosis.ReviewReason = ""
	now := s.now().UTC()
	diagnosis.CreatedAt = now
	diagnosis.UpdatedAt = now
	if err := validateDiagnosisCase(diagnosis); err != nil {
		return DiagnosisCase{}, err
	}
	diagnosis, changed, err := s.repository.CreateDiagnosisCase(ctx, command.ID, diagnosis)
	if err != nil {
		return DiagnosisCase{}, err
	}
	if err := s.publishEvent(ctx, diagnosisCaseEvent(diagnosis, command.ID), changed); err != nil {
		return diagnosis, err
	}
	return diagnosis, nil
}

func (s *Service) DecideDiagnosisCase(ctx context.Context, command DiagnosisDecisionCommand) (DiagnosisCase, error) {
	if s == nil || s.repository == nil {
		return DiagnosisCase{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	command.ID = strings.TrimSpace(command.ID)
	command.TenantID = strings.TrimSpace(command.TenantID)
	command.DiagnosisCaseID = strings.TrimSpace(command.DiagnosisCaseID)
	command.Reason = redactFailureSummary(command.Reason)
	command.Actor.Kind = normalizeToken(command.Actor.Kind)
	command.Actor.ID = strings.TrimSpace(command.Actor.ID)
	if err := validateDiagnosisDecisionCommand(command); err != nil {
		return DiagnosisCase{}, err
	}
	if command.Actor.Kind != "human" {
		return DiagnosisCase{}, fmt.Errorf("%w: only an attributed human may decide a diagnosis case", ErrInvalidRequest)
	}
	diagnosis, changed, err := s.repository.DecideDiagnosisCase(ctx, command, s.now().UTC())
	if err != nil {
		return DiagnosisCase{}, err
	}
	if err := s.publishEvent(ctx, diagnosisCaseEvent(diagnosis, command.ID), changed); err != nil {
		return diagnosis, err
	}
	return diagnosis, nil
}

// ProposeRepairRuleFromDiagnosis is the governed learning entry point for new
// rules. The lower-level UpsertRepairRule remains available for migrations and
// verified outcome updates, but production proposals use this evidence gate.
func (s *Service) ProposeRepairRuleFromDiagnosis(ctx context.Context, command RepairRuleCommand) (RepairRule, error) {
	if s == nil || s.repository == nil {
		return RepairRule{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	command.Rule.TenantID = strings.TrimSpace(command.Rule.TenantID)
	command.Rule.DiagnosisCaseID = strings.TrimSpace(command.Rule.DiagnosisCaseID)
	if command.Rule.TenantID == "" || command.Rule.DiagnosisCaseID == "" {
		return RepairRule{}, fmt.Errorf("%w: tenant_id and diagnosis_case_id are required", ErrInvalidRequest)
	}
	diagnosis, err := s.repository.GetDiagnosisCase(ctx, command.Rule.TenantID, command.Rule.DiagnosisCaseID)
	if err != nil {
		return RepairRule{}, err
	}
	incident, err := s.repository.GetIncident(ctx, command.Rule.TenantID, diagnosis.IncidentID)
	if err != nil {
		return RepairRule{}, err
	}
	if diagnosis.Status != DiagnosisCaseStatusConfirmed || diagnosis.IncidentVersion != incident.Version || len(diagnosis.EvidenceIDs) == 0 {
		return RepairRule{}, fmt.Errorf("%w: a current confirmed evidence-backed diagnosis is required", ErrInvalidRequest)
	}
	command.Rule.SourceIncidentID = incident.ID
	command.Rule.SelectorFingerprint = incident.Fingerprint
	command.Rule.Status = RepairRuleStatusProposed
	command.Rule.Confidence = 0
	command.Rule.SupportCount = 1
	command.Rule.SuccessCount = 0
	command.Rule.FailureCount = 0
	command.Rule.AutoApply = false
	return s.UpsertRepairRule(ctx, command)
}

func (s *Service) UpsertRepairRule(ctx context.Context, command RepairRuleCommand) (RepairRule, error) {
	if s == nil || s.repository == nil {
		return RepairRule{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	command.ID = strings.TrimSpace(command.ID)
	rule := normalizeRepairRule(command.Rule, s.now())
	if command.ID == "" {
		return RepairRule{}, fmt.Errorf("%w: command id is required", ErrInvalidRequest)
	}
	if err := validateRepairRule(rule); err != nil {
		return RepairRule{}, err
	}
	if rule.Status == RepairRuleStatusActive {
		if strings.TrimSpace(command.Actor.Kind) == "" || strings.TrimSpace(command.Actor.ID) == "" {
			return RepairRule{}, fmt.Errorf("%w: active repair rule requires an attributed actor", ErrInvalidRequest)
		}
		if command.Activation != nil && rule.ID != "" {
			existing, existingErr := s.repository.GetRepairRule(ctx, rule.TenantID, rule.ID)
			if existingErr == nil && existing.Status == RepairRuleStatusActive &&
				existing.ActivationDecisionID == strings.TrimSpace(command.Activation.DecisionID) &&
				existing.ActivatedBy == (ActorRef{Kind: strings.TrimSpace(command.Actor.Kind), ID: strings.TrimSpace(command.Actor.ID)}) &&
				sameRepairRuleActivationMaterial(existing, rule) {
				return existing, nil
			}
		}
		if s.authorizer == nil {
			return RepairRule{}, fmt.Errorf("%w: active repair rule requires a governance authorizer", ErrInvalidRequest)
		}
		command.Rule = rule
		if err := s.authorizer.AuthorizeRepairRuleActivation(ctx, command); err != nil {
			return RepairRule{}, fmt.Errorf("%w: repair rule activation authorization failed: %v", ErrInvalidRequest, err)
		}
		if command.Activation != nil {
			rule.ActivationDecisionID = strings.TrimSpace(command.Activation.DecisionID)
			rule.ActivatedBy = ActorRef{Kind: strings.TrimSpace(command.Actor.Kind), ID: strings.TrimSpace(command.Actor.ID)}
			activatedAt := s.now().UTC()
			rule.ActivatedAt = &activatedAt
		}
	}
	incident, err := s.repository.GetIncident(ctx, rule.TenantID, rule.SourceIncidentID)
	if err != nil {
		return RepairRule{}, err
	}
	if incident.Fingerprint != rule.SelectorFingerprint {
		return RepairRule{}, fmt.Errorf("%w: repair rule selector does not match its source incident", ErrInvalidRequest)
	}
	if rule.ID == "" {
		rule.ID = stableID("repair-rule", rule.TenantID, rule.SelectorFingerprint, string(rule.Strategy), rule.ActionTypeID, rule.DiagnosisCaseID)
	}
	rule, changed, err := s.repository.UpsertRepairRule(ctx, command.ID, rule)
	if err != nil {
		return RepairRule{}, err
	}
	if err := s.publishEvent(ctx, repairRuleEvent(rule, command.Actor, command.ID), changed); err != nil {
		return rule, err
	}
	return rule, nil
}

func sameRepairRuleActivationMaterial(existing, candidate RepairRule) bool {
	return existing.ID == candidate.ID && existing.TenantID == candidate.TenantID &&
		existing.SelectorFingerprint == candidate.SelectorFingerprint && existing.Strategy == candidate.Strategy &&
		existing.ActionTypeID == candidate.ActionTypeID && existing.RiskLevel == candidate.RiskLevel &&
		existing.Confidence == candidate.Confidence && existing.SupportCount == candidate.SupportCount &&
		existing.SuccessCount == candidate.SuccessCount && existing.FailureCount == candidate.FailureCount &&
		existing.Reversible == candidate.Reversible && existing.Idempotent == candidate.Idempotent &&
		existing.AutoApply == candidate.AutoApply &&
		existing.SourceIncidentID == candidate.SourceIncidentID && existing.DiagnosisCaseID == candidate.DiagnosisCaseID &&
		sameOptionalTime(existing.ExpiresAt, candidate.ExpiresAt)
}

func (s *Service) RecordRepairOutcome(ctx context.Context, outcome RepairOutcome) (RepairRule, error) {
	if s == nil || s.repository == nil {
		return RepairRule{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	outcome.ID = strings.TrimSpace(outcome.ID)
	outcome.TenantID = strings.TrimSpace(outcome.TenantID)
	outcome.RuleID = strings.TrimSpace(outcome.RuleID)
	outcome.IncidentID = strings.TrimSpace(outcome.IncidentID)
	if outcome.OccurredAt.IsZero() {
		outcome.OccurredAt = s.now().UTC()
	} else {
		outcome.OccurredAt = outcome.OccurredAt.UTC()
	}
	if outcome.ID == "" || outcome.TenantID == "" || outcome.RuleID == "" || outcome.IncidentID == "" || strings.TrimSpace(outcome.EvidenceID) == "" {
		return RepairRule{}, fmt.Errorf("%w: outcome id, tenant_id, rule_id, incident_id, and evidence_id are required", ErrInvalidRequest)
	}
	incident, err := s.repository.GetIncident(ctx, outcome.TenantID, outcome.IncidentID)
	if err != nil {
		return RepairRule{}, err
	}
	existingRule, err := s.repository.GetRepairRule(ctx, outcome.TenantID, outcome.RuleID)
	if err != nil {
		return RepairRule{}, err
	}
	if existingRule.SelectorFingerprint != incident.Fingerprint {
		return RepairRule{}, fmt.Errorf("%w: repair outcome incident does not match the rule selector", ErrInvalidRequest)
	}
	rule, changed, err := s.repository.RecordRepairOutcome(ctx, outcome, s.now().UTC())
	if err != nil {
		return RepairRule{}, err
	}
	if err := s.publishEvent(ctx, repairOutcomeEvent(rule, outcome), changed); err != nil {
		return rule, err
	}
	return rule, nil
}

func (s *Service) publishEvent(ctx context.Context, event Event, changed bool) error {
	if s == nil || s.sink == nil {
		return nil
	}
	if changed {
		return s.sink.PublishReliabilityEvent(ctx, event)
	}
	if replayer, ok := s.sink.(EventReplaySink); ok {
		return replayer.ReplayReliabilityEvent(ctx, event)
	}
	return nil
}

func normalizeObservation(observation FailureObservation, now time.Time) FailureObservation {
	observation.ID = strings.TrimSpace(observation.ID)
	observation.TenantID = strings.TrimSpace(observation.TenantID)
	observation.Source = normalizeToken(observation.Source)
	observation.Component = normalizeToken(observation.Component)
	observation.ErrorCode = strings.TrimSpace(observation.ErrorCode)
	if observation.ErrorCode == "" {
		observation.ErrorCode = "unknown_error"
	}
	if len(observation.ErrorCode) > 128 {
		observation.ErrorCode = observation.ErrorCode[:128]
	}
	if observation.OccurredAt.IsZero() {
		observation.OccurredAt = now.UTC()
	} else {
		observation.OccurredAt = observation.OccurredAt.UTC()
	}
	if observation.RepairTarget != nil {
		target := *observation.RepairTarget
		target.TenantID = observation.TenantID
		target.ObservationID = observation.ID
		target.AuthorizationTenantID = strings.TrimSpace(target.AuthorizationTenantID)
		target.MissionID = strings.TrimSpace(target.MissionID)
		target.RunID = strings.TrimSpace(target.RunID)
		target.OccurredAt = observation.OccurredAt
		observation.RepairTarget = &target
	}
	return observation
}

func validateObservation(observation FailureObservation) error {
	if observation.ID == "" || observation.TenantID == "" || observation.Source == "" || observation.Component == "" {
		return fmt.Errorf("%w: observation id, tenant_id, source, and component are required", ErrInvalidRequest)
	}
	if observation.Category != "" && !validFailureCategory(observation.Category) {
		return fmt.Errorf("%w: failure category %q is invalid", ErrInvalidRequest, observation.Category)
	}
	if observation.Severity != "" && severityRank(observation.Severity) == 0 {
		return fmt.Errorf("%w: failure severity %q is invalid", ErrInvalidRequest, observation.Severity)
	}
	if observation.RepairTarget != nil {
		target := observation.RepairTarget
		if target.Kind != RepairTargetMissionAgentRun || target.AuthorizationTenantID == "" || target.MissionID == "" || target.RunID == "" {
			return fmt.Errorf("%w: mission agent repair target requires authorization tenant, mission, and run", ErrInvalidRequest)
		}
	}
	return nil
}

func normalizeRepairRule(rule RepairRule, now time.Time) RepairRule {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.TenantID = strings.TrimSpace(rule.TenantID)
	rule.SelectorFingerprint = strings.TrimSpace(rule.SelectorFingerprint)
	rule.ActionTypeID = strings.TrimSpace(rule.ActionTypeID)
	rule.SourceIncidentID = strings.TrimSpace(rule.SourceIncidentID)
	rule.DiagnosisCaseID = strings.TrimSpace(rule.DiagnosisCaseID)
	rule.ActivationDecisionID = strings.TrimSpace(rule.ActivationDecisionID)
	rule.ActivatedBy = ActorRef{Kind: strings.TrimSpace(rule.ActivatedBy.Kind), ID: strings.TrimSpace(rule.ActivatedBy.ID)}
	if rule.Status == "" {
		rule.Status = RepairRuleStatusProposed
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now.UTC()
	}
	rule.UpdatedAt = now.UTC()
	if rule.Status != RepairRuleStatusActive {
		rule.AutoApply = false
	}
	if !autoApplyStrategyAllowed(rule.Strategy) || rule.RiskLevel != RiskLow || !rule.Reversible || !rule.Idempotent || rule.FailureCount > 0 {
		rule.AutoApply = false
	}
	return rule
}

func validateRepairRule(rule RepairRule) error {
	if rule.TenantID == "" || rule.SelectorFingerprint == "" || rule.ActionTypeID == "" || rule.SourceIncidentID == "" {
		return fmt.Errorf("%w: tenant_id, selector_fingerprint, action_type_id, and source_incident_id are required", ErrInvalidRequest)
	}
	if !validRepairStrategy(rule.Strategy) || !validRepairRuleStatus(rule.Status) || !validRiskLevel(rule.RiskLevel) {
		return fmt.Errorf("%w: repair strategy, status, or risk level is invalid", ErrInvalidRequest)
	}
	if rule.Confidence < 0 || rule.Confidence > 1 || rule.SupportCount < 0 || rule.SuccessCount < 0 || rule.FailureCount < 0 {
		return fmt.Errorf("%w: repair rule metrics are invalid", ErrInvalidRequest)
	}
	hasActivationProvenance := rule.ActivationDecisionID != "" || rule.ActivatedBy.Kind != "" || rule.ActivatedBy.ID != "" || rule.ActivatedAt != nil
	if hasActivationProvenance && (rule.Status != RepairRuleStatusActive || rule.ActivationDecisionID == "" || rule.ActivatedBy.Kind == "" || rule.ActivatedBy.ID == "" || rule.ActivatedAt == nil) {
		return fmt.Errorf("%w: repair rule activation provenance must be complete and belongs only to an active rule", ErrInvalidRequest)
	}
	return nil
}

func validateRepairRuleUpdate(existing, candidate RepairRule) error {
	if existing.SelectorFingerprint != candidate.SelectorFingerprint || existing.Strategy != candidate.Strategy ||
		existing.ActionTypeID != candidate.ActionTypeID || existing.SourceIncidentID != candidate.SourceIncidentID ||
		existing.DiagnosisCaseID != candidate.DiagnosisCaseID {
		return fmt.Errorf("%w: repair rule identity fields are immutable", ErrInvalidRequest)
	}
	if candidate.SupportCount < existing.SupportCount || candidate.SuccessCount < existing.SuccessCount || candidate.FailureCount < existing.FailureCount {
		return fmt.Errorf("%w: repair rule learning counters cannot decrease", ErrInvalidRequest)
	}
	if existing.Status == RepairRuleStatusQuarantined && candidate.Status != RepairRuleStatusQuarantined && candidate.Status != RepairRuleStatusRetired {
		return fmt.Errorf("%w: quarantined repair rule cannot be reactivated; create a new rule version", ErrInvalidRequest)
	}
	if existing.ActivationDecisionID != "" && (candidate.ActivationDecisionID != existing.ActivationDecisionID || candidate.ActivatedBy != existing.ActivatedBy || !sameOptionalTime(candidate.ActivatedAt, existing.ActivatedAt)) {
		return fmt.Errorf("%w: repair rule activation provenance is immutable", ErrInvalidRequest)
	}
	return nil
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func validateDiagnosisCase(diagnosis DiagnosisCase) error {
	if diagnosis.ID == "" || diagnosis.TenantID == "" || diagnosis.IncidentID == "" || diagnosis.IncidentVersion <= 0 ||
		diagnosis.RedactedSummary == "" || !validFailureCategory(diagnosis.RootCauseCategory) ||
		diagnosis.Confidence < 0 || diagnosis.Confidence > 1 || !validDiagnosisCaseStatus(diagnosis.Status) ||
		diagnosis.ProposedBy.Kind == "" || diagnosis.ProposedBy.ID == "" || len(diagnosis.EvidenceIDs) == 0 || len(diagnosis.EvidenceIDs) > 20 ||
		diagnosis.CreatedAt.IsZero() || diagnosis.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: complete evidence-backed diagnosis material is required", ErrInvalidRequest)
	}
	for _, evidenceID := range diagnosis.EvidenceIDs {
		if evidenceID == "" || len(evidenceID) > 256 {
			return fmt.Errorf("%w: diagnosis evidence ids are invalid", ErrInvalidRequest)
		}
	}
	return nil
}

func validateDiagnosisDecisionCommand(command DiagnosisDecisionCommand) error {
	if command.ID == "" || command.TenantID == "" || command.DiagnosisCaseID == "" || command.Reason == "" ||
		(command.Decision != DiagnosisDecisionConfirm && command.Decision != DiagnosisDecisionReject) ||
		command.Actor.Kind == "" || command.Actor.ID == "" {
		return fmt.Errorf("%w: complete diagnosis decision material is required", ErrInvalidRequest)
	}
	return nil
}

func normalizeEvidenceIDs(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func failureObservedEvent(observation FailureObservation, incident FailureIncident) Event {
	payload := incidentPayload(incident)
	payload["agent_run_id"] = observation.AgentRunID
	payload["action_run_id"] = observation.ActionRunID
	payload["evidence_id"] = observation.EvidenceID
	payload["observation_id"] = observation.ID
	return Event{
		ID: stableID("failure-observed", incident.TenantID, observation.ID), TenantID: incident.TenantID,
		Type: EventFailureObserved, StreamID: incident.ID, Version: incident.Version,
		CorrelationID: observation.CorrelationID, CausationID: observation.CausationID,
		OccurredAt: observation.OccurredAt, Actor: observation.Actor, Payload: payload,
	}
}

func failureStatusEvent(incident FailureIncident, actor ActorRef) Event {
	return Event{
		ID: stableEventID("failure-status", incident.ID, incident.Version), TenantID: incident.TenantID,
		Type: EventFailureStatusChanged, StreamID: incident.ID, Version: incident.Version,
		OccurredAt: incident.UpdatedAt, Actor: actor,
		Payload: map[string]any{"id": incident.ID, "status": string(incident.Status), "incident_version": incident.Version, "updated_at": formatTime(incident.UpdatedAt)},
	}
}

func diagnosisCaseEvent(diagnosis DiagnosisCase, causationID string) Event {
	payload := map[string]any{
		"id": diagnosis.ID, "incident_id": diagnosis.IncidentID, "incident_version": diagnosis.IncidentVersion,
		"root_cause_category": string(diagnosis.RootCauseCategory), "redacted_summary": diagnosis.RedactedSummary,
		"confidence": diagnosis.Confidence, "status": string(diagnosis.Status), "evidence_ids": append([]string(nil), diagnosis.EvidenceIDs...),
		"primary_evidence_id": diagnosis.EvidenceIDs[0], "diagnosis_version": diagnosis.Version,
		"proposed_by_kind": diagnosis.ProposedBy.Kind, "proposed_by_id": diagnosis.ProposedBy.ID,
		"reviewed_by_kind": diagnosis.ReviewedBy.Kind, "reviewed_by_id": diagnosis.ReviewedBy.ID,
		"review_reason": diagnosis.ReviewReason, "updated_at": formatTime(diagnosis.UpdatedAt),
	}
	return Event{
		ID: stableEventID("diagnosis-case", diagnosis.ID, diagnosis.Version), TenantID: diagnosis.TenantID,
		Type: EventDiagnosisCaseUpserted, StreamID: diagnosis.ID, Version: diagnosis.Version, CausationID: causationID,
		OccurredAt: diagnosis.UpdatedAt, Actor: firstDiagnosisActor(diagnosis), Payload: payload,
	}
}

func firstDiagnosisActor(diagnosis DiagnosisCase) ActorRef {
	if diagnosis.ReviewedBy.Kind != "" && diagnosis.ReviewedBy.ID != "" {
		return diagnosis.ReviewedBy
	}
	return diagnosis.ProposedBy
}

func repairRuleEvent(rule RepairRule, actor ActorRef, causationID string) Event {
	payload := map[string]any{
		"id": rule.ID, "selector_fingerprint": rule.SelectorFingerprint, "strategy": string(rule.Strategy),
		"action_type_id": rule.ActionTypeID, "risk_level": string(rule.RiskLevel), "status": string(rule.Status),
		"confidence": rule.Confidence, "support_count": rule.SupportCount, "success_count": rule.SuccessCount,
		"failure_count": rule.FailureCount, "reversible": rule.Reversible, "idempotent": rule.Idempotent,
		"auto_apply": rule.AutoApply, "rule_version": rule.Version, "source_incident_id": rule.SourceIncidentID,
		"diagnosis_case_id": rule.DiagnosisCaseID,
	}
	if rule.ActivationDecisionID != "" {
		payload["activation_decision_id"] = rule.ActivationDecisionID
		payload["activated_by_kind"] = rule.ActivatedBy.Kind
		payload["activated_by_id"] = rule.ActivatedBy.ID
	}
	if rule.ActivatedAt != nil {
		payload["activated_at"] = formatTime(*rule.ActivatedAt)
	}
	if rule.LastVerifiedAt != nil {
		payload["last_verified_at"] = formatTime(*rule.LastVerifiedAt)
	}
	if rule.ExpiresAt != nil {
		payload["expires_at"] = formatTime(*rule.ExpiresAt)
	}
	return Event{
		ID: stableID("repair-rule-upserted", rule.TenantID, causationID), TenantID: rule.TenantID,
		Type: EventRepairRuleUpserted, StreamID: rule.ID, Version: rule.Version, CausationID: causationID,
		OccurredAt: rule.UpdatedAt, Actor: actor, Payload: payload,
	}
}

func repairOutcomeEvent(rule RepairRule, outcome RepairOutcome) Event {
	event := repairRuleEvent(rule, outcome.Actor, outcome.ID)
	event.Payload["outcome_id"] = outcome.ID
	event.Payload["outcome_incident_id"] = outcome.IncidentID
	event.Payload["outcome_success"] = outcome.Success
	event.Payload["outcome_evidence_id"] = outcome.EvidenceID
	event.Payload["outcome_resolution_kind"] = "repair_rule"
	event.Payload["outcome_resolution_id"] = rule.ID
	result := "failed"
	if outcome.Success {
		result = "succeeded"
	}
	event.Payload["outcome_summary"] = fmt.Sprintf("Verified %s repair %s for the matching problem fingerprint.", rule.Strategy, result)
	event.OccurredAt = outcome.OccurredAt
	return event
}

func incidentPayload(incident FailureIncident) map[string]any {
	return map[string]any{
		"id": incident.ID, "fingerprint": incident.Fingerprint, "source": incident.Source, "component": incident.Component,
		"category": string(incident.Category), "severity": string(incident.Severity), "status": string(incident.Status),
		"error_code": incident.ErrorCode, "redacted_summary": incident.RedactedSummary, "retryable": incident.Retryable,
		"first_seen_at": formatTime(incident.FirstSeenAt), "last_seen_at": formatTime(incident.LastSeenAt),
		"occurrence_count": incident.OccurrenceCount, "incident_version": incident.Version,
	}
}

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)\b(password|passwd|token|secret|api[_-]?key)\s*[:=]\s*[^\s,;]+`)
	bearerPattern           = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)
	urlQueryPattern         = regexp.MustCompile(`https?://[^\s?]+\?[^\s]+`)
)

func redactFailureSummary(message string) string {
	message = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(message, "\r", "\n"), "\t", " "))
	if index := strings.Index(message, "\n"); index >= 0 {
		message = message[:index]
	}
	message = secretAssignmentPattern.ReplaceAllString(message, "$1=[REDACTED]")
	message = bearerPattern.ReplaceAllString(message, "Bearer [REDACTED]")
	message = urlQueryPattern.ReplaceAllStringFunc(message, func(value string) string {
		if index := strings.Index(value, "?"); index >= 0 {
			return value[:index] + "?[REDACTED]"
		}
		return value
	})
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > 240 {
		message = string(runes[:240])
	}
	return message
}

// RedactFailureSummary returns the bounded summary that is safe to project into
// the company context graph. Raw diagnostic material remains outside Ontology.
func RedactFailureSummary(message string) string {
	return redactFailureSummary(message)
}

func failureFingerprint(source, component, errorCode string, category FailureCategory) string {
	value := strings.Join([]string{normalizeToken(source), normalizeToken(component), normalizeToken(errorCode), string(category)}, "|")
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stableID(prefix string, values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + ":" + hex.EncodeToString(sum[:12])
}

func stableEventID(prefix, streamID string, version int64) string {
	return fmt.Sprintf("%s:%s:%d", prefix, streamID, version)
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(" ", "_", "-", "_", ".", "_").Replace(value)
	return value
}

func classifyFailure(errorCode, message string) FailureCategory {
	value := strings.ToLower(errorCode + " " + message)
	switch {
	case containsAny(value, "permission", "forbidden", "unauthorized", "authentication", "access denied"):
		return FailureCategoryPermission
	case containsAny(value, "configuration", "config", "missing env", "invalid setting"):
		return FailureCategoryConfiguration
	case containsAny(value, "timeout", "timed out", "temporary", "unavailable", "connection reset", "rate limit", "deadlock", "websocket", "connection_closed", "dial tcp"):
		return FailureCategoryTransient
	case containsAny(value, "panic", "nil pointer", "assertion failed", "segmentation"):
		return FailureCategoryCode
	case containsAny(value, "constraint", "schema", "decode", "parse", "malformed data"):
		return FailureCategoryData
	case containsAny(value, "validation", "policy", "business rule", "semantic"):
		return FailureCategorySemantic
	default:
		return FailureCategoryUnknown
	}
}

func classifySeverity(errorCode, message string) FailureSeverity {
	value := strings.ToLower(errorCode + " " + message)
	if containsAny(value, "panic", "corruption", "security breach", "data loss") {
		return FailureSeverityCritical
	}
	return FailureSeverityError
}

func containsAny(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func validFailureCategory(category FailureCategory) bool {
	switch category {
	case FailureCategoryTransient, FailureCategoryConfiguration, FailureCategoryCode, FailureCategoryPermission, FailureCategoryData, FailureCategorySemantic, FailureCategoryUnknown:
		return true
	default:
		return false
	}
}

func validIncidentStatus(status IncidentStatus) bool {
	switch status {
	case IncidentStatusOpen, IncidentStatusContained, IncidentStatusDiagnosed, IncidentStatusRepairing, IncidentStatusMonitoring, IncidentStatusResolved, IncidentStatusEscalated, IncidentStatusQuarantined:
		return true
	default:
		return false
	}
}

func validDiagnosisCaseStatus(status DiagnosisCaseStatus) bool {
	switch status {
	case DiagnosisCaseStatusProposed, DiagnosisCaseStatusConfirmed, DiagnosisCaseStatusRejected:
		return true
	default:
		return false
	}
}

func validIncidentTransition(from, to IncidentStatus) bool {
	allowed := map[IncidentStatus]map[IncidentStatus]bool{
		IncidentStatusOpen:        {IncidentStatusContained: true, IncidentStatusDiagnosed: true, IncidentStatusEscalated: true, IncidentStatusQuarantined: true, IncidentStatusResolved: true},
		IncidentStatusContained:   {IncidentStatusDiagnosed: true, IncidentStatusRepairing: true, IncidentStatusMonitoring: true, IncidentStatusResolved: true, IncidentStatusEscalated: true, IncidentStatusQuarantined: true},
		IncidentStatusDiagnosed:   {IncidentStatusRepairing: true, IncidentStatusEscalated: true, IncidentStatusQuarantined: true, IncidentStatusResolved: true},
		IncidentStatusRepairing:   {IncidentStatusMonitoring: true, IncidentStatusEscalated: true, IncidentStatusQuarantined: true},
		IncidentStatusMonitoring:  {IncidentStatusResolved: true, IncidentStatusRepairing: true, IncidentStatusEscalated: true, IncidentStatusQuarantined: true},
		IncidentStatusEscalated:   {IncidentStatusDiagnosed: true, IncidentStatusRepairing: true, IncidentStatusQuarantined: true, IncidentStatusResolved: true},
		IncidentStatusQuarantined: {IncidentStatusDiagnosed: true, IncidentStatusRepairing: true, IncidentStatusResolved: true},
	}
	return allowed[from][to]
}

func validRepairStrategy(strategy RepairStrategy) bool {
	switch strategy {
	case RepairStrategyRetry, RepairStrategyRestart, RepairStrategyFallback, RepairStrategyReconfigure, RepairStrategyPatch, RepairStrategyPermissionChange, RepairStrategyDataMutation:
		return true
	default:
		return false
	}
}

func validRepairRuleStatus(status RepairRuleStatus) bool {
	switch status {
	case RepairRuleStatusProposed, RepairRuleStatusActive, RepairRuleStatusQuarantined, RepairRuleStatusRetired:
		return true
	default:
		return false
	}
}

func validRiskLevel(level RiskLevel) bool {
	switch level {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
