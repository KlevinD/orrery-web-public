package main

import (
	"context"
	"testing"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/operations"
)

func TestAppendDiscussionAuthorizationBackfillSnapshotIncludesStandaloneDiscussion(t *testing.T) {
	snapshot := authorizationapp.RelationBackfillSnapshot{}
	repo := authzBackfillDiscussionRepo{conversations: []discussionsession.ConversationRecordView{{
		ConversationRecord: discussionsession.ConversationRecord{
			ConversationID: "discussion-direct", CreatedBy: "human-ding",
		},
		Members: []discussionsession.ConversationMemberRecord{
			{ConversationID: "discussion-direct", MemberKind: "user", MemberID: "human-ding"},
			{ConversationID: "discussion-direct", MemberKind: "agent", MemberID: "bianbian"},
		},
	}}}

	if err := appendDiscussionAuthorizationBackfillSnapshot(context.Background(), &snapshot, repo); err != nil {
		t.Fatalf("append discussion snapshot: %v", err)
	}
	if len(snapshot.Discussions) != 1 {
		t.Fatalf("discussions = %+v", snapshot.Discussions)
	}
	discussion := snapshot.Discussions[0]
	if discussion.DiscussionID != "discussion-direct" || discussion.TenantID != "global" || discussion.MissionID != "" ||
		discussion.Owner != (authorizationapp.SubjectRef{Kind: "user", ID: "human-ding"}) || len(discussion.Members) != 2 {
		t.Fatalf("standalone discussion = %+v", discussion)
	}
}

func TestBuildAuthorizationRelationBackfillSnapshotIncludesStandaloneDiscussion(t *testing.T) {
	repo := authzBackfillDiscussionRepo{conversations: []discussionsession.ConversationRecordView{{
		ConversationRecord: discussionsession.ConversationRecord{
			ConversationID: "discussion-direct", CreatedBy: "human-ding",
		},
		Members: []discussionsession.ConversationMemberRecord{
			{ConversationID: "discussion-direct", MemberKind: "user", MemberID: "human-ding"},
			{ConversationID: "discussion-direct", MemberKind: "agent", MemberID: "bianbian"},
		},
	}}}

	snapshot, err := buildAuthorizationRelationBackfillSnapshot(context.Background(), nil, repo, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.Discussions) != 1 || snapshot.Discussions[0].DiscussionID != "discussion-direct" {
		t.Fatalf("discussions = %+v", snapshot.Discussions)
	}
}

func TestBuildAuthorizationRelationBackfillSnapshotCanonicalizesLegacyDiscussionMembers(t *testing.T) {
	repo := authzBackfillDiscussionRepo{conversations: []discussionsession.ConversationRecordView{{
		ConversationRecord: discussionsession.ConversationRecord{
			ConversationID: "discussion-direct", CreatedBy: "ding",
		},
		Members: []discussionsession.ConversationMemberRecord{
			{ConversationID: "discussion-direct", MemberKind: "user", MemberID: "ding"},
			{ConversationID: "discussion-direct", MemberKind: "agent", MemberID: "bianbian"},
		},
	}}}
	ops := authzBackfillOperations{graph: operations.OrgGraphDTO{Members: []operations.WorkforceMemberDTO{{
		AgentProfileID: "human-ding",
		AgentKind:      "human",
		UserAccount:    operations.WorkforceUserAccountDTO{Username: "ding"},
	}}}}

	snapshot, err := buildAuthorizationRelationBackfillSnapshot(context.Background(), nil, repo, nil, nil, nil, nil, ops, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.Discussions) != 1 {
		t.Fatalf("discussions = %+v", snapshot.Discussions)
	}
	discussion := snapshot.Discussions[0]
	if discussion.Owner != (authorizationapp.SubjectRef{Kind: "user", ID: "human-ding"}) ||
		discussion.Members[0] != (authorizationapp.SubjectRef{Kind: "user", ID: "human-ding"}) {
		t.Fatalf("canonical discussion = %+v", discussion)
	}
}

func TestAppendDiscussionAuthorizationBackfillSnapshotMergesMissionDiscussion(t *testing.T) {
	snapshot := authorizationapp.RelationBackfillSnapshot{Discussions: []authorizationapp.DiscussionRelationSnapshot{{
		DiscussionID: "discussion-1", MissionID: "mission-1", TenantID: "tenant-a",
		Owner:   authorizationapp.SubjectRef{Kind: "user", ID: "human-ding"},
		Members: []authorizationapp.SubjectRef{{Kind: "agent", ID: "bianbian"}},
	}}}
	repo := authzBackfillDiscussionRepo{conversations: []discussionsession.ConversationRecordView{{
		ConversationRecord: discussionsession.ConversationRecord{
			ConversationID: "discussion-1", TenantID: "tenant-a", CreatedBy: "human-ding",
		},
		Members: []discussionsession.ConversationMemberRecord{
			{ConversationID: "discussion-1", MemberKind: "user", MemberID: "human-ding"},
			{ConversationID: "discussion-1", MemberKind: "agent", MemberID: "reviewer-agent"},
		},
		LinkedMissions: []discussionsession.LinkedMissionRecord{{ConversationID: "discussion-1", MissionID: "mission-1"}},
	}}}

	if err := appendDiscussionAuthorizationBackfillSnapshot(context.Background(), &snapshot, repo); err != nil {
		t.Fatalf("append discussion snapshot: %v", err)
	}
	if len(snapshot.Discussions) != 1 {
		t.Fatalf("merged discussions = %+v", snapshot.Discussions)
	}
	discussion := snapshot.Discussions[0]
	if discussion.MissionID != "mission-1" || len(discussion.Members) != 3 {
		t.Fatalf("merged discussion = %+v", discussion)
	}
}

type authzBackfillDiscussionRepo struct {
	conversations []discussionsession.ConversationRecordView
}

func (r authzBackfillDiscussionRepo) ListConversations(context.Context) ([]discussionsession.ConversationRecordView, error) {
	return append([]discussionsession.ConversationRecordView(nil), r.conversations...), nil
}
