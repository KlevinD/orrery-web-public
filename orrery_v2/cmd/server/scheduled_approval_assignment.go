package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
)

type scheduledApprovalAssignmentRepository interface {
	GetTask(context.Context, string) (schedulerapp.ScheduledTask, error)
	SaveTask(context.Context, schedulerapp.ScheduledTask) error
}

type scheduledApprovalAssignmentLifecycle interface {
	EnsureScheduledMission(context.Context, schedulerapp.ScheduledTask) (string, error)
}

type scheduledApprovalAssigner struct {
	repository scheduledApprovalAssignmentRepository
	lifecycle  scheduledApprovalAssignmentLifecycle
	now        func() time.Time
}

type scheduledApprovalAssignmentReport struct {
	ScheduledTaskID    string `json:"scheduled_task_id"`
	MissionID          string `json:"mission_id"`
	ApprovalAssigneeID string `json:"approval_assignee_id"`
	Status             string `json:"status"`
	Enabled            bool   `json:"enabled"`
}

func (a scheduledApprovalAssigner) Assign(ctx context.Context, scheduledTaskID string, assigneeID string) (scheduledApprovalAssignmentReport, error) {
	var report scheduledApprovalAssignmentReport
	if a.repository == nil || a.lifecycle == nil {
		return report, schedulerapp.ErrServiceDependency
	}
	scheduledTaskID = strings.TrimSpace(scheduledTaskID)
	assigneeID = strings.TrimSpace(assigneeID)
	if scheduledTaskID == "" || assigneeID == "" {
		return report, schedulerapp.ErrInvalidRequest
	}
	canonicalAssigneeID := humanAgentProfileID(assigneeID).String()
	if canonicalAssigneeID == "human-" {
		return report, schedulerapp.ErrInvalidRequest
	}
	task, err := a.repository.GetTask(ctx, scheduledTaskID)
	if err != nil {
		return report, err
	}
	if task.Target.Mode != schedulerapp.TargetModeAgentTask {
		return report, fmt.Errorf("%w: scheduled approval assignment requires agent_task target", schedulerapp.ErrInvalidTarget)
	}
	task.Target.ApprovalAssigneeID = canonicalAssigneeID
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	task.UpdatedAt = now().UTC()
	missionID, err := a.lifecycle.EnsureScheduledMission(ctx, task)
	if err != nil {
		return report, err
	}
	task.MissionID = strings.TrimSpace(missionID)
	if task.MissionID == "" {
		return report, schedulerapp.ErrServiceDependency
	}
	if err := a.repository.SaveTask(ctx, task); err != nil {
		return report, err
	}
	return scheduledApprovalAssignmentReport{
		ScheduledTaskID: task.ScheduledTaskID, MissionID: strings.TrimSpace(missionID), ApprovalAssigneeID: canonicalAssigneeID,
		Status: string(task.Status), Enabled: task.Enabled,
	}, nil
}

func runProductionScheduledApprovalAssignment(ctx context.Context, cfg config.Config, scheduledTaskID string, assigneeID string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	rt, err := openProductionRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()
	if err := rt.Initialize(ctx); err != nil {
		return err
	}
	profiles := agentcore.NewPlatformProfileRepository(rt.UnitOfWork)
	missionRepo := missioncontrol.NewDBProjectionRepository(rt.DBRouter)
	discussions, staffing := newProductionMissionCollaborationServices(rt, profiles)
	authorizationService, _, err := authorizationServiceFromRuntime(rt)
	if err != nil {
		return err
	}
	authorizationRelations := authorizationRelationsFromPlatform(rt.Config, authorizationService)
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		return err
	}
	lifecycle := newProductionLifecycleService(
		rt,
		missionRepo,
		profiles,
		discussions,
		staffing,
		newProductionHumanProfileBootstrap(profiles),
		nil,
		authorizationRelations,
		authorizationService,
		nil,
		missionevent.NewStoreSink(eventstore.New(core)),
	)
	repository := schedulerapp.NewRepository(rt.DBRouter)
	report, err := (scheduledApprovalAssigner{
		repository: repository,
		lifecycle:  productionSchedulerMissionLifecycle{missions: lifecycle},
		now:        rt.Clock.Now,
	}).Assign(ctx, scheduledTaskID, assigneeID)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}
