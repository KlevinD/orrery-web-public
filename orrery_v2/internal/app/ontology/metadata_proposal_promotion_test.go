package ontology

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

func TestDryRunMetadataProposalPromotionPlansObjectAllowedActionAppend(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Issues) != 0 || len(plan.Changes) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	change := plan.Changes[0]
	if change.Operation != PromotionOperationAppend ||
		change.TargetKind != "object_type" ||
		change.TargetID != "task" ||
		change.Field != "allowed_actions" ||
		change.CurrentValue != "task.complete" ||
		change.ProposedValue != "mission.plan" ||
		change.ExpectedVersion != 1 ||
		change.CurrentVersion != 1 {
		t.Fatalf("change = %+v", change)
	}
	objectType, err := registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if len(objectType.AllowedActions) != 1 || objectType.AllowedActions[0] != "task.complete" {
		t.Fatalf("dry-run modified registry object type: %+v", objectType)
	}
}

func TestDryRunMetadataProposalPromotionRequiresAcceptedProposal(t *testing.T) {
	plan, err := DryRunMetadataProposalPromotion(context.Background(), MetadataProposalPromotionDryRunRequest{
		Registry: NewMemoryRegistry(),
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusProposed,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if plan.CanApply || !testPromotionPlanHasIssue(plan, PromotionIssueProposalNotAccepted) {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestApplyMetadataProposalPromotionCreatesObjectTypeDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	proposal := OntologyMetadataProposal{
		ID:          "proposal-object-definition-create",
		TenantID:    "tenant-1",
		TargetKind:  "object_type",
		TargetID:    "project",
		MetadataKey: "definition",
		ProposedValue: objectTypeDefinitionValueForTest(t, ObjectType{
			ID:               "project",
			TenantID:         "tenant-1",
			TenantScope:      TenantScopeTenant,
			DisplayName:      "Project",
			CanonicalKeyRule: "project:{id}",
			OwnerDomain:      "mission",
			Properties: []PropertyDefinition{{
				PropertyID:  "title",
				DisplayName: "Title",
				ValueType:   ValueTypeString,
				Required:    true,
			}},
			AllowedActions: []string{"project.read"},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Changes) != 1 || plan.Changes[0].Operation != PromotionOperationCreate {
		t.Fatalf("plan = %+v", plan)
	}
	var canonical map[string]any
	if err := json.Unmarshal([]byte(plan.Changes[0].ProposedValue), &canonical); err != nil {
		t.Fatalf("Unmarshal canonical object definition: %v", err)
	}
	if _, ok := canonical["version"]; ok {
		t.Fatalf("canonical object definition should not include version: %s", plan.Changes[0].ProposedValue)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		TenantID:          "tenant-1",
		Proposal:          proposal,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-object-definition-create",
		ConfirmationToken: MetadataProposalPromotionConfirmationToken(plan),
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Changes[0].AppliedVersion != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	objectType, err := registry.GetObjectType(ctx, "project")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if objectType.TenantID != "tenant-1" || objectType.DisplayName != "Project" || objectType.OwnerDomain != "mission" || objectType.Version != 1 || len(objectType.Properties) != 1 {
		t.Fatalf("object type = %+v", objectType)
	}
}

func TestApplyMetadataProposalPromotionUpdatesAndNoopsObjectTypeDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "project",
		TenantID:         "tenant-1",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Project",
		CanonicalKeyRule: "project:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-object-definition-update",
		TenantID:    "tenant-1",
		TargetKind:  "object_type",
		TargetID:    "project",
		MetadataKey: "definition",
		ProposedValue: objectTypeDefinitionValueForTest(t, ObjectType{
			ID:               "project",
			TenantID:         "tenant-1",
			TenantScope:      TenantScopeTenant,
			DisplayName:      "Active Project",
			CanonicalKeyRule: "project:{id}",
			Lifecycle:        "active",
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Changes) != 1 || plan.Changes[0].Operation != PromotionOperationUpdate || plan.Changes[0].ExpectedVersion != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:                registry,
		TenantID:                "tenant-1",
		Proposal:                proposal,
		ActorID:                 "operator-1",
		ActorKind:               "user",
		IdempotencyKey:          "apply-object-definition-update",
		ConfirmationToken:       MetadataProposalPromotionConfirmationToken(plan),
		RegistryExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Changes[0].AppliedVersion != 2 {
		t.Fatalf("receipt = %+v", receipt)
	}
	objectType, err := registry.GetObjectType(ctx, "project")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if objectType.DisplayName != "Active Project" || objectType.Lifecycle != "active" || objectType.Version != 2 {
		t.Fatalf("object type after update = %+v", objectType)
	}
	noopPlan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion noop: %v", err)
	}
	if !noopPlan.CanApply || noopPlan.Changes[0].Operation != PromotionOperationNoop || noopPlan.Changes[0].CurrentVersion != 2 {
		t.Fatalf("noop plan = %+v", noopPlan)
	}
}

func TestDryRunMetadataProposalPromotionBlocksObjectTypeDefinitionWithUnknownAllowedLink(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:          "proposal-object-definition-invalid",
			TenantID:    "tenant-1",
			TargetKind:  "object_type",
			TargetID:    "project",
			MetadataKey: "definition",
			ProposedValue: objectTypeDefinitionValueForTest(t, ObjectType{
				ID:               "project",
				TenantID:         "tenant-1",
				TenantScope:      TenantScopeTenant,
				DisplayName:      "Project",
				CanonicalKeyRule: "project:{id}",
				AllowedLinkTypes: []string{"unknown_link"},
			}),
			Source:     "operator_suggested.ontology_admin",
			SourceKind: MetadataProposalSourceOperatorSuggested,
			Confidence: 1,
			Status:     MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if plan.CanApply || !testPromotionPlanHasIssue(plan, PromotionIssueInvalidProposal) || len(plan.Changes) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestDryRunMetadataProposalPromotionBlocksAcceptedProposalWithBlockingAnalysisIssue(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-model-missing-trace",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.91,
			Status:        MetadataProposalStatusAccepted,
			EvidenceRefs:  []MetadataEvidenceRef{{Kind: "document", ID: "brief-1"}},
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if plan.CanApply || !testPromotionPlanHasIssue(plan, PromotionIssueBlockingAnalysis) {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Changes) != 0 {
		t.Fatalf("blocking analysis should prevent promotion changes: %+v", plan)
	}
}

func TestDryRunMetadataProposalPromotionPlansLinkAndPropertyAllowedActionAppend(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "agent", TenantScope: TenantScopeGlobal, DisplayName: "Agent", CanonicalKeyRule: "agent:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(agent): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{
		ID:             "agent_can_use_tool",
		TenantScope:    TenantScopeGlobal,
		DisplayName:    "Agent can use tool",
		FromObjectType: "agent",
		ToObjectType:   "tool",
		Cardinality:    CardinalityManyToMany,
		AllowedActions: []string{"resource.bind"},
	}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	if err := registry.SavePropertyType(ctx, PropertyType{
		ID:              "tool.risk_level",
		ObjectTypeID:    "tool",
		PropertyID:      "risk_level",
		ValueType:       ValueTypeString,
		AllowedActions:  []string{"resource.classify"},
		AuthoritySource: "resource_admin",
		ConflictPolicy:  ConflictPolicyAuthorityWins,
	}); err != nil {
		t.Fatalf("SavePropertyType: %v", err)
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TargetKind:    "link_type",
			TargetID:      "agent_can_use_tool",
			MetadataKey:   "allowed_action",
			ProposedValue: "mcp.tool.call",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Issues) != 0 || len(plan.Changes) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Changes[0].Operation != PromotionOperationAppend ||
		plan.Changes[0].TargetKind != "link_type" ||
		plan.Changes[0].TargetID != "agent_can_use_tool" ||
		plan.Changes[0].Field != "allowed_actions" ||
		plan.Changes[0].CurrentValue != "resource.bind" ||
		plan.Changes[0].ProposedValue != "mcp.tool.call" {
		t.Fatalf("link change = %+v", plan.Changes[0])
	}

	propertyPlan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-2",
			TargetKind:    "property_type",
			TargetID:      "tool.risk_level",
			MetadataKey:   "allowed_action",
			ProposedValue: "mcp.tool.call",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion property: %v", err)
	}
	if !propertyPlan.CanApply || len(propertyPlan.Issues) != 0 || len(propertyPlan.Changes) != 1 {
		t.Fatalf("property plan = %+v", propertyPlan)
	}
	if propertyPlan.Changes[0].Operation != PromotionOperationAppend ||
		propertyPlan.Changes[0].TargetKind != "property_type" ||
		propertyPlan.Changes[0].TargetID != "tool.risk_level" ||
		propertyPlan.Changes[0].Field != "allowed_actions" ||
		propertyPlan.Changes[0].CurrentValue != "resource.classify" ||
		propertyPlan.Changes[0].ProposedValue != "mcp.tool.call" {
		t.Fatalf("property change = %+v", propertyPlan.Changes[0])
	}
}

func TestApplyMetadataProposalPromotionCreatesPropertyTypeDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-property-definition-create",
		TenantID:    "tenant-1",
		TargetKind:  "property_type",
		TargetID:    "tool.risk_level",
		MetadataKey: "definition",
		ProposedValue: propertyTypeDefinitionValueForTest(t, PropertyType{
			ID:              "tool.risk_level",
			TenantID:        "tenant-1",
			ObjectTypeID:    "tool",
			PropertyID:      "risk_level",
			DisplayName:     "Risk level",
			ValueType:       ValueTypeString,
			AuthoritySource: "resource_admin",
			ConflictPolicy:  ConflictPolicyAuthorityWins,
			AllowedActions:  []string{"resource.classify"},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Changes) != 1 || plan.Changes[0].Operation != PromotionOperationCreate {
		t.Fatalf("plan = %+v", plan)
	}
	var canonical map[string]any
	if err := json.Unmarshal([]byte(plan.Changes[0].ProposedValue), &canonical); err != nil {
		t.Fatalf("Unmarshal canonical property definition: %v", err)
	}
	if _, ok := canonical["version"]; ok {
		t.Fatalf("canonical property definition should not include version: %s", plan.Changes[0].ProposedValue)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		TenantID:          "tenant-1",
		Proposal:          proposal,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-property-definition-create",
		ConfirmationToken: MetadataProposalPromotionConfirmationToken(plan),
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Changes[0].AppliedVersion != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	propertyType, err := registry.GetPropertyType(ctx, "tool.risk_level")
	if err != nil {
		t.Fatalf("GetPropertyType: %v", err)
	}
	if propertyType.TenantID != "tenant-1" || propertyType.ObjectTypeID != "tool" || propertyType.ValueType != ValueTypeString || propertyType.Version != 1 {
		t.Fatalf("property type = %+v", propertyType)
	}
}

func TestApplyMetadataProposalPromotionUpdatesAndNoopsPropertyTypeDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SavePropertyType(ctx, PropertyType{
		ID:             "tool.risk_level",
		TenantID:       "tenant-1",
		ObjectTypeID:   "tool",
		PropertyID:     "risk_level",
		DisplayName:    "Risk",
		ValueType:      ValueTypeString,
		ConflictPolicy: ConflictPolicyManualReview,
	}); err != nil {
		t.Fatalf("SavePropertyType: %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-property-definition-update",
		TenantID:    "tenant-1",
		TargetKind:  "property_type",
		TargetID:    "tool.risk_level",
		MetadataKey: "definition",
		ProposedValue: propertyTypeDefinitionValueForTest(t, PropertyType{
			ID:              "tool.risk_level",
			TenantID:        "tenant-1",
			ObjectTypeID:    "tool",
			PropertyID:      "risk_level",
			DisplayName:     "Risk level",
			ValueType:       ValueTypeString,
			AuthoritySource: "resource_admin",
			ConflictPolicy:  ConflictPolicyAuthorityWins,
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Changes) != 1 || plan.Changes[0].Operation != PromotionOperationUpdate || plan.Changes[0].ExpectedVersion != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:                registry,
		TenantID:                "tenant-1",
		Proposal:                proposal,
		ActorID:                 "operator-1",
		ActorKind:               "user",
		IdempotencyKey:          "apply-property-definition-update",
		ConfirmationToken:       MetadataProposalPromotionConfirmationToken(plan),
		RegistryExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Changes[0].AppliedVersion != 2 {
		t.Fatalf("receipt = %+v", receipt)
	}
	propertyType, err := registry.GetPropertyType(ctx, "tool.risk_level")
	if err != nil {
		t.Fatalf("GetPropertyType: %v", err)
	}
	if propertyType.DisplayName != "Risk level" || propertyType.AuthoritySource != "resource_admin" || propertyType.ConflictPolicy != ConflictPolicyAuthorityWins || propertyType.Version != 2 {
		t.Fatalf("property type after update = %+v", propertyType)
	}
	noopPlan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion noop: %v", err)
	}
	if !noopPlan.CanApply || noopPlan.Changes[0].Operation != PromotionOperationNoop || noopPlan.Changes[0].CurrentVersion != 2 {
		t.Fatalf("noop plan = %+v", noopPlan)
	}
}

func TestDryRunMetadataProposalPromotionBlocksPropertyTypeDefinitionWithUnknownObject(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:          "proposal-property-definition-invalid",
			TenantID:    "tenant-1",
			TargetKind:  "property_type",
			TargetID:    "tool.risk_level",
			MetadataKey: "definition",
			ProposedValue: propertyTypeDefinitionValueForTest(t, PropertyType{
				ID:             "tool.risk_level",
				TenantID:       "tenant-1",
				ObjectTypeID:   "tool",
				PropertyID:     "risk_level",
				DisplayName:    "Risk level",
				ValueType:      ValueTypeString,
				ConflictPolicy: ConflictPolicyManualReview,
			}),
			Source:     "operator_suggested.ontology_admin",
			SourceKind: MetadataProposalSourceOperatorSuggested,
			Confidence: 1,
			Status:     MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if plan.CanApply || !testPromotionPlanHasIssue(plan, PromotionIssueInvalidProposal) || len(plan.Changes) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestDryRunMetadataProposalPromotionPlansLinkTypeDefinitionCreate(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-link-definition-create",
		TargetKind:  "link_type",
		TargetID:    "project_uses_tool",
		MetadataKey: "definition",
		ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{
			ID:               "project_uses_tool",
			TenantScope:      TenantScopeTenant,
			DisplayName:      "Project uses tool",
			FromObjectType:   "project",
			ToObjectType:     "tool",
			Cardinality:      CardinalityManyToMany,
			EvidenceRequired: true,
			SuggestedAllowed: true,
			AllowedActions:   []string{"mcp.tool.call"},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: proposal,
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Issues) != 0 || len(plan.Changes) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	change := plan.Changes[0]
	if change.Operation != PromotionOperationCreate ||
		change.TargetKind != "link_type" ||
		change.TargetID != "project_uses_tool" ||
		change.Field != "definition" ||
		change.CurrentValue != "" ||
		change.ExpectedVersion != 0 ||
		change.CurrentVersion != 0 {
		t.Fatalf("change = %+v", change)
	}
	if !json.Valid([]byte(change.ProposedValue)) {
		t.Fatalf("proposed value should be canonical JSON: %s", change.ProposedValue)
	}
	var canonical map[string]any
	if err := json.Unmarshal([]byte(change.ProposedValue), &canonical); err != nil {
		t.Fatalf("Unmarshal canonical link definition: %v", err)
	}
	if _, ok := canonical["version"]; ok {
		t.Fatalf("canonical link definition should not include version: %s", change.ProposedValue)
	}
	if _, ok := canonical["created_at"]; ok {
		t.Fatalf("canonical link definition should not include created_at: %s", change.ProposedValue)
	}
	if _, ok := canonical["updated_at"]; ok {
		t.Fatalf("canonical link definition should not include updated_at: %s", change.ProposedValue)
	}
	if _, err := registry.GetLinkType(ctx, "project_uses_tool"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dry-run should not create link type, err = %v", err)
	}
}

func TestApplyMetadataProposalPromotionCreatesLinkTypeDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-link-definition-create",
		TenantID:    "tenant-1",
		TargetKind:  "link_type",
		TargetID:    "project_uses_tool",
		MetadataKey: "definition",
		ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{
			ID:               "project_uses_tool",
			TenantID:         "tenant-1",
			TenantScope:      TenantScopeTenant,
			DisplayName:      "Project uses tool",
			FromObjectType:   "project",
			ToObjectType:     "tool",
			Cardinality:      CardinalityManyToMany,
			EvidenceRequired: true,
			SuggestedAllowed: true,
			AllowedActions:   []string{"mcp.tool.call"},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	request := MetadataProposalPromotionApplyRequest{
		Registry:       registry,
		ReceiptStore:   receipts,
		TenantID:       "tenant-1",
		Proposal:       proposal,
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "apply-link-definition-create",
	}
	request.ConfirmationToken = metadataPromotionConfirmationTokenForTest(t, ctx, registry, proposal)

	receipt, err := ApplyMetadataProposalPromotion(ctx, request)
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Status != PromotionReceiptStatusApplied || len(receipt.Changes) != 1 || len(receipt.Issues) != 0 {
		t.Fatalf("receipt = %+v", receipt)
	}
	change := receipt.Changes[0]
	if change.Operation != PromotionOperationCreate ||
		change.TargetKind != "link_type" ||
		change.TargetID != "project_uses_tool" ||
		change.Field != "definition" ||
		change.AppliedVersion != 1 {
		t.Fatalf("change = %+v", change)
	}
	linkType, err := registry.GetLinkType(ctx, "project_uses_tool")
	if err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	if linkType.TenantID != "tenant-1" ||
		linkType.FromObjectType != "project" ||
		linkType.ToObjectType != "tool" ||
		linkType.Cardinality != CardinalityManyToMany ||
		!linkType.EvidenceRequired ||
		!linkType.SuggestedAllowed ||
		len(linkType.AllowedActions) != 1 ||
		linkType.AllowedActions[0] != "mcp.tool.call" ||
		linkType.Version != 1 {
		t.Fatalf("link type = %+v", linkType)
	}
	replayed, err := ApplyMetadataProposalPromotion(ctx, request)
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion replay: %v", err)
	}
	if replayed.ReceiptID != receipt.ReceiptID || !replayed.Applied || replayed.Status != PromotionReceiptStatusApplied {
		t.Fatalf("replayed receipt = %+v, first receipt = %+v", replayed, receipt)
	}
}

func TestApplyMetadataProposalPromotionFillsTenantInDefinitionReceiptCanonicalValue(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-link-definition-fill-tenant",
		TenantID:    "tenant-1",
		TargetKind:  "link_type",
		TargetID:    "project_uses_tool",
		MetadataKey: "definition",
		ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{
			ID:             "project_uses_tool",
			TenantScope:    TenantScopeTenant,
			DisplayName:    "Project uses tool",
			FromObjectType: "project",
			ToObjectType:   "tool",
			Cardinality:    CardinalityManyToMany,
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		TenantID:          "tenant-1",
		Proposal:          proposal,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-link-definition-fill-tenant",
		ConfirmationToken: MetadataProposalPromotionConfirmationToken(plan),
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	var canonical map[string]any
	if err := json.Unmarshal([]byte(receipt.Changes[0].ProposedValue), &canonical); err != nil {
		t.Fatalf("Unmarshal receipt proposed value: %v", err)
	}
	if canonical["tenant_id"] != "tenant-1" {
		t.Fatalf("receipt proposed value should include applied tenant_id, got %s", receipt.Changes[0].ProposedValue)
	}
}

func TestApplyMetadataProposalPromotionUpdatesAndNoopsLinkTypeDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{
		ID:             "project_uses_tool",
		TenantScope:    TenantScopeTenant,
		TenantID:       "tenant-1",
		DisplayName:    "Project uses tool",
		FromObjectType: "project",
		ToObjectType:   "tool",
		Cardinality:    CardinalityManyToMany,
	}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-link-definition-update",
		TenantID:    "tenant-1",
		TargetKind:  "link_type",
		TargetID:    "project_uses_tool",
		MetadataKey: "definition",
		ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{
			ID:               "project_uses_tool",
			TenantID:         "tenant-1",
			TenantScope:      TenantScopeTenant,
			DisplayName:      "Project uses approved tool",
			FromObjectType:   "project",
			ToObjectType:     "tool",
			Cardinality:      CardinalityManyToMany,
			EvidenceRequired: true,
			SuggestedAllowed: true,
			AllowedActions:   []string{"mcp.tool.call"},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Changes) != 1 || plan.Changes[0].Operation != PromotionOperationUpdate || plan.Changes[0].ExpectedVersion != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:                registry,
		TenantID:                "tenant-1",
		Proposal:                proposal,
		ActorID:                 "operator-1",
		ActorKind:               "user",
		IdempotencyKey:          "apply-link-definition-update",
		ConfirmationToken:       MetadataProposalPromotionConfirmationToken(plan),
		RegistryExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Status != PromotionReceiptStatusApplied || receipt.Changes[0].Operation != PromotionOperationUpdate || receipt.Changes[0].AppliedVersion != 2 {
		t.Fatalf("receipt = %+v", receipt)
	}
	linkType, err := registry.GetLinkType(ctx, "project_uses_tool")
	if err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	if linkType.DisplayName != "Project uses approved tool" || !linkType.EvidenceRequired || linkType.Version != 2 {
		t.Fatalf("link type after update = %+v", linkType)
	}

	noopPlan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion noop: %v", err)
	}
	if !noopPlan.CanApply || len(noopPlan.Changes) != 1 || noopPlan.Changes[0].Operation != PromotionOperationNoop || noopPlan.Changes[0].CurrentVersion != 2 {
		t.Fatalf("noop plan = %+v", noopPlan)
	}
}

func TestDryRunMetadataProposalPromotionBlocksLinkTypeDefinitionWithUnknownEndpoint(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:          "proposal-link-definition-invalid",
			TargetKind:  "link_type",
			TargetID:    "project_uses_tool",
			MetadataKey: "definition",
			ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{
				ID:             "project_uses_tool",
				TenantScope:    TenantScopeTenant,
				DisplayName:    "Project uses tool",
				FromObjectType: "project",
				ToObjectType:   "tool",
				Cardinality:    CardinalityManyToMany,
			}),
			Source:     "operator_suggested.ontology_admin",
			SourceKind: MetadataProposalSourceOperatorSuggested,
			Confidence: 1,
			Status:     MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if plan.CanApply || !testPromotionPlanHasIssue(plan, PromotionIssueInvalidProposal) || len(plan.Changes) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestDryRunMetadataProposalPromotionPlansMappingRuleGovernanceUpdate(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TargetKind:    "mapping_rule",
			TargetID:      "mission.created",
			MetadataKey:   "source_domain",
			ProposedValue: "planning",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.91,
			Status:        MetadataProposalStatusAccepted,
			EvidenceRefs: []MetadataEvidenceRef{{
				Kind: "model_run",
				ID:   "run-1",
				Metadata: map[string]string{
					"model":       "ontology-designer",
					"version":     "2026-06-20",
					"input_hash":  "input-1",
					"prompt_hash": "prompt-1",
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Issues) != 0 || len(plan.Changes) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	change := plan.Changes[0]
	if change.Operation != PromotionOperationUpdate ||
		change.TargetKind != "mapping_rule" ||
		change.TargetID != "mission.created" ||
		change.Field != "source_domain" ||
		change.CurrentValue != "mission_control" ||
		change.ProposedValue != "planning" {
		t.Fatalf("change = %+v", change)
	}
}

func TestApplyMetadataProposalPromotionCreatesMappingRuleDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{ID: "project_uses_tool", TenantScope: TenantScopeTenant, TenantID: "tenant-1", FromObjectType: "project", ToObjectType: "tool", Cardinality: CardinalityManyToMany}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-mapping-definition-create",
		TenantID:    "tenant-1",
		TargetKind:  "mapping_rule",
		TargetID:    "project.tool.linked",
		MetadataKey: "definition",
		ProposedValue: mappingRuleDefinitionValueForTest(t, MappingRule{
			ID:           "project.tool.linked",
			TenantID:     "tenant-1",
			EventType:    "project.tool.linked",
			SourceDomain: "project",
			SourceRef:    "project.tool",
			ObjectTypes:  []string{"project", "tool"},
			LinkTypes:    []string{"project_uses_tool"},
			Enabled:      true,
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
					map[string]any{"type": "tool", "id": "$.tool_id", "canonical_key": "tool:{tool_id}"},
				},
				"links": []any{
					map[string]any{"type": "project_uses_tool", "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
				},
			},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Issues) != 0 || len(plan.Changes) != 1 || plan.Changes[0].Operation != PromotionOperationCreate {
		t.Fatalf("plan = %+v", plan)
	}
	var canonical map[string]any
	if err := json.Unmarshal([]byte(plan.Changes[0].ProposedValue), &canonical); err != nil {
		t.Fatalf("Unmarshal canonical mapping definition: %v", err)
	}
	if _, ok := canonical["version"]; ok {
		t.Fatalf("canonical mapping definition should not include version: %s", plan.Changes[0].ProposedValue)
	}
	if _, ok := canonical["created_at"]; ok {
		t.Fatalf("canonical mapping definition should not include created_at: %s", plan.Changes[0].ProposedValue)
	}
	if _, ok := canonical["updated_at"]; ok {
		t.Fatalf("canonical mapping definition should not include updated_at: %s", plan.Changes[0].ProposedValue)
	}
	if gotHash, ok := canonical["rule_hash"].(string); !ok || gotHash == "" {
		t.Fatalf("canonical mapping definition should include computed rule_hash: %+v", canonical)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		TenantID:          "tenant-1",
		Proposal:          proposal,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-mapping-definition-create",
		ConfirmationToken: MetadataProposalPromotionConfirmationToken(plan),
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Status != PromotionReceiptStatusApplied || receipt.Changes[0].AppliedVersion != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	projections := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)))
	if err := RegisterMappingRuleProjections(ctx, registry, projections, "tenant-1"); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	if registered := projections.ListBySourceType("project.tool.linked"); len(registered) != 1 {
		t.Fatalf("registered projections = %+v", registered)
	}
}

func TestApplyMetadataProposalPromotionFillsTenantInMappingRuleDefinitionReceiptCanonicalValue(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{ID: "project_uses_tool", TenantScope: TenantScopeTenant, TenantID: "tenant-1", FromObjectType: "project", ToObjectType: "tool", Cardinality: CardinalityManyToMany}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:          "proposal-mapping-definition-fill-tenant",
		TenantID:    "tenant-1",
		TargetKind:  "mapping_rule",
		TargetID:    "project.tool.linked",
		MetadataKey: "definition",
		ProposedValue: mappingRuleDefinitionValueForTest(t, MappingRule{
			ID:          "project.tool.linked",
			EventType:   "project.tool.linked",
			ObjectTypes: []string{"project", "tool"},
			LinkTypes:   []string{"project_uses_tool"},
			Enabled:     true,
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
					map[string]any{"type": "tool", "id": "$.tool_id", "canonical_key": "tool:{tool_id}"},
				},
				"links": []any{
					map[string]any{"type": "project_uses_tool", "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
				},
			},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		TenantID:          "tenant-1",
		Proposal:          proposal,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-mapping-definition-fill-tenant",
		ConfirmationToken: MetadataProposalPromotionConfirmationToken(plan),
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	var canonical map[string]any
	if err := json.Unmarshal([]byte(receipt.Changes[0].ProposedValue), &canonical); err != nil {
		t.Fatalf("Unmarshal receipt proposed value: %v", err)
	}
	if canonical["tenant_id"] != "tenant-1" {
		t.Fatalf("receipt proposed value should include applied tenant_id, got %s", receipt.Changes[0].ProposedValue)
	}
}

func TestDryRunMetadataProposalPromotionBlocksMappingRuleDefinitionWithUnknownLinkType(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:          "proposal-mapping-definition-invalid",
			TenantID:    "tenant-1",
			TargetKind:  "mapping_rule",
			TargetID:    "project.tool.linked",
			MetadataKey: "definition",
			ProposedValue: mappingRuleDefinitionValueForTest(t, MappingRule{
				ID:          "project.tool.linked",
				TenantID:    "tenant-1",
				EventType:   "project.tool.linked",
				ObjectTypes: []string{"project", "tool"},
				LinkTypes:   []string{"project_uses_tool"},
				Enabled:     true,
				Rule: map[string]any{
					"links": []any{
						map[string]any{"type": "project_uses_tool", "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
					},
				},
			}),
			Source:     "operator_suggested.ontology_admin",
			SourceKind: MetadataProposalSourceOperatorSuggested,
			Confidence: 1,
			Status:     MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if plan.CanApply || !testPromotionPlanHasIssue(plan, PromotionIssueInvalidProposal) || len(plan.Changes) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestApplyMetadataProposalPromotionUpdatesAndNoopsMappingRuleDefinition(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{ID: "project_uses_tool", TenantScope: TenantScopeTenant, TenantID: "tenant-1", FromObjectType: "project", ToObjectType: "tool", Cardinality: CardinalityManyToMany}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	baseRule := MappingRule{
		ID:           "project.tool.linked",
		TenantID:     "tenant-1",
		EventType:    "project.tool.linked",
		SourceDomain: "project",
		ObjectTypes:  []string{"project", "tool"},
		LinkTypes:    []string{"project_uses_tool"},
		Enabled:      true,
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
				map[string]any{"type": "tool", "id": "$.tool_id", "canonical_key": "tool:{tool_id}"},
			},
			"links": []any{
				map[string]any{"type": "project_uses_tool", "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
			},
		},
	}
	if err := registry.SaveMappingRule(ctx, baseRule); err != nil {
		t.Fatalf("SaveMappingRule: %v", err)
	}
	updatedRule := baseRule
	updatedRule.SourceRef = "project.tool.admin"
	proposal := OntologyMetadataProposal{
		ID:            "proposal-mapping-definition-update",
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "project.tool.linked",
		MetadataKey:   "definition",
		ProposedValue: mappingRuleDefinitionValueForTest(t, updatedRule),
		Source:        "operator_suggested.ontology_admin",
		SourceKind:    MetadataProposalSourceOperatorSuggested,
		Confidence:    1,
		Status:        MetadataProposalStatusAccepted,
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if !plan.CanApply || len(plan.Changes) != 1 || plan.Changes[0].Operation != PromotionOperationUpdate || plan.Changes[0].ExpectedVersion != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:                registry,
		TenantID:                "tenant-1",
		Proposal:                proposal,
		ActorID:                 "operator-1",
		ActorKind:               "user",
		IdempotencyKey:          "apply-mapping-definition-update",
		ConfirmationToken:       MetadataProposalPromotionConfirmationToken(plan),
		RegistryExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Status != PromotionReceiptStatusApplied || receipt.Changes[0].Operation != PromotionOperationUpdate || receipt.Changes[0].AppliedVersion != 2 {
		t.Fatalf("receipt = %+v", receipt)
	}
	rules, err := registry.ListMappingRules(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	if len(rules) != 1 || rules[0].SourceRef != "project.tool.admin" || rules[0].Version != 2 {
		t.Fatalf("rules after update = %+v", rules)
	}
	noopPlan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion noop: %v", err)
	}
	if !noopPlan.CanApply || len(noopPlan.Changes) != 1 || noopPlan.Changes[0].Operation != PromotionOperationNoop || noopPlan.Changes[0].CurrentVersion != 2 {
		t.Fatalf("noop plan = %+v", noopPlan)
	}
}

func TestApplyMetadataProposalPromotionAppendsObjectAllowedAction(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	request := MetadataProposalPromotionApplyRequest{
		Registry:       registry,
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "apply-1",
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusAccepted,
		},
	}
	request.ConfirmationToken = metadataPromotionConfirmationTokenForTest(t, ctx, registry, request.Proposal)
	receipt, err := ApplyMetadataProposalPromotion(ctx, request)
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied ||
		receipt.ProposalID != "proposal-1" ||
		receipt.ActorID != "operator-1" ||
		receipt.ActorKind != "user" ||
		receipt.IdempotencyKey != "apply-1" ||
		receipt.ReceiptID == "" ||
		len(receipt.Changes) != 1 ||
		len(receipt.Issues) != 0 ||
		receipt.AppliedAt.IsZero() {
		t.Fatalf("receipt = %+v", receipt)
	}
	if receipt.Changes[0].Operation != PromotionOperationAppend {
		t.Fatalf("change = %+v", receipt.Changes[0])
	}
	objectType, err := registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if !stringSliceContainsNormalized(objectType.AllowedActions, "task.complete") ||
		!stringSliceContainsNormalized(objectType.AllowedActions, "mission.plan") ||
		len(objectType.AllowedActions) != 2 {
		t.Fatalf("object allowed actions = %+v", objectType.AllowedActions)
	}
	replayReceipt, err := ApplyMetadataProposalPromotion(ctx, request)
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion replay: %v", err)
	}
	if replayReceipt.Applied || replayReceipt.ReceiptID != receipt.ReceiptID {
		t.Fatalf("replay receipt = %+v, first receipt = %+v", replayReceipt, receipt)
	}
}

func TestApplyMetadataProposalPromotionRequiresConfirmationToken(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:            "proposal-1",
		TargetKind:    "object_type",
		TargetID:      "task",
		MetadataKey:   "allowed_action",
		ProposedValue: "mission.plan",
		Source:        "deterministic.action_type_binding",
		Status:        MetadataProposalStatusAccepted,
	}
	request := MetadataProposalPromotionApplyRequest{
		Registry:       registry,
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "apply-1",
		Proposal:       proposal,
	}

	if _, err := ApplyMetadataProposalPromotion(ctx, request); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("missing confirmation token error = %v", err)
	}
	request.ConfirmationToken = "apply:proposal-1:2:0"
	if _, err := ApplyMetadataProposalPromotion(ctx, request); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("wrong confirmation token error = %v", err)
	}
	objectType, err := registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if stringSliceContainsNormalized(objectType.AllowedActions, "mission.plan") {
		t.Fatalf("object changed without matching confirmation token = %+v", objectType.AllowedActions)
	}
}

func TestApplyMetadataProposalPromotionPersistsReceiptAndReusesIdempotency(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	request := MetadataProposalPromotionApplyRequest{
		Registry:       registry,
		ReceiptStore:   receipts,
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "apply-1",
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TenantID:      "tenant-1",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusAccepted,
		},
	}
	request.ConfirmationToken = metadataPromotionConfirmationTokenForTest(t, ctx, registry, request.Proposal)
	receipt, err := ApplyMetadataProposalPromotion(ctx, request)
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || receipt.Status != PromotionReceiptStatusApplied || receipt.TenantID != "tenant-1" {
		t.Fatalf("receipt = %+v", receipt)
	}
	stored, err := receipts.GetPromotionReceipt(ctx, receipt.ReceiptID)
	if err != nil {
		t.Fatalf("GetPromotionReceipt: %v", err)
	}
	if stored.ReceiptID != receipt.ReceiptID || !stored.Applied || stored.Status != PromotionReceiptStatusApplied || len(stored.Changes) != 1 {
		t.Fatalf("stored receipt = %+v", stored)
	}
	if stored.Changes[0].ExpectedVersion != 1 || stored.Changes[0].CurrentVersion != 1 || stored.Changes[0].AppliedVersion != 2 {
		t.Fatalf("stored change versions = %+v", stored.Changes[0])
	}

	replayed, err := ApplyMetadataProposalPromotion(ctx, request)
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion replay: %v", err)
	}
	if replayed.ReceiptID != receipt.ReceiptID ||
		!replayed.Applied ||
		replayed.Status != PromotionReceiptStatusApplied ||
		!replayed.AppliedAt.Equal(receipt.AppliedAt) {
		t.Fatalf("replayed receipt = %+v, first receipt = %+v", replayed, receipt)
	}
	objectType, err := registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if len(objectType.AllowedActions) != 2 {
		t.Fatalf("object allowed actions after replay = %+v", objectType.AllowedActions)
	}
}

