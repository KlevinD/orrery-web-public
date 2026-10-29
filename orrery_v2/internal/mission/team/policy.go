package team

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type StaffingPolicy struct {
	AllowUnknownAvailability bool
	AllowHuman               bool
	AllowAI                  bool
	AllowUnknownRisk         bool
	RequireHumanInboxPolicy  bool
}

func DefaultStaffingPolicy() StaffingPolicy {
	return StaffingPolicy{
		AllowUnknownAvailability: false,
		AllowHuman:               false,
		AllowAI:                  true,
		AllowUnknownRisk:         false,
		RequireHumanInboxPolicy:  false,
	}
}

type AssignmentPolicy struct {
	defaults StaffingPolicy
}

func NewAssignmentPolicy(defaults StaffingPolicy) AssignmentPolicy {
	return AssignmentPolicy{defaults: defaults}
}

func (p AssignmentPolicy) Evaluate(request StaffingRequest, entry agentcore.AgentDirectoryEntry, team MissionTeam) CandidateEvaluation {
	eval := CandidateEvaluation{
		CandidateProfileID:  entry.AgentProfileID,
		CandidateInstanceID: entry.AgentInstanceID,
		MatchedRoles:        matchedRoles(entry.OrgIdentity.RoleRefs, request.DesiredRoles),
		MatchedSkills:       matchedSkills(entry.SkillRefs, request.RequiredSkills),
		MatchedTools:        matchedTools(entry.ToolRefs, request.RequiredTools),
		AvailabilityResult:  EvaluationResult{Allowed: true},
		AuthorityResult:     EvaluationResult{Allowed: true},
		CostResult:          EvaluationResult{Allowed: true},
		QualityResult:       EvaluationResult{Allowed: true},
		RiskResult:          EvaluationResult{Allowed: true},
	}
	p.evaluateIdentity(request, entry, &eval)
	p.evaluateRefs(request, entry, &eval)
	p.evaluateOperationalSignals(request, entry, &eval)
	p.evaluateDuplicate(request, entry, team, &eval)
	eval.Score = scoreCandidate(entry, eval)
	return eval
}

func (p AssignmentPolicy) evaluateIdentity(request StaffingRequest, entry agentcore.AgentDirectoryEntry, eval *CandidateEvaluation) {
	if !entry.AgentKind.IsValid() {
		addReject(eval, CodeCandidateNotAgent, "candidate is not an agent profile")
		return
	}
	if !entry.Enabled {
		addReject(eval, CodeProfileDisabled, "profile is disabled")
	}
	if entry.OrgIdentity.OrganizationRef.ID == "" || entry.OrgIdentity.DepartmentRef.ID == "" || entry.OrgIdentity.PositionRef.ID == "" || len(entry.OrgIdentity.RoleRefs) == 0 || strings.TrimSpace(entry.OrgIdentity.AuthorityScope) == "" {
		addReject(eval, CodeOrgIdentityIncomplete, "organization identity is incomplete")
	}
	allowAI := request.AllowAI || p.defaults.AllowAI
	allowHuman := request.AllowHuman || p.defaults.AllowHuman
	switch entry.AgentKind {
	case agentcore.AgentKindAI:
		if !allowAI {
			addReject(eval, CodeCandidateNotAgent, "AI agents are not allowed for this request")
		}
	case agentcore.AgentKindHuman:
		if !allowHuman {
			addReject(eval, CodeCandidateNotAgent, "human agents are not allowed for this request")
		}
		if p.defaults.RequireHumanInboxPolicy && strings.TrimSpace(entry.MemoryProfileRef.AccessScope) == "" {
			addReject(eval, CodeMemoryScopeDenied, "human inbox or handoff policy is not available")
		}
	}
}

func (p AssignmentPolicy) evaluateRefs(request StaffingRequest, entry agentcore.AgentDirectoryEntry, eval *CandidateEvaluation) {
	if len(request.DesiredRoles) > 0 && len(eval.MatchedRoles) == 0 {
		addReject(eval, CodeAuthorityDenied, "stable org roles do not support requested mission role")
		eval.AuthorityResult = EvaluationResult{Allowed: false, Code: CodeAuthorityDenied, Message: "role mismatch"}
	}
	if len(request.RequiredSkills) > 0 && len(eval.MatchedSkills) != len(request.RequiredSkills) {
		addReject(eval, CodeSkillMissing, "required skills are missing")
	}
	if len(request.RequiredTools) > 0 && len(eval.MatchedTools) != len(request.RequiredTools) {
		addReject(eval, CodeToolMissing, "required tools are missing")
	}
	if strings.TrimSpace(request.AuthorityRequirements) != "" && !scopeAllows(entry.OrgIdentity.AuthorityScope, request.AuthorityRequirements) {
		addReject(eval, CodeAuthorityDenied, "authority scope does not satisfy request")
		eval.AuthorityResult = EvaluationResult{Allowed: false, Code: CodeAuthorityDenied, Message: "authority denied"}
	}
	if (entry.RuntimeSpecSummary.Kind == "" || !entry.RuntimeSpecSummary.Kind.IsValid()) && !systemManagedBaseAgent(entry) {
		addReject(eval, CodeRuntimeNotAvailable, "runtime spec is not available")
	}
	if entry.MemoryProfileRef.IsZero() {
		addReject(eval, CodeMemoryScopeDenied, "memory profile ref is required")
	}
}

