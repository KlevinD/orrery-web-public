package hil

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"orrery_v2/internal/platform/db"
)

type legacyRuntimeApprovalAssigneeRepairInput struct {
	TenantID               string
	DecisionID             string
	ExpectedFingerprint    string
	ReplacementFingerprint string
	Assignee               string
}

type legacyRuntimeApprovalAssigneeRepairer interface {
	repairLegacyRuntimeApprovalAssignee(context.Context, legacyRuntimeApprovalAssigneeRepairInput) (DecisionRequest, error)
}

func (r *PlatformRepository) repairLegacyRuntimeApprovalAssignee(ctx context.Context, input legacyRuntimeApprovalAssigneeRepairInput) (DecisionRequest, error) {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.DecisionID = strings.TrimSpace(input.DecisionID)
	input.ExpectedFingerprint = strings.TrimSpace(input.ExpectedFingerprint)
	input.ReplacementFingerprint = strings.TrimSpace(input.ReplacementFingerprint)
	input.Assignee = strings.TrimSpace(input.Assignee)
	if r == nil || r.store == nil || input.TenantID == "" || input.DecisionID == "" || input.ExpectedFingerprint == "" || input.ReplacementFingerprint == "" || input.Assignee == "" {
		return DecisionRequest{}, ErrInvalidRequest
	}

	var repaired DecisionRequest
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		current, err := loadDecisionForLegacyAssigneeRepair(ctx, tx, input.TenantID, input.DecisionID)
		if err != nil {
			return err
		}
		compatible, terminal, err := legacyAssigneeRepairState(ctx, tx, current, input)
		if err != nil {
			return err
		}
		if compatible || terminal {
			repaired = current
			return nil
		}

		payload := cloneMap(current.PayloadPreview)
		if payload == nil {
			payload = map[string]any{}
		}
		payload[approvalAssigneePayloadKey] = input.Assignee
		existingPayloadJSON := mustJSON(current.PayloadPreview)
		payloadPredicate := "payload_preview_json = ?"
		if r.store.Dialect() == db.DialectPostgres {
			payloadPredicate = "payload_preview_json = CAST(? AS JSONB)"
		}
		result, err := tx.ExecContext(ctx, `
UPDATE hil_decision_requests
SET payload_preview_json = ?, request_fingerprint = ?
WHERE tenant_id = ? AND id = ? AND status = ? AND route_mode = ?
	AND action = ? AND resource_kind = ? AND request_fingerprint = ?
	AND `+payloadPredicate+`
	AND NOT EXISTS (
		SELECT 1 FROM hil_workflow_instances AS owned_instance
		WHERE owned_instance.tenant_id = hil_decision_requests.tenant_id
		  AND owned_instance.decision_request_id = hil_decision_requests.id
	)`, mustJSON(payload), input.ReplacementFingerprint, input.TenantID, input.DecisionID,
			DecisionPending, ModeHIL, "runtime.approval", "runtime_approval", input.ExpectedFingerprint, existingPayloadJSON)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 1 {
			current.PayloadPreview = payload
			current.RequestFingerprint = input.ReplacementFingerprint
			repaired = current
			return nil
		}

		latest, err := loadDecisionForLegacyAssigneeRepair(ctx, tx, input.TenantID, input.DecisionID)
		if err != nil {
			return err
		}
		compatible, terminal, err = legacyAssigneeRepairState(ctx, tx, latest, input)
		if err != nil {
			return err
		}
		if compatible || terminal {
			repaired = latest
			return nil
		}
		return ErrConflict
	})
	return repaired, err
}

func loadDecisionForLegacyAssigneeRepair(ctx context.Context, tx db.Tx, tenantID, decisionID string) (DecisionRequest, error) {
	decision, err := scanDecision(tx.QueryRowContext(ctx, decisionSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, decisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return DecisionRequest{}, ErrNotFound
	}
	return decision, err
}

// legacyAssigneeRepairState returns whether the requested repair is already
// present or the legacy decision is terminal and can be reused without repair.
func legacyAssigneeRepairState(ctx context.Context, tx db.Tx, decision DecisionRequest, input legacyRuntimeApprovalAssigneeRepairInput) (bool, bool, error) {
	if strings.TrimSpace(decision.Action) != "runtime.approval" || normalizeObjectRef(decision.Resource).Kind != "runtime_approval" {
		return false, false, ErrConflict
	}
	var workflowCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM hil_workflow_instances WHERE tenant_id = ? AND decision_request_id = ?`, decision.TenantID, decision.ID).Scan(&workflowCount); err != nil {
		return false, false, err
	}
	if workflowCount != 0 {
		return false, false, ErrConflict
	}
	if decisionHasApprovalAssignee(decision.PayloadPreview) {
		assigned, valid := canonicalApprovalAssignee(decision.PayloadPreview)
		if valid && assigned == input.Assignee && decision.RequestFingerprint == input.ReplacementFingerprint {
			return true, false, nil
		}
		return false, false, ErrConflict
	}
	if decision.RequestFingerprint != input.ExpectedFingerprint {
		return false, false, ErrConflict
	}
	switch decision.Status {
	case DecisionPending:
		if decision.Route.Mode != ModeHIL {
			return false, false, ErrConflict
		}
		return false, false, nil
	case DecisionResolved, DecisionDenied:
		return false, true, nil
	default:
		return false, false, ErrConflict
	}
}
