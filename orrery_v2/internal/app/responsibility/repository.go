package responsibility

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const (
	assignmentTable     = "responsibility_assignments"
	activationTable     = "responsibility_activations"
	attemptTable        = "responsibility_attempts"
	runtimeOutcomeTable = "responsibility_runtime_outcomes"
	auditTable          = "responsibility_audits"
	capacityLockKey     = "responsibility_agent_capacity"
)

var (
	ErrNotFound      = errors.New("responsibility record not found")
	ErrConflict      = errors.New("responsibility record conflict")
	ErrCapacityLimit = errors.New("responsibility Agent capacity is exhausted")
)

type AssignmentRecord struct {
	ResponsibilityAssignment
	MissionID       string
	SourceKind      string
	SourceID        string
	SourceStepID    string
	AcceptedAt      time.Time
	Paused          bool
	PauseReason     string
	TerminatedAt    time.Time
	TerminatedBy    string
	TerminationNote string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ActivationRecord struct {
	ResponsibilityActivation
	ClaimToken     string
	ClaimedBy      string
	ClaimExpiresAt time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AttemptRecord struct {
	ResponsibilityAttempt
	ActivationID   string
	RuntimeKind    string
	AgentProfileID string
	UsageTokens    int64
	CompletedAt    time.Time
	Error          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type RuntimeOutcomeRecord struct {
	ID               string
	TenantID         string
	TaskID           string
	RunID            string
	Outcome          AttemptOutcome
	Error            string
	TraceRef         string
	UsageTokens      int64
	ReceivedAt       time.Time
	AppliedAttemptID string
	AppliedAt        time.Time
	DeliveryCount    int
}

type AuditRecord struct {
	ID               string
	TenantID         string
	ResponsibilityID string
	EntityType       string
	EntityID         string
	Action           string
	ActorID          string
	FromState        string
	ToState          string
	Details          string
	OccurredAt       time.Time
}

type MissionTaskAssignmentInput struct {
	TenantID                  string
	MissionID                 string
	TaskID                    string
	SourceKind                string
	SourceID                  string
	SourceStepID              string
	ActorMemberID             string
	AgentProfileID            string
	Priority                  int
	AcceptBy                  time.Time
	DueAt                     time.Time
	PolicyRef                 string
	ActivationCause           ActivationCause
	ActivationDueAt           time.Time
	ActorID                   string
	Reason                    string
	ExpectedAssignmentVersion int64
}

type Repository struct {
	router *db.Router
	unit   *uow.UnitOfWork
}

func NewRepository(router *db.Router, unit *uow.UnitOfWork) *Repository {
	return &Repository{router: router, unit: unit}
}

func AssignmentID(tenantID string, kind WorkKind, workID string) string {
	return "responsibility-" + stableHex(strings.TrimSpace(tenantID), string(kind), strings.TrimSpace(workID))[:24]
}

func ActivationID(dedupeKey string) string {
	return "activation-" + stableHex(strings.TrimSpace(dedupeKey))[:24]
}

func AttemptID(activationID string, attempt int) string {
	return fmt.Sprintf("attempt-%s-%d", stableHex(strings.TrimSpace(activationID))[:20], attempt)
}

func RunIDForActivation(activationID string) string {
	return "responsibility-run-" + stableHex(strings.TrimSpace(activationID))[:24]
}

func RuntimeOutcomeID(tenantID, runID string) string {
	return "runtime-outcome-" + stableHex(strings.TrimSpace(tenantID), strings.TrimSpace(runID))[:24]
}

func (r *Repository) AssignMissionTaskTx(ctx context.Context, tx uow.Tx, input MissionTaskAssignmentInput, now time.Time) (AssignmentRecord, ActivationRecord, error) {
	if r == nil || r.unit == nil || tx == nil {
		return AssignmentRecord{}, ActivationRecord{}, ErrInvalidContract
	}
	input = normalizeAssignmentInput(input, now)
	if input.TenantID == "" || input.MissionID == "" || input.TaskID == "" || input.AgentProfileID == "" || input.AcceptBy.IsZero() || !input.ActivationCause.valid() || input.ExpectedAssignmentVersion < 0 {
		return AssignmentRecord{}, ActivationRecord{}, ErrInvalidContract
	}
	assignmentID := AssignmentID(input.TenantID, WorkKindMissionTask, input.TaskID)
	if err := lockResponsibilityAggregateTx(ctx, tx, assignmentID); err != nil {
		return AssignmentRecord{}, ActivationRecord{}, err
	}
	existing, found, err := getAssignmentTx(ctx, tx, assignmentID)
	if err != nil {
		return AssignmentRecord{}, ActivationRecord{}, err
	}
	if found && input.ExpectedAssignmentVersion > 0 && existing.Version != input.ExpectedAssignmentVersion {
		return AssignmentRecord{}, ActivationRecord{}, ErrConflict
	}
	if found && existing.AccountableAgent == input.AgentProfileID && (input.ActorMemberID == "" || existing.ActorMemberID == input.ActorMemberID) && !assignmentNeedsNewActivation(existing.State) {
		sourceChanged, err := mergeAssignmentSource(&existing, input.SourceKind, input.SourceID, input.SourceStepID)
		if err != nil {
			return AssignmentRecord{}, ActivationRecord{}, err
		}
		if sourceChanged {
			existing.UpdatedAt = now
			if err := putAssignmentTx(ctx, tx, existing); err != nil {
				return AssignmentRecord{}, ActivationRecord{}, err
			}
			if err := putAuditTx(ctx, tx, AuditRecord{
				TenantID: existing.TenantID, ResponsibilityID: existing.ID, EntityType: "assignment", EntityID: existing.ID,
				Action: AuditAssignmentSourceLinked, ActorID: input.ActorID, Details: assignmentSourceDetails(existing), OccurredAt: now,
			}); err != nil {
				return AssignmentRecord{}, ActivationRecord{}, err
			}
		}
		activation, ok, err := findActivationForVersionTx(ctx, tx, existing.ID, existing.Version)
		if err != nil {
			return AssignmentRecord{}, ActivationRecord{}, err
		}
		if ok {
			return existing, activation, nil
		}
	}
	version := int64(1)
	createdAt := now
	if found {
		version = existing.Version + 1
		createdAt = existing.CreatedAt
	}
	assignment := AssignmentRecord{
		ResponsibilityAssignment: ResponsibilityAssignment{
			ID: assignmentID, TenantID: input.TenantID, WorkKind: WorkKindMissionTask, WorkID: input.TaskID,
			ActorMemberID: input.ActorMemberID, AccountableAgent: input.AgentProfileID, State: AssignmentStateOffered, Priority: input.Priority,
			AcceptBy: input.AcceptBy, DueAt: input.DueAt, PolicyRef: input.PolicyRef, Version: version,
		},
		MissionID: input.MissionID, SourceKind: input.SourceKind, SourceID: input.SourceID, SourceStepID: input.SourceStepID,
		CreatedAt: createdAt, UpdatedAt: now,
	}
	if assignment.ActorMemberID == "" && found && existing.AccountableAgent == input.AgentProfileID {
		assignment.ActorMemberID = existing.ActorMemberID
	}
	if found {
		if _, err := mergeAssignmentSource(&assignment, existing.SourceKind, existing.SourceID, existing.SourceStepID); err != nil {
			return AssignmentRecord{}, ActivationRecord{}, err
		}
	}
	if err := assignment.Validate(); err != nil {
		return AssignmentRecord{}, ActivationRecord{}, err
	}
	activationCause := input.ActivationCause
	if found && existing.AccountableAgent != assignment.AccountableAgent {
		activationCause = ActivationCauseReassigned
	}
	dedupeKey := ActivationDedupeKey(input.TenantID, assignment.ID, activationCause, assignment.Version)
	activation := ActivationRecord{
		ResponsibilityActivation: ResponsibilityActivation{
			ID: ActivationID(dedupeKey), TenantID: input.TenantID, ResponsibilityID: assignment.ID,
			Cause: activationCause, NotBefore: input.ActivationDueAt, DedupeKey: dedupeKey,
			AssignmentVersion: assignment.Version, Status: ActivationStatusPending,
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := activation.Validate(); err != nil {
		return AssignmentRecord{}, ActivationRecord{}, err
	}
	if err := putAssignmentTx(ctx, tx, assignment); err != nil {
		return AssignmentRecord{}, ActivationRecord{}, err
	}
	if err := putActivationTx(ctx, tx, activation); err != nil {
		return AssignmentRecord{}, ActivationRecord{}, err
	}
	if found {
		if err := supersedeOlderActivationsTx(ctx, tx, assignment.ID, assignment.Version, now); err != nil {
			return AssignmentRecord{}, ActivationRecord{}, err
		}
	}
	auditAction := AuditAssignmentOffered
	auditDetails := assignment.AccountableAgent
	if found && existing.AccountableAgent != assignment.AccountableAgent {
		auditAction = AuditAssignmentReassigned
		auditDetails = existing.AccountableAgent + " -> " + assignment.AccountableAgent
		if input.Reason != "" {
			auditDetails += "; " + input.Reason
		}
	}
	if sourceDetails := assignmentSourceDetails(assignment); sourceDetails != "" {
		auditDetails += "; " + sourceDetails
	}
	if err := putAuditTx(ctx, tx, AuditRecord{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID,
		Action: auditAction, ActorID: input.ActorID, ToState: string(assignment.State), Details: auditDetails, OccurredAt: now,
	}); err != nil {
		return AssignmentRecord{}, ActivationRecord{}, err
	}
	return assignment, activation, nil
}

func (r *Repository) GetAssignment(ctx context.Context, assignmentID string) (AssignmentRecord, error) {
	if r == nil || r.unit == nil {
		return AssignmentRecord{}, ErrInvalidContract
	}
	var out AssignmentRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var ok bool
		var err error
		out, ok, err = getAssignmentTx(ctx, tx, strings.TrimSpace(assignmentID))
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		return nil
	})
	return out, err
}

func (r *Repository) GetAssignmentForWork(ctx context.Context, tenantID string, kind WorkKind, workID string) (AssignmentRecord, error) {
	return r.GetAssignment(ctx, AssignmentID(tenantID, kind, workID))
}

func (r *Repository) ListOpenAssignments(ctx context.Context, limit int) ([]AssignmentRecord, error) {
	if r == nil || r.unit == nil {
		return nil, ErrInvalidContract
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []AssignmentRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, assignmentTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var assignment AssignmentRecord
			if err := decodeRecord(record, &assignment); err != nil {
				return err
			}
			switch assignment.State {
			case AssignmentStateFulfilled, AssignmentStateDeclined, AssignmentStateReassigned, AssignmentStateTerminated:
				continue
			default:
				out = append(out, assignment)
			}
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.Before(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *Repository) ListReadyActivations(ctx context.Context, now time.Time, limit int) ([]ActivationRecord, error) {
	if r == nil || r.unit == nil {
		return nil, ErrInvalidContract
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []ActivationRecord
	priorities := map[string]int{}
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		assignments, err := uow.ListRecords(ctx, tx, assignmentTable)
		if err != nil {
			return err
		}
		for _, record := range assignments {
			var assignment AssignmentRecord
			if err := decodeRecord(record, &assignment); err != nil {
				return err
			}
			priorities[assignment.ID] = assignment.Priority
		}
		activations, err := uow.ListRecords(ctx, tx, activationTable)
		if err != nil {
			return err
		}
		for _, record := range activations {
			var activation ActivationRecord
			if err := decodeRecord(record, &activation); err != nil {
				return err
			}
			pending := activation.Status == ActivationStatusPending && !activation.NotBefore.After(now)
			expiredClaim := activation.Status == ActivationStatusClaimed && !activation.ClaimExpiresAt.IsZero() && !activation.ClaimExpiresAt.After(now)
			if pending || expiredClaim {
				out = append(out, activation)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if priorities[out[i].ResponsibilityID] != priorities[out[j].ResponsibilityID] {
			return priorities[out[i].ResponsibilityID] > priorities[out[j].ResponsibilityID]
		}
		if out[i].NotBefore.Equal(out[j].NotBefore) {
			return out[i].ID < out[j].ID
		}
		return out[i].NotBefore.Before(out[j].NotBefore)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *Repository) ClaimActivation(ctx context.Context, activationID, workerID, claimToken string, now time.Time, lease time.Duration, maxParallel int) (ActivationRecord, bool, error) {
	if r == nil || r.router == nil || strings.TrimSpace(activationID) == "" || strings.TrimSpace(workerID) == "" || strings.TrimSpace(claimToken) == "" || lease <= 0 {
		return ActivationRecord{}, false, ErrInvalidContract
	}
	if maxParallel <= 0 {
		maxParallel = 1
	}
	store, table, err := r.router.StoreForTable("core." + activationTable)
	if err != nil {
		return ActivationRecord{}, false, err
	}
	claimed := false
	var out ActivationRecord
	err = store.WithTx(ctx, func(tx db.Tx) error {
		record, ok, err := db.GetRecordTx(ctx, tx, table, activationID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := decodeRecord(record, &out); err != nil {
			return err
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, out.ResponsibilityID); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, table, activationID); err != nil {
			return err
		}
		record, ok, err = db.GetRecordTx(ctx, tx, table, activationID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := decodeRecord(record, &out); err != nil {
			return err
		}
		if out.Status == ActivationStatusClaimed && out.ClaimToken == claimToken && out.ClaimExpiresAt.After(now) {
			claimed = true
			return nil
		}
		if out.Status == ActivationStatusClaimed && !out.ClaimExpiresAt.After(now) {
			out.Status = ActivationStatusPending
			out.ClaimToken = ""
			out.ClaimedBy = ""
			out.ClaimExpiresAt = time.Time{}
		}
		if out.Status != ActivationStatusPending || out.NotBefore.After(now) {
			return nil
		}
		assignmentRecord, ok, err := db.GetRecordTx(ctx, tx, assignmentTable, out.ResponsibilityID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var assignment AssignmentRecord
		if err := decodeRecord(assignmentRecord, &assignment); err != nil {
			return err
		}
		if assignment.Version != out.AssignmentVersion || assignment.Paused || assignment.State == AssignmentStateFulfilled || assignment.State == AssignmentStateReassigned || assignment.State == AssignmentStateDeclined || assignment.State == AssignmentStateTerminated {
			out.Status = ActivationStatusSuperseded
			out.UpdatedAt = now
			return putRecordTx(ctx, tx, table, out.ID, out)
		}
		if err := db.LockRecordKeyTx(ctx, tx, capacityLockKey, assignment.TenantID+"\x00"+assignment.AccountableAgent); err != nil {
			return err
		}
		active, err := countActiveResponsibilitiesTx(ctx, tx, assignment.TenantID, assignment.AccountableAgent, activationID, now)
		if err != nil {
			return err
		}
		if active >= maxParallel {
			return ErrCapacityLimit
		}
		if err := ValidateActivationTransition(out.Status, ActivationStatusClaimed); err != nil {
			return err
		}
		out.Status = ActivationStatusClaimed
		out.ClaimToken = claimToken
		out.ClaimedBy = workerID
		out.ClaimExpiresAt = now.Add(lease)
		out.LastError = ""
		out.UpdatedAt = now
		if err := putRecordTx(ctx, tx, table, out.ID, out); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if errors.Is(err, ErrCapacityLimit) {
		return ActivationRecord{}, false, nil
	}
	return out, claimed, err
}

func (r *Repository) MarkDispatched(ctx context.Context, activationID, claimToken, runID, runtimeKind string, now time.Time) (AttemptRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(runID) == "" || strings.TrimSpace(runtimeKind) == "" {
		return AttemptRecord{}, ErrInvalidContract
	}
	var attempt AttemptRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		activation, ok, err := getActivationTx(ctx, tx, activationID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, activation.ResponsibilityID); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, activationTable, activation.ID); err != nil {
			return err
		}
		activation, ok, err = getActivationTx(ctx, tx, activationID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if activation.Status == ActivationStatusDispatched {
			existing, found, err := findAttemptByActivationTx(ctx, tx, activation.ID)
			if err != nil {
				return err
			}
			if found && existing.AgentRunID == runID {
				attempt = existing
				return nil
			}
			return ErrConflict
		}
		if activation.Status != ActivationStatusClaimed || activation.ClaimToken != strings.TrimSpace(claimToken) {
			return ErrConflict
		}
		assignment, ok, err := getAssignmentTx(ctx, tx, activation.ResponsibilityID)
		if err != nil {
			return err
		}
		if !ok || assignment.Version != activation.AssignmentVersion {
			return ErrConflict
		}
		attemptNumber, err := countAttemptsTx(ctx, tx, assignment.ID)
		if err != nil {
			return err
		}
		attemptNumber++
		attempt = AttemptRecord{
			ResponsibilityAttempt: ResponsibilityAttempt{
				ID: AttemptID(activation.ID, attemptNumber), TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
				AgentRunID: runID, ClaimToken: claimToken, Attempt: attemptNumber, StartedAt: now, HeartbeatAt: now,
			},
			ActivationID: activation.ID, RuntimeKind: runtimeKind, AgentProfileID: assignment.AccountableAgent, CreatedAt: now, UpdatedAt: now,
		}
		if err := attempt.Validate(); err != nil {
			return err
		}
		if err := ValidateActivationTransition(activation.Status, ActivationStatusDispatched); err != nil {
			return err
		}
		activation.Status = ActivationStatusDispatched
		activation.UpdatedAt = now
		if assignment.State == AssignmentStateOffered {
			if err := ValidateAssignmentTransition(assignment.State, AssignmentStateAccepted); err != nil {
				return err
			}
			assignment.State = AssignmentStateAccepted
			assignment.AcceptedAt = now
			if err := putAuditTx(ctx, tx, AuditRecord{
				TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID,
				Action: AuditAssignmentAccepted, ActorID: assignment.AccountableAgent, FromState: string(AssignmentStateOffered), ToState: string(AssignmentStateAccepted), OccurredAt: now,
			}); err != nil {
				return err
			}
		}
		if assignment.State == AssignmentStateAccepted || assignment.State == AssignmentStateWaiting || assignment.State == AssignmentStateEscalated {
			if err := ValidateAssignmentTransition(assignment.State, AssignmentStateActive); err != nil {
				return err
			}
			assignment.State = AssignmentStateActive
		}
		assignment.UpdatedAt = now
		if err := putActivationTx(ctx, tx, activation); err != nil {
			return err
		}
		if err := putAssignmentTx(ctx, tx, assignment); err != nil {
			return err
		}
		if err := putAttemptTx(ctx, tx, attempt); err != nil {
			return err
		}
		if err := putAuditTx(ctx, tx, AuditRecord{
			TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "activation", EntityID: activation.ID,
			Action: AuditActivationDispatched, FromState: string(ActivationStatusClaimed), ToState: string(activation.Status), Details: runID, OccurredAt: now,
		}); err != nil {
			return err
		}
		outcome, found, err := getRuntimeOutcomeTx(ctx, tx, assignment.TenantID, runID)
		if err != nil {
			return err
		}
		if found {
			if err := db.LockRecordKeyTx(ctx, tx, runtimeOutcomeTable, outcome.ID); err != nil {
				return err
			}
			outcome, found, err = getRuntimeOutcomeTx(ctx, tx, assignment.TenantID, runID)
			if err != nil {
				return err
			}
			if !found {
				return nil
			}
			attempt, err = applyRuntimeOutcomeToAttemptTx(ctx, tx, assignment, attempt, outcome, now)
			return err
		}
		return nil
	})
	return attempt, err
}

func (r *Repository) ReleaseClaim(ctx context.Context, activationID, claimToken string, notBefore time.Time, cause error, now time.Time) error {
	if r == nil || r.unit == nil {
		return ErrInvalidContract
	}
	return r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		activation, ok, err := getActivationTx(ctx, tx, activationID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, activation.ResponsibilityID); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, activationTable, activation.ID); err != nil {
			return err
		}
		activation, ok, err = getActivationTx(ctx, tx, activationID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if activation.Status != ActivationStatusClaimed || activation.ClaimToken != strings.TrimSpace(claimToken) {
			return ErrConflict
		}
		activation.Status = ActivationStatusPending
		activation.ClaimToken = ""
		activation.ClaimedBy = ""
		activation.ClaimExpiresAt = time.Time{}
		activation.NotBefore = notBefore
		activation.LastError = errorText(cause)
		activation.UpdatedAt = now
		return putActivationTx(ctx, tx, activation)
	})
}

func (r *Repository) SupersedeActivation(ctx context.Context, activationID string, now time.Time) error {
	if r == nil || r.unit == nil || strings.TrimSpace(activationID) == "" {
		return ErrInvalidContract
	}
	return r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		activation, ok, err := getActivationTx(ctx, tx, strings.TrimSpace(activationID))
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, activation.ResponsibilityID); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, activationTable, activation.ID); err != nil {
			return err
		}
		activation, ok, err = getActivationTx(ctx, tx, strings.TrimSpace(activationID))
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if activation.Status == ActivationStatusSuperseded || activation.Status == ActivationStatusCompleted {
			return nil
		}
		if activation.Status != ActivationStatusPending && activation.Status != ActivationStatusClaimed {
			return ErrConflict
		}
		activation.Status = ActivationStatusSuperseded
		activation.ClaimToken = ""
		activation.ClaimedBy = ""
		activation.ClaimExpiresAt = time.Time{}
		activation.UpdatedAt = now
		return putActivationTx(ctx, tx, activation)
	})
}

func (r *Repository) FindAttemptByRun(ctx context.Context, tenantID, runID string) (AttemptRecord, error) {
	if r == nil || r.unit == nil {
		return AttemptRecord{}, ErrInvalidContract
	}
	var out AttemptRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, attemptTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var candidate AttemptRecord
			if err := decodeRecord(record, &candidate); err != nil {
				return err
			}
			if candidate.TenantID == strings.TrimSpace(tenantID) && candidate.AgentRunID == strings.TrimSpace(runID) {
				out = candidate
				return nil
			}
		}
		return ErrNotFound
	})
	return out, err
}

func (r *Repository) PersistRuntimeOutcome(ctx context.Context, input RuntimeOutcomeRecord, now time.Time) (AttemptRecord, bool, error) {
	if r == nil || r.unit == nil {
		return AttemptRecord{}, false, ErrInvalidContract
	}
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.TaskID = strings.TrimSpace(input.TaskID)
	input.RunID = strings.TrimSpace(input.RunID)
	input.Error = strings.TrimSpace(input.Error)
	input.TraceRef = strings.TrimSpace(input.TraceRef)
	if input.TenantID == "" || input.TaskID == "" || input.RunID == "" || !input.Outcome.valid() || input.UsageTokens < 0 {
		return AttemptRecord{}, false, ErrInvalidContract
	}
	input.ID = RuntimeOutcomeID(input.TenantID, input.RunID)
	input.ReceivedAt = now
	input.DeliveryCount = 1
	var attempt AttemptRecord
	applied := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		candidate, candidateFound, err := findAttemptByRunTx(ctx, tx, input.TenantID, input.RunID)
		if err != nil {
			return err
		}
		if candidateFound {
			if err := lockResponsibilityAggregateTx(ctx, tx, candidate.ResponsibilityID); err != nil {
				return err
			}
		}
		if err := db.LockRecordKeyTx(ctx, tx, runtimeOutcomeTable, input.ID); err != nil {
			return err
		}
		record, ok := tx.Get(runtimeOutcomeTable, input.ID)
		if ok {
			var existing RuntimeOutcomeRecord
			if err := decodeRecord(record, &existing); err != nil {
				return err
			}
			if existing.TenantID != input.TenantID || existing.TaskID != input.TaskID || existing.RunID != input.RunID || existing.Outcome != input.Outcome {
				return ErrConflict
			}
			if existing.TraceRef == "" {
				existing.TraceRef = input.TraceRef
			}
			if input.UsageTokens > existing.UsageTokens {
				existing.UsageTokens = input.UsageTokens
			}
			existing.DeliveryCount++
			input = existing
			if err := putRuntimeOutcomeTx(ctx, tx, input); err != nil {
				return err
			}
		} else if err := putRuntimeOutcomeTx(ctx, tx, input); err != nil {
			return err
		}
		candidate, found, err := findAttemptByRunTx(ctx, tx, input.TenantID, input.RunID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		assignment, found, err := getAssignmentTx(ctx, tx, candidate.ResponsibilityID)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		attempt, err = applyRuntimeOutcomeToAttemptTx(ctx, tx, assignment, candidate, input, now)
		if err != nil {
			return err
		}
		applied = true
		return nil
	})
	return attempt, applied, err
}

