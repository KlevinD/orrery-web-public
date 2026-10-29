package hil

import "strings"

type RouteInput struct {
	SnapshotValid    bool
	ExplicitDeny     bool
	ContextComplete  bool
	ContextConflict  bool
	RiskLevel        RiskLevel
	CouncilRequired  bool
	HILRequired      bool
	ApprovalRequired bool
	PreferredMode    DecisionMode
}

type RouteDecision struct {
	Mode          DecisionMode   `json:"mode"`
	CouncilEffect DecisionEffect `json:"council_effect,omitempty"`
	Reasons       []string       `json:"reasons"`
}

func DecideRoute(input RouteInput) RouteDecision {
	if !input.SnapshotValid {
		return route(ModeDeny, EffectDeny, "immutable action snapshot is invalid")
	}
	if input.ExplicitDeny {
		return route(ModeDeny, EffectDeny, "explicit deny policy matched")
	}
	if input.CouncilRequired || input.PreferredMode == ModeCouncil {
		if risk := normalizeRisk(input.RiskLevel); risk == RiskHigh || risk == RiskCritical {
			return route(ModeCouncil, EffectRequiresApproval, "high-risk council requires fixed approval")
		}
		return route(ModeCouncil, EffectAuthorize, "explicit council review is required")
	}
	if input.ApprovalRequired || input.PreferredMode == ModeApproval {
		return route(ModeApproval, EffectNone, "fixed approval policy matched")
	}
	if input.HILRequired {
		return route(ModeHIL, EffectNone, "runtime human review is required")
	}
	if input.PreferredMode == ModeHIL {
		return route(ModeHIL, EffectNone, "caller requested stricter human review")
	}
	if input.ContextConflict {
		return route(ModeCouncil, EffectAuthorize, "multi-party context requires council")
	}
	if !input.ContextComplete {
		return route(ModeHIL, EffectNone, "ontology context is partial")
	}
	return route(ModeAuto, EffectAuthorize, "policy permits automatic execution")
}

func route(mode DecisionMode, effect DecisionEffect, reason string) RouteDecision {
	return RouteDecision{Mode: mode, CouncilEffect: effect, Reasons: []string{reason}}
}

func normalizeRisk(risk RiskLevel) RiskLevel {
	switch RiskLevel(strings.ToLower(strings.TrimSpace(string(risk)))) {
	case RiskLow:
		return RiskLow
	case RiskMedium:
		return RiskMedium
	case RiskHigh:
		return RiskHigh
	case RiskCritical:
		return RiskCritical
	default:
		return RiskHigh
	}
}
