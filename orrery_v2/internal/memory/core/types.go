package core

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/retrievalevidence"
)

type ScopeKind string

const (
	ScopeAgent        ScopeKind = "agent"
	ScopeOrganization ScopeKind = "organization"
	ScopeDepartment   ScopeKind = "department"
	ScopeProject      ScopeKind = "project"
	ScopeMission      ScopeKind = "mission"
	ScopeDiscussion   ScopeKind = "discussion"
)

type MemoryScope struct {
	Kind ScopeKind `json:"kind"`
	ID   string    `json:"id"`
}

type OwnerRef struct {
	AgentID        agentcore.AgentID `json:"agent_id,omitempty"`
	OrganizationID string            `json:"organization_id,omitempty"`
	DepartmentID   string            `json:"department_id,omitempty"`
	ProjectID      string            `json:"project_id,omitempty"`
}

type MemoryProfile struct {
	ID                agentcore.MemoryProfileID `json:"memory_profile_id"`
	OwnerRef          OwnerRef                  `json:"owner_ref"`
	MemoryKind        agentcore.MemoryKind      `json:"memory_kind"`
	DefaultScope      MemoryScope               `json:"default_scope"`
	RetrievalPolicy   RetrievalPolicy           `json:"retrieval_policy"`
	CompressionPolicy CompressionPolicy         `json:"compression_policy"`
	RetentionPolicy   RetentionPolicy           `json:"retention_policy"`
	VisibilityPolicy  VisibilityPolicy          `json:"visibility_policy"`
	Enabled           bool                      `json:"enabled"`
}

type RetrievalPolicy struct {
	MaxRecords int      `json:"max_records"`
	Tags       []string `json:"tags,omitempty"`
}

type CompressionPolicy struct {
	MaxSummaryBytes int `json:"max_summary_bytes"`
}

type RetentionPolicy struct {
	ExpiresAfter time.Duration `json:"expires_after"`
}