func (r *Repository) CompleteAttempt(ctx context.Context, attemptID string, outcome AttemptOutcome, cause error, now time.Time) (AttemptRecord, error) {
	if r == nil || r.unit == nil || !outcome.valid() {
		return AttemptRecord{}, ErrInvalidContract
	}
	var out AttemptRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(attemptTable, strings.TrimSpace(attemptID))
		if !ok {
			return ErrNotFound
		}
		if err := decodeRecord(record, &out); err != nil {
			return err
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, out.ResponsibilityID); err != nil {
			return err
		}
		record, ok = tx.Get(attemptTable, strings.TrimSpace(attemptID))
		if !ok {
			return ErrNotFound
		}
		if err := decodeRecord(record, &out); err != nil {
			return err
		}
		var err error
		out, err = completeAttemptTx(ctx, tx, out, outcome, errorText(cause), now)
		return err
	})
	return out, err
}

func (r *Repository) EnsureRetryForAttempt(ctx context.Context, attemptID string, maxAttempts int, notBefore, now time.Time) (bool, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(attemptID) == "" || maxAttempts < 1 || notBefore.IsZero() {
		return false, ErrInvalidContract
	}
	created := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(attemptTable, strings.TrimSpace(attemptID))
		if !ok {
			return ErrNotFound
		}
		var attempt AttemptRecord
		if err := decodeRecord(record, &attempt); err != nil {
			return err
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, attempt.ResponsibilityID); err != nil {
			return err
		}
		record, ok = tx.Get(attemptTable, strings.TrimSpace(attemptID))
		if !ok {
			return ErrNotFound
		}
		if err := decodeRecord(record, &attempt); err != nil {
			return err
		}
		if attempt.Outcome == "" || attempt.Outcome == AttemptOutcomeCompleted {
			return ErrConflict
		}
		activation, ok, err := getActivationTx(ctx, tx, attempt.ActivationID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		assignment, ok, err := getAssignmentTx(ctx, tx, attempt.ResponsibilityID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if assignment.TenantID != attempt.TenantID || activation.ResponsibilityID != assignment.ID {
			return ErrConflict
		}
		if assignment.Version > activation.AssignmentVersion {
			return nil
		}
		if assignment.Version != activation.AssignmentVersion {
			return ErrConflict
		}
		if attempt.Attempt >= maxAttempts {
			if assignment.State == AssignmentStateEscalated {
				return nil
			}
			if err := ValidateAssignmentTransition(assignment.State, AssignmentStateEscalated); err != nil {
				return err
			}
			from := assignment.State
			assignment.State = AssignmentStateEscalated
			assignment.UpdatedAt = now
			if err := putAssignmentTx(ctx, tx, assignment); err != nil {
				return err
			}
			return putAuditTx(ctx, tx, AuditRecord{
				TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID,
				Action: AuditResponsibilityEscalated, FromState: string(from), ToState: string(assignment.State), Details: attempt.ID, OccurredAt: now,
			})
		}
		assignment.Version++
		if assignment.State == AssignmentStateSubmitted {
			assignment.State = AssignmentStateActive
		}
		assignment.UpdatedAt = now
		dedupeKey := ActivationDedupeKey(assignment.TenantID, assignment.ID, ActivationCauseRetryDue, assignment.Version)
		retry := ActivationRecord{
			ResponsibilityActivation: ResponsibilityActivation{
				ID: ActivationID(dedupeKey), TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
				Cause: ActivationCauseRetryDue, NotBefore: notBefore, DedupeKey: dedupeKey,
				AssignmentVersion: assignment.Version, Status: ActivationStatusPending,
			},
			CreatedAt: now, UpdatedAt: now,
		}
		if err := retry.Validate(); err != nil {
			return err
		}
		if err := putAssignmentTx(ctx, tx, assignment); err != nil {
			return err
		}
		if err := supersedeOlderActivationsTx(ctx, tx, assignment.ID, assignment.Version, now); err != nil {
			return err
		}
		if err := putActivationTx(ctx, tx, retry); err != nil {
			return err
		}
		created = true
		return putAuditTx(ctx, tx, AuditRecord{
			TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "activation", EntityID: retry.ID,
			Action: AuditActivationCreated, ToState: string(retry.Status), Details: attempt.ID, OccurredAt: now,
		})
	})
	return created, err
}

