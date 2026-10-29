package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/uow"
)

type EventCandidate struct {
	Type              missionevent.EventType
	TenantID          string
	MissionID         missioncore.MissionID
	DiscussionGroupID DiscussionGroupID
	Message           Message
	InboxItem         InboxItem
	Reason            string
	Actor             ViewerRef
	IdempotencyKey    string
}

type EventSink struct{}

func NewEventSink() *EventSink {
	return &EventSink{}
}

func (s *EventSink) Emit(ctx context.Context, tx uow.Tx, candidate EventCandidate) error {
	streamID := discussionEventStreamID(candidate)
	if streamID == "" {
		return fmt.Errorf("%w: event stream id is required", ErrInvalidArgument)
	}
	payload, err := json.Marshal(candidate.Payload())
	if err != nil {
		return fmt.Errorf("encode discussion event payload: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"mission_id":          candidate.MissionID.String(),
		"tenant_id":           candidate.tenantID(),
		"discussion_group_id": string(candidate.DiscussionGroupID),
		"source":              "mission_discussion",
		"actor_ref":           missionActorRef(candidate.Actor),
	})
	if err != nil {
		return fmt.Errorf("encode discussion event metadata: %w", err)
	}
	version, err := nextDiscussionEventStreamVersion(ctx, tx, streamID)
	if err != nil {
		return err
	}
	_, err = tx.RecordEvent(ctx, uow.EventMessage{
		StreamID:       streamID,
		StreamVersion:  version,
		EventType:      string(candidate.Type),
		EventVersion:   1,
		Payload:        payload,
		Metadata:       metadata,
		IdempotencyKey: missionevent.TenantScopedIdempotencyKey(candidate.tenantID(), candidate.IdempotencyKey),
	})
	if err != nil {
		return fmt.Errorf("record discussion event: %w", err)
	}
	return nil
}

func discussionEventStreamID(candidate EventCandidate) string {
	if !candidate.MissionID.IsZero() {
		return missionevent.MissionStreamID(candidate.tenantID(), candidate.MissionID)
	}
	return string(candidate.DiscussionGroupID)
}

func (c EventCandidate) tenantID() string {
	for _, tenantID := range []string{c.TenantID, c.Message.TenantID, c.InboxItem.TenantID} {
		if tenantID = strings.TrimSpace(tenantID); tenantID != "" {
			return tenantID
		}
	}
	return "global"
}

func (c EventCandidate) Payload() missionevent.Payload {
	switch c.Type {
	case missionevent.EventDiscussionGroupCreated:
		return missionevent.DiscussionGroupCreatedPayload{DiscussionGroupID: string(c.DiscussionGroupID)}
	case missionevent.EventMessagePosted, missionevent.EventAgentReplied:
		return missionevent.MessagePostedPayload{
			MessageID:         string(c.Message.ID),
			DiscussionGroupID: string(c.Message.DiscussionGroupID),
			AuthorID:          eventAuthorID(c.Message.Author),
			AuthorType:        string(c.Message.Author.Kind),
			Body:              c.Message.Content,
			Mentions:          agentMentions(c.Message.Mentions),
			ReplyToMessageID:  string(c.Message.ReplyToMessageID),
			Visibility:        []string{string(c.Message.Visibility.Scope)},
		}
	case missionevent.EventAgentMentioned, missionevent.EventInboxItemCreated, missionevent.EventInboxItemClosed:
		return missionevent.InboxItemPayload{
			InboxItemID: string(c.InboxItem.ID),
			OwnerID:     agentcore.AgentID(c.InboxItem.Recipient.Ref),
			MessageID:   string(c.InboxItem.MessageID),
			Reason:      c.Reason,
		}
	case missionevent.EventDecisionRecorded:
		return missionevent.DecisionRecordedPayload{DecisionID: "decision-" + string(c.Message.ID), Summary: c.Message.Summary, SourceRefs: append([]string(nil), c.Message.SourceRefs...)}
	case missionevent.EventBlockerRaised:
		return missionevent.BlockerRaisedPayload{BlockerID: "blocker-" + string(c.Message.ID), TaskID: c.Message.RelatedTaskID, RaisedBy: eventAuthorID(c.Message.Author), Summary: c.Message.Summary}
	case missionevent.EventMemoryCandidateCreated:
		candidate, _ := MemoryCandidateFromMessage(c.Message)
		return missionevent.MemoryCandidatePayload{CandidateID: candidate.ID, Summary: candidate.Summary, SourceRefs: []string{candidate.SourceRef}, Reason: string(c.Message.MessageKind)}
	default:
		return missionevent.InboxItemPayload{Reason: c.Reason}
	}
}

func eventAuthorID(author AuthorRef) agentcore.AgentID {
	return author.StableID()
}

func missionActorRef(viewer ViewerRef) missionevent.ActorRef {
	switch viewer.Kind {
	case ViewerAgent:
		return missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: viewer.Ref}
	case ViewerUser:
		return missionevent.ActorRef{Kind: missionevent.ActorKindUser, ID: viewer.Ref}
	case ViewerSystem:
		return missionevent.ActorRef{Kind: missionevent.ActorKindSystem, ID: viewer.Ref}
	default:
		return missionevent.ActorRef{Kind: missionevent.ActorKindUser, ID: viewer.Ref}
	}
}

func nextDiscussionEventStreamVersion(ctx context.Context, tx uow.Tx, streamID string) (int, error) {
	events, err := tx.EventStore().Load(ctx, streamID)
	if err != nil {
		return 0, err
	}
	return len(events) + 1, nil
}

func agentMentions(mentions []Mention) []agentcore.AgentID {
	out := make([]agentcore.AgentID, 0, len(mentions))
	for _, mention := range mentions {
		if mention.Target.Kind == MentionTargetAgent {
			out = append(out, agentcore.AgentID(mention.Target.Ref))
		}
	}
	return out
}