func TestApplyMetadataProposalPromotionRejectsIdempotencyKeyWithDifferentRequest(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	request := MetadataProposalPromotionApplyRequest{
		Registry:       registry,
		ReceiptStore:   receipts,
		TenantID:       "tenant-1",
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "apply-1",
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TenantID:      "tenant-1",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusAccepted,
		},
	}
	request.ConfirmationToken = metadataPromotionConfirmationTokenForTest(t, ctx, registry, request.Proposal)
	receipt, err := ApplyMetadataProposalPromotion(ctx, request)
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if receipt.RequestHash == "" {
		t.Fatalf("receipt request hash is empty: %+v", receipt)
	}

	conflicting := request
	conflicting.ActorID = "operator-2"
	_, err = ApplyMetadataProposalPromotion(ctx, conflicting)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting idempotency error = %v", err)
	}
}

func TestApplyMetadataProposalPromotionRejectsRequestTenantMismatch(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:            "proposal-tenant-2",
		TenantID:      "tenant-2",
		TargetKind:    "object_type",
		TargetID:      "task",
		MetadataKey:   "allowed_action",
		ProposedValue: "mission.plan",
		Source:        "deterministic.action_type_binding",
		Status:        MetadataProposalStatusAccepted,
	}

	_, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		ReceiptStore:      receipts,
		Proposal:          proposal,
		TenantID:          "tenant-1",
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-tenant-mismatch",
		ConfirmationToken: metadataPromotionConfirmationTokenForTest(t, ctx, registry, proposal),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant mismatch error = %v", err)
	}

	objectType, err := registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if stringSliceContainsNormalized(objectType.AllowedActions, "mission.plan") {
		t.Fatalf("tenant mismatch modified object allowed actions = %+v", objectType.AllowedActions)
	}
	page, err := receipts.ListPromotionReceipts(ctx, PromotionReceiptQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListPromotionReceipts: %v", err)
	}
	if page.Page.Total != 0 || len(page.Receipts) != 0 {
		t.Fatalf("tenant mismatch persisted receipts = %+v", page)
	}
}