func (r *Repository) TransitionAssignment(ctx context.Context, assignmentID string, to AssignmentState, now time.Time) (AssignmentRecord, error) {
	if r == nil || r.unit == nil || !to.valid() {
		return AssignmentRecord{}, ErrInvalidContract
	}
	var out AssignmentRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := lockResponsibilityAggregateTx(ctx, tx, assignmentID); err != nil {
			return err
		}
		assignment, ok, err := getAssignmentTx(ctx, tx, assignmentID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if assignment.State == to {
			out = assignment
			return nil
		}
		if err := ValidateAssignmentTransition(assignment.State, to); err != nil {
			return err
		}
		from := assignment.State
		assignment.State = to
		assignment.UpdatedAt = now
		if err := putAssignmentTx(ctx, tx, assignment); err != nil {
			return err
		}
		action := AuditResultSubmitted
		if to == AssignmentStateFulfilled {
			action = AuditResponsibilityFulfilled
		}
		if err := putAuditTx(ctx, tx, AuditRecord{
			TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID,
			Action: action, FromState: string(from), ToState: string(to), OccurredAt: now,
		}); err != nil {
			return err
		}
		out = assignment
		return nil
	})
	return out, err
}

func (r *Repository) Reactivate(ctx context.Context, assignmentID string, cause ActivationCause, notBefore, now time.Time) (ActivationRecord, error) {
	if r == nil || r.unit == nil || !cause.valid() {
		return ActivationRecord{}, ErrInvalidContract
	}
	var out ActivationRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := lockResponsibilityAggregateTx(ctx, tx, assignmentID); err != nil {
			return err
		}
		assignment, ok, err := getAssignmentTx(ctx, tx, assignmentID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if assignment.Paused || assignment.State == AssignmentStateFulfilled || assignment.State == AssignmentStateDeclined || assignment.State == AssignmentStateReassigned || assignment.State == AssignmentStateTerminated {
			return ErrConflict
		}
		if existing, found, err := findActivationForVersionTx(ctx, tx, assignment.ID, assignment.Version); err != nil {
			return err
		} else if found && existing.Cause == cause && existing.Status != ActivationStatusCompleted && existing.Status != ActivationStatusSuperseded {
			out = existing
			return nil
		}
		assignment.Version++
		if assignment.State == AssignmentStateSubmitted {
			assignment.State = AssignmentStateActive
		}
		assignment.UpdatedAt = now
		dedupeKey := ActivationDedupeKey(assignment.TenantID, assignment.ID, cause, assignment.Version)
		activation := ActivationRecord{
			ResponsibilityActivation: ResponsibilityActivation{
				ID: ActivationID(dedupeKey), TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
				Cause: cause, NotBefore: notBefore, DedupeKey: dedupeKey, AssignmentVersion: assignment.Version, Status: ActivationStatusPending,
			},
			CreatedAt: now, UpdatedAt: now,
		}
		if err := activation.Validate(); err != nil {
			return err
		}
		if err := putAssignmentTx(ctx, tx, assignment); err != nil {
			return err
		}
		if err := supersedeOlderActivationsTx(ctx, tx, assignment.ID, assignment.Version, now); err != nil {
			return err
		}
		if err := putActivationTx(ctx, tx, activation); err != nil {
			return err
		}
		out = activation
		return nil
	})
	return out, err
}

