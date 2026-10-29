package missioncontrol

import (
	"context"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/projection"
)

type RuntimeHealthSnapshot struct {
	RuntimeID       string
	LastHeartbeatAt time.Time
	LeaseExpiresAt  time.Time
	Alive           bool
}

type RuntimeHealthPort interface {
	GetRuntimeHealth(ctx context.Context, runtimeIDs []string) (map[string]RuntimeHealthSnapshot, error)
}

func applyRuntimeHealth(ctx context.Context, model *projection.ReadModel, port RuntimeHealthPort) error {
	if model == nil || port == nil || len(model.RuntimeSessions) == 0 {
		return nil
	}
	runtimeIDs := make([]string, 0, len(model.RuntimeSessions))
	seen := map[string]struct{}{}
	for _, session := range model.RuntimeSessions {
		if !runtimeSessionNeedsHealth(session.Status) {
			continue
		}
		runtimeID := runtimeHealthID(session.RunID)
		if runtimeID == "" {
			continue
		}
		if _, ok := seen[runtimeID]; ok {
			continue
		}
		seen[runtimeID] = struct{}{}
		runtimeIDs = append(runtimeIDs, runtimeID)
	}
	if len(runtimeIDs) == 0 {
		return nil
	}
	health, err := port.GetRuntimeHealth(ctx, runtimeIDs)
	if err != nil {
		return err
	}
	for i := range model.RuntimeSessions {
		if !runtimeSessionNeedsHealth(model.RuntimeSessions[i].Status) {
			continue
		}
		snapshot, ok := health[runtimeHealthID(model.RuntimeSessions[i].RunID)]
		if !ok {
			continue
		}
		model.RuntimeSessions[i].LastHeartbeatAt = snapshot.LastHeartbeatAt
		model.RuntimeSessions[i].LeaseExpiresAt = snapshot.LeaseExpiresAt
		model.RuntimeSessions[i].IsAlive = snapshot.Alive
		model.RuntimeSessions[i].IsStale = !snapshot.Alive && !snapshot.LeaseExpiresAt.IsZero()
	}
	return nil
}

func runtimeSessionNeedsHealth(status projection.RuntimeSessionStatus) bool {
	switch status {
	case projection.RuntimeSessionQueued, projection.RuntimeSessionRunning:
		return true
	default:
		return false
	}
}

func runtimeHealthID(runID agentcore.AgentRunID) string {
	value := strings.TrimSpace(runID.String())
	if value == "" {
		return ""
	}
	return "agent-run:" + value
}
