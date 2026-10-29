package team

import (
	"context"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

func (s *Service) Replace(ctx context.Context, cmd ReplaceCommand) (MissionAssignment, error) {
	ctx, finish := s.startSpan(ctx, "mission_team.replace")
	var opErr error
	defer func() {
		finish(opErr)
		s.observeMutation(ctx, "replace", cmd.MissionID, "", cmd.IDempotencyKey, opErr)
	}()
	if cmd.MissionID.IsZero() || cmd.OldAssignmentID == "" || cmd.NewProfileID == "" || !cmd.Role.IsValid() {
		opErr = fmt.Errorf("%w: mission_id, old assignment, new profile and role are required", ErrInvalidArgument)
		return MissionAssignment{}, opErr
	}
	profile, err := s.profileRepo.GetProfile(ctx, agentcore.AgentProfileID(cmd.NewProfileID))
	if err != nil {
		opErr = err
		return MissionAssignment{}, opErr
	}
	entry, dirRejection, err := s.appointmentDirectoryEntry(ctx, profile.ID)
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
		oldAssignment, err := s.repo.GetAssignment(ctx, tx, cmd.OldAssignmentID)
		if err != nil {
			return err
		}
		if oldAssignment.MissionID != cmd.MissionID {
			return fmt.Errorf("%w: assignment does not belong to mission", ErrInvalidArgument)
		}
		if oldAssignment.Status != AssignmentStatusActive {
			if cmd.IDempotencyKey != "" && oldAssignment.ReplacementIdempotencyKey == cmd.IDempotencyKey && oldAssignment.ReplacedByAssignmentID != "" {
				out, err = s.repo.GetAssignment(ctx, tx, oldAssignment.ReplacedByAssignmentID)
				return err
			}
			return fmt.Errorf("%w: %s", ErrPolicyDenied, CodeAssignmentNotActive)
		}
		team, err := s.repo.GetTeam(ctx, tx, cmd.MissionID)
		if err != nil {
			return err
		}
		appointCmd := AppointmentCommand{
			IDempotencyKey: cmd.IDempotencyKey,
			MissionID:      cmd.MissionID,
			ProfileID:      profile.ID,
			Role:           cmd.Role,
			Responsibility: cmd.Responsibility,
			AuthorityScope: cmd.AuthorityScope,
			AppointedBy:    cmd.ReplacedBy,
			Reason:         cmd.ReplacementReason,
		}
		request := staffingRequestFromAppointment(appointCmd)
		evaluation := dirRejection
		if evaluation.CandidateProfileID == "" {
			evaluation = s.policy.Evaluate(request, entry, team)
		}
		if !evaluation.Accepted() {
			audit, err := s.rejectionAudit(appointCmd, evaluation)
			if err != nil {
				return err
			}
			if err := s.repo.AppendAudit(ctx, tx, audit); err != nil {
				return err
			}
			if err := s.events.Emit(ctx, tx, rejectedEvent(audit, evaluation, appointCmd)); err != nil {
				return err
			}
			policyErr = fmt.Errorf("%w: %s", ErrPolicyDenied, firstPolicyCode(evaluation.RejectionReasons))
			return nil
		}
		newAssignment, newMember, err := s.buildAppointment(ctx, profile, appointCmd, team)
		if err != nil {
			return err
		}
		evaluation.CandidateInstanceID = newAssignment.AgentInstanceID
		decision, audit, err := s.appointmentDecision(appointCmd, newAssignment)
		if err != nil {
			return err
		}
		replacementID, err := s.newID("replacement")
		if err != nil {
			return err
		}
		oldAssignment.Status = AssignmentStatusReplaced
		oldAssignment.ReplacedByAssignmentID = newAssignment.ID
		oldAssignment.ReplacementID = ReplacementID(replacementID)
		oldAssignment.ReplacementIdempotencyKey = cmd.IDempotencyKey
		oldAssignment.ReplacementReason = cmd.ReplacementReason
		oldAssignment.ReleasedAt = s.now()
		team = markMemberReplaced(team, oldAssignment.MemberID, ReplacementID(replacementID), oldAssignment.ReleasedAt)
		team = appendMember(team, newMember)
		if err := s.repo.SaveAssignment(ctx, tx, oldAssignment); err != nil {
			return err
		}
		if err := s.repo.SaveAssignment(ctx, tx, newAssignment); err != nil {
			return err
		}
		if err := s.repo.SaveTeam(ctx, tx, team); err != nil {
			return err
		}
		if err := s.repo.SaveStaffingDecision(ctx, tx, decision); err != nil {
			return err
		}
		if err := s.repo.AppendAudit(ctx, tx, audit); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, releaseEvent(missionevent.EventAgentReleased, oldAssignment, ReleaseCommand{IDempotencyKey: cmd.IDempotencyKey, MissionID: cmd.MissionID, AssignmentID: oldAssignment.ID, ReleasedBy: cmd.ReplacedBy, Reason: cmd.ReplacementReason}, ReleaseID(replacementID))); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, releaseEvent(missionevent.EventMissionAssignmentReleased, oldAssignment, ReleaseCommand{IDempotencyKey: cmd.IDempotencyKey, MissionID: cmd.MissionID, AssignmentID: oldAssignment.ID, ReleasedBy: cmd.ReplacedBy, Reason: cmd.ReplacementReason}, ReleaseID(replacementID))); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, decisionEvent(team, decision, newAssignment, appointCmd)); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, joinedEvent(team, newMember, appointCmd)); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, assignmentEvent(team, newAssignment, appointCmd)); err != nil {
			return err
		}
		if err := s.events.Emit(ctx, tx, StaffingEventCandidate{
			Type:            missionevent.EventAgentReplaced,
			MissionID:       cmd.MissionID,
			TeamID:          team.ID,
			AssignmentID:    oldAssignment.ID,
			AgentProfileID:  oldAssignment.AgentProfileID,
			AgentInstanceID: oldAssignment.AgentInstanceID,
			MissionRole:     oldAssignment.Role,
			Reason:          cmd.ReplacementReason,
			Actor:           cmd.ReplacedBy,
			IdempotencyKey:  scopedEventKey(cmd.IDempotencyKey, string(oldAssignment.ID), "replace"),
		}); err != nil {
			return err
		}
		out = newAssignment
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
