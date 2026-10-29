package team

import agentcore "orrery_v2/internal/agent/core"

type EvaluationResult struct {
	Allowed bool
	Code    PolicyCode
	Message string
}

type CandidateEvaluation struct {
	CandidateProfileID  agentcore.AgentProfileID
	CandidateInstanceID agentcore.AgentInstanceID
	MatchedRoles        []agentcore.RoleRef
	MatchedSkills       []agentcore.SkillRef
	MatchedTools        []agentcore.ToolRef
	AvailabilityResult  EvaluationResult
	AuthorityResult     EvaluationResult
	CostResult          EvaluationResult
	QualityResult       EvaluationResult
	RiskResult          EvaluationResult
	Score               int
	RejectionReasons    []PolicyCode
}

func (e CandidateEvaluation) Accepted() bool {
	return len(e.RejectionReasons) == 0
}
