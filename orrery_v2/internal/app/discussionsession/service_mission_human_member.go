package discussionsession

import (
	"context"
	"strings"
)

func (s *Service) reconcileManagedMissionHumanMembers(ctx context.Context, view ConversationRecordView, humanIDs []string, missionID, source string) error {
	// A link alone cannot grant access to existing private or shared chat history.
	// Bootstrap provenance identifies a group created specifically for this Mission.
	if !isDedicatedMissionConversation(view, missionID) {
		return nil
	}
	entries, err := s.resolveConversationMemberAddEntries(ctx, humanIDs)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if conversationMemberKindForDirectoryEntry(entry) != "user" {
			return ErrInvalidRequest
		}
	}
	desired := map[string]bool{}
	for _, entry := range entries {
		id := conversationDirectoryEntryMemberID(entry)
		desired[id] = true
		found := false
		for _, member := range view.Members {
			if member.MemberKind != "user" || member.MemberID != id {
				continue
			}
			found = true
			// Do not take ownership of the creator or explicitly invited members.
			if member.ExplicitGrantorID == "" && (member.ManagedSource != "" || len(member.ManagedSources) > 0) {
				if _, _, err := s.repo.MutateMemberManagedSource(ctx, member, source, true); err != nil {
					return err
				}
			}
			break
		}
		if found {
			continue
		}
		name, err := strictConversationDisplayName(id, entry.DisplayName, entry.RealName)
		if err != nil {
			return err
		}
		member := grantConversationMemberManagedSource(ConversationMemberRecord{
			ConversationID: view.ConversationID, MemberKind: "user", MemberID: id,
			DisplayName: name, Role: "Mission participant", CreatedAt: s.now(),
		}, source)
		if _, _, err := s.repo.MutateMemberManagedSource(ctx, member, source, true); err != nil {
			return err
		}
	}
	for _, member := range view.Members {
		if member.MemberKind != "user" || desired[member.MemberID] || !conversationMemberHasManagedSource(member, source) {
			continue
		}
		if _, exists, err := s.repo.MutateMemberManagedSource(ctx, member, source, false); err != nil {
			return err
		} else if !exists {
			s.deleteConversationMemberAuthorization(ctx, view.ConversationID, view.TenantID, member)
		}
	}
	s.writeConversationAuthorization(ctx, view.ConversationID)
	return nil
}

func isDedicatedMissionConversation(view ConversationRecordView, missionID string) bool {
	kind := strings.TrimSpace(view.ConversationKind)
	if (kind != "" && kind != ConversationKindGroup) || strings.TrimSpace(view.PurposeKind) != "" || len(view.LinkedMissions) != 1 || view.LinkedMissions[0].MissionID != missionID {
		return false
	}
	bootstrap := missionConversationBootstrapManagedSource(view.TenantID, missionID)
	for _, member := range view.Members {
		if conversationMemberHasManagedSource(member, bootstrap) {
			return true
		}
	}
	return false
}
