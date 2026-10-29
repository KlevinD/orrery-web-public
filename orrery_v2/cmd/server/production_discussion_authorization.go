package main

import (
	"context"
	"strings"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/discussionsession"
)

type productionDiscussionAuthorizationRelations struct {
	projector *authorizationapp.RelationProjector
}

func (r productionDiscussionAuthorizationRelations) WriteConversationRelations(ctx context.Context, snapshot discussionsession.ConversationAuthorizationSnapshot) error {
	if r.projector == nil {
		return nil
	}
	return r.projector.WriteDiscussionCreated(
		ctx,
		productionDiscussionAuthorizationSubject(snapshot.Owner),
		snapshot.ConversationID,
		snapshot.MissionID,
		snapshot.TenantID,
		productionDiscussionAuthorizationSubjects(snapshot.Members),
	)
}

func (r productionDiscussionAuthorizationRelations) DeleteConversationMemberRelation(ctx context.Context, conversationID, tenantID string, member discussionsession.ConversationAuthorizationSubject) error {
	if r.projector == nil {
		return nil
	}
	return r.projector.DeleteDiscussionMember(ctx, productionDiscussionAuthorizationSubject(member), conversationID, tenantID)
}

func (r productionDiscussionAuthorizationRelations) DeleteConversationRelations(ctx context.Context, snapshot discussionsession.ConversationAuthorizationSnapshot) error {
	if r.projector == nil {
		return nil
	}
	return r.projector.DeleteDiscussionRelations(
		ctx,
		productionDiscussionAuthorizationSubject(snapshot.Owner),
		snapshot.ConversationID,
		snapshot.MissionID,
		snapshot.TenantID,
		productionDiscussionAuthorizationSubjects(snapshot.Members),
	)
}

func productionDiscussionAuthorizationSubject(subject discussionsession.ConversationAuthorizationSubject) authorizationapp.SubjectRef {
	kind := strings.ToLower(strings.TrimSpace(subject.Kind))
	id := strings.TrimSpace(subject.ID)
	if id == "" || kind != "user" && kind != "agent" {
		return authorizationapp.SubjectRef{}
	}
	return authorizationapp.SubjectRef{Kind: kind, ID: id}
}

func productionDiscussionAuthorizationSubjects(subjects []discussionsession.ConversationAuthorizationSubject) []authorizationapp.SubjectRef {
	out := make([]authorizationapp.SubjectRef, 0, len(subjects))
	for _, subject := range subjects {
		mapped := productionDiscussionAuthorizationSubject(subject)
		if mapped.Kind == "" || mapped.ID == "" {
			continue
		}
		out = appendUniqueAuthorizationSubject(out, mapped)
	}
	return out
}
