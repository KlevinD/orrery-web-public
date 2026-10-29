package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/outputpolicy"
	"orrery_v2/internal/app/discussionsession"
	missioncore "orrery_v2/internal/mission/core"
	missiondiscussion "orrery_v2/internal/mission/discussion"
)

type canonicalAgentResultNoticePort interface {
	AppendNotice(context.Context, discussionsession.ViewerRef, discussionsession.AppendNoticeCommand) (discussionsession.ConversationDTO, error)
	AppendNoticeWithWorkspace(context.Context, discussionsession.ViewerRef, discussionsession.AppendNoticeCommand, discussionsession.PreparedAgentRunWorkspace) (discussionsession.ConversationDTO, error)
	PrepareAgentRunWorkspace(context.Context, string, string) (discussionsession.PreparedAgentRunWorkspace, error)
}

type productionAgentResultWorkspaceContextKey struct{}

func contextWithProductionAgentResultWorkspace(ctx context.Context, workspace discussionsession.PreparedAgentRunWorkspace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, productionAgentResultWorkspaceContextKey{}, workspace)
}

func productionAgentResultWorkspaceFromContext(ctx context.Context) (discussionsession.PreparedAgentRunWorkspace, bool) {
	if ctx == nil {
		return discussionsession.PreparedAgentRunWorkspace{}, false
	}
	workspace, ok := ctx.Value(productionAgentResultWorkspaceContextKey{}).(discussionsession.PreparedAgentRunWorkspace)
	return workspace, ok && strings.TrimSpace(workspace.ConversationID) != "" && strings.TrimSpace(workspace.WorkDir) != ""
}

type productionAgentResultNotifier struct {
	mu        sync.RWMutex
	canonical canonicalAgentResultNoticePort
	mission   *missiondiscussion.Service
}

func (n *productionAgentResultNotifier) SetCanonical(port canonicalAgentResultNoticePort) {
	if n == nil {
		return
	}
	n.mu.Lock()
	n.canonical = port
	n.mu.Unlock()
}

func (n *productionAgentResultNotifier) PrepareAgentRunWorkspace(ctx context.Context, conversationID, agentID string) (discussionsession.PreparedAgentRunWorkspace, error) {
	if n == nil {
		return discussionsession.PreparedAgentRunWorkspace{}, fmt.Errorf("agent result notifier is not configured")
	}
	n.mu.RLock()
	canonical := n.canonical
	n.mu.RUnlock()
	if canonical == nil {
		return discussionsession.PreparedAgentRunWorkspace{}, fmt.Errorf("canonical conversation notifier is not ready")
	}
	return canonical.PrepareAgentRunWorkspace(ctx, conversationID, agentID)
}

func (n *productionAgentResultNotifier) Notify(ctx context.Context, input agentcore.AgentInput, result agentcore.AgentResult) error {
	if n == nil {
		return fmt.Errorf("agent result notifier is not configured")
	}
	message := outputpolicy.PublicText(result.Summary.Text, "\u4efb\u52a1\u5df2\u5b8c\u6210\uff0c\u7ed3\u679c\u5df2\u8bb0\u5f55\u5230 Orrery\u3002")
	if message == "" {
		return fmt.Errorf("agent result notification requires a summary")
	}
	tenantID := firstNonEmptyText(input.TenantID, "global")
	missionID := firstNonEmptyText(result.MissionID.String(), input.Task.MissionID)
	taskID := firstNonEmptyText(result.TaskID.String(), input.Task.ID)
	if conversationID := strings.TrimSpace(input.ContextPack.ConversationID); conversationID != "" {
		n.mu.RLock()
		canonical := n.canonical
		n.mu.RUnlock()
		if canonical == nil {
			return fmt.Errorf("canonical conversation notifier is not ready")
		}
		viewer := discussionsession.ViewerRef{
			ViewerID: result.AgentID.String(), ViewerKind: discussionsession.ViewerKindAgent, TenantID: tenantID,
		}
		command := discussionsession.AppendNoticeCommand{
			ConversationID: conversationID, MissionID: missionID, MissionTaskID: taskID,
			Content: message, IdempotencyKey: "agent-result-notice:" + result.RunID.String(),
			Metadata: map[string]string{"kind": "agent_result", "agent_id": result.AgentID.String(), "run_id": result.RunID.String()},
		}
		if workspace, ok := productionAgentResultWorkspaceFromContext(ctx); ok {
			if !strings.EqualFold(strings.TrimSpace(workspace.ConversationID), conversationID) || !strings.EqualFold(strings.TrimSpace(workspace.AgentID), result.AgentID.String()) {
				return fmt.Errorf("agent result workspace does not match the canonical result notice")
			}
			_, err := canonical.AppendNoticeWithWorkspace(ctx, viewer, command, workspace)
			return err
		}
		_, err := canonical.AppendNotice(ctx, viewer, command)
		return err
	}
	if groupID := strings.TrimSpace(string(input.ContextPack.DiscussionGroupID)); groupID != "" {
		if n.mission == nil {
			return fmt.Errorf("Mission discussion notifier is not configured")
		}
		_, err := n.mission.PostMessage(ctx, missiondiscussion.PostMessageCommand{
			TenantID: tenantID, MissionID: missioncore.MissionID(missionID), DiscussionGroupID: missiondiscussion.DiscussionGroupID(groupID),
			Author:  missiondiscussion.ViewerRef{Kind: missiondiscussion.ViewerAgent, Ref: result.AgentID.String()},
			Content: message, Summary: truncateConversationDigestText(message, 320), MessageKind: missiondiscussion.MessageKindSummary,
			Visibility: missiondiscussion.MessageVisibility{Scope: missiondiscussion.VisibilityMission}, RelatedTaskID: missioncore.TaskID(taskID),
			SourceRefs: []string{"agent-run:" + result.RunID.String()}, IdempotencyKey: "agent-result-notice:" + result.RunID.String(),
		})
		return err
	}
	// Scheduler runs have no Mission discussion surface; their durable run and
	// scheduler lifecycle records are the intended notification destination.
	return nil
}

var _ agentcore.Notifier = (*productionAgentResultNotifier)(nil)
