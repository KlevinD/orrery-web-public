package core

import (
	"strings"
	"time"
)

import missioncore "orrery_v2/internal/mission/core"

type DeliverableID string
type EvidenceID string
type DecisionID string
type AgentRequestID string
type ToolLogID string
type ApprovalRecordID string
type HumanSubmissionID string

type Summary struct {
	Text string
}

func (s Summary) IsZero() bool {
	return s.Text == ""
}

type ArtifactKind string

const (
	ArtifactKindCode   ArtifactKind = "code"
	ArtifactKindLog    ArtifactKind = "log"
	ArtifactKindURL    ArtifactKind = "url"
	ArtifactKindReport ArtifactKind = "report"
	ArtifactKindVisual ArtifactKind = "visual"
)

type ArtifactRef struct {
	ID        string
	Kind      ArtifactKind
	URI       string
	SHA256    string `json:",omitempty"`
	SizeBytes int64  `json:",omitempty"`
}

type Deliverable struct {
	ID                    DeliverableID
	ExpectedDeliverableID string
	Title                 string
	ArtifactRef           ArtifactRef
}

type EvidenceKind string

const (
	EvidenceKindArtifact        EvidenceKind = "artifact"
	EvidenceKindToolLog         EvidenceKind = "tool_log"
	EvidenceKindExternalURL     EvidenceKind = "external_url"
	EvidenceKindApprovalRecord  EvidenceKind = "approval_record"
	EvidenceKindHumanSubmission EvidenceKind = "human_submission"
	EvidenceKindTestLog         EvidenceKind = "test_log"
)

type Evidence struct {
	ID                    EvidenceID
	EvidenceRequirementID string
	AcceptanceCriterionID string
	Kind                  EvidenceKind
	ArtifactRef           ArtifactRef
	ToolLogRef            ToolLogRef
	ApprovalRecordRef     ApprovalRecordRef
	HumanSubmissionRef    HumanSubmissionRef
	ExternalURL           string
}

func (e Evidence) HasTraceableSource() bool {
	switch e.Kind {
	case EvidenceKindArtifact, EvidenceKindTestLog:
		return !e.ArtifactRef.IsZero() || !e.ToolLogRef.IsZero()
	case EvidenceKindToolLog:
		return !e.ToolLogRef.IsZero()
	case EvidenceKindExternalURL:
		return e.ExternalURL != ""
	case EvidenceKindApprovalRecord:
		return !e.ApprovalRecordRef.IsZero()
	case EvidenceKindHumanSubmission:
		return !e.HumanSubmissionRef.IsZero()
	default:
		return false
	}
}

func (r ArtifactRef) IsZero() bool {
	return r.ID == "" && r.URI == ""
}

type ToolLogRef struct {
	ID ToolLogID
}

func (r ToolLogRef) IsZero() bool {
	return r.ID == ""
}

type ApprovalRecordRef struct {
	ID ApprovalRecordID
}

func (r ApprovalRecordRef) IsZero() bool {
	return r.ID == ""
}

type HumanSubmissionRef struct {
	ID HumanSubmissionID
}

func (r HumanSubmissionRef) IsZero() bool {
	return r.ID == ""
}

func (k EvidenceKind) IsValid() bool {
	switch k {
	case EvidenceKindArtifact, EvidenceKindToolLog, EvidenceKindExternalURL, EvidenceKindApprovalRecord, EvidenceKindHumanSubmission, EvidenceKindTestLog:
		return true
	default:
		return false
	}
}

type Decision struct {
	ID      DecisionID
	Summary string
}

type AgentRequestKind string

const (
	AgentRequestKindReview        AgentRequestKind = "review"
	AgentRequestKindClarification AgentRequestKind = "clarification"
	AgentRequestKindApproval      AgentRequestKind = "approval"
)

type AgentRequest struct {
	ID      AgentRequestID
	Kind    AgentRequestKind
	Summary string
}

type Blocker struct {
	Summary string
	Report  ArtifactRef `json:",omitempty"`
}

type AgentResult struct {
	ID        AgentResultID
	MissionID missioncore.MissionID
	TaskID    missioncore.TaskID
	AgentID   AgentID
	RunID     AgentRunID
	Summary   Summary
	// UserReport is the executing Agent's final public message. It is kept
	// separate from internal summaries and survives result/retry recovery.
	UserReport       string `json:",omitempty"`
	Deliverables     []Deliverable
	Evidence         []Evidence
	Decisions        []Decision
	Requests         []AgentRequest
	Blocker          *Blocker
	InternalTraceRef string
	RuntimeAttemptID string `json:",omitempty"`
	CreatedAt        time.Time
}

func ValidateAgentResult(r AgentResult) []ValidationError {
	var errs []ValidationError
	if r.Summary.IsZero() {
		errs = append(errs, validationError(CodeMissingSummary, "summary", "summary is required"))
	}
	if r.Blocker != nil && strings.TrimSpace(r.Blocker.Summary) == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "blocker.summary", "blocker summary is required"))
	}
	return errs
}
