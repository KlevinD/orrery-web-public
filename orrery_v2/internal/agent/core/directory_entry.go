package core

import "time"

type RuntimeSpecSummary struct {
	Kind      RuntimeKind
	ConfigRef string
}

type BaseAgentSummary struct {
	InheritsBaseAgent bool
	AuditRequired     bool
	HasSystemPrompt   bool
	HasLLMAPIKeyRef   bool
	LLMAPIKeyRef      string
	SkillScope        string
	ToolScope         string
	MCPScope          string
}

type AgentDirectoryEntry struct {
	AgentProfileID            AgentProfileID
	PersonalAssistantOwnerID  string
	PersonalAssistantTenantID string
	AgentInstanceID           AgentInstanceID
	RealName                  string
	DisplayName               string
	RoleName                  string
	Email                     string
	AgentKind                 AgentKind
	OrgIdentity               AgentOrgIdentity
	Placement                 TalentPlacement
	PersonaSummary            string
	BehaviorBoundary          string
	SkillRefs                 []SkillRef
	ToolRefs                  []ToolRef
	RuntimeSpecSummary        RuntimeSpecSummary
	BaseAgentSummary          BaseAgentSummary
	KnowledgeBaseID           string
	MemoryProfileRef          MemoryProfileRef
	Availability              AgentAvailability
	CostProfile               AgentCostProfile
	QualitySignal             AgentQualitySignal
	RiskProfile               AgentRiskProfile
	QAEnabled                 bool
	Enabled                   bool
	UpdatedAt                 time.Time
}
