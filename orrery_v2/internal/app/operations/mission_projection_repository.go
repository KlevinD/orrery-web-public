package operations

import (
	"context"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
)

type AutoProjectingMissionProjectionRepository struct {
	repo  MissionProjectionRepository
	evals interface {
		UpsertEvaluationItems(ctx context.Context, items []EvaluationItemDTO) error
	}
}

func NewAutoProjectingMissionProjectionRepository(repo MissionProjectionRepository, evals EvaluationProjectionStore) *AutoProjectingMissionProjectionRepository {
	return &AutoProjectingMissionProjectionRepository{repo: repo, evals: evals}
}

func (r *AutoProjectingMissionProjectionRepository) Save(ctx context.Context, model projection.ReadModel) error {
	if r == nil || r.repo == nil || r.evals == nil {
		return ErrServiceDependency
	}
	if err := r.repo.Save(ctx, model); err != nil {
		return err
	}
	return r.evals.UpsertEvaluationItems(ctx, evaluationsFromMission(model, EvaluationQuery{}))
}

func (r *AutoProjectingMissionProjectionRepository) SaveForTenant(ctx context.Context, tenantID string, model projection.ReadModel) error {
	if r == nil || r.repo == nil || r.evals == nil {
		return ErrServiceDependency
	}
	repo, ok := r.repo.(interface {
		SaveForTenant(context.Context, string, projection.ReadModel) error
	})
	if !ok {
		return ErrServiceDependency
	}
	if err := repo.SaveForTenant(ctx, tenantID, model); err != nil {
		return err
	}
	return r.evals.UpsertEvaluationItems(ctx, evaluationsFromMission(model, EvaluationQuery{}))
}

func (r *AutoProjectingMissionProjectionRepository) GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (projection.ReadModel, error) {
	if r == nil || r.repo == nil {
		return projection.ReadModel{}, ErrServiceDependency
	}
	return r.repo.GetMissionControl(ctx, missionID)
}

func (r *AutoProjectingMissionProjectionRepository) GetMissionControlForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) (projection.ReadModel, error) {
	if r == nil || r.repo == nil {
		return projection.ReadModel{}, ErrServiceDependency
	}
	repo, ok := r.repo.(interface {
		GetMissionControlForTenant(context.Context, string, missioncore.MissionID) (projection.ReadModel, error)
	})
	if !ok {
		return projection.ReadModel{}, ErrServiceDependency
	}
	return repo.GetMissionControlForTenant(ctx, tenantID, missionID)
}

func (r *AutoProjectingMissionProjectionRepository) ListMissionControls(ctx context.Context) ([]projection.ReadModel, error) {
	if r == nil || r.repo == nil {
		return nil, ErrServiceDependency
	}
	repo, ok := r.repo.(interface {
		ListMissionControls(context.Context) ([]projection.ReadModel, error)
	})
	if !ok {
		return nil, ErrServiceDependency
	}
	return repo.ListMissionControls(ctx)
}

func (r *AutoProjectingMissionProjectionRepository) ListMissionControlsForTenant(ctx context.Context, tenantID string) ([]projection.ReadModel, error) {
	if r == nil || r.repo == nil {
		return nil, ErrServiceDependency
	}
	repo, ok := r.repo.(interface {
		ListMissionControlsForTenant(context.Context, string) ([]projection.ReadModel, error)
	})
	if !ok {
		return nil, ErrServiceDependency
	}
	return repo.ListMissionControlsForTenant(ctx, tenantID)
}
