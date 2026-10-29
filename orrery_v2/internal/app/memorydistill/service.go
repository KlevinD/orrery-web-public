package memorydistill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/memory/distillation"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
)

type Repository interface {
	SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error
	ListProfiles(ctx context.Context) ([]memcore.MemoryProfile, error)
	SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error
	SaveRecord(ctx context.Context, record memcore.MemoryRecord) error
	GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error)
	FindRecords(ctx context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error)
}

type AuditRecorder interface {
	RecordMemoryAudit(ctx context.Context, event memaudit.Event) error
}

type ClockFunc func() time.Time

type Service struct {
	repository   Repository
	audit        AuditRecorder
	extractor    *distillation.ExperienceExtractor
	consolidator *distillation.WorldConsolidator
	recall       SemanticRecaller
	synthesizer  NaturalLanguageSynthesizer
	now          ClockFunc
}

type SemanticRecaller interface {
	Recall(context.Context, mempageindex.RecallRequest) ([]memcore.MemoryRecord, error)
}

func (s *Service) WithSemanticRecall(recall SemanticRecaller) *Service {
	if s != nil {
		s.recall = recall
	}
	return s
}

func (s *Service) WithNaturalLanguageSynthesizer(synthesizer NaturalLanguageSynthesizer) *Service {
	if s != nil {
		s.synthesizer = synthesizer
	}
	return s
}

func NewService(repository Repository, audit AuditRecorder, now ClockFunc) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		repository:   repository,
		audit:        audit,
		extractor:    distillation.NewExperienceExtractor(distillation.ExperienceExtractorOptions{Now: distillation.ClockFunc(now)}),
		consolidator: distillation.NewWorldConsolidator(distillation.WorldConsolidatorOptions{Now: distillation.ClockFunc(now)}),
		now:          now,
	}
}

