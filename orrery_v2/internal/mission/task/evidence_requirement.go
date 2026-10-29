package task

type EvidenceRequirementID string

type EvidenceRequirementKind string

const (
	EvidenceRequirementKindArtifact        EvidenceRequirementKind = "artifact"
	EvidenceRequirementKindToolLog         EvidenceRequirementKind = "tool_log"
	EvidenceRequirementKindExternalURL     EvidenceRequirementKind = "external_url"
	EvidenceRequirementKindApprovalRecord  EvidenceRequirementKind = "approval_record"
	EvidenceRequirementKindHumanSubmission EvidenceRequirementKind = "human_submission"
	EvidenceRequirementKindTestLog         EvidenceRequirementKind = "test_log"
)

func (k EvidenceRequirementKind) IsValid() bool {
	switch k {
	case EvidenceRequirementKindArtifact, EvidenceRequirementKindToolLog, EvidenceRequirementKindExternalURL, EvidenceRequirementKindApprovalRecord, EvidenceRequirementKindHumanSubmission, EvidenceRequirementKindTestLog:
		return true
	default:
		return false
	}
}

type EvidenceRequirement struct {
	ID          EvidenceRequirementID
	Description string
	Kind        EvidenceRequirementKind
	Required    bool
}
