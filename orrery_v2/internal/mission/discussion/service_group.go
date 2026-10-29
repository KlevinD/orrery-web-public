package discussion

import (
	"context"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

func (s *Service) CreateGroup(ctx context.Context, missionID missioncore.MissionID, goal string) (DiscussionGroup, error) {
	return s.CreateGroupForTenant(ctx, "global", missionID, goal)
}

func (s *Service) CreateGroupForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID, goal string) (DiscussionGroup, error) {
	start := s.now()
	tenantID = normalizeDiscussionTenantID(tenantID)
	if goal == "" {
		err := fmt.Errorf("%w: goal is required", ErrInvalidArgument)
		s.observeMutation(ctx, "create_group", missionID, "", "", start, err)
		return DiscussionGroup{}, err
	}
	var out DiscussionGroup
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if !missionID.IsZero() {
			lockKey := tenantID + "/" + missionID.String()
			if err := db.LockRecordKeyTx(ctx, tx, storage.TableDiscussionGroups.LogicalName, "mission:"+lockKey); err != nil {
				return err
			}
			finder, ok := s.repo.(interface {
				FindGroupByMissionForTenant(context.Context, uow.Tx, string, missioncore.MissionID) (DiscussionGroup, bool, error)
			})
			if !ok && tenantID != "global" {
				return fmt.Errorf("%w: tenant-aware discussion repository is required", ErrInvalidArgument)
			}
			var existing DiscussionGroup
			var found bool
			var err error
			if ok {
				existing, found, err = finder.FindGroupByMissionForTenant(ctx, tx, tenantID, missionID)
			} else {
				existing, found, err = s.repo.FindGroupByMission(ctx, tx, missionID)
			}
			if err != nil {
				return err
			} else if found {
				out = existing
				return nil
			}
		}
		id, err := s.newID("discussion-group")
		if err != nil {
			return err
		}
		now := s.now()
		out = DiscussionGroup{ID: DiscussionGroupID(id), TenantID: tenantID, MissionID: missionID, Goal: goal, Status: DiscussionGroupActive, CreatedAt: now, UpdatedAt: now}
		if err := s.repo.SaveGroup(ctx, tx, out); err != nil {
			return err
		}
		return s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventDiscussionGroupCreated, TenantID: tenantID, MissionID: missionID, DiscussionGroupID: out.ID, Actor: ViewerRef{Kind: ViewerSystem, Ref: "system"}, IdempotencyKey: discussionGroupEventKey(out)})
	})
	s.observeMutation(ctx, "create_group", missionID, out.ID, discussionGroupEventKey(out), start, err)
	return out, err
}

func (s *Service) AddMember(ctx context.Context, groupID DiscussionGroupID, member DiscussionMember) error {
	start := s.now()
	if groupID == "" {
		err := fmt.Errorf("%w: group_id and member ref are required", ErrInvalidArgument)
		s.observeMutation(ctx, "add_member", "", groupID, "", start, err)
		return err
	}
	if member.MemberKind == "" {
		if member.UserID != "" {
			member.MemberKind = DiscussionMemberHuman
		} else {
			member.MemberKind = DiscussionMemberAgent
		}
	}
	if err := validateDiscussionMemberIdentity(member); err != nil {
		s.observeMutation(ctx, "add_member", "", groupID, "", start, err)
		return err
	}
	var missionID missioncore.MissionID
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		group, err := s.repo.GetGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		missionID = group.MissionID
		member.DiscussionGroupID = group.ID
		if member.VisibilityScope == "" {
			member.VisibilityScope = VisibilityMission
		}
		if member.JoinedAt.IsZero() {
			member.JoinedAt = s.now()
		}
		return s.repo.SaveMember(ctx, tx, member)
	})
	s.observeMutation(ctx, "add_member", missionID, groupID, memberRef(member), start, err)
	return err
}

func (s *Service) ListMembers(ctx context.Context, groupID DiscussionGroupID) ([]DiscussionMember, error) {
	if groupID == "" {
		return nil, fmt.Errorf("%w: group_id is required", ErrInvalidArgument)
	}
	var out []DiscussionMember
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		members, err := s.repo.ListMembers(ctx, tx, groupID)
		if err != nil {
			return err
		}
		out = members
		return nil
	})
	return out, err
}

func validateDiscussionMemberIdentity(member DiscussionMember) error {
	switch member.MemberKind {
	case DiscussionMemberAgent:
		if member.AgentProfileID.IsZero() {
			return fmt.Errorf("%w: agent discussion member profile id is required", ErrInvalidArgument)
		}
		if !member.AgentInstanceID.IsZero() && member.AgentInstanceID != agentcore.AgentInstanceID(member.AgentProfileID) {
			return fmt.Errorf("%w: agent discussion member instance id must match stable profile id", ErrInvalidArgument)
		}
	case DiscussionMemberHuman:
		if member.UserID == "" {
			return fmt.Errorf("%w: human discussion member user id is required", ErrInvalidArgument)
		}
	default:
		if member.AgentProfileID.IsZero() && member.UserID == "" {
			return fmt.Errorf("%w: member ref is required", ErrInvalidArgument)
		}
	}
	return nil
}
