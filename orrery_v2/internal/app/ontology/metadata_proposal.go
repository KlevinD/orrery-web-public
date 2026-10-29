package ontology

import (
	"fmt"
	"strings"
	"time"
)

type MetadataProposalStatus string
type MetadataProposalSourceKind string

const (
	MetadataProposalStatusProposed MetadataProposalStatus = "proposed"
	MetadataProposalStatusAccepted MetadataProposalStatus = "accepted"
	MetadataProposalStatusRejected MetadataProposalStatus = "rejected"
)

const (
	MetadataProposalSourceDeterministic     MetadataProposalSourceKind = "deterministic"
	MetadataProposalSourceModelInference    MetadataProposalSourceKind = "model_inference"
	MetadataProposalSourceOperatorSuggested MetadataProposalSourceKind = "operator_suggested"
	MetadataProposalSourceMigration         MetadataProposalSourceKind = "migration"
)

type MetadataEvidenceRef struct {
	Kind     string            `json:"kind"`
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type OntologyMetadataProposal struct {
	ID                 string                     `json:"id"`
	TenantID           string                     `json:"tenant_id,omitempty"`
	Revision           int64                      `json:"revision"`
	ContentHash        string                     `json:"content_hash"`
	TargetKind         string                     `json:"target_kind"`
	TargetID           string                     `json:"target_id"`
	MetadataKey        string                     `json:"metadata_key"`
	ProposedValue      string                     `json:"proposed_value"`
	Source             string                     `json:"source"`
	SourceKind         MetadataProposalSourceKind `json:"source_kind,omitempty"`
	SourceRef          string                     `json:"source_ref,omitempty"`
	SourceVersion      string                     `json:"source_version,omitempty"`
	Confidence         float64                    `json:"confidence"`
	Status             MetadataProposalStatus     `json:"status"`
	PreviousStatus     MetadataProposalStatus     `json:"previous_status,omitempty"`
	Reason             string                     `json:"reason,omitempty"`
	EvidenceRefs       []MetadataEvidenceRef      `json:"evidence_refs,omitempty"`
	ReviewedBy         string                     `json:"reviewed_by,omitempty"`
	ReviewedByKind     string                     `json:"reviewed_by_kind,omitempty"`
	ReviewedAt         time.Time                  `json:"reviewed_at,omitempty"`
	ReviewReason       string                     `json:"review_reason,omitempty"`
	ReviewEvidenceRefs []MetadataEvidenceRef      `json:"review_evidence_refs,omitempty"`
	CreatedAt          time.Time                  `json:"created_at,omitempty"`
	UpdatedAt          time.Time                  `json:"updated_at,omitempty"`
}

type MetadataProposalQuery struct {
	TenantID    string
	TargetKind  string
	TargetID    string
	MetadataKey string
	SourceKind  MetadataProposalSourceKind
	Status      string
	Limit       int
	Offset      int
}

type MetadataProposalDecision struct {
	ProposalID       string                 `json:"proposal_id,omitempty"`
	TenantID         string                 `json:"tenant_id,omitempty"`
	ExpectedRevision int64                  `json:"expected_revision"`
	Status           MetadataProposalStatus `json:"status"`
	ActorID          string                 `json:"actor_id"`
	ActorKind        string                 `json:"actor_kind,omitempty"`
	Reason           string                 `json:"reason"`
	EvidenceRefs     []MetadataEvidenceRef  `json:"evidence_refs,omitempty"`
}

type MetadataProposalPage struct {
	Proposals []OntologyMetadataProposal `json:"proposals"`
	Page      Page                       `json:"page"`
}

type Page struct {
	Limit    int  `json:"limit,omitempty"`
	Offset   int  `json:"offset,omitempty"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

func InferMappingRuleMetadataProposals(rule MappingRule) ([]OntologyMetadataProposal, error) {
	rule.ID = normalizeID(rule.ID)
	rule.EventType = strings.TrimSpace(rule.EventType)
	if rule.ID == "" || rule.EventType == "" {
		return nil, fmt.Errorf("%w: mapping rule id and event_type are required", ErrInvalidDefinition)
	}
	fields := []struct {
		key   string
		value string
	}{
		{key: "source_domain", value: normalizeID(rule.SourceDomain)},
		{key: "source_ref", value: strings.TrimSpace(rule.SourceRef)},
		{key: "event_schema_version", value: strings.TrimSpace(rule.EventSchemaVersion)},
		{key: "rule_hash", value: strings.TrimSpace(rule.RuleHash)},
	}
	proposals := make([]OntologyMetadataProposal, 0, len(fields))
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		proposals = append(proposals, OntologyMetadataProposal{
			ID:            metadataProposalID(rule.TenantID, "mapping_rule", rule.ID, field.key, field.value),
			TenantID:      strings.TrimSpace(rule.TenantID),
			TargetKind:    "mapping_rule",
			TargetID:      rule.ID,
			MetadataKey:   field.key,
			ProposedValue: field.value,
			Source:        "deterministic.mapping_rule_metadata",
			SourceKind:    MetadataProposalSourceDeterministic,
			SourceRef:     "mapping_rule_metadata",
			SourceVersion: "v1",
			Confidence:    1,
			Status:        MetadataProposalStatusProposed,
			Reason:        "Derived from declared MappingRule governance metadata.",
			EvidenceRefs: []MetadataEvidenceRef{
				{Kind: "mapping_rule", ID: rule.ID},
				{Kind: "event_type", ID: rule.EventType},
			},
		})
	}
	return proposals, nil
}

func metadataProposalID(tenantID string, targetKind string, targetID string, key string, value string) string {
	parts := []string{
		strings.TrimSpace(tenantID),
		normalizeID(targetKind),
		normalizeID(targetID),
		normalizeID(key),
		strings.TrimSpace(value),
	}
	return "ontology_metadata_proposal:" + hashMappingRule(map[string]any{"parts": parts})
}
