package memoryadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/graphprotocol"
)

type Repository interface {
	SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error
	GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error)
	ListCandidates(ctx context.Context, query memstore.CandidateQuery) ([]memcore.MemoryCandidate, error)
	UpdateCandidateStatus(ctx context.Context, id string, status memcore.CandidateStatus) error
	SaveRecord(ctx context.Context, record memcore.MemoryRecord) error
	FindRecords(ctx context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error)
	UpdateRecordLifecycle(ctx context.Context, id string, status memcore.MemoryLifecycleStatus) error
}

type memoryProfileRepository interface {
	SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error
	GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error)
	ListProfiles(ctx context.Context) ([]memcore.MemoryProfile, error)
}

type AuditRecorder interface {
	RecordMemoryAudit(ctx context.Context, event memaudit.Event) error
}

type MemoryRecordMetadataSink interface {
	PublishMemoryRecordMetadata(ctx context.Context, event graphprotocol.DomainEvent) error
}

type CitationEvidenceQuery struct {
	MemoryID string
	TenantID string
	Refs     []string
}

type CitationEvidenceResolver interface {
	ResolveCitationEvidence(ctx context.Context, query CitationEvidenceQuery) ([]CitationEvidenceDTO, error)
}

type ClockFunc func() time.Time

type Service struct {
	repository       Repository
	audit            AuditRecorder
	metadataSink     MemoryRecordMetadataSink
	citationEvidence CitationEvidenceResolver
	now              ClockFunc
}

func NewService(repository Repository, audit AuditRecorder, now ClockFunc) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repository: repository, audit: audit, now: now}
}

func (s *Service) WithMemoryRecordMetadataSink(sink MemoryRecordMetadataSink) *Service {
	s.metadataSink = sink
	return s
}

func (s *Service) WithCitationEvidenceResolver(resolver CitationEvidenceResolver) *Service {
	s.citationEvidence = resolver
	return s
}

func (s *Service) ListCandidates(ctx context.Context, viewer ViewerRef, query ListCandidatesQuery) (MemoryCandidatePageDTO, error) {
	if err := ctx.Err(); err != nil {
		return MemoryCandidatePageDTO{}, err
	}
	if s == nil || s.repository == nil {
		return MemoryCandidatePageDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryRead); err != nil {
		return MemoryCandidatePageDTO{}, err
	}
	candidates, err := s.repository.ListCandidates(ctx, memstore.CandidateQuery{
		MissionID:      strings.TrimSpace(query.MissionID),
		ScopeKind:      memcore.ScopeKind(strings.TrimSpace(query.ScopeKind)),
		ScopeID:        strings.TrimSpace(query.ScopeID),
		Layer:          memcore.MemoryLayer(strings.TrimSpace(query.Layer)),
		Topic:          strings.TrimSpace(query.Topic),
		Status:         memcore.CandidateStatus(strings.TrimSpace(query.Status)),
		ReviewRequired: query.ReviewRequired,
		Limit:          query.Limit,
	})
	if err != nil {
		return MemoryCandidatePageDTO{}, err
	}
	page := MemoryCandidatePageDTO{Limit: query.Limit}
	for _, candidate := range candidates {
		if isPersonalAssistantMemoryCandidate(candidate) && !personalAssistantMemoryViewer(viewer, candidate.ProposedScope.ID) {
			continue
		}
		if !canReadCandidate(viewer, candidate) {
			continue
		}
		page.Candidates = append(page.Candidates, MapCandidateDTO(candidate))
	}
	page.Total = len(page.Candidates)
	return page, nil
}

func (s *Service) CreateCandidate(ctx context.Context, viewer ViewerRef, req CreateCandidateRequest) (MemoryCandidateDTO, error) {
	if err := ctx.Err(); err != nil {
		return MemoryCandidateDTO{}, err
	}
	if s == nil || s.repository == nil {
		return MemoryCandidateDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryWrite); err != nil {
		return MemoryCandidateDTO{}, err
	}
	candidate, err := s.candidateFromRequest(viewer, req)
	if err != nil {
		return MemoryCandidateDTO{}, err
	}
	if !canViewCandidate(viewer, candidate) {
		return MemoryCandidateDTO{}, ErrPermissionDenied
	}
	if err := s.repository.SaveCandidate(ctx, candidate); err != nil {
		return MemoryCandidateDTO{}, mapStoreError(err)
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryCandidateCreated,
		SubjectID:  candidate.ID,
		ActorID:    viewer.ViewerID,
		MissionID:  candidate.MissionID,
		Summary:    candidate.Summary,
		SourceRefs: append([]string(nil), candidate.EvidenceRefs...),
		OccurredAt: candidate.CreatedAt,
	}); err != nil {
		return MemoryCandidateDTO{}, err
	}
	return MapCandidateDTO(candidate), nil
}

func (s *Service) AcceptCandidate(ctx context.Context, viewer ViewerRef, candidateID string, req AcceptCandidateRequest) (MemoryRecordDTO, error) {
	if err := ctx.Err(); err != nil {
		return MemoryRecordDTO{}, err
	}
	if s == nil || s.repository == nil {
		return MemoryRecordDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MemoryRecordDTO{}, err
	}
	candidate, err := s.candidateForReview(ctx, viewer, candidateID)
	if err != nil {
		return MemoryRecordDTO{}, err
	}
	if !canAcceptCandidate(viewer, candidate) {
		return MemoryRecordDTO{}, ErrPermissionDenied
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		profileID, err := s.ensureCandidateMemoryProfile(ctx, candidate)
		if err != nil {
			return MemoryRecordDTO{}, err
		}
		req.ProfileID = string(profileID)
	}
	now := s.now()
	record, err := buildRecordFromCandidate(candidate, req, viewer.ViewerID, now)
	if err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.repository.UpdateCandidateStatus(ctx, candidate.ID, memcore.CandidateAccepted); err != nil {
		return MemoryRecordDTO{}, mapStoreError(err)
	}
	candidate.Status = memcore.CandidateAccepted
	if err := s.repository.SaveRecord(ctx, record); err != nil {
		return MemoryRecordDTO{}, mapStoreError(err)
	}
	if err := s.publishMemoryRecordMetadata(ctx, record); err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.publishProblemMemoryRecordAccepted(ctx, record); err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.publishMemoryScopeLinked(ctx, record); err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.publishMemoryKnowledgeCitationAdded(ctx, record); err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.publishMemoryKnowledgeChunkCitationAdded(ctx, record); err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.publishMemoryKnowledgeAnchorCitationAdded(ctx, record); err != nil {
		return MemoryRecordDTO{}, err
	}
	if record.Layer == memcore.MemoryLayerExperience {
		if err := s.markSourceFactsDistilled(ctx, record); err != nil {
			return MemoryRecordDTO{}, mapStoreError(err)
		}
	}
	if err := s.publishMemoryLineageLinked(ctx, record); err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryCandidateAccepted,
		SubjectID:  candidate.ID,
		ActorID:    viewer.ViewerID,
		MissionID:  candidate.MissionID,
		Summary:    candidate.Summary,
		SourceRefs: append([]string(nil), candidate.EvidenceRefs...),
		Reason:     strings.TrimSpace(req.Reason),
		OccurredAt: now,
	}); err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryRecordCreated,
		SubjectID:  record.ID,
		ActorID:    viewer.ViewerID,
		MissionID:  candidate.MissionID,
		Summary:    record.Summary,
		SourceRefs: append([]string(nil), record.SourceRefs...),
		Reason:     strings.TrimSpace(req.Reason),
		OccurredAt: now,
	}); err != nil {
		return MemoryRecordDTO{}, err
	}
	return MapRecordDTO(record), nil
}

