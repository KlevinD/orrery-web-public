package runtimeapproval

import (
	"context"
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/actioncontract"
)

func TestClassifyActionIntentUsesCanonicalCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		request    runtimecore.ApprovalRequest
		action     actioncontract.ActionTypeID
		risk       actioncontract.RiskLevel
		recognized bool
	}{
		{
			name: "vendor WeChat adapter", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "mcp_marketing_marketing_wechat_draft_create"},
			action: actioncontract.ActionCommunicationWechatDraftCreate, risk: actioncontract.RiskHigh, recognized: true,
		},
		{
			name: "runtime command", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command"},
			action: actioncontract.ActionSystemCommandExecute, risk: actioncontract.RiskHigh, recognized: true,
		},
		{
			name: "normalized network tool", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "browser"},
			action: actioncontract.ActionNetworkExternalAccess, risk: actioncontract.RiskMedium, recognized: true,
		},
		{
			name: "unknown tool", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "vendor_unregistered_mutation"},
			action: actioncontract.ActionToolUnclassifiedExecute, risk: actioncontract.RiskCritical, recognized: false,
		},
		{
			name: "deceptive browser name remains unknown", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "vendor_browser_delete"},
			action: actioncontract.ActionToolUnclassifiedExecute, risk: actioncontract.RiskCritical, recognized: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			intent := ClassifyActionIntent(test.request)
			if intent.ActionTypeID != test.action || intent.RiskLevel != test.risk || intent.Recognized != test.recognized {
				t.Fatalf("intent = %+v", intent)
			}
		})
	}
}

func TestDecisionServiceClassifiesTrustedUnknownWriteAsExternalTool(t *testing.T) {
	policy := DefaultPolicy()
	policy.Tools.Allowlist = []ToolRule{{
		RuleID: "crm-customer-update", ToolID: "vendor_crm_update",
		Effects: []string{"write"}, Risk: "high",
	}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)
	decision := service.Decide(context.Background(), DecisionInput{Request: runtimecore.ApprovalRequest{
		Kind: runtimecore.ApprovalKindTool, ToolName: "vendor_crm_update", Payload: []byte(`{"customer_id":"customer-1"}`),
	}})
	if decision.Outcome != DecisionOutcomeApproveOnce || decision.Intent.ActionTypeID != actioncontract.ActionToolExternalExecute ||
		decision.Intent.RiskLevel != actioncontract.RiskHigh || !decision.Intent.Recognized || decision.Intent.Source != "runtime_policy:crm-customer-update" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecisionServiceDoesNotLetProviderMetadataLowerCanonicalRisk(t *testing.T) {
	policy := DefaultPolicy()
	policy.Tools.Allowlist = []ToolRule{{
		RuleID: "email", ToolID: "email_send", Effects: []string{"read"}, Risk: "low", AutoApprove: true,
	}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)
	decision := service.Decide(context.Background(), DecisionInput{Request: runtimecore.ApprovalRequest{
		Kind: runtimecore.ApprovalKindTool, ToolName: "email_send", Metadata: map[string]string{"approval_risk": "low", "approval_effects": "read"},
	}})
	if decision.Outcome != DecisionOutcomeHuman || decision.Intent.ActionTypeID != actioncontract.ActionCommunicationEmailSend ||
		decision.Intent.RiskLevel != actioncontract.RiskHigh {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecisionServiceDerivesCommandRiskFromAnalyzedOperation(t *testing.T) {
	policy := DefaultPolicy()
	policy.Exec.Allowlist = []ExecRule{{RuleID: "pwd", Executable: "pwd", WorkspaceOnly: true}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	readOnly := service.Decide(context.Background(), DecisionInput{Request: runtimecore.ApprovalRequest{
		Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
		Metadata: map[string]string{"command": "pwd"}, WorkspaceRoot: "/workspace",
	}})
	if readOnly.Outcome != DecisionOutcomeApproveOnce || readOnly.Intent.ActionTypeID != actioncontract.ActionSystemCommandExecute ||
		readOnly.Intent.RiskLevel != actioncontract.RiskLow || readOnly.Intent.Source != "runtime_policy:pwd" {
		t.Fatalf("read-only decision = %+v", readOnly)
	}

	destructive := service.Decide(context.Background(), DecisionInput{Request: runtimecore.ApprovalRequest{
		Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
		Metadata: map[string]string{"command": "rm -rf ./workspace"}, WorkspaceRoot: "/workspace",
	}})
	if destructive.Outcome != DecisionOutcomeApproveOnce || destructive.DecisionSource != "command_execution_no_approval" ||
		destructive.Intent.ActionTypeID != actioncontract.ActionSystemCommandExecute || destructive.Intent.RiskLevel != actioncontract.RiskCritical ||
		destructive.Intent.Source != "exec_analyzer:exec_destructive" {
		t.Fatalf("destructive decision = %+v", destructive)
	}
}