func (s *Service) DistillExperience(ctx context.Context, viewer ViewerRef, req DistillExperienceRequest) (DistillExperienceResponse, error) {
	if err := ctx.Err(); err != nil {
		return DistillExperienceResponse{}, err
	}
	if s == nil || s.repository == nil || s.extractor == nil {
		return DistillExperienceResponse{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryDistill); err != nil {
		return DistillExperienceResponse{}, err
	}
	topic := strings.TrimSpace(req.Topic)
	targetScope := scopeFromDTO(req.TargetScope)
	if topic == "" || !isExperienceTargetScope(targetScope.Kind) || strings.TrimSpace(targetScope.ID) == "" {
		return DistillExperienceResponse{}, ErrInvalidRequest
	}
	if !viewerHasScope(viewer, targetScope.Kind, targetScope.ID) {
		return DistillExperienceResponse{}, ErrPermissionDenied
	}
	sourceScope := scopeFromDTO(req.SourceScope)
	if sourceScope.Kind != "" || strings.TrimSpace(sourceScope.ID) != "" {
		if sourceScope.Kind == "" || strings.TrimSpace(sourceScope.ID) == "" {
			return DistillExperienceResponse{}, ErrInvalidRequest
		}
		if !isExperienceSourceScope(sourceScope.Kind) {
			return DistillExperienceResponse{}, ErrInvalidRequest
		}
		if !viewerHasScope(viewer, sourceScope.Kind, sourceScope.ID) {
			return DistillExperienceResponse{}, ErrPermissionDenied
		}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	query := memstore.RecordQuery{
		Layers:            []memcore.MemoryLayer{memcore.MemoryLayerFact},
		Topics:            []string{topic},
		Tags:              []string{topic},
		LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleActive, memcore.MemoryLifecycleSettled},
		ScopeKinds:        experienceSourceScopes(),
		IncludeArchived:   true,
		Limit:             sourceCandidateLimit(limit),
	}
	if sourceScope.Kind != "" {
		query.ScopeKinds = []memcore.ScopeKind{sourceScope.Kind}
		query.ScopeIDs = []string{sourceScope.ID}
	}
	records, err := s.repository.FindRecords(ctx, query)
	if err != nil {
		return DistillExperienceResponse{}, err
	}
	visible := make([]memcore.MemoryRecord, 0, len(records))
	for _, record := range records {
		if canViewRecord(viewer, record) && safeCollectiveDistillationSource(record) {
			visible = append(visible, record)
		}
	}
	if len(visible) == 0 {
		return DistillExperienceResponse{}, ErrNoCandidates
	}
	visible = naturalLanguageSourceRecords(visible)
	visible = limitSourceRecords(visible, limit)
	visible, err = s.recallSourceRecords(ctx, viewer, visible, experienceRecallQuery(topic), limit)
	if err != nil {
		return DistillExperienceResponse{}, err
	}
	if len(visible) == 0 {
		return DistillExperienceResponse{}, ErrNoCandidates
	}
	now := s.now()
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryDistillationStarted,
		SubjectID:  targetScope.ID,
		ActorID:    viewer.ViewerID,
		Summary:    "experience distillation started for topic " + topic,
		OccurredAt: now,
	}); err != nil {
		return DistillExperienceResponse{}, err
	}
	result, err := s.extractor.Extract(visible, distillation.ExperienceRequest{
		TargetScope: targetScope,
		Topic:       topic,
		CreatedBy:   agentcore.AgentID(strings.TrimSpace(viewer.ViewerID)),
	})
	if err != nil {
		if errors.Is(err, distillation.ErrInsufficientFacts) {
			return DistillExperienceResponse{}, ErrNoCandidates
		}
		if errors.Is(err, distillation.ErrInvalidRequest) || errors.Is(err, distillation.ErrSensitiveScope) {
			return DistillExperienceResponse{}, ErrInvalidRequest
		}
		return DistillExperienceResponse{}, err
	}
	if len(result.Candidates) == 0 {
		return DistillExperienceResponse{}, ErrNoCandidates
	}
	if s.synthesizer != nil {
		synthesis, synthErr := s.synthesizer.Synthesize(ctx, NaturalLanguageSynthesisRequest{
			Layer:       memcore.MemoryLayerExperience,
			TargetScope: targetScope,
			Topic:       topic,
			Language:    viewer.Language,
			Sources:     recordsWithIDs(visible, result.SourceMemoryIDs),
		})
		if synthErr != nil {
			return DistillExperienceResponse{}, synthErr
		}
		result.Candidates[0] = applyNaturalLanguageSynthesis(result.Candidates[0], synthesis, naturalCandidateID(memcore.MemoryLayerExperience, targetScope, topic, result.SourceMemoryIDs), firstNonBlank(synthesis.Language, viewer.Language))
	}
	response := DistillExperienceResponse{
		SourceMemoryIDs: append([]string(nil), result.SourceMemoryIDs...),
	}
	for _, candidate := range result.Candidates {
		if err := s.repository.SaveCandidate(ctx, candidate); err != nil {
			return DistillExperienceResponse{}, err
		}
		if _, err := s.materializeAcceptedCandidate(ctx, candidate); err != nil {
			return DistillExperienceResponse{}, err
		}
		response.Candidates = append(response.Candidates, mapCandidateDTO(candidate))
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryDistillationCompleted,
		SubjectID:  targetScope.ID,
		ActorID:    viewer.ViewerID,
		Summary:    "experience distillation completed for topic " + topic,
		SourceRefs: result.SourceMemoryIDs,
		OccurredAt: now,
	}); err != nil {
		return DistillExperienceResponse{}, err
	}
	return response, nil
}

