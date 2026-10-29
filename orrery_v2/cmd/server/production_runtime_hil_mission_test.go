package main

import (
	"context"
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/missioncontrol"
)

func TestProductionRuntimeHILMissionResolverIgnoresNonWechatApproval(t *testing.T) {
	reported := false
	resolver := productionRuntimeHILMissionResolver{
		missions: &missioncontrol.CommandService{},
		report:   func(error) { reported = true },
	}
	err := resolver.ResumeDetachedHILRuntimeApproval(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "decision-generic", Status: hil.DecisionResolved, ArgsHash: "sha256:generic"},
		Receipt:  hil.DecisionReceipt{ID: "receipt-generic", Effect: hil.EffectAuthorize, ArgsHash: "sha256:generic", SingleUse: true},
	}, discussionsession.ConversationRecordView{}, discussionsession.EventRecord{Metadata: map[string]string{
		runtimecore.ApprovalMetadataToolName: "exec_command",
	}})
	if err != nil {
		t.Fatalf("ResumeDetachedHILRuntimeApproval: %v", err)
	}
	if reported {
		t.Fatal("non-WeChat approval was reported as a Mission continuation")
	}
}

func TestProductionRuntimeHILMissionResolverReportsWechatBindingFailure(t *testing.T) {
	var reported error
	resolver := productionRuntimeHILMissionResolver{
		missions: &missioncontrol.CommandService{},
		report:   func(err error) { reported = err },
	}
	err := resolver.ResumeDetachedHILRuntimeApproval(context.Background(), hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "decision-wechat", Status: hil.DecisionResolved, ArgsHash: "sha256:wechat"},
		Receipt:  hil.DecisionReceipt{ID: "receipt-wechat", Effect: hil.EffectAuthorize, ArgsHash: "sha256:wechat", SingleUse: true},
	}, discussionsession.ConversationRecordView{}, discussionsession.EventRecord{Metadata: map[string]string{
		runtimecore.ApprovalMetadataToolName: marketingWechatDraftApprovalTool,
	}})
	if err == nil {
		t.Fatal("invalid WeChat continuation binding was accepted")
	}
	if reported == nil || reported.Error() != err.Error() {
		t.Fatalf("reported error = %v, returned error = %v", reported, err)
	}
}

func TestProductionRuntimeHILRepeatedEventUsesOriginalRequestBinding(t *testing.T) {
	preview := map[string]any{"approval_request_id": "original-call"}
	if productionRuntimeHILRepeatedEvent(preview, map[string]string{"approval_request_id": "original-call"}) {
		t.Fatal("original event was treated as a retry")
	}
	if !productionRuntimeHILRepeatedEvent(preview, map[string]string{"approval_request_id": "retry-call"}) {
		t.Fatal("new request was not isolated from the original continuation")
	}
}
