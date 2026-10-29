package main

import (
	"context"

	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

type schedulerMissionConversationEnsurer func(context.Context, missioncontrol.ViewerRef, missioncontrol.MissionDTO) error

type productionSchedulerMissionCommands struct {
	next               schedulerMissionCommandPort
	ensureConversation schedulerMissionConversationEnsurer
}

func newProductionSchedulerMissionCommands(next schedulerMissionCommandPort, api *productionAPI) schedulerMissionCommandPort {
	return productionSchedulerMissionCommands{
		next: next,
		ensureConversation: func(ctx context.Context, viewer missioncontrol.ViewerRef, mission missioncontrol.MissionDTO) error {
			if api == nil {
				return missioncontrol.ErrServiceDependency
			}
			origin, err := api.missionPersonalAssistantOrigin(ctx, viewer.Tenant(), missioncore.MissionID(mission.MissionID))
			if err != nil {
				return err
			}
			_, err = api.ensureMissionDiscussionSession(ctx, viewer, mission, origin)
			return err
		},
	}
}

func (p productionSchedulerMissionCommands) CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	if p.next == nil {
		return missioncontrol.MissionDTO{}, missioncontrol.ErrServiceDependency
	}
	mission, err := p.next.CreateMission(ctx, viewer, cmd)
	if err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	return p.withCanonicalConversation(ctx, viewer, mission)
}

func (p productionSchedulerMissionCommands) SyncSchedulerMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SyncSchedulerMissionCommand) (missioncontrol.MissionDTO, error) {
	if p.next == nil {
		return missioncontrol.MissionDTO{}, missioncontrol.ErrServiceDependency
	}
	mission, err := p.next.SyncSchedulerMission(ctx, viewer, cmd)
	if err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	return p.withCanonicalConversation(ctx, viewer, mission)
}

func (p productionSchedulerMissionCommands) CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	if p.next == nil {
		return missioncontrol.TaskCardDTO{}, missioncontrol.ErrServiceDependency
	}
	return p.next.CreateTask(ctx, viewer, cmd)
}

func (p productionSchedulerMissionCommands) AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	if p.next == nil {
		return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrServiceDependency
	}
	return p.next.AssignTask(ctx, viewer, cmd)
}

func (p productionSchedulerMissionCommands) withCanonicalConversation(ctx context.Context, viewer missioncontrol.ViewerRef, mission missioncontrol.MissionDTO) (missioncontrol.MissionDTO, error) {
	if p.ensureConversation == nil {
		return mission, nil
	}
	if err := p.ensureConversation(ctx, viewer, mission); err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	return mission, nil
}
