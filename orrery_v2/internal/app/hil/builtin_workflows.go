package hil

const (
	BuiltInWorkflowTenantID                   = "system"
	BuiltInMarketingWorkflowTemplateID        = "builtin-marketing-wechat-draft-approval"
	BuiltInBusinessWorkflowTemplateID         = "builtin-business-approval"
	BuiltInExternalEmailWorkflowTemplateID    = "builtin-external-email-approval"
	BuiltInCommandExecutionWorkflowTemplateID = "builtin-command-execution-approval"
	BuiltInHighRiskWorkflowTemplateID         = "builtin-high-risk-two-stage-approval"
)

func IsBuiltInWorkflowTemplateID(templateID string) bool {
	switch templateID {
	case BuiltInMarketingWorkflowTemplateID,
		BuiltInBusinessWorkflowTemplateID,
		BuiltInExternalEmailWorkflowTemplateID,
		BuiltInCommandExecutionWorkflowTemplateID,
		BuiltInHighRiskWorkflowTemplateID:
		return true
	default:
		return false
	}
}