func systemManagedBaseAgent(entry agentcore.AgentDirectoryEntry) bool {
	return entry.BaseAgentSummary.InheritsBaseAgent &&
		entry.BaseAgentSummary.AuditRequired &&
		entry.BaseAgentSummary.HasSystemPrompt &&
		entry.BaseAgentSummary.HasLLMAPIKeyRef
}

func (p AssignmentPolicy) evaluateOperationalSignals(request StaffingRequest, entry agentcore.AgentDirectoryEntry, eval *CandidateEvaluation) {
	if !availabilityAllowed(entry.Availability, request.AvailabilityWindow, request.AllowUnknownAvailability || p.defaults.AllowUnknownAvailability) {
		addReject(eval, CodeAvailabilityDenied, "availability does not satisfy request")
		eval.AvailabilityResult = EvaluationResult{Allowed: false, Code: CodeAvailabilityDenied, Message: string(entry.Availability.Status)}
	}
	if request.BudgetConstraints.MaxCostPerHour > 0 && entry.CostProfile.EstimatedCostPerHour > request.BudgetConstraints.MaxCostPerHour {
		addReject(eval, CodeBudgetDenied, "cost exceeds budget")
		eval.CostResult = EvaluationResult{Allowed: false, Code: CodeBudgetDenied, Message: "max hourly cost exceeded"}
	}
	if request.BudgetConstraints.MaxCostPerTask > 0 && entry.CostProfile.EstimatedCostPerTask > request.BudgetConstraints.MaxCostPerTask {
		addReject(eval, CodeBudgetDenied, "task cost exceeds budget")
		eval.CostResult = EvaluationResult{Allowed: false, Code: CodeBudgetDenied, Message: "max task cost exceeded"}
	}
	if request.QualityConstraints.MinScore > 0 && entry.QualitySignal.Score < request.QualityConstraints.MinScore {
		addReject(eval, CodeQualityDenied, "quality score is below minimum")
		eval.QualityResult = EvaluationResult{Allowed: false, Code: CodeQualityDenied, Message: "quality score below minimum"}
	}
	if request.QualityConstraints.MinSampleSize > 0 && entry.QualitySignal.SampleSize < request.QualityConstraints.MinSampleSize {
		addReject(eval, CodeQualityDenied, "quality sample size is below minimum")
		eval.QualityResult = EvaluationResult{Allowed: false, Code: CodeQualityDenied, Message: "sample size below minimum"}
	}
	if len(request.RiskConstraints.AllowedLevels) > 0 && !riskAllowedByRequest(entry.RiskProfile.RiskLevel, request.RiskConstraints.AllowedLevels, p.defaults.AllowUnknownRisk) {
		addReject(eval, CodeRiskDenied, "risk level is not allowed")
		eval.RiskResult = EvaluationResult{Allowed: false, Code: CodeRiskDenied, Message: string(entry.RiskProfile.RiskLevel)}
	}
	if request.RiskConstraints.RequireMitigation && len(entry.RiskProfile.Mitigations) == 0 {
		addReject(eval, CodeRiskDenied, "risk mitigation is required")
		eval.RiskResult = EvaluationResult{Allowed: false, Code: CodeRiskDenied, Message: "missing mitigation"}
	}
}

func (p AssignmentPolicy) evaluateDuplicate(request StaffingRequest, entry agentcore.AgentDirectoryEntry, team MissionTeam, eval *CandidateEvaluation) {
	for _, member := range team.Members {
		if member.AgentProfileID != entry.AgentProfileID {
			continue
		}
		for _, ref := range member.AssignmentRefs {
			if ref.Status != AssignmentStatusActive {
				continue
			}
			if request.IdempotencyKey != "" && request.IdempotencyKey == string(ref.ID) {
				continue
			}
			for _, role := range request.DesiredRoles {
				if ref.Role == role {
					addReject(eval, CodeDuplicateAssignment, "active assignment already exists")
					return
				}
			}
		}
	}
}