func (s *Service) ensureCandidateMemoryProfile(ctx context.Context, candidate memcore.MemoryCandidate) (agentcore.MemoryProfileID, error) {
	repository, ok := s.repository.(memoryProfileRepository)
	if !ok {
		return "", ErrServiceDependency
	}
	scope := candidate.ProposedScope
	scope.ID = strings.TrimSpace(scope.ID)
	if scope.ID == "" || (scope.Kind != memcore.ScopeAgent && !memcore.IsCollectiveScope(scope.Kind)) {
		return "", ErrInvalidRequest
	}
	profiles, err := repository.ListProfiles(ctx)
	if err != nil {
		return "", mapStoreError(err)
	}
	for _, profile := range profiles {
		if profile.Enabled && memcore.MemoryProfileMatchesScope(profile, scope) {
			return profile.ID, nil
		}
	}
	owner := memcore.OwnerRef{}
	if scope.Kind == memcore.ScopeAgent {
		owner.AgentID = agentcore.AgentID(scope.ID)
	}
	profile := memcore.NewScopeMemoryProfile(scope, owner)
	if profile.ID == "" || !profile.Enabled {
		return "", ErrInvalidRequest
	}
	if err := repository.SaveProfile(ctx, profile); err != nil {
		return "", mapStoreError(err)
	}
	return profile.ID, nil
}

func (s *Service) publishMemoryRecordMetadata(ctx context.Context, record memcore.MemoryRecord) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	return s.metadataSink.PublishMemoryRecordMetadata(ctx, MemoryRecordAcceptedMetadataEvent(record))
}

// MemoryRecordAcceptedMetadataEvent is the canonical ontology projection event
// for every path that materializes a durable memory record.
func MemoryRecordAcceptedMetadataEvent(record memcore.MemoryRecord) graphprotocol.DomainEvent {
	record = record.Normalized().Clone()
	payload := map[string]any{
		"id":               record.ID,
		"summary":          record.Summary,
		"layer":            string(record.Layer),
		"topic":            record.Topic,
		"scope_kind":       string(record.Scope.Kind),
		"scope_id":         record.Scope.ID,
		"lifecycle_status": string(record.LifecycleStatus),
		"confidence":       record.Confidence,
		"source_refs":      append([]string(nil), record.SourceRefs...),
		"evidence_refs":    append([]string(nil), record.SourceEvidenceRefs...),
	}
	if knowledgeDocumentID := memoryRecordKnowledgeDocumentID(record); knowledgeDocumentID != "" {
		payload["knowledge_document_id"] = knowledgeDocumentID
	}
	if knowledgeChunkID := memoryRecordKnowledgeChunkID(record); knowledgeChunkID != "" {
		payload["knowledge_chunk_id"] = knowledgeChunkID
	}
	if knowledgeAnchorID := memoryRecordKnowledgeAnchorID(record); knowledgeAnchorID != "" {
		payload["knowledge_anchor_id"] = knowledgeAnchorID
	}
	if problemCaseID := memoryRecordProblemCaseID(record); problemCaseID != "" {
		payload["problem_case_id"] = problemCaseID
		payload["problem_lesson_candidate_id"] = record.CandidateID
	}
	return graphprotocol.DomainEvent{
		ID:          "memory-record-accepted:" + record.ID,
		TenantID:    memoryRecordTenantID(record),
		Type:        "memory.record.accepted",
		StreamID:    record.ID,
		Version:     1,
		OccurredAt:  record.UpdatedAt,
		Payload:     payload,
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
	}
}

func (s *Service) publishProblemMemoryRecordAccepted(ctx context.Context, record memcore.MemoryRecord) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	record = record.Normalized().Clone()
	problemCaseID := memoryRecordProblemCaseID(record)
	if problemCaseID == "" || strings.TrimSpace(record.CandidateID) == "" {
		return nil
	}
	return s.metadataSink.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "problem-memory-record-accepted:" + record.ID,
		TenantID:   memoryRecordTenantID(record),
		Type:       "problem.memory_record.accepted",
		StreamID:   record.ID,
		Version:    1,
		OccurredAt: record.UpdatedAt,
		Payload: map[string]any{
			"id":                          record.ID,
			"problem_case_id":             problemCaseID,
			"problem_lesson_candidate_id": record.CandidateID,
			"status":                      "accepted",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
	})
}

func (s *Service) publishMemoryRecordLifecycleUpdated(ctx context.Context, record memcore.MemoryRecord) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	record = record.Normalized().Clone()
	payload := map[string]any{
		"id":                 record.ID,
		"summary":            record.Summary,
		"lifecycle_status":   string(record.LifecycleStatus),
		"reference_count":    record.ReferenceCount,
		"last_referenced_at": formatMemoryEventTime(record.LastReferencedAt),
	}
	return s.metadataSink.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
		ID:          "memory-record-lifecycle-updated:" + record.ID + ":" + string(record.LifecycleStatus),
		TenantID:    memoryRecordTenantID(record),
		Type:        "memory.record.lifecycle.updated",
		StreamID:    record.ID,
		Version:     1,
		OccurredAt:  record.UpdatedAt,
		Payload:     payload,
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
	})
}

