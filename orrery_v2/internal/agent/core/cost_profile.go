package core

type AgentCostProfile struct {
	AgentProfileID       AgentProfileID
	CostUnit             string
	EstimatedCostPerHour float64
	EstimatedCostPerTask float64
	BudgetClass          string
	Source               string
}
