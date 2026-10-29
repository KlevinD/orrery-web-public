package team

import (
	"context"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/uow"
)

func (s *Service) ListTeam(ctx context.Context, missionID missioncore.MissionID) (MissionTeam, error) {
	ctx, finish := s.startSpan(ctx, "mission_team.list_team")
	var opErr error
	defer func() { finish(opErr) }()
	var team MissionTeam
	opErr = s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		team, err = s.repo.GetTeam(ctx, tx, missionID)
		return err
	})
	return team, opErr
}
