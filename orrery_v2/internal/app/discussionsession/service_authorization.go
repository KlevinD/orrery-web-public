package discussionsession

import (
	"context"
	"strings"
)

type ConversationAuthorizationSubject struct {
	Kind string
	ID   string
}

type ConversationAuthorizationSnapshot struct {
	ConversationID string
	TenantID       string
	MissionID      string
	Owner          ConversationAuthorizationSubject
	Members        []ConversationAuthorizationSubject
}

type ConversationAuthorizationRelations interface {
	WriteConversationRelations(context.Context, ConversationAuthorizationSnapshot) error
	DeleteConversationMemberRelation(context.Context, string, string, ConversationAuthorizationSubject) error
	DeleteConversationRelations(context.Context, ConversationAuthorizationSnapshot) error
}

func (s *Service) writeConversationAuthorization(ctx context.Context, conversationID string) {
	if s == nil || s.repo == nil || s.authorization == nil {
		return
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		s.warnConversationAuthorization(ctx, "write", conversationID, err)
		return
	}
	snapshot := conversationAuthorizationSnapshot(view)
	if err := s.authorization.WriteConversationRelations(ctx, snapshot); err != nil {
		s.warnConversationAuthorization(ctx, "write", snapshot.ConversationID, err)
	}
}

func (s *Service) deleteConversationMemberAuthorization(ctx context.Context, conversationID, tenantID string, member ConversationMemberRecord) {
	if s == nil || s.authorization == nil {
		return
	}
	subject, ok := conversationAuthorizationSubject(member.MemberKind, member.MemberID)
	if !ok {
		return
	}
	if err := s.authorization.DeleteConversationMemberRelation(ctx, strings.TrimSpace(conversationID), strings.TrimSpace(tenantID), subject); err != nil {
		s.warnConversationAuthorization(ctx, "delete_member", conversationID, err)
	}
}

func (s *Service) deleteConversationAuthorization(ctx context.Context, view ConversationRecordView) {
	if s == nil || s.authorization == nil {
		return
	}
	snapshot := conversationAuthorizationSnapshot(view)
	if err := s.authorization.DeleteConversationRelations(ctx, snapshot); err != nil {
		s.warnConversationAuthorization(ctx, "delete", snapshot.ConversationID, err)
	}
}

func (s *Service) warnConversationAuthorization(ctx context.Context, operation, conversationID string, err error) {
	if s == nil || s.logger == nil || err == nil {
		return
	}
	s.logger.Warn(ctx, "discussion authorization projection failed",
		"operation", operation,
		"conversation_id", strings.TrimSpace(conversationID),
		"error", err,
	)
}

func conversationAuthorizationSnapshot(view ConversationRecordView) ConversationAuthorizationSnapshot {
	snapshot := ConversationAuthorizationSnapshot{
		ConversationID: strings.TrimSpace(view.ConversationID),
		TenantID:       strings.TrimSpace(view.TenantID),
		Members:        make([]ConversationAuthorizationSubject, 0, len(view.Members)),
	}
	for _, member := range view.Members {
		subject, ok := conversationAuthorizationSubject(member.MemberKind, member.MemberID)
		if !ok {
			continue
		}
		snapshot.Members = appendUniqueConversationAuthorizationSubject(snapshot.Members, subject)
		if subject.ID == strings.TrimSpace(view.CreatedBy) {
			snapshot.Owner = subject
		}
	}
	for _, link := range view.LinkedMissions {
		if missionID := strings.TrimSpace(link.MissionID); missionID != "" {
			snapshot.MissionID = missionID
			break
		}
	}
	return snapshot
}

func conversationAuthorizationSubject(kind, id string) (ConversationAuthorizationSubject, bool) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	id = strings.TrimSpace(id)
	if id == "" || kind != "user" && kind != "agent" {
		return ConversationAuthorizationSubject{}, false
	}
	return ConversationAuthorizationSubject{Kind: kind, ID: id}, true
}

func appendUniqueConversationAuthorizationSubject(subjects []ConversationAuthorizationSubject, subject ConversationAuthorizationSubject) []ConversationAuthorizationSubject {
	for _, existing := range subjects {
		if existing == subject {
			return subjects
		}
	}
	return append(subjects, subject)
}