type MemoryRecord struct {
	ID                 string                    `json:"memory_id"`
	ProfileID          agentcore.MemoryProfileID `json:"profile_id"`
	CandidateID        string                    `json:"candidate_id"`
	Scope              MemoryScope               `json:"scope"`
	Layer              MemoryLayer               `json:"layer,omitempty"`
	Topic              string                    `json:"topic,omitempty"`
	SourceRefs         []string                  `json:"source_refs"`
	SourceEvidenceRefs []string                  `json:"source_evidence_refs"`
	Content            string                    `json:"content"`
	Summary            string                    `json:"summary"`
	Narrative          string                    `json:"narrative,omitempty"`
	Facts              []string                  `json:"facts"`
	Lessons            []string                  `json:"lessons,omitempty"`
	Preferences        []string                  `json:"preferences"`
	Warnings           []string                  `json:"warnings"`
	Decisions          []string                  `json:"decisions"`
	Tags               []string                  `json:"tags"`
	Entities           map[string]string         `json:"entities,omitempty"`
	LineageRefs        []string                  `json:"lineage_refs,omitempty"`
	SupersedesRefs     []string                  `json:"supersedes_refs,omitempty"`
	ContradictsRefs    []string                  `json:"contradicts_refs,omitempty"`
	Sensitivity        Sensitivity               `json:"sensitivity,omitempty"`
	DeIDLevel          DeIDLevel                 `json:"de_id_level,omitempty"`
	LifecycleStatus    MemoryLifecycleStatus     `json:"lifecycle_status,omitempty"`
	ReferenceCount     int                       `json:"reference_count,omitempty"`
	LastReferencedAt   time.Time                 `json:"last_referenced_at,omitempty"`
	Confidence         float64                   `json:"confidence"`
	Visibility         Visibility                `json:"visibility"`
	ValidFrom          time.Time                 `json:"valid_from"`
	ExpiresAt          time.Time                 `json:"expires_at"`
	CreatedBy          agentcore.AgentID         `json:"created_by"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
	DisabledAt         *time.Time                `json:"disabled_at,omitempty"`
}

type LayerDigest struct {
	Layer              MemoryLayer `json:"layer"`
	Topic              string      `json:"topic,omitempty"`
	SourceMemoryRefs   []string    `json:"source_memory_refs,omitempty"`
	SourceEvidenceRefs []string    `json:"source_evidence_refs,omitempty"`
	Summary            string      `json:"summary"`
	Facts              []string    `json:"facts,omitempty"`
	Lessons            []string    `json:"lessons,omitempty"`
	Preferences        []string    `json:"preferences,omitempty"`
	Warnings           []string    `json:"warnings,omitempty"`
	Decisions          []string    `json:"decisions,omitempty"`
	Confidence         float64     `json:"confidence,omitempty"`
}

type MemoryDigest struct {
	Evidence           []retrievalevidence.Item `json:"evidence,omitempty"`
	RetrievalMode      string                   `json:"retrieval_mode,omitempty"`
	FallbackReason     string                   `json:"fallback_reason,omitempty"`
	ID                 agentcore.MemoryDigestID `json:"digest_id"`
	AgentID            agentcore.AgentID        `json:"agent_id"`
	MissionID          string                   `json:"mission_id,omitempty"`
	TaskID             string                   `json:"task_id,omitempty"`
	Scope              MemoryScope              `json:"scope"`
	SourceMemoryRefs   []string                 `json:"source_memory_refs"`
	SourceEvidenceRefs []string                 `json:"source_evidence_refs"`
	Summary            string                   `json:"summary"`
	Facts              []string                 `json:"facts"`
	Preferences        []string                 `json:"preferences"`
	Warnings           []string                 `json:"warnings"`
	Decisions          []string                 `json:"decisions"`
	Layers             []LayerDigest            `json:"layers,omitempty"`
	Confidence         float64                  `json:"confidence"`
	Visibility         Visibility               `json:"visibility"`
	Sensitivity        Sensitivity              `json:"sensitivity,omitempty"`
	CreatedAt          time.Time                `json:"created_at"`
	ExpiresAt          time.Time                `json:"expires_at"`
}

func (r MemoryRecord) Normalized() MemoryRecord {
	r.Layer = NormalizeMemoryLayer(r.Layer)
	r.LifecycleStatus = NormalizeMemoryLifecycleStatus(r.LifecycleStatus)
	r.Sensitivity = NormalizeSensitivity(r.Sensitivity)
	r.DeIDLevel = NormalizeDeIDLevel(r.DeIDLevel)
	return r
}

func ValidateMemoryRecord(record MemoryRecord) []string {
	var errs []string
	if record.Layer != "" && !record.Layer.IsValid() {
		errs = append(errs, "layer is invalid")
	}
	if record.LifecycleStatus != "" && !record.LifecycleStatus.IsValid() {
		errs = append(errs, "lifecycle_status is invalid")
	}
	if record.Sensitivity != "" && !record.Sensitivity.IsValid() {
		errs = append(errs, "sensitivity is invalid")
	}
	if record.DeIDLevel != "" && !record.DeIDLevel.IsValid() {
		errs = append(errs, "de_id_level is invalid")
	}

	record = record.Normalized()
	if !ValidIdentifier(record.ID) {
		errs = append(errs, "memory_id must be between 1 and 512 bytes")
	}
	if record.ProfileID == "" {
		errs = append(errs, "profile_id is required")
	}
	if record.Layer == MemoryLayerExperience || record.Layer == MemoryLayerWorld {
		if len(record.LineageRefs) == 0 && len(record.SourceEvidenceRefs) == 0 {
			errs = append(errs, "lineage_refs or source_evidence_refs are required for experience and world memories")
		}
	}
	if record.Layer == MemoryLayerWorld &&
		record.Scope.Kind == ScopeOrganization &&
		record.Sensitivity != SensitivityNormal &&
		record.DeIDLevel == DeIDRaw {
		errs = append(errs, "organization world memory with sensitive content must be de-identified")
	}
	return errs
}

func (r MemoryRecord) Clone() MemoryRecord {
	r.SourceRefs = append([]string(nil), r.SourceRefs...)
	r.SourceEvidenceRefs = append([]string(nil), r.SourceEvidenceRefs...)
	r.Facts = append([]string(nil), r.Facts...)
	r.Lessons = append([]string(nil), r.Lessons...)
	r.Preferences = append([]string(nil), r.Preferences...)
	r.Warnings = append([]string(nil), r.Warnings...)
	r.Decisions = append([]string(nil), r.Decisions...)
	r.Tags = append([]string(nil), r.Tags...)
	r.LineageRefs = append([]string(nil), r.LineageRefs...)
	r.SupersedesRefs = append([]string(nil), r.SupersedesRefs...)
	r.ContradictsRefs = append([]string(nil), r.ContradictsRefs...)
	if r.Entities != nil {
		r.Entities = cloneStringMap(r.Entities)
	}
	return r
}

func (p MemoryProfile) Clone() MemoryProfile {
	p.RetrievalPolicy.Tags = append([]string(nil), p.RetrievalPolicy.Tags...)
	return p
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
