package responsibility

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const blockerTable = "responsibility_blockers"

const supervisionAlertTable = "responsibility_supervision_alerts"

const controlCommandTable = "responsibility_control_commands"

type ControlCommandRecord struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	IdempotencyKey   string    `json:"idempotency_key"`
	Fingerprint      string    `json:"fingerprint"`
	Action           string    `json:"action"`
	ActorID          string    `json:"actor_id"`
	ResponsibilityID string    `json:"responsibility_id"`
	ResultVersion    int64     `json:"result_version"`
	CreatedAt        time.Time `json:"created_at"`
}

type BlockerRecord struct {
	ResponsibilityBlocker
	Resolution             string    `json:"resolution"`
	ResolvedBy             string    `json:"resolved_by"`
	ResolvedAt             time.Time `json:"resolved_at"`
	ApprovalAgentProfileID string    `json:"approval_agent_profile_id,omitempty"`
	ApprovalPolicyID       string    `json:"approval_policy_id,omitempty"`
	ApprovalPolicyVersion  int64     `json:"approval_policy_version,omitempty"`
	ApprovalScopeDigest    string    `json:"approval_scope_digest,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	Version                int64     `json:"version"`
}

type ApprovalGrant struct {
	AgentProfileID string
	PolicyID       string
	PolicyVersion  int64
	ScopeDigest    string
}

type SupervisionAlertKind string

const (
	SupervisionAlertDeadlineWarning SupervisionAlertKind = "deadline_warning"
	SupervisionAlertOverdue         SupervisionAlertKind = "overdue"
)

type SupervisionAlertStatus string

const (
	SupervisionAlertOpen     SupervisionAlertStatus = "open"
	SupervisionAlertResolved SupervisionAlertStatus = "resolved"
)

type SupervisionAlertRecord struct {
	ID               string                 `json:"id"`
	TenantID         string                 `json:"tenant_id"`
	ResponsibilityID string                 `json:"responsibility_id"`
	Kind             SupervisionAlertKind   `json:"kind"`
	Severity         string                 `json:"severity"`
	RequestedFrom    string                 `json:"requested_from"`
	DueAt            time.Time              `json:"due_at"`
	NextEscalationAt time.Time              `json:"next_escalation_at,omitempty"`
	EscalationLevel  int                    `json:"escalation_level"`
	Status           SupervisionAlertStatus `json:"status"`
	Resolution       string                 `json:"resolution,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
	ResolvedAt       time.Time              `json:"resolved_at,omitempty"`
	Version          int64                  `json:"version"`
}

type OpenBlockerInput struct {
	TenantID         string
	ResponsibilityID string
	Kind             BlockerKind
	Needed           string
	RequestedFrom    string
	RecheckAt        time.Time
	DedupeKey        string
	// ApprovalScopeDigest binds the canonical runtime-approval blocker to the
	// exact task scopes evaluated by the coordinator. It lets an already
	// durable autonomous-plan execution grant resolve a concurrently opened
	// blocker in the same transaction.
	ApprovalScopeDigest string
}

type AssignmentControl struct {
	TenantID        string
	AssignmentID    string
	ActorID         string
	Reason          string
	ExpectedVersion int64
	AgentProfileID  string
	AcceptBy        time.Time
	IdempotencyKey  string
}

type AssignmentListQuery struct {
	TenantID string
	AgentID  string
	States   []AssignmentState
	Limit    int
}

func BlockerID(tenantID, responsibilityID, dedupeKey string) string {
	return "responsibility-blocker-" + stableHex(strings.TrimSpace(tenantID), strings.TrimSpace(responsibilityID), strings.TrimSpace(dedupeKey))[:24]
}

func SupervisionAlertID(tenantID, responsibilityID string, kind SupervisionAlertKind) string {
	return "responsibility-alert-" + stableHex(strings.TrimSpace(tenantID), strings.TrimSpace(responsibilityID), string(kind))[:24]
}

func (r *Repository) SavePolicy(ctx context.Context, policy ResponsibilityPolicyRecord, actorID, idempotencyKey string, expectedVersion int64, now time.Time) (ResponsibilityPolicyRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(actorID) == "" || strings.TrimSpace(idempotencyKey) == "" || expectedVersion < 1 {
		return ResponsibilityPolicyRecord{}, ErrInvalidContract
	}
	policy.TenantID = strings.TrimSpace(policy.TenantID)
	policy.AgentProfileID = strings.TrimSpace(policy.AgentProfileID)
	policy.ID = PolicyID(policy.TenantID, policy.AgentProfileID, policy.WorkKind)
	policy.ToolScopes = cleanPolicyStrings(policy.ToolScopes)
	policy.DataScopes = cleanPolicyStrings(policy.DataScopes)
	policy.AllowedRiskLevels = cleanPolicyStrings(policy.AllowedRiskLevels)
	payload, err := json.Marshal(policy)
	if err != nil {
		return ResponsibilityPolicyRecord{}, err
	}
	commandID := operationCommandID(policy.TenantID, idempotencyKey)
	fingerprint := stableHex("policy.update", policy.ID, strings.TrimSpace(actorID), string(payload), fmt.Sprint(expectedVersion))
	var out ResponsibilityPolicyRecord
	err = r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if existingCommand, found, err := getControlCommandTx(tx, commandID); err != nil {
			return err
		} else if found {
			if existingCommand.TenantID != policy.TenantID || existingCommand.Action != "policy.update" || existingCommand.Fingerprint != fingerprint {
				return ErrConflict
			}
			stored, found, err := getPolicyTx(tx, existingCommand.ResponsibilityID)
			if err != nil {
				return err
			}
			if !found || stored.Version != existingCommand.ResultVersion {
				return ErrConflict
			}
			out = stored
			return nil
		}
		if err := db.LockRecordKeyTx(ctx, tx, policyTable, policy.ID); err != nil {
			return err
		}
		existing, found, err := getPolicyTx(tx, policy.ID)
		if err != nil {
			return err
		}
		if found && expectedVersion > 0 && existing.Version != expectedVersion {
			return ErrConflict
		}
		if !found && expectedVersion != DefaultPolicy(policy.TenantID, policy.AgentProfileID, policy.WorkKind).Version {
			return ErrConflict
		}
		if found {
			policy.Version = existing.Version + 1
			policy.CreatedAt = existing.CreatedAt
		} else {
			policy.Version = expectedVersion + 1
			policy.CreatedAt = now
		}
		policy.UpdatedAt = now
		if err := policy.Validate(); err != nil {
			return err
		}
		if err := putRecordTx(ctx, tx, policyTable, policy.ID, policy); err != nil {
			return err
		}
		if err := putAuditTx(ctx, tx, AuditRecord{
			TenantID: policy.TenantID, ResponsibilityID: policy.ID, EntityType: "policy", EntityID: policy.ID,
			Action: AuditPolicyUpdated, ActorID: strings.TrimSpace(actorID), Details: policy.AgentProfileID, OccurredAt: now,
		}); err != nil {
			return err
		}
		if err := putControlCommandTx(ctx, tx, ControlCommandRecord{
			ID: commandID, TenantID: policy.TenantID, IdempotencyKey: strings.TrimSpace(idempotencyKey), Fingerprint: fingerprint,
			Action: "policy.update", ActorID: strings.TrimSpace(actorID), ResponsibilityID: policy.ID, ResultVersion: policy.Version, CreatedAt: now,
		}); err != nil {
			return err
		}
		out = policy
		return nil
	})
	return out, err
}

func (r *Repository) GetPolicy(ctx context.Context, tenantID, agentID string, kind WorkKind) (ResponsibilityPolicyRecord, error) {
	policy, found, err := r.lookupPolicy(ctx, tenantID, agentID, kind)
	if err != nil {
		return ResponsibilityPolicyRecord{}, err
	}
	if !found {
		return ResponsibilityPolicyRecord{}, ErrNotFound
	}
	return policy, nil
}

func (r *Repository) lookupPolicy(ctx context.Context, tenantID, agentID string, kind WorkKind) (ResponsibilityPolicyRecord, bool, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(agentID) == "" || !kind.valid() {
		return ResponsibilityPolicyRecord{}, false, ErrInvalidContract
	}
	var out ResponsibilityPolicyRecord
	found := false
	err := r.unit.Do(ctx, func(_ context.Context, tx uow.Tx) error {
		var err error
		out, found, err = getPolicyTx(tx, PolicyID(tenantID, agentID, kind))
		return err
	})
	return out, found, err
}

