package team

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const timeFormatRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

type MissionTeamRepository interface {
	GetTeam(ctx context.Context, tx uow.Tx, missionID missioncore.MissionID) (MissionTeam, error)
	SaveTeam(ctx context.Context, tx uow.Tx, team MissionTeam) error
	GetAssignment(ctx context.Context, tx uow.Tx, assignmentID MissionAssignmentID) (MissionAssignment, error)
	SaveAssignment(ctx context.Context, tx uow.Tx, assignment MissionAssignment) error
	SaveStaffingRequest(ctx context.Context, tx uow.Tx, request StaffingRequest) error
	SaveStaffingDecision(ctx context.Context, tx uow.Tx, decision StaffingDecision) error
	AppendAudit(ctx context.Context, tx uow.Tx, audit StaffingAudit) error
}

type PlatformTeamRepository struct{}

func NewPlatformTeamRepository() *PlatformTeamRepository {
	return &PlatformTeamRepository{}
}

func (r *PlatformTeamRepository) GetTeam(ctx context.Context, tx uow.Tx, missionID missioncore.MissionID) (MissionTeam, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableTeamRecords, missionID.String())
	if err != nil {
		return MissionTeam{}, err
	}
	if !ok {
		return MissionTeam{MissionID: missionID}, nil
	}
	var team MissionTeam
	if err := decodeTeamRecord(record, &team); err != nil {
		return MissionTeam{}, err
	}
	return team, nil
}

func (r *PlatformTeamRepository) SaveTeam(ctx context.Context, tx uow.Tx, team MissionTeam) error {
	if team.MissionID.IsZero() {
		return fmt.Errorf("%w: mission_id is required", ErrInvalidArgument)
	}
	payload, err := encodeTeamPayload(team)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTeamRecords, team.MissionID.String(), db.Record{
		"mission_id": team.MissionID.String(),
		"team_id":    string(team.ID),
		"version":    float64(team.Version),
		"payload":    payload,
	})
}

func (r *PlatformTeamRepository) GetAssignment(ctx context.Context, tx uow.Tx, assignmentID MissionAssignmentID) (MissionAssignment, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableTeamAssignments, string(assignmentID))
	if err != nil {
		return MissionAssignment{}, err
	}
	if !ok {
		return MissionAssignment{}, fmt.Errorf("%w: assignment %s", ErrNotFound, assignmentID)
	}
	var assignment MissionAssignment
	if err := decodeTeamRecord(record, &assignment); err != nil {
		return MissionAssignment{}, err
	}
	return assignment, nil
}

func (r *PlatformTeamRepository) SaveAssignment(ctx context.Context, tx uow.Tx, assignment MissionAssignment) error {
	payload, err := encodeTeamPayload(assignment)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTeamAssignments, string(assignment.ID), db.Record{
		"mission_id":       assignment.MissionID.String(),
		"assignment_id":    string(assignment.ID),
		"member_id":        string(assignment.MemberID),
		"agent_profile_id": assignment.AgentProfileID.String(),
		"status":           string(assignment.Status),
		"idempotency_key":  assignment.IdempotencyKey,
		"payload":          payload,
	})
}

func (r *PlatformTeamRepository) SaveStaffingRequest(ctx context.Context, tx uow.Tx, request StaffingRequest) error {
	payload, err := encodeTeamPayload(request)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTeamStaffingRequests, string(request.ID), db.Record{
		"mission_id":      request.MissionID.String(),
		"request_id":      string(request.ID),
		"status":          string(request.Status),
		"idempotency_key": request.IdempotencyKey,
		"payload":         payload,
	})
}

func (r *PlatformTeamRepository) SaveStaffingDecision(ctx context.Context, tx uow.Tx, decision StaffingDecision) error {
	payload, err := encodeTeamPayload(decision)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTeamStaffingDecisions, string(decision.ID), db.Record{
		"decision_id":     string(decision.ID),
		"request_id":      string(decision.RequestID),
		"audit_id":        string(decision.AuditRef),
		"idempotency_key": decision.IdempotencyKey,
		"payload":         payload,
	})
}

func (r *PlatformTeamRepository) AppendAudit(ctx context.Context, tx uow.Tx, audit StaffingAudit) error {
	payload, err := encodeTeamPayload(audit)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableTeamStaffingAudits, string(audit.ID), db.Record{
		"audit_id":    string(audit.ID),
		"mission_id":  audit.MissionID.String(),
		"request_id":  string(audit.RequestID),
		"decision_id": string(audit.DecisionID),
		"created_at":  audit.CreatedAt.Format(timeFormatRFC3339Nano),
		"payload":     payload,
	})
}

func (r *PlatformTeamRepository) GetAudit(ctx context.Context, tx uow.Tx, auditID StaffingAuditID) (StaffingAudit, bool, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableTeamStaffingAudits, string(auditID))
	if err != nil {
		return StaffingAudit{}, false, err
	}
	if !ok {
		return StaffingAudit{}, false, nil
	}
	var audit StaffingAudit
	if err := decodeTeamRecord(record, &audit); err != nil {
		return StaffingAudit{}, false, err
	}
	return audit, true, nil
}

func (r *PlatformTeamRepository) FindAssignmentByIdempotencyKey(ctx context.Context, tx uow.Tx, missionID missioncore.MissionID, key string) (MissionAssignment, bool, error) {
	if missionID.IsZero() || key == "" {
		return MissionAssignment{}, false, nil
	}
	var matches []MissionAssignment
	records, err := storage.Snapshot(ctx, tx, storage.TableTeamAssignments)
	if err != nil {
		return MissionAssignment{}, false, err
	}
	for _, record := range records {
		if got, _ := record["mission_id"].(string); got != missionID.String() {
			continue
		}
		if got, _ := record["idempotency_key"].(string); got != key {
			continue
		}
		var assignment MissionAssignment
		if err := decodeTeamRecord(record, &assignment); err != nil {
			return MissionAssignment{}, false, err
		}
		matches = append(matches, assignment)
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].AssignedAt.After(matches[j].AssignedAt)
	})
	if len(matches) == 0 {
		return MissionAssignment{}, false, nil
	}
	return matches[0], true, nil
}

func encodeTeamPayload(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode mission team payload: %w", err)
	}
	return string(payload), nil
}

func decodeTeamRecord(record db.Record, out any) error {
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("mission team repository payload missing")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode mission team payload: %w", err)
	}
	return nil
}
