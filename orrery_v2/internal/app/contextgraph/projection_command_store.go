package contextgraph

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

var ErrProjectionAuditCommandConflict = errors.New("projection audit command conflicts with current state")
var ErrProjectionAuditCommandEffect = errors.New("projection audit command effect failed")
var ErrProjectionAuditCommandFence = errors.New("projection audit command security fence failed")

const projectionAuditCommandLeaseDuration = 5 * time.Minute

type projectionAuditCommandRecord struct {
	TenantID         string
	IdempotencyKey   string
	AuditID          string
	Operation        string
	RequestHash      string
	ExpectedRevision int64
	State            ProjectionAuditCommandState
	ResultStatus     string
	ReasonCode       string
	ObservedAt       *time.Time
	FailureCode      string
}

type projectionAuditCommandLeaseRecord struct {
	IdempotencyKey   string
	ExpectedRevision int64
	LeaseToken       string
	LeaseExpiresAt   time.Time
}

func (s *PlatformStore) ClaimProjectionAuditCommand(ctx context.Context, request ProjectionAuditCommandClaimRequest) (ProjectionAuditCommandClaim, error) {
	request = normalizeProjectionAuditCommandClaim(request)
	if !s.ready() {
		return ProjectionAuditCommandClaim{}, fmt.Errorf("context graph platform store is required")
	}
	if err := validateProjectionAuditCommandClaim(request); err != nil {
		return ProjectionAuditCommandClaim{}, err
	}
	var claim ProjectionAuditCommandClaim
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		existing, found, err := projectionAuditCommandByKeyTx(ctx, tx, request.Audit.TenantID, request.IdempotencyKey)
		if err != nil {
			return err
		}
		if found {
			if !projectionAuditCommandMatches(existing, request.Audit.ID, request.Operation, request.RequestHash) {
				return ErrProjectionAuditCommandConflict
			}
			claim = projectionAuditCommandClaimFromRecord(existing)
			if existing.State != ProjectionAuditCommandClaimed {
				return nil
			}
			return acquireExistingProjectionAuditCommandLease(ctx, tx, existing, &claim)
		}
		if err := releaseExpiredCompetingProjectionAuditCommandTx(ctx, tx, request.Audit.TenantID, request.Audit.ID); err != nil {
			return err
		}

		now := time.Now().UTC()
		leaseToken, err := newProjectionAuditCommandLeaseToken()
		if err != nil {
			return err
		}
		leaseExpiresAt := now.Add(projectionAuditCommandLeaseDuration)
		inserted, err := tx.ExecContext(ctx, `
INSERT INTO context_graph_projection_audit_commands (
	tenant_id, idempotency_key, audit_id, operation, request_hash, expected_audit_revision,
	state, result_status, result_reason_code, failure_code, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, '', '', '', ?, ?)
ON CONFLICT(tenant_id, idempotency_key) DO NOTHING`,
			request.Audit.TenantID, request.IdempotencyKey, request.Audit.ID, request.Operation, request.RequestHash,
			request.Audit.Revision, ProjectionAuditCommandClaimed, timeToUnixNano(now), timeToUnixNano(now),
		)
		if err != nil {
			return fmt.Errorf("create projection audit command: %w", err)
		}
		created, err := exactlyOneRowAffected(inserted)
		if err != nil {
			return fmt.Errorf("read projection audit command insert count: %w", err)
		}
		if !created {
			existing, found, err = projectionAuditCommandByKeyTx(ctx, tx, request.Audit.TenantID, request.IdempotencyKey)
			if err != nil {
				return err
			}
			if !found || !projectionAuditCommandMatches(existing, request.Audit.ID, request.Operation, request.RequestHash) {
				return ErrProjectionAuditCommandConflict
			}
			claim = projectionAuditCommandClaimFromRecord(existing)
			if existing.State != ProjectionAuditCommandClaimed {
				return nil
			}
			return acquireExistingProjectionAuditCommandLease(ctx, tx, existing, &claim)
		}

		claimed, err := tx.ExecContext(ctx, `
INSERT INTO context_graph_projection_audit_command_claims (
	tenant_id, audit_id, idempotency_key, operation, expected_audit_revision, claimed_at, lease_token, lease_expires_at
)
SELECT tenant_id, id, ?, ?, revision, ?, ?, ?
FROM context_graph_projection_audits
WHERE tenant_id = ? AND id = ? AND revision = ? AND status = 'skipped' AND access_state = 'resolved'
ON CONFLICT(tenant_id, audit_id) DO NOTHING`,
			request.IdempotencyKey, request.Operation, timeToUnixNano(now), leaseToken, timeToUnixNano(leaseExpiresAt),
			request.Audit.TenantID, request.Audit.ID, request.Audit.Revision,
		)
		if err != nil {
			return fmt.Errorf("claim projection audit command: %w", err)
		}
		acquired, err := exactlyOneRowAffected(claimed)
		if err != nil {
			return fmt.Errorf("read projection audit claim count: %w", err)
		}
		if !acquired {
			return ErrProjectionAuditCommandConflict
		}
		claim = ProjectionAuditCommandClaim{
			Acquired: true, State: ProjectionAuditCommandClaimed, LeaseToken: leaseToken, LeaseExpiresAt: &leaseExpiresAt,
		}
		return nil
	})
	if err != nil {
		return ProjectionAuditCommandClaim{}, err
	}
	return claim, nil
}

