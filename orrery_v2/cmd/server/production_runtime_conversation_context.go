package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
)

const (
	runtimeConversationDigestMessageLimit = 12
	runtimeConversationDigestMessageRunes = 320
	runtimeConversationDigestTotalRunes   = 4200
)

type productionRuntimeConversationContextProvider struct {
	repository *discussionsession.Repository
}

func (p productionRuntimeConversationContextProvider) RuntimeConversationContext(ctx context.Context, request missioncontrol.RuntimeConversationContextRequest) (missioncontrol.RuntimeConversationContext, error) {
	if p.repository == nil {
		return missioncontrol.RuntimeConversationContext{}, missioncontrol.ErrServiceDependency
	}
	tenantID := strings.TrimSpace(request.TenantID)
	missionID := strings.TrimSpace(request.MissionID)
	conversationID := strings.TrimSpace(request.ConversationID)
	agentID := strings.TrimSpace(request.AgentID.String())
	if tenantID == "" || missionID == "" || conversationID == "" || agentID == "" {
		return missioncontrol.RuntimeConversationContext{}, missioncontrol.ErrInvalidRequest
	}
	view, err := p.repository.GetConversation(ctx, conversationID)
	if err != nil {
		return missioncontrol.RuntimeConversationContext{}, missioncontrol.ErrServiceDependency
	}
	// Legacy Mission conversations predate tenant persistence and therefore
	// have a blank tenant. Treat those records as global, while keeping every
	// named tenant strictly isolated.
	conversationTenantID := strings.TrimSpace(view.TenantID)
	if conversationTenantID == "" {
		conversationTenantID = "global"
	}
	if conversationTenantID != tenantID || !conversationLinksMission(view.LinkedMissions, missionID) || !conversationHasAgentMember(view.Members, agentID) {
		return missioncontrol.RuntimeConversationContext{}, missioncontrol.ErrProjectionNotFound
	}
	summary := runtimeConversationDigestSummary(view)
	digestID := runtimeConversationDigestID(view)
	return missioncontrol.RuntimeConversationContext{
		ConversationID: conversationID,
		Digest: agentcore.DiscussionDigestRef{
			ID: agentcore.DiscussionDigestID(digestID), SummaryRef: summary,
		},
	}, nil
}

func conversationHasAgentMember(members []discussionsession.ConversationMemberRecord, agentID string) bool {
	for _, member := range members {
		if strings.TrimSpace(member.MemberKind) == "agent" && strings.TrimSpace(member.MemberID) == agentID {
			return true
		}
	}
	return false
}

func conversationLinksMission(links []discussionsession.LinkedMissionRecord, missionID string) bool {
	for _, link := range links {
		if strings.TrimSpace(link.MissionID) == missionID {
			return true
		}
	}
	return false
}

func runtimeConversationDigestID(view discussionsession.ConversationRecordView) string {
	var source strings.Builder
	fmt.Fprintf(&source, "%s\x00%d", strings.TrimSpace(view.ConversationID), view.Revision)
	for _, message := range view.Messages {
		fmt.Fprintf(&source, "\x00%s", strings.TrimSpace(message.MessageID))
	}
	sum := sha256.Sum256([]byte(source.String()))
	return fmt.Sprintf("conversation-digest-%x", sum[:12])
}

func runtimeConversationDigestSummary(view discussionsession.ConversationRecordView) string {
	lines := []string{fmt.Sprintf("Canonical conversation %s (revision %d): %s", strings.TrimSpace(view.ConversationID), view.Revision, strings.TrimSpace(view.Title))}
	start := len(view.Messages) - runtimeConversationDigestMessageLimit
	if start < 0 {
		start = 0
	}
	for _, message := range view.Messages[start:] {
		content := truncateConversationDigestText(message.Content, runtimeConversationDigestMessageRunes)
		if content == "" {
			continue
		}
		author := strings.TrimSpace(message.AuthorName)
		if author == "" {
			author = strings.TrimSpace(message.AuthorID)
		}
		if author == "" {
			author = strings.TrimSpace(message.Role)
		}
		lines = append(lines, author+": "+content)
	}
	return truncateConversationDigestText(strings.Join(lines, "\n"), runtimeConversationDigestTotalRunes)
}

func truncateConversationDigestText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

var _ missioncontrol.RuntimeConversationContextProvider = productionRuntimeConversationContextProvider{}
