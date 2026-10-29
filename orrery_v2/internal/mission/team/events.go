package team

import (
	"context"
	"encoding/json"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/uow"
)

type StaffingEventCandidate struct {
	Type            missionevent.EventType
	MissionID       missioncore.MissionID
	TeamID          MissionTeamID
	RequestID       StaffingRequestID
	DecisionID      StaffingDecisionID
	MemberID        MissionTeamMemberID
	AssignmentID    MissionAssignmentID
	AgentProfileID  agentcore.AgentProfileID
	AgentInstanceID agentcore.AgentInstanceID
	AgentKind       agentcore.AgentKind
	MissionRole     MissionRole
	RationaleRef    string
	AuditRef        StaffingAuditID
	ReasonCode      PolicyCode
	Reason          string
	Actor           ActorRef
	IdempotencyKey  string
	CorrelationID   string
	CausationID     string
}

type StaffingEventSink struct{}

func NewStaffingEventSink() *StaffingEventSink {
	return &StaffingEventSink{}
}

func (s *StaffingEventSink) Emit(ctx context.Context, tx uow.Tx, candidate StaffingEventCandidate) error {
	if candidate.MissionID.IsZero() {
		return fmt.Errorf("%w: event mission_id is required", ErrInvalidArgument)
	}
	if candidate.Type == "" {
		return fmt.Errorf("%w: event type is required", ErrInvalidArgument)
	}
	payload, err := json.Marshal(candidate.Payload())
	if err != nil {
		return fmt.Errorf("encode staffing event payload: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"mission_id": candidate.MissionID.String(),
		"actor_ref":  candidate.eventActor(),
		"source":     "mission_team",
	})
	if err != nil {
		return fmt.Errorf("encode staffing event metadata: %w", err)
	}
	version, err := nextStaffingEventStreamVersion(ctx, tx, candidate.MissionID.String())
	if err != nil {
		return err
	}
	_, err = tx.RecordEvent(ctx, uow.EventMessage{
		StreamID:       candidate.MissionID.String(),
		StreamVersion:  version,
		EventType:      string(candidate.Type),
		EventVersion:   1,
		Payload:        payload,
		Metadata:       metadata,
		IdempotencyKey: candidate.IdempotencyKey,
		CorrelationID:  candidate.CorrelationID,
		CausationID:    candidate.CausationID,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRepositoryDependency, err)
	}
	return nil
}

func (c StaffingEventCandidate) Payload() missionevent.StaffingFactPayload {
	return missionevent.StaffingFactPayload{
		TeamID:          string(c.TeamID),
		RequestID:       string(c.RequestID),
		DecisionID:      string(c.DecisionID),
		MemberID:        string(c.MemberID),
		AssignmentID:    string(c.AssignmentID),
		AgentProfileID:  c.AgentProfileID.String(),
		AgentInstanceID: c.AgentInstanceID.String(),
		AgentKind:       string(c.AgentKind),
		MissionRole:     string(c.MissionRole),
		RationaleRef:    c.RationaleRef,
		AuditRef:        string(c.AuditRef),
		ReasonCode:      string(c.ReasonCode),
		Reason:          c.Reason,
	}
}

func (c StaffingEventCandidate) eventActor() missionevent.ActorRef {
	kind := missionevent.ActorKindSystem
	switch c.Actor.Kind {
	case ActorKindAgent:
		kind = missionevent.ActorKindAgent
	case ActorKindHuman:
		kind = missionevent.ActorKindHuman
	case ActorKindSystem:
		kind = missionevent.ActorKindSystem
	}
	id := c.Actor.ID
	if id == "" {
		id = "system"
	}
	return missionevent.ActorRef{Kind: kind, ID: id}
}

func nextStaffingEventStreamVersion(ctx context.Context, tx uow.Tx, streamID string) (int, error) {
	events, err := tx.EventStore().Load(ctx, streamID)
	if err != nil {
		return 0, err
	}
	return len(events) + 1, nil
}