func releaseExpiredCompetingProjectionAuditCommandTx(ctx context.Context, tx db.Tx, tenantID, auditID string) error {
	lease, found, err := projectionAuditCommandLeaseTx(ctx, tx, tenantID, auditID)
	if err != nil || !found {
		return err
	}
	if lease.LeaseExpiresAt.After(time.Now().UTC()) {
		return ErrProjectionAuditCommandConflict
	}
	command, found, err := projectionAuditCommandByKeyTx(ctx, tx, tenantID, lease.IdempotencyKey)
	if err != nil {
		return err
	}
	if !found || command.AuditID != auditID || command.ExpectedRevision != lease.ExpectedRevision || command.State != ProjectionAuditCommandClaimed {
		return ErrProjectionAuditCommandConflict
	}
	failureCode := "lease_expired"
	current, auditErr := projectionAuditByIDTx(ctx, tx, tenantID, auditID)
	if auditErr != nil && !errors.Is(auditErr, sql.ErrNoRows) {
		return fmt.Errorf("read projection audit while releasing expired command: %w", auditErr)
	}
	if errors.Is(auditErr, sql.ErrNoRows) || current.Revision != command.ExpectedRevision || current.Status != "skipped" || current.AccessState != AccessStateResolved {
		failureCode = "state_conflict"
	}
	return failAbandonedProjectionAuditCommandTx(ctx, tx, command, failureCode)
}

func acquireExistingProjectionAuditCommandLease(ctx context.Context, tx db.Tx, command projectionAuditCommandRecord, claim *ProjectionAuditCommandClaim) error {
	lease, found, err := projectionAuditCommandLeaseTx(ctx, tx, command.TenantID, command.AuditID)
	if err != nil {
		return err
	}
	if found && (lease.IdempotencyKey != command.IdempotencyKey || lease.ExpectedRevision != command.ExpectedRevision) {
		return ErrProjectionAuditCommandConflict
	}
	now := time.Now().UTC()
	if found && lease.LeaseExpiresAt.After(now) {
		return nil
	}
	leaseToken, err := newProjectionAuditCommandLeaseToken()
	if err != nil {
		return err
	}
	leaseExpiresAt := now.Add(projectionAuditCommandLeaseDuration)
	var result sql.Result
	if found {
		result, err = tx.ExecContext(ctx, `
UPDATE context_graph_projection_audit_command_claims
SET lease_token = ?, lease_expires_at = ?, claimed_at = ?
WHERE tenant_id = ? AND audit_id = ? AND idempotency_key = ? AND expected_audit_revision = ? AND lease_expires_at <= ?
	AND EXISTS (
		SELECT 1 FROM context_graph_projection_audits
		WHERE tenant_id = ? AND id = ? AND revision = ? AND status = 'skipped' AND access_state = 'resolved'
	)`, leaseToken, timeToUnixNano(leaseExpiresAt), timeToUnixNano(now),
			command.TenantID, command.AuditID, command.IdempotencyKey, command.ExpectedRevision, timeToUnixNano(now),
			command.TenantID, command.AuditID, command.ExpectedRevision)
	} else {
		result, err = tx.ExecContext(ctx, `
INSERT INTO context_graph_projection_audit_command_claims (
	tenant_id, audit_id, idempotency_key, operation, expected_audit_revision, claimed_at, lease_token, lease_expires_at
)
SELECT tenant_id, id, ?, ?, revision, ?, ?, ?
FROM context_graph_projection_audits
WHERE tenant_id = ? AND id = ? AND revision = ? AND status = 'skipped' AND access_state = 'resolved'
ON CONFLICT(tenant_id, audit_id) DO NOTHING`,
			command.IdempotencyKey, command.Operation, timeToUnixNano(now), leaseToken, timeToUnixNano(leaseExpiresAt),
			command.TenantID, command.AuditID, command.ExpectedRevision)
	}
	if err != nil {
		return fmt.Errorf("take over projection audit command lease: %w", err)
	}
	acquired, err := exactlyOneRowAffected(result)
	if err != nil {
		return err
	}
	if acquired {
		*claim = projectionAuditCommandClaimFromRecord(command)
		claim.Acquired, claim.LeaseToken, claim.LeaseExpiresAt = true, leaseToken, &leaseExpiresAt
		return nil
	}
	current, auditErr := projectionAuditByIDTx(ctx, tx, command.TenantID, command.AuditID)
	if auditErr != nil && !errors.Is(auditErr, sql.ErrNoRows) {
		return fmt.Errorf("read projection audit after lease takeover conflict: %w", auditErr)
	}
	if errors.Is(auditErr, sql.ErrNoRows) || current.Revision != command.ExpectedRevision || current.Status != "skipped" {
		if err := failAbandonedProjectionAuditCommandTx(ctx, tx, command, "state_conflict"); err != nil {
			return err
		}
		*claim = ProjectionAuditCommandClaim{State: ProjectionAuditCommandFailed, FailureCode: "state_conflict"}
	}
	return nil
}