func (r *Repository) ListAttempts(ctx context.Context, assignmentID string) ([]AttemptRecord, error) {
	if r == nil || r.unit == nil {
		return nil, ErrInvalidContract
	}
	var out []AttemptRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, attemptTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var attempt AttemptRecord
			if err := decodeRecord(record, &attempt); err != nil {
				return err
			}
			if attempt.ResponsibilityID == assignmentID {
				out = append(out, attempt)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attempt < out[j].Attempt })
	return out, nil
}

func normalizeAssignmentInput(input MissionTaskAssignmentInput, now time.Time) MissionTaskAssignmentInput {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.MissionID = strings.TrimSpace(input.MissionID)
	input.TaskID = strings.TrimSpace(input.TaskID)
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.SourceStepID = strings.TrimSpace(input.SourceStepID)
	input.ActorMemberID = strings.TrimSpace(input.ActorMemberID)
	input.AgentProfileID = strings.TrimSpace(input.AgentProfileID)
	input.PolicyRef = strings.TrimSpace(input.PolicyRef)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Priority < 0 {
		input.Priority = 0
	}
	if input.AcceptBy.IsZero() {
		input.AcceptBy = now.Add(2 * time.Minute)
	}
	if input.ActivationCause == "" {
		input.ActivationCause = ActivationCauseAssigned
	}
	if input.ActivationDueAt.IsZero() {
		input.ActivationDueAt = now
	}
	return input
}