func (r *Repository) ResolvePolicy(ctx context.Context, assignment AssignmentRecord) (ResponsibilityPolicyRecord, error) {
	policy, found, err := r.lookupPolicy(ctx, assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	if err != nil {
		return ResponsibilityPolicyRecord{}, err
	}
	if found {
		return policy, nil
	}
	return DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind), nil
}

func (r *Repository) ListAssignments(ctx context.Context, query AssignmentListQuery) ([]AssignmentRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(query.TenantID) == "" {
		return nil, ErrInvalidContract
	}
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 100
	}
	wanted := map[AssignmentState]bool{}
	for _, state := range query.States {
		if !state.valid() {
			return nil, ErrInvalidContract
		}
		wanted[state] = true
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
			if assignment.TenantID != strings.TrimSpace(query.TenantID) || (strings.TrimSpace(query.AgentID) != "" && assignment.AccountableAgent != strings.TrimSpace(query.AgentID)) {
				continue
			}
			if len(wanted) > 0 && !wanted[assignment.State] {
				continue
			}
			out = append(out, assignment)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func (r *Repository) ListAssignmentsForAgents(
	ctx context.Context,
	tenantID string,
	agentIDs []string,
	states []AssignmentState,
) (map[string][]AssignmentRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidContract
	}
	wantedAgents := make(map[string]struct{}, len(agentIDs))
	for _, agentID := range agentIDs {
		if agentID = strings.TrimSpace(agentID); agentID != "" {
			wantedAgents[agentID] = struct{}{}
		}
	}
	if len(wantedAgents) == 0 {
		return map[string][]AssignmentRecord{}, nil
	}
	wantedStates := make(map[AssignmentState]struct{}, len(states))
	for _, state := range states {
		if !state.valid() {
			return nil, ErrInvalidContract
		}
		wantedStates[state] = struct{}{}
	}
	out := make(map[string][]AssignmentRecord, len(wantedAgents))
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
			if assignment.TenantID != strings.TrimSpace(tenantID) {
				continue
			}
			if _, ok := wantedAgents[assignment.AccountableAgent]; !ok {
				continue
			}
			if len(wantedStates) > 0 {
				if _, ok := wantedStates[assignment.State]; !ok {
					continue
				}
			}
			out[assignment.AccountableAgent] = append(out[assignment.AccountableAgent], assignment)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for agentID := range wantedAgents {
		assignments := out[agentID]
		sort.Slice(assignments, func(i, j int) bool {
			if assignments[i].Priority != assignments[j].Priority {
				return assignments[i].Priority > assignments[j].Priority
			}
			if !assignments[i].UpdatedAt.Equal(assignments[j].UpdatedAt) {
				return assignments[i].UpdatedAt.After(assignments[j].UpdatedAt)
			}
			return assignments[i].ID < assignments[j].ID
		})
		out[agentID] = assignments
	}
	return out, nil
}

func (r *Repository) ListAssignmentsWithLiveAttempts(ctx context.Context, limit int) ([]AssignmentRecord, error) {
	if r == nil || r.unit == nil {
		return nil, ErrInvalidContract
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []AssignmentRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		live := map[string]bool{}
		attempts, err := uow.ListRecords(ctx, tx, attemptTable)
		if err != nil {
			return err
		}
		for _, record := range attempts {
			var attempt AttemptRecord
			if err := decodeRecord(record, &attempt); err != nil {
				return err
			}
			if attempt.Outcome == "" {
				live[attempt.ResponsibilityID] = true
			}
		}
		assignments, err := uow.ListRecords(ctx, tx, assignmentTable)
		if err != nil {
			return err
		}
		for _, record := range assignments {
			var assignment AssignmentRecord
			if err := decodeRecord(record, &assignment); err != nil {
				return err
			}
			if live[assignment.ID] {
				out = append(out, assignment)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *Repository) OpenBlocker(ctx context.Context, input OpenBlockerInput, now time.Time) (BlockerRecord, error) {
	return r.openBlocker(ctx, input, now, false)
}

// Runtime attempt failures are immutable facts. Once their blocker has been
// resolved, replaying the same fact must not undo that resolution. Other
// blockers may still reopen when their external condition becomes false again.
func (r *Repository) openBlocker(ctx context.Context, input OpenBlockerInput, now time.Time, preserveResolution bool) (BlockerRecord, error) {
	if r == nil || r.unit == nil {
		return BlockerRecord{}, ErrInvalidContract
	}
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.ResponsibilityID = strings.TrimSpace(input.ResponsibilityID)
	input.Needed = strings.TrimSpace(input.Needed)
	input.RequestedFrom = strings.TrimSpace(input.RequestedFrom)
	input.DedupeKey = strings.TrimSpace(input.DedupeKey)
	input.ApprovalScopeDigest = strings.TrimSpace(input.ApprovalScopeDigest)
	if input.DedupeKey == "" {
		input.DedupeKey = string(input.Kind) + "|" + input.Needed + "|" + input.RequestedFrom
	}
	id := BlockerID(input.TenantID, input.ResponsibilityID, input.DedupeKey)
	var out BlockerRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := lockResponsibilityAggregateTx(ctx, tx, input.ResponsibilityID); err != nil {
			return err
		}
		assignment, found, err := getAssignmentTx(ctx, tx, input.ResponsibilityID)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if assignment.TenantID != input.TenantID || assignmentTerminal(assignment.State) {
			return ErrConflict
		}
		existing, found, err := getBlockerTx(tx, id)
		if err != nil {
			return err
		}
		if preserveResolution && found && existing.Status == BlockerStatusResolved {
			out = existing
			return nil
		}
		blocker := existing
		if !found || existing.Status != BlockerStatusOpen {
			blocker = BlockerRecord{ResponsibilityBlocker: ResponsibilityBlocker{
				ID: id, TenantID: input.TenantID, ResponsibilityID: input.ResponsibilityID, DedupeKey: input.DedupeKey, Kind: input.Kind,
				Needed: input.Needed, RequestedFrom: input.RequestedFrom, RecheckAt: input.RecheckAt,
				Status: BlockerStatusOpen,
			}, CreatedAt: now, UpdatedAt: now, Version: 1}
			if found {
				blocker.CreatedAt = existing.CreatedAt
				blocker.Version = existing.Version + 1
			}
			if err := blocker.Validate(); err != nil {
				return err
			}
			invalidateActivation := assignment.State != AssignmentStateOffered
			if assignment.State == AssignmentStateActive || assignment.State == AssignmentStateAccepted {
				assignment.State = AssignmentStateWaiting
			}
			assignment.UpdatedAt = now
			if invalidateActivation {
				assignment.Version++
			}
			if err := putAssignmentTx(ctx, tx, assignment); err != nil {
				return err
			}
			if invalidateActivation {
				if err := supersedeOlderActivationsTx(ctx, tx, assignment.ID, assignment.Version, now); err != nil {
					return err
				}
			}
			if err := putRecordTx(ctx, tx, blockerTable, blocker.ID, blocker); err != nil {
				return err
			}
			if err := putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
				EntityType: "blocker", EntityID: blocker.ID, Action: AuditBlockerOpened, ToState: string(blocker.Status), Details: blocker.Needed, OccurredAt: now}); err != nil {
				return err
			}
		}
		if input.Kind == BlockerKindApproval && input.DedupeKey == "dispatch-policy-approval" && input.ApprovalScopeDigest != "" {
			policy, err := resolvePolicyTx(tx, assignment)
			if err != nil {
				return err
			}
			grant, granted, err := autoplanExecutionGrantForScopeDigestTx(ctx, tx, assignment, policy, input.ApprovalScopeDigest)
			if err != nil {
				return err
			}
			if granted {
				if err := resolveAutoplanExecutionApprovalBlockerTx(ctx, tx, assignment, grant, now); err != nil {
					return err
				}
				updated, present, err := getBlockerTx(tx, blocker.ID)
				if err != nil {
					return err
				}
				if !present {
					return ErrConflict
				}
				out = updated
				return nil
			}
		}
		out = blocker
		return nil
	})
	return out, err
}

