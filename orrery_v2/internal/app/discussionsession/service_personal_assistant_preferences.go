package discussionsession

import (
	"context"
	"fmt"
	"strings"
)

func (s *Service) attachPersonalAssistantPreferences(ctx context.Context, request *DiscussionAgentRequest) error {
	// Private owner preferences must not enter shared Mission/group contexts,
	// including groups that were originally created by a personal assistant.
	if request == nil || !isPersonalAssistantConversationKind(request.ConversationKind) {
		return nil
	}
	provider, ok := s.memory.(PersonalAssistantPreferenceProvider)
	if !ok {
		return nil
	}
	owner := PersonalAssistantPreferenceOwner{
		TenantID: firstNonBlank(request.ConversationTenantID, request.Viewer.TenantID, "global"),
		OwnerID:  firstNonBlank(request.ConversationOwnerID, request.Viewer.OwnerID, request.Viewer.ViewerID),
		AgentID:  strings.TrimSpace(request.LogicalAgentID),
	}
	if owner.AgentID == "" || owner.AgentID != PersonalAssistantAgentID(owner.TenantID, owner.OwnerID) {
		return fmt.Errorf("personal preference identity does not match the conversation owner")
	}
	digest, err := provider.PersonalAssistantPreferences(ctx, owner)
	if err != nil || strings.TrimSpace(digest.Summary) == "" {
		return err
	}
	if existing := request.Memory.PersonalDigest; strings.TrimSpace(existing.Summary) != "" {
		digest.Summary += "\n\n" + existing.Summary
		digest.SourceMemory = append(digest.SourceMemory, existing.SourceMemory...)
		digest.SourceEvidence = append(digest.SourceEvidence, existing.SourceEvidence...)
		digest.Evidence = append(digest.Evidence, existing.Evidence...)
		digest.DigestID += "." + existing.DigestID
		digest.Sensitivity = existing.Sensitivity
	}
	request.Memory.PersonalDigest = digest
	return nil
}
