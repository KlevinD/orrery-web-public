package main

import (
	"context"
	"testing"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/discussionsession"
)

func TestProductionDiscussionAuthorizationWritesAndDeletesRelations(t *testing.T) {
	sink := &authzRelationSink{}
	adapter := productionDiscussionAuthorizationRelations{projector: authorizationapp.NewRelationProjector(sink)}
	snapshot := discussionsession.ConversationAuthorizationSnapshot{
		ConversationID: "discussion-direct",
		Owner:          discussionsession.ConversationAuthorizationSubject{Kind: "user", ID: "human-ding"},
		Members: []discussionsession.ConversationAuthorizationSubject{
			{Kind: "user", ID: "human-ding"},
			{Kind: "agent", ID: "bianbian"},
		},
	}

	if err := adapter.WriteConversationRelations(context.Background(), snapshot); err != nil {
		t.Fatalf("WriteConversationRelations: %v", err)
	}
	if len(sink.written) != 3 {
		t.Fatalf("written relations = %+v", sink.written)
	}
	assertAuthzTuple(t, sink.written[0], "user", "human-ding", "owner", "discussion", "discussion-direct")
	assertAuthzTuple(t, sink.written[1], "user", "human-ding", "member", "discussion", "discussion-direct")
	assertAuthzTuple(t, sink.written[2], "agent", "bianbian", "member", "discussion", "discussion-direct")

	if err := adapter.DeleteConversationMemberRelation(context.Background(), "discussion-direct", "", discussionsession.ConversationAuthorizationSubject{Kind: "agent", ID: "bianbian"}); err != nil {
		t.Fatalf("DeleteConversationMemberRelation: %v", err)
	}
	if len(sink.deleted) != 1 {
		t.Fatalf("deleted member relations = %+v", sink.deleted)
	}
	assertAuthzTuple(t, sink.deleted[0], "agent", "bianbian", "member", "discussion", "discussion-direct")

	sink.deleted = nil
	if err := adapter.DeleteConversationRelations(context.Background(), snapshot); err != nil {
		t.Fatalf("DeleteConversationRelations: %v", err)
	}
	if len(sink.deleted) != 3 {
		t.Fatalf("deleted discussion relations = %+v", sink.deleted)
	}
}
