package discussion

import (
	"context"
	"errors"
	"sync"
	"testing"

	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/uow"
)

func TestPlatformRepositoryMessageIdempotencyBindingIsInsertOnly(t *testing.T) {
	ctx := context.Background()
	_, unit := newTestDiscussionService(t)
	repository := NewPlatformRepository()
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return repository.SaveMessageIdempotency(ctx, tx, "tenant-a", "notice:run-1", "message-1")
	}); err != nil {
		t.Fatalf("save first idempotency binding: %v", err)
	}
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return repository.SaveMessageIdempotency(ctx, tx, "tenant-a", "notice:run-1", "message-1")
	}); err != nil {
		t.Fatalf("replay same idempotency binding: %v", err)
	}
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return repository.SaveMessageIdempotency(ctx, tx, "tenant-a", "notice:run-1", "message-2")
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("replace idempotency binding error = %v, want ErrConflict", err)
	}
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		messageID, found, err := repository.GetMessageIdempotency(ctx, tx, "tenant-a", "notice:run-1")
		if err != nil {
			return err
		}
		if !found || messageID != "message-1" {
			t.Fatalf("stored binding = %q found=%v, want message-1", messageID, found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDiscussionServicePostMessageIdempotencySurvivesRestartAndConcurrency(t *testing.T) {
	ctx := context.Background()
	svc, unit := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-idem"), "Idempotent notices")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	member := testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1")
	if err := svc.AddMember(ctx, group.ID, member); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	command := PostMessageCommand{
		TenantID: "global", MissionID: group.MissionID, DiscussionGroupID: group.ID,
		Author: ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"}, Content: "Durable completion notice.",
		Summary: "Durable completion notice.", MessageKind: MessageKindSummary,
		Visibility: MessageVisibility{Scope: VisibilityMission}, RelatedTaskID: "task-1",
		SourceRefs: []string{"artifact://trace-1"}, IdempotencyKey: "agent-result-notice:run-1",
	}
	first, err := svc.PostMessage(ctx, command)
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	restarted := NewService(ServiceOptions{
		UnitOfWork: unit, Clock: svc.clock, IDGenerator: idgen.NewDeterministic("restart"), Logger: svc.logger,
	})
	second, err := restarted.PostMessage(ctx, command)
	if err != nil {
		t.Fatalf("PostMessage after restart: %v", err)
	}
	if second.ID != first.ID || second.CreatedAt != first.CreatedAt {
		t.Fatalf("restart replay message = %+v, want original %+v", second, first)
	}

	concurrentCommand := command
	concurrentCommand.IdempotencyKey = "agent-result-notice:run-2"
	concurrentCommand.Content = "Concurrent durable completion notice."
	concurrentCommand.Summary = concurrentCommand.Content
	const callers = 16
	results := make([]Message, callers)
	errs := make([]error, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := 0; index < callers; index++ {
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = restarted.PostMessage(ctx, concurrentCommand)
		}(index)
	}
	wait.Wait()
	for index := range results {
		if errs[index] != nil {
			t.Fatalf("concurrent PostMessage[%d]: %v", index, errs[index])
		}
		if results[index].ID != results[0].ID {
			t.Fatalf("concurrent message ids differ: %q != %q", results[index].ID, results[0].ID)
		}
	}

	snapshot := unitSnapshot(t, ctx, unit)
	if got := len(snapshot["mission_discussion_messages"]); got != 2 {
		t.Fatalf("persisted messages = %d, want 2", got)
	}
	if got := len(snapshot["mission_discussion_message_idempotency"]); got != 2 {
		t.Fatalf("persisted idempotency records = %d, want 2", got)
	}
	events := loadDiscussionEvents(t, ctx, unit, group.MissionID.String())
	posted := 0
	for _, eventType := range events {
		if eventType == string(missionevent.EventMessagePosted) {
			posted++
		}
	}
	if posted != 2 {
		t.Fatalf("message posted events = %d, want 2; events=%v", posted, events)
	}
}

func TestDiscussionServicePostMessageRejectsIdempotencyKeyReuseWithDifferentCommand(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	firstGroup, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-idem-a"), "First scope")
	if err != nil {
		t.Fatalf("CreateGroup first: %v", err)
	}
	secondGroup, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-idem-b"), "Second scope")
	if err != nil {
		t.Fatalf("CreateGroup second: %v", err)
	}
	for _, groupID := range []DiscussionGroupID{firstGroup.ID, secondGroup.ID} {
		for _, member := range []DiscussionMember{
			testAgentDiscussionMember(groupID, "profile-agent-1", "assignment-1"),
			testAgentDiscussionMember(groupID, "profile-agent-2", "assignment-2"),
		} {
			if err := svc.AddMember(ctx, groupID, member); err != nil {
				t.Fatalf("AddMember: %v", err)
			}
		}
	}
	base := PostMessageCommand{
		TenantID: "tenant-1", MissionID: firstGroup.MissionID, DiscussionGroupID: firstGroup.ID,
		Author: ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"}, Content: "Original notice.",
		MessageKind: MessageKindSummary, Visibility: MessageVisibility{Scope: VisibilityMission},
		RelatedTaskID: "task-1", IdempotencyKey: "shared-key",
	}
	if _, err := svc.PostMessage(ctx, base); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*PostMessageCommand)
	}{
		{name: "content", mutate: func(cmd *PostMessageCommand) { cmd.Content = "Changed notice." }},
		{name: "author", mutate: func(cmd *PostMessageCommand) { cmd.Author.Ref = "profile-agent-2" }},
		{name: "task", mutate: func(cmd *PostMessageCommand) { cmd.RelatedTaskID = "task-2" }},
		{name: "group and mission", mutate: func(cmd *PostMessageCommand) {
			cmd.MissionID = secondGroup.MissionID
			cmd.DiscussionGroupID = secondGroup.ID
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			tc.mutate(&changed)
			if _, err := svc.PostMessage(ctx, changed); !errors.Is(err, ErrConflict) {
				t.Fatalf("PostMessage error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestDiscussionServicePostMessageCreatesMentionsInboxAndEvents(t *testing.T) {
	ctx := context.Background()
	svc, unit := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stage 9")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	author := testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1")
	recipient := testAgentDiscussionMember(group.ID, "profile-agent-2", "assignment-2")
	if err := svc.AddMember(ctx, group.ID, author); err != nil {
		t.Fatalf("AddMember author: %v", err)
	}
	if err := svc.AddMember(ctx, group.ID, recipient); err != nil {
		t.Fatalf("AddMember recipient: %v", err)
	}

	msg, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "[@QA](mention://agent/profile-agent-2) please review this decision",
		MessageKind:       MessageKindDecision,
		RequiredResponse:  true,
	})

	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0].Target.Ref != "profile-agent-2" {
		t.Fatalf("mention not stored on message: %#v", msg.Mentions)
	}
	inbox, err := svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{})
	if err != nil {
		t.Fatalf("ListInbox returned error: %v", err)
	}
	if len(inbox.Items) != 1 || inbox.Items[0].MessageID != msg.ID || inbox.Items[0].Status != InboxPending {
		t.Fatalf("unexpected inbox page: %#v", inbox)
	}
	events := loadDiscussionEvents(t, ctx, unit, "mission-1")
	if !hasEvent(events, missionevent.EventMessagePosted) || !hasEvent(events, missionevent.EventAgentMentioned) || !hasEvent(events, missionevent.EventInboxItemCreated) {
		t.Fatalf("expected message, mention and inbox events, got %#v", eventTypes(events))
	}
	if !hasEvent(events, missionevent.EventDecisionRecorded) || !hasEvent(events, missionevent.EventMemoryCandidateCreated) {
		t.Fatalf("decision messages should emit decision and memory candidate events, got %#v", eventTypes(events))
	}
}

