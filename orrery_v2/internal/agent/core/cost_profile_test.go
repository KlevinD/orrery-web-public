package core

import "testing"

func TestCostProfileCarriesExplicitBudgetSource(t *testing.T) {
	cost := AgentCostProfile{AgentProfileID: "profile-001", EstimatedCostPerHour: 15, EstimatedCostPerTask: 30, BudgetClass: "standard", Source: "catalog"}
	if cost.Source == "" || cost.EstimatedCostPerHour <= 0 || cost.EstimatedCostPerTask <= 0 {
		t.Fatalf("cost profile should preserve explicit cost source and values: %#v", cost)
	}
}
