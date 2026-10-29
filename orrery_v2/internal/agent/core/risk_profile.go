package core

type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
	RiskLevelUnknown  RiskLevel = "unknown"
)

type AgentRiskProfile struct {
	AgentProfileID AgentProfileID
	RiskLevel      RiskLevel
	Reasons        []string
	Mitigations    []string
}