func (r *Repository) ListBlockers(ctx context.Context, tenantID, responsibilityID string, openOnly bool) ([]BlockerRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidContract
	}
	var out []BlockerRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, blockerTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var blocker BlockerRecord
			if err := decodeRecord(record, &blocker); err != nil {
				return err
			}
			if blocker.TenantID != strings.TrimSpace(tenantID) || (strings.TrimSpace(responsibilityID) != "" && blocker.ResponsibilityID != strings.TrimSpace(responsibilityID)) || (openOnly && blocker.Status != BlockerStatusOpen) {
				continue
			}
			out = append(out, blocker)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == BlockerStatusOpen
		}
		if !out[i].RecheckAt.Equal(out[j].RecheckAt) {
			return out[i].RecheckAt.Before(out[j].RecheckAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *Repository) GetBlocker(ctx context.Context, tenantID, blockerID string) (BlockerRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(blockerID) == "" {
		return BlockerRecord{}, ErrInvalidContract
	}
	var out BlockerRecord
	err := r.unit.Do(ctx, func(_ context.Context, tx uow.Tx) error {
		blocker, found, err := getBlockerTx(tx, strings.TrimSpace(blockerID))
		if err != nil {
			return err
		}
		if !found || blocker.TenantID != strings.TrimSpace(tenantID) {
			return ErrNotFound
		}
		out = blocker
		return nil
	})
	return out, err
}

func (r *Repository) ListActivations(ctx context.Context, tenantID, responsibilityID string) ([]ActivationRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(responsibilityID) == "" {
		return nil, ErrInvalidContract
	}
	var out []ActivationRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, activationTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var activation ActivationRecord
			if err := decodeRecord(record, &activation); err != nil {
				return err
			}
			if activation.TenantID == strings.TrimSpace(tenantID) && activation.ResponsibilityID == strings.TrimSpace(responsibilityID) {
				out = append(out, activation)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AssignmentVersion != out[j].AssignmentVersion {
			return out[i].AssignmentVersion > out[j].AssignmentVersion
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *Repository) RuntimeOutcomeDeliveryStats(ctx context.Context, assignment AssignmentRecord) (deliveries, duplicates int, err error) {
	if r == nil || r.unit == nil || strings.TrimSpace(assignment.TenantID) == "" || strings.TrimSpace(assignment.ID) == "" {
		return 0, 0, ErrInvalidContract
	}
	err = r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, attemptTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var attempt AttemptRecord
			if err := decodeRecord(record, &attempt); err != nil {
				return err
			}
			if attempt.ResponsibilityID != assignment.ID || attempt.TenantID != assignment.TenantID {
				continue
			}
			outcomeRecord, found := tx.Get(runtimeOutcomeTable, RuntimeOutcomeID(assignment.TenantID, attempt.AgentRunID))
			if !found {
				continue
			}
			var outcome RuntimeOutcomeRecord
			if err := decodeRecord(outcomeRecord, &outcome); err != nil {
				return err
			}
			count := outcome.DeliveryCount
			if count < 1 {
				count = 1
			}
			deliveries += count
			duplicates += count - 1
		}
		return nil
	})
	return deliveries, duplicates, err
}

