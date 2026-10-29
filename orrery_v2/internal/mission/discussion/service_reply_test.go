package discussion

import (
	"context"
	"errors"
	"testing"

	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
)

func TestDiscussionServiceReplyKeepsThreadAndClosesInbox(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	for _, member := range []DiscussionMember{
		testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1"),
		testAgentDiscussionMember(group.ID, "profile-agent-2", "assignment-2"),
	} {
		if err := svc.AddMember(ctx, group.ID, member); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}
	parent, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "[@Worker](mention://agent/profile-agent-2) can you respond?",
		MessageKind:       MessageKindRequest,
		RequiredResponse:  true,
	})
	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	inbox, err := svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{})
	if err != nil || len(inbox.Items) != 1 {
		t.Fatalf("expected inbox item, got page=%#v err=%v", inbox, err)
	}

	reply, err := svc.Reply(ctx, ReplyCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		ReplyToMessageID:  parent.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"},
		Content:           "I reviewed it and attached evidence.",
		InboxItemID:       inbox.Items[0].ID,
	})

	if err != nil {
		t.Fatalf("Reply returned error: %v", err)
	}
	if reply.ReplyToMessageID != parent.ID || reply.ThreadID != parent.ThreadID {
		t.Fatalf("reply threading mismatch: parent=%#v reply=%#v", parent, reply)
	}
	inbox, err = svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{IncludeClosed: true})
	if err != nil {
		t.Fatalf("ListInbox returned error: %v", err)
	}
	if inbox.Items[0].Status != InboxResponded || inbox.Items[0].RespondedByMessageID != reply.ID {
		t.Fatalf("inbox was not closed by reply: %#v", inbox.Items[0])
	}
}

func TestDiscussionServiceReplyCreatesAndDispatchesMentions(t *testing.T) {
	ctx := context.Background()
	dispatcher := &capturingMentionDispatcher{}
	svc, _ := newTestDiscussionServiceWithDispatcher(t, dispatcher)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	for _, member := range []DiscussionMember{
		testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1"),
		testAgentDiscussionMember(group.ID, "profile-agent-2", "assignment-2"),
		testAgentDiscussionMember(group.ID, "profile-agent-3", "assignment-3"),
	} {
		if err := svc.AddMember(ctx, group.ID, member); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}
	parent, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "[@Worker](mention://agent/profile-agent-2) can you respond?",
		MessageKind:       MessageKindRequest,
	})
	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	inbox, err := svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{})
	if err != nil || len(inbox.Items) != 1 {
		t.Fatalf("expected inbox item, got page=%#v err=%v", inbox, err)
	}
	dispatcher.items = nil

	reply, err := svc.Reply(ctx, ReplyCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		ReplyToMessageID:  parent.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"},
		Content:           "I need [@QA](mention://agent/profile-agent-3) to verify this.",
		InboxItemID:       inbox.Items[0].ID,
	})

	if err != nil {
		t.Fatalf("Reply returned error: %v", err)
	}
	nextInbox, err := svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-3"}, InboxQuery{})
	if err != nil {
		t.Fatalf("ListInbox returned error: %v", err)
	}
	if len(nextInbox.Items) != 1 || nextInbox.Items[0].MessageID != reply.ID || !nextInbox.Items[0].RequiredResponse {
		t.Fatalf("reply mention should create required inbox item: %#v", nextInbox.Items)
	}
	if len(dispatcher.items) != 1 || dispatcher.items[0].Recipient.Ref != "profile-agent-3" {
		t.Fatalf("reply mention should dispatch mentioned agent, got %#v", dispatcher.items)
	}
}

func TestDiscussionServiceCloseWithReplyRejectsWrongRecipient(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	for _, member := range []DiscussionMember{
		testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1"),
		testAgentDiscussionMember(group.ID, "profile-agent-2", "assignment-2"),
		testAgentDiscussionMember(group.ID, "profile-agent-3", "assignment-3"),
	} {
		if err := svc.AddMember(ctx, group.ID, member); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}
	parent, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "[@Worker](mention://agent/profile-agent-2) can you respond?",
		MessageKind:       MessageKindRequest,
		RequiredResponse:  true,
	})
	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	inbox, err := svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{})
	if err != nil || len(inbox.Items) != 1 {
		t.Fatalf("expected inbox item, got page=%#v err=%v", inbox, err)
	}

	_, err = svc.CloseWithReply(ctx, inbox.Items[0].ID, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-3"}, parent.ID)

	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("CloseWithReply error = %v, want ErrPermissionDenied", err)
	}
	inbox, err = svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{IncludeClosed: true})
	if err != nil {
		t.Fatalf("ListInbox returned error: %v", err)
	}
	if inbox.Items[0].Status != InboxPending || inbox.Items[0].RespondedByMessageID != "" {
		t.Fatalf("wrong recipient should not close inbox item: %#v", inbox.Items[0])
	}
}

func TestDiscussionServiceCloseWithReplyEmitsClosureEvent(t *testing.T) {
	ctx := context.Background()
	svc, unit := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	for _, member := range []DiscussionMember{
		testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1"),
		testAgentDiscussionMember(group.ID, "profile-agent-2", "assignment-2"),
	} {
		if err := svc.AddMember(ctx, group.ID, member); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}
	_, err = svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "[@Worker](mention://agent/profile-agent-2) can you close this?",
		MessageKind:       MessageKindRequest,
		RequiredResponse:  true,
		IdempotencyKey:    "post-close-event",
	})
	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	inbox, err := svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{})
	if err != nil || len(inbox.Items) != 1 {
		t.Fatalf("expected inbox item, got page=%#v err=%v", inbox, err)
	}

	_, err = svc.CloseWithReply(ctx, inbox.Items[0].ID, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, MessageID("reply-message-1"))

	if err != nil {
		t.Fatalf("CloseWithReply returned error: %v", err)
	}
	inbox, err = svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{IncludeClosed: true})
	if err != nil {
		t.Fatalf("ListInbox returned error: %v", err)
	}
	if inbox.Items[0].Status != InboxResponded || inbox.Items[0].RespondedByMessageID != "reply-message-1" {
		t.Fatalf("inbox was not closed with reply: %#v", inbox.Items[0])
	}
	events := loadDiscussionEvents(t, ctx, unit, "mission-1")
	if !hasEvent(events, missionevent.EventInboxItemClosed) {
		t.Fatalf("CloseWithReply must emit closure event for projection refresh, got %#v", eventTypes(events))
	}
}
