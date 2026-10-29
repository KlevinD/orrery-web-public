package discussion

import (
	"context"
	"testing"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func newTestDiscussionService(t *testing.T) (*Service, *uow.UnitOfWork) {
	t.Helper()
	return newTestDiscussionServiceWithDispatcher(t, nil)
}

func newTestDiscussionServiceWithDispatcher(t *testing.T, dispatcher MentionDispatcher) (*Service, *uow.UnitOfWork) {
	t.Helper()
	env := testkit.New(t)
	t.Cleanup(env.Close)
	env.Clock.Advance(48 * time.Hour)
	svc := NewService(ServiceOptions{UnitOfWork: env.UnitOfWork, Clock: env.Clock, IDGenerator: env.IDGenerator, Logger: env.Logger, Dispatcher: dispatcher})
	return svc, env.UnitOfWork
}

type capturingMentionDispatcher struct {
	items []InboxItem
}

func (d *capturingMentionDispatcher) DispatchMention(_ context.Context, item InboxItem) error {
	d.items = append(d.items, item)
	return nil
}

func loadDiscussionEvents(t *testing.T, ctx context.Context, unit *uow.UnitOfWork, streamID string) []string {
	t.Helper()
	var out []string
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		events, err := tx.EventStore().Load(ctx, streamID)
		if err != nil {
			return err
		}
		for _, evt := range events {
			out = append(out, evt.EventType)
		}
		return nil
	}); err != nil {
		t.Fatalf("load events: %v", err)
	}
	return out
}

func loadDiscussionInboxPayloads(t *testing.T, ctx context.Context, unit *uow.UnitOfWork, missionID missioncore.MissionID) []missionevent.InboxItemPayload {
	t.Helper()
	var out []missionevent.InboxItemPayload
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		events, err := missionevent.NewStoreReader(tx.EventStore()).LoadMission(ctx, missionID)
		if err != nil {
			return err
		}
		for _, evt := range events {
			switch evt.EventType {
			case missionevent.EventAgentMentioned, missionevent.EventInboxItemCreated:
				payload, ok := evt.Payload.(missionevent.InboxItemPayload)
				if !ok {
					t.Fatalf("payload type = %T, want InboxItemPayload", evt.Payload)
				}
				out = append(out, payload)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("load inbox payloads: %v", err)
	}
	return out
}

func loadDiscussionMessagePayloads(t *testing.T, ctx context.Context, unit *uow.UnitOfWork, missionID missioncore.MissionID) []missionevent.MessagePostedPayload {
	t.Helper()
	var out []missionevent.MessagePostedPayload
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		events, err := missionevent.NewStoreReader(tx.EventStore()).LoadMission(ctx, missionID)
		if err != nil {
			return err
		}
		for _, evt := range events {
			switch evt.EventType {
			case missionevent.EventMessagePosted, missionevent.EventAgentReplied:
				payload, ok := evt.Payload.(missionevent.MessagePostedPayload)
				if !ok {
					t.Fatalf("payload type = %T, want MessagePostedPayload", evt.Payload)
				}
				out = append(out, payload)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("load message payloads: %v", err)
	}
	return out
}

func loadDiscussionMessageMentionOwners(t *testing.T, ctx context.Context, unit *uow.UnitOfWork, missionID missioncore.MissionID) []string {
	t.Helper()
	var out []string
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		events, err := missionevent.NewStoreReader(tx.EventStore()).LoadMission(ctx, missionID)
		if err != nil {
			return err
		}
		for _, evt := range events {
			if evt.EventType != missionevent.EventMessagePosted {
				continue
			}
			payload, ok := evt.Payload.(missionevent.MessagePostedPayload)
			if !ok {
				t.Fatalf("payload type = %T, want MessagePostedPayload", evt.Payload)
			}
			for _, mention := range payload.Mentions {
				out = append(out, mention.String())
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("load message mentions: %v", err)
	}
	return out
}

func hasEvent(events []string, eventType missionevent.EventType) bool {
	for _, got := range events {
		if got == string(eventType) {
			return true
		}
	}
	return false
}

func eventTypes(events []string) []string {
	return append([]string(nil), events...)
}

func unitSnapshot(t *testing.T, ctx context.Context, unit *uow.UnitOfWork) map[string]map[string]db.Record {
	t.Helper()
	var snapshot map[string]map[string]db.Record
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		snapshot = tx.Snapshot()
		return nil
	}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snapshot
}