func (r *Repository) ListAudits(ctx context.Context, tenantID, responsibilityID string, limit int) ([]AuditRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(responsibilityID) == "" {
		return nil, ErrInvalidContract
	}
	tenantID = strings.TrimSpace(tenantID)
	responsibilityID = strings.TrimSpace(responsibilityID)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []AuditRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := listResponsibilityAuditRecordsTx(ctx, tx, tenantID, responsibilityID)
		if err != nil {
			return err
		}
		for _, record := range records {
			var audit AuditRecord
			if err := decodeRecord(record, &audit); err != nil {
				return err
			}
			if audit.TenantID == tenantID && audit.ResponsibilityID == responsibilityID {
				out = append(out, audit)
			}
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OccurredAt.Equal(out[j].OccurredAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].OccurredAt.After(out[j].OccurredAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *Repository) ListAuditsForResponsibilities(ctx context.Context, tenantID string, responsibilityIDs []string, limit int) (map[string][]AuditRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidContract
	}
	tenantID = strings.TrimSpace(tenantID)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	wanted := make(map[string]struct{}, len(responsibilityIDs))
	ids := make([]string, 0, len(responsibilityIDs))
	for _, responsibilityID := range responsibilityIDs {
		responsibilityID = strings.TrimSpace(responsibilityID)
		if responsibilityID == "" {
			continue
		}
		if _, exists := wanted[responsibilityID]; exists {
			continue
		}
		wanted[responsibilityID] = struct{}{}
		ids = append(ids, responsibilityID)
	}
	out := make(map[string][]AuditRecord, len(ids))
	for _, responsibilityID := range ids {
		out[responsibilityID] = make([]AuditRecord, 0)
	}
	if len(ids) == 0 {
		return out, nil
	}
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		const batchSize = 200
		for start := 0; start < len(ids); start += batchSize {
			end := min(start+batchSize, len(ids))
			records, err := listResponsibilityAuditRecordsForIDsTx(ctx, tx, tenantID, ids[start:end])
			if err != nil {
				return err
			}
			for _, record := range records {
				var audit AuditRecord
				if err := decodeRecord(record, &audit); err != nil {
					return err
				}
				if audit.TenantID != tenantID {
					continue
				}
				if _, exists := wanted[audit.ResponsibilityID]; !exists {
					continue
				}
				out[audit.ResponsibilityID] = append(out[audit.ResponsibilityID], audit)
			}
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	for responsibilityID, audits := range out {
		sort.Slice(audits, func(i, j int) bool {
			if audits[i].OccurredAt.Equal(audits[j].OccurredAt) {
				return audits[i].ID > audits[j].ID
			}
			return audits[i].OccurredAt.After(audits[j].OccurredAt)
		})
		if len(audits) > limit {
			audits = audits[:limit]
		}
		out[responsibilityID] = audits
	}
	return out, nil
}

func listResponsibilityAuditRecordsTx(ctx context.Context, tx uow.Tx, tenantID, responsibilityID string) ([]db.Record, error) {
	return listResponsibilityAuditRecordsForIDsTx(ctx, tx, tenantID, []string{responsibilityID})
}

func listResponsibilityAuditRecordsForIDsTx(ctx context.Context, tx uow.Tx, tenantID string, responsibilityIDs []string) ([]db.Record, error) {
	if len(responsibilityIDs) == 0 {
		return nil, nil
	}
	dialect := db.DialectSQLite
	if dialecter, ok := tx.(interface{ Dialect() db.Dialect }); ok {
		dialect = dialecter.Dialect()
	}
	tenantExpr := responsibilityRecordTextExpr(dialect, "TenantID")
	responsibilityExpr := responsibilityRecordTextExpr(dialect, "ResponsibilityID")
	placeholders := make([]string, len(responsibilityIDs))
	args := make([]any, 0, len(responsibilityIDs)+2)
	args = append(args, auditTable, tenantID)
	for index, responsibilityID := range responsibilityIDs {
		placeholders[index] = "?"
		args = append(args, responsibilityID)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT payload_json
FROM platform_records
WHERE table_name = ? AND `+tenantExpr+` = ? AND `+responsibilityExpr+` IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("query responsibility audits: %w", err)
	}
	defer rows.Close()
	records := make([]db.Record, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan responsibility audit: %w", err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("decode responsibility audit record: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate responsibility audits: %w", err)
	}
	return records, nil
}

func responsibilityRecordTextExpr(dialect db.Dialect, field string) string {
	if dialect == db.DialectPostgres {
		return "COALESCE(NULLIF(payload_json::jsonb ->> '" + field + "', ''), NULLIF(((NULLIF(payload_json::jsonb ->> 'payload', ''))::jsonb ->> '" + field + "'), ''), '')"
	}
	return "COALESCE(NULLIF(CAST(json_extract(payload_json, '$." + field + "') AS TEXT), ''), NULLIF(CAST(json_extract(json_extract(payload_json, '$.payload'), '$." + field + "') AS TEXT), ''), '')"
}

func (r *Repository) ListDueBlockers(ctx context.Context, now time.Time, limit int) ([]BlockerRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	all, err := r.listAllBlockers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BlockerRecord, 0)
	for _, blocker := range all {
		if blocker.Status == BlockerStatusOpen && !blocker.RecheckAt.After(now) {
			out = append(out, blocker)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RecheckAt.Equal(out[j].RecheckAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].RecheckAt.Before(out[j].RecheckAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *Repository) EnsureSupervisionAlert(ctx context.Context, assignment AssignmentRecord, policy ResponsibilityPolicyRecord, kind SupervisionAlertKind, now time.Time) (SupervisionAlertRecord, bool, error) {
	if r == nil || r.unit == nil || assignment.TenantID == "" || assignment.ID == "" || assignment.DueAt.IsZero() || !supervisionAlertKindValid(kind) {
		return SupervisionAlertRecord{}, false, ErrInvalidContract
	}
	if err := validatePolicyForAssignment(policy, assignment); err != nil {
		return SupervisionAlertRecord{}, false, err
	}
	id := SupervisionAlertID(assignment.TenantID, assignment.ID, kind)
	var out SupervisionAlertRecord
	created := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := lockResponsibilityAggregateTx(ctx, tx, assignment.ID); err != nil {
			return err
		}
		current, found, err := getAssignmentTx(ctx, tx, assignment.ID)
		if err != nil {
			return err
		}
		if !found || current.TenantID != assignment.TenantID || current.Version != assignment.Version || assignmentTerminal(current.State) {
			return ErrConflict
		}
		if existing, found, err := getSupervisionAlertTx(tx, id); err != nil {
			return err
		} else if found {
			out = existing
			return nil
		}
		alert := SupervisionAlertRecord{
			ID: id, TenantID: current.TenantID, ResponsibilityID: current.ID, Kind: kind,
			DueAt: current.DueAt, Status: SupervisionAlertOpen, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		switch kind {
		case SupervisionAlertDeadlineWarning:
			alert.Severity = "warning"
			alert.RequestedFrom = "agent:" + current.AccountableAgent
		case SupervisionAlertOverdue:
			alert.Severity = "critical"
			alert.RequestedFrom = "mission_owner:" + current.MissionID
			if len(policy.EscalationChain) > 0 {
				target := policy.EscalationChain[0]
				alert.RequestedFrom = target.SubjectKind + ":" + target.SubjectID
				alert.EscalationLevel = 1
				alert.NextEscalationAt = now.Add(target.After)
			}
			if err := resolveSupervisionAlertTx(ctx, tx, current, SupervisionAlertDeadlineWarning, "responsibility is overdue", now); err != nil {
				return err
			}
		}
		if err := putRecordTx(ctx, tx, supervisionAlertTable, alert.ID, alert); err != nil {
			return err
		}
		if err := putAuditTx(ctx, tx, AuditRecord{TenantID: current.TenantID, ResponsibilityID: current.ID,
			EntityType: "supervision_alert", EntityID: alert.ID, Action: AuditSupervisionAlertOpened,
			ToState: string(alert.Status), Details: string(alert.Kind) + "; requested_from=" + alert.RequestedFrom, OccurredAt: now}); err != nil {
			return err
		}
		out = alert
		created = true
		return nil
	})
	return out, created, err
}

func (r *Repository) ListSupervisionAlerts(ctx context.Context, tenantID, responsibilityID string, openOnly bool) ([]SupervisionAlertRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidContract
	}
	var out []SupervisionAlertRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, supervisionAlertTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var alert SupervisionAlertRecord
			if err := decodeRecord(record, &alert); err != nil {
				return err
			}
			if alert.TenantID != strings.TrimSpace(tenantID) || (strings.TrimSpace(responsibilityID) != "" && alert.ResponsibilityID != strings.TrimSpace(responsibilityID)) || (openOnly && alert.Status != SupervisionAlertOpen) {
				continue
			}
			out = append(out, alert)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == SupervisionAlertOpen
		}
		if out[i].Severity != out[j].Severity {
			return out[i].Severity == "critical"
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *Repository) ListOpenSupervisionAlerts(ctx context.Context, limit int) ([]SupervisionAlertRecord, error) {
	if r == nil || r.unit == nil {
		return nil, ErrInvalidContract
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []SupervisionAlertRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, supervisionAlertTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var alert SupervisionAlertRecord
			if err := decodeRecord(record, &alert); err != nil {
				return err
			}
			if alert.Status == SupervisionAlertOpen {
				out = append(out, alert)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DueAt.Equal(out[j].DueAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].DueAt.Before(out[j].DueAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *Repository) EscalateSupervisionAlert(ctx context.Context, alertID string, policy ResponsibilityPolicyRecord, now time.Time) (SupervisionAlertRecord, bool, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(alertID) == "" {
		return SupervisionAlertRecord{}, false, ErrInvalidContract
	}
	var out SupervisionAlertRecord
	escalated := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		candidate, found, err := getSupervisionAlertTx(tx, strings.TrimSpace(alertID))
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, candidate.ResponsibilityID); err != nil {
			return err
		}
		alert, found, err := getSupervisionAlertTx(tx, strings.TrimSpace(alertID))
		if err != nil {
			return err
		}
		if !found || alert.ResponsibilityID != candidate.ResponsibilityID {
			return ErrConflict
		}
		if alert.Status != SupervisionAlertOpen || alert.Kind != SupervisionAlertOverdue || alert.NextEscalationAt.IsZero() || alert.NextEscalationAt.After(now) {
			out = alert
			return nil
		}
		if policy.TenantID != alert.TenantID {
			return ErrConflict
		}
		assignment, found, err := getAssignmentTx(ctx, tx, alert.ResponsibilityID)
		if err != nil {
			return err
		}
		if !found || assignment.TenantID != alert.TenantID || assignmentTerminal(assignment.State) {
			return ErrConflict
		}
		if alert.EscalationLevel < len(policy.EscalationChain) {
			target := policy.EscalationChain[alert.EscalationLevel]
			alert.EscalationLevel++
			alert.RequestedFrom = target.SubjectKind + ":" + target.SubjectID
			alert.NextEscalationAt = now.Add(target.After)
		} else {
			alert.EscalationLevel++
			alert.RequestedFrom = "mission_owner:" + assignment.MissionID
			alert.NextEscalationAt = time.Time{}
		}
		alert.Version++
		alert.UpdatedAt = now
		if err := putRecordTx(ctx, tx, supervisionAlertTable, alert.ID, alert); err != nil {
			return err
		}
		if err := putAuditTx(ctx, tx, AuditRecord{TenantID: alert.TenantID, ResponsibilityID: alert.ResponsibilityID,
			EntityType: "supervision_alert", EntityID: alert.ID, Action: AuditSupervisionAlertEscalated,
			Details: alert.RequestedFrom, OccurredAt: now}); err != nil {
			return err
		}
		out = alert
		escalated = true
		return nil
	})
	return out, escalated, err
}

func (r *Repository) ResolveSupervisionAlerts(ctx context.Context, assignment AssignmentRecord, resolution string, now time.Time) (int, error) {
	if r == nil || r.unit == nil || assignment.TenantID == "" || assignment.ID == "" || strings.TrimSpace(resolution) == "" {
		return 0, ErrInvalidContract
	}
	resolved := 0
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := lockResponsibilityAggregateTx(ctx, tx, assignment.ID); err != nil {
			return err
		}
		current, found, err := getAssignmentTx(ctx, tx, assignment.ID)
		if err != nil {
			return err
		}
		if !found || current.TenantID != assignment.TenantID {
			return ErrNotFound
		}
		for _, kind := range []SupervisionAlertKind{SupervisionAlertDeadlineWarning, SupervisionAlertOverdue} {
			before, found, err := getSupervisionAlertTx(tx, SupervisionAlertID(current.TenantID, current.ID, kind))
			if err != nil {
				return err
			}
			if !found || before.Status != SupervisionAlertOpen {
				continue
			}
			if err := resolveSupervisionAlertTx(ctx, tx, current, kind, resolution, now); err != nil {
				return err
			}
			resolved++
		}
		return nil
	})
	return resolved, err
}

func (r *Repository) ResolveBlocker(ctx context.Context, tenantID, blockerID, actorID, resolution, idempotencyKey string, expectedVersion int64, cause ActivationCause, now time.Time) (BlockerRecord, ActivationRecord, error) {
	return r.resolveBlocker(ctx, tenantID, blockerID, actorID, resolution, idempotencyKey, expectedVersion, cause, nil, now)
}

func (r *Repository) ResolveApprovalBlocker(ctx context.Context, tenantID, blockerID, actorID, resolution, idempotencyKey string, expectedVersion int64, cause ActivationCause, grant ApprovalGrant, now time.Time) (BlockerRecord, ActivationRecord, error) {
	return r.resolveBlocker(ctx, tenantID, blockerID, actorID, resolution, idempotencyKey, expectedVersion, cause, &grant, now)
}

