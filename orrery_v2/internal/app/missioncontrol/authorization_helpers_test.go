package missioncontrol_test

import (
	"context"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

type allowAllMissionAuthorizationAdapter struct{}

func (allowAllMissionAuthorizationAdapter) Check(context.Context, authorization.CheckRequest) (authorization.Decision, error) {
	return authorization.Decision{Allowed: true, Source: "test"}, nil
}

func (a allowAllMissionAuthorizationAdapter) BatchCheck(ctx context.Context, requests []authorization.CheckRequest) ([]authorization.Decision, error) {
	decisions := make([]authorization.Decision, len(requests))
	for i := range requests {
		decisions[i] = authorization.Decision{Allowed: true, Source: "test"}
	}
	return decisions, nil
}

func (allowAllMissionAuthorizationAdapter) ListObjects(context.Context, identity.Principal, string, string, string) ([]identity.ObjectRef, error) {
	return nil, nil
}

func (allowAllMissionAuthorizationAdapter) WriteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func (allowAllMissionAuthorizationAdapter) DeleteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func testMissionAuthorization() *authorization.Service {
	return authorization.NewService(allowAllMissionAuthorizationAdapter{})
}

func authorizeCommandService(service *missioncontrol.CommandService) *missioncontrol.CommandService {
	service.SetAuthorizationService(testMissionAuthorization())
	return service
}

func newAuthorizedCommandService(delivery missioncontrol.TaskDeliveryPort) *missioncontrol.CommandService {
	return authorizeCommandService(missioncontrol.NewCommandService(delivery))
}

func newAuthorizedCommandServiceWithPorts(delivery missioncontrol.TaskDeliveryPort, qa missioncontrol.QAPort, discussion missioncontrol.DiscussionPort, inbox missioncontrol.InboxPort) *missioncontrol.CommandService {
	return authorizeCommandService(missioncontrol.NewCommandServiceWithPorts(delivery, qa, discussion, inbox))
}

func newAuthorizedCommandServiceWithMissionPorts(delivery missioncontrol.TaskDeliveryPort, qa missioncontrol.QAPort, discussion missioncontrol.DiscussionPort, inbox missioncontrol.InboxPort, missions missioncontrol.MissionTaskPort) *missioncontrol.CommandService {
	return authorizeCommandService(missioncontrol.NewCommandServiceWithMissionPorts(delivery, qa, discussion, inbox, missions))
}

func newAuthorizedQueryService(repo missioncontrol.ProjectionRepository) *missioncontrol.QueryService {
	service := missioncontrol.NewQueryService(repo)
	service.SetAuthorizationService(testMissionAuthorization())
	return service
}

func newAuthorizedQueryServiceWithCatalog(repo missioncontrol.ProjectionRepository, catalog missioncontrol.MissionCatalogPort) *missioncontrol.QueryService {
	service := missioncontrol.NewQueryServiceWithCatalog(repo, catalog)
	service.SetAuthorizationService(testMissionAuthorization())
	return service
}

func newAuthorizedApprovalService(repo missioncontrol.ProjectionRepository, commands missioncontrol.ApprovalCommandPort) *missioncontrol.ApprovalService {
	service := missioncontrol.NewApprovalService(repo, commands)
	service.SetAuthorizationService(testMissionAuthorization())
	return service
}

func newAuthorizedLifecycleService(unit *uow.UnitOfWork, router *db.Router, repo interface {
	missioncontrol.ProjectionRepository
	Save(context.Context, missionprojection.ReadModel) error
}, opts missioncontrol.LifecycleOptions) *missioncontrol.LifecycleService {
	opts.AuthorizationService = testMissionAuthorization()
	if opts.AuthorizationRelations == nil {
		opts.AuthorizationRelations = authorization.NewRelationProjector(allowAllMissionAuthorizationAdapter{})
	}
	return missioncontrol.NewLifecycleService(unit, router, repo, opts)
}
