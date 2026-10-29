package discussion

import (
	"time"
)

func (s *Service) materializeMentions(targets []MentionTarget, msg Message, members []DiscussionMember, cmd PostMessageCommand, now time.Time) ([]Mention, []InboxItem, error) {
	var mentions []Mention
	var inboxItems []InboxItem
	for _, target := range targets {
		target = normalizeMentionTarget(target, members)
		recipients := recipientsForTarget(target, members, cmd.Author)
		if err := validateMentionTargetRecipients(target, recipients); err != nil {
			return nil, nil, err
		}
		mentionID, err := s.newID("mention")
		if err != nil {
			return nil, nil, err
		}
		mention := Mention{ID: MentionID(mentionID), MessageID: msg.ID, Target: target, CreatedBy: msg.Author.StableID(), CreatedAt: now}
		mentions = append(mentions, mention)
		for _, recipient := range recipients {
			inboxID, err := s.newID("inbox")
			if err != nil {
				return nil, nil, err
			}
			required := cmd.RequiredResponse && !target.IsAll()
			priority := NotificationPriorityNormal
			if target.IsAll() {
				priority = NotificationPriorityLow
			}
			inboxItems = append(inboxItems, InboxItem{
				ID:                InboxItemID(inboxID),
				TenantID:          msg.TenantID,
				MissionID:         msg.MissionID,
				DiscussionGroupID: msg.DiscussionGroupID,
				MessageID:         msg.ID,
				MentionID:         mention.ID,
				Recipient:         recipient,
				RequiredResponse:  required,
				Status:            InboxPending,
				DueAt:             cmd.DueAt,
				NotificationState: NotificationState{Priority: priority},
				CreatedAt:         now,
			})
		}
	}
	return mentions, inboxItems, nil
}

func normalizeMentionTarget(target MentionTarget, members []DiscussionMember) MentionTarget {
	if target.Kind != MentionTargetAgent {
		return target
	}
	for _, member := range members {
		if !member.Active() || !memberMatchesAgent(member, target.Ref) {
			continue
		}
		if ref := discussionMemberStableAgentRef(member); ref != "" {
			target.Ref = ref
		}
		return target
	}
	return target
}

func recipientsForTarget(target MentionTarget, members []DiscussionMember, author ViewerRef) []ViewerRef {
	var out []ViewerRef
	seen := map[string]bool{}
	add := func(member DiscussionMember) {
		if !member.Active() || memberMatchesViewer(member, author) {
			return
		}
		recipient := ViewerRef{Kind: ViewerAgent, Ref: discussionMemberStableAgentRef(member)}
		if recipient.Ref == "" {
			return
		}
		if seen[recipient.key()] {
			return
		}
		seen[recipient.key()] = true
		out = append(out, recipient)
	}
	for _, member := range members {
		switch target.Kind {
		case MentionTargetAgent:
			if memberMatchesAgent(member, target.Ref) {
				add(member)
			}
		case MentionTargetRole:
			if string(member.MissionRole) == target.Role || string(member.MissionRole) == target.Ref {
				add(member)
			}
		case MentionTargetAll, MentionTargetTeam:
			add(member)
		}
	}
	if target.Kind == MentionTargetUser && target.Ref != "" && target.Ref != author.Ref {
		out = append(out, ViewerRef{Kind: ViewerUser, Ref: target.Ref})
	}
	return out
}

func validateMentionTargetRecipients(target MentionTarget, recipients []ViewerRef) error {
	if target.Kind != MentionTargetAgent || len(recipients) > 0 {
		return nil
	}
	return ErrInvalidArgument
}

func discussionMemberStableAgentRef(member DiscussionMember) string {
	if member.AgentProfileID != "" {
		return member.AgentProfileID.String()
	}
	return ""
}
