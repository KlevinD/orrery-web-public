package hil

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformRepository struct {
	store *db.Store
}

func NewPlatformRepository(store *db.Store) *PlatformRepository {
	return &PlatformRepository{store: store}
}

func (r *PlatformRepository) CreateDecision(ctx context.Context, decision DecisionRequest) error {
	if r == nil || r.store == nil || strings.TrimSpace(decision.TenantID) == "" || strings.TrimSpace(decision.ID) == "" || strings.TrimSpace(decision.IdempotencyKey) == "" || !validDecisionApprovalAssignee(decision) {
		return ErrInvalidRequest
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		_, err := insertDecision(ctx, tx, decision)
		return err
	})
}

func (r *PlatformRepository) CreateDecisionWithCouncil(ctx context.Context, decision DecisionRequest, council *CouncilSession) error {
	if r == nil || r.store == nil || strings.TrimSpace(decision.TenantID) == "" || strings.TrimSpace(decision.ID) == "" || strings.TrimSpace(decision.IdempotencyKey) == "" || !validDecisionApprovalAssignee(decision) {
		return ErrInvalidRequest
	}
	if council != nil {
		if council.TenantID != decision.TenantID || council.RequestID != decision.ID {
			return ErrInvalidRequest
		}
		if err := validateCouncil(*council); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		inserted, err := insertDecision(ctx, tx, decision)
		if err != nil || !inserted || council == nil {
			return err
		}
		return insertCouncil(ctx, tx, *council)
	})
}

