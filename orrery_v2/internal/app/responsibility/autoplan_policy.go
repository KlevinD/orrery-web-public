package responsibility

import (
	"fmt"
	"strings"
)

// AutoplanPolicy is an in-memory planning gate. It deliberately does not add
// persistence fields to ResponsibilityPolicyRecord yet; callers can derive it
// from the existing responsibility policy and later persist explicit planner
// limits when the feature is wired into production.
type AutoplanPolicy struct {
	Enabled                       bool
	AutonomyLevel                 AutonomyLevel
	ApprovalPolicy                ApprovalPolicy
	MaxSteps                      int
	MaxOpenPlans                  int
	AllowedAssigneeIDs            []string
	AllowedExecutionScopes        []string
	AllowedRiskLevels             []AutoplanRiskLevel
	RequireApprovalForSideEffects bool
	AllowAutomaticMaterialization bool
}

type AutoplanPolicyDecision struct {
	Allowed          bool
	ApprovalRequired bool
	Reason           string
}

// DefaultAutoplanPolicy starts conservatively at one executable step because
// the current scheduler ResponsibilityAction path materializes one task per
// scheduled run. A future multi-step PlanMaterializer can raise MaxSteps.
func DefaultAutoplanPolicy(agentProfileID string) AutoplanPolicy {
	policy := AutoplanPolicy{
		// Autonomous planning changes the creation behavior of an otherwise
		// read-only responsibility pulse, so it is deliberately opt-in.
		Enabled:                       false,
		AutonomyLevel:                 AutonomyExecuteScoped,
		ApprovalPolicy:                ApprovalOnlyHighRisk,
		MaxSteps:                      1,
		MaxOpenPlans:                  1,
		AllowAutomaticMaterialization: true,
	}
	if agentProfileID = strings.TrimSpace(agentProfileID); agentProfileID != "" {
		policy.AllowedAssigneeIDs = []string{agentProfileID}
	}
	return policy
}

// AutoplanPolicyFromResponsibility maps execution limits into a planner gate
// without changing the stored responsibility contract. Only an explicitly
// continuous, enabled responsibility is opted into autonomous planning; the
// normal enabled execution policies retain the existing read-only pulse.
func AutoplanPolicyFromResponsibility(policy ResponsibilityPolicyRecord) AutoplanPolicy {
	out := DefaultAutoplanPolicy(policy.AgentProfileID)
	out.Enabled = policy.Enabled && policy.AutonomyLevel == AutonomyContinuous
	out.AutonomyLevel = policy.AutonomyLevel
	out.ApprovalPolicy = policy.ApprovalPolicy
	out.MaxOpenPlans = policy.MaxParallel
	if out.MaxOpenPlans < 1 {
		out.MaxOpenPlans = 1
	}
	out.AllowedExecutionScopes = cleanAutoplanStrings(policy.ToolScopes)
	out.AllowedRiskLevels = autoplanRiskLevels(policy.AllowedRiskLevels)
	out.AllowAutomaticMaterialization = policy.AutonomyLevel != AutonomyAdvise
	return out
}

func (policy AutoplanPolicy) Normalized() AutoplanPolicy {
	policy.AllowedAssigneeIDs = cleanAutoplanStrings(policy.AllowedAssigneeIDs)
	policy.AllowedExecutionScopes = cleanAutoplanStrings(policy.AllowedExecutionScopes)
	seenRisks := make(map[AutoplanRiskLevel]struct{}, len(policy.AllowedRiskLevels))
	risks := make([]AutoplanRiskLevel, 0, len(policy.AllowedRiskLevels))
	for _, level := range policy.AllowedRiskLevels {
		level = AutoplanRiskLevel(strings.ToLower(strings.TrimSpace(string(level))))
		if !level.valid() {
			continue
		}
		if _, exists := seenRisks[level]; exists {
			continue
		}
		seenRisks[level] = struct{}{}
		risks = append(risks, level)
	}
	policy.AllowedRiskLevels = risks
	return policy
}

func (policy AutoplanPolicy) Validate() error {
	policy = policy.Normalized()
	if !policy.AutonomyLevel.valid() || !policy.ApprovalPolicy.valid() || policy.MaxSteps < 1 || policy.MaxSteps > 100 || policy.MaxOpenPlans < 1 || policy.MaxOpenPlans > 1000 {
		return fmt.Errorf("%w: autonomous planning policy is invalid", ErrInvalidContract)
	}
	return nil
}

// EvaluateAutoplanPolicy validates a proposal against its authorized planning
// envelope. A denied proposal is a normal policy outcome, while malformed
// policy or proposal input returns an error.
func EvaluateAutoplanPolicy(policy AutoplanPolicy, proposal AutoplanProposal) (AutoplanPolicyDecision, error) {
	policy = policy.Normalized()
	proposal = proposal.Normalized()
	if err := policy.Validate(); err != nil {
		return AutoplanPolicyDecision{}, err
	}
	if err := proposal.Validate(); err != nil {
		return AutoplanPolicyDecision{}, err
	}
	if !policy.Enabled {
		return AutoplanPolicyDecision{Reason: "responsibility policy is paused"}, nil
	}
	if len(proposal.Steps) == 0 {
		return AutoplanPolicyDecision{Allowed: true, Reason: proposal.NoActionReason}, nil
	}
	if len(proposal.Steps) > policy.MaxSteps {
		return AutoplanPolicyDecision{Reason: "plan exceeds the allowed step count"}, nil
	}
	approvalRequired := !policy.AllowAutomaticMaterialization || policy.AutonomyLevel == AutonomyAdvise || len(proposal.RequiredApprovals) > 0
	for _, step := range proposal.Steps {
		if len(policy.AllowedAssigneeIDs) > 0 && !containsString(policy.AllowedAssigneeIDs, step.AssigneeAgentID) {
			return AutoplanPolicyDecision{Reason: "plan assignee is outside the responsibility policy"}, nil
		}
		if len(policy.AllowedRiskLevels) > 0 && !autoplanRiskAllowed(policy.AllowedRiskLevels, step.RiskLevel) {
			return AutoplanPolicyDecision{Reason: "plan risk level is outside the responsibility policy"}, nil
		}
		for _, scope := range step.ExecutionScopes {
			if len(policy.AllowedExecutionScopes) > 0 && !policyScopeAllows(policy.AllowedExecutionScopes, scope) {
				return AutoplanPolicyDecision{Reason: "plan execution scope is outside the responsibility policy"}, nil
			}
		}
	}
	if approvalRequired {
		return AutoplanPolicyDecision{Allowed: true, ApprovalRequired: true, Reason: "plan requires approval before materialization"}, nil
	}
	return AutoplanPolicyDecision{Allowed: true, Reason: "plan is within the autonomous responsibility policy"}, nil
}

func autoplanRiskLevels(values []string) []AutoplanRiskLevel {
	levels := make([]AutoplanRiskLevel, 0, len(values))
	for _, value := range values {
		level := AutoplanRiskLevel(strings.ToLower(strings.TrimSpace(value)))
		if level.valid() {
			levels = append(levels, level)
		}
	}
	return AutoplanPolicy{AllowedRiskLevels: levels}.Normalized().AllowedRiskLevels
}

func autoplanRiskAllowed(levels []AutoplanRiskLevel, required AutoplanRiskLevel) bool {
	for _, level := range levels {
		if level == required {
			return true
		}
	}
	return false
}