func (s *Service) DistillWorld(ctx context.Context, viewer ViewerRef, req DistillWorldRequest) (DistillWorldResponse, error) {
	if err := ctx.Err(); err != nil {
		return DistillWorldResponse{}, err
	}
	if s == nil || s.repository == nil || s.consolidator == nil {
		return DistillWorldResponse{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionMemoryDistill); err != nil {
		return DistillWorldResponse{}, err
	}
	topic := strings.TrimSpace(req.Topic)
	targetScope := scopeFromDTO(req.TargetScope)
	if topic == "" || !isWorldTargetScope(targetScope.Kind) || strings.TrimSpace(targetScope.ID) == "" {
		return DistillWorldResponse{}, ErrInvalidRequest
	}
	if !viewerHasScope(viewer, targetScope.Kind, targetScope.ID) {
		return DistillWorldResponse{}, ErrPermissionDenied
	}
	sourceScope := scopeFromDTO(req.SourceScope)
	if sourceScope.Kind != "" || strings.TrimSpace(sourceScope.ID) != "" {
		if sourceScope.Kind == "" || strings.TrimSpace(sourceScope.ID) == "" {
			return DistillWorldResponse{}, ErrInvalidRequest
		}
		if !isWorldSourceScope(sourceScope.Kind) {
			return DistillWorldResponse{}, ErrInvalidRequest
		}
		if !viewerHasScope(viewer, sourceScope.Kind, sourceScope.ID) {
			return DistillWorldResponse{}, ErrPermissionDenied
		}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	sourceQuery := memstore.RecordQuery{
		Layers:            []memcore.MemoryLayer{memcore.MemoryLayerFact, memcore.MemoryLayerExperience},
		Topics:            []string{topic},
		Tags:              []string{topic},
		LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleActive, memcore.MemoryLifecycleSettled},
		ScopeKinds:        worldSourceScopes(),
		IncludeArchived:   true,
		Limit:             sourceCandidateLimit(limit),
	}
	if sourceScope.Kind != "" {
		sourceQuery.ScopeKinds = []memcore.ScopeKind{sourceScope.Kind}
		sourceQuery.ScopeIDs = []string{sourceScope.ID}
	}
	records, err := s.repository.FindRecords(ctx, sourceQuery)
	if err != nil {
		return DistillWorldResponse{}, err
	}
	worldRecords, err := s.repository.FindRecords(ctx, memstore.RecordQuery{
		Layers:            []memcore.MemoryLayer{memcore.MemoryLayerWorld},
		Topics:            []string{topic},
		Tags:              []string{topic},
		LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleActive, memcore.MemoryLifecycleSettled},
		ScopeKinds:        []memcore.ScopeKind{targetScope.Kind},
		ScopeIDs:          []string{targetScope.ID},
		IncludeArchived:   true,
		Limit:             limit,
	})
	if err != nil {
		return DistillWorldResponse{}, err
	}
	records = append(records, worldRecords...)
	visible := make([]memcore.MemoryRecord, 0, len(records))
	for _, record := range records {
		if canViewRecord(viewer, record) && safeCollectiveDistillationSource(record) {
			visible = append(visible, record)
		}
	}
	if len(visible) == 0 {
		return DistillWorldResponse{}, ErrNoCandidates
	}
	visible = naturalLanguageSourceRecords(visible)
	visible = limitSourceRecords(visible, limit)
	visible, err = s.recallSourceRecords(ctx, viewer, visible, worldRecallQuery(topic), limit)
	if err != nil {
		return DistillWorldResponse{}, err
	}
	if len(visible) == 0 {
		return DistillWorldResponse{}, ErrNoCandidates
	}
	now := s.now()
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryWorldConsolidationStarted,
		SubjectID:  targetScope.ID,
		ActorID:    viewer.ViewerID,
		Summary:    "world consolidation started for topic " + topic,
		OccurredAt: now,
	}); err != nil {
		return DistillWorldResponse{}, err
	}
	result, err := s.consolidator.Consolidate(visible, distillation.WorldRequest{
		TargetScope: targetScope,
		Topic:       topic,
		CreatedBy:   agentcore.AgentID(strings.TrimSpace(viewer.ViewerID)),
	})
	if err != nil {
		if errors.Is(err, distillation.ErrInsufficientWorldSources) {
			return DistillWorldResponse{}, ErrNoCandidates
		}
		if errors.Is(err, distillation.ErrInvalidRequest) || errors.Is(err, distillation.ErrSensitiveScope) {
			return DistillWorldResponse{}, ErrInvalidRequest
		}
		return DistillWorldResponse{}, err
	}
	if len(result.Candidates) == 0 {
		return DistillWorldResponse{}, ErrNoCandidates
	}
	if s.synthesizer != nil {
		synthesis, synthErr := s.synthesizer.Synthesize(ctx, NaturalLanguageSynthesisRequest{
			Layer:       memcore.MemoryLayerWorld,
			TargetScope: targetScope,
			Topic:       topic,
			Language:    viewer.Language,
			Sources:     recordsWithIDs(visible, result.SourceMemoryIDs),
		})
		if synthErr != nil {
			return DistillWorldResponse{}, synthErr
		}
		result.Candidates[0] = applyNaturalLanguageSynthesis(result.Candidates[0], synthesis, naturalCandidateID(memcore.MemoryLayerWorld, targetScope, topic, result.SourceMemoryIDs), firstNonBlank(synthesis.Language, viewer.Language))
	}
	response := DistillWorldResponse{
		SourceMemoryIDs: append([]string(nil), result.SourceMemoryIDs...),
	}
	for _, candidate := range result.Candidates {
		if err := s.repository.SaveCandidate(ctx, candidate); err != nil {
			return DistillWorldResponse{}, err
		}
		if _, err := s.materializeAcceptedCandidate(ctx, candidate); err != nil {
			return DistillWorldResponse{}, err
		}
		response.Candidates = append(response.Candidates, mapWorldCandidateDTO(candidate))
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryWorldConsolidationCompleted,
		SubjectID:  targetScope.ID,
		ActorID:    viewer.ViewerID,
		Summary:    "world consolidation completed for topic " + topic,
		SourceRefs: result.SourceMemoryIDs,
		OccurredAt: now,
	}); err != nil {
		return DistillWorldResponse{}, err
	}
	return response, nil
}