func (s *Service) publishMemoryKnowledgeCitationAdded(ctx context.Context, record memcore.MemoryRecord) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	record = record.Normalized().Clone()
	knowledgeDocumentID := memoryRecordKnowledgeDocumentID(record)
	if knowledgeDocumentID == "" {
		return nil
	}
	return s.metadataSink.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "memory-knowledge-citation-added:" + record.ID + ":" + knowledgeDocumentID,
		TenantID:   memoryRecordTenantID(record),
		Type:       "memory.knowledge.citation.added",
		StreamID:   record.ID,
		Version:    1,
		OccurredAt: record.UpdatedAt,
		Payload: map[string]any{
			"memory_record_id":      record.ID,
			"knowledge_document_id": knowledgeDocumentID,
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
	})
}

func (s *Service) publishMemoryKnowledgeChunkCitationAdded(ctx context.Context, record memcore.MemoryRecord) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	record = record.Normalized().Clone()
	knowledgeChunkID := memoryRecordKnowledgeChunkID(record)
	if knowledgeChunkID == "" {
		return nil
	}
	return s.metadataSink.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "memory-knowledge-chunk-citation-added:" + record.ID + ":" + knowledgeChunkID,
		TenantID:   memoryRecordTenantID(record),
		Type:       "memory.knowledge_chunk.citation.added",
		StreamID:   record.ID,
		Version:    1,
		OccurredAt: record.UpdatedAt,
		Payload: map[string]any{
			"memory_record_id":   record.ID,
			"knowledge_chunk_id": knowledgeChunkID,
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
	})
}

func (s *Service) publishMemoryKnowledgeAnchorCitationAdded(ctx context.Context, record memcore.MemoryRecord) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	record = record.Normalized().Clone()
	knowledgeAnchorID := memoryRecordKnowledgeAnchorID(record)
	if knowledgeAnchorID == "" {
		return nil
	}
	return s.metadataSink.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "memory-knowledge-anchor-citation-added:" + record.ID + ":" + knowledgeAnchorID,
		TenantID:   memoryRecordTenantID(record),
		Type:       "memory.knowledge_anchor.citation.added",
		StreamID:   record.ID,
		Version:    1,
		OccurredAt: record.UpdatedAt,
		Payload: map[string]any{
			"memory_record_id":    record.ID,
			"knowledge_anchor_id": knowledgeAnchorID,
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
	})
}

func (s *Service) publishMemoryScopeLinked(ctx context.Context, record memcore.MemoryRecord) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	record = record.Normalized().Clone()
	payload := map[string]any{
		"memory_record_id": record.ID,
	}
	if missionID := memoryRecordMissionScopeID(record); missionID != "" {
		payload["mission_id"] = missionID
	}
	if projectID := memoryRecordProjectScopeID(record); projectID != "" {
		payload["project_id"] = projectID
	}
	if taskID := memoryRecordTaskScopeID(record); taskID != "" {
		payload["task_id"] = taskID
	}
	if len(payload) == 1 {
		return nil
	}
	return s.metadataSink.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
		ID:          "memory-scope-linked:" + record.ID,
		TenantID:    memoryRecordTenantID(record),
		Type:        "memory.scope.linked",
		StreamID:    record.ID,
		Version:     1,
		OccurredAt:  record.UpdatedAt,
		Payload:     payload,
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
	})
}

func (s *Service) publishMemoryLineageLinked(ctx context.Context, record memcore.MemoryRecord) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	record = record.Normalized().Clone()
	switch record.Layer {
	case memcore.MemoryLayerExperience, memcore.MemoryLayerWorld:
	default:
		return nil
	}
	sourceIDs := memoryRecordLineageMemoryIDs(record)
	for _, sourceID := range sourceIDs {
		if err := s.metadataSink.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
			ID:         "memory-lineage-linked:" + record.ID + ":" + sourceID,
			TenantID:   memoryRecordTenantID(record),
			Type:       "memory.lineage.linked",
			StreamID:   record.ID,
			Version:    1,
			OccurredAt: record.UpdatedAt,
			Payload: map[string]any{
				"memory_record_id":        record.ID,
				"source_memory_record_id": sourceID,
			},
			PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		}); err != nil {
			return err
		}
	}
	return nil
}

func formatMemoryEventTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func memoryRecordKnowledgeDocumentID(record memcore.MemoryRecord) string {
	ids := map[string]struct{}{}
	for _, refs := range [][]string{record.SourceRefs, record.SourceEvidenceRefs} {
		for _, ref := range refs {
			id, ok := knowledgeDocumentIDFromRef(ref)
			if !ok {
				continue
			}
			ids[id] = struct{}{}
			if len(ids) > 1 {
				return ""
			}
		}
	}
	for id := range ids {
		return id
	}
	return ""
}

func knowledgeDocumentIDFromRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	const prefix = "knowledge_document:"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
	if id == "" {
		return "", false
	}
	return id, true
}

func memoryRecordKnowledgeChunkID(record memcore.MemoryRecord) string {
	ids := map[string]struct{}{}
	for _, refs := range [][]string{record.SourceRefs, record.SourceEvidenceRefs} {
		for _, ref := range refs {
			id, ok := knowledgeChunkIDFromRef(ref)
			if !ok {
				continue
			}
			ids[id] = struct{}{}
			if len(ids) > 1 {
				return ""
			}
		}
	}
	for id := range ids {
		return id
	}
	return ""
}

func knowledgeChunkIDFromRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	const prefix = "knowledge_chunk:"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
	if id == "" {
		return "", false
	}
	return id, true
}

func memoryRecordKnowledgeAnchorID(record memcore.MemoryRecord) string {
	ids := map[string]struct{}{}
	for _, refs := range [][]string{record.SourceRefs, record.SourceEvidenceRefs} {
		for _, ref := range refs {
			id, ok := knowledgeAnchorIDFromRef(ref)
			if !ok {
				continue
			}
			ids[id] = struct{}{}
			if len(ids) > 1 {
				return ""
			}
		}
	}
	for id := range ids {
		return id
	}
	return ""
}

func knowledgeAnchorIDFromRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	const prefix = "knowledge_anchor:"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
	if id == "" {
		return "", false
	}
	return id, true
}