func TestDiscussionServicePostMessageAcceptsStableAgentProfileID(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stable identities")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	member := DiscussionMember{
		DiscussionGroupID:    group.ID,
		AgentProfileID:       "profile-1",
		AgentInstanceID:      "profile-1",
		MissionAssignmentRef: "assignment-1",
		MissionRole:          "Lead",
		MemberKind:           DiscussionMemberAgent,
		VisibilityScope:      VisibilityMission,
		CanPost:              true,
		CanMention:           true,
	}
	if err := svc.AddMember(ctx, group.ID, member); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	msg, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-1"},
		Content:           "Stable identity should post across mission instances.",
		MessageKind:       MessageKindRequest,
	})

	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	if msg.Author.AgentID != "profile-1" || msg.AuthorMissionRole != "Lead" {
		t.Fatalf("message author = %+v role=%s, want stable author with member role", msg.Author, msg.AuthorMissionRole)
	}
}

func TestDiscussionServiceAgentAuthorUsesStableIdentityAcrossArtifacts(t *testing.T) {
	ctx := context.Background()
	svc, unit := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Keep author identity stable")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	for _, member := range []DiscussionMember{
		{DiscussionGroupID: group.ID, AgentProfileID: "profile-author", AgentInstanceID: "profile-author", MissionAssignmentRef: "assignment-author", MemberKind: DiscussionMemberAgent, VisibilityScope: VisibilityMission, CanPost: true, CanMention: true},
		{DiscussionGroupID: group.ID, AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker", MissionAssignmentRef: "assignment-worker", MemberKind: DiscussionMemberAgent, VisibilityScope: VisibilityMission, CanPost: true, CanMention: true},
	} {
		if err := svc.AddMember(ctx, group.ID, member); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}

	msg, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-author"},
		Content:           "[@Worker](mention://agent/profile-worker) decision: use stable author identity.",
		Summary:           "Use stable author identity.",
		MessageKind:       MessageKindDecision,
	})
	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}

	if msg.Author.AgentID != "profile-author" {
		t.Fatalf("message author agent id = %q, want stable profile id", msg.Author.AgentID)
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0].CreatedBy != "profile-author" {
		t.Fatalf("mention creator = %#v, want stable author id", msg.Mentions)
	}
	candidate, ok := MemoryCandidateFromMessage(msg)
	if !ok {
		t.Fatalf("MemoryCandidateFromMessage returned false")
	}
	if candidate.CreatedBy != "profile-author" {
		t.Fatalf("memory candidate creator = %q, want stable author id", candidate.CreatedBy)
	}
	payloads := loadDiscussionMessagePayloads(t, ctx, unit, "mission-1")
	if len(payloads) == 0 || payloads[len(payloads)-1].AuthorID != "profile-author" {
		t.Fatalf("message event payloads = %#v, want stable author id", payloads)
	}
}

