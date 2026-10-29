package discussionsession

import (
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/runtimeapproval"
)

// Keep policy provenance after material merging. Approval content must not
// replace server classification, routing, assignment or mandatory-human flags.
func preserveRuntimeHILDelegationPolicyMetadata(preview map[string]any, metadata map[string]string) {
	for _, key := range []string{
		runtimecore.ApprovalMetadataKind, runtimecore.ApprovalMetadataToolName,
		runtimeapproval.MetadataIntentVersion, runtimeapproval.MetadataActionTypeID,
		runtimeapproval.MetadataIntentEffects, runtimeapproval.MetadataIntentRisk,
		runtimeapproval.MetadataIntentSource, runtimeapproval.MetadataIntentRecognized,
		runtimeapproval.MetadataResourceKind, "approval_assigned_to", "approval_conversation_id", "approval_run_id",
		"approval_force_manual", "approval_decision_source", "approval_effective_hil_mode",
	} {
		delete(preview, key)
		if value := strings.TrimSpace(metadata[key]); value != "" {
			preview[key] = value
		}
	}
	preview["approval_delegation_policy_version"] = "1"
}
