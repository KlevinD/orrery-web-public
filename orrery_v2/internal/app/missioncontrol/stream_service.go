package missioncontrol

import (
	"context"
	"fmt"
	"reflect"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
)

type StreamService struct {
	repo ProjectionRepository
}

func NewStreamService() *StreamService {
	return &StreamService{}
}

func NewRepositoryStreamService(repo ProjectionRepository) *StreamService {
	return &StreamService{repo: repo}
}

func (s *StreamService) BuildPatch(ctx context.Context, viewer ViewerRef, model projection.ReadModel, baseVersion int) (MissionControlPatchDTO, error) {
	return s.buildPatch(ctx, viewer, projection.ReadModel{Checkpoint: projection.Checkpoint{Version: baseVersion}}, model, nil)
}

func (s *StreamService) BuildPatchFromBase(ctx context.Context, viewer ViewerRef, base projection.ReadModel, next projection.ReadModel) (MissionControlPatchDTO, error) {
	return s.buildPatch(ctx, viewer, base, next, nil)
}

func (s *StreamService) BuildLatestPatch(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID, base projection.ReadModel) (MissionControlPatchDTO, error) {
	if s == nil || s.repo == nil {
		return MissionControlPatchDTO{}, ErrServiceDependency
	}
	next, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), missionID)
	if err != nil {
		return MissionControlPatchDTO{}, err
	}
	return s.buildPatch(ctx, viewer, base, next, nil)
}

func (s *StreamService) buildPatch(ctx context.Context, viewer ViewerRef, base projection.ReadModel, next projection.ReadModel, rawEvents any) (MissionControlPatchDTO, error) {
	if err := ctx.Err(); err != nil {
		return MissionControlPatchDTO{}, err
	}
	if err := viewer.Validate(); err != nil {
		return MissionControlPatchDTO{}, err
	}
	view := MapViewDTO(next, viewer)
	return MissionControlPatchDTO{
		PatchID:      fmt.Sprintf("%s:%d:%d", next.Mission.MissionID, base.Checkpoint.Version, next.Checkpoint.Version),
		MissionID:    next.Mission.MissionID.String(),
		BaseVersion:  base.Checkpoint.Version,
		NextVersion:  next.Checkpoint.Version,
		ChangedViews: changedViews(MapViewDTO(base, viewer), view),
		Payload:      view,
		EmittedAt:    next.UpdatedAt,
		RawEvents:    rawEvents,
	}, nil
}

func changedViews(base MissionControlViewDTO, next MissionControlViewDTO) []string {
	if base.Version == next.Version && reflect.DeepEqual(base, next) {
		return nil
	}
	var changed []string
	if !reflect.DeepEqual(base.Mission, next.Mission) {
		changed = append(changed, "mission")
	}
	if !reflect.DeepEqual(base.Team, next.Team) {
		changed = append(changed, "team")
	}
	if !reflect.DeepEqual(base.TaskBoard, next.TaskBoard) {
		changed = append(changed, "tasks")
	}
	if !reflect.DeepEqual(base.Evidence, next.Evidence) {
		changed = append(changed, "evidence")
	}
	if !reflect.DeepEqual(base.QA, next.QA) {
		changed = append(changed, "qa")
	}
	if !reflect.DeepEqual(base.Discussion, next.Discussion) {
		changed = append(changed, "discussion")
	}
	if !reflect.DeepEqual(base.InboxSummary, next.InboxSummary) {
		changed = append(changed, "inbox_summary")
	}
	if !reflect.DeepEqual(base.ProgressGraph, next.ProgressGraph) {
		changed = append(changed, "progress")
	}
	if !reflect.DeepEqual(base.Summary, next.Summary) {
		changed = append(changed, "summary")
	}
	return changed
}
