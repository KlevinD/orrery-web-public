package actioncontract

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformRepository struct {
	store repositoryDatabase
}

func NewPlatformRepository(store *db.Store) *PlatformRepository {
	if store == nil {
		return &PlatformRepository{}
	}
	return &PlatformRepository{store: store}
}

func (r *PlatformRepository) SaveActionType(ctx context.Context, actionType ActionType) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	result, err := r.store.Exec(ctx, `
INSERT INTO action_types (
	id, tenant_id, name, category, description, object_type_refs_json, provider_kind,
	provider_ref, input_schema_json, output_schema_json, resource_resolver, permission_scope,
	field_profile, data_policy, purpose_policy, risk_level, hil_policy_json,
	dry_run_supported, writeback_target_json, idempotency_policy_json, audit_policy_json,
	failure_policy_json, semantics_json, enabled, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	name = excluded.name,
	category = excluded.category,
	description = excluded.description,
	object_type_refs_json = excluded.object_type_refs_json,
	provider_kind = excluded.provider_kind,
	provider_ref = excluded.provider_ref,
	input_schema_json = excluded.input_schema_json,
	output_schema_json = excluded.output_schema_json,
	resource_resolver = excluded.resource_resolver,
	permission_scope = excluded.permission_scope,
	field_profile = excluded.field_profile,
	data_policy = excluded.data_policy,
	purpose_policy = excluded.purpose_policy,
	risk_level = excluded.risk_level,
	hil_policy_json = excluded.hil_policy_json,
	dry_run_supported = excluded.dry_run_supported,
	writeback_target_json = excluded.writeback_target_json,
	idempotency_policy_json = excluded.idempotency_policy_json,
	audit_policy_json = excluded.audit_policy_json,
	failure_policy_json = excluded.failure_policy_json,
	semantics_json = excluded.semantics_json,
	enabled = excluded.enabled,
	version = excluded.version,
	updated_at = excluded.updated_at
WHERE action_types.tenant_id = excluded.tenant_id`,
		actionType.ID,
		actionType.TenantID,
		actionType.Name,
		actionType.Category,
		actionType.Description,
		mustJSONString(actionType.ObjectTypeRefs),
		actionType.ProviderKind,
		actionType.ProviderRef,
		mustJSONString(defaultAnyMap(actionType.InputSchema)),
		mustJSONString(defaultAnyMap(actionType.OutputSchema)),
		actionType.ResourceResolver,
		actionType.PermissionScope,
		actionType.FieldProfile,
		actionType.DataPolicy,
		actionType.PurposePolicy,
		actionType.RiskLevel,
		mustJSONString(defaultAnyMap(actionType.HILPolicy)),
		actionType.DryRunSupported,
		mustJSONString(defaultAnyMap(actionType.WritebackTarget)),
		mustJSONString(defaultAnyMap(actionType.IdempotencyPolicy)),
		mustJSONString(defaultAnyMap(actionType.AuditPolicy)),
		mustJSONString(defaultAnyMap(actionType.FailurePolicy)),
		mustJSONString(actionType.Semantics),
		actionType.Enabled,
		actionType.Version,
		timeToUnixNano(actionType.CreatedAt),
		timeToUnixNano(actionType.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save action type: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("save action type rows affected: %w", err)
	} else if affected == 0 {
		return fmt.Errorf("%w: action type id is owned by another tenant", ErrInvalidRequest)
	}
	return nil
}