func memoryRecordKnowledgeRefs(record memcore.MemoryRecord) []string {
	var refs []string
	seen := map[string]struct{}{}
	for _, candidates := range [][]string{record.SourceRefs, record.SourceEvidenceRefs} {
		for _, ref := range candidates {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if _, ok := knowledgeDocumentIDFromRef(ref); !ok {
				if _, ok := knowledgeChunkIDFromRef(ref); !ok {
					if _, ok := knowledgeAnchorIDFromRef(ref); !ok {
						continue
					}
				}
			}
			if _, ok := seen[ref]; ok {
				continue
			}
			seen[ref] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

func memoryRecordLineageMemoryIDs(record memcore.MemoryRecord) []string {
	var ids []string
	seen := map[string]struct{}{}
	for _, ref := range record.LineageRefs {
		id, ok := memoryIDFromRef(ref)
		if !ok {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func memoryRecordProblemCaseID(record memcore.MemoryRecord) string {
	ids := map[string]struct{}{}
	for _, refs := range [][]string{record.SourceRefs, record.LineageRefs, record.SourceEvidenceRefs} {
		for _, ref := range refs {
			id, ok := problemCaseIDFromMemoryRef(ref)
			if !ok {
				continue
			}
			ids[id] = struct{}{}
			if len(ids) > 1 {
				return ""
			}
		}
	}
	for id := range ids {
		return id
	}
	return ""
}

func problemCaseIDFromMemoryRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	const prefix = "problem_case:"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
	return id, id != ""
}

func memoryRecordMissionScopeID(record memcore.MemoryRecord) string {
	if record.Scope.Kind != memcore.ScopeMission {
		return ""
	}
	return strings.TrimSpace(record.Scope.ID)
}

func memoryRecordProjectScopeID(record memcore.MemoryRecord) string {
	if record.Scope.Kind != memcore.ScopeProject {
		return ""
	}
	return strings.TrimSpace(record.Scope.ID)
}

func memoryRecordTaskScopeID(record memcore.MemoryRecord) string {
	for _, refs := range [][]string{record.LineageRefs, record.SourceRefs, record.SourceEvidenceRefs} {
		for _, ref := range refs {
			if id, ok := taskIDFromMemoryRef(ref); ok {
				return id
			}
		}
	}
	return ""
}

func taskIDFromMemoryRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	for _, prefix := range []string{"task:", "task_contract:"} {
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
		if id == "" {
			return "", false
		}
		return id, true
	}
	return "", false
}

func memoryIDFromRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	const prefix = "memory:"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
	if id == "" {
		return "", false
	}
	return id, true
}

func memoryRecordTenantID(record memcore.MemoryRecord) string {
	scopeID := strings.TrimSpace(record.Scope.ID)
	switch record.Scope.Kind {
	case memcore.ScopeOrganization:
		if scopeID != "" {
			return scopeID
		}
	case memcore.ScopeDepartment, memcore.ScopeProject, memcore.ScopeMission, memcore.ScopeDiscussion, memcore.ScopeAgent:
		if scopeID != "" {
			return string(record.Scope.Kind) + ":" + scopeID
		}
	}
	if scopeID != "" {
		return scopeID
	}
	if record.ProfileID != "" {
		return string(record.ProfileID)
	}
	return record.ID
}

func (s *Service) markSourceFactsDistilled(ctx context.Context, record memcore.MemoryRecord) error {
	for _, ref := range record.LineageRefs {
		ref = strings.TrimSpace(ref)
		if !strings.HasPrefix(ref, "memory:") {
			continue
		}
		sourceMemoryID := strings.TrimSpace(strings.TrimPrefix(ref, "memory:"))
		if sourceMemoryID == "" {
			continue
		}
		if err := s.repository.UpdateRecordLifecycle(ctx, sourceMemoryID, memcore.MemoryLifecycleDistilled); err != nil {
			return err
		}
		sourceRecord, err := s.recordByID(ctx, sourceMemoryID)
		if err != nil {
			return err
		}
		if err := s.publishMemoryRecordLifecycleUpdated(ctx, sourceRecord); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RejectCandidate(ctx context.Context, viewer ViewerRef, candidateID string, req RejectCandidateRequest) (CommandReceiptDTO, error) {
	if err := ctx.Err(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if s == nil || s.repository == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryReview); err != nil {
		return CommandReceiptDTO{}, err
	}
	if strings.TrimSpace(req.Reason) == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	candidate, err := s.candidateForReview(ctx, viewer, candidateID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	now := s.now()
	if err := s.repository.UpdateCandidateStatus(ctx, candidate.ID, memcore.CandidateRejected); err != nil {
		return CommandReceiptDTO{}, mapStoreError(err)
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryCandidateRejected,
		SubjectID:  candidate.ID,
		ActorID:    viewer.ViewerID,
		MissionID:  candidate.MissionID,
		Summary:    candidate.Summary,
		SourceRefs: append([]string(nil), candidate.EvidenceRefs...),
		Reason:     strings.TrimSpace(req.Reason),
		OccurredAt: now,
	}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return CommandReceiptDTO{Status: string(memcore.CandidateRejected), CandidateID: candidate.ID, UpdatedAt: now}, nil
}

func (s *Service) MergeCandidate(ctx context.Context, viewer ViewerRef, candidateID string, req MergeCandidateRequest) (CommandReceiptDTO, error) {
	if err := ctx.Err(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if s == nil || s.repository == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryReview); err != nil {
		return CommandReceiptDTO{}, err
	}
	targetID := strings.TrimSpace(req.TargetMemoryID)
	if targetID == "" || strings.TrimSpace(req.Reason) == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	candidate, err := s.candidateForReview(ctx, viewer, candidateID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	now := s.now()
	if err := s.repository.UpdateCandidateStatus(ctx, candidate.ID, memcore.CandidateMerged); err != nil {
		return CommandReceiptDTO{}, mapStoreError(err)
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryCandidateMerged,
		SubjectID:  candidate.ID,
		ActorID:    viewer.ViewerID,
		MissionID:  candidate.MissionID,
		Summary:    candidate.Summary,
		SourceRefs: []string{targetID},
		Reason:     strings.TrimSpace(req.Reason),
		OccurredAt: now,
	}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return CommandReceiptDTO{Status: string(memcore.CandidateMerged), CandidateID: candidate.ID, TargetID: targetID, UpdatedAt: now}, nil
}

func (s *Service) ListRecords(ctx context.Context, viewer ViewerRef, query ListRecordsQuery) (MemoryRecordPageDTO, error) {
	if err := ctx.Err(); err != nil {
		return MemoryRecordPageDTO{}, err
	}
	if s == nil || s.repository == nil {
		return MemoryRecordPageDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryRead); err != nil {
		return MemoryRecordPageDTO{}, err
	}
	records, err := s.repository.FindRecords(ctx, recordQueryFromDTO(query))
	if err != nil {
		return MemoryRecordPageDTO{}, mapStoreError(err)
	}
	page := MemoryRecordPageDTO{Limit: query.Limit}
	for _, record := range records {
		if isPersonalAssistantMemoryRecord(record) && !personalAssistantMemoryViewer(viewer, record.Scope.ID) {
			continue
		}
		if !canReadRecord(viewer, record) {
			continue
		}
		dto, err := s.mapRecordDTO(ctx, record)
		if err != nil {
			return MemoryRecordPageDTO{}, err
		}
		page.Records = append(page.Records, dto)
	}
	page.Total = len(page.Records)
	return page, nil
}

func (s *Service) GetRecord(ctx context.Context, viewer ViewerRef, memoryID string) (MemoryRecordDTO, error) {
	if err := ctx.Err(); err != nil {
		return MemoryRecordDTO{}, err
	}
	if s == nil || s.repository == nil {
		return MemoryRecordDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryRead); err != nil {
		return MemoryRecordDTO{}, err
	}
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return MemoryRecordDTO{}, ErrInvalidRequest
	}
	records, err := s.repository.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true})
	if err != nil {
		return MemoryRecordDTO{}, mapStoreError(err)
	}
	for _, record := range records {
		if record.ID != memoryID {
			continue
		}
		if isPersonalAssistantMemoryRecord(record) && !personalAssistantMemoryViewer(viewer, record.Scope.ID) {
			return MemoryRecordDTO{}, ErrNotFound
		}
		if !canReadRecord(viewer, record) {
			return MemoryRecordDTO{}, ErrPermissionDenied
		}
		return s.mapRecordDTO(ctx, record)
	}
	return MemoryRecordDTO{}, ErrNotFound
}

func (s *Service) mapRecordDTO(ctx context.Context, record memcore.MemoryRecord) (MemoryRecordDTO, error) {
	dto := MapRecordDTO(record)
	if s == nil || s.citationEvidence == nil {
		return dto, nil
	}
	record = record.Normalized().Clone()
	refs := memoryRecordKnowledgeRefs(record)
	if len(refs) == 0 {
		return dto, nil
	}
	evidence, err := s.citationEvidence.ResolveCitationEvidence(ctx, CitationEvidenceQuery{
		MemoryID: record.ID,
		TenantID: memoryRecordTenantID(record),
		Refs:     refs,
	})
	if err != nil {
		return MemoryRecordDTO{}, err
	}
	dto.CitationEvidence = evidence
	return dto, nil
}

func (s *Service) TouchRecords(ctx context.Context, viewer ViewerRef, req TouchMemoryRecordsRequest) (CommandReceiptDTO, error) {
	if err := ctx.Err(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if s == nil || s.repository == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryRead); err != nil {
		return CommandReceiptDTO{}, err
	}
	now := s.now()
	var touched []string
	for _, memoryID := range req.MemoryIDs {
		record, err := s.recordForLifecycle(ctx, viewer, memoryID)
		if err != nil {
			return CommandReceiptDTO{}, err
		}
		record.ReferenceCount++
		record.LastReferencedAt = now
		record.UpdatedAt = now
		if err := s.repository.SaveRecord(ctx, record); err != nil {
			return CommandReceiptDTO{}, mapStoreError(err)
		}
		touched = append(touched, record.ID)
		if err := s.recordAudit(ctx, memaudit.Event{
			Type:       memaudit.EventMemoryRecordReferenced,
			SubjectID:  record.ID,
			ActorID:    viewer.ViewerID,
			Summary:    record.Summary,
			SourceRefs: append([]string(nil), record.SourceRefs...),
			Reason:     strings.TrimSpace(req.Reason),
			OccurredAt: now,
		}); err != nil {
			return CommandReceiptDTO{}, err
		}
		if err := s.publishMemoryRecordLifecycleUpdated(ctx, record); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	if len(touched) == 0 {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	return CommandReceiptDTO{Status: "referenced", TargetID: strings.Join(touched, ","), UpdatedAt: now}, nil
}

func (s *Service) ArchiveRecord(ctx context.Context, viewer ViewerRef, memoryID string, req MemoryLifecycleRequest) (CommandReceiptDTO, error) {
	record, err := s.transitionRecordLifecycle(ctx, viewer, memoryID, req, memcore.MemoryLifecycleArchived, memaudit.EventMemoryRecordArchived)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	return CommandReceiptDTO{Status: string(record.LifecycleStatus), MemoryID: record.ID, UpdatedAt: record.UpdatedAt}, nil
}

func (s *Service) DeleteRecord(ctx context.Context, viewer ViewerRef, memoryID string, req MemoryLifecycleRequest) (CommandReceiptDTO, error) {
	record, err := s.transitionRecordLifecycle(ctx, viewer, memoryID, req, memcore.MemoryLifecycleDeleted, memaudit.EventMemoryRecordDeleted)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	return CommandReceiptDTO{Status: string(record.LifecycleStatus), MemoryID: record.ID, UpdatedAt: record.UpdatedAt}, nil
}

func (s *Service) AnonymizeRecord(ctx context.Context, viewer ViewerRef, memoryID string, req MemoryLifecycleRequest) (MemoryRecordDTO, error) {
	if err := ctx.Err(); err != nil {
		return MemoryRecordDTO{}, err
	}
	if s == nil || s.repository == nil {
		return MemoryRecordDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryReview); err != nil {
		return MemoryRecordDTO{}, err
	}
	record, err := s.recordForLifecycle(ctx, viewer, memoryID)
	if err != nil {
		return MemoryRecordDTO{}, err
	}
	if requiresWorldManageRecord(record) && !viewer.Can(PermissionMemoryWorldManage) {
		return MemoryRecordDTO{}, ErrPermissionDenied
	}
	now := s.now()
	record = anonymizedRecord(record, now)
	if err := s.repository.SaveRecord(ctx, record); err != nil {
		return MemoryRecordDTO{}, mapStoreError(err)
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryRecordAnonymized,
		SubjectID:  record.ID,
		ActorID:    viewer.ViewerID,
		Summary:    record.Summary,
		SourceRefs: append([]string(nil), record.LineageRefs...),
		Reason:     strings.TrimSpace(req.Reason),
		OccurredAt: now,
	}); err != nil {
		return MemoryRecordDTO{}, err
	}
	if err := s.publishMemoryRecordLifecycleUpdated(ctx, record); err != nil {
		return MemoryRecordDTO{}, err
	}
	return MapRecordDTO(record), nil
}

func (s *Service) SweepRetention(ctx context.Context, viewer ViewerRef, req MemoryRetentionSweepRequest) (MemoryRetentionSweepResultDTO, error) {
	if err := ctx.Err(); err != nil {
		return MemoryRetentionSweepResultDTO{}, err
	}
	if s == nil || s.repository == nil {
		return MemoryRetentionSweepResultDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryReview); err != nil {
		return MemoryRetentionSweepResultDTO{}, err
	}
	if req.Limit < 0 {
		return MemoryRetentionSweepResultDTO{}, ErrInvalidRequest
	}
	records, err := s.repository.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true, Limit: req.Limit})
	if err != nil {
		return MemoryRetentionSweepResultDTO{}, mapStoreError(err)
	}
	now := s.now()
	result := MemoryRetentionSweepResultDTO{Status: "swept", UpdatedAt: now}
	for _, record := range records {
		record = record.Normalized()
		if isPersonalAssistantMemoryRecord(record) {
			continue
		}
		result.CheckedCount++
		if !canViewRecord(viewer, record) || (requiresWorldManageRecord(record) && !viewer.Can(PermissionMemoryWorldManage)) || !shouldArchiveByRetention(record, now) {
			continue
		}
		record.LifecycleStatus = memcore.MemoryLifecycleArchived
		record.UpdatedAt = now
		if err := s.repository.SaveRecord(ctx, record); err != nil {
			return MemoryRetentionSweepResultDTO{}, mapStoreError(err)
		}
		result.ArchivedMemoryIDs = append(result.ArchivedMemoryIDs, record.ID)
		if err := s.recordAudit(ctx, memaudit.Event{
			Type:       memaudit.EventMemoryRecordArchived,
			SubjectID:  record.ID,
			ActorID:    viewer.ViewerID,
			Summary:    record.Summary,
			SourceRefs: append([]string(nil), record.SourceRefs...),
			Reason:     strings.TrimSpace(req.Reason),
			OccurredAt: now,
		}); err != nil {
			return MemoryRetentionSweepResultDTO{}, err
		}
		if err := s.publishMemoryRecordLifecycleUpdated(ctx, record); err != nil {
			return MemoryRetentionSweepResultDTO{}, err
		}
	}
	result.ArchivedCount = len(result.ArchivedMemoryIDs)
	return result, nil
}

func (s *Service) transitionRecordLifecycle(ctx context.Context, viewer ViewerRef, memoryID string, req MemoryLifecycleRequest, status memcore.MemoryLifecycleStatus, eventType memaudit.EventType) (memcore.MemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return memcore.MemoryRecord{}, err
	}
	if s == nil || s.repository == nil {
		return memcore.MemoryRecord{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryReview); err != nil {
		return memcore.MemoryRecord{}, err
	}
	record, err := s.recordForLifecycle(ctx, viewer, memoryID)
	if err != nil {
		return memcore.MemoryRecord{}, err
	}
	if requiresWorldManageRecord(record) && !viewer.Can(PermissionMemoryWorldManage) {
		return memcore.MemoryRecord{}, ErrPermissionDenied
	}
	now := s.now()
	record.LifecycleStatus = memcore.NormalizeMemoryLifecycleStatus(status)
	record.UpdatedAt = now
	if err := s.repository.SaveRecord(ctx, record); err != nil {
		return memcore.MemoryRecord{}, mapStoreError(err)
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       eventType,
		SubjectID:  record.ID,
		ActorID:    viewer.ViewerID,
		Summary:    record.Summary,
		SourceRefs: append([]string(nil), record.SourceRefs...),
		Reason:     strings.TrimSpace(req.Reason),
		OccurredAt: now,
	}); err != nil {
		return memcore.MemoryRecord{}, err
	}
	if err := s.publishMemoryRecordLifecycleUpdated(ctx, record); err != nil {
		return memcore.MemoryRecord{}, err
	}
	return record, nil
}

func (s *Service) recordByID(ctx context.Context, memoryID string) (memcore.MemoryRecord, error) {
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return memcore.MemoryRecord{}, ErrInvalidRequest
	}
	records, err := s.repository.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true})
	if err != nil {
		return memcore.MemoryRecord{}, mapStoreError(err)
	}
	for _, record := range records {
		if record.ID == memoryID {
			return record.Normalized().Clone(), nil
		}
	}
	return memcore.MemoryRecord{}, ErrNotFound
}

