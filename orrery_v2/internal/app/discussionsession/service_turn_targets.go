package discussionsession

import "strings"

type discussionTurnTargetMode string

const (
	discussionTurnTargetGroup                discussionTurnTargetMode = "group"
	discussionTurnTargetDirectAgent          discussionTurnTargetMode = "direct_agent"
	discussionTurnTargetDirectPassive        discussionTurnTargetMode = "direct_passive"
	discussionTurnTargetMention              discussionTurnTargetMode = "mention"
	discussionTurnTargetApprovalContinuation discussionTurnTargetMode = "approval_continuation"
)

type discussionTurnTargetPlan struct {
	Mode    discussionTurnTargetMode
	Members []ConversationMemberRecord
}

func resolveDiscussionTurnTargets(current ConversationRecordView, message MessageRecord, defaultAgentID string) discussionTurnTargetPlan {
	if strings.TrimSpace(current.ConversationKind) == "direct" {
		if target, ok := directConversationTargetMember(current); ok {
			if strings.TrimSpace(target.MemberKind) == "agent" {
				return discussionTurnTargetPlan{Mode: discussionTurnTargetDirectAgent, Members: []ConversationMemberRecord{target}}
			}
			return discussionTurnTargetPlan{Mode: discussionTurnTargetDirectPassive, Members: []ConversationMemberRecord{target}}
		}
		return discussionTurnTargetPlan{Mode: discussionTurnTargetDirectPassive}
	}
	if strings.TrimSpace(message.Role) == "user" {
		if targets := discussionAgentMentionTargets(message, current.Members); len(targets) > 0 {
			return discussionTurnTargetPlan{Mode: discussionTurnTargetMention, Members: targets}
		}
	}
	if agentID := strings.TrimSpace(defaultAgentID); agentID != "" {
		if member, ok := conversationAgentMemberByID(current.Members, agentID); ok {
			return discussionTurnTargetPlan{Mode: discussionTurnTargetGroup, Members: []ConversationMemberRecord{member}}
		}
	}
	return discussionTurnTargetPlan{Mode: discussionTurnTargetGroup}
}

func (plan discussionTurnTargetPlan) requiresDiscussionAgent() bool {
	return plan.Mode == discussionTurnTargetGroup
}

func (plan discussionTurnTargetPlan) requiresMentionResponder() bool {
	return plan.Mode == discussionTurnTargetDirectAgent || plan.Mode == discussionTurnTargetMention || plan.Mode == discussionTurnTargetApprovalContinuation
}

func conversationAgentMemberByID(members []ConversationMemberRecord, agentID string) (ConversationMemberRecord, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ConversationMemberRecord{}, false
	}
	for _, member := range members {
		if strings.TrimSpace(member.MemberKind) == "agent" && strings.TrimSpace(member.MemberID) == agentID {
			return member, true
		}
	}
	return ConversationMemberRecord{}, false
}

func resolveRuntimeApprovalTurnTargets(current ConversationRecordView, blockedRun RunRecord) discussionTurnTargetPlan {
	if member, ok := conversationAgentMemberByID(current.Members, blockedRun.AgentID); ok {
		return discussionTurnTargetPlan{Mode: discussionTurnTargetApprovalContinuation, Members: []ConversationMemberRecord{member}}
	}
	return discussionTurnTargetPlan{Mode: discussionTurnTargetApprovalContinuation}
}
