package ontology

import (
	"fmt"
	"strings"
)

type ActionBindingTargetKind string

const (
	ActionBindingTargetObjectType   ActionBindingTargetKind = "object_type"
	ActionBindingTargetLinkType     ActionBindingTargetKind = "link_type"
	ActionBindingTargetPropertyType ActionBindingTargetKind = "property_type"
)

const (
	actionBindingMetadataKey = "allowed_action"
	actionBindingSource      = "deterministic.action_type_binding"
)

type ActionTypeBindingProposalRequest struct {
	TenantID     string
	ActionTypeID string
	Targets      []ActionTypeBindingTarget
	EvidenceRefs []MetadataEvidenceRef
}

type ActionTypeBindingTarget struct {
	Kind              ActionBindingTargetKind
	ID                string
	ExistingActionIDs []string
}

type ActionTypeBindingProposalResult struct {
	Proposals            []OntologyMetadataProposal
	ValidTargetCount     int
	SkippedExistingCount int
	SkippedInvalidCount  int
	ValidationErrors     []string
}

func InferActionTypeBindingMetadataProposals(req ActionTypeBindingProposalRequest) (ActionTypeBindingProposalResult, error) {
	actionTypeID := normalizeID(req.ActionTypeID)
	if actionTypeID == "" {
		return ActionTypeBindingProposalResult{}, fmt.Errorf("%w: action_type_id is required", ErrInvalidDefinition)
	}
	tenantID := strings.TrimSpace(req.TenantID)
	baseEvidence := normalizeActionBindingEvidence(req.EvidenceRefs)
	if !metadataEvidenceRefExists(baseEvidence, "action_type", actionTypeID) {
		baseEvidence = append(baseEvidence, MetadataEvidenceRef{Kind: "action_type", ID: actionTypeID})
	}

	result := ActionTypeBindingProposalResult{}
	for i, target := range req.Targets {
		targetKind := ActionBindingTargetKind(normalizeID(string(target.Kind)))
		targetID := normalizeID(target.ID)
		if !isSupportedActionBindingTargetKind(targetKind) {
			result.SkippedInvalidCount++
			result.ValidationErrors = append(result.ValidationErrors, fmt.Sprintf("target[%d] kind %q is not supported", i, target.Kind))
			continue
		}
		if targetID == "" {
			result.SkippedInvalidCount++
			result.ValidationErrors = append(result.ValidationErrors, fmt.Sprintf("target[%d] id is required", i))
			continue
		}
		result.ValidTargetCount++
		if actionBindingContainsAction(target.ExistingActionIDs, actionTypeID) {
			result.SkippedExistingCount++
			continue
		}

		evidence := append([]MetadataEvidenceRef(nil), baseEvidence...)
		if !metadataEvidenceRefExists(evidence, string(targetKind), targetID) {
			evidence = append(evidence, MetadataEvidenceRef{Kind: string(targetKind), ID: targetID})
		}
		result.Proposals = append(result.Proposals, OntologyMetadataProposal{
			ID:            metadataProposalID(tenantID, string(targetKind), targetID, actionBindingMetadataKey, actionTypeID),
			TenantID:      tenantID,
			TargetKind:    string(targetKind),
			TargetID:      targetID,
			MetadataKey:   actionBindingMetadataKey,
			ProposedValue: actionTypeID,
			Source:        actionBindingSource,
			SourceKind:    MetadataProposalSourceDeterministic,
			SourceRef:     "action_type_binding",
			SourceVersion: "v1",
			Confidence:    1,
			Status:        MetadataProposalStatusProposed,
			Reason:        "Derived from declared ActionType ontology target binding.",
			EvidenceRefs:  evidence,
		})
	}
	return result, nil
}

func isSupportedActionBindingTargetKind(kind ActionBindingTargetKind) bool {
	switch kind {
	case ActionBindingTargetObjectType, ActionBindingTargetLinkType, ActionBindingTargetPropertyType:
		return true
	default:
		return false
	}
}

func actionBindingContainsAction(actionIDs []string, actionTypeID string) bool {
	for _, actionID := range actionIDs {
		if normalizeID(actionID) == actionTypeID {
			return true
		}
	}
	return false
}

func normalizeActionBindingEvidence(refs []MetadataEvidenceRef) []MetadataEvidenceRef {
	out := make([]MetadataEvidenceRef, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		kind := normalizeID(ref.Kind)
		id := normalizeID(ref.ID)
		if kind == "" || id == "" {
			continue
		}
		key := kind + "\x00" + id
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, MetadataEvidenceRef{Kind: kind, ID: id})
	}
	return out
}

func metadataEvidenceRefExists(refs []MetadataEvidenceRef, kind string, id string) bool {
	kind = normalizeID(kind)
	id = normalizeID(id)
	for _, ref := range refs {
		if normalizeID(ref.Kind) == kind && normalizeID(ref.ID) == id {
			return true
		}
	}
	return false
}
