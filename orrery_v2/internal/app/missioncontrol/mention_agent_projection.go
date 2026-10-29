package missioncontrol

import (
	"context"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	missionprojection "orrery_v2/internal/mission/projection"
)

func (r *MentionAgentRunner) nextStreamVersion(ctx context.Context, tenantID string, missionID missioncore.MissionID) (int, error) {
	if r == nil || r.reader == nil {
		return 1, nil
	}
	events, err := loadMissionEvents(ctx, r.reader, tenantID, missionID)
	if err != nil {
		return 0, err
	}
	return len(events) + 1, nil
}

func (r *MentionAgentRunner) refreshProjection(ctx context.Context, tenantID string, missionID missioncore.MissionID, fallback missionevent.Envelope) error {
	if r == nil || r.repo == nil {
		return nil
	}
	model, err := loadMissionProjection(ctx, r.repo, tenantID, missionID)
	if err != nil {
		return err
	}
	var events []missionevent.Envelope
	if r.reader != nil {
		events, err = loadMissionEvents(ctx, r.reader, tenantID, missionID)
		if err != nil {
			return err
		}
	}
	if len(events) == 0 && fallback.EventID != "" {
		events = []missionevent.Envelope{fallback}
	}
	if len(events) == 0 {
		return nil
	}
	replayed := missionprojection.NewReadModel(missionID)
	projector := missionprojection.NewProjector()
	for _, event := range events {
		if err := projector.Apply(ctx, replayed, event); err != nil {
			return err
		}
	}
	model.ProgressGraph = replayed.ProgressGraph
	if at := events[len(events)-1].OccurredAt; !at.IsZero() {
		model.UpdatedAt = at
		model.Checkpoint.UpdatedAt = at
	}
	model.Checkpoint.Version++
	if model.Checkpoint.MissionID.IsZero() {
		model.Checkpoint.MissionID = missionID
	}
	return saveMissionProjection(ctx, r.repo, tenantID, model)
}

func (r *MentionAgentRunner) viewFromEvents(ctx context.Context, tenantID string, missionID missioncore.MissionID, messageID string, agentID string) (MissionControlViewDTO, bool, error) {
	if r == nil || r.reader == nil {
		return MissionControlViewDTO{}, false, nil
	}
	events, err := loadMissionEvents(ctx, r.reader, tenantID, missionID)
	if err != nil {
		return MissionControlViewDTO{}, false, err
	}
	if len(events) == 0 {
		return MissionControlViewDTO{}, false, nil
	}
	replayed := missionprojection.NewReadModel(missionID)
	projector := missionprojection.NewProjector()
	for _, event := range events {
		if err := projector.Apply(ctx, replayed, event); err != nil {
			return MissionControlViewDTO{}, false, err
		}
	}
	replayed.TenantID = missionTenantID(tenantID)
	replayed.Mission.TenantID = missionTenantID(tenantID)
	view := MapViewDTO(*replayed, r.mentionViewer(tenantID, agentID))
	return view, findDiscussionMessage(view.Discussion.Messages, messageID).MessageID != "", nil
}

func mergeMentionView(base MissionControlViewDTO, fresh MissionControlViewDTO) MissionControlViewDTO {
	if base.Discussion.DiscussionGroupID == "" || len(fresh.Discussion.Messages) > 0 {
		base.Discussion = fresh.Discussion
	}
	if base.Mission.FinalGoal == "" && fresh.Mission.FinalGoal != "" {
		base.Mission = fresh.Mission
	}
	return base
}
