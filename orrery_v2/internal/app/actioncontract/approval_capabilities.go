package actioncontract

import "strings"

const (
	ActionBusinessApprovalRequest        ActionTypeID = "business.approval.request"
	ActionMarketingWechatDraftApproval   ActionTypeID = "marketing_wechat_draft_approval"
	ActionCommunicationEmailSend         ActionTypeID = "communication.email.send"
	ActionCommunicationWechatDraftCreate ActionTypeID = "communication.wechat_draft.create"
	ActionSystemCommandExecute           ActionTypeID = "system.command.execute"
	ActionSystemFileChange               ActionTypeID = "system.file.change"
	ActionNetworkExternalAccess          ActionTypeID = "network.external.access"
	ActionInformationRead                ActionTypeID = "information.read"
	ActionInteractionUserInputRequest    ActionTypeID = "interaction.user_input.request"
	ActionToolExternalExecute            ActionTypeID = "tool.external.execute"
	ActionToolUnclassifiedExecute        ActionTypeID = "tool.unclassified.execute"
)

// ApprovalCapability is the trusted adapter registry between runtime/vendor
// tool identifiers and stable business actions. Provider metadata may raise
// risk later, but it cannot replace this server-owned mapping.
type ApprovalCapability struct {
	ActionTypeID  ActionTypeID
	ApprovalKinds []string
	ToolNames     []string
	Effects       []string
	RiskLevel     RiskLevel
	ResourceKind  string
}

func DefaultApprovalCapabilities() []ApprovalCapability {
	return []ApprovalCapability{
		{
			ActionTypeID: ActionBusinessApprovalRequest, ApprovalKinds: []string{"tool"},
			ToolNames: []string{"request_business_approval"}, Effects: []string{"approval_requested"},
			RiskLevel: RiskHigh, ResourceKind: "approval_case",
		},
		{
			ActionTypeID: ActionCommunicationEmailSend, ApprovalKinds: []string{"tool"},
			ToolNames: []string{"email_send", "mcp_system_email_send"}, Effects: []string{"external_write", "message_send"},
			RiskLevel: RiskHigh, ResourceKind: "message",
		},
		{
			ActionTypeID: ActionCommunicationWechatDraftCreate, ApprovalKinds: []string{"tool"},
			ToolNames: []string{"marketing_wechat_draft_create", "mcp_marketing_marketing_wechat_draft_create"},
			Effects:   []string{"external_write", "draft_create"}, RiskLevel: RiskHigh, ResourceKind: "official_account_draft",
		},
		{
			ActionTypeID: ActionSystemCommandExecute, ApprovalKinds: []string{"tool"},
			ToolNames: []string{"personal_assistant_shell_exec"}, Effects: []string{"execute"},
			RiskLevel: RiskHigh, ResourceKind: "runtime_process",
		},
		{
			ActionTypeID: ActionInformationRead, ApprovalKinds: []string{"tool"},
			ToolNames: []string{
				"discussion_talent_list", "memory_search", "knowledge_search",
				"enterprise_graph.build_context_bundle", "enterprise_graph.search_objects",
				"web_search", "web_fetch", "mcp_server_browser_navigate_page", "mcp_server_browser_take_screenshot",
			},
			Effects: []string{"read"}, RiskLevel: RiskLow, ResourceKind: "information_resource",
		},
		{
			ActionTypeID: ActionNetworkExternalAccess, ApprovalKinds: []string{"tool"},
			ToolNames: []string{"browser", "web", "fetch", "url", "http", "network"},
			Effects:   []string{"network", "read"}, RiskLevel: RiskMedium, ResourceKind: "network_endpoint",
		},
		{
			ActionTypeID: ActionSystemCommandExecute, ApprovalKinds: []string{"command_execution"},
			Effects: []string{"execute"}, RiskLevel: RiskHigh, ResourceKind: "runtime_process",
		},
		{
			ActionTypeID: ActionSystemFileChange, ApprovalKinds: []string{"file_change"},
			Effects: []string{"write"}, RiskLevel: RiskHigh, ResourceKind: "workspace_file",
		},
		{
			ActionTypeID: ActionInteractionUserInputRequest, ApprovalKinds: []string{"mcp_elicitation", "user_input"},
			Effects: []string{"human_input"}, RiskLevel: RiskLow, ResourceKind: "human_input",
		},
	}
}

func ResolveDefaultApprovalCapability(approvalKind, toolName string) (ApprovalCapability, bool) {
	kind := normalizeApprovalCapabilityToken(approvalKind)
	tool := normalizeApprovalCapabilityToken(toolName)
	capabilities := DefaultApprovalCapabilities()
	for _, capability := range capabilities {
		if tool == "" || len(capability.ToolNames) == 0 || !approvalCapabilityContains(capability.ToolNames, tool) {
			continue
		}
		if len(capability.ApprovalKinds) > 0 && !approvalCapabilityContains(capability.ApprovalKinds, kind) {
			continue
		}
		return cloneApprovalCapability(capability), true
	}
	for _, capability := range capabilities {
		if len(capability.ToolNames) != 0 || !approvalCapabilityContains(capability.ApprovalKinds, kind) {
			continue
		}
		return cloneApprovalCapability(capability), true
	}
	return ApprovalCapability{}, false
}

