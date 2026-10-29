package projection_test

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

func TestProjectorClosesInboxAndClearsPendingMention(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Coordinate"}, at),
		event(2, "evt-discussion", missionevent.EventDiscussionGroupCreated, missionevent.DiscussionGroupCreatedPayload{DiscussionGroupID: "discussion-1"}, at.Add(time.Minute)),
		event(3, "evt-message", missionevent.EventMessagePosted, missionevent.MessagePostedPayload{MessageID: "message-1", DiscussionGroupID: "discussion-1", AuthorID: "agent-1", Body: "Need reply", Mentions: []agentcore.AgentID{"agent-2"}}, at.Add(2*time.Minute)),
		event(4, "evt-inbox", missionevent.EventInboxItemCreated, missionevent.InboxItemPayload{InboxItemID: "inbox-1", OwnerID: "agent-2", MessageID: "message-1", Reason: "mention"}, at.Add(3*time.Minute)),
		event(5, "evt-closed", missionevent.EventInboxItemClosed, missionevent.InboxItemPayload{InboxItemID: "inbox-1", OwnerID: "agent-2", MessageID: "message-1", Reason: "responded"}, at.Add(4*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if model.InboxSummary.OpenCount != 0 || len(model.InboxSummary.Items) != 1 || model.InboxSummary.Items[0].Status != "closed" {
		t.Fatalf("inbox did not close: %+v", model.InboxSummary)
	}
	if len(model.Discussion.PendingMentions) != 0 {
		t.Fatalf("pending mention should be cleared after inbox close: %+v", model.Discussion.PendingMentions)
	}
}
