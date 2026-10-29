package discussion

import (
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
)

type ViewerKind string

const (
	ViewerAgent  ViewerKind = "agent"
	ViewerUser   ViewerKind = "user"
	ViewerSystem ViewerKind = "system"
)

type ViewerRef struct {
	Kind ViewerKind
	Ref  string
}

type MessagePolicy struct{}

func NewMessagePolicy() MessagePolicy {
	return MessagePolicy{}
}

func (p MessagePolicy) CanPost(group DiscussionGroup, members []DiscussionMember, viewer ViewerRef) error {
	if group.Status != DiscussionGroupActive {
		return fmt.Errorf("%w: discussion group is not active", ErrPermissionDenied)
	}
	if viewer.Kind == ViewerSystem && viewer.Ref != "" {
		return nil
	}
	member, ok := findPostingMember(members, viewer)
	if !ok {
		return fmt.Errorf("%w: author is not a discussion member", ErrPermissionDenied)
	}
	if !member.CanPost {
		return fmt.Errorf("%w: member cannot post", ErrPermissionDenied)
	}
	return nil
}

func (p MessagePolicy) CanMention(members []DiscussionMember, viewer ViewerRef) error {
	if viewer.Kind == ViewerSystem && viewer.Ref != "" {
		return nil
	}
	member, ok := findPostingMember(members, viewer)
	if !ok {
		return fmt.Errorf("%w: author is not a discussion member", ErrPermissionDenied)
	}
	if !member.CanMention {
		return fmt.Errorf("%w: member cannot mention", ErrPermissionDenied)
	}
	return nil
}

func findPostingMember(members []DiscussionMember, viewer ViewerRef) (DiscussionMember, bool) {
	if viewer.Ref == "" {
		return DiscussionMember{}, false
	}
	for _, member := range members {
		switch viewer.Kind {
		case ViewerAgent:
			if memberMatchesViewer(member, viewer) && member.Active() {
				return member, true
			}
		case ViewerUser:
			if memberMatchesViewer(member, viewer) && member.Active() {
				return member, true
			}
		}
	}
	return DiscussionMember{}, false
}

func memberMatchesViewer(member DiscussionMember, viewer ViewerRef) bool {
	switch viewer.Kind {
	case ViewerAgent:
		return memberMatchesAgent(member, viewer.Ref)
	case ViewerUser:
		return member.UserID == viewer.Ref
	default:
		return false
	}
}

func memberMatchesAgent(member DiscussionMember, id string) bool {
	if id == "" {
		return false
	}
	return member.AgentProfileID == agentcore.AgentProfileID(id)
}