func TestApplyMetadataProposalPromotionBatchAppliesMultipleAcceptedProposals(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "agent", TenantScope: TenantScopeGlobal, DisplayName: "Agent", CanonicalKeyRule: "agent:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(agent): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}", AllowedActions: []string{"tool.inspect"}}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{
		ID:             "agent_can_use_tool",
		TenantScope:    TenantScopeGlobal,
		DisplayName:    "Agent can use tool",
		FromObjectType: "agent",
		ToObjectType:   "tool",
		Cardinality:    CardinalityManyToMany,
		AllowedActions: []string{"resource.bind"},
	}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}

	batch, err := ApplyMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchApplyRequest{
		Registry:       registry,
		ReceiptStore:   receipts,
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "batch-1",
		ConfirmationToken: metadataPromotionBatchConfirmationTokenForTest(t, ctx, registry, []OntologyMetadataProposal{
			{ID: "proposal-object", TargetKind: "object_type", TargetID: "tool", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
			{ID: "proposal-link", TargetKind: "link_type", TargetID: "agent_can_use_tool", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
		}),
		Proposals: []OntologyMetadataProposal{
			{ID: "proposal-object", TargetKind: "object_type", TargetID: "tool", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
			{ID: "proposal-link", TargetKind: "link_type", TargetID: "agent_can_use_tool", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
		},
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotionBatch: %v", err)
	}
	if batch.Status != PromotionReceiptStatusApplied || !batch.Applied || len(batch.Receipts) != 2 || len(batch.Changes) != 2 {
		t.Fatalf("batch = %+v", batch)
	}
	tool, err := registry.GetObjectType(ctx, "tool")
	if err != nil {
		t.Fatalf("GetObjectType(tool): %v", err)
	}
	if !stringSliceContainsNormalized(tool.AllowedActions, "mcp.tool.call") {
		t.Fatalf("tool allowed actions = %+v", tool.AllowedActions)
	}
	link, err := registry.GetLinkType(ctx, "agent_can_use_tool")
	if err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	if !stringSliceContainsNormalized(link.AllowedActions, "mcp.tool.call") {
		t.Fatalf("link allowed actions = %+v", link.AllowedActions)
	}
	for _, receipt := range batch.Receipts {
		stored, err := receipts.GetPromotionReceipt(ctx, receipt.ReceiptID)
		if err != nil {
			t.Fatalf("GetPromotionReceipt(%s): %v", receipt.ReceiptID, err)
		}
		if stored.Status != PromotionReceiptStatusApplied {
			t.Fatalf("stored receipt = %+v", stored)
		}
	}
}

func TestApplyMetadataProposalPromotionBatchRequiresConfirmationToken(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}", AllowedActions: []string{"tool.inspect"}}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	proposals := []OntologyMetadataProposal{
		{ID: "proposal-tool", TargetKind: "object_type", TargetID: "tool", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
	}
	request := MetadataProposalPromotionBatchApplyRequest{
		Registry:       registry,
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "batch-1",
		Proposals:      proposals,
	}

	if _, err := ApplyMetadataProposalPromotionBatch(ctx, request); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("missing batch confirmation token error = %v", err)
	}
	request.ConfirmationToken = "batch:proposal-tool:2:0"
	if _, err := ApplyMetadataProposalPromotionBatch(ctx, request); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("wrong batch confirmation token error = %v", err)
	}
	tool, err := registry.GetObjectType(ctx, "tool")
	if err != nil {
		t.Fatalf("GetObjectType(tool): %v", err)
	}
	if stringSliceContainsNormalized(tool.AllowedActions, "mcp.tool.call") {
		t.Fatalf("tool changed without matching batch confirmation token = %+v", tool.AllowedActions)
	}
}

