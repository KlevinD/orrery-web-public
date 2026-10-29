package memoryadmin

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

func MapCandidateDTO(candidate memcore.MemoryCandidate) MemoryCandidateDTO {
	candidate = candidate.Clone()
	candidate.ProposedLayer = memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
	candidate.Sensitivity = memcore.NormalizeSensitivity(candidate.Sensitivity)
	candidate.DeIDLevel = memcore.NormalizeDeIDLevel(candidate.DeIDLevel)
	candidate.Consent = memcore.NormalizeConsentState(candidate.Consent)
	return MemoryCandidateDTO{
		CandidateID:       candidate.ID,
		MissionID:         candidate.MissionID,
		DiscussionGroupID: candidate.DiscussionGroupID,
		SourceType:        string(candidate.SourceType),
		SourceRef:         candidate.SourceRef,
		ProposedLayer:     string(candidate.ProposedLayer),
		ProposedKind:      string(candidate.ProposedKind),
		ProposedScope:     mapScope(candidate.ProposedScope),
		Topic:             candidate.Topic,
		Summary:           candidate.Summary,
		Narrative:         candidate.Narrative,
		Lessons:           append([]string(nil), candidate.Lessons...),
		Warnings:          append([]string(nil), candidate.Warnings...),
		Entities:          cloneStringMap(candidate.Entities),
		Tags:              append([]string(nil), candidate.Tags...),
		EvidenceRefs:      append([]string(nil), candidate.EvidenceRefs...),
		LineageRefs:       append([]string(nil), candidate.LineageRefs...),
		SupersedesRefs:    append([]string(nil), candidate.SupersedesRefs...),
		ContradictsRefs:   append([]string(nil), candidate.ContradictsRefs...),
		Sensitivity:       string(candidate.Sensitivity),
		DeIDLevel:         string(candidate.DeIDLevel),
		Consent:           string(candidate.Consent),
		Confidence:        candidate.Confidence,
		ReviewRequired:    candidate.ReviewRequired,
		ReviewReason:      candidate.ReviewReason,
		Status:            string(candidate.Status),
		CreatedBy:         string(candidate.CreatedBy),
		CreatedAt:         candidate.CreatedAt,
		UpdatedAt:         candidate.UpdatedAt,
	}
}

func MapRecordDTO(record memcore.MemoryRecord) MemoryRecordDTO {
	record = record.Normalized().Clone()
	return MemoryRecordDTO{
		MemoryID:           record.ID,
		ProfileID:          string(record.ProfileID),
		CandidateID:        record.CandidateID,
		Layer:              string(record.Layer),
		Scope:              mapScope(record.Scope),
		Topic:              record.Topic,
		Summary:            record.Summary,
		Content:            record.Content,
		Narrative:          record.Narrative,
		Lessons:            append([]string(nil), record.Lessons...),
		Facts:              append([]string(nil), record.Facts...),
		Preferences:        append([]string(nil), record.Preferences...),
		Warnings:           append([]string(nil), record.Warnings...),
		Decisions:          append([]string(nil), record.Decisions...),
		Tags:               append([]string(nil), record.Tags...),
		SourceMemoryRefs:   append([]string(nil), record.SourceRefs...),
		SourceEvidenceRefs: append([]string(nil), record.SourceEvidenceRefs...),
		LineageRefs:        append([]string(nil), record.LineageRefs...),
		SupersedesRefs:     append([]string(nil), record.SupersedesRefs...),
		ContradictsRefs:    append([]string(nil), record.ContradictsRefs...),
		Confidence:         record.Confidence,
		Visibility: MemoryVisibilityDTO{
			Mode:      string(record.Visibility.Mode),
			ScopeKind: string(record.Visibility.ScopeKind),
			ScopeID:   record.Visibility.ScopeID,
		},
		Sensitivity:      string(record.Sensitivity),
		DeIDLevel:        string(record.DeIDLevel),
		LifecycleStatus:  string(record.LifecycleStatus),
		ReferenceCount:   record.ReferenceCount,
		LastReferencedAt: record.LastReferencedAt,
		CreatedBy:        string(record.CreatedBy),
		CreatedAt:        record.CreatedAt,
		UpdatedAt:        record.UpdatedAt,
	}
}

func buildRecordFromCandidate(candidate memcore.MemoryCandidate, req AcceptCandidateRequest, acceptedBy string, now time.Time) (memcore.MemoryRecord, error) {
	profileID := strings.TrimSpace(req.ProfileID)
	if profileID == "" {
		return memcore.MemoryRecord{}, ErrInvalidRequest
	}
	recordID := strings.TrimSpace(req.MemoryID)
	if recordID == "" {
		recordID = defaultRecordID(candidate.ID)
	}
	confidence := req.Confidence
	if confidence <= 0 {
		confidence = candidate.Confidence
	}
	if confidence <= 0 {
		confidence = 1
	}
	sourceRef := candidateSourceRef(candidate)
	sourceRefs := []string{}
	if sourceRef != "" {
		sourceRefs = append(sourceRefs, sourceRef)
	}
	sourceRefs = append(sourceRefs, candidate.LineageRefs...)
	createdBy := candidate.CreatedBy
	if createdBy == "" {
		createdBy = agentcore.AgentID(acceptedBy)
	}
	record := memcore.MemoryRecord{
		ID:                 recordID,
		ProfileID:          agentcore.MemoryProfileID(profileID),
		CandidateID:        candidate.ID,
		Scope:              candidate.ProposedScope,
		Layer:              memcore.NormalizeMemoryLayer(candidate.ProposedLayer),
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
		Confidence:         confidence,
		Visibility: memcore.Visibility{
			Mode:      memcore.VisibilityScopedMembers,
			ScopeKind: candidate.ProposedScope.Kind,
			ScopeID:   candidate.ProposedScope.ID,
		},
		ValidFrom: now,
		CreatedBy: createdBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if strings.TrimSpace(record.Summary) == "" {
		return memcore.MemoryRecord{}, ErrInvalidRequest
	}
	return record, nil
}

func mapScope(scope memcore.MemoryScope) MemoryScopeDTO {
	return MemoryScopeDTO{Kind: string(scope.Kind), ID: scope.ID}
}

func candidateSourceRef(candidate memcore.MemoryCandidate) string {
	sourceType := strings.TrimSpace(string(candidate.SourceType))
	sourceRef := strings.TrimSpace(candidate.SourceRef)
	if sourceType == "" || sourceRef == "" {
		return ""
	}
	return sourceType + ":" + sourceRef
}

func defaultRecordID(candidateID string) string {
	return memcore.RecordIdentifierForCandidate(idSegment(candidateID))
}

func idSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
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
