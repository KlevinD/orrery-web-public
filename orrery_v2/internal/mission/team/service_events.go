package team

import missionevent "orrery_v2/internal/mission/event"

func joinedEvent(team MissionTeam, member MissionTeamMember, cmd AppointmentCommand) StaffingEventCandidate {
	return StaffingEventCandidate{
		Type:            missionevent.EventAgentJoined,
		MissionID:       member.MissionID,
		TeamID:          team.ID,
		RequestID:       cmd.RequestID,
		MemberID:        member.ID,
		AgentProfileID:  member.AgentProfileID,
		AgentInstanceID: member.AgentInstanceID,
		AgentKind:       member.AgentKind,
		MissionRole:     cmd.Role,
		Actor:           cmd.AppointedBy,
		Reason:          cmd.Reason,
		IdempotencyKey:  scopedEventKey(cmd.IDempotencyKey, string(member.ID), "joined"),
	}
}

func assignmentEvent(team MissionTeam, assignment MissionAssignment, cmd AppointmentCommand) StaffingEventCandidate {
	return StaffingEventCandidate{
		Type:            missionevent.EventMissionAssignmentCreated,
		MissionID:       assignment.MissionID,
		TeamID:          team.ID,
		RequestID:       cmd.RequestID,
		MemberID:        assignment.MemberID,
		AssignmentID:    assignment.ID,
		AgentProfileID:  assignment.AgentProfileID,
		AgentInstanceID: assignment.AgentInstanceID,
		AgentKind:       assignment.AgentKind,
		MissionRole:     assignment.Role,
		Actor:           cmd.AppointedBy,
		Reason:          cmd.Reason,
		IdempotencyKey:  scopedEventKey(cmd.IDempotencyKey, string(assignment.ID), "assignment_created"),
	}
}

func decisionEvent(team MissionTeam, decision StaffingDecision, assignment MissionAssignment, cmd AppointmentCommand) StaffingEventCandidate {
	return StaffingEventCandidate{
		Type:            missionevent.EventStaffingDecisionRecorded,
		MissionID:       assignment.MissionID,
		TeamID:          team.ID,
		RequestID:       cmd.RequestID,
		DecisionID:      decision.ID,
		MemberID:        assignment.MemberID,
		AssignmentID:    assignment.ID,
		AgentProfileID:  assignment.AgentProfileID,
		AgentInstanceID: assignment.AgentInstanceID,
		AgentKind:       assignment.AgentKind,
		MissionRole:     assignment.Role,
		AuditRef:        decision.AuditRef,
		Actor:           cmd.AppointedBy,
		Reason:          decision.Rationale,
		IdempotencyKey:  scopedEventKey(cmd.IDempotencyKey, string(decision.ID), "staffing_decision_recorded"),
	}
}

func rejectedEvent(audit StaffingAudit, evaluation CandidateEvaluation, cmd AppointmentCommand) StaffingEventCandidate {
	return StaffingEventCandidate{
		Type:            missionevent.EventCandidateRejected,
		MissionID:       cmd.MissionID,
		RequestID:       cmd.RequestID,
		AgentProfileID:  evaluation.CandidateProfileID,
		AgentInstanceID: evaluation.CandidateInstanceID,
		MissionRole:     cmd.Role,
		AuditRef:        audit.ID,
		ReasonCode:      firstPolicyCode(evaluation.RejectionReasons),
		Reason:          cmd.Reason,
		Actor:           cmd.AppointedBy,
		IdempotencyKey:  scopedEventKey(cmd.IDempotencyKey, string(audit.ID), "candidate_rejected"),
	}
}

func releaseEvent(eventType missionevent.EventType, assignment MissionAssignment, cmd ReleaseCommand, releaseID ReleaseID) StaffingEventCandidate {
	return StaffingEventCandidate{
		Type:            eventType,
		MissionID:       assignment.MissionID,
		AssignmentID:    assignment.ID,
		AgentProfileID:  assignment.AgentProfileID,
		AgentInstanceID: assignment.AgentInstanceID,
		AgentKind:       assignment.AgentKind,
		MissionRole:     assignment.Role,
		AuditRef:        StaffingAuditID(releaseID),
		Actor:           cmd.ReleasedBy,
		Reason:          cmd.Reason,
		IdempotencyKey:  scopedEventKey(cmd.IDempotencyKey, string(assignment.ID), string(eventType)),
	}
}