func TestApplyMetadataProposalPromotionBatchBlocksBeforePartialApply(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "agent", TenantScope: TenantScopeGlobal, DisplayName: "Agent", CanonicalKeyRule: "agent:{id}", AllowedActions: []string{"agent.assign"}}); err != nil {
		t.Fatalf("SaveObjectType(agent): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}", AllowedActions: []string{"tool.inspect"}}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	plan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{
		Registry: registry,
		Proposals: []OntologyMetadataProposal{
			{ID: "proposal-agent", TargetKind: "object_type", TargetID: "agent", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
			{ID: "proposal-tool", TargetKind: "object_type", TargetID: "tool", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotionBatch: %v", err)
	}
	if len(plan.Plans) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}", AllowedActions: []string{"tool.inspect", "tool.audit"}, Version: 2}); err != nil {
		t.Fatalf("concurrent SaveObjectType(tool): %v", err)
	}

	batch, err := ApplyMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchApplyRequest{
		Registry:          registry,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "batch-stale-1",
		ConfirmationToken: MetadataProposalPromotionBatchConfirmationToken(plan),
		ExpectedVersions: map[string]int{
			"proposal-agent": plan.Plans[0].Changes[0].ExpectedVersion,
			"proposal-tool":  plan.Plans[1].Changes[0].ExpectedVersion,
		},
		Proposals: []OntologyMetadataProposal{
			{ID: "proposal-agent", TargetKind: "object_type", TargetID: "agent", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
			{ID: "proposal-tool", TargetKind: "object_type", TargetID: "tool", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
		},
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("ApplyMetadataProposalPromotionBatch error = %v", err)
	}
	if batch.Status != PromotionReceiptStatusBlocked || batch.Applied || len(batch.Receipts) != 0 {
		t.Fatalf("batch = %+v", batch)
	}
	agent, err := registry.GetObjectType(ctx, "agent")
	if err != nil {
		t.Fatalf("GetObjectType(agent): %v", err)
	}
	if stringSliceContainsNormalized(agent.AllowedActions, "mcp.tool.call") {
		t.Fatalf("agent changed after blocked batch = %+v", agent.AllowedActions)
	}
}

func TestDryRunMetadataProposalPromotionBatchRejectsDuplicateTargets(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}", AllowedActions: []string{"tool.inspect"}}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}

	plan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{
		Registry: registry,
		Proposals: []OntologyMetadataProposal{
			{ID: "proposal-tool-call", TargetKind: "object_type", TargetID: "tool", MetadataKey: "allowed_action", ProposedValue: "mcp.tool.call", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
			{ID: "proposal-tool-audit", TargetKind: "object_type", TargetID: "tool", MetadataKey: "allowed_action", ProposedValue: "tool.audit", Source: "deterministic.action_type_binding", Status: MetadataProposalStatusAccepted},
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotionBatch: %v", err)
	}
	if plan.CanApply || len(plan.Issues) == 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Issues[0].Code != PromotionIssueInvalidProposal {
		t.Fatalf("issue = %+v", plan.Issues[0])
	}
}

func TestDryRunMetadataProposalPromotionBatchUsesOverlayForLinkTypeAndMappingRuleDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	linkProposal := OntologyMetadataProposal{
		ID:          "proposal-link-definition",
		TenantID:    "tenant-1",
		TargetKind:  "link_type",
		TargetID:    "project_uses_tool",
		MetadataKey: "definition",
		ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{
			ID:             "project_uses_tool",
			TenantID:       "tenant-1",
			TenantScope:    TenantScopeTenant,
			DisplayName:    "Project uses tool",
			FromObjectType: "project",
			ToObjectType:   "tool",
			Cardinality:    CardinalityManyToMany,
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	mappingProposal := OntologyMetadataProposal{
		ID:          "proposal-mapping-definition",
		TenantID:    "tenant-1",
		TargetKind:  "mapping_rule",
		TargetID:    "project.tool.linked",
		MetadataKey: "definition",
		ProposedValue: mappingRuleDefinitionValueForTest(t, MappingRule{
			ID:          "project.tool.linked",
			TenantID:    "tenant-1",
			EventType:   "project.tool.linked",
			ObjectTypes: []string{"project", "tool"},
			LinkTypes:   []string{"project_uses_tool"},
			Enabled:     true,
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
					map[string]any{"type": "tool", "id": "$.tool_id", "canonical_key": "tool:{tool_id}"},
				},
				"links": []any{
					map[string]any{"type": "project_uses_tool", "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
				},
			},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}

	plan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{
		Registry:  registry,
		Proposals: []OntologyMetadataProposal{linkProposal, mappingProposal},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotionBatch: %v", err)
	}
	if !plan.CanApply || len(plan.Issues) != 0 || len(plan.Plans) != 2 || len(plan.Changes) != 2 {
		t.Fatalf("batch plan = %+v", plan)
	}
	if plan.Plans[0].Changes[0].TargetKind != "link_type" || plan.Plans[1].Changes[0].TargetKind != "mapping_rule" {
		t.Fatalf("batch plan order = %+v", plan.Plans)
	}
	if _, err := registry.GetLinkType(ctx, "project_uses_tool"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dry-run should not persist overlay link type, err = %v", err)
	}
}

func TestDryRunMetadataProposalPromotionBatchOrdersDependentDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	objectProposal := metadataPromotionObjectDefinitionProposalForTest(t, "proposal-object-definition", "project")
	toolProposal := metadataPromotionObjectDefinitionProposalForTest(t, "proposal-tool-definition", "tool")
	propertyProposal := metadataPromotionPropertyDefinitionProposalForTest(t, "proposal-property-definition", "tool.risk_level", "tool", "risk_level")
	linkProposal := metadataPromotionLinkDefinitionProposalForTest(t, "proposal-link-definition", "project_uses_tool", "project", "tool")
	mappingProposal := metadataPromotionMappingDefinitionProposalForTest(t, "proposal-mapping-definition", "project.tool.linked", "project_uses_tool")

	plan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{
		Registry: registry,
		Proposals: []OntologyMetadataProposal{
			mappingProposal,
			linkProposal,
			propertyProposal,
			toolProposal,
			objectProposal,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotionBatch: %v", err)
	}
	if !plan.CanApply || len(plan.Issues) != 0 || len(plan.Plans) != 5 || len(plan.Changes) != 5 {
		t.Fatalf("batch plan = %+v", plan)
	}
	gotKinds := make([]string, 0, len(plan.Plans))
	for _, childPlan := range plan.Plans {
		if len(childPlan.Changes) != 1 {
			t.Fatalf("child plan = %+v", childPlan)
		}
		gotKinds = append(gotKinds, childPlan.Changes[0].TargetKind)
	}
	wantKinds := []string{"object_type", "object_type", "property_type", "link_type", "mapping_rule"}
	for i := range wantKinds {
		if gotKinds[i] != wantKinds[i] {
			t.Fatalf("plan order = %+v, want %+v", gotKinds, wantKinds)
		}
	}
	if _, err := registry.GetObjectType(ctx, "project"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dry-run should not persist ordered overlay object type, err = %v", err)
	}
}

func TestDryRunMetadataProposalPromotionRejectsCrossTenantMappingRuleDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{ID: "project_uses_tool", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "Project uses tool", FromObjectType: "project", ToObjectType: "tool", Cardinality: CardinalityManyToMany}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}

	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:          "proposal-cross-tenant-mapping-definition",
			TenantID:    "tenant-2",
			TargetKind:  "mapping_rule",
			TargetID:    "project.tool.linked",
			MetadataKey: "definition",
			ProposedValue: mappingRuleDefinitionValueForTest(t, MappingRule{
				ID:          "project.tool.linked",
				TenantID:    "tenant-2",
				EventType:   "project.tool.linked",
				ObjectTypes: []string{"project", "tool"},
				LinkTypes:   []string{"project_uses_tool"},
				Enabled:     true,
				Rule: map[string]any{
					"objects": []any{
						map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
						map[string]any{"type": "tool", "id": "$.tool_id", "canonical_key": "tool:{tool_id}"},
					},
					"links": []any{
						map[string]any{"type": "project_uses_tool", "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
					},
				},
			}),
			Source:     "operator_suggested.ontology_admin",
			SourceKind: MetadataProposalSourceOperatorSuggested,
			Confidence: 1,
			Status:     MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if plan.CanApply || !testPromotionPlanHasIssue(plan, PromotionIssueInvalidProposal) {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestApplyMetadataProposalPromotionBatchAppliesLinkTypeAndMappingRuleDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeGlobal, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(project): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	linkProposal := OntologyMetadataProposal{
		ID:            "proposal-link-definition",
		TenantID:      "tenant-1",
		TargetKind:    "link_type",
		TargetID:      "project_uses_tool",
		MetadataKey:   "definition",
		ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{ID: "project_uses_tool", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "Project uses tool", FromObjectType: "project", ToObjectType: "tool", Cardinality: CardinalityManyToMany}),
		Source:        "operator_suggested.ontology_admin",
		SourceKind:    MetadataProposalSourceOperatorSuggested,
		Confidence:    1,
		Status:        MetadataProposalStatusAccepted,
	}
	mappingProposal := OntologyMetadataProposal{
		ID:          "proposal-mapping-definition",
		TenantID:    "tenant-1",
		TargetKind:  "mapping_rule",
		TargetID:    "project.tool.linked",
		MetadataKey: "definition",
		ProposedValue: mappingRuleDefinitionValueForTest(t, MappingRule{
			ID:          "project.tool.linked",
			TenantID:    "tenant-1",
			EventType:   "project.tool.linked",
			ObjectTypes: []string{"project", "tool"},
			LinkTypes:   []string{"project_uses_tool"},
			Enabled:     true,
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
					map[string]any{"type": "tool", "id": "$.tool_id", "canonical_key": "tool:{tool_id}"},
				},
				"links": []any{
					map[string]any{"type": "project_uses_tool", "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
				},
			},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	proposals := []OntologyMetadataProposal{linkProposal, mappingProposal}
	plan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{Registry: registry, Proposals: proposals})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotionBatch: %v", err)
	}
	batch, err := ApplyMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchApplyRequest{
		Registry:          registry,
		ReceiptStore:      receipts,
		TenantID:          "tenant-1",
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "batch-definition-1",
		ConfirmationToken: MetadataProposalPromotionBatchConfirmationToken(plan),
		Proposals:         proposals,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotionBatch: %v", err)
	}
	if batch.Status != PromotionReceiptStatusApplied || !batch.Applied || len(batch.Receipts) != 2 || len(batch.Changes) != 2 {
		t.Fatalf("batch = %+v", batch)
	}
	if _, err := registry.GetLinkType(ctx, "project_uses_tool"); err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	rules, err := registry.ListMappingRules(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != "project.tool.linked" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestApplyMetadataProposalPromotionBatchOrdersDependentDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	objectProposal := metadataPromotionObjectDefinitionProposalForTest(t, "proposal-object-definition", "project")
	toolProposal := metadataPromotionObjectDefinitionProposalForTest(t, "proposal-tool-definition", "tool")
	propertyProposal := metadataPromotionPropertyDefinitionProposalForTest(t, "proposal-property-definition", "tool.risk_level", "tool", "risk_level")
	linkProposal := metadataPromotionLinkDefinitionProposalForTest(t, "proposal-link-definition", "project_uses_tool", "project", "tool")
	mappingProposal := metadataPromotionMappingDefinitionProposalForTest(t, "proposal-mapping-definition", "project.tool.linked", "project_uses_tool")
	proposals := []OntologyMetadataProposal{mappingProposal, linkProposal, propertyProposal, toolProposal, objectProposal}
	plan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{Registry: registry, Proposals: proposals})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotionBatch: %v", err)
	}
	if !plan.CanApply {
		t.Fatalf("batch plan = %+v", plan)
	}

	batch, err := ApplyMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchApplyRequest{
		Registry:          registry,
		ReceiptStore:      receipts,
		TenantID:          "tenant-1",
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "batch-ordered-definition-1",
		ConfirmationToken: MetadataProposalPromotionBatchConfirmationToken(plan),
		Proposals:         proposals,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotionBatch: %v", err)
	}
	if batch.Status != PromotionReceiptStatusApplied || !batch.Applied || len(batch.Receipts) != 5 || len(batch.Changes) != 5 {
		t.Fatalf("batch = %+v", batch)
	}
	if _, err := registry.GetObjectType(ctx, "project"); err != nil {
		t.Fatalf("GetObjectType(project): %v", err)
	}
	if _, err := registry.GetObjectType(ctx, "tool"); err != nil {
		t.Fatalf("GetObjectType(tool): %v", err)
	}
	if _, err := registry.GetPropertyType(ctx, "tool.risk_level"); err != nil {
		t.Fatalf("GetPropertyType(tool.risk_level): %v", err)
	}
	if _, err := registry.GetLinkType(ctx, "project_uses_tool"); err != nil {
		t.Fatalf("GetLinkType(project_uses_tool): %v", err)
	}
	rules, err := registry.ListMappingRules(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != "project.tool.linked" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestApplyMetadataProposalPromotionBatchAppliesObjectPropertyLinkAndMappingDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	objectProposal := OntologyMetadataProposal{
		ID:          "proposal-object-definition",
		TenantID:    "tenant-1",
		TargetKind:  "object_type",
		TargetID:    "project",
		MetadataKey: "definition",
		ProposedValue: objectTypeDefinitionValueForTest(t, ObjectType{
			ID:               "project",
			TenantID:         "tenant-1",
			TenantScope:      TenantScopeTenant,
			DisplayName:      "Project",
			CanonicalKeyRule: "project:{id}",
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	toolProposal := OntologyMetadataProposal{
		ID:          "proposal-tool-definition",
		TenantID:    "tenant-1",
		TargetKind:  "object_type",
		TargetID:    "tool",
		MetadataKey: "definition",
		ProposedValue: objectTypeDefinitionValueForTest(t, ObjectType{
			ID:               "tool",
			TenantID:         "tenant-1",
			TenantScope:      TenantScopeTenant,
			DisplayName:      "Tool",
			CanonicalKeyRule: "tool:{id}",
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	propertyProposal := OntologyMetadataProposal{
		ID:          "proposal-property-definition",
		TenantID:    "tenant-1",
		TargetKind:  "property_type",
		TargetID:    "tool.risk_level",
		MetadataKey: "definition",
		ProposedValue: propertyTypeDefinitionValueForTest(t, PropertyType{
			ID:             "tool.risk_level",
			TenantID:       "tenant-1",
			ObjectTypeID:   "tool",
			PropertyID:     "risk_level",
			DisplayName:    "Risk level",
			ValueType:      ValueTypeString,
			ConflictPolicy: ConflictPolicyManualReview,
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	linkProposal := OntologyMetadataProposal{
		ID:          "proposal-link-definition",
		TenantID:    "tenant-1",
		TargetKind:  "link_type",
		TargetID:    "project_uses_tool",
		MetadataKey: "definition",
		ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{
			ID:             "project_uses_tool",
			TenantID:       "tenant-1",
			TenantScope:    TenantScopeTenant,
			DisplayName:    "Project uses tool",
			FromObjectType: "project",
			ToObjectType:   "tool",
			Cardinality:    CardinalityManyToMany,
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	mappingProposal := OntologyMetadataProposal{
		ID:          "proposal-mapping-definition",
		TenantID:    "tenant-1",
		TargetKind:  "mapping_rule",
		TargetID:    "project.tool.linked",
		MetadataKey: "definition",
		ProposedValue: mappingRuleDefinitionValueForTest(t, MappingRule{
			ID:          "project.tool.linked",
			TenantID:    "tenant-1",
			EventType:   "project.tool.linked",
			ObjectTypes: []string{"project", "tool"},
			LinkTypes:   []string{"project_uses_tool"},
			Enabled:     true,
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
					map[string]any{"type": "tool", "id": "$.tool_id", "canonical_key": "tool:{tool_id}"},
				},
				"facts": []any{
					map[string]any{"object_type": "tool", "object_id": "$.tool_id", "predicate": "risk_level", "value": "$.risk_level", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "project_uses_tool", "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
				},
			},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
	proposals := []OntologyMetadataProposal{objectProposal, toolProposal, propertyProposal, linkProposal, mappingProposal}
	plan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{Registry: registry, Proposals: proposals})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotionBatch: %v", err)
	}
	if !plan.CanApply || len(plan.Issues) != 0 || len(plan.Plans) != 5 || len(plan.Changes) != 5 {
		t.Fatalf("batch plan = %+v", plan)
	}
	if _, err := registry.GetObjectType(ctx, "project"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dry-run should not persist overlay object type, err = %v", err)
	}

	batch, err := ApplyMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchApplyRequest{
		Registry:          registry,
		ReceiptStore:      receipts,
		TenantID:          "tenant-1",
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "batch-all-definition-1",
		ConfirmationToken: MetadataProposalPromotionBatchConfirmationToken(plan),
		Proposals:         proposals,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotionBatch: %v", err)
	}
	if batch.Status != PromotionReceiptStatusApplied || !batch.Applied || len(batch.Receipts) != 5 || len(batch.Changes) != 5 {
		t.Fatalf("batch = %+v", batch)
	}
	if _, err := registry.GetObjectType(ctx, "project"); err != nil {
		t.Fatalf("GetObjectType(project): %v", err)
	}
	if _, err := registry.GetObjectType(ctx, "tool"); err != nil {
		t.Fatalf("GetObjectType(tool): %v", err)
	}
	if _, err := registry.GetPropertyType(ctx, "tool.risk_level"); err != nil {
		t.Fatalf("GetPropertyType: %v", err)
	}
	if _, err := registry.GetLinkType(ctx, "project_uses_tool"); err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	rules, err := registry.ListMappingRules(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != "project.tool.linked" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestApplyMetadataProposalPromotionRejectsStaleRegistryVersion(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusAccepted,
		},
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ExpectedVersion != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete", "task.assign"},
		Version:          2,
	}); err != nil {
		t.Fatalf("concurrent SaveObjectType: %v", err)
	}

	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:                registry,
		ReceiptStore:            receipts,
		ActorID:                 "operator-1",
		ActorKind:               "user",
		IdempotencyKey:          "stale-apply-1",
		ConfirmationToken:       MetadataProposalPromotionConfirmationToken(plan),
		RegistryExpectedVersion: plan.Changes[0].ExpectedVersion,
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusAccepted,
		},
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("ApplyMetadataProposalPromotion error = %v", err)
	}
	if receipt.Status != PromotionReceiptStatusBlocked || receipt.Applied || len(receipt.Issues) != 1 || receipt.Issues[0].Code != PromotionIssueRegistryVersionConflict {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(receipt.Changes) != 1 || receipt.Changes[0].ExpectedVersion != 1 || receipt.Changes[0].CurrentVersion != 2 || receipt.Changes[0].AppliedVersion != 0 {
		t.Fatalf("receipt change = %+v", receipt.Changes)
	}
	stored, err := receipts.GetPromotionReceipt(ctx, receipt.ReceiptID)
	if err != nil {
		t.Fatalf("GetPromotionReceipt: %v", err)
	}
	if stored.Status != PromotionReceiptStatusBlocked || len(stored.Changes) != 1 || stored.Changes[0].CurrentVersion != 2 {
		t.Fatalf("stored receipt = %+v", stored)
	}
	objectType, err := registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if stringSliceContainsNormalized(objectType.AllowedActions, "mission.plan") {
		t.Fatalf("stale apply modified object allowed actions = %+v", objectType.AllowedActions)
	}
}

func metadataPromotionConfirmationTokenForTest(t *testing.T, ctx context.Context, registry Registry, proposal OntologyMetadataProposal) string {
	t.Helper()
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: registry,
		Proposal: proposal,
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	return MetadataProposalPromotionConfirmationToken(plan)
}

func metadataPromotionBatchConfirmationTokenForTest(t *testing.T, ctx context.Context, registry Registry, proposals []OntologyMetadataProposal) string {
	t.Helper()
	plan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{
		Registry:  registry,
		Proposals: proposals,
	})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotionBatch: %v", err)
	}
	return MetadataProposalPromotionBatchConfirmationToken(plan)
}

func TestApplyMetadataProposalPromotionPersistsBlockedReceipt(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		ReceiptStore:      receipts,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "blocked-1",
		ConfirmationToken: "apply:proposal-1:0:1",
		Proposal: OntologyMetadataProposal{
			ID:            "proposal-1",
			TargetKind:    "object_type",
			TargetID:      "task",
			MetadataKey:   "allowed_action",
			ProposedValue: "mission.plan",
			Source:        "deterministic.action_type_binding",
			Status:        MetadataProposalStatusProposed,
		},
	})
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("ApplyMetadataProposalPromotion error = %v", err)
	}
	if receipt.Status != PromotionReceiptStatusBlocked || receipt.ErrorMessage == "" || len(receipt.Issues) != 1 {
		t.Fatalf("blocked receipt = %+v", receipt)
	}
	stored, err := receipts.GetPromotionReceipt(ctx, receipt.ReceiptID)
	if err != nil {
		t.Fatalf("GetPromotionReceipt: %v", err)
	}
	if stored.Status != PromotionReceiptStatusBlocked || stored.ErrorMessage == "" || len(stored.Issues) != 1 {
		t.Fatalf("stored blocked receipt = %+v", stored)
	}
}

func TestApplyMetadataProposalPromotionWithPlatformRegistryPersistsReceipt(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-promotion-platform-receipts"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyTemporalSemanticsMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
		ontologyActionBindingsMigration(),
		ontologyMetadataProposalPromotionReceiptMigration(),
		ontologyMetadataPromotionReceiptRequestHashMigration(),
		ontologyMetadataPromotionReceiptTenantIdempotencyMigration(),
		ontologyMetadataReceiptProposalBindingMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	registry := NewPlatformRegistry(sqlStore)
	receipts := NewPlatformMetadataProposalPromotionReceiptStore(sqlStore)
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantID:         "tenant-1",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
		AllowedActions:   []string{"task.complete"},
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	platformProposal := OntologyMetadataProposal{
		ID:            "proposal-platform",
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "task",
		MetadataKey:   "allowed_action",
		ProposedValue: "mission.plan",
		Source:        "deterministic.action_type_binding",
		Status:        MetadataProposalStatusAccepted,
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		ReceiptStore:      receipts,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-platform-1",
		ConfirmationToken: metadataPromotionConfirmationTokenForTest(t, ctx, registry, platformProposal),
		Proposal:          platformProposal,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	stored, err := receipts.GetPromotionReceipt(ctx, receipt.ReceiptID)
	if err != nil {
		t.Fatalf("GetPromotionReceipt: %v", err)
	}
	if stored.Status != PromotionReceiptStatusApplied || !stored.Applied || len(stored.Changes) != 1 {
		t.Fatalf("stored receipt = %+v", stored)
	}
	objectType, err := registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if !stringSliceContainsNormalized(objectType.AllowedActions, "mission.plan") {
		t.Fatalf("object allowed actions = %+v", objectType.AllowedActions)
	}
}

func TestApplyMetadataProposalPromotionAppendsLinkAndPropertyAllowedActions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "agent", TenantScope: TenantScopeGlobal, DisplayName: "Agent", CanonicalKeyRule: "agent:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(agent): %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "tool", TenantScope: TenantScopeGlobal, DisplayName: "Tool", CanonicalKeyRule: "tool:{id}"}); err != nil {
		t.Fatalf("SaveObjectType(tool): %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{
		ID:             "agent_can_use_tool",
		TenantScope:    TenantScopeGlobal,
		DisplayName:    "Agent can use tool",
		FromObjectType: "agent",
		ToObjectType:   "tool",
		Cardinality:    CardinalityManyToMany,
		AllowedActions: []string{"resource.bind"},
	}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	if err := registry.SavePropertyType(ctx, PropertyType{
		ID:              "tool.risk_level",
		ObjectTypeID:    "tool",
		PropertyID:      "risk_level",
		ValueType:       ValueTypeString,
		AllowedActions:  []string{"resource.classify"},
		AuthoritySource: "resource_admin",
		ConflictPolicy:  ConflictPolicyAuthorityWins,
	}); err != nil {
		t.Fatalf("SavePropertyType: %v", err)
	}

	linkProposal := OntologyMetadataProposal{
		ID:            "proposal-link",
		TargetKind:    "link_type",
		TargetID:      "agent_can_use_tool",
		MetadataKey:   "allowed_action",
		ProposedValue: "mcp.tool.call",
		Source:        "deterministic.action_type_binding",
		Status:        MetadataProposalStatusAccepted,
	}
	if _, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-link-1",
		ConfirmationToken: metadataPromotionConfirmationTokenForTest(t, ctx, registry, linkProposal),
		Proposal:          linkProposal,
	}); err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion link: %v", err)
	}
	propertyProposal := OntologyMetadataProposal{
		ID:            "proposal-property",
		TargetKind:    "property_type",
		TargetID:      "tool.risk_level",
		MetadataKey:   "allowed_action",
		ProposedValue: "mcp.tool.call",
		Source:        "deterministic.action_type_binding",
		Status:        MetadataProposalStatusAccepted,
	}
	if _, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-property-1",
		ConfirmationToken: metadataPromotionConfirmationTokenForTest(t, ctx, registry, propertyProposal),
		Proposal:          propertyProposal,
	}); err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion property: %v", err)
	}

	linkType, err := registry.GetLinkType(ctx, "agent_can_use_tool")
	if err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	if !stringSliceContainsNormalized(linkType.AllowedActions, "resource.bind") ||
		!stringSliceContainsNormalized(linkType.AllowedActions, "mcp.tool.call") ||
		len(linkType.AllowedActions) != 2 {
		t.Fatalf("link allowed actions = %+v", linkType.AllowedActions)
	}
	propertyType, err := registry.GetPropertyType(ctx, "tool.risk_level")
	if err != nil {
		t.Fatalf("GetPropertyType: %v", err)
	}
	if !stringSliceContainsNormalized(propertyType.AllowedActions, "resource.classify") ||
		!stringSliceContainsNormalized(propertyType.AllowedActions, "mcp.tool.call") ||
		len(propertyType.AllowedActions) != 2 {
		t.Fatalf("property allowed actions = %+v", propertyType.AllowedActions)
	}
}

func TestApplyMetadataProposalPromotionUpdatesMappingRuleGovernance(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	ruleProposal := OntologyMetadataProposal{
		ID:            "proposal-rule",
		TargetKind:    "mapping_rule",
		TargetID:      "mission.created",
		MetadataKey:   "source_domain",
		ProposedValue: "planning",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.91,
		Status:        MetadataProposalStatusAccepted,
		EvidenceRefs: []MetadataEvidenceRef{{
			Kind: "model_run",
			ID:   "run-1",
			Metadata: map[string]string{
				"model":       "ontology-designer",
				"version":     "2026-06-20",
				"input_hash":  "input-1",
				"prompt_hash": "prompt-1",
			},
		}},
	}
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		ActorID:           "operator-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-rule-1",
		ConfirmationToken: metadataPromotionConfirmationTokenForTest(t, ctx, registry, ruleProposal),
		Proposal:          ruleProposal,
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if !receipt.Applied || len(receipt.Changes) != 1 || receipt.Changes[0].Operation != PromotionOperationUpdate {
		t.Fatalf("receipt = %+v", receipt)
	}
	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	var found MappingRule
	for _, rule := range rules {
		if rule.ID == "mission.created" {
			found = rule
			break
		}
	}
	if found.ID == "" {
		t.Fatal("mission.created mapping rule was not found")
	}
	if found.SourceDomain != "planning" {
		t.Fatalf("source domain = %q", found.SourceDomain)
	}
}

func TestApplyMetadataProposalPromotionRejectsUnsafeRequests(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantScope:      TenantScopeGlobal,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	proposal := OntologyMetadataProposal{
		ID:            "proposal-1",
		TargetKind:    "object_type",
		TargetID:      "task",
		MetadataKey:   "allowed_action",
		ProposedValue: "mission.plan",
		Source:        "deterministic.action_type_binding",
		Status:        MetadataProposalStatusAccepted,
	}

	if _, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:       registry,
		IdempotencyKey: "apply-1",
		Proposal:       proposal,
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("missing actor error = %v", err)
	}
	if _, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:  registry,
		ActorID:   "operator-1",
		ActorKind: "user",
		Proposal:  proposal,
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("missing idempotency key error = %v", err)
	}
	proposal.Status = MetadataProposalStatusProposed
	if _, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:       registry,
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "apply-1",
		Proposal:       proposal,
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("not accepted error = %v", err)
	}
}