func (r *Repository) resolveBlocker(ctx context.Context, tenantID, blockerID, actorID, resolution, idempotencyKey string, expectedVersion int64, cause ActivationCause, grant *ApprovalGrant, now time.Time) (BlockerRecord, ActivationRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(blockerID) == "" || strings.TrimSpace(actorID) == "" || strings.TrimSpace(resolution) == "" || strings.TrimSpace(idempotencyKey) == "" || expectedVersion < 1 || !cause.valid() {
		return BlockerRecord{}, ActivationRecord{}, ErrInvalidContract
	}
	if grant != nil {
		grant.AgentProfileID = strings.TrimSpace(grant.AgentProfileID)
		grant.PolicyID = strings.TrimSpace(grant.PolicyID)
		grant.ScopeDigest = strings.TrimSpace(grant.ScopeDigest)
		if grant.AgentProfileID == "" || grant.PolicyID == "" || grant.PolicyVersion < 1 || grant.ScopeDigest == "" {
			return BlockerRecord{}, ActivationRecord{}, ErrInvalidContract
		}
	}
	commandID := operationCommandID(tenantID, idempotencyKey)
	fingerprint := stableHex("blocker.resolve", blockerID, actorID, resolution, fmt.Sprint(expectedVersion), string(cause))
	var out BlockerRecord
	var activation ActivationRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if existingCommand, found, err := getControlCommandTx(tx, commandID); err != nil {
			return err
		} else if found {
			if existingCommand.TenantID != strings.TrimSpace(tenantID) || existingCommand.Action != "blocker.resolve" || existingCommand.Fingerprint != fingerprint || existingCommand.ResponsibilityID != strings.TrimSpace(blockerID) {
				return ErrConflict
			}
			stored, found, err := getBlockerTx(tx, existingCommand.ResponsibilityID)
			if err != nil {
				return err
			}
			if !found || stored.Version != existingCommand.ResultVersion {
				return ErrConflict
			}
			out = stored
			return nil
		}
		candidate, found, err := getBlockerTx(tx, strings.TrimSpace(blockerID))
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if candidate.TenantID != strings.TrimSpace(tenantID) {
			return ErrNotFound
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, candidate.ResponsibilityID); err != nil {
			return err
		}
		blocker, found, err := getBlockerTx(tx, strings.TrimSpace(blockerID))
		if err != nil {
			return err
		}
		if !found || blocker.ResponsibilityID != candidate.ResponsibilityID || blocker.TenantID != strings.TrimSpace(tenantID) {
			return ErrConflict
		}
		if expectedVersion > 0 && blocker.Version != expectedVersion {
			return ErrConflict
		}
		if blocker.Status == BlockerStatusResolved {
			return ErrConflict
		}
		if blocker.Status != BlockerStatusOpen {
			return ErrConflict
		}
		assignment, found, err := getAssignmentTx(ctx, tx, blocker.ResponsibilityID)
		if err != nil {
			return err
		}
		if !found || assignment.TenantID != blocker.TenantID || assignmentTerminal(assignment.State) {
			return ErrConflict
		}
		if grant != nil {
			if blocker.Kind != BlockerKindApproval || assignment.AccountableAgent != grant.AgentProfileID {
				return ErrConflict
			}
			policy, err := resolvePolicyTx(tx, assignment)
			if err != nil {
				return err
			}
			if policy.ID != grant.PolicyID || policy.Version != grant.PolicyVersion {
				return ErrConflict
			}
		}
		blocker.Status = BlockerStatusResolved
		blocker.Resolution = strings.TrimSpace(resolution)
		blocker.ResolvedBy = strings.TrimSpace(actorID)
		blocker.ResolvedAt = now
		blocker.UpdatedAt = now
		blocker.Version++
		if grant != nil {
			blocker.ApprovalAgentProfileID = grant.AgentProfileID
			blocker.ApprovalPolicyID = grant.PolicyID
			blocker.ApprovalPolicyVersion = grant.PolicyVersion
			blocker.ApprovalScopeDigest = grant.ScopeDigest
		}
		if err := putRecordTx(ctx, tx, blockerTable, blocker.ID, blocker); err != nil {
			return err
		}
		hasOpenBlocker, err := hasOpenBlockerTx(ctx, tx, assignment.ID)
		if err != nil {
			return err
		}
		if hasOpenBlocker {
			if err := putAssignmentTx(ctx, tx, assignment); err != nil {
				return err
			}
		} else if assignment.State == AssignmentStateOffered {
			var found bool
			activation, found, err = findActivationForVersionTx(ctx, tx, assignment.ID, assignment.Version)
			if err != nil {
				return err
			}
			if !found {
				return ErrConflict
			}
		} else {
			assignment.Paused = false
			assignment.PauseReason = ""
			if assignment.State == AssignmentStateWaiting || assignment.State == AssignmentStateEscalated {
				assignment.State = AssignmentStateActive
			}
			var errActivation error
			activation, errActivation = createActivationTx(ctx, tx, &assignment, cause, now, now)
			if errActivation != nil {
				return errActivation
			}
			if err := putAssignmentTx(ctx, tx, assignment); err != nil {
				return err
			}
		}
		if err := putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
			EntityType: "blocker", EntityID: blocker.ID, Action: AuditBlockerResolved, ActorID: strings.TrimSpace(actorID), FromState: string(BlockerStatusOpen), ToState: string(BlockerStatusResolved), Details: blocker.Resolution, OccurredAt: now}); err != nil {
			return err
		}
		if err := putControlCommandTx(ctx, tx, ControlCommandRecord{
			ID: commandID, TenantID: strings.TrimSpace(tenantID), IdempotencyKey: strings.TrimSpace(idempotencyKey), Fingerprint: fingerprint,
			Action: "blocker.resolve", ActorID: strings.TrimSpace(actorID), ResponsibilityID: blocker.ID, ResultVersion: blocker.Version, CreatedAt: now,
		}); err != nil {
			return err
		}
		out = blocker
		return nil
	})
	return out, activation, err
}

func (r *Repository) ApprovalGranted(ctx context.Context, assignment AssignmentRecord, policy ResponsibilityPolicyRecord, executionScopes []string) (bool, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(assignment.TenantID) == "" || strings.TrimSpace(assignment.ID) == "" {
		return false, ErrInvalidContract
	}
	if err := validatePolicyForAssignment(policy, assignment); err != nil {
		return false, err
	}
	wantedDigest := approvalScopeDigest(executionScopes)
	granted := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, blockerTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var blocker BlockerRecord
			if err := decodeRecord(record, &blocker); err != nil {
				return err
			}
			if blocker.TenantID != assignment.TenantID || blocker.ResponsibilityID != assignment.ID || blocker.Kind != BlockerKindApproval || blocker.Status != BlockerStatusResolved {
				continue
			}
			if blocker.ApprovalAgentProfileID == assignment.AccountableAgent && blocker.ApprovalPolicyID == policy.ID && blocker.ApprovalPolicyVersion == policy.Version && blocker.ApprovalScopeDigest == wantedDigest {
				granted = true
				return nil
			}
		}
		if granted {
			return nil
		}
		granted, err = autoplanExecutionGrantApprovedTx(ctx, tx, assignment, policy, executionScopes)
		return err
	})
	return granted, err
}

