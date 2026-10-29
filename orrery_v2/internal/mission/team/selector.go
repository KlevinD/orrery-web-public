package team

import (
	"context"
	"sort"

	agentcore "orrery_v2/internal/agent/core"
)

type AgentDirectory interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type CandidateSelection struct {
	Accepted []CandidateEvaluation
	Rejected []CandidateEvaluation
	Selected CandidateEvaluation
}

type DirectoryCandidateSelector struct {
	directory AgentDirectory
	policy    AssignmentPolicy
}

func NewDirectoryCandidateSelector(directory AgentDirectory, policy AssignmentPolicy) *DirectoryCandidateSelector {
	return &DirectoryCandidateSelector{directory: directory, policy: policy}
}

func (s *DirectoryCandidateSelector) Select(ctx context.Context, request StaffingRequest, team MissionTeam) (CandidateSelection, error) {
	if err := ctx.Err(); err != nil {
		return CandidateSelection{}, err
	}
	query := directoryQueryForRequest(request, team)
	page, err := s.directory.QueryDirectory(ctx, query)
	if err != nil {
		return CandidateSelection{}, err
	}
	selection := CandidateSelection{}
	for _, rejection := range page.Rejected {
		selection.Rejected = append(selection.Rejected, evaluationFromDirectoryRejection(rejection))
	}
	for _, entry := range page.Entries {
		eval := s.policy.Evaluate(request, entry, team)
		if eval.Accepted() {
			selection.Accepted = append(selection.Accepted, eval)
			continue
		}
		selection.Rejected = append(selection.Rejected, eval)
	}
	sort.SliceStable(selection.Accepted, func(i, j int) bool {
		return selection.Accepted[i].Score > selection.Accepted[j].Score
	})
	if len(selection.Accepted) > 0 {
		selection.Selected = selection.Accepted[0]
	}
	return selection, nil
}

func directoryQueryForRequest(request StaffingRequest, team MissionTeam) agentcore.AgentDirectoryQuery {
	roles := make([]agentcore.RoleID, 0, len(request.DesiredRoles))
	for _, role := range request.DesiredRoles {
		roles = append(roles, agentcore.RoleID(role))
		roles = append(roles, agentcore.RoleID(lowerMissionRole(role)))
	}
	excluded := make([]agentcore.AgentProfileID, 0)
	for _, member := range team.Members {
		if member.Status == MemberStatusActive {
			excluded = append(excluded, member.AgentProfileID)
		}
	}
	return agentcore.AgentDirectoryQuery{
		Roles:                 roles,
		SkillRefs:             request.RequiredSkills,
		ToolRefs:              request.RequiredTools,
		AuthorityScope:        request.AuthorityRequirements,
		MaxCost:               request.BudgetConstraints.MaxCostPerHour,
		MinQualityScore:       request.QualityConstraints.MinScore,
		AllowedRiskLevels:     request.RiskConstraints.AllowedLevels,
		EnabledOnly:           true,
		IncludeHumans:         request.AllowHuman,
		IncludeAI:             request.AllowAI,
		ExcludeAgentProfiles:  excluded,
		AvailabilityWindow: agentcore.QueryAvailabilityWindow{
			From:  request.AvailabilityWindow.From,
			Until: request.AvailabilityWindow.Until,
		},
		Sort: agentcore.DirectorySortScoreDesc,
	}
}

func lowerMissionRole(role MissionRole) string {
	out := []rune(string(role))
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + ('a' - 'A')
		}
	}
	return string(out)
}

func evaluationFromDirectoryRejection(rejection agentcore.DirectoryRejection) CandidateEvaluation {
	return CandidateEvaluation{
		CandidateProfileID:  rejection.AgentProfileID,
		CandidateInstanceID: rejection.AgentInstanceID,
		Score:               0,
		RejectionReasons:    []PolicyCode{policyCodeFromDirectory(rejection.Code)},
	}
}

func policyCodeFromDirectory(code string) PolicyCode {
	switch code {
	case string(CodeCandidateNotAgent):
		return CodeCandidateNotAgent
	case string(CodeProfileDisabled):
		return CodeProfileDisabled
	case string(CodeAvailabilityDenied):
		return CodeAvailabilityDenied
	case string(CodeAuthorityDenied):
		return CodeAuthorityDenied
	case string(CodeSkillMissing):
		return CodeSkillMissing
	case string(CodeToolMissing):
		return CodeToolMissing
	case string(CodeRuntimeNotAvailable):
		return CodeRuntimeNotAvailable
	case string(CodeMemoryScopeDenied):
		return CodeMemoryScopeDenied
	case string(CodeRiskDenied):
		return CodeRiskDenied
	case string(CodeBudgetDenied):
		return CodeBudgetDenied
	case string(CodeQualityDenied):
		return CodeQualityDenied
	default:
		return PolicyCode(code)
	}
}