func (s *Service) recordForLifecycle(ctx context.Context, viewer ViewerRef, memoryID string) (memcore.MemoryRecord, error) {
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return memcore.MemoryRecord{}, ErrInvalidRequest
	}
	records, err := s.repository.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true})
	if err != nil {
		return memcore.MemoryRecord{}, mapStoreError(err)
	}
	for _, record := range records {
		if record.ID != memoryID {
			continue
		}
		if isPersonalAssistantMemoryRecord(record) && !personalAssistantMemoryViewer(viewer, record.Scope.ID) {
			return memcore.MemoryRecord{}, ErrNotFound
		}
		if !canViewRecord(viewer, record) {
			return memcore.MemoryRecord{}, ErrPermissionDenied
		}
		return record.Normalized().Clone(), nil
	}
	return memcore.MemoryRecord{}, ErrNotFound
}

func anonymizedRecord(record memcore.MemoryRecord, now time.Time) memcore.MemoryRecord {
	record = record.Normalized().Clone()
	redactionTerms := memoryEntityRedactionTerms(record.Entities)
	record.Content = ""
	record.Narrative = ""
	record.Summary = redactMemoryText(record.Summary, redactionTerms)
	record.Facts = redactMemoryTexts(record.Facts, redactionTerms)
	record.Lessons = redactMemoryTexts(record.Lessons, redactionTerms)
	record.Preferences = redactMemoryTexts(record.Preferences, redactionTerms)
	record.Warnings = redactMemoryTexts(record.Warnings, redactionTerms)
	record.Decisions = redactMemoryTexts(record.Decisions, redactionTerms)
	record.Tags = redactMemoryTexts(record.Tags, redactionTerms)
	record.Entities = nil
	record.SourceRefs = nil
	record.SourceEvidenceRefs = nil
	record.Sensitivity = memcore.SensitivityNormal
	record.DeIDLevel = memcore.DeIDOrganizationAnonymous
	record.LifecycleStatus = memcore.MemoryLifecycleAnonymized
	record.UpdatedAt = now
	return record
}