func (r *Repository) ResolveBlockersForExternalSignal(ctx context.Context, signal ExternalSignal, now time.Time) (int, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(signal.TenantID) == "" || strings.TrimSpace(signal.ActorID) == "" || strings.TrimSpace(signal.IdempotencyKey) == "" || strings.TrimSpace(signal.SourceRef) == "" || strings.TrimSpace(signal.Summary) == "" {
		return 0, ErrInvalidContract
	}
	if signal.BlockerKind != "" && !signal.BlockerKind.valid() {
		return 0, ErrInvalidContract
	}
	signal.TenantID = strings.TrimSpace(signal.TenantID)
	signal.ResponsibilityID = strings.TrimSpace(signal.ResponsibilityID)
	signal.RequestedFrom = strings.TrimSpace(signal.RequestedFrom)
	signal.SourceRef = strings.TrimSpace(signal.SourceRef)
	signal.Summary = strings.TrimSpace(signal.Summary)
	signal.ActorID = strings.TrimSpace(signal.ActorID)
	signal.IdempotencyKey = strings.TrimSpace(signal.IdempotencyKey)
	commandID := operationCommandID(signal.TenantID, signal.IdempotencyKey)
	fingerprint := stableHex("external_signal", signal.ResponsibilityID, string(signal.BlockerKind), signal.RequestedFrom, signal.SourceRef, signal.Summary, signal.ActorID)
	resolved := 0
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if existing, found, err := getControlCommandTx(tx, commandID); err != nil {
			return err
		} else if found {
			if existing.TenantID != signal.TenantID || existing.Action != "external_signal" || existing.Fingerprint != fingerprint {
				return ErrConflict
			}
			resolved = int(existing.ResultVersion)
			return nil
		}
		matches := make([]BlockerRecord, 0)
		records, err := uow.ListRecords(ctx, tx, blockerTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var blocker BlockerRecord
			if err := decodeRecord(record, &blocker); err != nil {
				return err
			}
			if blocker.TenantID != signal.TenantID || blocker.Status != BlockerStatusOpen || blocker.Kind == BlockerKindApproval || (signal.ResponsibilityID != "" && blocker.ResponsibilityID != signal.ResponsibilityID) || (signal.BlockerKind != "" && blocker.Kind != signal.BlockerKind) || (signal.RequestedFrom != "" && blocker.RequestedFrom != signal.RequestedFrom) {
				continue
			}
			matches = append(matches, blocker)
		}
		sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
		assignmentIDs := make([]string, 0)
		seenAssignments := map[string]bool{}
		for _, blocker := range matches {
			if !seenAssignments[blocker.ResponsibilityID] {
				seenAssignments[blocker.ResponsibilityID] = true
				assignmentIDs = append(assignmentIDs, blocker.ResponsibilityID)
			}
		}
		sort.Strings(assignmentIDs)
		for _, assignmentID := range assignmentIDs {
			if err := lockResponsibilityAggregateTx(ctx, tx, assignmentID); err != nil {
				return err
			}
		}
		for _, blocker := range matches {
			if err := db.LockRecordKeyTx(ctx, tx, blockerTable, blocker.ID); err != nil {
				return err
			}
		}
		resolution := signal.Summary + " [source=" + signal.SourceRef + "]"
		for _, candidate := range matches {
			blocker, found, err := getBlockerTx(tx, candidate.ID)
			if err != nil {
				return err
			}
			if !found || blocker.Status != BlockerStatusOpen || blocker.Version != candidate.Version {
				return ErrConflict
			}
			blocker.Status = BlockerStatusResolved
			blocker.Resolution = resolution
			blocker.ResolvedBy = signal.ActorID
			blocker.ResolvedAt = now
			blocker.UpdatedAt = now
			blocker.Version++
			if err := putRecordTx(ctx, tx, blockerTable, blocker.ID, blocker); err != nil {
				return err
			}
			if err := putAuditTx(ctx, tx, AuditRecord{TenantID: signal.TenantID, ResponsibilityID: blocker.ResponsibilityID, EntityType: "blocker", EntityID: blocker.ID, Action: AuditBlockerResolved, ActorID: signal.ActorID, FromState: string(BlockerStatusOpen), ToState: string(BlockerStatusResolved), Details: resolution, OccurredAt: now}); err != nil {
				return err
			}
			resolved++
		}
		for _, assignmentID := range assignmentIDs {
			hasOpenBlocker, err := hasOpenBlockerTx(ctx, tx, assignmentID)
			if err != nil {
				return err
			}
			if hasOpenBlocker {
				continue
			}
			assignment, found, err := getAssignmentTx(ctx, tx, assignmentID)
			if err != nil {
				return err
			}
			if !found || assignment.TenantID != signal.TenantID || assignmentTerminal(assignment.State) {
				return ErrConflict
			}
			assignment.Paused = false
			assignment.PauseReason = ""
			if assignment.State == AssignmentStateWaiting || assignment.State == AssignmentStateEscalated {
				assignment.State = AssignmentStateActive
			}
			if assignment.State != AssignmentStateOffered {
				if _, err := createActivationTx(ctx, tx, &assignment, ActivationCauseExternalSignal, now, now); err != nil {
					return err
				}
			}
			if err := putAssignmentTx(ctx, tx, assignment); err != nil {
				return err
			}
		}
		return putControlCommandTx(ctx, tx, ControlCommandRecord{ID: commandID, TenantID: signal.TenantID, IdempotencyKey: signal.IdempotencyKey, Fingerprint: fingerprint, Action: "external_signal", ActorID: signal.ActorID, ResponsibilityID: signal.ResponsibilityID, ResultVersion: int64(resolved), CreatedAt: now})
	})
	return resolved, err
}

func (r *Repository) EscalateBlocker(ctx context.Context, blockerID string, policy ResponsibilityPolicyRecord, now time.Time) (BlockerRecord, bool, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(blockerID) == "" {
		return BlockerRecord{}, false, ErrInvalidContract
	}
	var out BlockerRecord
	assignmentEscalated := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		candidate, found, err := getBlockerTx(tx, blockerID)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, candidate.ResponsibilityID); err != nil {
			return err
		}
		blocker, found, err := getBlockerTx(tx, blockerID)
		if err != nil {
			return err
		}
		if !found || blocker.ResponsibilityID != candidate.ResponsibilityID {
			return ErrConflict
		}
		if blocker.Status != BlockerStatusOpen || blocker.RecheckAt.After(now) {
			out = blocker
			return nil
		}
		if policy.TenantID != blocker.TenantID {
			return ErrConflict
		}
		assignment, found, err := getAssignmentTx(ctx, tx, blocker.ResponsibilityID)
		if err != nil {
			return err
		}
		if !found || assignment.TenantID != blocker.TenantID || assignmentTerminal(assignment.State) {
			return ErrConflict
		}
		if assignment.State == AssignmentStateEscalated && blocker.EscalationLevel > len(policy.EscalationChain) {
			out = blocker
			return nil
		}
		level := blocker.EscalationLevel + 1
		blocker.EscalationLevel = level
		blocker.Version++
		blocker.UpdatedAt = now
		if level <= len(policy.EscalationChain) {
			target := policy.EscalationChain[level-1]
			blocker.RequestedFrom = target.SubjectKind + ":" + target.SubjectID
			blocker.RecheckAt = now.Add(target.After)
		} else {
			assignment.State = AssignmentStateEscalated
			assignment.UpdatedAt = now
			assignmentEscalated = true
			if err := putAssignmentTx(ctx, tx, assignment); err != nil {
				return err
			}
		}
		if err := putRecordTx(ctx, tx, blockerTable, blocker.ID, blocker); err != nil {
			return err
		}
		if err := putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
			EntityType: "blocker", EntityID: blocker.ID, Action: AuditBlockerEscalated, Details: blocker.RequestedFrom, OccurredAt: now}); err != nil {
			return err
		}
		out = blocker
		return nil
	})
	return out, assignmentEscalated, err
}

func (r *Repository) PauseAssignment(ctx context.Context, command AssignmentControl, now time.Time) (AssignmentRecord, error) {
	return r.updateAssignmentControl(ctx, command, "pause", now, func(ctx context.Context, tx uow.Tx, assignment *AssignmentRecord) error {
		if assignment.Paused {
			return nil
		}
		assignment.Paused = true
		assignment.PauseReason = strings.TrimSpace(command.Reason)
		assignment.Version++
		assignment.UpdatedAt = now
		if err := supersedeOlderActivationsTx(ctx, tx, assignment.ID, assignment.Version, now); err != nil {
			return err
		}
		return putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID, Action: AuditResponsibilityPaused, ActorID: command.ActorID, Details: command.Reason, OccurredAt: now})
	})
}

func (r *Repository) ResumeAssignment(ctx context.Context, command AssignmentControl, now time.Time) (AssignmentRecord, ActivationRecord, error) {
	var activation ActivationRecord
	assignment, err := r.updateAssignmentControl(ctx, command, "resume", now, func(ctx context.Context, tx uow.Tx, assignment *AssignmentRecord) error {
		if !assignment.Paused {
			return nil
		}
		assignment.Paused = false
		assignment.PauseReason = ""
		var createErr error
		activation, createErr = createActivationTx(ctx, tx, assignment, ActivationCauseManualRetry, now, now)
		if createErr != nil {
			return createErr
		}
		return putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID, Action: AuditResponsibilityResumed, ActorID: command.ActorID, OccurredAt: now})
	})
	return assignment, activation, err
}

func (r *Repository) RetryAssignment(ctx context.Context, command AssignmentControl, now time.Time) (AssignmentRecord, ActivationRecord, error) {
	var activation ActivationRecord
	assignment, err := r.updateAssignmentControl(ctx, command, "retry", now, func(ctx context.Context, tx uow.Tx, assignment *AssignmentRecord) error {
		if assignment.Paused {
			return ErrConflict
		}
		hasLiveAttempt, err := hasLiveAttemptTx(ctx, tx, assignment.ID)
		if err != nil {
			return err
		}
		if hasLiveAttempt {
			return ErrConflict
		}
		if assignment.State == AssignmentStateOffered && !command.AcceptBy.IsZero() {
			if !command.AcceptBy.After(now) {
				return ErrInvalidContract
			}
			assignment.AcceptBy = command.AcceptBy
		}
		if assignment.State == AssignmentStateWaiting || assignment.State == AssignmentStateEscalated || assignment.State == AssignmentStateSubmitted {
			assignment.State = AssignmentStateActive
		}
		var createErr error
		activation, createErr = createActivationTx(ctx, tx, assignment, ActivationCauseManualRetry, now, now)
		if createErr != nil {
			return createErr
		}
		return putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID, Action: AuditResponsibilityRetried, ActorID: command.ActorID, Details: command.Reason, OccurredAt: now})
	})
	return assignment, activation, err
}