func projectionAuditCommandLeaseTx(ctx context.Context, tx db.Tx, tenantID, auditID string) (projectionAuditCommandLeaseRecord, bool, error) {
	var lease projectionAuditCommandLeaseRecord
	var expiresAt int64
	err := tx.QueryRowContext(ctx, `
SELECT idempotency_key, expected_audit_revision, lease_token, lease_expires_at
FROM context_graph_projection_audit_command_claims
WHERE tenant_id = ? AND audit_id = ?`, tenantID, auditID).Scan(
		&lease.IdempotencyKey, &lease.ExpectedRevision, &lease.LeaseToken, &expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return projectionAuditCommandLeaseRecord{}, false, nil
	}
	if err != nil {
		return projectionAuditCommandLeaseRecord{}, false, fmt.Errorf("read projection audit command lease: %w", err)
	}
	lease.LeaseExpiresAt = unixNanoToTime(expiresAt)
	return lease, true, nil
}

func newProjectionAuditCommandLeaseToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate projection audit command lease: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func failAbandonedProjectionAuditCommandTx(ctx context.Context, tx db.Tx, command projectionAuditCommandRecord, failureCode string) error {
	updated, err := tx.ExecContext(ctx, `
UPDATE context_graph_projection_audit_commands
SET state = ?, failure_code = ?, updated_at = ?
WHERE tenant_id = ? AND idempotency_key = ? AND audit_id = ? AND operation = ? AND request_hash = ?
	AND expected_audit_revision = ? AND state = ?`,
		ProjectionAuditCommandFailed, failureCode, timeToUnixNano(time.Now().UTC()),
		command.TenantID, command.IdempotencyKey, command.AuditID, command.Operation, command.RequestHash,
		command.ExpectedRevision, ProjectionAuditCommandClaimed)
	if err != nil {
		return fmt.Errorf("fail abandoned projection audit command: %w", err)
	}
	changed, err := exactlyOneRowAffected(updated)
	if err != nil {
		return err
	}
	if !changed {
		return ErrProjectionAuditCommandConflict
	}
	deleted, err := tx.ExecContext(ctx, `
DELETE FROM context_graph_projection_audit_command_claims
WHERE tenant_id = ? AND audit_id = ? AND idempotency_key = ? AND expected_audit_revision = ?`,
		command.TenantID, command.AuditID, command.IdempotencyKey, command.ExpectedRevision)
	if err != nil {
		return fmt.Errorf("release abandoned projection audit command claim: %w", err)
	}
	changed, err = exactlyOneRowAffected(deleted)
	if err != nil {
		return err
	}
	if !changed {
		return ErrProjectionAuditCommandConflict
	}
	return nil
}

