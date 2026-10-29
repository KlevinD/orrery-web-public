package reliability

import (
	"fmt"
	"strings"
	"time"
)

const (
	minimumAutoApplyConfidence = 0.90
	minimumAutoApplySupport    = int64(3)
)

func EvaluateAutoApply(rule RepairRule, incident FailureIncident, now time.Time) AutoApplyDecision {
	var reasons []string
	if strings.TrimSpace(rule.TenantID) == "" || rule.TenantID != incident.TenantID {
		reasons = append(reasons, "rule and incident tenants do not match")
	}
	if incident.Status != IncidentStatusOpen && incident.Status != IncidentStatusContained && incident.Status != IncidentStatusDiagnosed {
		reasons = append(reasons, "incident state is not eligible for automatic repair")
	}
	if rule.Status != RepairRuleStatusActive {
		reasons = append(reasons, "rule is not active")
	}
	if !rule.AutoApply {
		reasons = append(reasons, "automatic application is disabled")
	}
	if strings.TrimSpace(rule.SelectorFingerprint) == "" || rule.SelectorFingerprint != incident.Fingerprint {
		reasons = append(reasons, "rule fingerprint does not exactly match the incident")
	}
	if rule.RiskLevel != RiskLow {
		reasons = append(reasons, "only low-risk repairs can be applied automatically")
	}
	if !rule.Reversible {
		reasons = append(reasons, "repair is not reversible")
	}
	if !rule.Idempotent {
		reasons = append(reasons, "repair is not idempotent")
	}
	if rule.Confidence < minimumAutoApplyConfidence {
		reasons = append(reasons, fmt.Sprintf("confidence %.2f is below %.2f", rule.Confidence, minimumAutoApplyConfidence))
	}
	if rule.SupportCount < minimumAutoApplySupport || rule.SuccessCount < minimumAutoApplySupport {
		reasons = append(reasons, "rule has insufficient verified support")
	}
	if rule.FailureCount != 0 {
		reasons = append(reasons, "rule has a recorded failure")
	}
	if rule.ExpiresAt != nil && !rule.ExpiresAt.After(now.UTC()) {
		reasons = append(reasons, "rule has expired")
	}
	if !autoApplyStrategyAllowed(rule.Strategy) {
		reasons = append(reasons, "repair strategy always requires governed execution")
	}
	return AutoApplyDecision{Allowed: len(reasons) == 0, Reasons: reasons}
}

func autoApplyStrategyAllowed(strategy RepairStrategy) bool {
	switch strategy {
	case RepairStrategyRetry, RepairStrategyRestart, RepairStrategyFallback, RepairStrategyReconfigure:
		return true
	default:
		return false
	}
}
