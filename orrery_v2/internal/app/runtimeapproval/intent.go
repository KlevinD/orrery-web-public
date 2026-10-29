package runtimeapproval

import (
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/actioncontract"
)

const (
	MetadataIntentVersion    = "approval_intent_version"
	MetadataActionTypeID     = "approval_action_type_id"
	MetadataIntentEffects    = "approval_intent_effects"
	MetadataIntentRisk       = "approval_intent_risk"
	MetadataIntentSource     = "approval_intent_source"
	MetadataIntentRecognized = "approval_intent_recognized"
	MetadataResourceKind     = "approval_resource_kind"
)

type ActionIntent struct {
	ActionTypeID actioncontract.ActionTypeID
	Effects      []string
	RiskLevel    actioncontract.RiskLevel
	ResourceKind string
	Recognized   bool
	Source       string
}

// ClassifyActionIntent uses only server-owned request kind/tool identity. Raw
// provider descriptions and prompts are deliberately excluded from routing.
func ClassifyActionIntent(request runtimecore.ApprovalRequest) ActionIntent {
	if capability, ok := actioncontract.ResolveDefaultApprovalCapability(string(request.Kind), request.ToolName); ok {
		return intentFromCapability(capability, "capability_registry")
	}
	return ActionIntent{
		ActionTypeID: actioncontract.ActionToolUnclassifiedExecute, Effects: []string{"unknown"},
		RiskLevel: actioncontract.RiskCritical, ResourceKind: "tool", Recognized: false, Source: "fail_closed",
	}
}

func EnsureActionIntent(request runtimecore.ApprovalRequest, intent ActionIntent) ActionIntent {
	baseline := ClassifyActionIntent(request)
	if intent.ActionTypeID == "" {
		return baseline
	}
	intent.Effects = normalizedEffects(intent.Effects, nil)
	if len(intent.Effects) == 0 {
		intent.Effects = append([]string(nil), baseline.Effects...)
	}
	if intent.RiskLevel == "" {
		intent.RiskLevel = baseline.RiskLevel
	}
	if strings.TrimSpace(intent.ResourceKind) == "" {
		intent.ResourceKind = baseline.ResourceKind
	}
	if strings.TrimSpace(intent.Source) == "" {
		intent.Source = baseline.Source
	}
	return intent
}

func refineToolActionIntent(request runtimecore.ApprovalRequest, analysis ToolAnalysis) ActionIntent {
	intent := ClassifyActionIntent(request)
	if analysis.MatchedRuleID != "" {
		if !intent.Recognized {
			if readOnlyEffects(analysis.Effects) && normalizeIntentRisk(analysis.Risk) == actioncontract.RiskLow {
				intent.ActionTypeID = actioncontract.ActionInformationRead
				intent.ResourceKind = "information_resource"
			} else {
				intent.ActionTypeID = actioncontract.ActionToolExternalExecute
				intent.ResourceKind = "tool"
			}
			intent.Effects = nil
			intent.RiskLevel = ""
			intent.Recognized = true
		}
		intent.Source = "runtime_policy:" + analysis.MatchedRuleID
	}
	intent.Effects = mergeEffects(intent.Effects, analysis.Effects)
	if len(intent.Effects) == 0 {
		intent.Effects = []string{"unknown"}
	}
	intent.RiskLevel = maxIntentRisk(intent.RiskLevel, normalizeIntentRisk(analysis.Risk))
	if intent.RiskLevel == "" {
		intent.RiskLevel = actioncontract.RiskCritical
	}
	return intent
}

func refineExecActionIntent(request runtimecore.ApprovalRequest, analysis ExecAnalysis) ActionIntent {
	intent := ClassifyActionIntent(request)
	if analysis.ForcedHuman {
		intent.Effects = mergeEffects(intent.Effects, []string{"write"})
		intent.RiskLevel = actioncontract.RiskHigh
		switch analysis.Reason {
		case "exec_destructive", "exec_permission_change":
			intent.Effects = mergeEffects(intent.Effects, []string{"delete", "permission"})
			intent.RiskLevel = actioncontract.RiskCritical
		}
		intent.Source = "exec_analyzer:" + analysis.Reason
		return intent
	}
	if analysis.Safe && len(analysis.MatchedRuleIDs) > 0 {
		intent.Effects = []string{"read", "execute"}
		intent.RiskLevel = actioncontract.RiskLow
		intent.Source = "runtime_policy:" + strings.Join(analysis.MatchedRuleIDs, ",")
	}
	return intent
}

func intentFromCapability(capability actioncontract.ApprovalCapability, source string) ActionIntent {
	return ActionIntent{
		ActionTypeID: capability.ActionTypeID, Effects: append([]string(nil), capability.Effects...),
		RiskLevel: capability.RiskLevel, ResourceKind: capability.ResourceKind, Recognized: true, Source: source,
	}
}

func normalizeIntentRisk(value string) actioncontract.RiskLevel {
	switch actioncontract.RiskLevel(strings.ToLower(strings.TrimSpace(value))) {
	case actioncontract.RiskLow:
		return actioncontract.RiskLow
	case actioncontract.RiskMedium:
		return actioncontract.RiskMedium
	case actioncontract.RiskHigh:
		return actioncontract.RiskHigh
	case actioncontract.RiskCritical:
		return actioncontract.RiskCritical
	default:
		return ""
	}
}

func maxIntentRisk(left, right actioncontract.RiskLevel) actioncontract.RiskLevel {
	ranks := map[actioncontract.RiskLevel]int{
		"": 0, actioncontract.RiskLow: 1, actioncontract.RiskMedium: 2,
		actioncontract.RiskHigh: 3, actioncontract.RiskCritical: 4,
	}
	if ranks[right] > ranks[left] {
		return right
	}
	return left
}

func readOnlyEffects(effects []string) bool {
	if len(effects) == 0 {
		return false
	}
	for _, effect := range effects {
		if strings.ToLower(strings.TrimSpace(effect)) != "read" {
			return false
		}
	}
	return true
}