func TestDiscussionServiceMentionInboxUsesStableAgentProfileRecipient(t *testing.T) {
	ctx := context.Background()
	dispatcher := &capturingMentionDispatcher{}
	svc, unit := newTestDiscussionServiceWithDispatcher(t, dispatcher)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stable mention recipients")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	for _, member := range []DiscussionMember{
		{DiscussionGroupID: group.ID, AgentProfileID: "profile-author", AgentInstanceID: "profile-author", MissionAssignmentRef: "assignment-author", MemberKind: DiscussionMemberAgent, VisibilityScope: VisibilityMission, CanPost: true, CanMention: true},
		{DiscussionGroupID: group.ID, AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker", MissionAssignmentRef: "assignment-worker", MemberKind: DiscussionMemberAgent, VisibilityScope: VisibilityMission, CanPost: true, CanMention: true},
	} {
		if err := svc.AddMember(ctx, group.ID, member); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}

	msg, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-author"},
		Content:           "[@Worker](mention://agent/profile-worker) please handle this",
		MessageKind:       MessageKindRequest,
	})
	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	inbox, err := svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-worker"}, InboxQuery{})
	if err != nil {
		t.Fatalf("ListInbox stable recipient returned error: %v", err)
	}
	if len(inbox.Items) != 1 || inbox.Items[0].Recipient.Ref != "profile-worker" || inbox.Items[0].MessageID != msg.ID {
		t.Fatalf("stable inbox page = %#v", inbox)
	}
	if len(dispatcher.items) != 1 || dispatcher.items[0].Recipient.Ref != "profile-worker" {
		t.Fatalf("dispatcher should receive stable recipient item: %#v", dispatcher.items)
	}
	payloads := loadDiscussionInboxPayloads(t, ctx, unit, missioncore.MissionID("mission-1"))
	if len(payloads) != 2 || payloads[0].OwnerID != "profile-worker" || payloads[1].OwnerID != "profile-worker" {
		t.Fatalf("inbox events should use stable owner id, got %#v", payloads)
	}
}

