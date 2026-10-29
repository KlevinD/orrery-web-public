package missioncontrol

import (
	"context"

	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

type persistedMissionEventMatcher func(missionevent.Envelope) (bool, error)

func projectPersistedMissionEvents(
	ctx context.Context,
	projector projection.Projector,
	model *projection.ReadModel,
	events []missionevent.Envelope,
	match persistedMissionEventMatcher,
) (bool, error) {
	if projector == nil || model == nil || match == nil {
		return false, ErrServiceDependency
	}
	persisted := false
	for _, event := range events {
		matched, err := match(event)
		if err != nil {
			return false, err
		}
		if matched {
			persisted = true
		}
		if event.StreamVersion <= model.Checkpoint.Version {
			if !matched {
				continue
			}
			// Command-side projection mutations can advance the read-model
			// checkpoint beyond the durable stream. Project the matched event
			// at the next read-model version without rewriting durable history.
			event.StreamVersion = model.Checkpoint.Version + 1
		}
		if err := projector.Apply(ctx, model, event); err != nil {
			return false, err
		}
	}
	return persisted, nil
}