func mergeAssignmentSource(assignment *AssignmentRecord, sourceKind, sourceID, sourceStepID string) (bool, error) {
	if assignment == nil {
		return false, ErrInvalidContract
	}
	incoming := []string{strings.TrimSpace(sourceKind), strings.TrimSpace(sourceID), strings.TrimSpace(sourceStepID)}
	current := []*string{&assignment.SourceKind, &assignment.SourceID, &assignment.SourceStepID}
	changed := false
	for index := range current {
		if *current[index] != "" && incoming[index] != "" && *current[index] != incoming[index] {
			return false, ErrConflict
		}
		if *current[index] == "" && incoming[index] != "" {
			*current[index] = incoming[index]
			changed = true
		}
	}
	return changed, nil
}

func assignmentSourceDetails(assignment AssignmentRecord) string {
	parts := make([]string, 0, 3)
	for _, value := range []string{assignment.SourceKind, assignment.SourceID, assignment.SourceStepID} {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "source=" + strings.Join(parts, "/")
}

func assignmentNeedsNewActivation(state AssignmentState) bool {
	switch state {
	case AssignmentStateFulfilled, AssignmentStateDeclined, AssignmentStateReassigned:
		return true
	default:
		return false
	}
}

func stableHex(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func getAssignmentTx(ctx context.Context, tx uow.Tx, id string) (AssignmentRecord, bool, error) {
	record, ok := tx.Get(assignmentTable, id)
	if !ok {
		return AssignmentRecord{}, false, nil
	}
	var out AssignmentRecord
	if err := decodeRecord(record, &out); err != nil {
		return AssignmentRecord{}, false, err
	}
	return out, true, ctx.Err()
}

func getActivationTx(ctx context.Context, tx uow.Tx, id string) (ActivationRecord, bool, error) {
	record, ok := tx.Get(activationTable, id)
	if !ok {
		return ActivationRecord{}, false, nil
	}
	var out ActivationRecord
	if err := decodeRecord(record, &out); err != nil {
		return ActivationRecord{}, false, err
	}
	return out, true, ctx.Err()
}

func findActivationForVersionTx(ctx context.Context, tx uow.Tx, assignmentID string, version int64) (ActivationRecord, bool, error) {
	records, err := uow.ListRecords(ctx, tx, activationTable)
	if err != nil {
		return ActivationRecord{}, false, err
	}
	for _, record := range records {
		var activation ActivationRecord
		if err := decodeRecord(record, &activation); err != nil {
			return ActivationRecord{}, false, err
		}
		if activation.ResponsibilityID == assignmentID && activation.AssignmentVersion == version {
			return activation, true, nil
		}
	}
	return ActivationRecord{}, false, ctx.Err()
}

func findAttemptByActivationTx(ctx context.Context, tx uow.Tx, activationID string) (AttemptRecord, bool, error) {
	records, err := uow.ListRecords(ctx, tx, attemptTable)
	if err != nil {
		return AttemptRecord{}, false, err
	}
	for _, record := range records {
		var attempt AttemptRecord
		if err := decodeRecord(record, &attempt); err != nil {
			return AttemptRecord{}, false, err
		}
		if attempt.ActivationID == activationID {
			return attempt, true, nil
		}
	}
	return AttemptRecord{}, false, ctx.Err()
}

func findAttemptByRunTx(ctx context.Context, tx uow.Tx, tenantID, runID string) (AttemptRecord, bool, error) {
	records, err := uow.ListRecords(ctx, tx, attemptTable)
	if err != nil {
		return AttemptRecord{}, false, err
	}
	for _, record := range records {
		var attempt AttemptRecord
		if err := decodeRecord(record, &attempt); err != nil {
			return AttemptRecord{}, false, err
		}
		if attempt.TenantID == strings.TrimSpace(tenantID) && attempt.AgentRunID == strings.TrimSpace(runID) {
			return attempt, true, nil
		}
	}
	return AttemptRecord{}, false, ctx.Err()
}

func getRuntimeOutcomeTx(ctx context.Context, tx uow.Tx, tenantID, runID string) (RuntimeOutcomeRecord, bool, error) {
	record, ok := tx.Get(runtimeOutcomeTable, RuntimeOutcomeID(tenantID, runID))
	if !ok {
		return RuntimeOutcomeRecord{}, false, nil
	}
	var outcome RuntimeOutcomeRecord
	if err := decodeRecord(record, &outcome); err != nil {
		return RuntimeOutcomeRecord{}, false, err
	}
	return outcome, true, ctx.Err()
}

func applyRuntimeOutcomeToAttemptTx(ctx context.Context, tx uow.Tx, assignment AssignmentRecord, attempt AttemptRecord, outcome RuntimeOutcomeRecord, now time.Time) (AttemptRecord, error) {
	if assignment.TenantID != outcome.TenantID || assignment.WorkID != outcome.TaskID || attempt.TenantID != outcome.TenantID || attempt.AgentRunID != outcome.RunID || attempt.ResponsibilityID != assignment.ID {
		return AttemptRecord{}, ErrConflict
	}
	var err error
	attempt, err = completeAttemptTx(ctx, tx, attempt, outcome.Outcome, outcome.Error, now)
	if err != nil {
		return AttemptRecord{}, err
	}
	if outcome.UsageTokens > attempt.UsageTokens {
		attempt.UsageTokens = outcome.UsageTokens
		attempt.UpdatedAt = now
		if err := putAttemptTx(ctx, tx, attempt); err != nil {
			return AttemptRecord{}, err
		}
	}
	if outcome.AppliedAttemptID != "" && outcome.AppliedAttemptID != attempt.ID {
		return AttemptRecord{}, ErrConflict
	}
	outcome.AppliedAttemptID = attempt.ID
	if outcome.AppliedAt.IsZero() {
		outcome.AppliedAt = now
	}
	if err := putRuntimeOutcomeTx(ctx, tx, outcome); err != nil {
		return AttemptRecord{}, err
	}
	return attempt, nil
}

func completeAttemptTx(ctx context.Context, tx uow.Tx, attempt AttemptRecord, outcome AttemptOutcome, errorMessage string, now time.Time) (AttemptRecord, error) {
	if attempt.Outcome != "" {
		if attempt.Outcome == outcome {
			return attempt, nil
		}
		return AttemptRecord{}, ErrConflict
	}
	attempt.Outcome = outcome
	attempt.Error = strings.TrimSpace(errorMessage)
	attempt.HeartbeatAt = now
	attempt.CompletedAt = now
	attempt.UpdatedAt = now
	if err := putAttemptTx(ctx, tx, attempt); err != nil {
		return AttemptRecord{}, err
	}
	activation, ok, err := getActivationTx(ctx, tx, attempt.ActivationID)
	if err != nil {
		return AttemptRecord{}, err
	}
	if ok && activation.Status == ActivationStatusDispatched {
		activation.Status = ActivationStatusCompleted
		activation.UpdatedAt = now
		if err := putActivationTx(ctx, tx, activation); err != nil {
			return AttemptRecord{}, err
		}
	}
	if err := putAuditTx(ctx, tx, AuditRecord{
		TenantID: attempt.TenantID, ResponsibilityID: attempt.ResponsibilityID, EntityType: "attempt", EntityID: attempt.ID,
		Action: AuditAttemptCompleted, ToState: string(outcome), Details: attempt.Error, OccurredAt: now,
	}); err != nil {
		return AttemptRecord{}, err
	}
	return attempt, nil
}

func countAttemptsTx(ctx context.Context, tx uow.Tx, assignmentID string) (int, error) {
	count := 0
	records, err := uow.ListRecords(ctx, tx, attemptTable)
	if err != nil {
		return 0, err
	}
	for _, record := range records {
		var attempt AttemptRecord
		if err := decodeRecord(record, &attempt); err != nil {
			return 0, err
		}
		if attempt.ResponsibilityID == assignmentID {
			count++
		}
	}
	return count, ctx.Err()
}

func countActiveResponsibilitiesTx(ctx context.Context, tx db.Tx, tenantID, agentID, excludeActivationID string, now time.Time) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload_json FROM platform_records WHERE table_name = ?`, attemptTable)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return 0, err
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return 0, err
		}
		var attempt AttemptRecord
		if err := decodeRecord(record, &attempt); err != nil {
			return 0, err
		}
		if attempt.Outcome == "" && attempt.TenantID == tenantID {
			attemptAgentID := strings.TrimSpace(attempt.AgentProfileID)
			if attemptAgentID == "" {
				assignmentRecord, ok, err := db.GetRecordTx(ctx, tx, assignmentTable, attempt.ResponsibilityID)
				if err != nil {
					return 0, err
				}
				if !ok {
					continue
				}
				var assignment AssignmentRecord
				if err := decodeRecord(assignmentRecord, &assignment); err != nil {
					return 0, err
				}
				attemptAgentID = assignment.AccountableAgent
			}
			if attemptAgentID == agentID {
				count++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	claimedRows, err := tx.QueryContext(ctx, `SELECT payload_json FROM platform_records WHERE table_name = ?`, activationTable)
	if err != nil {
		return 0, err
	}
	defer claimedRows.Close()
	for claimedRows.Next() {
		var raw string
		if err := claimedRows.Scan(&raw); err != nil {
			return 0, err
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return 0, err
		}
		var activation ActivationRecord
		if err := decodeRecord(record, &activation); err != nil {
			return 0, err
		}
		if activation.ID == excludeActivationID || activation.TenantID != tenantID || activation.Status != ActivationStatusClaimed || !activation.ClaimExpiresAt.After(now) {
			continue
		}
		assignmentRecord, ok, err := db.GetRecordTx(ctx, tx, assignmentTable, activation.ResponsibilityID)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		var assignment AssignmentRecord
		if err := decodeRecord(assignmentRecord, &assignment); err != nil {
			return 0, err
		}
		if assignment.AccountableAgent == agentID {
			count++
		}
	}
	return count, claimedRows.Err()
}

func supersedeOlderActivationsTx(ctx context.Context, tx uow.Tx, assignmentID string, currentVersion int64, now time.Time) error {
	records, err := uow.ListRecords(ctx, tx, activationTable)
	if err != nil {
		return err
	}
	for key, record := range records {
		var activation ActivationRecord
		if err := decodeRecord(record, &activation); err != nil {
			return err
		}
		if activation.ResponsibilityID != assignmentID || activation.AssignmentVersion >= currentVersion || activation.Status == ActivationStatusCompleted || activation.Status == ActivationStatusSuperseded {
			continue
		}
		activation.Status = ActivationStatusSuperseded
		activation.UpdatedAt = now
		if err := putRecordTx(ctx, tx, activationTable, key, activation); err != nil {
			return err
		}
	}
	return nil
}

func supersedeActiveActivationsTx(ctx context.Context, tx uow.Tx, assignmentID string, now time.Time) error {
	records, err := uow.ListRecords(ctx, tx, activationTable)
	if err != nil {
		return err
	}
	for key, record := range records {
		var activation ActivationRecord
		if err := decodeRecord(record, &activation); err != nil {
			return err
		}
		if activation.ResponsibilityID != assignmentID || activation.Status == ActivationStatusCompleted || activation.Status == ActivationStatusSuperseded {
			continue
		}
		activation.Status = ActivationStatusSuperseded
		activation.ClaimToken = ""
		activation.ClaimedBy = ""
		activation.ClaimExpiresAt = time.Time{}
		activation.UpdatedAt = now
		if err := putRecordTx(ctx, tx, activationTable, key, activation); err != nil {
			return err
		}
	}
	return nil
}

func putAssignmentTx(ctx context.Context, tx uow.Tx, value AssignmentRecord) error {
	return putRecordTx(ctx, tx, assignmentTable, value.ID, value)
}

func putActivationTx(ctx context.Context, tx uow.Tx, value ActivationRecord) error {
	return putRecordTx(ctx, tx, activationTable, value.ID, value)
}

func putAttemptTx(ctx context.Context, tx uow.Tx, value AttemptRecord) error {
	return putRecordTx(ctx, tx, attemptTable, value.ID, value)
}

func putRuntimeOutcomeTx(ctx context.Context, tx uow.Tx, value RuntimeOutcomeRecord) error {
	return putRecordTx(ctx, tx, runtimeOutcomeTable, value.ID, value)
}

func lockResponsibilityAggregateTx(ctx context.Context, tx db.Tx, assignmentID string) error {
	assignmentID = strings.TrimSpace(assignmentID)
	if assignmentID == "" {
		return ErrInvalidContract
	}
	return db.LockRecordKeyTx(ctx, tx, assignmentTable, assignmentID)
}

func putAuditTx(ctx context.Context, tx uow.Tx, value AuditRecord) error {
	value.TenantID = strings.TrimSpace(value.TenantID)
	value.ResponsibilityID = strings.TrimSpace(value.ResponsibilityID)
	value.EntityType = strings.TrimSpace(value.EntityType)
	value.EntityID = strings.TrimSpace(value.EntityID)
	value.Action = strings.TrimSpace(value.Action)
	if value.TenantID == "" || value.ResponsibilityID == "" || value.EntityType == "" || value.EntityID == "" || value.Action == "" || value.OccurredAt.IsZero() {
		return ErrInvalidContract
	}
	value.ID = "responsibility-audit-" + stableHex(value.TenantID, value.ResponsibilityID, value.EntityType, value.EntityID, value.Action, value.FromState, value.ToState, value.OccurredAt.UTC().Format(time.RFC3339Nano))[:24]
	return putRecordTx(ctx, tx, auditTable, value.ID, value)
}

func putRecordTx(ctx context.Context, tx db.Tx, table, key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode responsibility record: %w", err)
	}
	record := db.Record{"payload": string(payload)}
	if err := db.PutRecordTx(ctx, tx, table, key, record); err != nil {
		return err
	}
	return publishRuntimeWake(ctx, tx, table, key)
}

func decodeRecord(record db.Record, out any) error {
	raw, ok := record["payload"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return fmt.Errorf("responsibility repository payload is missing")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode responsibility record: %w", err)
	}
	return nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
