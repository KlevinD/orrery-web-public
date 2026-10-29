package ontology

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type MetadataPromotionPlanMaterial struct {
	DryRun   MetadataProposalPromotionPlan
	Targets  []MetadataPromotionPlanTarget
	Diff     []MetadataProposalPromotionChange
	DiffHash string
}

func BuildMetadataPromotionPlanMaterial(ctx context.Context, registry Registry, proposal OntologyMetadataProposal) (MetadataPromotionPlanMaterial, error) {
	dryRun, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		return MetadataPromotionPlanMaterial{}, err
	}
	if !dryRun.CanApply || len(dryRun.Issues) > 0 {
		return MetadataPromotionPlanMaterial{}, fmt.Errorf("%w: metadata promotion dry-run is not clean", ErrInvalidDefinition)
	}
	diff, diffHash, err := CanonicalMetadataPromotionPlanDiff(dryRun.Changes)
	if err != nil {
		return MetadataPromotionPlanMaterial{}, err
	}
	targets := make([]MetadataPromotionPlanTarget, 0, len(diff))
	seen := make(map[string]struct{}, len(diff))
	for _, change := range diff {
		key := strings.Join([]string{change.TargetKind, change.TargetID}, "\x00")
		if _, exists := seen[key]; exists {
			continue
		}
		target, err := metadataPromotionTargetSnapshot(ctx, registry, proposal, change)
		if err != nil {
			return MetadataPromotionPlanMaterial{}, err
		}
		targets = append(targets, target)
		seen[key] = struct{}{}
	}
	targets, err = CanonicalMetadataPromotionPlanTargets(targets)
	if err != nil {
		return MetadataPromotionPlanMaterial{}, err
	}
	dryRun.Changes = append([]MetadataProposalPromotionChange(nil), diff...)
	return MetadataPromotionPlanMaterial{DryRun: dryRun, Targets: targets, Diff: diff, DiffHash: diffHash}, nil
}

func MetadataPromotionPlanMaterialMatches(record MetadataPromotionPlanRecord, material MetadataPromotionPlanMaterial) bool {
	normalizedRecord, err := normalizeMetadataPromotionPlan(record, func() time.Time { return record.UpdatedAt })
	if err != nil {
		return false
	}
	diff, diffHash, err := CanonicalMetadataPromotionPlanDiff(material.Diff)
	if err != nil || diffHash != normalizedRecord.DiffHash || !metadataPromotionChangesEqual(normalizedRecord.Diff, diff) {
		return false
	}
	targets, err := CanonicalMetadataPromotionPlanTargets(material.Targets)
	return err == nil && metadataPromotionTargetsEqual(normalizedRecord.Targets, targets)
}

func metadataPromotionTargetSnapshot(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, change MetadataProposalPromotionChange) (MetadataPromotionPlanTarget, error) {
	if registry == nil {
		return MetadataPromotionPlanTarget{}, fmt.Errorf("%w: ontology registry is required", ErrInvalidDefinition)
	}
	switch change.TargetKind {
	case "object_type":
		definition, err := registry.GetObjectType(ctx, change.TargetID)
		if errors.Is(err, ErrNotFound) && change.Operation == PromotionOperationCreate {
			proposed, _, parseErr := objectTypeDefinitionFromProposal(ctx, registry, proposal)
			if parseErr != nil {
				return MetadataPromotionPlanTarget{}, parseErr
			}
			return absentMetadataPromotionTarget(change.TargetKind, change.TargetID, proposed.TenantID), nil
		}
		if err != nil {
			return MetadataPromotionPlanTarget{}, err
		}
		canonical, err := canonicalObjectTypeDefinitionValue(definition)
		return existingMetadataPromotionTarget(change, definition.TenantID, definition.Version, canonical, err)
	case "link_type":
		definition, err := registry.GetLinkType(ctx, change.TargetID)
		if errors.Is(err, ErrNotFound) && change.Operation == PromotionOperationCreate {
			proposed, _, parseErr := linkTypeDefinitionFromProposal(ctx, registry, proposal)
			if parseErr != nil {
				return MetadataPromotionPlanTarget{}, parseErr
			}
			return absentMetadataPromotionTarget(change.TargetKind, change.TargetID, proposed.TenantID), nil
		}
		if err != nil {
			return MetadataPromotionPlanTarget{}, err
		}
		canonical, err := canonicalLinkTypeDefinitionValue(definition)
		return existingMetadataPromotionTarget(change, definition.TenantID, definition.Version, canonical, err)
	case "property_type":
		definition, err := registry.GetPropertyType(ctx, change.TargetID)
		if errors.Is(err, ErrNotFound) && change.Operation == PromotionOperationCreate {
			proposed, _, parseErr := propertyTypeDefinitionFromProposal(ctx, registry, proposal)
			if parseErr != nil {
				return MetadataPromotionPlanTarget{}, parseErr
			}
			return absentMetadataPromotionTarget(change.TargetKind, change.TargetID, proposed.TenantID), nil
		}
		if err != nil {
			return MetadataPromotionPlanTarget{}, err
		}
		canonical, err := canonicalPropertyTypeDefinitionValue(definition)
		return existingMetadataPromotionTarget(change, definition.TenantID, definition.Version, canonical, err)
	case "mapping_rule":
		definition, found, err := findMappingRule(ctx, registry, proposal.TenantID, change.TargetID)
		if err != nil {
			return MetadataPromotionPlanTarget{}, err
		}
		if !found && change.Operation == PromotionOperationCreate {
			proposed, _, parseErr := mappingRuleDefinitionFromProposal(ctx, registry, proposal)
			if parseErr != nil {
				return MetadataPromotionPlanTarget{}, parseErr
			}
			return absentMetadataPromotionTarget(change.TargetKind, change.TargetID, proposed.TenantID), nil
		}
		if !found {
			return MetadataPromotionPlanTarget{}, fmt.Errorf("%w: mapping rule %s", ErrNotFound, change.TargetID)
		}
		canonical, err := canonicalMappingRuleDefinitionValue(definition)
		return existingMetadataPromotionTarget(change, definition.TenantID, definition.Version, canonical, err)
	default:
		return MetadataPromotionPlanTarget{}, fmt.Errorf("%w: unsupported promotion target kind %s", ErrInvalidDefinition, change.TargetKind)
	}
}

func absentMetadataPromotionTarget(kind, id, tenantID string) MetadataPromotionPlanTarget {
	return MetadataPromotionPlanTarget{Kind: kind, ID: id, DefinitionTenantID: strings.TrimSpace(tenantID), Version: 0, ContentHash: "absent"}
}

func existingMetadataPromotionTarget(change MetadataProposalPromotionChange, tenantID string, version int, canonical string, err error) (MetadataPromotionPlanTarget, error) {
	if err != nil {
		return MetadataPromotionPlanTarget{}, err
	}
	if version <= 0 || change.CurrentVersion > 0 && change.CurrentVersion != version || change.ExpectedVersion > 0 && change.ExpectedVersion != version {
		return MetadataPromotionPlanTarget{}, fmt.Errorf("%w: promotion target %s/%s version changed", ErrVersionConflict, change.TargetKind, change.TargetID)
	}
	return MetadataPromotionPlanTarget{
		Kind: change.TargetKind, ID: change.TargetID, DefinitionTenantID: strings.TrimSpace(tenantID),
		Version: version, ContentHash: secureMetadataPromotionHash([]byte(canonical)),
	}, nil
}

func metadataPromotionChangesEqual(left, right []MetadataProposalPromotionChange) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func metadataPromotionTargetsEqual(left, right []MetadataPromotionPlanTarget) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
