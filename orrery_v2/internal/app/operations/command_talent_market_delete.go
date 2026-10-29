package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *CommandService) DeleteTalentMarketMember(ctx context.Context, viewer ViewerRef, cmd DeleteTalentMarketMemberCommand) error {
	if s == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceManage); err != nil {
		return err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionWorkforceManage); err != nil {
		return err
	}
	deleter, ok := s.profiles.(WorkforceProfileDeletePort)
	if !ok {
		return ErrServiceDependency
	}
	profileID := agentcore.AgentProfileID(strings.TrimSpace(cmd.AgentProfileID))
	if profileID.IsZero() {
		return fmt.Errorf("%w: agent_profile_id is required", ErrInvalidRequest)
	}
	return s.deleteTalentMarketProfile(ctx, viewer, deleter, profileID, cmd.Reason)
}

func (s *CommandService) DeleteTalentMarketMembers(ctx context.Context, viewer ViewerRef, cmd DeleteTalentMarketMembersCommand) (DeleteTalentMarketMembersResponseDTO, error) {
	if s == nil || s.profiles == nil {
		return DeleteTalentMarketMembersResponseDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceManage); err != nil {
		return DeleteTalentMarketMembersResponseDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionWorkforceManage); err != nil {
		return DeleteTalentMarketMembersResponseDTO{}, err
	}
	deleter, ok := s.profiles.(WorkforceProfileDeletePort)
	if !ok {
		return DeleteTalentMarketMembersResponseDTO{}, ErrServiceDependency
	}
	if len(cmd.AgentProfileIDs) == 0 {
		return DeleteTalentMarketMembersResponseDTO{}, fmt.Errorf("%w: agent_profile_ids are required", ErrInvalidRequest)
	}
	var response DeleteTalentMarketMembersResponseDTO
	seen := map[agentcore.AgentProfileID]struct{}{}
	for _, rawID := range cmd.AgentProfileIDs {
		profileID := agentcore.AgentProfileID(strings.TrimSpace(rawID))
		if profileID.IsZero() {
			response.Skipped = append(response.Skipped, TalentMarketDeleteSkippedDTO{AgentProfileID: "", Reason: "invalid_id"})
			continue
		}
		if _, ok := seen[profileID]; ok {
			response.Skipped = append(response.Skipped, TalentMarketDeleteSkippedDTO{AgentProfileID: profileID.String(), Reason: "duplicate"})
			continue
		}
		seen[profileID] = struct{}{}
		if err := s.deleteTalentMarketProfile(ctx, viewer, deleter, profileID, cmd.Reason); err != nil {
			response.Skipped = append(response.Skipped, TalentMarketDeleteSkippedDTO{AgentProfileID: profileID.String(), Reason: talentMarketDeleteSkipReason(err)})
			continue
		}
		response.Deleted = append(response.Deleted, profileID.String())
	}
	response.DeletedCount = len(response.Deleted)
	return response, nil
}

func (s *CommandService) deleteTalentMarketProfile(ctx context.Context, viewer ViewerRef, deleter WorkforceProfileDeletePort, profileID agentcore.AgentProfileID, reason string) error {
	if agentcore.IsReservedNonWorkforceProfileID(profileID.String()) {
		return fmt.Errorf("%w: reserved non-workforce principal cannot be deleted from the talent market", ErrInvalidRequest)
	}
	profile, err := s.profiles.GetProfile(ctx, profileID)
	if err != nil {
		return ErrNotFound
	}
	if profile.IsPersonalAssistant() {
		return ErrNotFound
	}
	if !profile.Placement.IsMarket(profile.OrgIdentity) {
		return fmt.Errorf("%w: only talent market profiles can be physically deleted", ErrInvalidRequest)
	}
	deleteReason := strings.TrimSpace(reason)
	if deleteReason == "" {
		deleteReason = "deleted from talent market"
	}
	ownerType := profileKnowledgeOwnerType(string(profile.AgentKind))
	if err := s.deleteLifecycleKnowledgeBase(ctx, viewer, ownerType, profile.ID.String(), profile.PersonalKnowledgeBaseID); err != nil {
		return err
	}
	if err := deleter.DeleteProfile(ctx, profileID, deleteReason); err != nil {
		return err
	}
	return s.recordAudit(ctx, viewer, "talent_market.member.delete", profileID.String(), map[string]string{
		"agent_id":         profileID.String(),
		"agent_profile_id": profileID.String(),
		"placement":        "market",
		"reason":           deleteReason,
	})
}

func talentMarketDeleteSkipReason(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrInvalidRequest):
		return "not_market"
	case errors.Is(err, ErrPermissionDenied):
		return "permission_denied"
	default:
		return "failed"
	}
}
