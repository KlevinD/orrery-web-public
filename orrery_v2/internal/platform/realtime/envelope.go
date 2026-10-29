package realtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	AggregateConversation = "conversation"
	AggregateMission      = "mission"
	AggregateUser         = "user"
	AggregateRun          = "run"
	AggregateProject      = "project"
)

const (
	EventDiscussionSnapshotInvalidated = "discussion.snapshot.invalidated"
	EventDiscussionMessageCreated      = "discussion.message.created"
	EventDiscussionMessageCompleted    = "discussion.message.completed"
	EventDiscussionRunStarted          = "discussion.run.started"
	EventDiscussionRunCompleted        = "discussion.run.completed"
	EventDiscussionRunFailed           = "discussion.run.failed"
	EventDiscussionToolCall            = "discussion.tool.call"
	EventDiscussionToolResult          = "discussion.tool.result"
	EventMissionSnapshotInvalidated    = "mission.snapshot.invalidated"
	EventUserDiscussionListInvalidated = "user.discussion_list.invalidated"
	EventProjectExecutionUpdated       = "development_project.execution.updated"
)

type Envelope struct {
	EventID       string          `json:"event_id"`
	Type          string          `json:"type"`
	Channel       string          `json:"channel"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Version       int64           `json:"version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}

func (e Envelope) Validate() error {
	if strings.TrimSpace(e.EventID) == "" {
		return fmt.Errorf("realtime envelope event id is required")
	}
	if strings.TrimSpace(e.Type) == "" {
		return fmt.Errorf("realtime envelope type is required")
	}
	if strings.TrimSpace(e.Channel) == "" {
		return fmt.Errorf("realtime envelope channel is required")
	}
	if strings.TrimSpace(e.AggregateType) == "" {
		return fmt.Errorf("realtime envelope aggregate type is required")
	}
	if strings.TrimSpace(e.AggregateID) == "" {
		return fmt.Errorf("realtime envelope aggregate id is required")
	}
	if e.Version <= 0 {
		return fmt.Errorf("realtime envelope version must be positive")
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("realtime envelope occurred_at is required")
	}
	if len(e.Payload) == 0 || !json.Valid(e.Payload) {
		return fmt.Errorf("realtime envelope payload must be valid JSON")
	}
	return nil
}

func (e Envelope) MarshalPayload() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal realtime envelope: %w", err)
	}
	return payload, nil
}
