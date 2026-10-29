package capture

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
)

type AgentResultReconciliationReport struct {
	Scanned      int
	Accepted     int
	Materialized int
}

type EpisodicWriter struct {
	repository                    Repository
	audit                         AuditRecorder
	metadata                      RecordMetadataSink
	policy                        CapturePolicy
	now                           ClockFunc
	autoRecordAgentRunDiarySource bool
}

func NewEpisodicWriter(opts EpisodicWriterOptions) *EpisodicWriter {
	policy := opts.Policy
	if policy == nil {
		policy = DefaultCapturePolicy{}
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &EpisodicWriter{
		repository:                    opts.Repository,
		audit:                         opts.Audit,
		metadata:                      opts.Metadata,
		policy:                        policy,
		now:                           now,
		autoRecordAgentRunDiarySource: opts.AutoRecordAgentRunDiarySource,
	}
}

func (w *EpisodicWriter) CaptureCandidate(ctx context.Context, draft CandidateDraft) (memcore.MemoryCandidate, error) {
	if err := ctx.Err(); err != nil {
		return memcore.MemoryCandidate{}, err
	}
	if w == nil || w.repository == nil {
		return memcore.MemoryCandidate{}, fmt.Errorf("memory capture repository is required")
	}
	if strings.TrimSpace(draft.ID) == "" {
		return memcore.MemoryCandidate{}, fmt.Errorf("memory candidate id is required")
	}
	if existing, err := w.repository.GetCandidate(ctx, draft.ID); err == nil {
		if _, _, materializeErr := w.MaterializeAcceptedCandidate(ctx, existing); materializeErr != nil {
			return memcore.MemoryCandidate{}, materializeErr
		}
		return existing, nil
	} else if !errors.Is(err, memcore.ErrNotFound) {
		return memcore.MemoryCandidate{}, err
	}
	candidate := w.buildCandidate(draft)
	if err := w.repository.SaveCandidate(ctx, candidate); err != nil {
		return memcore.MemoryCandidate{}, err
	}
	if w.audit != nil {
		if err := w.audit.RecordMemoryAudit(ctx, memaudit.Event{
			Type:       memaudit.EventMemoryCandidateCreated,
			SubjectID:  candidate.ID,
			ActorID:    string(candidate.CreatedBy),
			MissionID:  candidate.MissionID,
			Summary:    candidate.Summary,
			SourceRefs: append([]string(nil), candidate.EvidenceRefs...),
			OccurredAt: candidate.CreatedAt,
		}); err != nil {
			return memcore.MemoryCandidate{}, err
		}
	}
	if candidate.Status == memcore.CandidateAccepted {
		if _, _, err := w.MaterializeAcceptedCandidate(ctx, candidate); err != nil {
			return memcore.MemoryCandidate{}, err
		}
	}
	return candidate, nil
}

// MaterializeAcceptedCandidate turns an accepted fact candidate into its
// deterministic formal record. It is safe to call repeatedly.
func (w *EpisodicWriter) MaterializeAcceptedCandidate(ctx context.Context, candidate memcore.MemoryCandidate) (memcore.MemoryRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return memcore.MemoryRecord{}, false, err
	}
	if w == nil || w.repository == nil {
		return memcore.MemoryRecord{}, false, fmt.Errorf("memory capture repository is required")
	}
	if !candidate.CanCreateRecordDirectly() {
		return memcore.MemoryRecord{}, false, nil
	}
	recordID := memcore.RecordIdentifierForCandidate(idSegment(candidate.ID))
	if existing, err := w.repository.GetRecord(ctx, recordID); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, memcore.ErrNotFound) {
		return memcore.MemoryRecord{}, false, err
	}
	record, ok, err := w.buildAutoAcceptedRecord(ctx, candidate)
	if err != nil || !ok {
		return memcore.MemoryRecord{}, false, err
	}
	if err := w.repository.SaveRecord(ctx, record); err != nil {
		return memcore.MemoryRecord{}, false, err
	}
	if w.metadata != nil {
		if err := w.metadata.PublishMaterializedMemoryRecord(ctx, record); err != nil {
			return memcore.MemoryRecord{}, false, err
		}
	}
	if w.audit != nil {
		if err := w.audit.RecordMemoryAudit(ctx, memaudit.Event{
			Type:       memaudit.EventMemoryRecordCreated,
			SubjectID:  record.ID,
			ActorID:    string(record.CreatedBy),
			MissionID:  candidate.MissionID,
			Summary:    record.Summary,
			SourceRefs: append([]string(nil), record.SourceRefs...),
			OccurredAt: record.CreatedAt,
		}); err != nil {
			return memcore.MemoryRecord{}, false, err
		}
	}
	return record, true, nil
}

