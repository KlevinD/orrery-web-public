package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func TestReconcileSchedulerMissionProfilesPlansAppliesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	profiles := &schedulerMissionProfileMap{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-a": schedulerMissionTestProfile("agent-a", true, agentcore.AgentKindAI),
		"agent-b": schedulerMissionTestProfile("agent-b", true, agentcore.AgentKindAI),
	}}
	agentB := profiles.profiles["agent-b"]
	agentB.OrgIdentity.RoleRefs = []agentcore.RoleRef{{ID: "specialist"}, {ID: "worker"}}
	agentB.OrgIdentity.AuthorityScope = "existing_scope"
	profiles.profiles["agent-b"] = agentB
	tasks := schedulerMissionTaskList{tasks: []schedulerapp.ScheduledTask{
		{ScheduledTaskID: "schedule-a", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-a"}},
		{ScheduledTaskID: "schedule-a-duplicate", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-a"}},
		{ScheduledTaskID: "schedule-b", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-b"}},
		{ScheduledTaskID: "not-agent-task", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeToolCall, AgentID: "agent-a"}},
	}}

	planned, err := reconcileSchedulerMissionProfiles(ctx, tasks, profiles, false)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if planned.Applied || planned.ScannedTasks != 3 || planned.TargetProfiles != 2 || planned.PendingProfiles != 1 || planned.UpdatedProfiles != 0 || len(planned.Issues) != 0 {
		t.Fatalf("plan report = %+v", planned)
	}
	if got := profiles.profiles["agent-a"]; len(got.OrgIdentity.RoleRefs) != 0 || got.OrgIdentity.AuthorityScope != "" {
		t.Fatalf("plan changed profile: %+v", got.OrgIdentity)
	}

	applied, err := reconcileSchedulerMissionProfiles(ctx, tasks, profiles, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !applied.Applied || applied.PendingProfiles != 1 || applied.UpdatedProfiles != 1 || len(applied.Issues) != 0 {
		t.Fatalf("apply report = %+v", applied)
	}
	got := profiles.profiles["agent-a"]
	if !schedulerMissionProfileHasRole(got, "worker") || got.OrgIdentity.AuthorityScope != schedulerMissionWorkerAuthorityScope {
		t.Fatalf("repaired profile = %+v", got.OrgIdentity)
	}
	unchanged := profiles.profiles["agent-b"]
	if len(unchanged.OrgIdentity.RoleRefs) != 2 || unchanged.OrgIdentity.AuthorityScope != "existing_scope" {
		t.Fatalf("eligible profile changed = %+v", unchanged.OrgIdentity)
	}

	reused, err := reconcileSchedulerMissionProfiles(ctx, tasks, profiles, true)
	if err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
	if reused.PendingProfiles != 0 || reused.UpdatedProfiles != 0 || reused.EligibleProfiles != 2 {
		t.Fatalf("idempotent report = %+v", reused)
	}
}

func TestReconcileSchedulerMissionProfilesIncludesExplicitTargetsWithoutTasks(t *testing.T) {
	ctx := context.Background()
	profiles := &schedulerMissionProfileMap{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-explicit": schedulerMissionTestProfile("agent-explicit", true, agentcore.AgentKindAI),
		"agent-other":    schedulerMissionTestProfile("agent-other", true, agentcore.AgentKindAI),
	}}
	tasks := schedulerMissionTaskList{}

	planned, err := reconcileSchedulerMissionProfilesForTargets(ctx, tasks, profiles, false, []string{" agent-explicit ", "agent-explicit"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if planned.ScannedTasks != 0 || planned.TargetProfiles != 1 || planned.PendingProfiles != 1 || planned.UpdatedProfiles != 0 || len(planned.Issues) != 0 {
		t.Fatalf("plan report = %+v", planned)
	}
	if got := profiles.profiles["agent-explicit"]; schedulerMissionProfileHasRole(got, "worker") || got.OrgIdentity.AuthorityScope != "" {
		t.Fatalf("plan changed explicit profile: %+v", got.OrgIdentity)
	}

	applied, err := reconcileSchedulerMissionProfilesForTargets(ctx, tasks, profiles, true, []string{"agent-explicit"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.TargetProfiles != 1 || applied.PendingProfiles != 1 || applied.UpdatedProfiles != 1 || applied.EligibleProfiles != 1 || len(applied.Issues) != 0 {
		t.Fatalf("apply report = %+v", applied)
	}
	updated := profiles.profiles["agent-explicit"]
	if !schedulerMissionProfileHasRole(updated, "worker") || updated.OrgIdentity.AuthorityScope != schedulerMissionWorkerAuthorityScope {
		t.Fatalf("explicit profile was not repaired: %+v", updated.OrgIdentity)
	}
	untouched := profiles.profiles["agent-other"]
	if schedulerMissionProfileHasRole(untouched, "worker") || untouched.OrgIdentity.AuthorityScope != "" {
		t.Fatalf("unselected profile changed: %+v", untouched.OrgIdentity)
	}
}

func TestReconcileSchedulerMissionProfilesDoesNotRepairUnsafeTargets(t *testing.T) {
	ctx := context.Background()
	disabled := schedulerMissionTestProfile("disabled", false, agentcore.AgentKindAI)
	human := schedulerMissionTestProfile("human", true, agentcore.AgentKindHuman)
	incomplete := schedulerMissionTestProfile("incomplete", true, agentcore.AgentKindAI)
	incomplete.OrgIdentity.DepartmentRef = agentcore.DepartmentRef{}
	profiles := &schedulerMissionProfileMap{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"disabled": disabled, "human": human, "incomplete": incomplete,
	}}
	tasks := schedulerMissionTaskList{tasks: []schedulerapp.ScheduledTask{
		{ScheduledTaskID: "missing", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "missing"}},
		{ScheduledTaskID: "disabled", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "disabled"}},
		{ScheduledTaskID: "human", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "human"}},
		{ScheduledTaskID: "incomplete", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "incomplete"}},
	}}

	report, err := reconcileSchedulerMissionProfiles(ctx, tasks, profiles, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if report.UpdatedProfiles != 0 || report.PendingProfiles != 0 || len(report.Issues) != 4 {
		t.Fatalf("report = %+v", report)
	}
	for id, profile := range profiles.profiles {
		if schedulerMissionProfileHasRole(profile, "worker") || profile.OrgIdentity.AuthorityScope != "" {
			t.Fatalf("unsafe profile %s changed: %+v", id, profile.OrgIdentity)
		}
	}
}

func TestSchedulerMissionProfilesApplyRequiresExplicitConfirmation(t *testing.T) {
	err := run(context.Background(), []string{"scheduler-mission-profiles", "apply", "--db", "memory://test"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "explicit --apply") {
		t.Fatalf("error = %v", err)
	}
}

type schedulerMissionTaskList struct {
	tasks []schedulerapp.ScheduledTask
	err   error
}

func (f schedulerMissionTaskList) ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error) {
	return append([]schedulerapp.ScheduledTask(nil), f.tasks...), f.err
}

type schedulerMissionProfileMap struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
	err      error
}

func (f *schedulerMissionProfileMap) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if f.err != nil {
		return agentcore.AgentProfile{}, f.err
	}
	profile, ok := f.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, errors.Join(agentcore.ErrNotFound, errors.New(id.String()))
	}
	return profile, nil
}

func (f *schedulerMissionProfileMap) SaveProfile(_ context.Context, profile agentcore.AgentProfile) error {
	if f.err != nil {
		return f.err
	}
	f.profiles[profile.ID] = profile
	return nil
}

func schedulerMissionTestProfile(id agentcore.AgentProfileID, enabled bool, kind agentcore.AgentKind) agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID: id, AgentKind: kind, Enabled: enabled,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org"},
			DepartmentRef:   agentcore.DepartmentRef{ID: "department"},
			PositionRef:     agentcore.PositionRef{ID: "position"},
		},
	}
}
