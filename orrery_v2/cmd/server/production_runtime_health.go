package main

import (
	"context"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/platform/execution"
)

type productionRuntimeHealthPort struct {
	liveness interface {
		SnapshotBatch(context.Context, []string) (map[string]execution.LivenessSnapshot, error)
	}
}

func (p productionRuntimeHealthPort) GetRuntimeHealth(ctx context.Context, runtimeIDs []string) (map[string]missioncontrol.RuntimeHealthSnapshot, error) {
	if p.liveness == nil {
		return nil, missioncontrol.ErrServiceDependency
	}
	snapshots, err := p.liveness.SnapshotBatch(ctx, runtimeIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]missioncontrol.RuntimeHealthSnapshot, len(snapshots))
	for runtimeID, snapshot := range snapshots {
		out[runtimeID] = missioncontrol.RuntimeHealthSnapshot{
			RuntimeID:       snapshot.RuntimeID,
			LastHeartbeatAt: snapshot.LastSeenAt,
			LeaseExpiresAt:  snapshot.ExpiresAt,
			Alive:           snapshot.Alive,
		}
	}
	return out, nil
}