func addReject(eval *CandidateEvaluation, code PolicyCode, message string) {
	for _, existing := range eval.RejectionReasons {
		if existing == code {
			return
		}
	}
	eval.RejectionReasons = append(eval.RejectionReasons, code)
	switch code {
	case CodeAvailabilityDenied:
		eval.AvailabilityResult = EvaluationResult{Allowed: false, Code: code, Message: message}
	case CodeAuthorityDenied:
		eval.AuthorityResult = EvaluationResult{Allowed: false, Code: code, Message: message}
	case CodeBudgetDenied:
		eval.CostResult = EvaluationResult{Allowed: false, Code: code, Message: message}
	case CodeQualityDenied:
		eval.QualityResult = EvaluationResult{Allowed: false, Code: code, Message: message}
	case CodeRiskDenied:
		eval.RiskResult = EvaluationResult{Allowed: false, Code: code, Message: message}
	}
}

func matchedRoles(have []agentcore.RoleRef, desired []MissionRole) []agentcore.RoleRef {
	if len(desired) == 0 {
		return append([]agentcore.RoleRef(nil), have...)
	}
	var out []agentcore.RoleRef
	for _, ref := range have {
		for _, role := range desired {
			if strings.EqualFold(string(ref.ID), string(role)) || strings.EqualFold(string(ref.ID), strings.ToLower(string(role))) {
				out = append(out, ref)
			}
		}
	}
	return out
}

func matchedSkills(have []agentcore.SkillRef, want []agentcore.SkillRef) []agentcore.SkillRef {
	var out []agentcore.SkillRef
	for _, required := range want {
		for _, ref := range have {
			if ref.SkillID == required.SkillID {
				out = append(out, ref)
				break
			}
		}
	}
	return out
}

func matchedTools(have []agentcore.ToolRef, want []agentcore.ToolRef) []agentcore.ToolRef {
	var out []agentcore.ToolRef
	for _, required := range want {
		for _, ref := range have {
			if ref.ToolID == required.ToolID {
				out = append(out, ref)
				break
			}
		}
	}
	return out
}

func scopeAllows(have, want string) bool {
	for _, scope := range strings.Split(have, ",") {
		if strings.TrimSpace(scope) == strings.TrimSpace(want) {
			return true
		}
	}
	return strings.TrimSpace(have) == strings.TrimSpace(want)
}

func availabilityAllowed(availability agentcore.AgentAvailability, window AvailabilityWindow, allowUnknown bool) bool {
	if availability.Status == agentcore.AvailabilityUnknown {
		return allowUnknown
	}
	if availability.Status != agentcore.AvailabilityAvailable {
		return false
	}
	if availability.Capacity > 0 && availability.CurrentLoad >= availability.Capacity {
		return false
	}
	return windowCovered(availability.AvailableFrom, availability.AvailableUntil, window)
}

func windowCovered(from, until time.Time, window AvailabilityWindow) bool {
	if !window.From.IsZero() && !from.IsZero() && from.After(window.From) {
		return false
	}
	if !window.Until.IsZero() && !until.IsZero() && until.Before(window.Until) {
		return false
	}
	return true
}

func riskAllowedByRequest(level agentcore.RiskLevel, allowed []agentcore.RiskLevel, allowUnknown bool) bool {
	if level == agentcore.RiskLevelUnknown {
		return allowUnknown
	}
	for _, candidate := range allowed {
		if candidate == level {
			return true
		}
	}
	return false
}

func scoreCandidate(entry agentcore.AgentDirectoryEntry, eval CandidateEvaluation) int {
	if len(eval.RejectionReasons) > 0 {
		return maxInt(0, int(entry.QualitySignal.Score*40)-len(eval.RejectionReasons)*10)
	}
	score := 50
	score += len(eval.MatchedRoles) * 10
	score += len(eval.MatchedSkills) * 8
	score += len(eval.MatchedTools) * 6
	score += int(entry.QualitySignal.Score * 20)
	if entry.Availability.Capacity > 0 {
		score += maxInt(0, (entry.Availability.Capacity-entry.Availability.CurrentLoad)*2)
	}
	switch entry.RiskProfile.RiskLevel {
	case agentcore.RiskLevelLow:
		score += 10
	case agentcore.RiskLevelMedium:
		score += 4
	case agentcore.RiskLevelHigh:
		score -= 10
	case agentcore.RiskLevelCritical:
		score -= 20
	}
	if entry.CostProfile.EstimatedCostPerHour > 0 {
		score -= int(entry.CostProfile.EstimatedCostPerHour / 20)
	}
	return score
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
