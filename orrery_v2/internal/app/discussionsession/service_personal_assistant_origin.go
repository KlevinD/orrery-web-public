package discussionsession

import (
	"context"
	"strings"
)

type PersonalAssistantOrigin struct {
	OwnerID                string
	AuthorizationSubjectID string
}

// ResolvePersonalAssistantOrigin validates the source conversation server-side
// before a Mission is created. The subject check uses the canonical discussion
// identity, avoiding a username/account-id mismatch.
func (s *Service) ResolvePersonalAssistantOrigin(ctx context.Context, viewer ViewerRef, conversationID string) (PersonalAssistantOrigin, error) {
	view, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return PersonalAssistantOrigin{}, err
	}
	if isPersonalAssistantConversationKind(view.ConversationKind) {
		ownerID := strings.TrimSpace(view.OwnerID)
		if strings.TrimSpace(view.CreatedBy) == "" ||
			strings.TrimSpace(view.CreatedBy) != strings.TrimSpace(viewer.ViewerID) ||
			(ownerID != "" && ownerID != strings.TrimSpace(viewer.OwnerID)) {
			return PersonalAssistantOrigin{}, ErrForbidden
		}
		return PersonalAssistantOrigin{
			OwnerID:                strings.TrimSpace(firstNonBlank(ownerID, viewer.OwnerID, view.CreatedBy)),
			AuthorizationSubjectID: strings.TrimSpace(view.CreatedBy),
		}, nil
	}
	if !isPersonalAssistantRuntimeConversation(view.ConversationRecord) {
		return PersonalAssistantOrigin{}, ErrInvalidRequest
	}
	ownerID := strings.TrimSpace(view.PersonalAssistantOriginOwnerID)
	subjectID := strings.TrimSpace(view.PersonalAssistantOriginSubjectID)
	if ownerID == "" || subjectID == "" {
		return PersonalAssistantOrigin{}, ErrInvalidRequest
	}
	if subjectID != strings.TrimSpace(viewer.ViewerID) ||
		(strings.TrimSpace(view.CreatedBy) != "" && strings.TrimSpace(view.CreatedBy) != subjectID) ||
		(strings.TrimSpace(view.OwnerID) != "" && strings.TrimSpace(view.OwnerID) != ownerID) {
		return PersonalAssistantOrigin{}, ErrForbidden
	}
	return PersonalAssistantOrigin{OwnerID: ownerID, AuthorizationSubjectID: subjectID}, nil
}