func TestDiscussionServiceRejectsRuntimeInstanceAgentMention(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stable mention identities")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	for _, member := range []DiscussionMember{
		{DiscussionGroupID: group.ID, AgentProfileID: "profile-author", AgentInstanceID: "profile-author", MissionAssignmentRef: "assignment-author", MemberKind: DiscussionMemberAgent, VisibilityScope: VisibilityMission, CanPost: true, CanMention: true},
		{DiscussionGroupID: group.ID, AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker", MissionAssignmentRef: "assignment-worker", MemberKind: DiscussionMemberAgent, VisibilityScope: VisibilityMission, CanPost: true, CanMention: true},
	} {
		if err := svc.AddMember(ctx, group.ID, member); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}

	_, err = svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-author"},
		Content:           "[@Worker](mention://agent/profile-worker:mission-1) please handle this",
		MessageKind:       MessageKindRequest,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostMessage error = %v, want ErrInvalidArgument for runtime instance mention", err)
	}
}

func TestDiscussionServiceMentionDefaultsToRequiredAndDispatchesAgentMentions(t *testing.T) {
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

	msg, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "[@Worker](mention://agent/profile-agent-2) [@QA](mention://agent/profile-agent-3) please handle this",
		MessageKind:       MessageKindRequest,
	})

	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	if len(msg.Mentions) != 2 {
		t.Fatalf("expected two stored mentions, got %#v", msg.Mentions)
	}
	if len(dispatcher.items) != 2 {
		t.Fatalf("expected both agent mentions to dispatch, got %#v", dispatcher.items)
	}
	for _, item := range dispatcher.items {
		if !item.RequiredResponse || item.Status != InboxPending {
			t.Fatalf("mention dispatch must use pending required inbox item: %#v", item)
		}
	}
}

func TestDiscussionServiceRejectsNonMemberPost(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	_, err = svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "agent-outsider"},
		Content:           "I should not be here",
		MessageKind:       MessageKindRequest,
	})

	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected permission error, got %v", err)
	}
}

func TestAllMentionCreatesLowPriorityInboxWithoutTaskQueue(t *testing.T) {
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

	_, err = svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "[@All](mention://all/all) FYI",
		MessageKind:       MessageKindSystemNotice,
	})
	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	inbox, err := svc.ListInbox(ctx, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"}, InboxQuery{})
	if err != nil {
		t.Fatalf("ListInbox returned error: %v", err)
	}
	if len(inbox.Items) != 1 || inbox.Items[0].NotificationState.Priority != NotificationPriorityLow || inbox.Items[0].RequiredResponse {
		t.Fatalf("@all should create low-priority non-required notification: %#v", inbox.Items)
	}
	if table := unitSnapshot(t, ctx, svc.unit)["agent_"+"task_queue"]; len(table) != 0 {
		t.Fatalf("@all must not enqueue task queue records: %#v", table)
	}
}

func TestDiscussionServiceRejectsMissionMismatch(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if err := svc.AddMember(ctx, group.ID, testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1")); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	_, err = svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-2",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "wrong mission",
		MessageKind:       MessageKindRequest,
	})

	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid mission mismatch error, got %v", err)
	}
}