func insertDecision(ctx context.Context, tx db.Tx, decision DecisionRequest) (bool, error) {
	result, err := tx.ExecContext(ctx, `
INSERT INTO hil_decision_requests (
	id, tenant_id, requester_kind, requester_id, action, action_type_id, resource_kind,
	resource_id, object_refs_json, args_hash, purpose, risk_level, preferred_mode,
	route_mode, route_effect, route_reasons_json, context_json, payload_preview_json,
	idempotency_key, request_fingerprint, status, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, idempotency_key) DO NOTHING`,
		decision.ID, decision.TenantID, decision.Requester.Kind, decision.Requester.ID, decision.Action,
		decision.ActionTypeID, decision.Resource.Kind, decision.Resource.ID, mustJSON(decision.ObjectRefs),
		decision.ArgsHash, decision.Purpose, decision.RiskLevel, decision.PreferredMode, decision.Route.Mode,
		decision.Route.CouncilEffect, mustJSON(decision.Route.Reasons), mustJSON(decision.Context),
		mustJSON(decision.PayloadPreview), decision.IdempotencyKey, decision.RequestFingerprint, decision.Status,
		timeToInt(decision.CreatedAt), optionalTimeToInt(decision.ExpiresAt),
	)
	if err != nil {
		return false, fmt.Errorf("create HIL decision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("create HIL decision rows affected: %w", err)
	}
	if affected > 0 {
		return true, nil
	}
	var fingerprint string
	err = tx.QueryRowContext(ctx, `SELECT request_fingerprint FROM hil_decision_requests WHERE tenant_id = ? AND idempotency_key = ?`, decision.TenantID, decision.IdempotencyKey).Scan(&fingerprint)
	if err != nil {
		return false, fmt.Errorf("load idempotent HIL decision: %w", err)
	}
	if fingerprint == decision.RequestFingerprint {
		return false, nil
	}
	return false, ErrIdempotencyConflict
}

func (r *PlatformRepository) GetDecision(ctx context.Context, tenantID, decisionID string) (DecisionRequest, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(decisionID) == "" {
		return DecisionRequest{}, ErrInvalidRequest
	}
	decision, err := scanDecision(r.store.QueryRow(ctx, decisionSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, decisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return DecisionRequest{}, ErrNotFound
	}
	return decision, err
}

func (r *PlatformRepository) GetDecisionByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (DecisionRequest, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return DecisionRequest{}, ErrInvalidRequest
	}
	decision, err := scanDecision(r.store.QueryRow(ctx, decisionSelectSQL()+` WHERE tenant_id = ? AND idempotency_key = ?`, tenantID, idempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return DecisionRequest{}, ErrNotFound
	}
	return decision, err
}

func (r *PlatformRepository) ListDecisions(ctx context.Context, query DecisionListQuery) (DecisionPage, error) {
	tenantIDs := decisionListTenantIDs(query)
	if r == nil || r.store == nil || len(tenantIDs) == 0 {
		return DecisionPage{}, ErrInvalidRequest
	}
	where := ` WHERE tenant_id IN (` + decisionListPlaceholders(len(tenantIDs)) + `)`
	args := make([]any, 0, len(tenantIDs)+8)
	for _, tenantID := range tenantIDs {
		args = append(args, tenantID)
	}
	if decisionID := strings.TrimSpace(query.DecisionID); decisionID != "" {
		where += ` AND id = ?`
		args = append(args, decisionID)
	}
	if query.Mode == ModeCouncil {
		// A passed high-risk council is atomically promoted to a pending fixed
		// approval. Keep it discoverable in council history through the durable
		// council session rather than losing the audit trail when route_mode
		// changes to approval.
		where += ` AND (route_mode = ? OR EXISTS (
			SELECT 1 FROM hil_council_sessions AS council_history
			WHERE council_history.tenant_id = hil_decision_requests.tenant_id
			  AND council_history.request_id = hil_decision_requests.id
		))`
		args = append(args, ModeCouncil)
	} else if query.Mode != "" {
		where += ` AND route_mode = ?`
		args = append(args, query.Mode)
	}
	if query.Status != "" {
		where += ` AND status = ?`
		args = append(args, query.Status)
	}
	if query.RequestedBy.ID != "" {
		actor := normalizeActor(query.RequestedBy)
		if actor.Kind == "" || actor.ID == "" {
			return DecisionPage{}, ErrInvalidRequest
		}
		where += ` AND requester_kind = ? AND requester_id = ?`
		args = append(args, actor.Kind, actor.ID)
	}
	if query.Mine {
		actor := normalizeActor(query.ActionableBy)
		if actor.Kind == "" || actor.ID == "" {
			return DecisionPage{}, ErrInvalidRequest
		}
		switch query.Mode {
		case ModeCouncil:
			if query.Status != DecisionPending {
				where += ` AND EXISTS (
				SELECT 1 FROM hil_council_sessions AS history_council
				JOIN hil_council_participants AS history_participant
				  ON history_participant.tenant_id = history_council.tenant_id
				  AND history_participant.council_id = history_council.id
				WHERE history_council.tenant_id = hil_decision_requests.tenant_id
				  AND history_council.request_id = hil_decision_requests.id
				  AND history_participant.actor_kind = ? AND history_participant.actor_id = ?
				)`
				args = append(args, actor.Kind, actor.ID)
				break
			}
			where += ` AND EXISTS (
			SELECT 1 FROM hil_council_sessions AS actionable_council
			WHERE actionable_council.tenant_id = hil_decision_requests.tenant_id
			  AND actionable_council.request_id = hil_decision_requests.id
			  AND actionable_council.status = ?
			  AND EXISTS (
				SELECT 1 FROM hil_council_participants AS actionable_participant
				WHERE actionable_participant.tenant_id = actionable_council.tenant_id
				  AND actionable_participant.council_id = actionable_council.id
				  AND actionable_participant.actor_kind = ?
				  AND actionable_participant.actor_id = ?
			  )
			  AND NOT EXISTS (
				SELECT 1 FROM hil_council_votes AS actionable_vote
				WHERE actionable_vote.tenant_id = actionable_council.tenant_id
				  AND actionable_vote.council_id = actionable_council.id
				  AND actionable_vote.actor_kind = ?
				  AND actionable_vote.actor_id = ?
			  )
		)`
			args = append(args, CouncilOpen, actor.Kind, actor.ID, actor.Kind, actor.ID)
		case ModeHIL, ModeApproval:
			if actor.Kind != ActorHuman {
				return DecisionPage{}, ErrInvalidRequest
			}
			legacyPredicate, legacyArgument := approvalAssigneePredicate(r.store.Dialect(), actor.ID)
			workflowTaskStatusPredicate := `assigned_task.status <> ?`
			workflowTaskStatus := TaskScheduled
			if query.Status == DecisionPending {
				workflowTaskStatusPredicate = `assigned_task.status = ?`
				workflowTaskStatus = TaskPending
			}
			where += ` AND ((NOT EXISTS (
				SELECT 1
				FROM hil_workflow_instances AS owned_instance
				WHERE owned_instance.tenant_id = hil_decision_requests.tenant_id
				  AND owned_instance.decision_request_id = hil_decision_requests.id
			) AND (` + legacyPredicate + `)) OR EXISTS (
				SELECT 1
				FROM hil_workflow_instances AS assigned_instance
				JOIN hil_workflow_tasks AS assigned_task
				  ON assigned_task.tenant_id = assigned_instance.tenant_id
				 AND assigned_task.workflow_instance_id = assigned_instance.id
				WHERE assigned_instance.tenant_id = hil_decision_requests.tenant_id
				  AND assigned_instance.decision_request_id = hil_decision_requests.id
				  AND assigned_task.current_assignee_kind = ?
				  AND assigned_task.current_assignee_id = ?
				  AND ` + workflowTaskStatusPredicate + `
			))`
			args = append(args, legacyArgument, actor.Kind, actor.ID, workflowTaskStatus)
		default:
			return DecisionPage{}, ErrInvalidRequest
		}
	}
	var total int
	if err := r.store.QueryRow(ctx, `SELECT COUNT(*) FROM hil_decision_requests`+where, args...).Scan(&total); err != nil {
		return DecisionPage{}, fmt.Errorf("count HIL decisions: %w", err)
	}
	limit, offset := normalizePage(query.Limit, query.Offset)
	rows, err := r.store.Query(ctx, decisionSelectSQL()+where+` ORDER BY created_at DESC, id LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return DecisionPage{}, fmt.Errorf("list HIL decisions: %w", err)
	}
	defer rows.Close()
	page := DecisionPage{Items: make([]DecisionRequest, 0), Total: total}
	for rows.Next() {
		decision, err := scanDecision(rows)
		if err != nil {
			return DecisionPage{}, err
		}
		page.Items = append(page.Items, decision)
	}
	return page, rows.Err()
}

func decisionListTenantIDs(query DecisionListQuery) []string {
	candidates := make([]string, 0, 1+len(query.AdditionalTenantIDs))
	candidates = append(candidates, query.TenantID)
	candidates = append(candidates, query.AdditionalTenantIDs...)
	seen := make(map[string]struct{}, len(candidates))
	tenantIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		tenantID := strings.TrimSpace(candidate)
		if tenantID == "" {
			continue
		}
		if _, exists := seen[tenantID]; exists {
			continue
		}
		seen[tenantID] = struct{}{}
		tenantIDs = append(tenantIDs, tenantID)
	}
	return tenantIDs
}

func decisionListPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func escapeLikeValue(value string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
}

func approvalAssigneePredicate(dialect db.Dialect, actorID string) (string, any) {
	if dialect == db.DialectPostgres {
		return `payload_preview_json ->> 'approval_assigned_to' = ?`, actorID
	}
	encodedActor, _ := json.Marshal(actorID)
	token := `"approval_assigned_to":` + string(encodedActor)
	return `payload_preview_json LIKE ? ESCAPE '\'`, "%" + escapeLikeValue(token) + "%"
}

// ResolveDecision atomically closes a non-council pending decision and records
// the receipt consumed by the waiting runtime. Council decisions deliberately
// use ResolveCouncil so quorum and vote invariants cannot be bypassed.
func (r *PlatformRepository) ResolveDecision(ctx context.Context, decision DecisionRequest, receipt DecisionReceipt) error {
	if r == nil || r.store == nil || strings.TrimSpace(decision.TenantID) == "" || strings.TrimSpace(decision.ID) == "" ||
		(decision.Route.Mode != ModeHIL && decision.Route.Mode != ModeApproval) || decision.Status == DecisionPending ||
		receipt.RequestID != decision.ID || receipt.TenantID != decision.TenantID || receipt.ArgsHash != decision.ArgsHash || strings.TrimSpace(receipt.ID) == "" {
		return ErrInvalidRequest
	}
	if decision.Status != DecisionResolved && decision.Status != DecisionDenied {
		return ErrInvalidRequest
	}
	if (decision.Status == DecisionResolved && receipt.Effect != EffectAuthorize) || (decision.Status == DecisionDenied && receipt.Effect != EffectDeny) {
		return ErrInvalidRequest
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		if evidence, ok := receipt.ResolverSummary["delegation"].(DelegationEvidence); ok {
			if err := r.lockDelegatedDecision(ctx, tx, decision.TenantID, decision.ID, evidence); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `
UPDATE hil_decision_requests SET status = ?
WHERE tenant_id = ? AND id = ? AND args_hash = ? AND route_mode IN (?, ?) AND status = ?`,
			decision.Status, decision.TenantID, decision.ID, decision.ArgsHash, ModeHIL, ModeApproval, DecisionPending)
		if err != nil {
			return fmt.Errorf("resolve HIL decision: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("resolve HIL decision rows affected: %w", err)
		}
		if affected != 1 {
			var current DecisionStatus
			err = tx.QueryRowContext(ctx, `SELECT status FROM hil_decision_requests WHERE tenant_id = ? AND id = ?`, decision.TenantID, decision.ID).Scan(&current)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO hil_decision_receipts (
		id, tenant_id, request_id, council_id, args_hash, effect, single_use, created_at, expires_at, used_at,
		resolver_actor_kind, resolver_actor_id, resolver_reason, resolver_summary_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, receipt.ID, receipt.TenantID, receipt.RequestID, receipt.CouncilID,
			receipt.ArgsHash, receipt.Effect, receipt.SingleUse, timeToInt(receipt.CreatedAt), optionalTimeToInt(receipt.ExpiresAt), optionalTimeToInt(receipt.UsedAt),
			receipt.ResolverActorKind, receipt.ResolverActorID, receipt.ResolverReason, mustJSON(receipt.ResolverSummary)); err != nil {
			return fmt.Errorf("create HIL decision receipt: %w", err)
		}
		return nil
	})
}

// Lock the reviewed row through commit. Version/hash-only checks would miss a
// concurrent reassignment or a change to preview material with unchanged args.
func (r *PlatformRepository) lockDelegatedDecision(ctx context.Context, tx db.Tx, tenant, id string, evidence DelegationEvidence) error {
	query := decisionSelectSQL() + ` WHERE tenant_id = ? AND id = ?`
	if r.store.Dialect() == db.DialectPostgres {
		query += ` FOR UPDATE`
	}
	current, err := scanDecision(tx.QueryRowContext(ctx, query, tenant, id))
	if err != nil {
		return err
	}
	if !DelegatedDecisionEligible(current) || DecisionReviewFingerprint(current) != evidence.ReviewFingerprint {
		return ErrConflict
	}
	return nil
}

func (r *PlatformRepository) ConsumeDecisionReceipt(ctx context.Context, tenantID, requestID, receiptID string, now time.Time) error {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(requestID) == "" || strings.TrimSpace(receiptID) == "" || now.IsZero() {
		return ErrInvalidRequest
	}
	result, err := r.store.Exec(ctx, `
UPDATE hil_decision_receipts SET used_at = ?
WHERE tenant_id = ? AND request_id = ? AND id = ? AND single_use = ? AND used_at IS NULL
  AND (expires_at IS NULL OR expires_at > ?)`, timeToInt(now), tenantID, requestID, receiptID, true, timeToInt(now))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrConflict
	}
	return nil
}

func (r *PlatformRepository) CreateCouncil(ctx context.Context, session CouncilSession) error {
	if r == nil || r.store == nil || strings.TrimSpace(session.TenantID) == "" {
		return ErrInvalidRequest
	}
	if err := validateCouncil(session); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		return insertCouncil(ctx, tx, session)
	})
}

func insertCouncil(ctx context.Context, tx db.Tx, session CouncilSession) error {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO hil_council_sessions (
	id, tenant_id, request_id, args_hash, risk_level, status, effect, quorum,
	approval_threshold, dissent_json, created_at, updated_at, expires_at, resolved_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.TenantID, session.RequestID, session.ArgsHash, session.RiskLevel, session.Status,
		session.Effect, session.Quorum, session.ApprovalThreshold, mustJSON(session.Dissent),
		timeToInt(session.CreatedAt), timeToInt(session.UpdatedAt), optionalTimeToInt(session.ExpiresAt), optionalTimeToInt(session.ResolvedAt),
	); err != nil {
		return fmt.Errorf("create council: %w", err)
	}
	for _, participant := range session.Participants {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO hil_council_participants (
	tenant_id, council_id, actor_kind, actor_id, display_name, role, required
) VALUES (?, ?, ?, ?, ?, ?, ?)`, session.TenantID, session.ID, participant.Actor.Kind, participant.Actor.ID, participant.DisplayName, participant.Role, participant.Required); err != nil {
			return fmt.Errorf("create council participant: %w", err)
		}
	}
	return nil
}

func (r *PlatformRepository) GetCouncil(ctx context.Context, tenantID, councilID string) (CouncilSession, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(councilID) == "" {
		return CouncilSession{}, ErrInvalidRequest
	}
	var session CouncilSession
	var dissentJSON string
	var createdAt, updatedAt int64
	var expiresAt, resolvedAt sql.NullInt64
	err := r.store.QueryRow(ctx, `
SELECT id, tenant_id, request_id, args_hash, risk_level, status, effect, quorum,
	approval_threshold, dissent_json, created_at, updated_at, expires_at, resolved_at
FROM hil_council_sessions WHERE tenant_id = ? AND id = ?`, tenantID, councilID).Scan(
		&session.ID, &session.TenantID, &session.RequestID, &session.ArgsHash, &session.RiskLevel,
		&session.Status, &session.Effect, &session.Quorum, &session.ApprovalThreshold, &dissentJSON,
		&createdAt, &updatedAt, &expiresAt, &resolvedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CouncilSession{}, ErrNotFound
	}
	if err != nil {
		return CouncilSession{}, fmt.Errorf("get council: %w", err)
	}
	decodeJSON(dissentJSON, &session.Dissent)
	session.CreatedAt = intToTime(createdAt)
	session.UpdatedAt = intToTime(updatedAt)
	session.ExpiresAt = nullIntToTime(expiresAt)
	session.ResolvedAt = nullIntToTime(resolvedAt)
	var loadErr error
	if session.Participants, loadErr = r.listParticipants(ctx, tenantID, councilID); loadErr != nil {
		return CouncilSession{}, loadErr
	}
	if session.Contributions, loadErr = r.listContributions(ctx, tenantID, councilID); loadErr != nil {
		return CouncilSession{}, loadErr
	}
	if session.Votes, loadErr = r.listVotes(ctx, tenantID, councilID); loadErr != nil {
		return CouncilSession{}, loadErr
	}
	return session, nil
}

func (r *PlatformRepository) GetCouncilByRequestID(ctx context.Context, tenantID, requestID string) (CouncilSession, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(requestID) == "" {
		return CouncilSession{}, ErrInvalidRequest
	}
	var councilID string
	err := r.store.QueryRow(ctx, `SELECT id FROM hil_council_sessions WHERE tenant_id = ? AND request_id = ?`, tenantID, requestID).Scan(&councilID)
	if errors.Is(err, sql.ErrNoRows) {
		return CouncilSession{}, ErrNotFound
	}
	if err != nil {
		return CouncilSession{}, fmt.Errorf("get council by request: %w", err)
	}
	return r.GetCouncil(ctx, tenantID, councilID)
}

func (r *PlatformRepository) SaveContribution(ctx context.Context, tenantID, councilID string, contribution CouncilContribution, idempotencyKey, fingerprint string) error {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(councilID) == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(fingerprint) == "" {
		return ErrInvalidRequest
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		var existingFingerprint string
		err := tx.QueryRowContext(ctx, `SELECT request_fingerprint FROM hil_council_contributions WHERE tenant_id = ? AND idempotency_key = ?`, tenantID, idempotencyKey).Scan(&existingFingerprint)
		if err == nil {
			if existingFingerprint == fingerprint {
				return nil
			}
			return ErrIdempotencyConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var councilStatus CouncilStatus
		if err := tx.QueryRowContext(ctx, `SELECT status FROM hil_council_sessions WHERE tenant_id = ? AND id = ?`, tenantID, councilID).Scan(&councilStatus); err != nil {
			return err
		}
		if councilStatus != CouncilOpen {
			return ErrConflict
		}
		var voteCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM hil_council_votes WHERE tenant_id = ? AND council_id = ? AND actor_kind = ? AND actor_id = ?`, tenantID, councilID, contribution.Actor.Kind, contribution.Actor.ID).Scan(&voteCount); err != nil {
			return err
		}
		if voteCount > 0 {
			return ErrVoteImmutable
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO hil_council_contributions (
	tenant_id, council_id, actor_kind, actor_id, position, reasoning, evidence_refs_json,
	idempotency_key, request_fingerprint, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, council_id, actor_kind, actor_id) DO UPDATE SET
	position = excluded.position,
	reasoning = excluded.reasoning,
	evidence_refs_json = excluded.evidence_refs_json,
	idempotency_key = excluded.idempotency_key,
	request_fingerprint = excluded.request_fingerprint,
	updated_at = excluded.updated_at`, tenantID, councilID, contribution.Actor.Kind, contribution.Actor.ID,
			contribution.Position, contribution.Reasoning, mustJSON(contribution.EvidenceRefs), idempotencyKey,
			fingerprint, timeToInt(contribution.CreatedAt), timeToInt(contribution.UpdatedAt))
		return err
	})
}

func (r *PlatformRepository) SaveVote(ctx context.Context, tenantID, councilID string, vote CouncilVote, idempotencyKey, fingerprint string) error {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(councilID) == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(fingerprint) == "" {
		return ErrInvalidRequest
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		var idempotentCouncilID, idempotentActorKind, idempotentActorID, idempotentFingerprint string
		err := tx.QueryRowContext(ctx, `
SELECT council_id, actor_kind, actor_id, request_fingerprint FROM hil_council_votes
WHERE tenant_id = ? AND idempotency_key = ?`, tenantID, idempotencyKey).Scan(&idempotentCouncilID, &idempotentActorKind, &idempotentActorID, &idempotentFingerprint)
		if err == nil {
			if idempotentCouncilID == councilID && idempotentActorKind == string(vote.Actor.Kind) && idempotentActorID == vote.Actor.ID && idempotentFingerprint == fingerprint {
				return nil
			}
			return ErrIdempotencyConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var existingChoice VoteChoice
		var existingKey, existingFingerprint string
		err = tx.QueryRowContext(ctx, `
SELECT choice, idempotency_key, request_fingerprint FROM hil_council_votes
WHERE tenant_id = ? AND council_id = ? AND actor_kind = ? AND actor_id = ?`, tenantID, councilID, vote.Actor.Kind, vote.Actor.ID).Scan(&existingChoice, &existingKey, &existingFingerprint)
		if err == nil {
			if existingChoice == vote.Choice && existingKey == idempotencyKey && existingFingerprint == fingerprint {
				return nil
			}
			return ErrVoteImmutable
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var councilStatus CouncilStatus
		if err := tx.QueryRowContext(ctx, `SELECT status FROM hil_council_sessions WHERE tenant_id = ? AND id = ?`, tenantID, councilID).Scan(&councilStatus); err != nil {
			return err
		}
		if councilStatus != CouncilOpen {
			return ErrConflict
		}
		var participantCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM hil_council_participants WHERE tenant_id = ? AND council_id = ? AND actor_kind = ? AND actor_id = ?`, tenantID, councilID, vote.Actor.Kind, vote.Actor.ID).Scan(&participantCount); err != nil {
			return err
		}
		if participantCount == 0 {
			return ErrInvalidRequest
		}
		var contributionCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM hil_council_contributions WHERE tenant_id = ? AND council_id = ? AND actor_kind = ? AND actor_id = ?`, tenantID, councilID, vote.Actor.Kind, vote.Actor.ID).Scan(&contributionCount); err != nil {
			return err
		}
		if contributionCount == 0 {
			return ErrContributionRequired
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO hil_council_votes (
	tenant_id, council_id, actor_kind, actor_id, choice, reason,
	idempotency_key, request_fingerprint, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, tenantID, councilID, vote.Actor.Kind, vote.Actor.ID,
			vote.Choice, vote.Reason, idempotencyKey, fingerprint, timeToInt(vote.CreatedAt))
		return err
	})
}

func (r *PlatformRepository) ResolveCouncil(ctx context.Context, session CouncilSession, receipt DecisionReceipt) error {
	if r == nil || r.store == nil || session.Status == CouncilOpen || strings.TrimSpace(session.TenantID) == "" || receipt.RequestID != session.RequestID || receipt.ArgsHash != session.ArgsHash {
		return ErrInvalidRequest
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		if session.Status == CouncilPassed && session.Effect == EffectRequiresApproval {
			var payloadJSON string
			err := tx.QueryRowContext(ctx, `
SELECT payload_preview_json FROM hil_decision_requests
WHERE tenant_id = ? AND id = ? AND args_hash = ? AND route_mode = ? AND status = ?`,
				session.TenantID, session.RequestID, session.ArgsHash, ModeCouncil, DecisionPending).Scan(&payloadJSON)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return fmt.Errorf("load fixed-approval assignee for council handoff: %w", err)
			}
			payload := map[string]any{}
			if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || !validApprovalAssigneeForRoute(RouteDecision{Mode: ModeCouncil, CouncilEffect: EffectRequiresApproval}, payload) {
				return ErrInvalidRequest
			}
		}
		result, err := tx.ExecContext(ctx, `
UPDATE hil_council_sessions SET status = ?, effect = ?, dissent_json = ?, updated_at = ?, resolved_at = ?
WHERE tenant_id = ? AND id = ? AND status = ?`, session.Status, session.Effect, mustJSON(session.Dissent),
			timeToInt(session.ResolvedAt), timeToInt(session.ResolvedAt), session.TenantID, session.ID, CouncilOpen)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return ErrConflict
		}
		decisionStatus := DecisionResolved
		decisionMode := ModeCouncil
		if session.Status == CouncilRejected || session.Status == CouncilExpired {
			decisionStatus = DecisionDenied
		}
		// A successful high-risk council is deliberately only the first gate.
		// Keep the immutable request and its original args hash, but move it into
		// the fixed-approval queue in the same transaction as the council receipt.
		if session.Status == CouncilPassed && session.Effect == EffectRequiresApproval {
			decisionStatus = DecisionPending
			decisionMode = ModeApproval
		}
		decisionResult, err := tx.ExecContext(ctx, `
UPDATE hil_decision_requests SET status = ?, route_mode = ?
WHERE tenant_id = ? AND id = ? AND args_hash = ? AND route_mode = ? AND status = ?`,
			decisionStatus, decisionMode, session.TenantID, session.RequestID, session.ArgsHash, ModeCouncil, DecisionPending)
		if err != nil {
			return err
		}
		affected, err = decisionResult.RowsAffected()
		if err != nil || affected != 1 {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO hil_decision_receipts (
	id, tenant_id, request_id, council_id, args_hash, effect, single_use, created_at, expires_at, used_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, receipt.ID, receipt.TenantID, receipt.RequestID, receipt.CouncilID,
			receipt.ArgsHash, receipt.Effect, receipt.SingleUse, timeToInt(receipt.CreatedAt), optionalTimeToInt(receipt.ExpiresAt), optionalTimeToInt(receipt.UsedAt))
		return err
	})
}

func (r *PlatformRepository) ListDecisionReceipts(ctx context.Context, tenantID, requestID string) ([]DecisionReceipt, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(requestID) == "" {
		return nil, ErrInvalidRequest
	}
	rows, err := r.store.Query(ctx, `
SELECT id, tenant_id, request_id, council_id, workflow_instance_id, args_hash, material_hash,
	effect, resolver_actor_kind, resolver_actor_id, resolver_reason, resolver_summary_json,
	single_use, created_at, expires_at, used_at
FROM hil_decision_receipts WHERE tenant_id = ? AND request_id = ? ORDER BY created_at, id`, tenantID, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := []DecisionReceipt{}
	for rows.Next() {
		var receipt DecisionReceipt
		var resolverSummaryJSON string
		var createdAt int64
		var expiresAt, usedAt sql.NullInt64
		if err := rows.Scan(&receipt.ID, &receipt.TenantID, &receipt.RequestID, &receipt.CouncilID,
			&receipt.WorkflowInstanceID, &receipt.ArgsHash, &receipt.MaterialHash, &receipt.Effect,
			&receipt.ResolverActorKind, &receipt.ResolverActorID, &receipt.ResolverReason,
			&resolverSummaryJSON, &receipt.SingleUse, &createdAt, &expiresAt, &usedAt); err != nil {
			return nil, err
		}
		decodeJSON(resolverSummaryJSON, &receipt.ResolverSummary)
		receipt.CreatedAt = intToTime(createdAt)
		receipt.ExpiresAt = nullIntToTime(expiresAt)
		receipt.UsedAt = nullIntToTime(usedAt)
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

func (r *PlatformRepository) listParticipants(ctx context.Context, tenantID, councilID string) ([]CouncilParticipant, error) {
	rows, err := r.store.Query(ctx, `
SELECT actor_kind, actor_id, display_name, role, required FROM hil_council_participants
WHERE tenant_id = ? AND council_id = ? ORDER BY required DESC, actor_kind DESC, actor_id`, tenantID, councilID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CouncilParticipant
	for rows.Next() {
		var participant CouncilParticipant
		if err := rows.Scan(&participant.Actor.Kind, &participant.Actor.ID, &participant.DisplayName, &participant.Role, &participant.Required); err != nil {
			return nil, err
		}
		out = append(out, participant)
	}
	return out, rows.Err()
}

func (r *PlatformRepository) listContributions(ctx context.Context, tenantID, councilID string) ([]CouncilContribution, error) {
	rows, err := r.store.Query(ctx, `
SELECT actor_kind, actor_id, position, reasoning, evidence_refs_json, idempotency_key, created_at, updated_at
FROM hil_council_contributions WHERE tenant_id = ? AND council_id = ? ORDER BY created_at, actor_kind, actor_id`, tenantID, councilID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CouncilContribution
	for rows.Next() {
		var contribution CouncilContribution
		var evidenceJSON string
		var createdAt, updatedAt int64
		if err := rows.Scan(&contribution.Actor.Kind, &contribution.Actor.ID, &contribution.Position, &contribution.Reasoning, &evidenceJSON, &contribution.IdempotencyKey, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		decodeJSON(evidenceJSON, &contribution.EvidenceRefs)
		contribution.CreatedAt = intToTime(createdAt)
		contribution.UpdatedAt = intToTime(updatedAt)
		out = append(out, contribution)
	}
	return out, rows.Err()
}

func (r *PlatformRepository) listVotes(ctx context.Context, tenantID, councilID string) ([]CouncilVote, error) {
	rows, err := r.store.Query(ctx, `
SELECT actor_kind, actor_id, choice, reason, created_at FROM hil_council_votes
WHERE tenant_id = ? AND council_id = ? ORDER BY created_at, actor_kind, actor_id`, tenantID, councilID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CouncilVote
	for rows.Next() {
		var vote CouncilVote
		var createdAt int64
		if err := rows.Scan(&vote.Actor.Kind, &vote.Actor.ID, &vote.Choice, &vote.Reason, &createdAt); err != nil {
			return nil, err
		}
		vote.CreatedAt = intToTime(createdAt)
		out = append(out, vote)
	}
	return out, rows.Err()
}

func decisionSelectSQL() string {
	return `SELECT id, tenant_id, requester_kind, requester_id, action, action_type_id, resource_kind,
	resource_id, object_refs_json, args_hash, purpose, risk_level, preferred_mode, route_mode,
	route_effect, route_reasons_json, context_json, payload_preview_json, idempotency_key,
	request_fingerprint, status, created_at, expires_at FROM hil_decision_requests`
}

type rowScanner interface {
	Scan(...any) error
}

func scanDecision(row rowScanner) (DecisionRequest, error) {
	var decision DecisionRequest
	var objectRefsJSON, reasonsJSON, contextJSON, payloadJSON string
	var createdAt int64
	var expiresAt sql.NullInt64
	err := row.Scan(
		&decision.ID, &decision.TenantID, &decision.Requester.Kind, &decision.Requester.ID, &decision.Action,
		&decision.ActionTypeID, &decision.Resource.Kind, &decision.Resource.ID, &objectRefsJSON, &decision.ArgsHash,
		&decision.Purpose, &decision.RiskLevel, &decision.PreferredMode, &decision.Route.Mode,
		&decision.Route.CouncilEffect, &reasonsJSON, &contextJSON, &payloadJSON, &decision.IdempotencyKey,
		&decision.RequestFingerprint, &decision.Status, &createdAt, &expiresAt,
	)
	if err != nil {
		return DecisionRequest{}, err
	}
	decodeJSON(objectRefsJSON, &decision.ObjectRefs)
	decodeJSON(reasonsJSON, &decision.Route.Reasons)
	decodeJSON(contextJSON, &decision.Context)
	decodeJSON(payloadJSON, &decision.PayloadPreview)
	decision.CreatedAt = intToTime(createdAt)
	decision.ExpiresAt = nullIntToTime(expiresAt)
	return decision, nil
}

func mustJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(payload)
}

func decodeJSON(payload string, target any) {
	_ = json.Unmarshal([]byte(payload), target)
}

func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func timeToInt(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func optionalTimeToInt(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return timeToInt(value)
}

func intToTime(value int64) time.Time {
	return time.Unix(0, value).UTC()
}

func nullIntToTime(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return intToTime(value.Int64)
}
