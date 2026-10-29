package responsibility

import (
	"fmt"
	"strings"
	"time"
)

const policyTable = "responsibility_policies"

type ApprovalPolicy string

const (
	ApprovalBeforeSideEffect ApprovalPolicy = "before_side_effect"
	ApprovalOnlyHighRisk     ApprovalPolicy = "only_high_risk"
	ApprovalNone             ApprovalPolicy = "none"
)

type EscalationTarget struct {
	SubjectKind string        `json:"subject_kind"`
	SubjectID   string        `json:"subject_id"`
	After       time.Duration `json:"after"`
}

type ResponsibilityPolicyRecord struct {
	ID                    string             `json:"id"`
	TenantID              string             `json:"tenant_id"`
	AgentProfileID        string             `json:"agent_profile_id"`
	WorkKind              WorkKind           `json:"work_kind"`
	AutonomyLevel         AutonomyLevel      `json:"autonomy_level"`
	MaxParallel           int                `json:"max_parallel"`
	AcceptTimeout         time.Duration      `json:"accept_timeout"`
	HeartbeatTimeout      time.Duration      `json:"heartbeat_timeout"`
	MaxAttempts           int                `json:"max_attempts"`
	RetryBackoff          []time.Duration    `json:"retry_backoff"`
	MaxCostPerTask        float64            `json:"max_cost_per_task"`
	MaxTokenBudget        int64              `json:"max_token_budget"`
	ApprovalPolicy        ApprovalPolicy     `json:"approval_policy"`
	AllowedRiskLevels     []string           `json:"allowed_risk_levels"`
	ToolScopes            []string           `json:"tool_scopes"`
	DataScopes            []string           `json:"data_scopes"`
	EscalationChain       []EscalationTarget `json:"escalation_chain"`
	RecheckInterval       time.Duration      `json:"recheck_interval"`
	DeadlineWarningWindow time.Duration      `json:"deadline_warning_window"`
	Enabled               bool               `json:"enabled"`
	Version               int64              `json:"version"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`
}

func PolicyID(tenantID, agentID string, kind WorkKind) string {
	return "responsibility-policy-" + stableHex(strings.TrimSpace(tenantID), strings.TrimSpace(agentID), string(kind))[:24]
}

func DefaultPolicy(tenantID, agentID string, kind WorkKind) ResponsibilityPolicyRecord {
	return ResponsibilityPolicyRecord{
		ID: PolicyID(tenantID, agentID, kind), TenantID: strings.TrimSpace(tenantID), AgentProfileID: strings.TrimSpace(agentID),
		WorkKind: kind, AutonomyLevel: AutonomyExecuteScoped, MaxParallel: 2, AcceptTimeout: 2 * time.Minute,
		HeartbeatTimeout: 90 * time.Second, MaxAttempts: 3,
		RetryBackoff:   []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute},
		ApprovalPolicy: ApprovalOnlyHighRisk, RecheckInterval: 15 * time.Minute,
		DeadlineWarningWindow: 30 * time.Minute, Enabled: true, Version: 1,
	}
}

func (policy ResponsibilityPolicyRecord) Validate() error {
	if blank(policy.ID) || blank(policy.TenantID) || blank(policy.AgentProfileID) || !policy.WorkKind.valid() {
		return fmt.Errorf("%w: policy identity, tenant, Agent, and work kind are required", ErrInvalidContract)
	}
	if policy.ID != PolicyID(policy.TenantID, policy.AgentProfileID, policy.WorkKind) {
		return fmt.Errorf("%w: policy id does not match its tenant, Agent, and work kind", ErrInvalidContract)
	}
	if !policy.AutonomyLevel.valid() || !policy.ApprovalPolicy.valid() {
		return fmt.Errorf("%w: unsupported autonomy or approval policy", ErrInvalidContract)
	}
	if policy.MaxParallel < 1 || policy.MaxParallel > 100 || policy.AcceptTimeout <= 0 || policy.HeartbeatTimeout <= 0 || policy.MaxAttempts < 1 || policy.MaxAttempts > 100 {
		return fmt.Errorf("%w: policy concurrency and timeout limits must be positive and bounded", ErrInvalidContract)
	}
	if policy.MaxCostPerTask < 0 || policy.MaxTokenBudget < 0 || policy.RecheckInterval <= 0 || policy.DeadlineWarningWindow < 0 {
		return fmt.Errorf("%w: policy budgets and supervision windows are invalid", ErrInvalidContract)
	}
	if len(policy.RetryBackoff) == 0 {
		return fmt.Errorf("%w: policy retry backoff is required", ErrInvalidContract)
	}
	for _, delay := range policy.RetryBackoff {
		if delay < 0 || delay > 30*24*time.Hour {
			return fmt.Errorf("%w: policy retry backoff is outside the supported range", ErrInvalidContract)
		}
	}
	for _, target := range policy.EscalationChain {
		if blank(target.SubjectKind) || blank(target.SubjectID) || target.After <= 0 {
			return fmt.Errorf("%w: escalation targets require subject kind, subject id, and delay", ErrInvalidContract)
		}
	}
	if policy.Version < 1 {
		return fmt.Errorf("%w: policy version must be positive", ErrInvalidContract)
	}
	return nil
}

func (level AutonomyLevel) valid() bool {
	switch level {
	case AutonomyAdvise, AutonomyExecuteReversible, AutonomyExecuteScoped, AutonomyContinuous:
		return true
	default:
		return false
	}
}

func (policy ApprovalPolicy) valid() bool {
	switch policy {
	case ApprovalBeforeSideEffect, ApprovalOnlyHighRisk, ApprovalNone:
		return true
	default:
		return false
	}
}

func cleanPolicyStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