func (r *Repository) TerminateAssignment(ctx context.Context, command AssignmentControl, now time.Time) (AssignmentRecord, error) {
	return r.updateAssignmentControl(ctx, command, "terminate", now, func(ctx context.Context, tx uow.Tx, assignment *AssignmentRecord) error {
		from := assignment.State
		if assignment.State == AssignmentStateTerminated {
			return resolveOpenBlockersForTerminalAssignmentTx(ctx, tx, *assignment, command.ActorID, command.Reason, now)
		}
		if err := ValidateAssignmentTransition(assignment.State, AssignmentStateTerminated); err != nil {
			return err
		}
		assignment.State = AssignmentStateTerminated
		assignment.Paused = false
		assignment.TerminatedAt = now
		assignment.TerminatedBy = strings.TrimSpace(command.ActorID)
		assignment.TerminationNote = strings.TrimSpace(command.Reason)
		assignment.Version++
		assignment.UpdatedAt = now
		if err := supersedeOlderActivationsTx(ctx, tx, assignment.ID, assignment.Version, now); err != nil {
			return err
		}
		if err := resolveOpenBlockersForTerminalAssignmentTx(ctx, tx, *assignment, command.ActorID, command.Reason, now); err != nil {
			return err
		}
		return putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "assignment", EntityID: assignment.ID, Action: AuditResponsibilityTerminated, ActorID: command.ActorID, FromState: string(from), ToState: string(assignment.State), Details: command.Reason, OccurredAt: now})
	})
}

func (r *Repository) TerminateMissionAssignments(ctx context.Context, tenantID, missionID, actorID, reason string, now time.Time) (int, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(missionID) == "" || strings.TrimSpace(actorID) == "" || now.IsZero() {
		return 0, ErrInvalidContract
	}
	tenantID = strings.TrimSpace(tenantID)
	missionID = strings.TrimSpace(missionID)
	actorID = strings.TrimSpace(actorID)
	reason = strings.TrimSpace(reason)
	terminated := 0
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, assignmentTable)
		if err != nil {
			return err
		}
		assignmentIDs := make([]string, 0)
		for _, record := range records {
			var assignment AssignmentRecord
			if err := decodeRecord(record, &assignment); err != nil {
				return err
			}
			if assignment.TenantID == tenantID && assignment.MissionID == missionID && assignment.WorkKind == WorkKindMissionTask {
				assignmentIDs = append(assignmentIDs, assignment.ID)
			}
		}
		sort.Strings(assignmentIDs)
		for _, assignmentID := range assignmentIDs {
			if err := lockResponsibilityAggregateTx(ctx, tx, assignmentID); err != nil {
				return err
			}
			assignment, found, err := getAssignmentTx(ctx, tx, assignmentID)
			if err != nil {
				return err
			}
			if !found || assignment.TenantID != tenantID || assignment.MissionID != missionID || assignment.WorkKind != WorkKindMissionTask {
				continue
			}
			if !assignmentTerminal(assignment.State) {
				from := assignment.State
				if err := ValidateAssignmentTransition(from, AssignmentStateTerminated); err != nil {
					return err
				}
				assignment.State = AssignmentStateTerminated
				assignment.Paused = false
				assignment.TerminatedAt = now
				assignment.TerminatedBy = actorID
				assignment.TerminationNote = reason
				assignment.Version++
				assignment.UpdatedAt = now
				if err := resolveOpenBlockersForTerminalAssignmentTx(ctx, tx, assignment, actorID, reason, now); err != nil {
					return err
				}
				if err := assignment.Validate(); err != nil {
					return err
				}
				if err := putAssignmentTx(ctx, tx, assignment); err != nil {
					return err
				}
				if err := putAuditTx(ctx, tx, AuditRecord{
					TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
					EntityType: "assignment", EntityID: assignment.ID, Action: AuditResponsibilityTerminated,
					ActorID: actorID, FromState: string(from), ToState: string(assignment.State), Details: reason, OccurredAt: now,
				}); err != nil {
					return err
				}
				terminated++
			}
			if err := supersedeActiveActivationsTx(ctx, tx, assignment.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
	return terminated, err
}

func resolveOpenBlockersForTerminalAssignmentTx(ctx context.Context, tx uow.Tx, assignment AssignmentRecord, actorID, reason string, now time.Time) error {
	if !assignmentTerminal(assignment.State) {
		return ErrConflict
	}
	resolution := "assignment terminated"
	if reason = strings.TrimSpace(reason); reason != "" {
		resolution += ": " + reason
	}
	records, err := uow.ListRecords(ctx, tx, blockerTable)
	if err != nil {
		return err
	}
	for _, record := range records {
		var blocker BlockerRecord
		if err := decodeRecord(record, &blocker); err != nil {
			return err
		}
		if blocker.ResponsibilityID != assignment.ID || blocker.TenantID != assignment.TenantID || blocker.Status != BlockerStatusOpen {
			continue
		}
		blocker.Status = BlockerStatusResolved
		blocker.Resolution = resolution
		blocker.ResolvedBy = strings.TrimSpace(actorID)
		blocker.ResolvedAt = now
		blocker.UpdatedAt = now
		blocker.Version++
		if err := blocker.Validate(); err != nil {
			return err
		}
		if err := putRecordTx(ctx, tx, blockerTable, blocker.ID, blocker); err != nil {
			return err
		}
		if err := putAuditTx(ctx, tx, AuditRecord{
			TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "blocker", EntityID: blocker.ID,
			Action: AuditBlockerResolved, ActorID: strings.TrimSpace(actorID), FromState: string(BlockerStatusOpen), ToState: string(BlockerStatusResolved), Details: resolution, OccurredAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) UpdateAttemptHeartbeat(ctx context.Context, tenantID, runID string, heartbeat time.Time, usageTokens int64, now time.Time) (AttemptRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" || heartbeat.IsZero() || usageTokens < 0 {
		return AttemptRecord{}, ErrInvalidContract
	}
	var out AttemptRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		candidate, found, err := findAttemptByRunTx(ctx, tx, tenantID, runID)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, candidate.ResponsibilityID); err != nil {
			return err
		}
		attempt, found, err := findAttemptByRunTx(ctx, tx, tenantID, runID)
		if err != nil {
			return err
		}
		if !found || attempt.ID != candidate.ID || attempt.ResponsibilityID != candidate.ResponsibilityID {
			return ErrConflict
		}
		if attempt.Outcome != "" {
			out = attempt
			return nil
		}
		if heartbeat.After(attempt.HeartbeatAt) {
			attempt.HeartbeatAt = heartbeat
		}
		if usageTokens > attempt.UsageTokens {
			attempt.UsageTokens = usageTokens
		}
		attempt.UpdatedAt = now
		if err := putAttemptTx(ctx, tx, attempt); err != nil {
			return err
		}
		out = attempt
		return nil
	})
	return out, err
}

func (r *Repository) updateAssignmentControl(ctx context.Context, command AssignmentControl, action string, now time.Time, mutate func(context.Context, uow.Tx, *AssignmentRecord) error) (AssignmentRecord, error) {
	if r == nil || r.unit == nil || strings.TrimSpace(command.TenantID) == "" || strings.TrimSpace(command.AssignmentID) == "" || strings.TrimSpace(command.ActorID) == "" || strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(action) == "" || mutate == nil {
		return AssignmentRecord{}, ErrInvalidContract
	}
	var out AssignmentRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		commandID := operationCommandID(command.TenantID, command.IdempotencyKey)
		fingerprint := stableHex(action, command.AssignmentID, command.ActorID, command.Reason, command.AgentProfileID, command.AcceptBy.UTC().Format(time.RFC3339Nano), fmt.Sprint(command.ExpectedVersion))
		if existing, found, err := getControlCommandTx(tx, commandID); err != nil {
			return err
		} else if found {
			if existing.TenantID != strings.TrimSpace(command.TenantID) || existing.Fingerprint != fingerprint {
				return ErrConflict
			}
			assignment, found, err := getAssignmentTx(ctx, tx, existing.ResponsibilityID)
			if err != nil {
				return err
			}
			if !found || assignment.TenantID != existing.TenantID {
				return ErrConflict
			}
			out = assignment
			return nil
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, command.AssignmentID); err != nil {
			return err
		}
		assignment, found, err := getAssignmentTx(ctx, tx, command.AssignmentID)
		if err != nil {
			return err
		}
		if !found || assignment.TenantID != strings.TrimSpace(command.TenantID) {
			return ErrNotFound
		}
		if command.ExpectedVersion > 0 && assignment.Version != command.ExpectedVersion {
			return ErrConflict
		}
		if assignmentTerminal(assignment.State) && assignment.State != AssignmentStateTerminated {
			return ErrConflict
		}
		if err := mutate(ctx, tx, &assignment); err != nil {
			return err
		}
		if err := assignment.Validate(); err != nil {
			return err
		}
		if err := putAssignmentTx(ctx, tx, assignment); err != nil {
			return err
		}
		if err := putControlCommandTx(ctx, tx, ControlCommandRecord{
			ID: commandID, TenantID: strings.TrimSpace(command.TenantID), IdempotencyKey: strings.TrimSpace(command.IdempotencyKey),
			Fingerprint: fingerprint, Action: action, ActorID: strings.TrimSpace(command.ActorID), ResponsibilityID: assignment.ID, ResultVersion: assignment.Version, CreatedAt: now,
		}); err != nil {
			return err
		}
		out = assignment
		return nil
	})
	return out, err
}