func (s *PlatformStore) CompleteProjectionAuditCommand(ctx context.Context, completion ProjectionAuditCommandCompletion) error {
	completion = normalizeProjectionAuditCommandCompletion(completion)
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if err := validateProjectionAuditCommandCompletion(completion); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		command, found, err := projectionAuditCommandByKeyTx(ctx, tx, completion.TenantID, completion.IdempotencyKey)
		if err != nil {
			return err
		}
		if !found || !projectionAuditCommandMatches(command, completion.AuditID, completion.Operation, completion.RequestHash) {
			return ErrProjectionAuditCommandConflict
		}
		if command.State == ProjectionAuditCommandSucceeded {
			if command.ResultStatus == completion.Result.Status && command.ReasonCode == completion.Result.ReasonCode && sameOptionalProjectionTime(command.ObservedAt, completion.Result.ObservedAt) {
				return nil
			}
			return ErrProjectionAuditCommandConflict
		}
		if command.State != ProjectionAuditCommandClaimed {
			return ErrProjectionAuditCommandConflict
		}
		if err := verifyProjectionAuditCommandLeaseTx(ctx, tx, command, completion.LeaseToken); err != nil {
			return ErrProjectionAuditCommandConflict
		}
		if err := fenceProjectionAuditCommandSecurityTx(ctx, tx, completion.Fence); err != nil {
			return errors.Join(ErrProjectionAuditCommandConflict, ErrProjectionAuditCommandFence, err)
		}
		if completion.Effect != nil {
			if err := completion.Effect(ctx, NewPlatformStoreTx(tx)); err != nil {
				return errors.Join(ErrProjectionAuditCommandEffect, err)
			}
		}
		persisted, err := updateProjectionAuditCommandResultTx(ctx, tx, command, completion.Result)
		if err != nil {
			return err
		}
		if completion.History != nil {
			if err := insertProjectionAuditCommandHistoryTx(ctx, tx, persisted, *completion.History); err != nil {
				return err
			}
		}
		var observedAt any
		if !persisted.ObservedAt.IsZero() {
			observedAt = timeToUnixNano(persisted.ObservedAt)
		}
		now := time.Now().UTC()
		updated, err := tx.ExecContext(ctx, `
UPDATE context_graph_projection_audit_commands
SET state = ?, result_status = ?, result_reason_code = ?, result_observed_at = ?, failure_code = '', updated_at = ?
WHERE tenant_id = ? AND idempotency_key = ? AND audit_id = ? AND operation = ? AND request_hash = ? AND state = ?`,
			ProjectionAuditCommandSucceeded, persisted.Status, persisted.ReasonCode, observedAt, timeToUnixNano(now),
			completion.TenantID, completion.IdempotencyKey, completion.AuditID, completion.Operation, completion.RequestHash, ProjectionAuditCommandClaimed,
		)
		if err != nil {
			return fmt.Errorf("complete projection audit command: %w", err)
		}
		changed, err := exactlyOneRowAffected(updated)
		if err != nil {
			return err
		}
		if !changed {
			return ErrProjectionAuditCommandConflict
		}
		return deleteProjectionAuditCommandClaimTx(ctx, tx, completion.TenantID, completion.AuditID, completion.IdempotencyKey, completion.LeaseToken)
	})
}

func sameOptionalProjectionTime(left *time.Time, right time.Time) bool {
	if left == nil {
		return right.IsZero()
	}
	return left.Equal(right)
}

func (s *PlatformStore) FailProjectionAuditCommand(ctx context.Context, failure ProjectionAuditCommandFailure) error {
	failure = normalizeProjectionAuditCommandFailure(failure)
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if err := validateProjectionAuditCommandFailure(failure); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		command, found, err := projectionAuditCommandByKeyTx(ctx, tx, failure.TenantID, failure.IdempotencyKey)
		if err != nil {
			return err
		}
		if !found || !projectionAuditCommandMatches(command, failure.AuditID, failure.Operation, failure.RequestHash) {
			return ErrProjectionAuditCommandConflict
		}
		if command.State == ProjectionAuditCommandFailed {
			if command.FailureCode == failure.FailureCode {
				return nil
			}
			return ErrProjectionAuditCommandConflict
		}
		if command.State != ProjectionAuditCommandClaimed {
			return ErrProjectionAuditCommandConflict
		}
		if err := verifyProjectionAuditCommandLeaseTx(ctx, tx, command, failure.LeaseToken); err != nil {
			return ErrProjectionAuditCommandConflict
		}
		if failure.Fence != nil {
			if err := fenceProjectionAuditCommandSecurityTx(ctx, tx, *failure.Fence); err != nil {
				return errors.Join(ErrProjectionAuditCommandConflict, ErrProjectionAuditCommandFence, err)
			}
		}
		if failure.Result != nil {
			persisted, err := updateProjectionAuditCommandResultTx(ctx, tx, command, *failure.Result)
			if err != nil {
				return err
			}
			if failure.History != nil {
				if err := insertProjectionAuditCommandHistoryTx(ctx, tx, persisted, *failure.History); err != nil {
					return err
				}
			}
		} else if failure.History != nil {
			return ErrProjectionAuditCommandConflict
		}
		updated, err := tx.ExecContext(ctx, `
UPDATE context_graph_projection_audit_commands
SET state = ?, failure_code = ?, updated_at = ?
WHERE tenant_id = ? AND idempotency_key = ? AND audit_id = ? AND operation = ? AND request_hash = ? AND state = ?`,
			ProjectionAuditCommandFailed, failure.FailureCode, timeToUnixNano(time.Now().UTC()),
			failure.TenantID, failure.IdempotencyKey, failure.AuditID, failure.Operation, failure.RequestHash, ProjectionAuditCommandClaimed,
		)
		if err != nil {
			return fmt.Errorf("fail projection audit command: %w", err)
		}
		changed, err := exactlyOneRowAffected(updated)
		if err != nil {
			return err
		}
		if !changed {
			return ErrProjectionAuditCommandConflict
		}
		return deleteProjectionAuditCommandClaimTx(ctx, tx, failure.TenantID, failure.AuditID, failure.IdempotencyKey, failure.LeaseToken)
	})
}