func objectTypeDefinitionValueForTest(t *testing.T, objectType ObjectType) string {
	t.Helper()
	data, err := json.Marshal(objectType)
	if err != nil {
		t.Fatalf("Marshal object type definition: %v", err)
	}
	return string(data)
}

func linkTypeDefinitionValueForTest(t *testing.T, linkType LinkType) string {
	t.Helper()
	data, err := json.Marshal(linkType)
	if err != nil {
		t.Fatalf("Marshal link type definition: %v", err)
	}
	return string(data)
}

func propertyTypeDefinitionValueForTest(t *testing.T, propertyType PropertyType) string {
	t.Helper()
	data, err := json.Marshal(propertyType)
	if err != nil {
		t.Fatalf("Marshal property type definition: %v", err)
	}
	return string(data)
}

func mappingRuleDefinitionValueForTest(t *testing.T, rule MappingRule) string {
	t.Helper()
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("Marshal mapping rule definition: %v", err)
	}
	return string(data)
}

func metadataPromotionObjectDefinitionProposalForTest(t *testing.T, proposalID string, objectTypeID string) OntologyMetadataProposal {
	t.Helper()
	return OntologyMetadataProposal{
		ID:          proposalID,
		TenantID:    "tenant-1",
		TargetKind:  "object_type",
		TargetID:    objectTypeID,
		MetadataKey: "definition",
		ProposedValue: objectTypeDefinitionValueForTest(t, ObjectType{
			ID:               objectTypeID,
			TenantID:         "tenant-1",
			TenantScope:      TenantScopeTenant,
			DisplayName:      objectTypeID,
			CanonicalKeyRule: objectTypeID + ":{id}",
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
}

func metadataPromotionPropertyDefinitionProposalForTest(t *testing.T, proposalID string, propertyTypeID string, objectTypeID string, propertyID string) OntologyMetadataProposal {
	t.Helper()
	return OntologyMetadataProposal{
		ID:          proposalID,
		TenantID:    "tenant-1",
		TargetKind:  "property_type",
		TargetID:    propertyTypeID,
		MetadataKey: "definition",
		ProposedValue: propertyTypeDefinitionValueForTest(t, PropertyType{
			ID:             propertyTypeID,
			TenantID:       "tenant-1",
			ObjectTypeID:   objectTypeID,
			PropertyID:     propertyID,
			DisplayName:    propertyID,
			ValueType:      ValueTypeString,
			ConflictPolicy: ConflictPolicyManualReview,
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
}

func metadataPromotionLinkDefinitionProposalForTest(t *testing.T, proposalID string, linkTypeID string, fromObjectType string, toObjectType string) OntologyMetadataProposal {
	t.Helper()
	return OntologyMetadataProposal{
		ID:          proposalID,
		TenantID:    "tenant-1",
		TargetKind:  "link_type",
		TargetID:    linkTypeID,
		MetadataKey: "definition",
		ProposedValue: linkTypeDefinitionValueForTest(t, LinkType{
			ID:             linkTypeID,
			TenantID:       "tenant-1",
			TenantScope:    TenantScopeTenant,
			DisplayName:    linkTypeID,
			FromObjectType: fromObjectType,
			ToObjectType:   toObjectType,
			Cardinality:    CardinalityManyToMany,
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
}

func metadataPromotionMappingDefinitionProposalForTest(t *testing.T, proposalID string, mappingRuleID string, linkTypeID string) OntologyMetadataProposal {
	t.Helper()
	return OntologyMetadataProposal{
		ID:          proposalID,
		TenantID:    "tenant-1",
		TargetKind:  "mapping_rule",
		TargetID:    mappingRuleID,
		MetadataKey: "definition",
		ProposedValue: mappingRuleDefinitionValueForTest(t, MappingRule{
			ID:          mappingRuleID,
			TenantID:    "tenant-1",
			EventType:   mappingRuleID,
			ObjectTypes: []string{"project", "tool"},
			LinkTypes:   []string{linkTypeID},
			Enabled:     true,
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
					map[string]any{"type": "tool", "id": "$.tool_id", "canonical_key": "tool:{tool_id}"},
				},
				"facts": []any{
					map[string]any{"object_type": "tool", "object_id": "$.tool_id", "predicate": "risk_level", "value": "$.risk_level", "optional": true},
				},
				"links": []any{
					map[string]any{"type": linkTypeID, "from_type": "project", "from_id": "$.project_id", "to_type": "tool", "to_id": "$.tool_id"},
				},
			},
		}),
		Source:     "operator_suggested.ontology_admin",
		SourceKind: MetadataProposalSourceOperatorSuggested,
		Confidence: 1,
		Status:     MetadataProposalStatusAccepted,
	}
}

func testPromotionPlanHasIssue(plan MetadataProposalPromotionPlan, code PromotionIssueCode) bool {
	for _, issue := range plan.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
