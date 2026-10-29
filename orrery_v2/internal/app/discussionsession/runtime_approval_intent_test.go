package discussionsession

import (
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/actioncontract"
)

func TestRuntimeHILActionIntentFailsClosedForLegacyUnknownTool(t *testing.T) {
	intent := runtimeHILActionIntent(runtimecore.ApprovalRequest{
		Kind: runtimecore.ApprovalKindTool, ToolName: "vendor_unregistered_mutation",
	}, nil)
	if intent.ActionTypeID != actioncontract.ActionToolUnclassifiedExecute || intent.RiskLevel != actioncontract.RiskCritical || intent.Recognized {
		t.Fatalf("legacy unknown intent = %+v", intent)
	}
}