func verifyProjectionAuditCommandLeaseTx(ctx context.Context, tx db.Tx, command projectionAuditCommandRecord, leaseToken string) error {
	leaseToken = strings.TrimSpace(leaseToken)
	if leaseToken == "" || len(leaseToken) != 64 {
		return ErrProjectionAuditCommandConflict
	}
	lease, found, err := projectionAuditCommandLeaseTx(ctx, tx, command.TenantID, command.AuditID)
	if err != nil {
		return err
	}
	if !found || lease.IdempotencyKey != command.IdempotencyKey || lease.ExpectedRevision != command.ExpectedRevision || lease.LeaseToken != leaseToken {
		return ErrProjectionAuditCommandConflict
	}
	if !lease.LeaseExpiresAt.After(time.Now().UTC()) {
		return ErrProjectionAuditCommandConflict
	}
	return nil
}

func fenceProjectionAuditCommandSecurityTx(ctx context.Context, tx db.Tx, fence ProjectionAuditCommandFence) error {
	authorizationRevision, err := parseProjectionAuditCommandRevision(fence.AuthorizationRevision, "authorization:")
	if err != nil {
		return err
	}
	policyRevision, err := parseProjectionAuditCommandRevision(fence.PolicyRevision, "policy:")
	if err != nil {
		return err
	}
	for _, expected := range []struct {
		table    string
		revision int64
	}{{"rebac_authorization_revision", authorizationRevision}, {"data_policy_revision", policyRevision}} {
		result, err := tx.ExecContext(ctx, `UPDATE `+expected.table+` SET revision = revision WHERE id = 'global' AND revision = ?`, expected.revision)
		if err != nil {
			return err
		}
		matched, err := exactlyOneRowAffected(result)
		if err != nil {
			return err
		}
		if !matched {
			return ErrProjectionAuditCommandFence
		}
	}
	if fence.PersistedBindingVersion == 0 {
		if fence.PersistenceRequired {
			return ErrProjectionAuditCommandFence
		}
		return nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE ontology_scope_bindings
SET binding_version = binding_version
WHERE tenant_id = ? AND binding_version = ? AND state = 'active'`, fence.TenantID, fence.PersistedBindingVersion)
	if err != nil {
		return err
	}
	matched, err := exactlyOneRowAffected(result)
	if err != nil {
		return err
	}
	if !matched {
		return ErrProjectionAuditCommandFence
	}
	return nil
}

func parseProjectionAuditCommandRevision(value, prefix string) (int64, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, prefix) {
		return 0, ErrProjectionAuditCommandFence
	}
	revision, err := strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
	if err != nil || revision < 0 {
		return 0, ErrProjectionAuditCommandFence
	}
	return revision, nil
}

func updateProjectionAuditCommandResultTx(ctx context.Context, tx db.Tx, command projectionAuditCommandRecord, result ProjectionAudit) (ProjectionAudit, error) {
	current, err := projectionAuditByIDTx(ctx, tx, command.TenantID, command.AuditID)
	if err != nil {
		return ProjectionAudit{}, err
	}
	result.SourceAccessRef = NormalizeAccessRef(result.SourceAccessRef)
	if current.Revision != command.ExpectedRevision || current.Status != "skipped" ||
		current.TenantID != result.TenantID || current.ID != result.ID || current.SourceEventID != result.SourceEventID ||
		current.SourceEventType != result.SourceEventType || current.SourceAccessRef != result.SourceAccessRef ||
		current.AccessState != AccessStateResolved || result.AccessState != AccessStateResolved || result.ObservedAt.IsZero() {
		return ProjectionAudit{}, ErrProjectionAuditCommandConflict
	}
	updated, err := tx.ExecContext(ctx, `
UPDATE context_graph_projection_audits
SET status = ?, reason_code = ?, reason = ?, details_json = ?, observed_at = ?, revision = revision + 1
WHERE tenant_id = ? AND id = ? AND revision = ? AND status = 'skipped' AND access_state = 'resolved'
	AND source_access_kind = ? AND source_access_id = ? AND source_access_tenant_id = ?`,
		result.Status, result.ReasonCode, result.Reason, mustGraphJSON(defaultGraphMap(result.Details)), timeToUnixNano(result.ObservedAt),
		command.TenantID, command.AuditID, command.ExpectedRevision,
		result.SourceAccessRef.Kind, result.SourceAccessRef.ID, result.SourceAccessRef.TenantID,
	)
	if err != nil {
		return ProjectionAudit{}, fmt.Errorf("compare-and-swap projection audit command result: %w", err)
	}
	changed, err := exactlyOneRowAffected(updated)
	if err != nil {
		return ProjectionAudit{}, err
	}
	if !changed {
		return ProjectionAudit{}, ErrProjectionAuditCommandConflict
	}
	result.Revision = command.ExpectedRevision + 1
	return result, nil
}

func insertProjectionAuditCommandHistoryTx(ctx context.Context, tx db.Tx, result ProjectionAudit, history ProjectionAuditReplayHistory) error {
	history.SourceAccessRef = NormalizeAccessRef(history.SourceAccessRef)
	if strings.TrimSpace(history.ID) == "" || history.TenantID != result.TenantID || history.AuditID != result.ID ||
		history.SourceEventID != result.SourceEventID || history.SourceEventType != result.SourceEventType ||
		history.SourceAccessRef != result.SourceAccessRef || history.AccessState != AccessStateResolved || history.AttemptedAt.IsZero() {
		return ErrProjectionAuditCommandConflict
	}
	inserted, err := tx.ExecContext(ctx, `
INSERT INTO context_graph_projection_audit_replay_history (
	id, audit_id, tenant_id, source_event_id, source_event_type, status, error, details_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, attempted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`,
		history.ID, history.AuditID, history.TenantID, history.SourceEventID, history.SourceEventType,
		history.Status, history.Error, mustGraphJSON(defaultGraphMap(history.Details)),
		history.SourceAccessRef.Kind, history.SourceAccessRef.ID, history.SourceAccessRef.TenantID,
		history.AccessState, timeToUnixNano(history.AttemptedAt),
	)
	if err != nil {
		return fmt.Errorf("insert projection audit command history: %w", err)
	}
	created, err := exactlyOneRowAffected(inserted)
	if err != nil {
		return err
	}
	if !created {
		return ErrProjectionAuditCommandConflict
	}
	return nil
}

func projectionAuditCommandByKeyTx(ctx context.Context, tx db.Tx, tenantID, idempotencyKey string) (projectionAuditCommandRecord, bool, error) {
	var record projectionAuditCommandRecord
	var observedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `
SELECT tenant_id, idempotency_key, audit_id, operation, request_hash, expected_audit_revision,
	state, result_status, result_reason_code, result_observed_at, failure_code
FROM context_graph_projection_audit_commands
WHERE tenant_id = ? AND idempotency_key = ?`, tenantID, idempotencyKey).Scan(
		&record.TenantID, &record.IdempotencyKey, &record.AuditID, &record.Operation, &record.RequestHash, &record.ExpectedRevision,
		&record.State, &record.ResultStatus, &record.ReasonCode, &observedAt, &record.FailureCode,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return projectionAuditCommandRecord{}, false, nil
	}
	if err != nil {
		return projectionAuditCommandRecord{}, false, fmt.Errorf("read projection audit command: %w", err)
	}
	if observedAt.Valid {
		at := unixNanoToTime(observedAt.Int64)
		record.ObservedAt = &at
	}
	return record, true, nil
}

func projectionAuditByIDTx(ctx context.Context, tx db.Tx, tenantID, auditID string) (ProjectionAudit, error) {
	var audit ProjectionAudit
	var detailsJSON string
	var observedAt int64
	err := tx.QueryRowContext(ctx, `
SELECT id, tenant_id, source_event_id, source_event_type, status, reason_code, reason, details_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at, revision
FROM context_graph_projection_audits
WHERE tenant_id = ? AND id = ?`, tenantID, auditID).Scan(
		&audit.ID, &audit.TenantID, &audit.SourceEventID, &audit.SourceEventType, &audit.Status,
		&audit.ReasonCode, &audit.Reason, &detailsJSON, &audit.SourceAccessRef.Kind, &audit.SourceAccessRef.ID,
		&audit.SourceAccessRef.TenantID, &audit.AccessState, &observedAt, &audit.Revision,
	)
	if err != nil {
		return ProjectionAudit{}, err
	}
	decodeGraphJSON(detailsJSON, &audit.Details)
	audit.ObservedAt = unixNanoToTime(observedAt)
	return audit, nil
}

func deleteProjectionAuditCommandClaimTx(ctx context.Context, tx db.Tx, tenantID, auditID, idempotencyKey, leaseToken string) error {
	deleted, err := tx.ExecContext(ctx, `
DELETE FROM context_graph_projection_audit_command_claims
WHERE tenant_id = ? AND audit_id = ? AND idempotency_key = ? AND lease_token = ?`, tenantID, auditID, idempotencyKey, leaseToken)
	if err != nil {
		return fmt.Errorf("release projection audit command claim: %w", err)
	}
	changed, err := exactlyOneRowAffected(deleted)
	if err != nil {
		return err
	}
	if !changed {
		return ErrProjectionAuditCommandConflict
	}
	return nil
}

func exactlyOneRowAffected(result sql.Result) (bool, error) {
	if result == nil {
		return false, nil
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func normalizeProjectionAuditCommandClaim(request ProjectionAuditCommandClaimRequest) ProjectionAuditCommandClaimRequest {
	request.Audit.TenantID = strings.TrimSpace(request.Audit.TenantID)
	request.Audit.ID = strings.TrimSpace(request.Audit.ID)
	request.Operation = strings.ToLower(strings.TrimSpace(request.Operation))
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.RequestHash = strings.TrimSpace(request.RequestHash)
	return request
}

func validateProjectionAuditCommandClaim(request ProjectionAuditCommandClaimRequest) error {
	if request.Audit.TenantID == "" || request.Audit.ID == "" || request.Audit.Revision <= 0 ||
		request.Audit.AccessState != AccessStateResolved || !request.Audit.SourceAccessRef.Resolved() || !validProjectionAuditCommandIdentity(request.Operation, request.IdempotencyKey, request.RequestHash) {
		return fmt.Errorf("projection audit command claim is invalid")
	}
	return nil
}

func normalizeProjectionAuditCommandCompletion(completion ProjectionAuditCommandCompletion) ProjectionAuditCommandCompletion {
	completion.TenantID = strings.TrimSpace(completion.TenantID)
	completion.AuditID = strings.TrimSpace(completion.AuditID)
	completion.Operation = strings.ToLower(strings.TrimSpace(completion.Operation))
	completion.IdempotencyKey = strings.TrimSpace(completion.IdempotencyKey)
	completion.RequestHash = strings.TrimSpace(completion.RequestHash)
	completion.LeaseToken = strings.TrimSpace(completion.LeaseToken)
	completion.Fence.TenantID = strings.TrimSpace(completion.Fence.TenantID)
	completion.Fence.AuthorizationRevision = strings.TrimSpace(completion.Fence.AuthorizationRevision)
	completion.Fence.PolicyRevision = strings.TrimSpace(completion.Fence.PolicyRevision)
	completion.Result.SourceAccessRef = NormalizeAccessRef(completion.Result.SourceAccessRef)
	if completion.History != nil {
		completion.History.SourceAccessRef = NormalizeAccessRef(completion.History.SourceAccessRef)
	}
	return completion
}

func validateProjectionAuditCommandCompletion(completion ProjectionAuditCommandCompletion) error {
	if completion.TenantID == "" || completion.AuditID == "" || completion.Result.TenantID != completion.TenantID || completion.Result.ID != completion.AuditID ||
		strings.TrimSpace(completion.Result.Status) == "" || completion.Result.AccessState != AccessStateResolved || !completion.Result.SourceAccessRef.Resolved() ||
		!validProjectionAuditCommandIdentity(completion.Operation, completion.IdempotencyKey, completion.RequestHash) || len(completion.LeaseToken) != 64 ||
		!validProjectionAuditCommandFence(completion.TenantID, completion.Fence) {
		return fmt.Errorf("projection audit command completion is invalid")
	}
	switch completion.Operation {
	case ProjectionAuditCommandReplay:
		if completion.Result.Status != "replayed" || completion.History == nil || completion.History.Status != "replayed" || strings.TrimSpace(completion.History.Error) != "" || completion.Effect == nil {
			return fmt.Errorf("projection audit replay command completion is invalid")
		}
	case ProjectionAuditCommandAcknowledge:
		if completion.Result.Status != "acknowledged" || completion.History != nil || completion.Effect != nil {
			return fmt.Errorf("projection audit acknowledge command completion is invalid")
		}
	}
	return nil
}

func validProjectionAuditCommandFence(tenantID string, fence ProjectionAuditCommandFence) bool {
	if fence.TenantID != tenantID || fence.BindingVersion <= 0 || fence.PersistedBindingVersion < 0 || fence.PersistedBindingVersion > fence.BindingVersion ||
		fence.PersistenceRequired && fence.PersistedBindingVersion == 0 {
		return false
	}
	_, authorizationErr := parseProjectionAuditCommandRevision(fence.AuthorizationRevision, "authorization:")
	_, policyErr := parseProjectionAuditCommandRevision(fence.PolicyRevision, "policy:")
	return authorizationErr == nil && policyErr == nil
}

func normalizeProjectionAuditCommandFailure(failure ProjectionAuditCommandFailure) ProjectionAuditCommandFailure {
	failure.TenantID = strings.TrimSpace(failure.TenantID)
	failure.AuditID = strings.TrimSpace(failure.AuditID)
	failure.Operation = strings.ToLower(strings.TrimSpace(failure.Operation))
	failure.IdempotencyKey = strings.TrimSpace(failure.IdempotencyKey)
	failure.RequestHash = strings.TrimSpace(failure.RequestHash)
	failure.LeaseToken = strings.TrimSpace(failure.LeaseToken)
	failure.FailureCode = strings.ToLower(strings.TrimSpace(failure.FailureCode))
	if failure.Fence != nil {
		failure.Fence.TenantID = strings.TrimSpace(failure.Fence.TenantID)
		failure.Fence.AuthorizationRevision = strings.TrimSpace(failure.Fence.AuthorizationRevision)
		failure.Fence.PolicyRevision = strings.TrimSpace(failure.Fence.PolicyRevision)
	}
	if failure.Result != nil {
		failure.Result.SourceAccessRef = NormalizeAccessRef(failure.Result.SourceAccessRef)
	}
	if failure.History != nil {
		failure.History.SourceAccessRef = NormalizeAccessRef(failure.History.SourceAccessRef)
	}
	return failure
}

func validateProjectionAuditCommandFailure(failure ProjectionAuditCommandFailure) error {
	if failure.TenantID == "" || failure.AuditID == "" || failure.FailureCode == "" || len(failure.FailureCode) > 64 ||
		!validProjectionAuditCommandIdentity(failure.Operation, failure.IdempotencyKey, failure.RequestHash) || len(failure.LeaseToken) != 64 {
		return fmt.Errorf("projection audit command failure is invalid")
	}
	if failure.Fence != nil && !validProjectionAuditCommandFence(failure.TenantID, *failure.Fence) {
		return fmt.Errorf("projection audit command failure fence is invalid")
	}
	if (failure.Result != nil || failure.History != nil) && failure.Fence == nil {
		return fmt.Errorf("projection audit command failure fence is invalid")
	}
	switch failure.Operation {
	case ProjectionAuditCommandReplay:
		if failure.Result == nil && failure.History == nil {
			break
		}
		if failure.Result == nil || failure.History == nil || failure.Result.Status != "skipped" || failure.History.Status != "failed" || failure.History.Error != failure.FailureCode {
			return fmt.Errorf("projection audit replay command failure is invalid")
		}
	case ProjectionAuditCommandAcknowledge:
		if failure.Result != nil || failure.History != nil {
			return fmt.Errorf("projection audit acknowledge command failure is invalid")
		}
	}
	return nil
}

func validProjectionAuditCommandIdentity(operation, idempotencyKey, requestHash string) bool {
	if operation != ProjectionAuditCommandReplay && operation != ProjectionAuditCommandAcknowledge {
		return false
	}
	return idempotencyKey != "" && len(idempotencyKey) <= 256 && requestHash != "" && len(requestHash) == 64
}

func projectionAuditCommandMatches(record projectionAuditCommandRecord, auditID, operation, requestHash string) bool {
	return record.AuditID == auditID && record.Operation == operation && record.RequestHash == requestHash
}

func projectionAuditCommandClaimFromRecord(record projectionAuditCommandRecord) ProjectionAuditCommandClaim {
	return ProjectionAuditCommandClaim{
		State: record.State, ResultStatus: record.ResultStatus, ReasonCode: record.ReasonCode,
		ObservedAt: record.ObservedAt, FailureCode: record.FailureCode,
	}
}
