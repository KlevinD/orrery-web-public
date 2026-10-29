package hil

import "testing"

func TestDecideRouteRejectsInvalidSnapshot(t *testing.T) {
	decision := DecideRoute(RouteInput{SnapshotValid: false, RiskLevel: RiskMedium})
	if decision.Mode != ModeDeny {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeDeny)
	}
}

func TestDecideRouteDenyTakesPrecedence(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid:   true,
		ExplicitDeny:    true,
		RiskLevel:       RiskHigh,
		CouncilRequired: true,
		HILRequired:     true,
	})
	if decision.Mode != ModeDeny {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeDeny)
	}
}

func TestDecideRouteHighRiskCouncilRequiresApproval(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid:   true,
		ContextComplete: true,
		RiskLevel:       RiskHigh,
		CouncilRequired: true,
	})
	if decision.Mode != ModeCouncil || decision.CouncilEffect != EffectRequiresApproval {
		t.Fatalf("decision = %+v, want council requiring approval", decision)
	}
}

func TestDecideRouteHighRiskAloneRemainsAutomatic(t *testing.T) {
	decision := DecideRoute(RouteInput{SnapshotValid: true, ContextComplete: true, RiskLevel: RiskHigh})
	if decision.Mode != ModeAuto {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeAuto)
	}
}

func TestDecideRouteCriticalRuntimeInteractionDoesNotBecomeApproval(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid: true, ContextComplete: true, RiskLevel: RiskCritical, HILRequired: true,
	})
	if decision.Mode != ModeHIL {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeHIL)
	}
}

func TestDecideRouteMediumRiskCouncilCanAuthorize(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid:   true,
		ContextComplete: true,
		RiskLevel:       RiskMedium,
		ContextConflict: true,
	})
	if decision.Mode != ModeCouncil || decision.CouncilEffect != EffectAuthorize {
		t.Fatalf("decision = %+v, want authorizing council", decision)
	}
}

func TestDecideRoutePartialContextCannotAutoApprove(t *testing.T) {
	decision := DecideRoute(RouteInput{SnapshotValid: true, ContextComplete: false, RiskLevel: RiskLow})
	if decision.Mode != ModeHIL {
		t.Fatalf("mode = %q, want conservative %q", decision.Mode, ModeHIL)
	}
}

func TestDecideRouteSelectsRuntimeHIL(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid:   true,
		ContextComplete: true,
		RiskLevel:       RiskLow,
		HILRequired:     true,
	})
	if decision.Mode != ModeHIL {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeHIL)
	}
}

func TestDecideRouteSelectsFixedApproval(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid:    true,
		ContextComplete:  true,
		RiskLevel:        RiskMedium,
		ApprovalRequired: true,
	})
	if decision.Mode != ModeApproval {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeApproval)
	}
}

func TestDecideRouteFixedApprovalOwnsConflictedContextUnlessCouncilIsExplicit(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid: true, ContextComplete: true, ContextConflict: true,
		RiskLevel: RiskHigh, ApprovalRequired: true, PreferredMode: ModeApproval,
	})
	if decision.Mode != ModeApproval {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeApproval)
	}
}

func TestDecideRouteDefaultsToAuto(t *testing.T) {
	decision := DecideRoute(RouteInput{SnapshotValid: true, ContextComplete: true, RiskLevel: RiskLow})
	if decision.Mode != ModeAuto {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeAuto)
	}
}

func TestDecideRoutePreferredModeCannotLowerScrutiny(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid:    true,
		ContextComplete:  true,
		RiskLevel:        RiskMedium,
		ApprovalRequired: true,
		PreferredMode:    ModeAuto,
	})
	if decision.Mode != ModeApproval {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeApproval)
	}
}

func TestDecideRoutePreferredModeCanRaiseScrutiny(t *testing.T) {
	decision := DecideRoute(RouteInput{
		SnapshotValid:   true,
		ContextComplete: true,
		RiskLevel:       RiskLow,
		PreferredMode:   ModeCouncil,
	})
	if decision.Mode != ModeCouncil {
		t.Fatalf("mode = %q, want %q", decision.Mode, ModeCouncil)
	}
}