func sourceCandidateLimit(limit int) int {
	if limit <= 0 {
		return 500
	}
	limit *= 10
	if limit > 1000 {
		return 1000
	}
	return limit
}

func limitSourceRecords(records []memcore.MemoryRecord, limit int) []memcore.MemoryRecord {
	if limit <= 0 || len(records) <= limit {
		return records
	}
	return records[:limit]
}

func isExperienceTargetScope(kind memcore.ScopeKind) bool {
	switch kind {
	case memcore.ScopeProject, memcore.ScopeDepartment, memcore.ScopeOrganization:
		return true
	default:
		return false
	}
}

func isExperienceSourceScope(kind memcore.ScopeKind) bool {
	switch kind {
	case memcore.ScopeMission, memcore.ScopeDiscussion, memcore.ScopeProject, memcore.ScopeDepartment, memcore.ScopeOrganization:
		return true
	default:
		return false
	}
}

func isWorldTargetScope(kind memcore.ScopeKind) bool {
	return kind == memcore.ScopeOrganization
}

func isWorldSourceScope(kind memcore.ScopeKind) bool {
	switch kind {
	case memcore.ScopeProject, memcore.ScopeDepartment, memcore.ScopeOrganization:
		return true
	default:
		return false
	}
}

func worldSourceScopes() []memcore.ScopeKind {
	return []memcore.ScopeKind{
		memcore.ScopeProject,
		memcore.ScopeDepartment,
		memcore.ScopeOrganization,
	}
}

func experienceSourceScopes() []memcore.ScopeKind {
	return []memcore.ScopeKind{
		memcore.ScopeMission,
		memcore.ScopeDiscussion,
		memcore.ScopeProject,
		memcore.ScopeDepartment,
		memcore.ScopeOrganization,
	}
}

func safeCollectiveDistillationSource(record memcore.MemoryRecord) bool {
	return memcore.NormalizeSensitivity(record.Sensitivity) == memcore.SensitivityNormal
}

