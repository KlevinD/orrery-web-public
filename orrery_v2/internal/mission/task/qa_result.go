package task

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type QAResultID string
type QAPlanID string
type QASkipID string
type QAStatus string
type CheckStatus string

const (
	QAStatusApproved    QAStatus = "approved"
	QAStatusRejected    QAStatus = "rejected"
	QAStatusNeedsRework QAStatus = "needs_rework"
	QAStatusBlocked     QAStatus = "blocked"

	CheckStatusPassed  CheckStatus = "passed"
	CheckStatusFailed  CheckStatus = "failed"
	CheckStatusMissing CheckStatus = "missing"
	CheckStatusSkipped CheckStatus = "skipped"
)

type CheckedCriterion struct {
	CriterionID AcceptanceCriterionID
	Status      CheckStatus
	Reason      string
}

type EvidenceCheckResult struct {
	RequirementID EvidenceRequirementID
	EvidenceID    agentcore.EvidenceID
	Status        CheckStatus
	Reason        string
}

type QAResultRecord struct {
	ID                  QAResultID
	TaskID              TaskID
	SubmissionID        string
	CheckedBy           agentcore.AgentID
	Status              QAStatus
	CheckedCriteria     []CheckedCriterion
	EvidenceResults     []EvidenceCheckResult
	MissingEvidence     []EvidenceRequirementID
	RejectionReasons    []string
	AcceptanceReportRef string
	ReworkRequestRef    string
	CheckedAt           time.Time
	IdempotencyKey      string
}

type QAPlanRecord struct {
	ID             QAPlanID
	TaskID         TaskID
	CreatedBy      agentcore.AgentID
	ArtifactRef    agentcore.ArtifactRef
	Summary        string
	CompletedAt    time.Time
	IdempotencyKey string
}

type QASkipRecord struct {
	ID           QASkipID
	TaskID       TaskID
	SubmissionID TaskSubmissionID
	AgentID      agentcore.AgentID
	Reason       string
	SkippedAt    time.Time
}

type AcceptanceReportID string

type AcceptanceReport struct {
	ID           AcceptanceReportID
	TaskID       TaskID
	SubmissionID string
	QAResultID   QAResultID
	CreatedBy    agentcore.AgentID
	Summary      string
	CreatedAt    time.Time
}
