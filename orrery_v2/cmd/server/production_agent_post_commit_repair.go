package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/outbox"
)

const productionAgentPostCommitRepairTopic = "agent.runtime.post_commit_repair"

type productionAgentPostCommitRepairPublisher interface {
	Publish(context.Context, platform.OutboxMessage) (string, error)
}

type productionAgentPostCommitRepair struct {
	publisher productionAgentPostCommitRepairPublisher
	notifier  agentcore.Notifier
}

type productionAgentPostCommitRepairPayload struct {
	TenantID          string `json:"tenant_id"`
	RunID             string `json:"run_id"`
	MissionID         string `json:"mission_id,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	AgentID           string `json:"agent_id"`
	ConversationID    string `json:"conversation_id,omitempty"`
	DiscussionGroupID string `json:"discussion_group_id,omitempty"`
	Summary           string `json:"summary"`
}

func (r *productionAgentPostCommitRepair) Schedule(ctx context.Context, input agentcore.AgentInput, result agentcore.AgentResult) error {
	if r == nil || r.publisher == nil || r.notifier == nil {
		return fmt.Errorf("Agent post-commit repair is not configured")
	}
	payload := productionAgentPostCommitRepairPayload{
		TenantID: firstNonEmptyText(input.TenantID, "global"), RunID: result.RunID.String(),
		MissionID: firstNonEmptyText(result.MissionID.String(), input.Task.MissionID),
		TaskID:    firstNonEmptyText(result.TaskID.String(), input.Task.ID), AgentID: result.AgentID.String(),
		ConversationID:    strings.TrimSpace(input.ContextPack.ConversationID),
		DiscussionGroupID: strings.TrimSpace(string(input.ContextPack.DiscussionGroupID)),
		Summary:           strings.TrimSpace(result.Summary.Text),
	}
	if payload.RunID == "" || payload.AgentID == "" || payload.Summary == "" {
		return fmt.Errorf("Agent post-commit repair requires run, agent, and summary")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Agent post-commit repair: %w", err)
	}
	_, err = r.publisher.Publish(ctx, platform.OutboxMessage{
		Topic: productionAgentPostCommitRepairTopic, Payload: raw,
		IdempotencyKey: "agent-post-commit-repair:" + payload.RunID, MaxAttempts: 20,
	})
	if err != nil {
		return fmt.Errorf("schedule Agent post-commit repair: %w", err)
	}
	return nil
}

func (r *productionAgentPostCommitRepair) HandleOutbox(ctx context.Context, job outbox.Job) error {
	if r == nil || r.notifier == nil {
		return fmt.Errorf("Agent post-commit repair notifier is not configured")
	}
	if job.Topic != productionAgentPostCommitRepairTopic {
		return fmt.Errorf("unsupported Agent post-commit repair topic %q", job.Topic)
	}
	var payload productionAgentPostCommitRepairPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode Agent post-commit repair: %w", err)
	}
	if strings.TrimSpace(payload.RunID) == "" || strings.TrimSpace(payload.AgentID) == "" || strings.TrimSpace(payload.Summary) == "" {
		return fmt.Errorf("Agent post-commit repair payload is incomplete")
	}
	input := agentcore.AgentInput{
		TenantID: firstNonEmptyText(payload.TenantID, "global"), RunID: agentcore.AgentRunID(payload.RunID),
		Task: agentcore.TaskInput{MissionID: payload.MissionID, ID: payload.TaskID},
		ContextPack: agentcore.ContextPack{
			ConversationID:    payload.ConversationID,
			DiscussionGroupID: agentcore.DiscussionGroupID(payload.DiscussionGroupID),
		},
	}
	result := agentcore.AgentResult{
		MissionID: missioncore.MissionID(payload.MissionID), TaskID: missioncore.TaskID(payload.TaskID),
		AgentID: agentcore.AgentID(payload.AgentID), RunID: agentcore.AgentRunID(payload.RunID),
		Summary: agentcore.Summary{Text: payload.Summary},
	}
	return r.notifier.Notify(ctx, input, result)
}
