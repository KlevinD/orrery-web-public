package team

import (
	"context"
	"fmt"

	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

func (s *Service) Appoint(ctx context.Context, cmd AppointmentCommand) (MissionAssignment, error) {
	ctx, finish := s.startSpan(ctx, "mission_team.appoint")
	var opErr error
	defer func() {
		finish(opErr)
		s.observeMutation(ctx, "appoint", cmd.MissionID, cmd.RequestID, cmd.IDempotencyKey, opErr)
	}()
	if cmd.MissionID.IsZero() || cmd.ProfileID.IsZero() || !cmd.Role.IsValid() {
		opErr = fmt.Errorf("%w: mission_id, profile_id and role are required", ErrInvalidArgument)
		return MissionAssignment{}, opErr
	}
	if cmd.AllowOrganizationalRoleBridge && cmd.AppointedBy.Kind != ActorKindSystem {
		opErr = fmt.Errorf("%w: organizational role bridging requires a system actor", ErrInvalidArgument)
		return MissionAssignment{}, opErr
	}
	profile, err := s.profileRepo.GetProfile(ctx, cmd.ProfileID)
	if err != nil {
		opErr = err
		return MissionAssignment{}, opErr
	}
	entry, dirRejection, err := s.appointmentDirectoryEntry(ctx, cmd.ProfileID)
	if err != nil {
		opErr = err
		return MissionAssignment{}, opErr
	}
	var out MissionAssignment
	var policyErr error
	err = s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, storage.TableTeamRecords.LogicalName, cmd.MissionID.String()); err != nil {
			return err
		}
		if existing, ok, err := s.repo.FindAssignmentByIdempotencyKey(ctx, tx, cmd.MissionID, cmd.IDempotencyKey); err != nil {
			return err
		} else if ok {
			out = existing
			return nil
		}
		team, err := s.repo.GetTeam(ctx, tx, cmd.MissionID)
		if err != nil {
			return err
		}
		if duplicateActiveAssignment(team, cmd.ProfileID, cmd.Role) {
			return fmt.Errorf("%w: %s", ErrConflict, CodeDuplicateAssignment)
		}
		request := staffingRequestFromAppointment(cmd)
		evaluation := dirRejection
		if evaluation.CandidateProfileID == "" {
			evaluation = s.policy.Evaluate(request, entry, team)
		}
		if !evaluation.Accepted() {
			audit, err := s.rejectionAudit(cmd, evaluation)
			if err != nil {
				return err
			}
			if err := s.repo.AppendAudit(ctx, tx, audit); err != nil {
				return err
			}
			if err := s.events.Emit(ctx, tx, rejectedEvent(audit, evaluation, cmd)); err != nil {
				return err
			}
			policyErr = fmt.Errorf("%w: profile_id=%s code=%s", ErrPolicyDenied, cmd.ProfileID, firstPolicyCode(evaluation.RejectionReasons))
			return nil
		}
		assignment, member, err := s.buildAppointment(ctx, profile, cmd, team)
		if err != nil {
			return err
		}
		evaluation.CandidateInstanceID = assignment.AgentInstanceID
		decision, audit, err := s.appointmentDecision(cmd, assignment)
		if err != nil {
			return err
		}
		team = appendMember(team, member)
		if err := s.repo.SaveTeam(ctx, tx, team); err != nil {
			return err
		}
		if err := s.repo.SaveAssignment(ctx, tx, assignment); err != nil {
			return err
		}
		if err := s.repo.SaveStaffingDecision(ctx, tx, decision); err != nil {
			return err
		}
		if err := s.repo.AppendAudit(ctx, tx, audit); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, decisionEvent(team, decision, assignment, cmd)); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, joinedEvent(team, member, cmd)); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, assignmentEvent(team, assignment, cmd)); err != nil {
			return err
		}
		out = assignment
		return nil
	})
	if err == nil && policyErr != nil {
		opErr = policyErr
		return MissionAssignment{}, opErr
	}
	if err != nil {
		opErr = err
	}
	return out, opErr
}
