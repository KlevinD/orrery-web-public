package ontology

import "testing"

func TestInferActionTypeBindingMetadataProposals(t *testing.T) {
	result, err := InferActionTypeBindingMetadataProposals(ActionTypeBindingProposalRequest{
		TenantID:     "tenant-1",
		ActionTypeID: "mcp.tool.call",
		Targets: []ActionTypeBindingTarget{
			{Kind: ActionBindingTargetObjectType, ID: "tool"},
			{Kind: ActionBindingTargetLinkType, ID: "agent_can_use_tool"},
			{Kind: ActionBindingTargetPropertyType, ID: "tool.risk_level"},
			{Kind: ActionBindingTargetObjectType, ID: "mcp_server", ExistingActionIDs: []string{"mcp.tool.call"}},
		},
		EvidenceRefs: []MetadataEvidenceRef{{Kind: "action_type", ID: "mcp.tool.call"}},
	})
	if err != nil {
		t.Fatalf("InferActionTypeBindingMetadataProposals: %v", err)
	}
	if result.ValidTargetCount != 4 || result.SkippedExistingCount != 1 || len(result.ValidationErrors) != 0 {
		t.Fatalf("result counters = %+v", result)
	}
	if len(result.Proposals) != 3 {
		t.Fatalf("proposal count = %d, want 3: %+v", len(result.Proposals), result.Proposals)
	}

	wantTargets := map[string]bool{
		"object_type:tool":              false,
		"link_type:agent_can_use_tool":  false,
		"property_type:tool.risk_level": false,
	}
	for _, proposal := range result.Proposals {
		key := proposal.TargetKind + ":" + proposal.TargetID
		if _, ok := wantTargets[key]; !ok {
			t.Fatalf("unexpected proposal target = %+v", proposal)
		}
		wantTargets[key] = true
		if proposal.ID == "" || proposal.TenantID != "tenant-1" {
			t.Fatalf("proposal identity = %+v", proposal)
		}
		if proposal.MetadataKey != "allowed_action" || proposal.ProposedValue != "mcp.tool.call" {
			t.Fatalf("proposal binding = %+v", proposal)
		}
		if proposal.Source != "deterministic.action_type_binding" ||
			proposal.SourceKind != MetadataProposalSourceDeterministic ||
			proposal.SourceRef != "action_type_binding" ||
			proposal.SourceVersion != "v1" ||
			proposal.Status != MetadataProposalStatusProposed ||
			proposal.Confidence != 1 ||
			proposal.Reason == "" {
			t.Fatalf("proposal governance metadata = %+v", proposal)
		}
		if !metadataEvidenceContains(proposal.EvidenceRefs, "action_type", "mcp.tool.call") ||
			!metadataEvidenceContains(proposal.EvidenceRefs, proposal.TargetKind, proposal.TargetID) {
			t.Fatalf("proposal evidence = %+v", proposal.EvidenceRefs)
		}
	}
	for target, seen := range wantTargets {
		if !seen {
			t.Fatalf("missing proposal for %s", target)
		}
	}
}

func TestInferActionTypeBindingMetadataProposalsReportsValidation(t *testing.T) {
	result, err := InferActionTypeBindingMetadataProposals(ActionTypeBindingProposalRequest{
		ActionTypeID: "mission.plan",
		Targets: []ActionTypeBindingTarget{
			{Kind: "workflow", ID: "mission"},
			{Kind: ActionBindingTargetObjectType, ID: ""},
		},
	})
	if err != nil {
		t.Fatalf("InferActionTypeBindingMetadataProposals: %v", err)
	}
	if len(result.Proposals) != 0 || result.ValidTargetCount != 0 || result.SkippedInvalidCount != 2 {
		t.Fatalf("result counters = %+v", result)
	}
	if len(result.ValidationErrors) != 2 {
		t.Fatalf("validation errors = %+v", result.ValidationErrors)
	}
}

func TestInferActionTypeBindingMetadataProposalsRequiresActionType(t *testing.T) {
	_, err := InferActionTypeBindingMetadataProposals(ActionTypeBindingProposalRequest{
		Targets: []ActionTypeBindingTarget{{Kind: ActionBindingTargetObjectType, ID: "task"}},
	})
	if err == nil {
		t.Fatalf("InferActionTypeBindingMetadataProposals allowed missing action_type_id")
	}
}

func metadataEvidenceContains(refs []MetadataEvidenceRef, kind string, id string) bool {
	for _, ref := range refs {
		if ref.Kind == kind && ref.ID == id {
			return true
		}
	}
	return false
}
