package main

import (
	"context"

	authorizationapp "orrery_v2/internal/app/authorization"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/mission/projection"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform"
)

func newProductionLifecycleService(
	rt *platform.Runtime,
	repo interface {
		missioncontrol.ProjectionRepository
		Save(context.Context, projection.ReadModel) error
	},
	agents missioncontrol.AgentDirectoryPort,
	discussions missioncontrol.DiscussionInitializerPort,
	staffing missioncontrol.TeamBootstrapPort,
	humanProfiles missioncontrol.HumanProfilePort,
	systemRoles []missioncontrol.SystemRoleBootstrap,
	authorizationRelations *authorizationapp.RelationProjector,
	authorizationService *authorizationapp.Service,
	taskResponsibility missioncontrol.TaskResponsibilityAssignmentPort,
	missionEvents missioncontrol.MissionEventSinkPort,
) *missioncontrol.LifecycleService {
	return missioncontrol.NewLifecycleService(rt.UnitOfWork, rt.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock:          rt.Clock,
		IDGenerator:    rt.IDGenerator,
		AgentDirectory: agents,
		Discussion:     discussions,
		TeamBootstrap:  staffing,
		HumanProfiles:  humanProfiles,
		SystemRoles:    systemRoles,
		SystemAutomation: []authorizationapp.SubjectRef{
			{Kind: string(missioncontrol.ViewerKindSystem), ID: qaPlanSystemViewerID},
		},
		HumanBootstrap: missioncontrol.HumanAgentBootstrap{
			Enabled:        true,
			MissionRole:    missionteam.MissionRoleRealHumanContributor,
			Responsibility: "Provide original human intent, clarification, review, and collaboration context.",
			AuthorityScope: "mission_collaboration",
		},
		AuthorizationRelations: authorizationRelations,
		AuthorizationService:   authorizationService,
		TaskResponsibility:     taskResponsibility,
		MissionEvents:          missionEvents,
	})
}
