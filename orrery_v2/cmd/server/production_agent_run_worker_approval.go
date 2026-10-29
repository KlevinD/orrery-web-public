package main

import (
	"context"
	"fmt"
	"strings"

	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

type missionRuntimeApprovalConversationResolver interface {
	ResolveMissionRuntimeApprovalConversation(context.Context, string, missioncore.MissionID) (string, error)
}

type missionCatalogRuntimeApprovalConversationResolver struct {
	catalog *missioncontrol.DBProjectionRepository
}

func (resolver missionCatalogRuntimeApprovalConversationResolver) ResolveMissionRuntimeApprovalConversation(ctx context.Context, tenantID string, missionID missioncore.MissionID) (string, error) {
	if resolver.catalog == nil {
		return "", fmt.Errorf("mission catalog is not configured")
	}
	if strings.TrimSpace(missionID.String()) == "" {
		return "", fmt.Errorf("mission id is required")
	}
	model, err := resolver.catalog.GetMissionControlForTenant(ctx, tenantID, missionID)
	if err != nil {
		return "", fmt.Errorf("load mission %s approval conversation: %w", missionID, err)
	}
	conversationID := strings.TrimSpace(model.Discussion.ConversationID)
	if conversationID == "" {
		return "", fmt.Errorf("mission %s has no canonical discussion conversation", missionID)
	}
	return conversationID, nil
}

func (w productionAgentRunWorker) runtimeApprovalContext(ctx context.Context, run *runtimedaemon.AgentRun) (context.Context, error) {
	if run == nil {
		return ctx, fmt.Errorf("resolve mission runtime approval context: agent run is required")
	}
	providerSpecific := run.Request.RuntimeOptions.ProviderSpecific
	conversationID := firstNonBlank(
		strings.TrimSpace(providerSpecific["conversation_id"]),
		strings.TrimSpace(run.Request.ContextPack.ConversationID),
	)
	scheduled := isSchedulerAgentRun(*run)
	if conversationID == "" && !scheduled && w.approvalConversations != nil {
		resolved, err := w.approvalConversations.ResolveMissionRuntimeApprovalConversation(ctx, run.Request.TenantID, missionIDFromRun(*run))
		if err != nil {
			return ctx, fmt.Errorf("resolve mission runtime approval context: %w", err)
		}
		conversationID = strings.TrimSpace(resolved)
		if conversationID == "" {
			return ctx, fmt.Errorf("resolve mission runtime approval context: conversation id is empty")
		}
	}
	if conversationID == "" {
		return ctx, nil
	}
	if !scheduled {
		run.Request.ContextPack.ConversationID = conversationID
	}
	runID := firstNonBlank(run.RunID.String(), run.Request.RunID.String())
	if runID == "" {
		return ctx, fmt.Errorf("resolve mission runtime approval context: run id is empty")
	}
	agentID := firstNonBlank(run.AgentID.String(), run.Request.AgentID.String())
	runtimeKind := firstNonBlank(strings.TrimSpace(run.RuntimeID), strings.TrimSpace(providerSpecific["runtime_kind"]))
	return discussionsession.ContextWithRuntimeApproval(ctx, discussionsession.RuntimeApprovalContext{
		ConversationID:   conversationID,
		RunID:            runID,
		AgentID:          agentID,
		LogicalAgentID:   strings.TrimSpace(providerSpecific["logical_agent_id"]),
		ConversationKind: strings.TrimSpace(providerSpecific["conversation_kind"]),
		RuntimeKind:      runtimeKind,
		AssignedTo:       strings.TrimSpace(providerSpecific["approval_assigned_to"]),
	}), nil
}
