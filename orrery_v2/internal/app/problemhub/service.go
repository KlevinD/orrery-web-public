package problemhub

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

type Service struct {
	repository Repository
	events     EventSink
	lessons    LessonSink
	now        func() time.Time
}

type Option func(*Service)

func WithEventSink(sink EventSink) Option {
	return func(service *Service) { service.events = sink }
}

func WithLessonSink(sink LessonSink) Option {
	return func(service *Service) { service.lessons = sink }
}

func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

func NewService(repository Repository, options ...Option) *Service {
	service := &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Observe(ctx context.Context, observation Observation) (ProblemCase, error) {
	if s == nil || s.repository == nil {
		return ProblemCase{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	observation = normalizeObservation(observation, s.now())
	if err := validateObservation(observation); err != nil {
		return ProblemCase{}, err
	}
	now := s.now().UTC()
	fingerprint := observation.SourceDomain + ":" + observation.Fingerprint
	candidate := ProblemCase{
		ID: stableID("problem-case", observation.TenantID, fingerprint), TenantID: observation.TenantID,
		Fingerprint: fingerprint, SourceDomain: observation.SourceDomain, Category: observation.Category,
		Severity: observation.Severity, Status: StatusOpen, Title: observation.Title,
		RedactedSummary: observation.RedactedSummary, PrimarySource: observation.Source,
		SourceRefs: []ObjectRef{observation.Source}, EvidenceIDs: []string{observation.EvidenceID},
		OccurrenceCount: 1, Version: 1, FirstSeenAt: observation.OccurredAt,
		LastSeenAt: observation.OccurredAt, CreatedAt: now, UpdatedAt: now,
	}
	problem, changed, err := s.repository.Observe(ctx, observation, candidate)
	if err != nil {
		return ProblemCase{}, err
	}
	if err := s.publish(ctx, problemCaseEvent(problem, observation.Actor, observation.CorrelationID, observation.CausationID), changed); err != nil {
		return problem, err
	}
	return problem, nil
}

func (s *Service) SyncSourceStatus(ctx context.Context, tenantID string, source ObjectRef, status Status, actor ActorRef, occurredAt time.Time) (ProblemCase, error) {
	if s == nil || s.repository == nil {
		return ProblemCase{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	tenantID, source, actor = strings.TrimSpace(tenantID), normalizeObjectRef(source), normalizeActor(actor)
	if tenantID == "" || source.Kind == "" || source.ID == "" || !status.IsValid() {
		return ProblemCase{}, fmt.Errorf("%w: tenant_id, source, and valid status are required", ErrInvalidRequest)
	}
	if occurredAt.IsZero() {
		occurredAt = s.now().UTC()
	} else {
		occurredAt = occurredAt.UTC()
	}
	problem, changed, err := s.repository.SyncStatusBySource(ctx, tenantID, source, status, occurredAt)
	if err != nil {
		return ProblemCase{}, err
	}
	if err := s.publish(ctx, problemCaseEvent(problem, actor, "", source.Kind+":"+source.ID), changed); err != nil {
		return problem, err
	}
	return s.ensureLessonCandidate(ctx, problem, actor)
}

func (s *Service) RecordOutcome(ctx context.Context, outcome Outcome) (ProblemCase, error) {
	if s == nil || s.repository == nil {
		return ProblemCase{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	outcome = normalizeOutcome(outcome, s.now())
	if err := validateOutcome(outcome); err != nil {
		return ProblemCase{}, err
	}
	problem, changed, err := s.repository.RecordOutcome(ctx, outcome, s.now().UTC())
	if err != nil {
		return ProblemCase{}, err
	}
	if err := s.publish(ctx, problemCaseEvent(problem, outcome.Actor, outcome.CorrelationID, outcome.CausationID), changed); err != nil {
		return problem, err
	}
	return s.ensureLessonCandidate(ctx, problem, outcome.Actor)
}

func (s *Service) Route(ctx context.Context, command RouteCommand) (ProblemCase, error) {
	if s == nil || s.repository == nil {
		return ProblemCase{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	command.ID, command.TenantID, command.ProblemCaseID = strings.TrimSpace(command.ID), strings.TrimSpace(command.TenantID), strings.TrimSpace(command.ProblemCaseID)
	command.Owner, command.Actor = normalizeActor(command.Owner), normalizeActor(command.Actor)
	if command.OccurredAt.IsZero() {
		command.OccurredAt = s.now().UTC()
	} else {
		command.OccurredAt = command.OccurredAt.UTC()
	}
	if command.ID == "" || command.TenantID == "" || command.ProblemCaseID == "" || command.Owner.Kind == "" || command.Owner.ID == "" {
		return ProblemCase{}, fmt.Errorf("%w: command, tenant, problem case, and owner are required", ErrInvalidRequest)
	}
	problem, changed, err := s.repository.Route(ctx, command, s.now().UTC())
	if err != nil {
		return ProblemCase{}, err
	}
	if err := s.publish(ctx, problemCaseEvent(problem, command.Actor, "", command.ID), changed); err != nil {
		return problem, err
	}
	return problem, nil
}

func (s *Service) Get(ctx context.Context, tenantID, problemCaseID string) (ProblemCase, error) {
	if s == nil || s.repository == nil {
		return ProblemCase{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	return s.repository.Get(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(problemCaseID))
}

func (s *Service) List(ctx context.Context, query ListQuery) ([]ProblemCase, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.OwnerKind = normalizeToken(query.OwnerKind)
	query.OwnerID = strings.TrimSpace(query.OwnerID)
	query.SourceDomain = normalizeToken(query.SourceDomain)
	query.Search = strings.TrimSpace(query.Search)
	if query.TenantID == "" || (query.Status != "" && !query.Status.IsValid()) || len(query.Search) > 256 {
		return nil, fmt.Errorf("%w: tenant_id and valid filters are required", ErrInvalidRequest)
	}
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 100
	}
	return s.repository.List(ctx, query)
}

func (s *Service) ensureLessonCandidate(ctx context.Context, problem ProblemCase, actor ActorRef) (ProblemCase, error) {
	if problem.Status != StatusResolved || !problem.LatestOutcomeSuccess || problem.LatestOutcomeID == "" || s.lessons == nil {
		return problem, nil
	}
	evidence := append([]string(nil), problem.EvidenceIDs...)
	sort.Strings(evidence)
	lineage := []string{"problem_case:" + problem.ID}
	for _, source := range problem.SourceRefs {
		lineage = append(lineage, source.Kind+":"+source.ID)
	}
	if problem.LatestResolution.Kind != "" && problem.LatestResolution.ID != "" {
		lineage = append(lineage, problem.LatestResolution.Kind+":"+problem.LatestResolution.ID)
	}
	candidateID := problem.LessonCandidateID
	if candidateID == "" {
		candidateID = stableID("problem-lesson", problem.TenantID, problem.ID, problem.LatestOutcomeID)
	}
	createdBy := strings.TrimSpace(actor.ID)
	if createdBy == "" {
		createdBy = "problem-hub"
	}
	lesson := problem.LatestOutcomeSummary
	if lesson == "" {
		lesson = "Verified resolution succeeded for " + problem.Title
	}
	createdAt := problem.UpdatedAt
	if problem.ResolvedAt != nil {
		createdAt = *problem.ResolvedAt
	}
	proposal := LessonProposal{
		ID: candidateID, TenantID: problem.TenantID, ProblemCaseID: problem.ID,
		Topic:        problem.SourceDomain + "." + problem.Category,
		Summary:      "Verified resolution: " + problem.Title,
		Lesson:       lesson,
		Warning:      "Reuse only when the problem fingerprint and operating context match; human review is required.",
		EvidenceRefs: evidence, LineageRefs: uniqueStrings(lineage), Confidence: 0.7,
		CreatedBy: createdBy, CreatedAt: createdAt,
	}
	storedID := candidateID
	updated, changed := problem, false
	if problem.LessonCandidateID == "" {
		proposedID, err := s.lessons.ProposeProblemLesson(ctx, proposal)
		if err != nil {
			return problem, err
		}
		storedID = proposedID
		if storedID == "" {
			storedID = candidateID
		}
		linked, linkChanged, err := s.repository.SetLessonCandidate(ctx, problem.TenantID, problem.ID, problem.Version, storedID, s.now().UTC())
		if err != nil {
			return problem, err
		}
		updated, changed = linked, linkChanged
	}
	if changed {
		if err := s.publish(ctx, problemCaseEvent(updated, ActorRef{Kind: "service", ID: "problem-hub"}, "", problem.LatestOutcomeID), true); err != nil {
			return updated, err
		}
	}
	if err := s.publish(ctx, lessonCandidateEvent(updated, proposal, storedID), changed); err != nil {
		return updated, err
	}
	return updated, nil
}

func (s *Service) publish(ctx context.Context, event Event, changed bool) error {
	if s.events == nil {
		return nil
	}
	if changed {
		return s.events.PublishProblemHubEvent(ctx, event)
	}
	if replay, ok := s.events.(EventReplaySink); ok {
		return replay.ReplayProblemHubEvent(ctx, event)
	}
	return nil
}

func problemCaseEvent(problem ProblemCase, actor ActorRef, correlationID, causationID string) Event {
	payload := map[string]any{
		"id": problem.ID, "fingerprint": problem.Fingerprint, "source_domain": problem.SourceDomain,
		"category": problem.Category, "severity": problem.Severity, "status": string(problem.Status),
		"title": problem.Title, "redacted_summary": problem.RedactedSummary,
		"owner_kind": problem.Owner.Kind, "owner_id": problem.Owner.ID,
		"primary_source_kind": problem.PrimarySource.Kind, "primary_source_id": problem.PrimarySource.ID,
		"evidence_ids": append([]string(nil), problem.EvidenceIDs...), "occurrence_count": problem.OccurrenceCount,
		"latest_outcome_id": problem.LatestOutcomeID, "latest_outcome_success": problem.LatestOutcomeSuccess,
		"latest_outcome_summary": problem.LatestOutcomeSummary,
		"latest_resolution_kind": problem.LatestResolution.Kind, "latest_resolution_id": problem.LatestResolution.ID,
		"lesson_candidate_id": problem.LessonCandidateID, "problem_version": problem.Version,
		"first_seen_at": formatTime(problem.FirstSeenAt), "last_seen_at": formatTime(problem.LastSeenAt),
		"updated_at": formatTime(problem.UpdatedAt),
	}
	if len(problem.EvidenceIDs) > 0 {
		payload["primary_evidence_id"] = problem.EvidenceIDs[0]
	}
	if problem.PrimarySource.Kind == "failure_incident" {
		payload["source_failure_incident_id"] = problem.PrimarySource.ID
	}
	if problem.LatestResolution.Kind == "repair_rule" {
		payload["resolution_repair_rule_id"] = problem.LatestResolution.ID
	}
	if problem.ResolvedAt != nil {
		payload["resolved_at"] = formatTime(*problem.ResolvedAt)
	}
	return Event{
		ID: stableEventID("problem-case", problem.ID, problem.Version), TenantID: problem.TenantID,
		Type: EventProblemCaseUpserted, StreamID: problem.ID, Version: problem.Version,
		CorrelationID: correlationID, CausationID: causationID, OccurredAt: problem.UpdatedAt,
		Actor: fallbackActor(actor), Payload: payload,
	}
}

func lessonCandidateEvent(problem ProblemCase, proposal LessonProposal, candidateID string) Event {
	payload := map[string]any{
		"id": candidateID, "memory_candidate_id": candidateID, "problem_case_id": problem.ID,
		"status": "proposed", "proposed_layer": "experience", "topic": proposal.Topic,
		"summary": proposal.Summary, "confidence": proposal.Confidence, "review_required": true,
		"evidence_ids": append([]string(nil), proposal.EvidenceRefs...), "created_at": formatTime(proposal.CreatedAt),
	}
	if len(proposal.EvidenceRefs) > 0 {
		payload["primary_evidence_id"] = proposal.EvidenceRefs[0]
	}
	return Event{
		ID: stableID("problem-lesson-proposed", problem.TenantID, candidateID), TenantID: problem.TenantID,
		Type: EventLessonCandidateProposed, StreamID: candidateID, Version: 1,
		CausationID: problem.LatestOutcomeID, OccurredAt: proposal.CreatedAt,
		Actor: ActorRef{Kind: "service", ID: "problem-hub"}, Payload: payload,
	}
}

func (s Status) IsValid() bool {
	switch s {
	case StatusOpen, StatusTriaged, StatusContained, StatusDiagnosed, StatusInProgress, StatusMonitoring, StatusResolved, StatusEscalated, StatusQuarantined:
		return true
	default:
		return false
	}
}

func normalizeObservation(observation Observation, now time.Time) Observation {
	observation.ID = strings.TrimSpace(observation.ID)
	observation.TenantID = strings.TrimSpace(observation.TenantID)
	observation.Fingerprint = strings.TrimSpace(observation.Fingerprint)
	observation.SourceDomain = normalizeToken(observation.SourceDomain)
	observation.Source = normalizeObjectRef(observation.Source)
	observation.Category = normalizeToken(observation.Category)
	observation.Severity = normalizeToken(observation.Severity)
	observation.Title = redactText(observation.Title)
	observation.RedactedSummary = redactText(observation.RedactedSummary)
	observation.EvidenceID = strings.TrimSpace(observation.EvidenceID)
	observation.Actor = fallbackActor(normalizeActor(observation.Actor))
	if observation.OccurredAt.IsZero() {
		observation.OccurredAt = now.UTC()
	} else {
		observation.OccurredAt = observation.OccurredAt.UTC()
	}
	if observation.Title == "" {
		observation.Title = firstNonBlank(observation.RedactedSummary, observation.Category+" problem")
	}
	return observation
}

func validateObservation(observation Observation) error {
	if observation.ID == "" || observation.TenantID == "" || observation.Fingerprint == "" || observation.SourceDomain == "" ||
		observation.Source.Kind == "" || observation.Source.ID == "" || observation.Category == "" || observation.Severity == "" ||
		observation.Title == "" || observation.EvidenceID == "" {
		return fmt.Errorf("%w: observation, tenant, fingerprint, source, classification, title, and evidence are required", ErrInvalidRequest)
	}
	return nil
}

func normalizeOutcome(outcome Outcome, now time.Time) Outcome {
	outcome.ID, outcome.TenantID, outcome.ProblemCaseID = strings.TrimSpace(outcome.ID), strings.TrimSpace(outcome.TenantID), strings.TrimSpace(outcome.ProblemCaseID)
	outcome.Source, outcome.Resolution = normalizeObjectRef(outcome.Source), normalizeObjectRef(outcome.Resolution)
	outcome.RedactedSummary = redactText(outcome.RedactedSummary)
	outcome.EvidenceID = strings.TrimSpace(outcome.EvidenceID)
	outcome.Actor = fallbackActor(normalizeActor(outcome.Actor))
	if outcome.OccurredAt.IsZero() {
		outcome.OccurredAt = now.UTC()
	} else {
		outcome.OccurredAt = outcome.OccurredAt.UTC()
	}
	return outcome
}

func validateOutcome(outcome Outcome) error {
	if outcome.ID == "" || outcome.TenantID == "" || (outcome.ProblemCaseID == "" && (outcome.Source.Kind == "" || outcome.Source.ID == "")) ||
		outcome.Resolution.Kind == "" || outcome.Resolution.ID == "" || outcome.EvidenceID == "" || outcome.RedactedSummary == "" {
		return fmt.Errorf("%w: outcome, tenant, problem source, resolution, summary, and evidence are required", ErrInvalidRequest)
	}
	return nil
}

func normalizeObjectRef(ref ObjectRef) ObjectRef {
	return ObjectRef{Kind: normalizeToken(ref.Kind), ID: strings.TrimSpace(ref.ID)}
}

func normalizeActor(actor ActorRef) ActorRef {
	return ActorRef{Kind: normalizeToken(actor.Kind), ID: strings.TrimSpace(actor.ID)}
}

func fallbackActor(actor ActorRef) ActorRef {
	if actor.Kind == "" || actor.ID == "" {
		return ActorRef{Kind: "service", ID: "problem-hub"}
	}
	return actor
}

var nonTokenPattern = regexp.MustCompile(`[^a-z0-9_.-]+`)

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Trim(nonTokenPattern.ReplaceAllString(value, "_"), "_")
}

var (
	secretPattern = regexp.MustCompile(`(?i)\b(password|passwd|token|secret|api[_-]?key)\s*[:=]\s*[^\s,;]+`)
	bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)
	queryPattern  = regexp.MustCompile(`https?://[^\s?]+\?[^\s]+`)
)

func redactText(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", "\n"), "\t", " "))
	if index := strings.Index(value, "\n"); index >= 0 {
		value = value[:index]
	}
	value = secretPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = queryPattern.ReplaceAllString(value, "[REDACTED_URL]")
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}

func stableID(prefix string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + ":" + hex.EncodeToString(digest[:12])
}

func stableEventID(prefix, streamID string, version int64) string {
	return fmt.Sprintf("%s:%s:%d", prefix, streamID, version)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
