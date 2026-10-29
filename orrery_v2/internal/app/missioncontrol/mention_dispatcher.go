package missioncontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	missiondiscussion "orrery_v2/internal/mission/discussion"
	"orrery_v2/internal/platform/execution"
)

const MentionDispatchTopic = "mission.discussion.mention.agent"

type MentionDispatchQueue interface {
	Submit(ctx context.Context, message execution.Message) (string, error)
}

type MentionExecutionDispatcher struct {
	queue MentionDispatchQueue
}

type MentionDispatchPayload struct {
	TenantID          string `json:"tenant_id"`
	InboxItemID       string `json:"inbox_item_id"`
	MissionID         string `json:"mission_id"`
	DiscussionGroupID string `json:"discussion_group_id"`
	MessageID         string `json:"message_id"`
	AgentID           string `json:"agent_id"`
	RequiredResponse  bool   `json:"required_response"`
}

func NewMentionExecutionDispatcher(queue MentionDispatchQueue) *MentionExecutionDispatcher {
	return &MentionExecutionDispatcher{queue: queue}
}

func (d *MentionExecutionDispatcher) DispatchMention(ctx context.Context, item missiondiscussion.InboxItem) error {
	if d == nil || d.queue == nil {
		return ErrServiceDependency
	}
	if item.Recipient.Kind != missiondiscussion.ViewerAgent || strings.TrimSpace(item.Recipient.Ref) == "" {
		return nil
	}
	if !item.RequiredResponse || item.Status != missiondiscussion.InboxPending {
		return nil
	}
	payload, err := json.Marshal(MentionDispatchPayload{
		TenantID:          missionTenantID(item.TenantID),
		InboxItemID:       string(item.ID),
		MissionID:         item.MissionID.String(),
		DiscussionGroupID: string(item.DiscussionGroupID),
		MessageID:         string(item.MessageID),
		AgentID:           strings.TrimSpace(item.Recipient.Ref),
		RequiredResponse:  item.RequiredResponse,
	})
	if err != nil {
		return fmt.Errorf("encode mention dispatch payload: %w", err)
	}
	_, err = d.queue.Submit(ctx, execution.Message{
		Topic:       MentionDispatchTopic,
		Payload:     payload,
		Priority:    10,
		MaxAttempts: 3,
	})
	return err
}
