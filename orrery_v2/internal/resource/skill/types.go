package skill

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type PromptAssetKind string
type RiskLevel string

const (
	PromptAssetMarkdown PromptAssetKind = "markdown"
	PromptAssetText     PromptAssetKind = "text"
	PromptAssetFile     PromptAssetKind = "file"
)

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type PromptAsset struct {
	Path       string          `json:"path"`
	ContentRef string          `json:"content_ref"`
	Content    string          `json:"content,omitempty"`
	Kind       PromptAssetKind `json:"kind"`
}

type SkillDefinition struct {
	ID              agentcore.SkillID       `json:"skill_id"`
	Name            string                  `json:"name"`
	Version         string                  `json:"version"`
	Description     string                  `json:"description"`
	DomainTags      []string                `json:"domain_tags"`
	TaskTypeTags    []string                `json:"task_type_tags"`
	RiskLevel       RiskLevel               `json:"risk_level"`
	PromptAssets    []PromptAsset           `json:"prompt_assets"`
	RequiredTools   []agentcore.ToolRef     `json:"required_tools"`
	EvidenceRefs    []agentcore.EvidenceRef `json:"evidence_refs"`
	Enabled         bool                    `json:"enabled"`
	SourceRef       string                  `json:"source_ref,omitempty"`
	PackageSnapshot *PackageSnapshot        `json:"package_snapshot,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
}

type ResolvedSkill struct {
	Ref             agentcore.SkillRef
	Definition      SkillDefinition
	MatchedVersion  string
	BindingWarnings []BindingWarning
}

type BindingWarning struct {
	Code    string
	Message string
}

const WarningTaskTagMismatch = "task_tag_mismatch"

func (d SkillDefinition) Clone() SkillDefinition {
	d.DomainTags = append([]string(nil), d.DomainTags...)
	d.TaskTypeTags = append([]string(nil), d.TaskTypeTags...)
	d.PromptAssets = append([]PromptAsset(nil), d.PromptAssets...)
	d.RequiredTools = append([]agentcore.ToolRef(nil), d.RequiredTools...)
	d.EvidenceRefs = append([]agentcore.EvidenceRef(nil), d.EvidenceRefs...)
	if d.PackageSnapshot != nil {
		snapshot := *d.PackageSnapshot
		snapshot.Files = append([]SnapshotFile(nil), snapshot.Files...)
		d.PackageSnapshot = &snapshot
	}
	return d
}