// ReconcilePendingAgentResults upgrades legacy agent-result candidates created
// before automatic materialization was enabled. Other proposed candidates are
// deliberately left untouched.
func (w *EpisodicWriter) ReconcilePendingAgentResults(ctx context.Context, limit int) (AgentResultReconciliationReport, error) {
	report := AgentResultReconciliationReport{}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if w == nil || w.repository == nil {
		return report, fmt.Errorf("memory capture repository is required")
	}
	if limit <= 0 {
		limit = 1000
	}
	for _, status := range []memcore.CandidateStatus{memcore.CandidateAccepted, memcore.CandidateProposed} {
		candidates, err := w.repository.ListCandidates(ctx, memstore.CandidateQuery{
			ScopeKind: memcore.ScopeMission,
			Layer:     memcore.MemoryLayerFact,
			Topic:     "agent_result",
			Status:    status,
			Limit:     limit,
		})
		if err != nil {
			return report, err
		}
		for _, candidate := range candidates {
			if !isAutoMaterializedAgentResult(candidate) {
				continue
			}
			report.Scanned++
			if candidate.Status == memcore.CandidateProposed {
				candidate.Status = memcore.CandidateAccepted
				candidate.ReviewRequired = false
				candidate.ReviewReason = ""
				candidate.UpdatedAt = w.now().UTC()
				if err := w.repository.SaveCandidate(ctx, candidate); err != nil {
					return report, err
				}
				report.Accepted++
				if w.audit != nil {
					if err := w.audit.RecordMemoryAudit(ctx, memaudit.Event{
						Type:       memaudit.EventMemoryCandidateAccepted,
						SubjectID:  candidate.ID,
						ActorID:    string(candidate.CreatedBy),
						MissionID:  candidate.MissionID,
						Summary:    candidate.Summary,
						SourceRefs: append([]string(nil), candidate.EvidenceRefs...),
						OccurredAt: candidate.UpdatedAt,
					}); err != nil {
						return report, err
					}
				}
			}
			if _, created, err := w.MaterializeAcceptedCandidate(ctx, candidate); err != nil {
				return report, err
			} else if created {
				report.Materialized++
			}
		}
	}
	return report, nil
}

func isAutoMaterializedAgentResult(candidate memcore.MemoryCandidate) bool {
	return candidate.SourceType == memcore.SourceSummary &&
		candidate.ProposedScope.Kind == memcore.ScopeMission &&
		strings.TrimSpace(candidate.ProposedScope.ID) != "" &&
		memcore.NormalizeMemoryLayer(candidate.ProposedLayer) == memcore.MemoryLayerFact &&
		strings.TrimSpace(candidate.Topic) == "agent_result" &&
		strings.TrimSpace(candidate.Summary) != "" &&
		!looksLikeOperationalTrace(candidate.Summary+"\n"+candidate.Narrative)
}

