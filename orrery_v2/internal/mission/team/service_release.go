package team

import (
	"context"
	"fmt"

	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

func (s *Service) Release(ctx context.Context, cmd ReleaseCommand) error {
	ctx, finish := s.startSpan(ctx, "mission_team.release")
	var opErr error
	defer func() {
		finish(opErr)
		s.observeMutation(ctx, "release", cmd.MissionID, "", cmd.IDempotencyKey, opErr)
	}()
	if cmd.MissionID.IsZero() || cmd.AssignmentID == "" {
		opErr = fmt.Errorf("%w: mission_id and assignment_id are required", ErrInvalidArgument)
		return opErr
	}
	opErr = s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTeamRecords.LogicalName, cmd.MissionID.String()); err != nil {
			return err
		}
		assignment, err := s.repo.GetAssignment(ctx, tx, cmd.AssignmentID)
		if err != nil {
			return err
		}
		if assignment.MissionID != cmd.MissionID {
			return fmt.Errorf("%w: assignment does not belong to mission", ErrInvalidArgument)
		}
		if assignment.Status != AssignmentStatusActive {
			if cmd.IDempotencyKey != "" && assignment.ReleaseIdempotencyKey == cmd.IDempotencyKey {
				return nil
			}
			return fmt.Errorf("%w: %s", ErrPolicyDenied, CodeAssignmentNotActive)
		}
		team, err := s.repo.GetTeam(ctx, tx, cmd.MissionID)
		if err != nil {
			return err
		}
		releaseID, err := s.newID("release")
		if err != nil {
			return err
		}
		now := s.now()
		assignment.Status = AssignmentStatusReleased
		assignment.ReleasedAt = now
		assignment.ReleaseID = ReleaseID(releaseID)
		assignment.ReleaseIdempotencyKey = cmd.IDempotencyKey
		if err := s.repo.SaveAssignment(ctx, tx, assignment); err != nil {
			return err
		}
		team = markMemberReleased(team, assignment.MemberID, ReleaseID(releaseID), now)
		if err := s.repo.SaveTeam(ctx, tx, team); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, releaseEvent(missionevent.EventAgentReleased, assignment, cmd, ReleaseID(releaseID))); err != nil {
			return err
		}
		return s.events.Emit(ctx, tx, releaseEvent(missionevent.EventMissionAssignmentReleased, assignment, cmd, ReleaseID(releaseID)))
	})
	return opErr
}
