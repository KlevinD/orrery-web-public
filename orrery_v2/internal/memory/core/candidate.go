package core

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type CandidateStatus string

const (
	CandidateProposed CandidateStatus = "proposed"
	CandidateAccepted CandidateStatus = "accepted"
	CandidateRejected CandidateStatus = "rejected"
	CandidateMerged   CandidateStatus = "merged"
	CandidateExpired  CandidateStatus = "expired"
)

type SourceType string

const (
	SourceDiscussionMessage         SourceType = "discussion_message"
	SourceSummary                   SourceType = "summary"
	SourceDeliverable               SourceType = "deliverable"
	SourceEvidence                  SourceType = "evidence"
	SourceQAResult                  SourceType = "qa_result"
	SourceDecision                  SourceType = "decision"
	SourceAgentRunDiary             SourceType = "agent_run_diary"
	SourcePersonalAssistantExchange SourceType = "personal_assistant_exchange"
	SourceProblemCase               SourceType = "problem_case"
)

type MemoryCandidate struct {
	ID                string               `json:"candidate_id"`
	MissionID         string               `json:"mission_id,omitempty"`
	DiscussionGroupID string               `json:"discussion_group_id,omitempty"`
	SourceType        SourceType           `json:"source_type"`
	SourceRef         string               `json:"source_ref"`
	ProposedScope     MemoryScope          `json:"proposed_scope"`
	ProposedKind      agentcore.MemoryKind `json:"proposed_kind"`
	ProposedLayer     MemoryLayer          `json:"proposed_layer,omitempty"`
	Topic             string               `json:"topic,omitempty"`
	Summary           string               `json:"summary"`
	Narrative         string               `json:"narrative,omitempty"`
	Lessons           []string             `json:"lessons,omitempty"`
	Warnings          []string             `json:"warnings,omitempty"`
	Entities          map[string]string    `json:"entities,omitempty"`
	Tags              []string             `json:"tags,omitempty"`
	LineageRefs       []string             `json:"lineage_refs,omitempty"`
	SupersedesRefs    []string             `json:"supersedes_refs,omitempty"`
	ContradictsRefs   []string             `json:"contradicts_refs,omitempty"`
	Sensitivity       Sensitivity          `json:"sensitivity,omitempty"`
	DeIDLevel         DeIDLevel            `json:"de_id_level,omitempty"`
	Consent           ConsentState         `json:"consent,omitempty"`
	Confidence        float64              `json:"confidence,omitempty"`
	ReviewRequired    bool                 `json:"review_required,omitempty"`
	ReviewReason      string               `json:"review_reason,omitempty"`
	EvidenceRefs      []string             `json:"evidence_refs"`
	CreatedBy         agentcore.AgentID    `json:"created_by"`
	Status            CandidateStatus      `json:"status"`
	CreatedAt         time.Time            `json:"created_at,omitempty"`
	UpdatedAt         time.Time            `json:"updated_at,omitempty"`
}

func (c MemoryCandidate) CanCreateRecordDirectly() bool {
	return c.Status == CandidateAccepted || c.Status == CandidateMerged
}

func (c MemoryCandidate) NormalizedLayer() MemoryLayer {
	return NormalizeMemoryLayer(c.ProposedLayer)
}

func (c MemoryCandidate) Clone() MemoryCandidate {
	c.EvidenceRefs = append([]string(nil), c.EvidenceRefs...)
	c.Lessons = append([]string(nil), c.Lessons...)
	c.Warnings = append([]string(nil), c.Warnings...)
	c.Tags = append([]string(nil), c.Tags...)
	c.LineageRefs = append([]string(nil), c.LineageRefs...)
	c.SupersedesRefs = append([]string(nil), c.SupersedesRefs...)
	c.ContradictsRefs = append([]string(nil), c.ContradictsRefs...)
	if c.Entities != nil {
		c.Entities = cloneStringMap(c.Entities)
	}
	return c
}
