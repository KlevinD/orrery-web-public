package missioncontrol

import (
	"context"
	"errors"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncore "orrery_v2/internal/mission/core"
)

type QueryService struct {
	repo           ProjectionRepository
	catalog        MissionCatalogPort
	runtimeHealth  RuntimeHealthPort
	agentResources AgentResourcePort
	recoveryAdvice RecoveryAdvicePort
	authorization  *authorization.Service
}

func (s *QueryService) SetAuthorizationService(service *authorization.Service) {
	if s != nil {
		s.authorization = service
	}
}

func NewQueryService(repo ProjectionRepository) *QueryService {
	return &QueryService{repo: repo}
}

func NewQueryServiceWithCatalog(repo ProjectionRepository, catalog MissionCatalogPort) *QueryService {
	return &QueryService{repo: repo, catalog: catalog}
}

func (s *QueryService) SetRuntimeHealthPort(port RuntimeHealthPort) {
	if s == nil {
		return
	}
	s.runtimeHealth = port
}

func (s *QueryService) SetAgentResourcePort(port AgentResourcePort) {
	if s == nil {
		return
	}
	s.agentResources = port
}

type ListMissionsQuery struct {
	Search     string
	Status     string
	OriginKind string
	OriginID   string
	Limit      int
	Offset     int
}

type MissionCatalogPort interface {
	ListMissions(ctx context.Context, viewer ViewerRef, query ListMissionsQuery) (MissionListDTO, error)
}

func (s *QueryService) ListMissions(ctx context.Context, viewer ViewerRef, query ListMissionsQuery) (MissionListDTO, error) {
	if s == nil || s.catalog == nil || s.authorization == nil {
		return MissionListDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionListDTO{}, err
	}
	return s.catalog.ListMissions(ctx, viewer, query)
}

func (s *QueryService) GetMissionControlView(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID) (MissionControlViewDTO, error) {
	if s == nil || s.repo == nil {
		return MissionControlViewDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionControlViewDTO{}, err
	}
	if err := s.authorizeMissionView(ctx, viewer, missionID); err != nil {
		if errors.Is(err, authorization.ErrDenied) {
			return MissionControlViewDTO{}, ErrProjectionNotFound
		}
		return MissionControlViewDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), missionID)
	if err != nil {
		return MissionControlViewDTO{}, err
	}
	return MapViewDTO(model, viewer), nil
}

func (s *QueryService) GetMissionOpsView(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID) (MissionOpsViewDTO, error) {
	if s == nil || s.repo == nil {
		return MissionOpsViewDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return MissionOpsViewDTO{}, err
	}
	if err := s.authorizeMissionView(ctx, viewer, missionID); err != nil {
		if errors.Is(err, authorization.ErrDenied) {
			return MissionOpsViewDTO{}, ErrProjectionNotFound
		}
		return MissionOpsViewDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), missionID)
	if err != nil {
		return MissionOpsViewDTO{}, err
	}
	if err := applyRuntimeHealth(ctx, &model, s.runtimeHealth); err != nil {
		return MissionOpsViewDTO{}, err
	}
	view := MapOpsViewDTO(model, viewer)
	if err := applyAgentResources(ctx, &view, s.agentResources); err != nil {
		return MissionOpsViewDTO{}, err
	}
	s.applyRecoveryAdvice(ctx, &view, viewer)
	return view, nil
}

func (s *QueryService) authorizeMissionView(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID) error {
	if s == nil || s.authorization == nil {
		return ErrServiceDependency
	}
	if isBreakglassSystemViewer(viewer) {
		return nil
	}
	principal := missionPrincipal(viewer)
	object := identity.ObjectRef{Type: "mission", ID: missionID.String(), TenantID: viewer.Tenant()}
	err := s.authorization.Can(ctx, principal, "mission.view", object)
	if err == nil || !errors.Is(err, authorization.ErrDenied) {
		return err
	}
	return s.authorization.Can(ctx, principal, "mission.monitor", object)
}
