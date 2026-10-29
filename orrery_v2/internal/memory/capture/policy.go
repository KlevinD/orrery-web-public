package capture

import (
	"strings"

	memcore "orrery_v2/internal/memory/core"
)

type DefaultCapturePolicy struct{}

func (DefaultCapturePolicy) Decide(draft CandidateDraft) PolicyDecision {
	sensitivity, reason := classifySensitivity(draft.Summary + " " + draft.Narrative)
	decision := PolicyDecision{
		Status:         memcore.CandidateProposed,
		Sensitivity:    sensitivity,
		DeIDLevel:      memcore.DeIDRaw,
		Consent:        memcore.ConsentNotRequired,
		ReviewRequired: true,
		ReviewReason:   reason,
	}
	autoAcceptAgentResult := draft.SourceType == memcore.SourceSummary &&
		draft.Layer == memcore.MemoryLayerFact &&
		draft.Topic == "agent_result" &&
		draft.ProposedScope.Kind == memcore.ScopeMission &&
		strings.TrimSpace(draft.ProposedScope.ID) != ""
	if autoAcceptAgentResult || sensitivity == memcore.SensitivityNormal &&
		draft.Layer == memcore.MemoryLayerFact &&
		((len(draft.EvidenceRefs) > 0 &&
			(draft.SourceType == memcore.SourceQAResult || draft.SourceType == memcore.SourceEvidence)) ||
			((draft.SourceType == memcore.SourceAgentRunDiary || draft.SourceType == memcore.SourcePersonalAssistantExchange) && draft.ProposedScope.Kind == memcore.ScopeAgent)) {
		decision.Status = memcore.CandidateAccepted
		decision.ReviewRequired = false
		decision.ReviewReason = ""
	}
	return decision
}

func classifySensitivity(text string) (memcore.Sensitivity, string) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "compensation"), strings.Contains(lower, "salary"), strings.Contains(lower, "pay "):
		return memcore.SensitivityCompensation, "compensation content requires review"
	case strings.Contains(lower, "candidate"):
		return memcore.SensitivityCandidate, "candidate content requires review"
	case strings.Contains(lower, "performance disagreement"), strings.Contains(lower, "performance review"):
		return memcore.SensitivityPerformance, "performance content requires review"
	case strings.Contains(lower, "legal"), strings.Contains(lower, "dispute"), strings.Contains(lower, "labor"):
		return memcore.SensitivityLegal, "legal content requires review"
	case strings.Contains(lower, "executive"):
		return memcore.SensitivityExecutive, "executive content requires review"
	case strings.Contains(lower, "secret"):
		return memcore.SensitivitySecret, "secret content requires review"
	default:
		return memcore.SensitivityNormal, ""
	}
}
