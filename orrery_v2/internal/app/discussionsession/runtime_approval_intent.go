package discussionsession

import (
	"strconv"
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/runtimeapproval"
)

func runtimeHILActionIntent(req runtimecore.ApprovalRequest, metadata map[string]string) runtimeapproval.ActionIntent {
	intent := runtimeapproval.ClassifyActionIntent(req)
	if strings.TrimSpace(metadata[runtimeapproval.MetadataIntentVersion]) != "1" {
		return intent
	}
	if value := strings.TrimSpace(metadata[runtimeapproval.MetadataActionTypeID]); value != "" {
		intent.ActionTypeID = actioncontract.ActionTypeID(value)
	}
	if value := strings.TrimSpace(metadata[runtimeapproval.MetadataIntentEffects]); value != "" {
		intent.Effects = commaSeparatedRuntimeApprovalValue(value)
	}
	if value := strings.TrimSpace(metadata[runtimeapproval.MetadataIntentRisk]); value != "" {
		intent.RiskLevel = actioncontract.RiskLevel(value)
	}
	if value := strings.TrimSpace(metadata[runtimeapproval.MetadataIntentSource]); value != "" {
		intent.Source = value
	}
	if value := strings.TrimSpace(metadata[runtimeapproval.MetadataResourceKind]); value != "" {
		intent.ResourceKind = value
	}
	if value := strings.TrimSpace(metadata[runtimeapproval.MetadataIntentRecognized]); value != "" {
		if recognized, err := strconv.ParseBool(value); err == nil {
			intent.Recognized = recognized
		}
	}
	return runtimeapproval.EnsureActionIntent(req, intent)
}

func runtimeHILRiskLevel(intent runtimeapproval.ActionIntent) hil.RiskLevel {
	switch strings.ToLower(strings.TrimSpace(string(intent.RiskLevel))) {
	case string(hil.RiskLow):
		return hil.RiskLow
	case string(hil.RiskMedium):
		return hil.RiskMedium
	case string(hil.RiskHigh):
		return hil.RiskHigh
	case string(hil.RiskCritical):
		return hil.RiskCritical
	default:
		return hil.RiskCritical
	}
}

func runtimeHILDecisionRoute(intent runtimeapproval.ActionIntent) (hil.DecisionMode, bool, bool) {
	if actioncontract.IsHumanBusinessDecisionAction(intent.ActionTypeID) {
		return hil.ModeApproval, false, true
	}
	return hil.ModeHIL, true, false
}

func commaSeparatedRuntimeApprovalValue(value string) []string {
	result := make([]string, 0)
	seen := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return result
}
