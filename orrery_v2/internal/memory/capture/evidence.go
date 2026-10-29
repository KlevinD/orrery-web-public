package capture

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missioncore "orrery_v2/internal/mission/core"
)

type EvidenceCaptureOptions struct {
	MissionID missioncore.MissionID
	TaskID    missioncore.TaskID
	AgentID   agentcore.AgentID
	CreatedAt time.Time
}

type DecisionCaptureOptions struct {
	MissionID missioncore.MissionID
	TaskID    missioncore.TaskID
	AgentID   agentcore.AgentID
	CreatedAt time.Time
}

func BuildEvidenceDraft(evidence agentcore.Evidence, opts EvidenceCaptureOptions) CandidateDraft {
	evidenceRefs := []string{"evidence:" + string(evidence.ID)}
	if strings.TrimSpace(evidence.ArtifactRef.ID) != "" {
		evidenceRefs = append(evidenceRefs, "artifact:"+strings.TrimSpace(evidence.ArtifactRef.ID))
	}
	if evidence.ToolLogRef.ID != "" {
		evidenceRefs = append(evidenceRefs, "tool_log:"+string(evidence.ToolLogRef.ID))
	}
	if strings.TrimSpace(evidence.ExternalURL) != "" {
		evidenceRefs = append(evidenceRefs, "url:"+strings.TrimSpace(evidence.ExternalURL))
	}
	if strings.TrimSpace(evidence.AcceptanceCriterionID) != "" {
		evidenceRefs = append(evidenceRefs, "acceptance:"+strings.TrimSpace(evidence.AcceptanceCriterionID))
	}
	return CandidateDraft{
		ID:            "candidate.evidence." + idSegment(string(evidence.ID)),
		MissionID:     opts.MissionID.String(),
		SourceType:    memcore.SourceEvidence,
		SourceRef:     string(evidence.ID),
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: opts.MissionID.String()},
		ProposedKind:  agentcore.MemoryKindMission,
		Layer:         memcore.MemoryLayerFact,
		Topic:         "evidence",
		Summary:       evidenceSummary(evidence),
		Tags:          []string{"evidence", string(evidence.Kind)},
		EvidenceRefs:  evidenceRefs,
		LineageRefs:   lineageRefsForTask("evidence:"+string(evidence.ID), opts.TaskID),
		CreatedBy:     opts.AgentID,
		CreatedAt:     opts.CreatedAt,
		UpdatedAt:     opts.CreatedAt,
	}
}

func BuildDecisionDraft(decision agentcore.Decision, opts DecisionCaptureOptions) CandidateDraft {
	return CandidateDraft{
		ID:            "candidate.decision." + idSegment(string(decision.ID)),
		MissionID:     opts.MissionID.String(),
		SourceType:    memcore.SourceDecision,
		SourceRef:     string(decision.ID),
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: opts.MissionID.String()},
		ProposedKind:  agentcore.MemoryKindMission,
		Layer:         memcore.MemoryLayerFact,
		Topic:         "decision",
		Summary:       strings.TrimSpace(decision.Summary),
		Tags:          []string{"decision"},
		EvidenceRefs:  []string{"decision:" + string(decision.ID)},
		LineageRefs:   lineageRefsForTask("decision:"+string(decision.ID), opts.TaskID),
		CreatedBy:     opts.AgentID,
		CreatedAt:     opts.CreatedAt,
		UpdatedAt:     opts.CreatedAt,
	}
}

func evidenceSummary(evidence agentcore.Evidence) string {
	parts := []string{"Evidence " + string(evidence.ID)}
	if evidence.Kind != "" {
		parts = append(parts, "kind "+string(evidence.Kind))
	}
	if strings.TrimSpace(evidence.EvidenceRequirementID) != "" {
		parts = append(parts, "requirement "+strings.TrimSpace(evidence.EvidenceRequirementID))
	}
	return strings.Join(parts, " ")
}

func lineageRefsForTask(source string, taskID missioncore.TaskID) []string {
	refs := []string{source}
	if taskID != "" {
		refs = append(refs, "task:"+taskID.String())
	}
	return refs
}
