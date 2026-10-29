package main

import (
	"context"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/execution"
)

type staticProductionResponsibilityEventReader struct {
	events []missionevent.Envelope
	err    error
}

func (r staticProductionResponsibilityEventReader) LoadMission(context.Context, missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), r.events...), r.err
}

func (r staticProductionResponsibilityEventReader) LoadMissionForTenant(context.Context, string, missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), r.events...), r.err
}

type staticProductionResponsibilityJobReader struct {
	jobs map[string]execution.JobStatus
	err  error
}

func (r staticProductionResponsibilityJobReader) GetStatus(_ context.Context, runID string) (execution.JobStatus, bool, error) {
	if r.err != nil {
		return execution.JobStatus{}, false, r.err
	}
	job, ok := r.jobs[runID]
	return job, ok, nil
}

type staticProductionResponsibilityLiveness struct {
	snapshots map[string]execution.LivenessSnapshot
	err       error
}

func (l staticProductionResponsibilityLiveness) SnapshotBatch(context.Context, []string) (map[string]execution.LivenessSnapshot, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.snapshots, nil
}
