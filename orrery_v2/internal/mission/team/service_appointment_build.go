package team

import (
	"context"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *Service) buildAppointment(ctx context.Context, profile agentcore.AgentProfile, cmd AppointmentCommand, team MissionTeam) (MissionAssignment, MissionTeamMember, error) {
	instance, err := s.provisioner.GetOrCreateInstance(ctx, profile, agentcore.AgentInstanceScope{MissionID: cmd.MissionID.String(), ReuseWarm: true, IsolationPolicy: "mission"})
	if err != nil {
		return MissionAssignment{}, MissionTeamMember{}, fmt.Errorf("%w: %v", ErrProvisioningFailed, err)
	}
	if instance.ID != agentcore.AgentInstanceID(profile.ID) {
		return MissionAssignment{}, MissionTeamMember{}, fmt.Errorf("%w: provisioned agent identity %s must match stable profile %s", ErrProvisioningFailed, instance.ID, profile.ID)
	}
	memberID, err := s.newID("member")
	if err != nil {
		return MissionAssignment{}, MissionTeamMember{}, err
	}
	assignmentID, err := s.newID("assignment")
	if err != nil {
		return MissionAssignment{}, MissionTeamMember{}, err
	}
	if team.ID == "" {
		team.ID = MissionTeamID("team-" + cmd.MissionID.String())
	}
	now := s.now()
	member := MissionTeamMember{
		ID:              MissionTeamMemberID(memberID),
		MissionID:       cmd.MissionID,
		AgentInstanceID: instance.ID,
		AgentProfileID:  profile.ID,
		AgentKind:       profile.AgentKind,
		JoinedAt:        now,
		JoinedBy:        cmd.AppointedBy,
		Status:          MemberStatusActive,
		Visibility:      TeamVisibilityMission,
		AssignmentRefs:  []AssignmentRef{{ID: MissionAssignmentID(assignmentID), Role: cmd.Role, Status: AssignmentStatusActive}},
	}
	assignment := MissionAssignment{
		ID:                     MissionAssignmentID(assignmentID),
		MissionID:              cmd.MissionID,
		MemberID:               member.ID,
		AgentInstanceID:        instance.ID,
		AgentProfileID:         profile.ID,
		AgentKind:              profile.AgentKind,
		Role:                   cmd.Role,
		ResponsibilityBoundary: cmd.Responsibility,
		AuthorityScope:         firstNonEmpty(cmd.AuthorityScope, profile.OrgIdentity.AuthorityScope),
		AssignedBy:             cmd.AppointedBy,
		AssignedAt:             now,
		Status:                 AssignmentStatusActive,
		IdempotencyKey:         cmd.IDempotencyKey,
	}
	return assignment, member, nil
}

func (s *Service) appointmentDirectoryEntry(ctx context.Context, profileID agentcore.AgentProfileID) (agentcore.AgentDirectoryEntry, CandidateEvaluation, error) {
	page, err := s.profileRepo.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		EnabledOnly:     false,
		IncludeAI:       true,
		IncludeHumans:   true,
		Sort:            agentcore.DirectorySortScoreDesc,
		MinQualityScore: 0,
	})
	if err != nil {
		return agentcore.AgentDirectoryEntry{}, CandidateEvaluation{}, err
	}
	for _, entry := range page.Entries {
		if entry.AgentProfileID == profileID {
			return entry, CandidateEvaluation{}, nil
		}
	}
	for _, rejection := range page.Rejected {
		if rejection.AgentProfileID == profileID {
			return agentcore.AgentDirectoryEntry{}, evaluationFromDirectoryRejection(rejection), nil
		}
	}
	return agentcore.AgentDirectoryEntry{}, CandidateEvaluation{}, fmt.Errorf("%w: directory entry %s", ErrNotFound, profileID)
}

