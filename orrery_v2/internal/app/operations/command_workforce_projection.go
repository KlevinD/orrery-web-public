package operations

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/userauth"
)

func (s *CommandService) applyWorkforceAccountProjection(ctx context.Context, profile agentcore.AgentProfile, member *WorkforceMemberDTO) error {
	if profile.AgentKind != agentcore.AgentKindHuman || strings.EqualFold(strings.TrimSpace(profile.ID.String()), userauth.SuperadminAgentProfileID) || s.accountProjector == nil {
		return nil
	}
	result, err := s.accountProjector.ProjectLatest(ctx, profile.ID.String())
	if err != nil {
		return err
	}
	if member != nil {
		member.AccountProjection = WorkforceAccountProjectionDTO{
			Status: result.Status, Revision: result.Revision,
			KratosIdentityID: result.KratosIdentityID, InitialPassword: result.InitialPassword,
		}
	}
	return nil
}