func (s *Service) recordAudit(ctx context.Context, event memaudit.Event) error {
	if s == nil || s.audit == nil {
		return nil
	}
	return s.audit.RecordMemoryAudit(ctx, event)
}

func (s *Service) materializeAcceptedCandidate(ctx context.Context, candidate memcore.MemoryCandidate) (memcore.MemoryRecord, error) {
	if !candidate.CanCreateRecordDirectly() {
		return memcore.MemoryRecord{}, memcore.ErrCandidateRequired
	}
	recordID := memcore.RecordIdentifierForCandidate(distillationRecordIDSegment(candidate.ID))
	if existing, err := s.repository.GetRecord(ctx, recordID); err == nil {
		if candidateLanguage := naturalLanguageTag(candidate.Tags); candidateLanguage != "" && candidateLanguage != naturalLanguageTag(existing.Tags) {
			return s.updateLocalizedNaturalLanguageRecord(ctx, existing, candidate)
		}
		return existing, nil
	} else if !errors.Is(err, memcore.ErrNotFound) {
		return memcore.MemoryRecord{}, err
	}
	profileID, err := s.ensureScopeProfile(ctx, candidate.ProposedScope)
	if err != nil {
		return memcore.MemoryRecord{}, err
	}
	now := candidate.CreatedAt.UTC()
	if now.IsZero() {
		now = s.now().UTC()
	}
	visibility := memcore.Visibility{
		Mode:      memcore.VisibilityScopedMembers,
		ScopeKind: candidate.ProposedScope.Kind,
		ScopeID:   candidate.ProposedScope.ID,
	}
	if candidate.ProposedScope.Kind == memcore.ScopeOrganization && candidate.NormalizedLayer() == memcore.MemoryLayerWorld {
		visibility.Mode = memcore.VisibilityOrganization
	}
	sourceRefs := candidateLineageSourceRefs(candidate)
	record := memcore.MemoryRecord{
		ID:                 recordID,
		ProfileID:          profileID,
		CandidateID:        candidate.ID,
		Scope:              candidate.ProposedScope,
		Layer:              candidate.NormalizedLayer(),
		Topic:              candidate.Topic,
		SourceRefs:         sourceRefs,
		SourceEvidenceRefs: append([]string(nil), candidate.EvidenceRefs...),
		Content:            firstNonBlank(candidate.Narrative, candidate.Summary),
		Summary:            candidate.Summary,
		Narrative:          candidate.Narrative,
		Facts:              []string{candidate.Summary},
		Lessons:            append([]string(nil), candidate.Lessons...),
		Warnings:           append([]string(nil), candidate.Warnings...),
		Tags:               append([]string(nil), candidate.Tags...),
		Entities:           cloneStringMap(candidate.Entities),
		LineageRefs:        append([]string(nil), candidate.LineageRefs...),
		SupersedesRefs:     append([]string(nil), candidate.SupersedesRefs...),
		ContradictsRefs:    append([]string(nil), candidate.ContradictsRefs...),
		Sensitivity:        memcore.NormalizeSensitivity(candidate.Sensitivity),
		DeIDLevel:          memcore.NormalizeDeIDLevel(candidate.DeIDLevel),
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Confidence:         candidate.Confidence,
		Visibility:         visibility,
		ValidFrom:          now,
		CreatedBy:          candidate.CreatedBy,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if record.Confidence <= 0 {
		record.Confidence = 1
	}
	if errs := memcore.ValidateMemoryRecord(record); len(errs) > 0 {
		return memcore.MemoryRecord{}, errors.New(strings.Join(errs, "; "))
	}
	if err := s.repository.SaveRecord(ctx, record); err != nil {
		return memcore.MemoryRecord{}, err
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryRecordCreated,
		SubjectID:  record.ID,
		ActorID:    string(record.CreatedBy),
		Summary:    record.Summary,
		SourceRefs: append([]string(nil), record.SourceRefs...),
		OccurredAt: now,
	}); err != nil {
		return memcore.MemoryRecord{}, err
	}
	return record, nil
}

