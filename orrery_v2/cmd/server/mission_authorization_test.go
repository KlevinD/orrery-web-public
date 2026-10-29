package main

import (
	"orrery_v2/internal/app/authorization"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/rebac"
)

type productionMissionAuthorizationTestFixture struct {
	service   *authorization.Service
	relations *authorization.RelationProjector
}

func newProductionMissionAuthorizationTestFixture() productionMissionAuthorizationTestFixture {
	adapter := authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil)
	return productionMissionAuthorizationTestFixture{
		service:   authorization.NewService(adapter),
		relations: authorization.NewRelationProjector(adapter),
	}
}

func (f productionMissionAuthorizationTestFixture) wireCommand(service *missioncontrol.CommandService) {
	service.SetAuthorizationService(f.service)
}

func (f productionMissionAuthorizationTestFixture) wireQuery(service *missioncontrol.QueryService) {
	service.SetAuthorizationService(f.service)
}

func authorizedMissionTestViewer(viewerID string, kind missioncontrol.ViewerKind) missioncontrol.ViewerRef {
	return missioncontrol.ViewerRef{
		ViewerID:   viewerID,
		ViewerKind: kind,
		PermissionScope: []string{
			"mission.read",
			"mission.write",
			"discussion.read",
			"discussion.write",
		},
	}
}
