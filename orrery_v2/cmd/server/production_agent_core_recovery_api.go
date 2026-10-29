package main

import (
	"context"
	"errors"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/app/agentrunrecovery"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
)

type agentCoreRecoveryStore interface {
	InspectTask(context.Context, agentkernel.TaskRef) (agentkernel.TaskInspection, error)
	ReviewTask(context.Context, agentkernel.TaskRef, string, string, agentkernel.TaskReviewInput) (agentkernel.TaskReview, error)
	CheckTaskRestart(context.Context, string, string, string) error
}

type productionAgentCoreRestartGuard struct{ store agentCoreRecoveryStore }

func (g productionAgentCoreRestartGuard) CheckTaskRestart(ctx context.Context, tenant, mission, task string) error {
	if g.store == nil {
		return missioncontrol.ErrServiceDependency
	}
	return recoveryAPIError(g.store.CheckTaskRestart(ctx, tenant, mission, task))
}

func recoveryAPIError(err error) error {
	switch {
	case errors.Is(err, agentkernel.ErrInput):
		return missioncontrol.ErrInvalidRequest
	case errors.Is(err, agentkernel.ErrUnsafeResume), errors.Is(err, agentkernel.ErrCheckpoint):
		return missioncontrol.ErrConflict
	default:
		return err
	}
}

func (p productionAPI) recoveryTaskRef(ctx context.Context, viewer missioncontrol.ViewerRef, mission missioncore.MissionID, run string, action string) (agentkernel.TaskRef, missioncontrol.RuntimeSessionDTO, error) {
	if err := viewer.Validate(); err != nil {
		return agentkernel.TaskRef{}, missioncontrol.RuntimeSessionDTO{}, err
	}
	if err := p.authorizeMissionAction(ctx, viewer, mission, action); err != nil {
		return agentkernel.TaskRef{}, missioncontrol.RuntimeSessionDTO{}, err
	}
	if p.missionQuery == nil || p.agentCoreRecovery == nil {
		return agentkernel.TaskRef{}, missioncontrol.RuntimeSessionDTO{}, missioncontrol.ErrServiceDependency
	}
	ops, err := p.GetMissionOpsView(ctx, viewer, mission)
	if err != nil {
		return agentkernel.TaskRef{}, missioncontrol.RuntimeSessionDTO{}, err
	}
	for _, session := range ops.RuntimeSessions {
		if session.RunID == run && session.RuntimeKind == string(agentcore.RuntimeKindManaged) && session.TaskID != "" {
			return agentkernel.TaskRef{TenantID: viewer.Tenant(), MissionID: mission.String(), TaskID: session.TaskID, AgentID: agentcore.AgentID(session.AgentID), RunID: agentcore.AgentRunID(run)}, session, nil
		}
	}
	return agentkernel.TaskRef{}, missioncontrol.RuntimeSessionDTO{}, missioncontrol.ErrProjectionNotFound
}

func recoveryTerminal(status string) bool {
	switch status {
	case string(projection.RuntimeSessionFailed), string(projection.RuntimeSessionCancelled), string(projection.RuntimeSessionTimedOut), string(projection.RuntimeSessionBlocked):
		return true
	}
	return false
}

func recoveryOperationScope(viewer missioncontrol.ViewerRef, manage bool) bool {
	for _, scope := range viewer.PermissionScope {
		if scope == "*" || scope == operations.PermissionOperationsManage || (!manage && scope == operations.PermissionOperationsRead) {
			return true
		}
	}
	return false
}

func (p productionAPI) GetAgentRunRecovery(ctx context.Context, viewer missioncontrol.ViewerRef, mission missioncore.MissionID, run string) (agentrunrecovery.View, error) {
	if !recoveryOperationScope(viewer, false) {
		return agentrunrecovery.View{}, operations.ErrPermissionDenied
	}
	ref, session, err := p.recoveryTaskRef(ctx, viewer, mission, run, "mission.view")
	if err != nil {
		return agentrunrecovery.View{}, err
	}
	status, err := p.agentCoreRecovery.InspectTask(ctx, ref)
	if err != nil {
		return agentrunrecovery.View{}, recoveryAPIError(err)
	}
	terminal := recoveryTerminal(session.Status) && !session.IsAlive
	canManage := recoveryOperationScope(viewer, true) && p.authorizeMissionAction(ctx, viewer, mission, "mission.edit") == nil
	return agentrunrecovery.View{TaskInspection: status, CanReview: terminal && status.Phase == string(agentkernel.CheckpointToolsPending) && viewer.ViewerKind == missioncontrol.ViewerKindUser && canManage, CanRestart: terminal && !status.RestartBlocked && canManage}, nil
}

func (p productionAPI) ReviewAgentRunRecovery(ctx context.Context, viewer missioncontrol.ViewerRef, mission missioncore.MissionID, run, key string, input agentrunrecovery.ReviewInput) (agentrunrecovery.Review, error) {
	if viewer.ViewerKind != missioncontrol.ViewerKindUser || !recoveryOperationScope(viewer, true) {
		return agentrunrecovery.Review{}, operations.ErrPermissionDenied
	}
	ref, session, err := p.recoveryTaskRef(ctx, viewer, mission, run, "mission.edit")
	if err != nil {
		return agentrunrecovery.Review{}, err
	}
	if !recoveryTerminal(session.Status) || session.IsAlive {
		return agentrunrecovery.Review{}, missioncontrol.ErrConflict
	}
	review, err := p.agentCoreRecovery.ReviewTask(ctx, ref, viewer.ViewerID, key, input)
	return review, recoveryAPIError(err)
}