func (s *Service) updateLocalizedNaturalLanguageRecord(ctx context.Context, record memcore.MemoryRecord, candidate memcore.MemoryCandidate) (memcore.MemoryRecord, error) {
	now := s.now().UTC()
	record.Content = firstNonBlank(candidate.Narrative, candidate.Summary)
	record.Summary = candidate.Summary
	record.Narrative = candidate.Narrative
	record.Facts = []string{candidate.Summary}
	record.Lessons = append([]string(nil), candidate.Lessons...)
	record.Warnings = append([]string(nil), candidate.Warnings...)
	record.Tags = append([]string(nil), candidate.Tags...)
	record.Entities = cloneStringMap(candidate.Entities)
	record.LineageRefs = append([]string(nil), candidate.LineageRefs...)
	record.SupersedesRefs = append([]string(nil), candidate.SupersedesRefs...)
	record.ContradictsRefs = append([]string(nil), candidate.ContradictsRefs...)
	record.SourceRefs = candidateLineageSourceRefs(candidate)
	record.SourceEvidenceRefs = append([]string(nil), candidate.EvidenceRefs...)
	record.Confidence = candidate.Confidence
	record.UpdatedAt = now
	if errs := memcore.ValidateMemoryRecord(record); len(errs) > 0 {
		return memcore.MemoryRecord{}, errors.New(strings.Join(errs, "; "))
	}
	if err := s.repository.SaveRecord(ctx, record); err != nil {
		return memcore.MemoryRecord{}, err
	}
	if err := s.recordAudit(ctx, memaudit.Event{
		Type:       memaudit.EventMemoryRecordUpdated,
		SubjectID:  record.ID,
		ActorID:    string(candidate.CreatedBy),
		Summary:    record.Summary,
		SourceRefs: append([]string(nil), record.SourceRefs...),
		OccurredAt: now,
	}); err != nil {
		return memcore.MemoryRecord{}, err
	}
	return record.Clone(), nil
}

func (s *Service) ensureScopeProfile(ctx context.Context, scope memcore.MemoryScope) (agentcore.MemoryProfileID, error) {
	profiles, err := s.repository.ListProfiles(ctx)
	if err != nil {
		return "", err
	}
	for _, profile := range profiles {
		if profile.Enabled && memcore.MemoryProfileMatchesScope(profile, scope) {
			return profile.ID, nil
		}
	}
	owner := memcore.OwnerRef{}
	switch scope.Kind {
	case memcore.ScopeOrganization:
		owner.OrganizationID = scope.ID
	case memcore.ScopeDepartment:
		owner.DepartmentID = scope.ID
	case memcore.ScopeProject:
		owner.ProjectID = scope.ID
	case memcore.ScopeAgent:
		owner.AgentID = agentcore.AgentID(scope.ID)
	}
	profile := memcore.NewScopeMemoryProfile(scope, owner)
	if profile.ID == "" || !profile.Enabled {
		return "", memcore.ErrInvalid
	}
	if err := s.repository.SaveProfile(ctx, profile); err != nil {
		return "", err
	}
	return profile.ID, nil
}

func candidateLineageSourceRefs(candidate memcore.MemoryCandidate) []string {
	refs := make([]string, 0, 1+len(candidate.LineageRefs))
	if sourceType, sourceRef := strings.TrimSpace(string(candidate.SourceType)), strings.TrimSpace(candidate.SourceRef); sourceType != "" && sourceRef != "" {
		refs = append(refs, sourceType+":"+sourceRef)
	}
	return append(refs, candidate.LineageRefs...)
}

func distillationRecordIDSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func applyNaturalLanguageSynthesis(candidate memcore.MemoryCandidate, synthesis NaturalLanguageSynthesis, id, language string) memcore.MemoryCandidate {
	candidate.ID = id
	candidate.Summary = synthesis.Summary
	candidate.Narrative = synthesis.Narrative
	candidate.Lessons = append([]string(nil), synthesis.Lessons...)
	candidate.Warnings = append([]string(nil), synthesis.Warnings...)
	candidate.Tags = setNaturalLanguageTag(candidate.Tags, normalizeNaturalLanguageLocale(language))
	return candidate
}

