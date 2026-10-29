package main

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

type productionRuntimeHILMissionResolver struct {
	missions *missioncontrol.CommandService
	report   func(error)
}

func (r productionRuntimeHILMissionResolver) ResumeDetachedHILRuntimeApproval(ctx context.Context, resolution hil.DecisionResolution, conversation discussionsession.ConversationRecordView, event discussionsession.EventRecord) (resultErr error) {
	if r.missions == nil {
		return fmt.Errorf("mission command service is required")
	}
	if resolution.Decision.Status != hil.DecisionResolved || resolution.Receipt.Effect != hil.EffectAuthorize || !resolution.Receipt.SingleUse {
		return nil
	}
	decisionID := strings.TrimSpace(resolution.Decision.ID)
	receiptID := strings.TrimSpace(resolution.Receipt.ID)
	if decisionID == "" || receiptID == "" || strings.TrimSpace(resolution.Decision.ArgsHash) == "" ||
		strings.TrimSpace(resolution.Receipt.ArgsHash) != strings.TrimSpace(resolution.Decision.ArgsHash) {
		return fmt.Errorf("detached runtime HIL receipt binding is invalid")
	}
	metadata := cloneProductionRuntimeHILMetadata(event.Metadata)
	if strings.TrimSpace(metadata[runtimecore.ApprovalMetadataToolName]) != marketingWechatDraftApprovalTool {
		return nil
	}
	defer func() {
		if r.report != nil {
			r.report(resultErr)
		}
	}()
	if strings.TrimSpace(metadata["approval_resume_required"]) != "true" ||
		strings.TrimSpace(metadata["hil_decision_id"]) != decisionID ||
		strings.TrimSpace(metadata["hil_receipt_id"]) != receiptID ||
		strings.TrimSpace(metadata["hil_receipt_effect"]) != string(hil.EffectAuthorize) ||
		strings.TrimSpace(metadata["approval_action_id"]) != "approve_once" {
		return fmt.Errorf("detached runtime HIL event binding is invalid")
	}
	if err := validateProductionRuntimeHILMaterial(resolution.Decision.PayloadPreview, metadata); err != nil {
		return err
	}
	if strings.TrimSpace(metadata["approval_business_key"]) == "" ||
		strings.TrimSpace(metadata["approval_business_key"]) != productionRuntimeHILString(resolution.Decision.PayloadPreview["approval_business_key"]) {
		return fmt.Errorf("detached runtime HIL business binding is invalid")
	}
	if strings.TrimSpace(conversation.TenantID) != strings.TrimSpace(resolution.Decision.TenantID) {
		return fmt.Errorf("detached runtime HIL tenant binding is invalid")
	}
	missionID, err := detachedRuntimeMissionID(conversation.LinkedMissions)
	if err != nil {
		return err
	}
	idempotencyKey := "hil-runtime-mission-resume:" + decisionID + ":" + receiptID
	if productionRuntimeHILRepeatedEvent(resolution.Decision.PayloadPreview, metadata) {
		idempotencyKey += ":" + event.EventID
	}
	_, err = r.missions.ResumeRunAfterDurableHIL(ctx, missioncontrol.ResumeDurableHILRunCommand{
		IdempotencyKey: idempotencyKey,
		TenantID:       strings.TrimSpace(resolution.Decision.TenantID),
		MissionID:      missionID,
		RunID:          agentcore.AgentRunID(strings.TrimSpace(metadata["approval_run_id"])),
		ApprovalRef:    strings.TrimSpace(metadata[runtimecore.ApprovalMetadataRequestID]),
		ToolName:       strings.TrimSpace(metadata[runtimecore.ApprovalMetadataToolName]),
		HILDecisionID:  decisionID,
		HILReceiptID:   receiptID,
		HILArgsHash:    strings.TrimSpace(resolution.Decision.ArgsHash),
		ResolverID:     strings.TrimSpace(resolution.Actor.ID),
		DecidedAt:      resolution.Receipt.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Metadata:       metadata,
	})
	return err
}

func productionRuntimeHILRepeatedEvent(preview map[string]any, metadata map[string]string) bool {
	originalRequestID := productionRuntimeHILString(preview[runtimecore.ApprovalMetadataRequestID])
	currentRequestID := strings.TrimSpace(metadata[runtimecore.ApprovalMetadataRequestID])
	return originalRequestID != "" && currentRequestID != "" && originalRequestID != currentRequestID
}

func detachedRuntimeMissionID(links []discussionsession.LinkedMissionRecord) (missioncore.MissionID, error) {
	var missionID missioncore.MissionID
	for _, link := range links {
		candidate := missioncore.MissionID(strings.TrimSpace(link.MissionID))
		if candidate.IsZero() {
			continue
		}
		if !missionID.IsZero() && missionID != candidate {
			return "", fmt.Errorf("detached runtime approval links multiple missions")
		}
		missionID = candidate
	}
	if missionID.IsZero() {
		return "", fmt.Errorf("detached runtime approval has no linked mission")
	}
	return missionID, nil
}

func validateProductionRuntimeHILMaterial(preview map[string]any, metadata map[string]string) error {
	materialKind, _ := preview["material_kind"].(string)
	materialHash, _ := preview["material_hash"].(string)
	fields, _ := preview["fields"].(map[string]any)
	if fields == nil {
		return fmt.Errorf("detached runtime HIL material fields are missing")
	}
	bindings := map[string]string{
		"approval_material_kind":        strings.TrimSpace(materialKind),
		"approval_material_hash":        strings.TrimSpace(materialHash),
		"approval_material_account_id":  productionRuntimeHILString(fields["account_id"]),
		"approval_material_intent_id":   productionRuntimeHILString(fields["intent_id"]),
		"approval_material_intent_hash": productionRuntimeHILString(fields["intent_hash"]),
	}
	for key, value := range bindings {
		if value == "" || strings.TrimSpace(metadata[key]) != value {
			return fmt.Errorf("detached runtime HIL material binding %s is invalid", key)
		}
	}
	return nil
}

func productionRuntimeHILString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func cloneProductionRuntimeHILMetadata(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
