package memorydistill

import "time"

type MemoryScopeDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type DistillExperienceRequest struct {
	TargetScope MemoryScopeDTO `json:"target_scope"`
	SourceScope MemoryScopeDTO `json:"source_scope,omitempty"`
	Topic       string         `json:"topic"`
	Limit       int            `json:"limit,omitempty"`
}

type ExperienceCandidateDTO struct {
	CandidateID    string         `json:"candidate_id"`
	ProposedLayer  string         `json:"proposed_layer"`
	ProposedScope  MemoryScopeDTO `json:"proposed_scope"`
	Topic          string         `json:"topic"`
	Summary        string         `json:"summary"`
	Lessons        []string       `json:"lessons,omitempty"`
	Warnings       []string       `json:"warnings,omitempty"`
	LineageRefs    []string       `json:"lineage_refs,omitempty"`
	EvidenceRefs   []string       `json:"evidence_refs,omitempty"`
	Confidence     float64        `json:"confidence"`
	ReviewRequired bool           `json:"review_required"`
	ReviewReason   string         `json:"review_reason,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type DistillExperienceResponse struct {
	Candidates      []ExperienceCandidateDTO `json:"candidates"`
	SourceMemoryIDs []string                 `json:"source_memory_ids"`
}

type DistillWorldRequest struct {
	TargetScope MemoryScopeDTO `json:"target_scope"`
	SourceScope MemoryScopeDTO `json:"source_scope,omitempty"`
	Topic       string         `json:"topic"`
	Limit       int            `json:"limit,omitempty"`
}

type WorldCandidateDTO struct {
	CandidateID     string         `json:"candidate_id"`
	ProposedLayer   string         `json:"proposed_layer"`
	ProposedScope   MemoryScopeDTO `json:"proposed_scope"`
	Topic           string         `json:"topic"`
	Summary         string         `json:"summary"`
	Narrative       string         `json:"narrative,omitempty"`
	Lessons         []string       `json:"lessons,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
	LineageRefs     []string       `json:"lineage_refs,omitempty"`
	SupersedesRefs  []string       `json:"supersedes_refs,omitempty"`
	ContradictsRefs []string       `json:"contradicts_refs,omitempty"`
	EvidenceRefs    []string       `json:"evidence_refs,omitempty"`
	Confidence      float64        `json:"confidence"`
	ReviewRequired  bool           `json:"review_required"`
	ReviewReason    string         `json:"review_reason,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type DistillWorldResponse struct {
	Candidates      []WorldCandidateDTO `json:"candidates"`
	SourceMemoryIDs []string            `json:"source_memory_ids"`
}