func (w *EpisodicWriter) buildCandidate(draft CandidateDraft) memcore.MemoryCandidate {
	now := w.now().UTC()
	if !draft.CreatedAt.IsZero() {
		now = draft.CreatedAt.UTC()
	}
	decision := w.policy.Decide(draft)
	return memcore.MemoryCandidate{
		ID:                draft.ID,
		MissionID:         draft.MissionID,
		DiscussionGroupID: draft.DiscussionGroupID,
		SourceType:        draft.SourceType,
		SourceRef:         draft.SourceRef,
		ProposedScope:     draft.ProposedScope,
		ProposedKind:      draft.ProposedKind,
		ProposedLayer:     memcore.NormalizeMemoryLayer(draft.Layer),
		Topic:             draft.Topic,
		Summary:           strings.TrimSpace(draft.Summary),
		Narrative:         strings.TrimSpace(draft.Narrative),
		Lessons:           append([]string(nil), draft.Lessons...),
		Entities:          cloneMap(draft.Entities),
		Tags:              append([]string(nil), draft.Tags...),
		EvidenceRefs:      append([]string(nil), draft.EvidenceRefs...),
		LineageRefs:       append([]string(nil), draft.LineageRefs...),
		Sensitivity:       decision.Sensitivity,
		DeIDLevel:         decision.DeIDLevel,
		Consent:           decision.Consent,
		ReviewRequired:    decision.ReviewRequired,
		ReviewReason:      decision.ReviewReason,
		CreatedBy:         draft.CreatedBy,
		Status:            decision.Status,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (w *EpisodicWriter) buildAutoAcceptedRecord(ctx context.Context, candidate memcore.MemoryCandidate) (memcore.MemoryRecord, bool, error) {
	candidate = candidate.Clone()
	if candidate.SourceType == memcore.SourceAgentRunDiary && !w.autoRecordAgentRunDiarySource {
		return memcore.MemoryRecord{}, false, nil
	}
	layer := memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
	if layer != memcore.MemoryLayerFact ||
		strings.TrimSpace(candidate.ProposedScope.ID) == "" {
		return memcore.MemoryRecord{}, false, nil
	}
	if candidate.ProposedScope.Kind != memcore.ScopeAgent && !memcore.IsCollectiveScope(candidate.ProposedScope.Kind) {
		return memcore.MemoryRecord{}, false, nil
	}
	summary := strings.TrimSpace(candidate.Summary)
	if summary == "" {
		return memcore.MemoryRecord{}, false, nil
	}
	profileID, err := w.memoryProfileID(ctx, candidate)
	if err != nil {
		return memcore.MemoryRecord{}, false, err
	}
	now := candidate.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	confidence := candidate.Confidence
	if confidence <= 0 {
		confidence = 1
	}
	sourceRefs := []string{}
	if ref := candidateSourceRef(candidate); ref != "" {
		sourceRefs = append(sourceRefs, ref)
	}
	sourceRefs = append(sourceRefs, candidate.LineageRefs...)
	visibility := memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: candidate.ProposedScope.Kind, ScopeID: candidate.ProposedScope.ID}
	if candidate.ProposedScope.Kind == memcore.ScopeAgent {
		visibility.Mode = memcore.VisibilityOwnerOnly
	}
	return memcore.MemoryRecord{
		ID:                 memcore.RecordIdentifierForCandidate(idSegment(candidate.ID)),
		ProfileID:          profileID,
		CandidateID:        candidate.ID,
		Scope:              candidate.ProposedScope,
		Layer:              layer,
		Topic:              candidate.Topic,
		SourceRefs:         sourceRefs,
		SourceEvidenceRefs: append([]string(nil), candidate.EvidenceRefs...),
		Content:            firstNonBlank(candidate.Narrative, summary),
		Summary:            summary,
		Narrative:          candidate.Narrative,
		Facts:              []string{summary},
		Lessons:            append([]string(nil), candidate.Lessons...),
		Warnings:           append([]string(nil), candidate.Warnings...),
		Tags:               append([]string(nil), candidate.Tags...),
		Entities:           cloneMap(candidate.Entities),
		LineageRefs:        append([]string(nil), candidate.LineageRefs...),
		Sensitivity:        memcore.NormalizeSensitivity(candidate.Sensitivity),
		DeIDLevel:          memcore.NormalizeDeIDLevel(candidate.DeIDLevel),
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Confidence:         confidence,
		Visibility:         visibility,
		ValidFrom:          now,
		CreatedBy:          candidate.CreatedBy,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, true, nil
}

func (w *EpisodicWriter) memoryProfileID(ctx context.Context, candidate memcore.MemoryCandidate) (agentcore.MemoryProfileID, error) {
	scope := candidate.ProposedScope
	scope.ID = strings.TrimSpace(scope.ID)
	if w != nil && w.repository != nil {
		profiles, err := w.repository.ListProfiles(ctx)
		if err == nil {
			for _, profile := range profiles {
				if profile.Enabled && memcore.MemoryProfileMatchesScope(profile, scope) {
					return profile.ID, nil
				}
			}
		} else {
			return "", err
		}
	}
	owner := memcore.OwnerRef{}
	if scope.Kind == memcore.ScopeAgent {
		owner.AgentID = agentcore.AgentID(scope.ID)
	}
	profile := memcore.NewScopeMemoryProfile(scope, owner)
	if profile.ID == "" || !profile.Enabled {
		return "", fmt.Errorf("memory profile scope is invalid")
	}
	if err := w.repository.SaveProfile(ctx, profile); err != nil {
		return "", err
	}
	return profile.ID, nil
}

func candidateSourceRef(candidate memcore.MemoryCandidate) string {
	sourceType := strings.TrimSpace(string(candidate.SourceType))
	sourceRef := strings.TrimSpace(candidate.SourceRef)
	if sourceType == "" || sourceRef == "" {
		return ""
	}
	return sourceType + ":" + sourceRef
}
