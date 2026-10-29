package evidence

import (
	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
)

func ExpectedAgentEvidenceKind(kind missiontask.EvidenceRequirementKind) agentcore.EvidenceKind {
	switch kind {
	case missiontask.EvidenceRequirementKindArtifact:
		return agentcore.EvidenceKindArtifact
	case missiontask.EvidenceRequirementKindToolLog:
		return agentcore.EvidenceKindToolLog
	case missiontask.EvidenceRequirementKindTestLog:
		return agentcore.EvidenceKindTestLog
	case missiontask.EvidenceRequirementKindExternalURL:
		return agentcore.EvidenceKindExternalURL
	case missiontask.EvidenceRequirementKindApprovalRecord:
		return agentcore.EvidenceKindApprovalRecord
	case missiontask.EvidenceRequirementKindHumanSubmission:
		return agentcore.EvidenceKindHumanSubmission
	default:
		return ""
	}
}
