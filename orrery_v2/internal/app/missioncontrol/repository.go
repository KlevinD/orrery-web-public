package missioncontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
)

type ProjectionRepository interface {
	GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (projection.ReadModel, error)
}

type ProjectionListRepository interface {
	ProjectionRepository
	ListMissionControls(ctx context.Context) ([]projection.ReadModel, error)
}

type TenantProjectionRepository interface {
	GetMissionControlForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) (projection.ReadModel, error)
}

type TenantProjectionListRepository interface {
	ListMissionControlsForTenant(ctx context.Context, tenantID string) ([]projection.ReadModel, error)
}

type TenantProjectionSaveRepository interface {
	SaveForTenant(ctx context.Context, tenantID string, model projection.ReadModel) error
}

type MemoryProjectionRepository struct {
	mu     sync.RWMutex
	models map[string]projection.ReadModel
}

func NewMemoryProjectionRepository() *MemoryProjectionRepository {
	return &MemoryProjectionRepository{models: map[string]projection.ReadModel{}}
}

func (r *MemoryProjectionRepository) Save(ctx context.Context, model projection.ReadModel) error {
	if missionModelTenantID(model) != "global" {
		return ErrServiceDependency
	}
	return r.SaveForTenant(ctx, "global", model)
}

func (r *MemoryProjectionRepository) SaveForTenant(ctx context.Context, tenantID string, model projection.ReadModel) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	tenantID = missionTenantID(tenantID)
	if !validMissionProjectionIdentity(tenantID, model.Mission.MissionID.String()) {
		return ErrInvalidRequest
	}
	model.TenantID = tenantID
	model.Mission.TenantID = tenantID
	payload, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("encode mission control projection: %w", err)
	}
	key := missionProjectionKey(tenantID, model.Mission.MissionID)
	if current, found := r.models[key]; found {
		if current.Checkpoint.Version > model.Checkpoint.Version {
			return ErrConflict
		}
		if current.Checkpoint.Version == model.Checkpoint.Version {
			currentPayload, marshalErr := json.Marshal(current)
			if marshalErr != nil {
				return fmt.Errorf("encode current mission control projection: %w", marshalErr)
			}
			if bytes.Equal(currentPayload, payload) {
				return nil
			}
			return ErrConflict
		}
	}
	var stored projection.ReadModel
	if err := json.Unmarshal(payload, &stored); err != nil {
		return fmt.Errorf("clone mission control projection: %w", err)
	}
	r.models[key] = stored
	return nil
}

func (r *MemoryProjectionRepository) repairRuntimeProjection(ctx context.Context, tenantID string, expected, replacement projection.ReadModel) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r == nil {
		return false, ErrServiceDependency
	}
	tenantID = missionTenantID(tenantID)
	missionID := expected.Mission.MissionID
	if !validMissionProjectionIdentity(tenantID, missionID.String()) || replacement.Mission.MissionID != missionID || replacement.Checkpoint.Version != expected.Checkpoint.Version {
		return false, ErrInvalidRequest
	}
	expected.TenantID = tenantID
	expected.Mission.TenantID = tenantID
	replacement.TenantID = tenantID
	replacement.Mission.TenantID = tenantID
	expectedPayload, err := json.Marshal(expected)
	if err != nil {
		return false, fmt.Errorf("encode expected mission control projection: %w", err)
	}
	replacementPayload, err := json.Marshal(replacement)
	if err != nil {
		return false, fmt.Errorf("encode replacement mission control projection: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	key := missionProjectionKey(tenantID, missionID)
	current, found := r.models[key]
	if !found {
		return false, nil
	}
	currentPayload, err := json.Marshal(current)
	if err != nil {
		return false, fmt.Errorf("encode current mission control projection: %w", err)
	}
	if !bytes.Equal(currentPayload, expectedPayload) {
		return false, nil
	}
	var stored projection.ReadModel
	if err := json.Unmarshal(replacementPayload, &stored); err != nil {
		return false, fmt.Errorf("clone replacement mission control projection: %w", err)
	}
	r.models[key] = stored
	return true, nil
}

func (r *MemoryProjectionRepository) GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (projection.ReadModel, error) {
	return r.GetMissionControlForTenant(ctx, "global", missionID)
}

func (r *MemoryProjectionRepository) GetMissionControlForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) (projection.ReadModel, error) {
	if err := ctx.Err(); err != nil {
		return projection.ReadModel{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	model, ok := r.models[missionProjectionKey(tenantID, missionID)]
	if !ok {
		return projection.ReadModel{}, ErrProjectionNotFound
	}
	return cloneMemoryProjection(model)
}

func (r *MemoryProjectionRepository) ListMissionControls(ctx context.Context) ([]projection.ReadModel, error) {
	return r.ListMissionControlsForTenant(ctx, "global")
}

func (r *MemoryProjectionRepository) ListMissionControlsForTenant(ctx context.Context, tenantID string) ([]projection.ReadModel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]projection.ReadModel, 0, len(r.models))
	for _, model := range r.models {
		if missionModelTenantID(model) != missionTenantID(tenantID) {
			continue
		}
		cloned, err := cloneMemoryProjection(model)
		if err != nil {
			return nil, err
		}
		out = append(out, cloned)
	}
	return out, nil
}

func cloneMemoryProjection(model projection.ReadModel) (projection.ReadModel, error) {
	payload, err := json.Marshal(model)
	if err != nil {
		return projection.ReadModel{}, fmt.Errorf("encode mission control projection: %w", err)
	}
	var cloned projection.ReadModel
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return projection.ReadModel{}, fmt.Errorf("clone mission control projection: %w", err)
	}
	return cloned, nil
}

func loadMissionProjection(ctx context.Context, repo ProjectionRepository, tenantID string, missionID missioncore.MissionID) (projection.ReadModel, error) {
	if tenantRepo, ok := repo.(TenantProjectionRepository); ok {
		return tenantRepo.GetMissionControlForTenant(ctx, tenantID, missionID)
	}
	if missionTenantID(tenantID) != "global" {
		return projection.ReadModel{}, ErrServiceDependency
	}
	model, err := repo.GetMissionControl(ctx, missionID)
	if err != nil {
		return projection.ReadModel{}, err
	}
	if missionModelTenantID(model) != missionTenantID(tenantID) {
		return projection.ReadModel{}, ErrProjectionNotFound
	}
	return model, nil
}

func saveMissionProjection(ctx context.Context, repo interface {
	Save(context.Context, projection.ReadModel) error
}, tenantID string, model projection.ReadModel) error {
	tenantID = missionTenantID(tenantID)
	model.TenantID = tenantID
	model.Mission.TenantID = tenantID
	if tenantRepo, ok := repo.(TenantProjectionSaveRepository); ok {
		return tenantRepo.SaveForTenant(ctx, tenantID, model)
	}
	if tenantID != "global" {
		return ErrServiceDependency
	}
	return repo.Save(ctx, model)
}

func missionModelTenantID(model projection.ReadModel) string {
	if value := strings.TrimSpace(model.TenantID); value != "" {
		return missionTenantID(value)
	}
	return missionTenantID(model.Mission.TenantID)
}

func missionTenantID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "global"
	}
	return value
}

func missionProjectionKey(tenantID string, missionID missioncore.MissionID) string {
	return missionTenantID(tenantID) + "/" + missionID.String()
}
