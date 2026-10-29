package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/discussionsession"
	platformrealtime "orrery_v2/internal/platform/realtime"
)

type productionDiscussionRealtimePublisher struct {
	store *platformrealtime.Store
}

func (p productionDiscussionRealtimePublisher) AppendRealtimeEvent(ctx context.Context, event discussionsession.RealtimeEvent) error {
	if p.store == nil {
		return fmt.Errorf("production realtime outbox store is not configured")
	}
	if err := validateDiscussionRealtimeChannel(event.Channel); err != nil {
		return err
	}
	_, err := p.store.Append(ctx, platformrealtime.AppendEvent{
		EventID:       event.EventID,
		Type:          event.Type,
		Channel:       event.Channel,
		AggregateType: event.AggregateType,
		AggregateID:   event.AggregateID,
		Payload:       event.Payload,
	})
	return err
}

func validateDiscussionRealtimeChannel(channel string) error {
	channel = strings.TrimSpace(channel)
	switch {
	case strings.HasPrefix(channel, "conversation:"):
		_, err := platformrealtime.ConversationChannel(strings.TrimPrefix(channel, "conversation:"))
		return err
	case strings.HasPrefix(channel, "user:"):
		_, err := platformrealtime.UserChannel(strings.TrimPrefix(channel, "user:"))
		return err
	default:
		return fmt.Errorf("unsupported discussion realtime channel %q", channel)
	}
}

var _ discussionsession.RealtimePublisher = productionDiscussionRealtimePublisher{}