func createActivationTx(ctx context.Context, tx uow.Tx, assignment *AssignmentRecord, cause ActivationCause, notBefore, now time.Time) (ActivationRecord, error) {
	if assignment == nil || !cause.valid() || assignmentTerminal(assignment.State) {
		return ActivationRecord{}, ErrConflict
	}
	assignment.Version++
	assignment.UpdatedAt = now
	dedupeKey := ActivationDedupeKey(assignment.TenantID, assignment.ID, cause, assignment.Version)
	activation := ActivationRecord{ResponsibilityActivation: ResponsibilityActivation{
		ID: ActivationID(dedupeKey), TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Cause: cause,
		NotBefore: notBefore, DedupeKey: dedupeKey, AssignmentVersion: assignment.Version, Status: ActivationStatusPending,
	}, CreatedAt: now, UpdatedAt: now}
	if err := activation.Validate(); err != nil {
		return ActivationRecord{}, err
	}
	if err := supersedeOlderActivationsTx(ctx, tx, assignment.ID, assignment.Version, now); err != nil {
		return ActivationRecord{}, err
	}
	if err := putActivationTx(ctx, tx, activation); err != nil {
		return ActivationRecord{}, err
	}
	if err := putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, EntityType: "activation", EntityID: activation.ID, Action: AuditActivationCreated, ToState: string(activation.Status), Details: string(cause), OccurredAt: now}); err != nil {
		return ActivationRecord{}, err
	}
	return activation, nil
}

func getPolicyTx(tx uow.Tx, id string) (ResponsibilityPolicyRecord, bool, error) {
	record, found := tx.Get(policyTable, id)
	if !found {
		return ResponsibilityPolicyRecord{}, false, nil
	}
	var policy ResponsibilityPolicyRecord
	if err := decodeRecord(record, &policy); err != nil {
		return ResponsibilityPolicyRecord{}, false, err
	}
	return policy, true, nil
}

func resolvePolicyTx(tx uow.Tx, assignment AssignmentRecord) (ResponsibilityPolicyRecord, error) {
	policy, found, err := getPolicyTx(tx, PolicyID(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind))
	if err != nil {
		return ResponsibilityPolicyRecord{}, err
	}
	if !found {
		policy = DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	}
	if err := validatePolicyForAssignment(policy, assignment); err != nil {
		return ResponsibilityPolicyRecord{}, err
	}
	return policy, nil
}

func approvalScopeDigest(scopes []string) string {
	clean := cleanPolicyStrings(scopes)
	sort.Strings(clean)
	return stableHex(clean...)
}

func operationCommandID(tenantID, idempotencyKey string) string {
	return "responsibility-command-" + stableHex(strings.TrimSpace(tenantID), strings.TrimSpace(idempotencyKey))[:24]
}

func getControlCommandTx(tx uow.Tx, id string) (ControlCommandRecord, bool, error) {
	record, found := tx.Get(controlCommandTable, id)
	if !found {
		return ControlCommandRecord{}, false, nil
	}
	var command ControlCommandRecord
	if err := decodeRecord(record, &command); err != nil {
		return ControlCommandRecord{}, false, err
	}
	return command, true, nil
}

func putControlCommandTx(ctx context.Context, tx uow.Tx, command ControlCommandRecord) error {
	return putRecordTx(ctx, tx, controlCommandTable, command.ID, command)
}

func getBlockerTx(tx uow.Tx, id string) (BlockerRecord, bool, error) {
	record, found := tx.Get(blockerTable, id)
	if !found {
		return BlockerRecord{}, false, nil
	}
	var blocker BlockerRecord
	if err := decodeRecord(record, &blocker); err != nil {
		return BlockerRecord{}, false, err
	}
	return blocker, true, nil
}

func getSupervisionAlertTx(tx uow.Tx, id string) (SupervisionAlertRecord, bool, error) {
	record, found := tx.Get(supervisionAlertTable, id)
	if !found {
		return SupervisionAlertRecord{}, false, nil
	}
	var alert SupervisionAlertRecord
	if err := decodeRecord(record, &alert); err != nil {
		return SupervisionAlertRecord{}, false, err
	}
	return alert, true, nil
}

func resolveSupervisionAlertTx(ctx context.Context, tx uow.Tx, assignment AssignmentRecord, kind SupervisionAlertKind, resolution string, now time.Time) error {
	alert, found, err := getSupervisionAlertTx(tx, SupervisionAlertID(assignment.TenantID, assignment.ID, kind))
	if err != nil || !found || alert.Status != SupervisionAlertOpen {
		return err
	}
	alert.Status = SupervisionAlertResolved
	alert.Resolution = strings.TrimSpace(resolution)
	alert.NextEscalationAt = time.Time{}
	alert.ResolvedAt = now
	alert.UpdatedAt = now
	alert.Version++
	if err := putRecordTx(ctx, tx, supervisionAlertTable, alert.ID, alert); err != nil {
		return err
	}
	return putAuditTx(ctx, tx, AuditRecord{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
		EntityType: "supervision_alert", EntityID: alert.ID, Action: AuditSupervisionAlertResolved,
		FromState: string(SupervisionAlertOpen), ToState: string(SupervisionAlertResolved), Details: alert.Resolution, OccurredAt: now})
}

func supervisionAlertKindValid(kind SupervisionAlertKind) bool {
	return kind == SupervisionAlertDeadlineWarning || kind == SupervisionAlertOverdue
}

func (r *Repository) listAllBlockers(ctx context.Context) ([]BlockerRecord, error) {
	if r == nil || r.unit == nil {
		return nil, ErrInvalidContract
	}
	var out []BlockerRecord
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := uow.ListRecords(ctx, tx, blockerTable)
		if err != nil {
			return err
		}
		for _, record := range records {
			var blocker BlockerRecord
			if err := decodeRecord(record, &blocker); err != nil {
				return err
			}
			out = append(out, blocker)
		}
		return nil
	})
	return out, err
}

func hasLiveAttemptTx(ctx context.Context, tx uow.Tx, responsibilityID string) (bool, error) {
	records, err := uow.ListRecords(ctx, tx, attemptTable)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		var attempt AttemptRecord
		if decodeRecord(record, &attempt) == nil && attempt.ResponsibilityID == responsibilityID && attempt.Outcome == "" {
			return true, nil
		}
	}
	return false, nil
}

func hasOpenBlockerTx(ctx context.Context, tx uow.Tx, responsibilityID string) (bool, error) {
	records, err := uow.ListRecords(ctx, tx, blockerTable)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		var blocker BlockerRecord
		if decodeRecord(record, &blocker) == nil && blocker.ResponsibilityID == responsibilityID && blocker.Status == BlockerStatusOpen {
			return true, nil
		}
	}
	return false, nil
}

func assignmentTerminal(state AssignmentState) bool {
	switch state {
	case AssignmentStateFulfilled, AssignmentStateDeclined, AssignmentStateReassigned, AssignmentStateTerminated:
		return true
	default:
		return false
	}
}

func validatePolicyForAssignment(policy ResponsibilityPolicyRecord, assignment AssignmentRecord) error {
	if policy.TenantID != assignment.TenantID || policy.AgentProfileID != assignment.AccountableAgent || policy.WorkKind != assignment.WorkKind {
		return fmt.Errorf("%w: policy does not belong to responsibility", ErrConflict)
	}
	return policy.Validate()
}