func (r *PlatformRepository) GetActionType(ctx context.Context, id ActionTypeID) (ActionType, error) {
	if r == nil || r.store == nil {
		return ActionType{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	row := r.store.QueryRow(ctx, actionTypeSelectSQL()+` WHERE id = ?`, id)
	actionType, err := scanActionType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionType{}, fmt.Errorf("%w: action type %s", ErrNotFound, id)
	}
	return actionType, err
}

func (r *PlatformRepository) ListActionTypes(ctx context.Context) ([]ActionType, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	rows, err := r.store.Query(ctx, actionTypeSelectSQL()+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list action types: %w", err)
	}
	defer rows.Close()
	var out []ActionType
	for rows.Next() {
		actionType, err := scanActionType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, actionType)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list action types: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *PlatformRepository) SaveActionRun(ctx context.Context, run ActionRun) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	if run.ExecutionMode == "" {
		run.ExecutionMode = ExecutionModeApply
	}
	result, err := r.store.Exec(ctx, `
INSERT INTO action_runs (
	id, tenant_id, workspace_id, action_type_id, action_category, actor_kind, actor_id, actor_name,
	effective_user_kind, effective_user_id, effective_user_name, delegation_json, runtime_kind,
	runtime_id, session_json, human_intent_json, approval_json, purpose, object_refs_json,
	requested_fields_json, args_hash, risk_level, execution_mode, policy_decision_id, hil_token_id, status,
	provider_call_ref_json, result_ref_json, evidence_refs_json, audit_ref_json, error_code,
	error_message, created_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	status = excluded.status,
	provider_call_ref_json = excluded.provider_call_ref_json,
	result_ref_json = excluded.result_ref_json,
	evidence_refs_json = excluded.evidence_refs_json,
	error_code = excluded.error_code,
	error_message = excluded.error_message,
	completed_at = excluded.completed_at
WHERE action_runs.tenant_id = excluded.tenant_id`,
		run.ID,
		run.TenantID,
		run.WorkspaceID,
		run.ActionTypeID,
		run.ActionCategory,
		run.Actor.Kind,
		run.Actor.ID,
		run.Actor.Name,
		run.EffectiveUser.Kind,
		run.EffectiveUser.ID,
		run.EffectiveUser.Name,
		mustJSONString(run.Delegation),
		run.Runtime.Kind,
		run.Runtime.ID,
		mustJSONString(run.Session),
		mustJSONString(run.HumanIntent),
		mustJSONString(run.Approval),
		run.Purpose,
		mustJSONString(run.ObjectRefs),
		mustJSONString(run.RequestedFields),
		run.ArgsHash,
		run.RiskLevel,
		run.ExecutionMode,
		run.PolicyDecisionID,
		run.HILTokenID,
		run.Status,
		mustJSONString(run.ProviderCallRef),
		mustJSONString(run.ResultRef),
		mustJSONString(run.EvidenceRefs),
		mustJSONString(run.AuditRef),
		run.ErrorCode,
		run.ErrorMessage,
		timeToUnixNano(run.CreatedAt),
		optionalTimeToUnixNano(run.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("save action run: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("save action run rows affected: %w", err)
	} else if affected == 0 {
		return fmt.Errorf("%w: action run id is owned by another tenant", ErrInvalidRequest)
	}
	return nil
}

func (r *PlatformRepository) GetActionRun(ctx context.Context, id ActionRunID) (ActionRun, error) {
	if r == nil || r.store == nil {
		return ActionRun{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	row := r.store.QueryRow(ctx, actionRunSelectSQL()+` WHERE id = ?`, id)
	run, err := scanActionRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionRun{}, fmt.Errorf("%w: action run %s", ErrNotFound, id)
	}
	return run, err
}

func (r *PlatformRepository) GetActionRuns(ctx context.Context, ids []ActionRunID) (map[ActionRunID]ActionRun, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	unique := uniqueActionRunIDs(ids)
	out := make(map[ActionRunID]ActionRun, len(unique))
	const batchSize = 500
	for start := 0; start < len(unique); start += batchSize {
		end := min(start+batchSize, len(unique))
		placeholders := make([]string, end-start)
		args := make([]any, end-start)
		for index, id := range unique[start:end] {
			placeholders[index] = "?"
			args[index] = id
		}
		rows, err := r.store.Query(ctx, actionRunSelectSQL()+` WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("get action runs: %w", err)
		}
		for rows.Next() {
			run, scanErr := scanActionRun(rows)
			if scanErr != nil {
				rows.Close()
				return nil, fmt.Errorf("scan action run batch: %w", scanErr)
			}
			out[run.ID] = run
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate action run batch: %w", rowsErr)
		}
		rows.Close()
	}
	return out, nil
}

// GetActionRunTenants reads only the ownership columns required by ontology
// visibility checks instead of decoding complete action-run payloads.
func (r *PlatformRepository) GetActionRunTenants(ctx context.Context, ids []ActionRunID) (map[ActionRunID]string, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	unique := uniqueActionRunIDs(ids)
	out := make(map[ActionRunID]string, len(unique))
	const batchSize = 500
	for start := 0; start < len(unique); start += batchSize {
		end := min(start+batchSize, len(unique))
		placeholders := make([]string, end-start)
		args := make([]any, end-start)
		for index, id := range unique[start:end] {
			placeholders[index] = "?"
			args[index] = id
		}
		rows, err := r.store.Query(ctx, `SELECT id, tenant_id FROM action_runs WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("get action run tenants: %w", err)
		}
		for rows.Next() {
			var id ActionRunID
			var tenantID string
			if scanErr := rows.Scan(&id, &tenantID); scanErr != nil {
				rows.Close()
				return nil, fmt.Errorf("scan action run tenant batch: %w", scanErr)
			}
			out[id] = strings.TrimSpace(tenantID)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate action run tenant batch: %w", rowsErr)
		}
		rows.Close()
	}
	return out, nil
}

func uniqueActionRunIDs(ids []ActionRunID) []ActionRunID {
	unique := make([]ActionRunID, 0, len(ids))
	seen := make(map[ActionRunID]struct{}, len(ids))
	for _, id := range ids {
		id = ActionRunID(strings.TrimSpace(string(id)))
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}

func (r *PlatformRepository) ListActionRuns(ctx context.Context, query ActionRunQuery) (ActionRunPage, error) {
	if r == nil || r.store == nil {
		return ActionRunPage{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	where, args := actionRunWhereSQL(query)
	var total int
	countSQL := `SELECT COUNT(*) FROM action_runs` + where
	if err := r.store.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return ActionRunPage{}, fmt.Errorf("count action runs: %w", err)
	}
	limit, offset := normalizePage(query.Limit, query.Offset)
	listArgs := append(append([]any(nil), args...), limit, offset)
	rows, err := r.store.Query(ctx, actionRunSelectSQL()+where+` ORDER BY created_at DESC, id LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return ActionRunPage{}, fmt.Errorf("list action runs: %w", err)
	}
	defer rows.Close()
	var runs []ActionRun
	for rows.Next() {
		run, err := scanActionRun(rows)
		if err != nil {
			return ActionRunPage{}, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return ActionRunPage{}, fmt.Errorf("list action runs: %w", err)
	}
	return ActionRunPage{
		Runs: runs,
		Page: PageDTO{Limit: limit, Offset: offset, Total: total, Returned: len(runs)},
	}, nil
}

func (r *PlatformRepository) SaveWritebackReceipt(ctx context.Context, receipt WritebackReceipt) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	_, err := r.store.Exec(ctx, `
INSERT INTO action_writeback_receipts (
	id, tenant_id, action_run_id, target_system, target_resource_refs_json, before_hash,
	after_hash, changed_fields_json, external_operation_id, idempotency_key, status,
	evidence_refs_json, audit_ref_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receipt.ID,
		receipt.TenantID,
		receipt.ActionRunID,
		receipt.TargetSystem,
		mustJSONString(receipt.TargetResourceRefs),
		receipt.BeforeHash,
		receipt.AfterHash,
		mustJSONString(receipt.ChangedFields),
		receipt.ExternalOperationID,
		receipt.IdempotencyKey,
		receipt.Status,
		mustJSONString(receipt.EvidenceRefs),
		mustJSONString(receipt.AuditRef),
		timeToUnixNano(receipt.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("save writeback receipt: %w", err)
	}
	return nil
}

func actionRunWhereSQL(query ActionRunQuery) (string, []any) {
	var clauses []string
	var args []any
	if value := strings.TrimSpace(query.TenantID); value != "" {
		clauses = append(clauses, "tenant_id = ?")
		args = append(args, value)
	}
	if query.ActionTypeID != "" {
		clauses = append(clauses, "action_type_id = ?")
		args = append(args, query.ActionTypeID)
	}
	if query.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, query.Status)
	}
	if value := strings.TrimSpace(query.ActorID); value != "" {
		clauses = append(clauses, "actor_id = ?")
		args = append(args, value)
	}
	if query.RuntimeKind != "" {
		clauses = append(clauses, "runtime_kind = ?")
		args = append(args, query.RuntimeKind)
	}
	if value := strings.TrimSpace(query.RuntimeID); value != "" {
		clauses = append(clauses, "runtime_id = ?")
		args = append(args, value)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (r *PlatformRepository) ListWritebackReceipts(ctx context.Context, runID ActionRunID) ([]WritebackReceipt, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	rows, err := r.store.Query(ctx, `
SELECT id, tenant_id, action_run_id, target_system, target_resource_refs_json, before_hash,
	after_hash, changed_fields_json, external_operation_id, idempotency_key, status,
	evidence_refs_json, audit_ref_json, created_at
FROM action_writeback_receipts
WHERE action_run_id = ?
ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list writeback receipts: %w", err)
	}
	defer rows.Close()
	var out []WritebackReceipt
	for rows.Next() {
		var receipt WritebackReceipt
		var targetRefsJSON, changedFieldsJSON, evidenceRefsJSON, auditRefJSON string
		var createdAt int64
		if err := rows.Scan(
			&receipt.ID,
			&receipt.TenantID,
			&receipt.ActionRunID,
			&receipt.TargetSystem,
			&targetRefsJSON,
			&receipt.BeforeHash,
			&receipt.AfterHash,
			&changedFieldsJSON,
			&receipt.ExternalOperationID,
			&receipt.IdempotencyKey,
			&receipt.Status,
			&evidenceRefsJSON,
			&auditRefJSON,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan writeback receipt: %w", err)
		}
		mustDecodeJSON(targetRefsJSON, &receipt.TargetResourceRefs)
		mustDecodeJSON(changedFieldsJSON, &receipt.ChangedFields)
		mustDecodeJSON(evidenceRefsJSON, &receipt.EvidenceRefs)
		mustDecodeJSON(auditRefJSON, &receipt.AuditRef)
		receipt.CreatedAt = unixNanoToTime(createdAt)
		out = append(out, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list writeback receipts: %w", err)
	}
	return out, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func actionTypeSelectSQL() string {
	return `
SELECT id, tenant_id, name, category, description, object_type_refs_json, provider_kind,
	provider_ref, input_schema_json, output_schema_json, resource_resolver, permission_scope,
	field_profile, data_policy, purpose_policy, risk_level, hil_policy_json,
	dry_run_supported, writeback_target_json, idempotency_policy_json, audit_policy_json,
	failure_policy_json, semantics_json, enabled, version, created_at, updated_at
FROM action_types`
}

func scanActionType(row rowScanner) (ActionType, error) {
	var actionType ActionType
	var objectRefsJSON, inputSchemaJSON, outputSchemaJSON, hilPolicyJSON string
	var writebackTargetJSON, idempotencyPolicyJSON, auditPolicyJSON, failurePolicyJSON, semanticsJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(
		&actionType.ID,
		&actionType.TenantID,
		&actionType.Name,
		&actionType.Category,
		&actionType.Description,
		&objectRefsJSON,
		&actionType.ProviderKind,
		&actionType.ProviderRef,
		&inputSchemaJSON,
		&outputSchemaJSON,
		&actionType.ResourceResolver,
		&actionType.PermissionScope,
		&actionType.FieldProfile,
		&actionType.DataPolicy,
		&actionType.PurposePolicy,
		&actionType.RiskLevel,
		&hilPolicyJSON,
		&actionType.DryRunSupported,
		&writebackTargetJSON,
		&idempotencyPolicyJSON,
		&auditPolicyJSON,
		&failurePolicyJSON,
		&semanticsJSON,
		&actionType.Enabled,
		&actionType.Version,
		&createdAt,
		&updatedAt,
	); err != nil {
		return ActionType{}, err
	}
	mustDecodeJSON(objectRefsJSON, &actionType.ObjectTypeRefs)
	mustDecodeJSON(inputSchemaJSON, &actionType.InputSchema)
	mustDecodeJSON(outputSchemaJSON, &actionType.OutputSchema)
	mustDecodeJSON(hilPolicyJSON, &actionType.HILPolicy)
	mustDecodeJSON(writebackTargetJSON, &actionType.WritebackTarget)
	mustDecodeJSON(idempotencyPolicyJSON, &actionType.IdempotencyPolicy)
	mustDecodeJSON(auditPolicyJSON, &actionType.AuditPolicy)
	mustDecodeJSON(failurePolicyJSON, &actionType.FailurePolicy)
	mustDecodeJSON(semanticsJSON, &actionType.Semantics)
	actionType.CreatedAt = unixNanoToTime(createdAt)
	actionType.UpdatedAt = unixNanoToTime(updatedAt)
	return actionType, nil
}

func actionRunSelectSQL() string {
	return `
SELECT id, tenant_id, workspace_id, action_type_id, action_category, actor_kind, actor_id, actor_name,
	effective_user_kind, effective_user_id, effective_user_name, delegation_json, runtime_kind,
	runtime_id, session_json, human_intent_json, approval_json, purpose, object_refs_json,
	requested_fields_json, args_hash, risk_level, execution_mode, policy_decision_id, hil_token_id, status,
	provider_call_ref_json, result_ref_json, evidence_refs_json, audit_ref_json, error_code,
	error_message, created_at, completed_at
FROM action_runs`
}

func scanActionRun(row rowScanner) (ActionRun, error) {
	var run ActionRun
	var delegationJSON, sessionJSON, humanIntentJSON, approvalJSON string
	var objectRefsJSON, requestedFieldsJSON, providerCallRefJSON, resultRefJSON, evidenceRefsJSON, auditRefJSON string
	var createdAt int64
	var completedAt sql.NullInt64
	if err := row.Scan(
		&run.ID,
		&run.TenantID,
		&run.WorkspaceID,
		&run.ActionTypeID,
		&run.ActionCategory,
		&run.Actor.Kind,
		&run.Actor.ID,
		&run.Actor.Name,
		&run.EffectiveUser.Kind,
		&run.EffectiveUser.ID,
		&run.EffectiveUser.Name,
		&delegationJSON,
		&run.Runtime.Kind,
		&run.Runtime.ID,
		&sessionJSON,
		&humanIntentJSON,
		&approvalJSON,
		&run.Purpose,
		&objectRefsJSON,
		&requestedFieldsJSON,
		&run.ArgsHash,
		&run.RiskLevel,
		&run.ExecutionMode,
		&run.PolicyDecisionID,
		&run.HILTokenID,
		&run.Status,
		&providerCallRefJSON,
		&resultRefJSON,
		&evidenceRefsJSON,
		&auditRefJSON,
		&run.ErrorCode,
		&run.ErrorMessage,
		&createdAt,
		&completedAt,
	); err != nil {
		return ActionRun{}, err
	}
	mustDecodeJSON(delegationJSON, &run.Delegation)
	mustDecodeJSON(sessionJSON, &run.Session)
	mustDecodeJSON(humanIntentJSON, &run.HumanIntent)
	mustDecodeJSON(approvalJSON, &run.Approval)
	mustDecodeJSON(objectRefsJSON, &run.ObjectRefs)
	mustDecodeJSON(requestedFieldsJSON, &run.RequestedFields)
	mustDecodeJSON(providerCallRefJSON, &run.ProviderCallRef)
	mustDecodeJSON(resultRefJSON, &run.ResultRef)
	mustDecodeJSON(evidenceRefsJSON, &run.EvidenceRefs)
	mustDecodeJSON(auditRefJSON, &run.AuditRef)
	run.CreatedAt = unixNanoToTime(createdAt)
	if completedAt.Valid {
		t := unixNanoToTime(completedAt.Int64)
		run.CompletedAt = &t
	}
	return run, nil
}

func mustJSONString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func mustDecodeJSON(raw string, out any) {
	if raw == "" {
		raw = "{}"
	}
	_ = json.Unmarshal([]byte(raw), out)
}

func defaultAnyMap(src map[string]any) map[string]any {
	if src != nil {
		return src
	}
	return map[string]any{}
}

func timeToUnixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano()
}

func optionalTimeToUnixNano(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().UnixNano()
}

func unixNanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

var _ Repository = (*PlatformRepository)(nil)