func memoryEntityRedactionTerms(entities map[string]string) []string {
	seen := map[string]struct{}{}
	var terms []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if len(value) < 3 {
			return
		}
		for _, candidate := range []string{value, strings.ToLower(value), strings.ToUpper(value)} {
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			terms = append(terms, candidate)
		}
	}
	for _, value := range entities {
		add(value)
		for _, part := range strings.Fields(value) {
			add(part)
		}
	}
	return terms
}

func redactMemoryText(value string, terms []string) string {
	for _, term := range terms {
		value = strings.ReplaceAll(value, term, "[redacted]")
	}
	return value
}

func redactMemoryTexts(values []string, terms []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, redactMemoryText(value, terms))
	}
	return out
}

func shouldArchiveByRetention(record memcore.MemoryRecord, now time.Time) bool {
	return memcore.RetentionDue(record, now)
}

func (s *Service) candidateFromRequest(viewer ViewerRef, req CreateCandidateRequest) (memcore.MemoryCandidate, error) {
	id := strings.TrimSpace(req.CandidateID)
	if id == "" {
		return memcore.MemoryCandidate{}, ErrInvalidRequest
	}
	sourceType := memcore.SourceType(strings.TrimSpace(req.SourceType))
	sourceRef := strings.TrimSpace(req.SourceRef)
	summary := strings.TrimSpace(req.Summary)
	if sourceType == "" || sourceRef == "" || summary == "" {
		return memcore.MemoryCandidate{}, ErrInvalidRequest
	}
	scopeKind := memcore.ScopeKind(strings.TrimSpace(req.ProposedScope.Kind))
	scopeID := strings.TrimSpace(req.ProposedScope.ID)
	if scopeKind == "" || scopeID == "" {
		return memcore.MemoryCandidate{}, ErrInvalidRequest
	}
	now := s.now()
	sensitivity := memcore.NormalizeSensitivity(memcore.Sensitivity(strings.TrimSpace(req.Sensitivity)))
	deIDLevel := memcore.NormalizeDeIDLevel(memcore.DeIDLevel(strings.TrimSpace(req.DeIDLevel)))
	consent := memcore.NormalizeConsentState(memcore.ConsentState(strings.TrimSpace(req.Consent)))
	confidence := req.Confidence
	if confidence <= 0 {
		confidence = 1
	}
	return memcore.MemoryCandidate{
		ID:                id,
		MissionID:         strings.TrimSpace(req.MissionID),
		DiscussionGroupID: strings.TrimSpace(req.DiscussionGroupID),
		SourceType:        sourceType,
		SourceRef:         sourceRef,
		ProposedScope:     memcore.MemoryScope{Kind: scopeKind, ID: scopeID},
		ProposedKind:      agentcore.MemoryKind(strings.TrimSpace(req.ProposedKind)),
		ProposedLayer:     memcore.NormalizeMemoryLayer(memcore.MemoryLayer(strings.TrimSpace(req.ProposedLayer))),
		Topic:             strings.TrimSpace(req.Topic),
		Summary:           summary,
		Narrative:         strings.TrimSpace(req.Narrative),
		Lessons:           append([]string(nil), req.Lessons...),
		Warnings:          append([]string(nil), req.Warnings...),
		Entities:          cloneStringMap(req.Entities),
		Tags:              append([]string(nil), req.Tags...),
		LineageRefs:       append([]string(nil), req.LineageRefs...),
		SupersedesRefs:    append([]string(nil), req.SupersedesRefs...),
		ContradictsRefs:   append([]string(nil), req.ContradictsRefs...),
		Sensitivity:       sensitivity,
		DeIDLevel:         deIDLevel,
		Consent:           consent,
		Confidence:        confidence,
		ReviewRequired:    req.ReviewRequired,
		ReviewReason:      strings.TrimSpace(req.ReviewReason),
		EvidenceRefs:      append([]string(nil), req.EvidenceRefs...),
		CreatedBy:         agentcore.AgentID(viewer.ViewerID),
		Status:            memcore.CandidateProposed,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func recordQueryFromDTO(query ListRecordsQuery) memstore.RecordQuery {
	recordQuery := memstore.RecordQuery{
		IncludeArchived: query.IncludeArchived,
		Limit:           query.Limit,
	}
	if profileID := strings.TrimSpace(query.ProfileID); profileID != "" {
		recordQuery.ProfileIDs = []agentcore.MemoryProfileID{agentcore.MemoryProfileID(profileID)}
	}
	if scopeKind := strings.TrimSpace(query.ScopeKind); scopeKind != "" {
		recordQuery.ScopeKinds = []memcore.ScopeKind{memcore.ScopeKind(scopeKind)}
	}
	if scopeID := strings.TrimSpace(query.ScopeID); scopeID != "" {
		recordQuery.ScopeIDs = []string{scopeID}
	}
	if layer := strings.TrimSpace(query.Layer); layer != "" {
		recordQuery.Layers = []memcore.MemoryLayer{memcore.MemoryLayer(layer)}
	}
	if topic := strings.TrimSpace(query.Topic); topic != "" {
		recordQuery.Topics = []string{topic}
	}
	if status := strings.TrimSpace(query.LifecycleStatus); status != "" {
		recordQuery.LifecycleStatuses = []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleStatus(status)}
	}
	return recordQuery
}

func canViewSensitivity(viewer ViewerRef, sensitivity memcore.Sensitivity) bool {
	sensitivity = memcore.NormalizeSensitivity(sensitivity)
	return sensitivity == memcore.SensitivityNormal || viewer.Can(PermissionMemoryAuditRead)
}

func canViewCandidate(viewer ViewerRef, candidate memcore.MemoryCandidate) bool {
	return canViewSensitivity(viewer, candidate.Sensitivity) && viewerHasScope(viewer, candidate.ProposedScope.Kind, candidate.ProposedScope.ID)
}

func canReadCandidate(viewer ViewerRef, candidate memcore.MemoryCandidate) bool {
	return canViewSensitivity(viewer, candidate.Sensitivity) &&
		(viewer.Can(PermissionMemoryAuditRead) || viewerHasScope(viewer, candidate.ProposedScope.Kind, candidate.ProposedScope.ID))
}

func canViewRecord(viewer ViewerRef, record memcore.MemoryRecord) bool {
	if isPersonalAssistantMemoryRecord(record) {
		return canViewSensitivity(viewer, record.Sensitivity) && personalAssistantMemoryViewer(viewer, record.Scope.ID)
	}
	return canViewSensitivity(viewer, record.Sensitivity) && canViewVisibility(viewer, record.Visibility, record.Scope)
}

func canReadRecord(viewer ViewerRef, record memcore.MemoryRecord) bool {
	if isPersonalAssistantMemoryRecord(record) {
		return canViewSensitivity(viewer, record.Sensitivity) && personalAssistantMemoryViewer(viewer, record.Scope.ID)
	}
	return canViewSensitivity(viewer, record.Sensitivity) &&
		(viewer.Can(PermissionMemoryAuditRead) || canViewVisibility(viewer, record.Visibility, record.Scope))
}

func isPersonalAssistantMemoryRecord(record memcore.MemoryRecord) bool {
	if agentcore.IsPersonalAssistantProfileID(strings.TrimPrefix(string(record.ProfileID), "memory-")) {
		return true
	}
	return record.Scope.Kind == memcore.ScopeAgent && agentcore.IsPersonalAssistantProfileID(record.Scope.ID)
}

func isPersonalAssistantMemoryCandidate(candidate memcore.MemoryCandidate) bool {
	return candidate.ProposedScope.Kind == memcore.ScopeAgent && agentcore.IsPersonalAssistantProfileID(candidate.ProposedScope.ID)
}

func personalAssistantMemoryViewer(viewer ViewerRef, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	return agentcore.IsPersonalAssistantProfileID(agentID) &&
		((viewer.AgentID != "" && strings.TrimSpace(string(viewer.AgentID)) == agentID) || strings.TrimSpace(viewer.ViewerID) == agentID)
}

func requiresWorldManage(candidate memcore.MemoryCandidate) bool {
	return candidate.NormalizedLayer() == memcore.MemoryLayerWorld &&
		candidate.ProposedScope.Kind == memcore.ScopeOrganization
}

func requiresWorldManageRecord(record memcore.MemoryRecord) bool {
	record = record.Normalized()
	return record.Layer == memcore.MemoryLayerWorld && record.Scope.Kind == memcore.ScopeOrganization
}

func canAcceptCandidate(viewer ViewerRef, candidate memcore.MemoryCandidate) bool {
	if requiresWorldManage(candidate) {
		return viewer.Can(PermissionMemoryWorldManage)
	}
	return viewer.Can(PermissionMemoryReview)
}

func canViewVisibility(viewer ViewerRef, visibility memcore.Visibility, fallback memcore.MemoryScope) bool {
	switch visibility.Mode {
	case "", memcore.VisibilityOwnerOnly:
		if viewer.Can(PermissionMemoryAuditRead) {
			return true
		}
		return viewer.AgentID != "" && string(viewer.AgentID) == fallback.ID || viewer.ViewerID != "" && viewer.ViewerID == fallback.ID
	case memcore.VisibilityScopedMembers:
		scope := memcore.MemoryScope{Kind: visibility.ScopeKind, ID: visibility.ScopeID}
		if scope.Kind == "" || scope.ID == "" {
			scope = fallback
		}
		return viewerHasScope(viewer, scope.Kind, scope.ID)
	case memcore.VisibilityOrganization:
		scopeID := strings.TrimSpace(visibility.ScopeID)
		if scopeID == "" && fallback.Kind == memcore.ScopeOrganization {
			scopeID = fallback.ID
		}
		return containsString(viewer.OrganizationIDs, scopeID)
	default:
		return false
	}
}

func viewerHasScope(viewer ViewerRef, kind memcore.ScopeKind, id string) bool {
	switch kind {
	case memcore.ScopeOrganization:
		return containsString(viewer.OrganizationIDs, id)
	case memcore.ScopeDepartment:
		return containsString(viewer.DepartmentIDs, id)
	case memcore.ScopeProject:
		return containsString(viewer.ProjectIDs, id)
	case memcore.ScopeMission:
		return containsString(viewer.MissionIDs, id)
	case memcore.ScopeDiscussion:
		return containsString(viewer.DiscussionIDs, id)
	case memcore.ScopeAgent:
		return viewer.AgentID != "" && string(viewer.AgentID) == id || viewer.ViewerID != "" && viewer.ViewerID == id
	default:
		return false
	}
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func (s *Service) candidateForReview(ctx context.Context, viewer ViewerRef, candidateID string) (memcore.MemoryCandidate, error) {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return memcore.MemoryCandidate{}, ErrInvalidRequest
	}
	candidate, err := s.repository.GetCandidate(ctx, candidateID)
	if err != nil {
		return memcore.MemoryCandidate{}, mapStoreError(err)
	}
	if isPersonalAssistantMemoryCandidate(candidate) && !personalAssistantMemoryViewer(viewer, candidate.ProposedScope.ID) {
		return memcore.MemoryCandidate{}, ErrNotFound
	}
	if !canReadCandidate(viewer, candidate) {
		return memcore.MemoryCandidate{}, ErrPermissionDenied
	}
	if candidate.Status != memcore.CandidateProposed {
		return memcore.MemoryCandidate{}, ErrConflict
	}
	return candidate, nil
}

func (s *Service) recordAudit(ctx context.Context, event memaudit.Event) error {
	if s == nil || s.audit == nil {
		return nil
	}
	return s.audit.RecordMemoryAudit(ctx, event)
}

func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, memcore.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, memcore.ErrCandidateRequired):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	case errors.Is(err, memcore.ErrInvalid):
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	default:
		return err
	}
}