const naturalLanguageTagPrefix = "language:"

func setNaturalLanguageTag(tags []string, language string) []string {
	out := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(tag)), naturalLanguageTagPrefix) {
			out = append(out, tag)
		}
	}
	return append(out, naturalLanguageTagPrefix+normalizeNaturalLanguageLocale(language))
}

func naturalLanguageTag(tags []string) string {
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if strings.HasPrefix(strings.ToLower(tag), naturalLanguageTagPrefix) {
			return normalizeNaturalLanguageLocale(strings.TrimSpace(tag[len(naturalLanguageTagPrefix):]))
		}
	}
	return ""
}

func naturalCandidateID(layer memcore.MemoryLayer, scope memcore.MemoryScope, topic string, sourceIDs []string) string {
	sum := sha256.Sum256([]byte(strings.Join(sourceIDs, "\x00")))
	value := "candidate." + string(layer) + ".natural.v1." + distillationRecordIDSegment(string(scope.Kind)) + "." +
		distillationRecordIDSegment(scope.ID) + "." + distillationRecordIDSegment(topic) + "." + hex.EncodeToString(sum[:8])
	return memcore.BoundedIdentifier(value, "candidate."+string(layer)+".natural")
}

func scopeFromDTO(dto MemoryScopeDTO) memcore.MemoryScope {
	return memcore.MemoryScope{Kind: memcore.ScopeKind(strings.TrimSpace(dto.Kind)), ID: strings.TrimSpace(dto.ID)}
}

func mapScopeDTO(scope memcore.MemoryScope) MemoryScopeDTO {
	return MemoryScopeDTO{Kind: string(scope.Kind), ID: scope.ID}
}

func mapCandidateDTO(candidate memcore.MemoryCandidate) ExperienceCandidateDTO {
	candidate = candidate.Clone()
	return ExperienceCandidateDTO{
		CandidateID:    candidate.ID,
		ProposedLayer:  string(candidate.NormalizedLayer()),
		ProposedScope:  mapScopeDTO(candidate.ProposedScope),
		Topic:          candidate.Topic,
		Summary:        candidate.Summary,
		Lessons:        append([]string(nil), candidate.Lessons...),
		Warnings:       append([]string(nil), candidate.Warnings...),
		LineageRefs:    append([]string(nil), candidate.LineageRefs...),
		EvidenceRefs:   append([]string(nil), candidate.EvidenceRefs...),
		Confidence:     candidate.Confidence,
		ReviewRequired: candidate.ReviewRequired,
		ReviewReason:   candidate.ReviewReason,
		CreatedAt:      candidate.CreatedAt,
	}
}

func mapWorldCandidateDTO(candidate memcore.MemoryCandidate) WorldCandidateDTO {
	candidate = candidate.Clone()
	return WorldCandidateDTO{
		CandidateID:     candidate.ID,
		ProposedLayer:   string(candidate.NormalizedLayer()),
		ProposedScope:   mapScopeDTO(candidate.ProposedScope),
		Topic:           candidate.Topic,
		Summary:         candidate.Summary,
		Narrative:       candidate.Narrative,
		Lessons:         append([]string(nil), candidate.Lessons...),
		Warnings:        append([]string(nil), candidate.Warnings...),
		LineageRefs:     append([]string(nil), candidate.LineageRefs...),
		SupersedesRefs:  append([]string(nil), candidate.SupersedesRefs...),
		ContradictsRefs: append([]string(nil), candidate.ContradictsRefs...),
		EvidenceRefs:    append([]string(nil), candidate.EvidenceRefs...),
		Confidence:      candidate.Confidence,
		ReviewRequired:  candidate.ReviewRequired,
		ReviewReason:    candidate.ReviewReason,
		CreatedAt:       candidate.CreatedAt,
	}
}