func IsHumanBusinessDecisionAction(id ActionTypeID) bool {
	switch id {
	case ActionBusinessApprovalRequest,
		ActionMarketingWechatDraftApproval,
		ActionCommunicationEmailSend,
		ActionCommunicationWechatDraftCreate:
		return true
	default:
		return false
	}
}

func RuntimeToolRequiresHumanBusinessDecision(approvalKind, toolName string) bool {
	capability, ok := ResolveDefaultApprovalCapability(approvalKind, toolName)
	if !ok || capability.ActionTypeID == ActionBusinessApprovalRequest {
		return false
	}
	return IsHumanBusinessDecisionAction(capability.ActionTypeID)
}

func defaultApprovalActionTypes() []ActionType {
	return []ActionType{
		approvalActionType(ActionBusinessApprovalRequest, "Request business approval", ProviderApproval, "request_business_approval", RiskHigh, []string{"approval_requested"}),
		approvalActionType(ActionMarketingWechatDraftApproval, "Approve Marketing WeChat draft", ProviderInternalService, "marketing", RiskHigh, []string{"approval_requested", "draft_create"}),
		approvalActionType(ActionCommunicationEmailSend, "Send external email", ProviderMCPTool, "email_send", RiskHigh, []string{"external_write", "message_send"}),
		approvalActionType(ActionCommunicationWechatDraftCreate, "Create WeChat official account draft", ProviderMCPTool, "mcp_marketing_marketing_wechat_draft_create", RiskHigh, []string{"external_write", "draft_create"}),
		approvalActionType(ActionSystemCommandExecute, "Execute runtime command", ProviderWorker, "exec_command", RiskHigh, []string{"execute"}),
		approvalActionType(ActionSystemFileChange, "Change workspace file", ProviderWorker, "patch_apply", RiskHigh, []string{"write"}),
		approvalActionType(ActionNetworkExternalAccess, "Access external network", ProviderMCPTool, "network", RiskMedium, []string{"network", "read"}),
		approvalActionType(ActionInformationRead, "Read information", ProviderMCPTool, "read", RiskLow, []string{"read"}),
		approvalActionType(ActionInteractionUserInputRequest, "Request human input", ProviderApproval, "mcp_elicitation", RiskLow, []string{"human_input"}),
		approvalActionType(ActionToolExternalExecute, "Execute external tool action", ProviderMCPTool, "tool_registry", RiskHigh, []string{"external_write"}),
		approvalActionType(ActionToolUnclassifiedExecute, "Review unclassified tool action", ProviderMCPTool, "unclassified", RiskCritical, []string{"unknown"}),
	}
}

func approvalActionType(id ActionTypeID, name string, provider ProviderKind, providerRef string, risk RiskLevel, effects []string) ActionType {
	objectTypes := []string{"tool", "agent_run"}
	resolver := "tool"
	if id == ActionBusinessApprovalRequest || id == ActionMarketingWechatDraftApproval {
		objectTypes = []string{"approval_case", "agent_run"}
		resolver = "approval_case"
	}
	hilPolicy := map[string]any{}
	if risk == RiskHigh || risk == RiskCritical {
		hilPolicy["required_for_risk"] = []string{"high", "critical"}
	}
	return ActionType{
		ID: id, TenantID: "system", Name: name, Category: CategoryHybrid,
		Description:    "Canonical server-governed action used for runtime approval policy and audit.",
		ObjectTypeRefs: objectTypes, ProviderKind: provider, ProviderRef: providerRef,
		ResourceResolver: resolver, PermissionScope: "runtime.action.execute", FieldProfile: "runtime.approval",
		DataPolicy: "tenant_scoped", PurposePolicy: "runtime_execution", RiskLevel: risk, HILPolicy: hilPolicy,
		Enabled: true, Version: "v1", Semantics: ActionSemanticProfile{Effects: append([]string(nil), effects...)},
		IdempotencyPolicy: map[string]any{"args_hash_required": true, "object_refs_bound": true},
		AuditPolicy:       map[string]any{"record_actor": true, "record_runtime": true, "record_object_ref": true, "record_args_hash": true},
	}
}

func approvalCapabilityContains(values []string, value string) bool {
	for _, candidate := range values {
		if normalizeApprovalCapabilityToken(candidate) == value {
			return true
		}
	}
	return false
}

func normalizeApprovalCapabilityToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func cloneApprovalCapability(capability ApprovalCapability) ApprovalCapability {
	capability.ApprovalKinds = append([]string(nil), capability.ApprovalKinds...)
	capability.ToolNames = append([]string(nil), capability.ToolNames...)
	capability.Effects = append([]string(nil), capability.Effects...)
	return capability
}
