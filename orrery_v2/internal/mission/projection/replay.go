package projection

import (
	"context"
	"fmt"

	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
)

type EventReader interface {
	LoadMission(ctx context.Context, missionID missioncore.MissionID) ([]missionevent.Envelope, error)
}

type ReplayService struct {
	reader    EventReader
	projector Projector
}

func NewReplayService(reader EventReader, projector Projector) *ReplayService {
	return &ReplayService{reader: reader, projector: projector}
}

func (s *ReplayService) ReplayMission(ctx context.Context, missionID missioncore.MissionID) (ReadModel, error) {
	if s == nil || s.reader == nil || s.projector == nil {
		return ReadModel{}, ErrProjectionDependency
	}
	events, err := s.reader.LoadMission(ctx, missionID)
	if err != nil {
		return ReadModel{}, err
	}
	model := NewReadModel(missionID)
	for _, event := range events {
		if err := s.projector.Apply(ctx, model, event); err != nil {
			return ReadModel{}, fmt.Errorf("replay mission %s event %s: %w", missionID, event.EventID, err)
		}
	}
	return *model, nil
}

type MemoryEventReader struct {
	events []missionevent.Envelope
}

func NewMemoryEventReader(events []missionevent.Envelope) *MemoryEventReader {
	copied := append([]missionevent.Envelope(nil), events...)
	return &MemoryEventReader{events: copied}
}

func (r *MemoryEventReader) LoadMission(ctx context.Context, missionID missioncore.MissionID) ([]missionevent.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var events []missionevent.Envelope
	for _, event := range r.events {
		if event.MissionID == missionID {
			events = append(events, event)
		}
	}
	return events, nil
}