func staffingRequestFromAppointment(cmd AppointmentCommand) StaffingRequest {
	allowAI := cmd.AllowAI
	if !cmd.AllowAI && !cmd.AllowHuman {
		allowAI = true
	}
	desiredRoles := []MissionRole{cmd.Role}
	if cmd.AllowOrganizationalRoleBridge {
		desiredRoles = nil
	}
	return StaffingRequest{
		ID:                       cmd.RequestID,
		MissionID:                cmd.MissionID,
		RequestedBy:              cmd.AppointedBy,
		DesiredRoles:             desiredRoles,
		RequiredSkills:           append([]agentcore.SkillRef(nil), cmd.RequiredSkills...),
		RequiredTools:            append([]agentcore.ToolRef(nil), cmd.RequiredTools...),
		AuthorityRequirements:    cmd.AuthorityScope,
		AvailabilityWindow:       cmd.AvailabilityWindow,
		BudgetConstraints:        cmd.BudgetConstraints,
		QualityConstraints:       cmd.QualityConstraints,
		RiskConstraints:          cmd.RiskConstraints,
		AllowUnknownAvailability: cmd.AllowUnknownAvailability,
		AllowHuman:               cmd.AllowHuman,
		AllowAI:                  allowAI,
		Reason:                   cmd.Reason,
		IdempotencyKey:           cmd.IDempotencyKey,
	}
}

func (s *Service) appointmentDecision(cmd AppointmentCommand, assignment MissionAssignment) (StaffingDecision, StaffingAudit, error) {
	decisionID, err := s.newID("staffing-decision")
	if err != nil {
		return StaffingDecision{}, StaffingAudit{}, err
	}
	auditID, err := s.newID("staffing-audit")
	if err != nil {
		return StaffingDecision{}, StaffingAudit{}, err
	}
	appointmentID, err := s.newID("appointment")
	if err != nil {
		return StaffingDecision{}, StaffingAudit{}, err
	}
	now := s.now()
	rationale := firstNonEmpty(cmd.Reason, "candidate satisfied staffing policy")
	decision := StaffingDecision{
		ID:                      StaffingDecisionID(decisionID),
		RequestID:               cmd.RequestID,
		SelectedAgentProfileID:  assignment.AgentProfileID,
		SelectedAgentInstanceID: assignment.AgentInstanceID,
		Appointment: Appointment{
			ID:              AppointmentID(appointmentID),
			MissionID:       assignment.MissionID,
			MemberID:        assignment.MemberID,
			AssignmentID:    assignment.ID,
			AgentProfileID:  assignment.AgentProfileID,
			AgentInstanceID: assignment.AgentInstanceID,
			Role:            assignment.Role,
			AppointedBy:     cmd.AppointedBy,
			AppointedAt:     assignment.AssignedAt,
		},
		Rationale:      rationale,
		DecidedBy:      cmd.AppointedBy,
		DecidedAt:      now,
		AuditRef:       StaffingAuditID(auditID),
		IdempotencyKey: cmd.IDempotencyKey,
	}
	audit := StaffingAudit{
		ID:                StaffingAuditID(auditID),
		MissionID:         cmd.MissionID,
		RequestID:         cmd.RequestID,
		DecisionID:        decision.ID,
		Action:            "appoint",
		SelectedRationale: rationale,
		CreatedAt:         now,
		Metadata:          map[string]string{"profile_id": assignment.AgentProfileID.String(), "assignment_id": string(assignment.ID)},
	}
	return decision, audit, nil
}

func (s *Service) rejectionAudit(cmd AppointmentCommand, evaluation CandidateEvaluation) (StaffingAudit, error) {
	auditID, err := s.newID("staffing-audit")
	if err != nil {
		return StaffingAudit{}, err
	}
	return StaffingAudit{
		ID:                  StaffingAuditID(auditID),
		MissionID:           cmd.MissionID,
		RequestID:           cmd.RequestID,
		Action:              "reject",
		RejectedEvaluations: []CandidateEvaluation{evaluation},
		CreatedAt:           s.now(),
		Metadata:            map[string]string{"profile_id": evaluation.CandidateProfileID.String()},
	}, nil
}
