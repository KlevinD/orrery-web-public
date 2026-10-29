package problemhub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

type MemoryLessonSink struct {
	repository memstore.Repository
	mu         sync.Mutex
}

func NewMemoryLessonSink(repository memstore.Repository) *MemoryLessonSink {
	return &MemoryLessonSink{repository: repository}
}

func (s *MemoryLessonSink) ProposeProblemLesson(ctx context.Context, proposal LessonProposal) (string, error) {
	if s == nil || s.repository == nil {
		return "", fmt.Errorf("%w: memory repository is required", ErrInvalidRequest)
	}
	proposal.ID, proposal.TenantID, proposal.ProblemCaseID = strings.TrimSpace(proposal.ID), strings.TrimSpace(proposal.TenantID), strings.TrimSpace(proposal.ProblemCaseID)
	proposal.Topic, proposal.Summary, proposal.Lesson = strings.TrimSpace(proposal.Topic), strings.TrimSpace(proposal.Summary), strings.TrimSpace(proposal.Lesson)
	if proposal.ID == "" || proposal.TenantID == "" || proposal.ProblemCaseID == "" || proposal.Topic == "" || proposal.Summary == "" || proposal.Lesson == "" || len(proposal.EvidenceRefs) == 0 {
		return "", fmt.Errorf("%w: complete evidence-backed lesson proposal is required", ErrInvalidRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.repository.GetCandidate(ctx, proposal.ID)
	if err == nil {
		if existing.SourceType != memcore.SourceProblemCase || strings.TrimSpace(existing.SourceRef) != proposal.ProblemCaseID ||
			existing.ProposedScope.Kind != memcore.ScopeOrganization || strings.TrimSpace(existing.ProposedScope.ID) != proposal.TenantID {
			return "", fmt.Errorf("%w: memory candidate id is already used by another source", ErrConflict)
		}
		return existing.ID, nil
	}
	if !errors.Is(err, memcore.ErrNotFound) {
		return "", err
	}

	evidenceRefs := make([]string, 0, len(proposal.EvidenceRefs))
	for _, evidenceID := range proposal.EvidenceRefs {
		evidenceID = strings.TrimSpace(evidenceID)
		if evidenceID != "" {
			evidenceRefs = append(evidenceRefs, "evidence_item:"+evidenceID)
		}
	}
	candidate := memcore.MemoryCandidate{
		ID: proposal.ID, SourceType: memcore.SourceProblemCase, SourceRef: proposal.ProblemCaseID,
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: proposal.TenantID},
		ProposedKind:  agentcore.MemoryKindOrganization, ProposedLayer: memcore.MemoryLayerExperience,
		Topic: proposal.Topic, Summary: proposal.Summary, Narrative: proposal.Lesson,
		Lessons: []string{proposal.Lesson}, Warnings: []string{proposal.Warning},
		Entities:    map[string]string{"problem_case": proposal.ProblemCaseID},
		Tags:        []string{"problem", "lesson_candidate", proposal.Topic},
		LineageRefs: append([]string(nil), proposal.LineageRefs...), EvidenceRefs: evidenceRefs,
		Sensitivity: memcore.SensitivityNormal, DeIDLevel: memcore.DeIDTeamSafe,
		Consent: memcore.ConsentNotRequired, Confidence: proposal.Confidence,
		ReviewRequired: true,
		ReviewReason:   "A verified outcome exists; human review is required before this case-specific lesson becomes organizational memory.",
		CreatedBy:      agentcore.AgentID(proposal.CreatedBy), Status: memcore.CandidateProposed,
		CreatedAt: proposal.CreatedAt.UTC(), UpdatedAt: proposal.CreatedAt.UTC(),
	}
	if err := s.repository.SaveCandidate(ctx, candidate); err != nil {
		return "", err
	}
	return candidate.ID, nil
}

var _ LessonSink = (*MemoryLessonSink)(nil)
