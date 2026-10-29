package missioncontrol_test

import (
	"context"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	missionteam "orrery_v2/internal/mission/team"
)

type fakeLifecycleAgentDirectory struct {
	entries []agentcore.AgentDirectoryEntry
}

func (f *fakeLifecycleAgentDirectory) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{Entries: append([]agentcore.AgentDirectoryEntry(nil), f.entries...)}, nil
}

type fakeDiscussionInitializer struct {
	tenantID  string
	missionID missioncore.MissionID
	goal      string
	members   []discussion.DiscussionMember
}

func (f *fakeDiscussionInitializer) CreateGroup(ctx context.Context, missionID missioncore.MissionID, goal string) (discussion.DiscussionGroup, error) {
	return f.CreateGroupForTenant(ctx, "global", missionID, goal)
}

func (f *fakeDiscussionInitializer) CreateGroupForTenant(_ context.Context, tenantID string, missionID missioncore.MissionID, goal string) (discussion.DiscussionGroup, error) {
	f.tenantID = tenantID
	f.missionID = missionID
	f.goal = goal
	return discussion.DiscussionGroup{ID: "discussion-1", MissionID: missionID, Goal: goal, Status: discussion.DiscussionGroupActive}, nil
}

func (f *fakeDiscussionInitializer) AddMember(ctx context.Context, groupID discussion.DiscussionGroupID, member discussion.DiscussionMember) error {
	member.DiscussionGroupID = groupID
	f.members = append(f.members, member)
	return nil
}

type fakeTeamBootstrap struct {
	commands    []missionteam.AppointmentCommand
	assignments []missionteam.MissionAssignment
	err         error
}

type fakeHumanProfileBootstrap struct {
	requests  []missioncontrol.EnsureHumanProfileCommand
	profileID agentcore.AgentProfileID
	profile   agentcore.AgentProfile
	err       error
}

func (f *fakeHumanProfileBootstrap) EnsureHumanProfile(_ context.Context, cmd missioncontrol.EnsureHumanProfileCommand) (agentcore.AgentProfile, error) {
	f.requests = append(f.requests, cmd)
	if f.err != nil {
		return agentcore.AgentProfile{}, f.err
	}
	if !f.profile.ID.IsZero() {
		return f.profile, nil
	}
	profileID := f.profileID
	if profileID.IsZero() {
		profileID = agentcore.AgentProfileID("human-" + cmd.HumanAgentID)
	}
	return agentcore.AgentProfile{ID: profileID, AgentKind: agentcore.AgentKindHuman}, nil
}

func (f *fakeTeamBootstrap) Appoint(_ context.Context, cmd missionteam.AppointmentCommand) (missionteam.MissionAssignment, error) {
	f.commands = append(f.commands, cmd)
	if f.err != nil {
		return missionteam.MissionAssignment{}, f.err
	}
	if len(f.assignments) >= len(f.commands) {
		assignment := f.assignments[len(f.commands)-1]
		assignment.MissionID = cmd.MissionID
		if assignment.AssignedAt.IsZero() {
			assignment.AssignedAt = time.Now().UTC()
		}
		return assignment, nil
	}
	return missionteam.MissionAssignment{
		ID:              missionteam.MissionAssignmentID(cmd.IDempotencyKey),
		MissionID:       cmd.MissionID,
		AgentInstanceID: agentcore.AgentInstanceID(cmd.ProfileID),
		AgentProfileID:  cmd.ProfileID,
		Role:            cmd.Role,
		AssignedAt:      time.Now().UTC(),
	}, nil
}
