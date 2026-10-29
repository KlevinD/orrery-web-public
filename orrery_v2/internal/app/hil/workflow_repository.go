package hil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const (
	workflowTemplateCommandTable      = "hil_workflow_template_commands"
	workflowNotificationProcessingTTL = 5 * time.Minute
)

func (r *PlatformRepository) SaveTemplateDraft(ctx context.Context, template WorkflowTemplate, idempotencyKey, fingerprint string) error {
	if r == nil || r.store == nil || strings.TrimSpace(template.ID) == "" || strings.TrimSpace(template.TenantID) == "" || strings.TrimSpace(template.Name) == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(fingerprint) == "" {
		return ErrInvalidRequest
	}
	if template.Status == "" {
		template.Status = WorkflowTemplateDisabled
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		claimed, err := claimWorkflowTemplateCommand(ctx, tx, template.TenantID, idempotencyKey, fingerprint)
		if err != nil || !claimed {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO hil_workflow_templates (
	id, tenant_id, name, description, status, draft_definition_json, latest_published_version,
	created_by_kind, created_by_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name = excluded.name,
	description = excluded.description,
	status = CASE
		WHEN hil_workflow_templates.status = 'active' THEN hil_workflow_templates.status
		ELSE excluded.status
	END,
	draft_definition_json = excluded.draft_definition_json,
	updated_at = excluded.updated_at
WHERE hil_workflow_templates.tenant_id = excluded.tenant_id`,
			template.ID, template.TenantID, template.Name, template.Description, template.Status,
			mustJSON(template.DraftDefinition), template.LatestPublishedVersion, template.CreatedBy.Kind,
			template.CreatedBy.ID, timeToInt(template.CreatedAt), timeToInt(template.UpdatedAt))
		return err
	})
}

func claimWorkflowTemplateCommand(ctx context.Context, tx db.Tx, tenantID, key, fingerprint string) (bool, error) {
	recordKey := strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(key)
	inserted, err := db.InsertRecordTx(ctx, tx, workflowTemplateCommandTable, recordKey, db.Record{"fingerprint": fingerprint})
	if err != nil || inserted {
		return inserted, err
	}
	record, found, err := db.GetRecordTx(ctx, tx, workflowTemplateCommandTable, recordKey)
	if err != nil {
		return false, err
	}
	if !found || strings.TrimSpace(fmt.Sprint(record["fingerprint"])) != strings.TrimSpace(fingerprint) {
		return false, ErrIdempotencyConflict
	}
	return false, nil
}

func (r *PlatformRepository) PublishTemplate(ctx context.Context, template WorkflowTemplate, version WorkflowTemplateVersion, bindings []WorkflowTemplateBinding, idempotencyKey, fingerprint string) (WorkflowTemplateVersion, error) {
	if r == nil || r.store == nil || strings.TrimSpace(template.TenantID) == "" || strings.TrimSpace(template.ID) == "" || version.TenantID != template.TenantID || version.TemplateID != template.ID || strings.TrimSpace(version.ID) == "" || version.Version <= 0 || strings.TrimSpace(version.DefinitionHash) == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(fingerprint) == "" {
		return WorkflowTemplateVersion{}, ErrInvalidRequest
	}
	if err := ValidateWorkflowDefinition(version.Definition); err != nil {
		return WorkflowTemplateVersion{}, err
	}
	for _, binding := range bindings {
		if err := validateWorkflowBinding(binding, template.TenantID, template.ID); err != nil {
			return WorkflowTemplateVersion{}, err
		}
	}
	var published WorkflowTemplateVersion
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		recordKey := strings.TrimSpace(template.TenantID) + ":" + strings.TrimSpace(idempotencyKey)
		inserted, err := db.InsertRecordTx(ctx, tx, workflowTemplateCommandTable, recordKey, db.Record{
			"fingerprint": fingerprint,
			"version_id":  version.ID,
		})
		if err != nil {
			return err
		}
		if !inserted {
			record, found, err := db.GetRecordTx(ctx, tx, workflowTemplateCommandTable, recordKey)
			if err != nil {
				return err
			}
			if !found || strings.TrimSpace(fmt.Sprint(record["fingerprint"])) != strings.TrimSpace(fingerprint) {
				return ErrIdempotencyConflict
			}
			versionID := strings.TrimSpace(fmt.Sprint(record["version_id"]))
			if versionID == "" {
				return ErrIdempotencyConflict
			}
			stored, err := getWorkflowTemplateVersionTx(ctx, tx, template.TenantID, versionID)
			if err != nil {
				return err
			}
			published = stored
			return nil
		}
		var existingID, existingHash string
		err = tx.QueryRowContext(ctx, `SELECT id, definition_hash FROM hil_workflow_template_versions WHERE tenant_id = ? AND template_id = ? AND version = ?`, template.TenantID, template.ID, version.Version).Scan(&existingID, &existingHash)
		if err == nil {
			if existingID == version.ID && existingHash == version.DefinitionHash {
				published = version
				return nil
			}
			return ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO hil_workflow_template_versions (
	id, tenant_id, template_id, version, definition_json, definition_hash,
	published_by_kind, published_by_id, published_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, version.ID, version.TenantID, version.TemplateID, version.Version,
			mustJSON(version.Definition), version.DefinitionHash, version.PublishedBy.Kind, version.PublishedBy.ID,
			timeToInt(version.PublishedAt)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE hil_workflow_templates
SET status = ?, draft_definition_json = ?, latest_published_version = ?, updated_at = ?
WHERE tenant_id = ? AND id = ? AND latest_published_version < ?`, WorkflowTemplateActive,
			mustJSON(template.DraftDefinition), version.Version, timeToInt(version.PublishedAt), template.TenantID, template.ID, version.Version)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return ErrConflict
		}
		for _, binding := range bindings {
			if err := putWorkflowBindingTx(ctx, tx, binding); err != nil {
				return err
			}
		}
		published = version
		return nil
	})
	if err != nil {
		return WorkflowTemplateVersion{}, err
	}
	return published, nil
}

func getWorkflowTemplateVersionTx(ctx context.Context, tx db.Tx, tenantID, versionID string) (WorkflowTemplateVersion, error) {
	var version WorkflowTemplateVersion
	var definitionJSON string
	var publishedAt int64
	err := tx.QueryRowContext(ctx, `
SELECT id, tenant_id, template_id, version, definition_json, definition_hash,
	published_by_kind, published_by_id, published_at
FROM hil_workflow_template_versions WHERE tenant_id = ? AND id = ?`, tenantID, versionID).Scan(
		&version.ID, &version.TenantID, &version.TemplateID, &version.Version, &definitionJSON,
		&version.DefinitionHash, &version.PublishedBy.Kind, &version.PublishedBy.ID, &publishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowTemplateVersion{}, ErrConflict
	}
	if err != nil {
		return WorkflowTemplateVersion{}, err
	}
	decodeJSON(definitionJSON, &version.Definition)
	version.PublishedAt = intToTime(publishedAt)
	return version, nil
}

func (r *PlatformRepository) DisableTemplate(ctx context.Context, tenantID, templateID string, actor ActorRef, reason string) error {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(templateID) == "" || normalizeActor(actor).Kind == "" || strings.TrimSpace(reason) == "" {
		return ErrInvalidRequest
	}
	result, err := r.store.Exec(ctx, `UPDATE hil_workflow_templates SET status = ?, updated_at = ? WHERE tenant_id = ? AND id = ?`, WorkflowTemplateDisabled, timeToInt(time.Now().UTC()), tenantID, templateID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *PlatformRepository) PutTemplateBinding(ctx context.Context, binding WorkflowTemplateBinding, actor ActorRef, reason string) error {
	if r == nil || r.store == nil || normalizeActor(actor).Kind == "" || strings.TrimSpace(reason) == "" {
		return ErrInvalidRequest
	}
	if err := validateWorkflowBinding(binding, binding.TenantID, binding.TemplateID); err != nil {
		return err
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error { return putWorkflowBindingTx(ctx, tx, binding) })
}

func validateWorkflowBinding(binding WorkflowTemplateBinding, tenantID, templateID string) error {
	if strings.TrimSpace(binding.ID) == "" || strings.TrimSpace(binding.TenantID) == "" || strings.TrimSpace(binding.ActionTypeID) == "" || strings.TrimSpace(binding.TemplateID) == "" || binding.TenantID != tenantID || binding.TemplateID != templateID {
		return ErrInvalidRequest
	}
	if binding.ActionTypeID == "tool_use" && strings.TrimSpace(binding.ToolName) == "" {
		return ErrInvalidRequest
	}
	return nil
}

func putWorkflowBindingTx(ctx context.Context, tx db.Tx, binding WorkflowTemplateBinding) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO hil_workflow_template_bindings (
	id, tenant_id, action_type_id, tool_name, risk_level, template_id, active,
	created_by_kind, created_by_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, action_type_id, tool_name, risk_level) DO UPDATE SET
	template_id = excluded.template_id,
	active = excluded.active,
	updated_at = excluded.updated_at`, binding.ID, binding.TenantID, binding.ActionTypeID,
		strings.TrimSpace(binding.ToolName), binding.RiskLevel, binding.TemplateID, binding.Active,
		binding.CreatedBy.Kind, binding.CreatedBy.ID, timeToInt(binding.CreatedAt), timeToInt(binding.UpdatedAt))
	return err
}

func (r *PlatformRepository) ListTemplates(ctx context.Context, query WorkflowTemplateListQuery) ([]WorkflowTemplateSummary, error) {
	if r == nil || r.store == nil || strings.TrimSpace(query.TenantID) == "" {
		return nil, ErrInvalidRequest
	}
	sqlQuery := `SELECT id, tenant_id, name, description, status, latest_published_version, updated_at FROM hil_workflow_templates WHERE tenant_id = ?`
	args := []any{query.TenantID}
	if query.Status != "" {
		sqlQuery += ` AND status = ?`
		args = append(args, query.Status)
	}
	sqlQuery += ` ORDER BY updated_at DESC, id`
	rows, err := r.store.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkflowTemplateSummary{}
	for rows.Next() {
		var item WorkflowTemplateSummary
		var updatedAt int64
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Name, &item.Description, &item.Status, &item.LatestPublishedVersion, &updatedAt); err != nil {
			return nil, err
		}
		item.UpdatedAt = intToTime(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PlatformRepository) GetWorkflowTemplate(ctx context.Context, tenantID, templateID string) (WorkflowTemplate, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(templateID) == "" {
		return WorkflowTemplate{}, ErrInvalidRequest
	}
	var template WorkflowTemplate
	var definitionJSON string
	var createdAt, updatedAt int64
	err := r.store.QueryRow(ctx, `
SELECT id, tenant_id, name, description, status, draft_definition_json,
	latest_published_version, created_by_kind, created_by_id, created_at, updated_at
FROM hil_workflow_templates WHERE tenant_id = ? AND id = ?`, tenantID, templateID).Scan(
		&template.ID, &template.TenantID, &template.Name, &template.Description, &template.Status,
		&definitionJSON, &template.LatestPublishedVersion, &template.CreatedBy.Kind,
		&template.CreatedBy.ID, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowTemplate{}, ErrNotFound
	}
	if err != nil {
		return WorkflowTemplate{}, err
	}
	decodeJSON(definitionJSON, &template.DraftDefinition)
	template.CreatedAt = intToTime(createdAt)
	template.UpdatedAt = intToTime(updatedAt)
	return template, nil
}

func (r *PlatformRepository) ResolveTemplate(ctx context.Context, selector WorkflowTemplateSelector) (WorkflowTemplateVersion, error) {
	if r == nil || r.store == nil || strings.TrimSpace(selector.TenantID) == "" || strings.TrimSpace(selector.ActionTypeID) == "" {
		return WorkflowTemplateVersion{}, ErrInvalidRequest
	}
	row := r.store.QueryRow(ctx, `
SELECT version.id, version.tenant_id, version.template_id, version.version,
	version.definition_json, version.definition_hash, version.published_by_kind,
	version.published_by_id, version.published_at
FROM hil_workflow_template_bindings AS binding
JOIN hil_workflow_templates AS template
	ON template.tenant_id = binding.tenant_id AND template.id = binding.template_id
JOIN hil_workflow_template_versions AS version
	ON version.tenant_id = template.tenant_id AND version.template_id = template.id
	AND version.version = template.latest_published_version
WHERE binding.tenant_id IN (?, ?) AND binding.active = TRUE AND template.status = ?
	AND binding.action_type_id = ?
	AND (binding.tool_name = ? OR binding.tool_name = '')
	AND (binding.risk_level = ? OR binding.risk_level = '')
ORDER BY CASE WHEN binding.tenant_id = ? THEN 0 ELSE 1 END,
	CASE WHEN binding.risk_level = ? THEN 0 ELSE 1 END,
	CASE WHEN binding.tool_name = ? THEN 0 ELSE 1 END, binding.id
LIMIT 1`, selector.TenantID, BuiltInWorkflowTenantID, WorkflowTemplateActive, selector.ActionTypeID, strings.TrimSpace(selector.ToolName), selector.RiskLevel, selector.TenantID, selector.RiskLevel, strings.TrimSpace(selector.ToolName))
	var version WorkflowTemplateVersion
	var definitionJSON string
	var publishedAt int64
	if err := row.Scan(&version.ID, &version.TenantID, &version.TemplateID, &version.Version, &definitionJSON, &version.DefinitionHash, &version.PublishedBy.Kind, &version.PublishedBy.ID, &publishedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowTemplateVersion{}, ErrNotFound
		}
		return WorkflowTemplateVersion{}, err
	}
	decodeJSON(definitionJSON, &version.Definition)
	version.PublishedAt = intToTime(publishedAt)
	return version, nil
}

func (r *PlatformRepository) CreateDecisionWorkflow(ctx context.Context, decision DecisionRequest, caseRecord WorkflowCase, instance WorkflowInstance, stages []WorkflowStageInstance, tasks []WorkflowTask, audit []WorkflowAuditEvent, notifications []WorkflowNotification) error {
	if r == nil || r.store == nil || !validWorkflowAggregate(decision, caseRecord, instance, stages, tasks, audit, notifications) {
		return ErrInvalidRequest
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		inserted, err := insertDecision(ctx, tx, decision)
		if err != nil {
			return err
		}
		if !inserted {
			var existingInstanceID string
			err := tx.QueryRowContext(ctx, `SELECT id FROM hil_workflow_instances WHERE tenant_id = ? AND decision_request_id = ?`, decision.TenantID, decision.ID).Scan(&existingInstanceID)
			if err == nil && existingInstanceID == instance.ID {
				return nil
			}
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			return err
		}
		var runningInstances int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM hil_workflow_instances WHERE tenant_id = ? AND case_id = ? AND status = ?`, instance.TenantID, instance.CaseID, WorkflowRunning).Scan(&runningInstances); err != nil {
			return err
		}
		if runningInstances != 0 {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO hil_workflow_cases (
	id, tenant_id, requester_kind, requester_id, action_type_id, tool_name,
	resource_kind, resource_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`, caseRecord.ID, caseRecord.TenantID, caseRecord.Requester.Kind,
			caseRecord.Requester.ID, caseRecord.ActionTypeID, caseRecord.ToolName, caseRecord.Resource.Kind,
			caseRecord.Resource.ID, timeToInt(caseRecord.CreatedAt)); err != nil {
			return err
		}
		var storedTenantID, storedRequesterKind, storedRequesterID, storedResourceKind, storedResourceID string
		if err := tx.QueryRowContext(ctx, `SELECT tenant_id, requester_kind, requester_id, resource_kind, resource_id FROM hil_workflow_cases WHERE id = ?`, caseRecord.ID).Scan(&storedTenantID, &storedRequesterKind, &storedRequesterID, &storedResourceKind, &storedResourceID); err != nil {
			return err
		}
		if storedTenantID != caseRecord.TenantID || storedRequesterKind != string(caseRecord.Requester.Kind) || storedRequesterID != caseRecord.Requester.ID || storedResourceKind != caseRecord.Resource.Kind || storedResourceID != caseRecord.Resource.ID {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO hil_workflow_instances (
	id, tenant_id, case_id, revision, decision_request_id, template_version_id,
	predecessor_instance_id, definition_hash, material_hash, args_hash, status,
	current_stage_key, version, created_at, updated_at, resolved_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, instance.ID, instance.TenantID,
			instance.CaseID, instance.Revision, instance.DecisionRequestID, instance.TemplateVersionID,
			instance.PredecessorInstanceID, instance.DefinitionHash, instance.MaterialHash, instance.ArgsHash,
			instance.Status, instance.CurrentStageKey, instance.Version, timeToInt(instance.CreatedAt),
			timeToInt(instance.UpdatedAt), optionalTimeToInt(instance.ResolvedAt), optionalTimeToInt(instance.ExpiresAt)); err != nil {
			return err
		}
		for _, stage := range stages {
			if err := insertWorkflowStage(ctx, tx, stage); err != nil {
				return err
			}
		}
		for _, task := range tasks {
			if err := insertWorkflowTask(ctx, tx, task); err != nil {
				return err
			}
		}
		for _, event := range audit {
			if err := insertWorkflowAudit(ctx, tx, event); err != nil {
				return err
			}
		}
		for _, notification := range notifications {
			if err := insertWorkflowNotification(ctx, tx, notification); err != nil {
				return err
			}
		}
		for _, stage := range stages {
			if stage.Status != WorkflowStageActive || stage.Definition.Type != WorkflowStageCouncil {
				continue
			}
			council, err := workflowCouncilForStage(decision, stage)
			if err != nil {
				return err
			}
			if err := insertCouncil(ctx, tx, council); err != nil {
				return err
			}
		}
		return nil
	})
}

func validWorkflowAggregate(decision DecisionRequest, caseRecord WorkflowCase, instance WorkflowInstance, stages []WorkflowStageInstance, tasks []WorkflowTask, audit []WorkflowAuditEvent, notifications []WorkflowNotification) bool {
	if strings.TrimSpace(decision.ID) == "" || strings.TrimSpace(decision.TenantID) == "" || strings.TrimSpace(decision.IdempotencyKey) == "" || strings.TrimSpace(decision.RequestFingerprint) == "" || caseRecord.TenantID != decision.TenantID || instance.TenantID != decision.TenantID || instance.CaseID != caseRecord.ID || instance.DecisionRequestID != decision.ID || instance.ArgsHash != decision.ArgsHash || instance.Status != WorkflowRunning || instance.Version != 1 {
		return false
	}
	stageIDs := map[string]WorkflowStageStatus{}
	active := 0
	for _, stage := range stages {
		if strings.TrimSpace(stage.ID) == "" || stage.TenantID != instance.TenantID || stage.WorkflowInstanceID != instance.ID || strings.TrimSpace(stage.Definition.Key) == "" {
			return false
		}
		if stage.Status == WorkflowStageActive {
			active++
			if stage.Definition.Key != instance.CurrentStageKey {
				return false
			}
		}
		stageIDs[stage.ID] = stage.Status
	}
	if active != 1 {
		return false
	}
	for _, task := range tasks {
		stageStatus, ok := stageIDs[task.StageInstanceID]
		if !ok || task.TenantID != instance.TenantID || task.WorkflowInstanceID != instance.ID || task.OriginalAssignee.Kind != ActorHuman || task.CurrentAssignee.Kind != ActorHuman || strings.TrimSpace(task.CurrentAssignee.ID) == "" {
			return false
		}
		if (stageStatus == WorkflowStageActive && task.Status != TaskPending) || (stageStatus == WorkflowStageScheduled && task.Status != TaskScheduled) {
			return false
		}
	}
	for _, event := range audit {
		if event.TenantID != instance.TenantID || event.WorkflowInstanceID != instance.ID || event.CaseID != caseRecord.ID || strings.TrimSpace(event.IdempotencyKey) == "" || strings.TrimSpace(event.RequestFingerprint) == "" {
			return false
		}
	}
	for _, notification := range notifications {
		if notification.TenantID != instance.TenantID || notification.WorkflowInstanceID != instance.ID || strings.TrimSpace(notification.IdempotencyKey) == "" {
			return false
		}
	}
	return true
}

func insertWorkflowStage(ctx context.Context, tx db.Tx, stage WorkflowStageInstance) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO hil_workflow_stage_instances (
	id, tenant_id, workflow_instance_id, stage_key, stage_order, stage_type, policy_json,
	resolved_participants_json, resolved_escalation_actors_json, status, deadline_at, escalation_index, created_at,
	activated_at, resolved_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, stage.ID, stage.TenantID, stage.WorkflowInstanceID,
		stage.Definition.Key, stage.Definition.Order, stage.Definition.Type, mustJSON(stage.Definition),
		mustJSON(stage.ResolvedParticipants), mustJSON(stage.ResolvedEscalationActors), stage.Status, optionalTimeToInt(stage.DeadlineAt), stage.EscalationIndex,
		timeToInt(stage.CreatedAt), optionalTimeToInt(stage.ActivatedAt), optionalTimeToInt(stage.ResolvedAt))
	return err
}

func insertWorkflowTask(ctx context.Context, tx db.Tx, task WorkflowTask) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO hil_workflow_tasks (
	id, tenant_id, workflow_instance_id, stage_instance_id, seat_key,
	original_assignee_kind, original_assignee_id, current_assignee_kind, current_assignee_id,
	status, decision, reason, transfer_count, transferred_from_id, transferred_to_id,
	transfer_reason, deadline_at, created_at, updated_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, task.ID, task.TenantID,
		task.WorkflowInstanceID, task.StageInstanceID, task.SeatKey, task.OriginalAssignee.Kind,
		task.OriginalAssignee.ID, task.CurrentAssignee.Kind, task.CurrentAssignee.ID, task.Status,
		task.Decision, task.Reason, task.TransferCount, task.TransferredFromID, task.TransferredToID,
		task.TransferReason, optionalTimeToInt(task.DeadlineAt), timeToInt(task.CreatedAt),
		timeToInt(task.UpdatedAt), optionalTimeToInt(task.CompletedAt))
	return err
}

func insertWorkflowAudit(ctx context.Context, tx db.Tx, event WorkflowAuditEvent) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO hil_workflow_audit_events (
	id, tenant_id, case_id, workflow_instance_id, stage_instance_id, task_id,
	actor_kind, actor_id, event_type, reason, metadata_json, correlation_id,
	idempotency_key, request_fingerprint, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.TenantID, event.CaseID,
		event.WorkflowInstanceID, event.StageInstanceID, event.TaskID, event.Actor.Kind, event.Actor.ID,
		event.EventType, event.Reason, mustJSON(event.Metadata), event.CorrelationID, event.IdempotencyKey,
		event.RequestFingerprint, timeToInt(event.CreatedAt))
	return err
}

func insertWorkflowNotification(ctx context.Context, tx db.Tx, notification WorkflowNotification) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO hil_workflow_notification_outbox (
	id, tenant_id, workflow_instance_id, stage_instance_id, task_id, channel,
	recipient_kind, recipient_id, event_type, payload_json, status, attempt_count,
	next_attempt_at, provider_receipt, last_error, idempotency_key, created_at,
	updated_at, delivered_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, idempotency_key) DO NOTHING`, notification.ID, notification.TenantID,
		notification.WorkflowInstanceID, notification.StageInstanceID, notification.TaskID, notification.Channel,
		notification.Recipient.Kind, notification.Recipient.ID, notification.EventType, mustJSON(notification.Payload),
		notification.Status, notification.AttemptCount, optionalTimeToInt(notification.NextAttemptAt),
		notification.ProviderReceipt, notification.LastError, notification.IdempotencyKey,
		timeToInt(notification.CreatedAt), timeToInt(notification.UpdatedAt), optionalTimeToInt(notification.DeliveredAt))
	return err
}

func (r *PlatformRepository) GetWorkflowInstance(ctx context.Context, tenantID, instanceID string) (WorkflowInstanceDetail, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(instanceID) == "" {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	instance, err := scanWorkflowInstance(r.store.QueryRow(ctx, workflowInstanceSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, instanceID))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowInstanceDetail{}, ErrNotFound
	}
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	caseRecord, err := scanWorkflowCase(r.store.QueryRow(ctx, `
SELECT id, tenant_id, requester_kind, requester_id, action_type_id, tool_name,
	resource_kind, resource_id, created_at
FROM hil_workflow_cases WHERE tenant_id = ? AND id = ?`, tenantID, instance.CaseID))
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	decision, err := r.GetDecision(ctx, tenantID, instance.DecisionRequestID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	stages, err := r.listWorkflowStages(ctx, tenantID, instanceID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	tasks, err := r.listWorkflowTasks(ctx, tenantID, instanceID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	audit, err := r.listWorkflowAudit(ctx, tenantID, instanceID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	notifications, err := r.listWorkflowNotifications(ctx, tenantID, instanceID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	receipts, err := r.ListDecisionReceipts(ctx, tenantID, instance.DecisionRequestID)
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	return WorkflowInstanceDetail{
		Case: caseRecord, Instance: instance, Decision: decision, Stages: stages,
		Tasks: tasks, Audit: audit, Notifications: notifications, Receipts: receipts,
	}, nil
}

func (r *PlatformRepository) GetWorkflowInstanceByTaskID(ctx context.Context, tenantID, taskID string) (WorkflowInstanceDetail, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(taskID) == "" {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	var instanceID string
	if err := r.store.QueryRow(ctx, `SELECT workflow_instance_id FROM hil_workflow_tasks WHERE tenant_id = ? AND id = ?`, tenantID, taskID).Scan(&instanceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowInstanceDetail{}, ErrNotFound
		}
		return WorkflowInstanceDetail{}, err
	}
	return r.GetWorkflowInstance(ctx, tenantID, instanceID)
}

func (r *PlatformRepository) GetWorkflowInstanceByDecisionID(ctx context.Context, tenantID, decisionID string) (WorkflowInstanceDetail, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(decisionID) == "" {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	var instanceID string
	if err := r.store.QueryRow(ctx, `SELECT id FROM hil_workflow_instances WHERE tenant_id = ? AND decision_request_id = ?`, tenantID, decisionID).Scan(&instanceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowInstanceDetail{}, ErrNotFound
		}
		return WorkflowInstanceDetail{}, err
	}
	return r.GetWorkflowInstance(ctx, tenantID, instanceID)
}

func (r *PlatformRepository) GetWorkflowInstanceByCouncilID(ctx context.Context, tenantID, councilID string) (WorkflowInstanceDetail, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(councilID) == "" {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	var instanceID string
	if err := r.store.QueryRow(ctx, `SELECT workflow_instance_id FROM hil_workflow_stage_instances WHERE tenant_id = ? AND id = ? AND stage_type = ?`, tenantID, councilID, WorkflowStageCouncil).Scan(&instanceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowInstanceDetail{}, ErrNotFound
		}
		return WorkflowInstanceDetail{}, err
	}
	return r.GetWorkflowInstance(ctx, tenantID, instanceID)
}

func workflowInstanceSelectSQL() string {
	return `SELECT id, tenant_id, case_id, revision, decision_request_id, template_version_id,
	predecessor_instance_id, definition_hash, material_hash, args_hash, status,
	current_stage_key, version, created_at, updated_at, resolved_at, expires_at
FROM hil_workflow_instances`
}

func scanWorkflowInstance(row rowScanner) (WorkflowInstance, error) {
	var instance WorkflowInstance
	var createdAt, updatedAt int64
	var resolvedAt, expiresAt sql.NullInt64
	err := row.Scan(&instance.ID, &instance.TenantID, &instance.CaseID, &instance.Revision,
		&instance.DecisionRequestID, &instance.TemplateVersionID, &instance.PredecessorInstanceID,
		&instance.DefinitionHash, &instance.MaterialHash, &instance.ArgsHash, &instance.Status,
		&instance.CurrentStageKey, &instance.Version, &createdAt, &updatedAt, &resolvedAt, &expiresAt)
	if err != nil {
		return WorkflowInstance{}, err
	}
	instance.CreatedAt = intToTime(createdAt)
	instance.UpdatedAt = intToTime(updatedAt)
	instance.ResolvedAt = nullIntToTime(resolvedAt)
	instance.ExpiresAt = nullIntToTime(expiresAt)
	return instance, nil
}

func scanWorkflowCase(row rowScanner) (WorkflowCase, error) {
	var record WorkflowCase
	var createdAt int64
	if err := row.Scan(&record.ID, &record.TenantID, &record.Requester.Kind, &record.Requester.ID,
		&record.ActionTypeID, &record.ToolName, &record.Resource.Kind, &record.Resource.ID, &createdAt); err != nil {
		return WorkflowCase{}, err
	}
	record.CreatedAt = intToTime(createdAt)
	return record, nil
}

func (r *PlatformRepository) listWorkflowStages(ctx context.Context, tenantID, instanceID string) ([]WorkflowStageInstance, error) {
	rows, err := r.store.Query(ctx, `
SELECT id, tenant_id, workflow_instance_id, policy_json, resolved_participants_json,
	resolved_escalation_actors_json, status, deadline_at, escalation_index, created_at, activated_at, resolved_at
FROM hil_workflow_stage_instances
WHERE tenant_id = ? AND workflow_instance_id = ? ORDER BY stage_order, id`, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkflowStageInstance{}
	for rows.Next() {
		var item WorkflowStageInstance
		var definitionJSON, participantsJSON, escalationActorsJSON string
		var deadlineAt, activatedAt, resolvedAt sql.NullInt64
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.TenantID, &item.WorkflowInstanceID, &definitionJSON,
			&participantsJSON, &escalationActorsJSON, &item.Status, &deadlineAt, &item.EscalationIndex, &createdAt,
			&activatedAt, &resolvedAt); err != nil {
			return nil, err
		}
		decodeJSON(definitionJSON, &item.Definition)
		decodeJSON(participantsJSON, &item.ResolvedParticipants)
		decodeJSON(escalationActorsJSON, &item.ResolvedEscalationActors)
		item.DeadlineAt = nullIntToTime(deadlineAt)
		item.CreatedAt = intToTime(createdAt)
		item.ActivatedAt = nullIntToTime(activatedAt)
		item.ResolvedAt = nullIntToTime(resolvedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PlatformRepository) listWorkflowTasks(ctx context.Context, tenantID, instanceID string) ([]WorkflowTask, error) {
	rows, err := r.store.Query(ctx, `
SELECT task.id, task.tenant_id, task.workflow_instance_id, task.stage_instance_id, task.seat_key,
	task.original_assignee_kind, task.original_assignee_id, task.current_assignee_kind, task.current_assignee_id,
	task.status, task.decision, task.reason, task.transfer_count, task.transferred_from_id, task.transferred_to_id,
	task.transfer_reason, task.deadline_at, task.created_at, task.updated_at, task.completed_at
FROM hil_workflow_tasks AS task
JOIN hil_workflow_stage_instances AS stage
	ON stage.tenant_id = task.tenant_id AND stage.id = task.stage_instance_id
WHERE task.tenant_id = ? AND task.workflow_instance_id = ?
ORDER BY stage.stage_order, task.seat_key, task.id`, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkflowTask{}
	for rows.Next() {
		var item WorkflowTask
		var deadlineAt, completedAt sql.NullInt64
		var createdAt, updatedAt int64
		if err := rows.Scan(&item.ID, &item.TenantID, &item.WorkflowInstanceID, &item.StageInstanceID,
			&item.SeatKey, &item.OriginalAssignee.Kind, &item.OriginalAssignee.ID,
			&item.CurrentAssignee.Kind, &item.CurrentAssignee.ID, &item.Status, &item.Decision,
			&item.Reason, &item.TransferCount, &item.TransferredFromID, &item.TransferredToID,
			&item.TransferReason, &deadlineAt, &createdAt, &updatedAt, &completedAt); err != nil {
			return nil, err
		}
		item.DeadlineAt = nullIntToTime(deadlineAt)
		item.CreatedAt = intToTime(createdAt)
		item.UpdatedAt = intToTime(updatedAt)
		item.CompletedAt = nullIntToTime(completedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PlatformRepository) listWorkflowAudit(ctx context.Context, tenantID, instanceID string) ([]WorkflowAuditEvent, error) {
	rows, err := r.store.Query(ctx, `
SELECT id, tenant_id, case_id, workflow_instance_id, stage_instance_id, task_id,
	actor_kind, actor_id, event_type, reason, metadata_json, correlation_id,
	idempotency_key, request_fingerprint, created_at
FROM hil_workflow_audit_events WHERE tenant_id = ? AND workflow_instance_id = ?
ORDER BY created_at, id`, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkflowAuditEvent{}
	for rows.Next() {
		var item WorkflowAuditEvent
		var metadataJSON string
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.TenantID, &item.CaseID, &item.WorkflowInstanceID,
			&item.StageInstanceID, &item.TaskID, &item.Actor.Kind, &item.Actor.ID, &item.EventType,
			&item.Reason, &metadataJSON, &item.CorrelationID, &item.IdempotencyKey,
			&item.RequestFingerprint, &createdAt); err != nil {
			return nil, err
		}
		decodeJSON(metadataJSON, &item.Metadata)
		item.CreatedAt = intToTime(createdAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PlatformRepository) listWorkflowNotifications(ctx context.Context, tenantID, instanceID string) ([]WorkflowNotification, error) {
	rows, err := r.store.Query(ctx, workflowNotificationSelectSQL()+` WHERE tenant_id = ? AND workflow_instance_id = ? ORDER BY created_at, id`, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWorkflowNotificationRows(rows)
}

func workflowNotificationSelectSQL() string {
	return `SELECT id, tenant_id, workflow_instance_id, stage_instance_id, task_id,
	channel, recipient_kind, recipient_id, event_type, payload_json, status,
	attempt_count, next_attempt_at, provider_receipt, last_error, idempotency_key,
	created_at, updated_at, delivered_at FROM hil_workflow_notification_outbox`
}

func scanWorkflowNotificationRows(rows *sql.Rows) ([]WorkflowNotification, error) {
	items := []WorkflowNotification{}
	for rows.Next() {
		var item WorkflowNotification
		var payloadJSON string
		var nextAttemptAt, deliveredAt sql.NullInt64
		var createdAt, updatedAt int64
		if err := rows.Scan(&item.ID, &item.TenantID, &item.WorkflowInstanceID, &item.StageInstanceID,
			&item.TaskID, &item.Channel, &item.Recipient.Kind, &item.Recipient.ID, &item.EventType,
			&payloadJSON, &item.Status, &item.AttemptCount, &nextAttemptAt, &item.ProviderReceipt,
			&item.LastError, &item.IdempotencyKey, &createdAt, &updatedAt, &deliveredAt); err != nil {
			return nil, err
		}
		decodeJSON(payloadJSON, &item.Payload)
		item.NextAttemptAt = nullIntToTime(nextAttemptAt)
		item.CreatedAt = intToTime(createdAt)
		item.UpdatedAt = intToTime(updatedAt)
		item.DeliveredAt = nullIntToTime(deliveredAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PlatformRepository) ListWorkflowInstances(ctx context.Context, query WorkflowListQuery) (WorkflowInstancePage, error) {
	if r == nil || r.store == nil || strings.TrimSpace(query.TenantID) == "" {
		return WorkflowInstancePage{}, ErrInvalidRequest
	}
	where := ` WHERE instance.tenant_id = ?`
	args := []any{query.TenantID}
	if decisionID := strings.TrimSpace(query.DecisionRequestID); decisionID != "" {
		where += ` AND instance.decision_request_id = ?`
		args = append(args, decisionID)
	}
	if query.Status != "" {
		where += ` AND instance.status = ?`
		args = append(args, query.Status)
	}
	actor := normalizeActor(query.ActionableBy)
	if actor.Kind != "" || actor.ID != "" {
		if actor.Kind == "" || actor.ID == "" {
			return WorkflowInstancePage{}, ErrInvalidRequest
		}
		where += ` AND EXISTS (
			SELECT 1 FROM hil_decision_requests AS decision
			WHERE decision.tenant_id = instance.tenant_id
			AND decision.id = instance.decision_request_id
			AND decision.status = ?
		) AND EXISTS (
			SELECT 1 FROM hil_workflow_tasks AS task
			WHERE task.tenant_id = instance.tenant_id
			AND task.workflow_instance_id = instance.id
			AND task.status = ? AND task.current_assignee_kind = ? AND task.current_assignee_id = ?
		)`
		args = append(args, DecisionPending, TaskPending, actor.Kind, actor.ID)
	}
	var total int
	if err := r.store.QueryRow(ctx, `SELECT COUNT(*) FROM hil_workflow_instances AS instance`+where, args...).Scan(&total); err != nil {
		return WorkflowInstancePage{}, err
	}
	limit, offset := normalizePage(query.Limit, query.Offset)
	rows, err := r.store.Query(ctx, `SELECT instance.id FROM hil_workflow_instances AS instance`+where+` ORDER BY instance.created_at DESC, instance.id LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return WorkflowInstancePage{}, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return WorkflowInstancePage{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return WorkflowInstancePage{}, err
	}
	items := make([]WorkflowInstanceDetail, 0, len(ids))
	for _, id := range ids {
		item, err := r.GetWorkflowInstance(ctx, query.TenantID, id)
		if err != nil {
			return WorkflowInstancePage{}, err
		}
		items = append(items, item)
	}
	return WorkflowInstancePage{Items: items, Total: total}, nil
}

func (r *PlatformRepository) RecordWorkflowViewed(ctx context.Context, tenantID, instanceID string, actor ActorRef, idempotencyKey, fingerprint string) error {
	actor = normalizeActor(actor)
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(instanceID) == "" || actor.Kind == "" || actor.ID == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(fingerprint) == "" {
		return ErrInvalidRequest
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		replayed, err := workflowAuditReplay(ctx, tx, tenantID, instanceID, idempotencyKey)
		if err != nil || replayed {
			return err
		}
		var caseID string
		if err := tx.QueryRowContext(ctx, `SELECT case_id FROM hil_workflow_instances WHERE tenant_id = ? AND id = ?`, tenantID, instanceID).Scan(&caseID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var viewedCount int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM hil_workflow_audit_events
WHERE tenant_id = ? AND workflow_instance_id = ? AND event_type = ?`, tenantID, instanceID, "workflow.viewed").Scan(&viewedCount); err != nil {
			return err
		}
		if viewedCount > 0 {
			return nil
		}
		return insertWorkflowAudit(ctx, tx, WorkflowAuditEvent{
			ID: "viewed:" + instanceID + ":" + idempotencyKey, TenantID: tenantID, CaseID: caseID,
			WorkflowInstanceID: instanceID, Actor: actor, EventType: "workflow.viewed",
			IdempotencyKey: idempotencyKey, RequestFingerprint: fingerprint, CreatedAt: time.Now().UTC(),
		})
	})
}

func workflowAuditReplay(ctx context.Context, tx db.Tx, tenantID, instanceID, key string) (bool, error) {
	var existingInstanceID string
	err := tx.QueryRowContext(ctx, `SELECT workflow_instance_id FROM hil_workflow_audit_events WHERE tenant_id = ? AND idempotency_key = ?`, tenantID, key).Scan(&existingInstanceID)
	if err == nil {
		if existingInstanceID == instanceID {
			return true, nil
		}
		return false, ErrIdempotencyConflict
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (r *PlatformRepository) ApplyWorkflowCommand(ctx context.Context, command WorkflowCommand) (WorkflowInstanceDetail, error) {
	if r == nil || r.store == nil || strings.TrimSpace(command.TenantID) == "" || strings.TrimSpace(command.InstanceID) == "" || command.ExpectedInstanceVersion <= 0 || command.Status == "" || command.UpdatedAt.IsZero() || strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.RequestFingerprint) == "" || len(command.Audit) == 0 {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	var existingInstanceID, existingFingerprint string
	err := r.store.QueryRow(ctx, `SELECT workflow_instance_id, request_fingerprint FROM hil_workflow_audit_events WHERE tenant_id = ? AND idempotency_key = ?`, command.TenantID, command.IdempotencyKey).Scan(&existingInstanceID, &existingFingerprint)
	if err == nil {
		if existingInstanceID != command.InstanceID || existingFingerprint != command.RequestFingerprint {
			return WorkflowInstanceDetail{}, ErrIdempotencyConflict
		}
		return r.GetWorkflowInstance(ctx, command.TenantID, command.InstanceID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkflowInstanceDetail{}, err
	}
	err = r.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE hil_workflow_instances
SET status = ?, current_stage_key = ?, version = version + 1, updated_at = ?, resolved_at = ?
WHERE tenant_id = ? AND id = ? AND version = ? AND status = ?`, command.Status,
			command.CurrentStageKey, timeToInt(command.UpdatedAt), terminalWorkflowResolvedAt(command.Status, command.UpdatedAt),
			command.TenantID, command.InstanceID, command.ExpectedInstanceVersion, WorkflowRunning)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return ErrConflict
		}
		if command.Task != nil {
			if err := updateWorkflowTask(ctx, tx, command.TenantID, command.InstanceID, *command.Task); err != nil {
				return err
			}
		}
		for _, task := range command.Tasks {
			if err := updateWorkflowTask(ctx, tx, command.TenantID, command.InstanceID, task); err != nil {
				return err
			}
		}
		for _, task := range command.NewTasks {
			if task.TenantID != command.TenantID || task.WorkflowInstanceID != command.InstanceID {
				return ErrInvalidRequest
			}
			if err := insertWorkflowTask(ctx, tx, task); err != nil {
				return err
			}
		}
		if command.CouncilToCreate != nil {
			if command.CouncilToCreate.TenantID != command.TenantID || command.CouncilToCreate.RequestID == "" {
				return ErrInvalidRequest
			}
			if err := insertCouncil(ctx, tx, *command.CouncilToCreate); err != nil {
				return err
			}
		}
		if command.CouncilVote != nil {
			if err := insertWorkflowCouncilVote(ctx, tx, command.TenantID, command.CouncilID, *command.CouncilVote, command.IdempotencyKey, command.RequestFingerprint); err != nil {
				return err
			}
		}
		if command.CouncilToResolve != nil {
			council := command.CouncilToResolve
			result, err := tx.ExecContext(ctx, `
UPDATE hil_council_sessions SET status = ?, effect = ?, dissent_json = ?, updated_at = ?, resolved_at = ?
WHERE tenant_id = ? AND id = ? AND status = ?`, council.Status, council.Effect, mustJSON(council.Dissent),
				timeToInt(council.UpdatedAt), optionalTimeToInt(council.ResolvedAt), command.TenantID, council.ID, CouncilOpen)
			if err != nil {
				return err
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 1 {
				return ErrConflict
			}
		}
		for _, stage := range command.Stages {
			if err := updateWorkflowStage(ctx, tx, command.TenantID, command.InstanceID, stage); err != nil {
				return err
			}
		}
		for _, event := range command.Audit {
			if event.IdempotencyKey == "" {
				event.IdempotencyKey = command.IdempotencyKey
			}
			if event.RequestFingerprint == "" {
				event.RequestFingerprint = command.RequestFingerprint
			}
			if event.WorkflowInstanceID != command.InstanceID || event.TenantID != command.TenantID {
				return ErrInvalidRequest
			}
			if err := insertWorkflowAudit(ctx, tx, event); err != nil {
				return err
			}
		}
		for _, notification := range command.Notifications {
			if notification.WorkflowInstanceID != command.InstanceID || notification.TenantID != command.TenantID {
				return ErrInvalidRequest
			}
			if err := insertWorkflowNotification(ctx, tx, notification); err != nil {
				return err
			}
		}
		var decisionID, argsHash, materialHash string
		if err := tx.QueryRowContext(ctx, `SELECT decision_request_id, args_hash, material_hash FROM hil_workflow_instances WHERE tenant_id = ? AND id = ?`, command.TenantID, command.InstanceID).Scan(&decisionID, &argsHash, &materialHash); err != nil {
			return err
		}
		for _, event := range command.Audit {
			if evidence, ok := event.Metadata["delegation"].(DelegationEvidence); ok {
				if err := r.lockDelegatedDecision(ctx, tx, command.TenantID, decisionID, evidence); err != nil {
					return err
				}
			}
		}
		if command.DecisionStatus != "" {
			decisionResult, err := tx.ExecContext(ctx, `UPDATE hil_decision_requests SET status = ? WHERE tenant_id = ? AND id = ? AND args_hash = ? AND status = ?`, command.DecisionStatus, command.TenantID, decisionID, argsHash, DecisionPending)
			if err != nil {
				return err
			}
			affected, err := decisionResult.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				var currentStatus DecisionStatus
				if err := tx.QueryRowContext(ctx, `SELECT status FROM hil_decision_requests WHERE tenant_id = ? AND id = ? AND args_hash = ?`, command.TenantID, decisionID, argsHash).Scan(&currentStatus); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return ErrConflict
					}
					return err
				}
				if currentStatus != command.DecisionStatus {
					return ErrConflict
				}
			}
		}
		if command.Receipt != nil {
			receipt := *command.Receipt
			if receipt.TenantID != command.TenantID || receipt.RequestID != decisionID || receipt.WorkflowInstanceID != command.InstanceID || receipt.ArgsHash != argsHash || receipt.MaterialHash != materialHash {
				return ErrInvalidRequest
			}
			if err := insertWorkflowReceipt(ctx, tx, receipt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return WorkflowInstanceDetail{}, err
	}
	return r.GetWorkflowInstance(ctx, command.TenantID, command.InstanceID)
}

func workflowCouncilForStage(decision DecisionRequest, stage WorkflowStageInstance) (CouncilSession, error) {
	if stage.Definition.Council == nil || stage.ID == "" || len(stage.ResolvedParticipants) == 0 {
		return CouncilSession{}, ErrInvalidRequest
	}
	participants := make([]CouncilParticipant, 0, len(stage.ResolvedParticipants))
	for _, actor := range stage.ResolvedParticipants {
		participants = append(participants, CouncilParticipant{Actor: actor})
	}
	return CouncilSession{
		ID: stage.ID, TenantID: decision.TenantID, RequestID: decision.ID, ArgsHash: decision.ArgsHash,
		RiskLevel: decision.RiskLevel, Status: CouncilOpen, Quorum: stage.Definition.Council.Quorum,
		ApprovalThreshold: stage.Definition.Council.ApprovalThreshold, Participants: participants,
		CreatedAt: stage.ActivatedAt, UpdatedAt: stage.ActivatedAt, ExpiresAt: stage.DeadlineAt,
	}, nil
}

func insertWorkflowCouncilVote(ctx context.Context, tx db.Tx, tenantID, councilID string, vote CouncilVote, idempotencyKey, fingerprint string) error {
	if strings.TrimSpace(councilID) == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(fingerprint) == "" {
		return ErrInvalidRequest
	}
	var status CouncilStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM hil_council_sessions WHERE tenant_id = ? AND id = ?`, tenantID, councilID).Scan(&status); err != nil {
		return err
	}
	if status != CouncilOpen {
		return ErrConflict
	}
	var participantCount, contributionCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM hil_council_participants WHERE tenant_id = ? AND council_id = ? AND actor_kind = ? AND actor_id = ?`, tenantID, councilID, vote.Actor.Kind, vote.Actor.ID).Scan(&participantCount); err != nil {
		return err
	}
	if participantCount == 0 {
		return ErrParticipantRequired
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM hil_council_contributions WHERE tenant_id = ? AND council_id = ? AND actor_kind = ? AND actor_id = ?`, tenantID, councilID, vote.Actor.Kind, vote.Actor.ID).Scan(&contributionCount); err != nil {
		return err
	}
	if contributionCount == 0 {
		return ErrContributionRequired
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO hil_council_votes (
	tenant_id, council_id, actor_kind, actor_id, choice, reason,
	idempotency_key, request_fingerprint, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, tenantID, councilID, vote.Actor.Kind, vote.Actor.ID,
		vote.Choice, vote.Reason, idempotencyKey, fingerprint, timeToInt(vote.CreatedAt))
	return err
}

func terminalWorkflowResolvedAt(status WorkflowInstanceStatus, resolvedAt time.Time) any {
	if status == WorkflowRunning {
		return nil
	}
	return timeToInt(resolvedAt)
}

func updateWorkflowTask(ctx context.Context, tx db.Tx, tenantID, instanceID string, task WorkflowTask) error {
	result, err := tx.ExecContext(ctx, `
UPDATE hil_workflow_tasks SET
	current_assignee_kind = ?, current_assignee_id = ?, status = ?, decision = ?, reason = ?,
	transfer_count = ?, transferred_from_id = ?, transferred_to_id = ?, transfer_reason = ?,
	deadline_at = ?, updated_at = ?, completed_at = ?
WHERE tenant_id = ? AND workflow_instance_id = ? AND id = ?`, task.CurrentAssignee.Kind,
		task.CurrentAssignee.ID, task.Status, task.Decision, task.Reason, task.TransferCount,
		task.TransferredFromID, task.TransferredToID, task.TransferReason, optionalTimeToInt(task.DeadlineAt),
		timeToInt(task.UpdatedAt), optionalTimeToInt(task.CompletedAt), tenantID, instanceID, task.ID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrConflict
	}
	return nil
}

func updateWorkflowStage(ctx context.Context, tx db.Tx, tenantID, instanceID string, stage WorkflowStageInstance) error {
	result, err := tx.ExecContext(ctx, `
UPDATE hil_workflow_stage_instances SET
	status = ?, deadline_at = ?, escalation_index = ?, activated_at = ?, resolved_at = ?
WHERE tenant_id = ? AND workflow_instance_id = ? AND id = ?`, stage.Status,
		optionalTimeToInt(stage.DeadlineAt), stage.EscalationIndex, optionalTimeToInt(stage.ActivatedAt),
		optionalTimeToInt(stage.ResolvedAt), tenantID, instanceID, stage.ID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrConflict
	}
	return nil
}

func insertWorkflowReceipt(ctx context.Context, tx db.Tx, receipt DecisionReceipt) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO hil_decision_receipts (
	id, tenant_id, request_id, council_id, workflow_instance_id, args_hash, material_hash,
	effect, resolver_actor_kind, resolver_actor_id, resolver_reason, resolver_summary_json,
	single_use, created_at, expires_at, used_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, receipt.ID, receipt.TenantID,
		receipt.RequestID, receipt.CouncilID, receipt.WorkflowInstanceID, receipt.ArgsHash,
		receipt.MaterialHash, receipt.Effect, receipt.ResolverActorKind, receipt.ResolverActorID,
		receipt.ResolverReason, mustJSON(receipt.ResolverSummary), receipt.SingleUse,
		timeToInt(receipt.CreatedAt), optionalTimeToInt(receipt.ExpiresAt), optionalTimeToInt(receipt.UsedAt))
	return err
}

func (r *PlatformRepository) ClaimDueWorkflowWork(ctx context.Context, tenantID string, now time.Time, limit int) ([]WorkflowDueItem, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || now.IsZero() {
		return nil, ErrInvalidRequest
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.store.Query(ctx, `
SELECT stage.workflow_instance_id, stage.id
FROM hil_workflow_stage_instances AS stage
JOIN hil_workflow_instances AS instance
	ON instance.tenant_id = stage.tenant_id AND instance.id = stage.workflow_instance_id
WHERE stage.tenant_id = ? AND stage.status = ? AND stage.deadline_at IS NOT NULL
	AND instance.status = ?
ORDER BY stage.deadline_at, stage.id`, tenantID, WorkflowStageActive, WorkflowRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type dueKey struct{ instanceID, stageID string }
	keys := []dueKey{}
	for rows.Next() {
		var key dueKey
		if err := rows.Scan(&key.instanceID, &key.stageID); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]WorkflowDueItem, 0, len(keys))
	for _, key := range keys {
		detail, err := r.GetWorkflowInstance(ctx, tenantID, key.instanceID)
		if err != nil {
			return nil, err
		}
		for _, stage := range detail.Stages {
			if stage.ID == key.stageID {
				if workflowStageNeedsSweep(detail, stage, now) {
					items = append(items, WorkflowDueItem{Instance: detail.Instance, Stage: stage})
				}
				break
			}
		}
		if len(items) >= limit {
			break
		}
	}
	return items, nil
}

func workflowStageNeedsSweep(detail WorkflowInstanceDetail, stage WorkflowStageInstance, now time.Time) bool {
	deadline := workflowEffectiveDeadline(detail, stage)
	if !now.Before(deadline) {
		return true
	}
	if stage.Definition.Type != WorkflowStageApproval || stage.Definition.Approval == nil {
		return false
	}
	existing := make(map[string]struct{}, len(detail.Notifications))
	assigned := make(map[string]struct{}, len(detail.Notifications))
	for _, notification := range detail.Notifications {
		existing[notification.IdempotencyKey] = struct{}{}
		if workflowAssignmentNotification(notification.EventType) {
			assigned[notification.TaskID] = struct{}{}
		}
	}
	for _, task := range detail.Tasks {
		if task.StageInstanceID != stage.ID || task.Status != TaskPending {
			continue
		}
		if _, found := assigned[task.ID]; !found {
			return true
		}
	}
	for _, offset := range stage.Definition.Approval.ReminderOffsetsMinutes {
		if offset <= 0 || now.Before(deadline.Add(-time.Duration(offset)*time.Minute)) {
			continue
		}
		baseKey := workflowReminderIdempotencyKey(detail.Instance.ID, stage.ID, offset)
		for _, task := range detail.Tasks {
			if task.StageInstanceID != stage.ID || task.Status != TaskPending {
				continue
			}
			if _, found := existing[baseKey+":task:"+task.ID]; !found {
				return true
			}
		}
	}
	return false
}

func workflowReminderIdempotencyKey(instanceID, stageID string, offset int) string {
	return fmt.Sprintf("workflow:%s:stage:%s:reminder:%d", instanceID, stageID, offset)
}

func (r *PlatformRepository) EnqueueWorkflowNotifications(ctx context.Context, notifications []WorkflowNotification) error {
	if r == nil || r.store == nil || len(notifications) == 0 {
		return ErrInvalidRequest
	}
	tenantID := notifications[0].TenantID
	if strings.TrimSpace(tenantID) == "" {
		return ErrInvalidRequest
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		for _, notification := range notifications {
			if notification.TenantID != tenantID || strings.TrimSpace(notification.WorkflowInstanceID) == "" || strings.TrimSpace(notification.IdempotencyKey) == "" {
				return ErrInvalidRequest
			}
			if err := insertWorkflowNotification(ctx, tx, notification); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *PlatformRepository) ClaimWorkflowNotifications(ctx context.Context, tenantID string, now time.Time, limit int) ([]WorkflowNotification, error) {
	if r == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || now.IsZero() {
		return nil, ErrInvalidRequest
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	claimed := []WorkflowNotification{}
	staleBefore := now.UTC().Add(-workflowNotificationProcessingTTL)
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		rows, err := tx.QueryContext(ctx, workflowNotificationSelectSQL()+`
WHERE tenant_id = ? AND (
	(status IN (?, ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
	OR (status = ? AND updated_at <= ?)
)
ORDER BY next_attempt_at, created_at, id LIMIT ?`, tenantID,
			WorkflowNotificationPending, WorkflowNotificationFailed, timeToInt(now),
			WorkflowNotificationProcessing, timeToInt(staleBefore), limit)
		if err != nil {
			return err
		}
		candidates, err := scanWorkflowNotificationRows(rows)
		rows.Close()
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			result, err := tx.ExecContext(ctx, `
UPDATE hil_workflow_notification_outbox
SET status = ?, attempt_count = attempt_count + 1, updated_at = ?
WHERE tenant_id = ? AND id = ? AND (
	status IN (?, ?) OR (status = ? AND updated_at <= ?)
)`, WorkflowNotificationProcessing, timeToInt(now), tenantID, candidate.ID,
				WorkflowNotificationPending, WorkflowNotificationFailed, WorkflowNotificationProcessing, timeToInt(staleBefore))
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected == 1 {
				candidate.Status = WorkflowNotificationProcessing
				candidate.AttemptCount++
				candidate.UpdatedAt = now.UTC()
				claimed = append(claimed, candidate)
			}
		}
		return nil
	})
	return claimed, err
}

func (r *PlatformRepository) CompleteWorkflowNotification(ctx context.Context, result WorkflowNotificationResult) error {
	terminalChoices := 0
	for _, chosen := range []bool{result.Delivered, result.Pending, result.Cancelled, result.NeedsAttention} {
		if chosen {
			terminalChoices++
		}
	}
	if r == nil || r.store == nil || strings.TrimSpace(result.TenantID) == "" || strings.TrimSpace(result.NotificationID) == "" || result.CompletedAt.IsZero() ||
		terminalChoices > 1 || ((result.Cancelled || result.NeedsAttention) && strings.TrimSpace(result.Failure) == "") ||
		(result.Delivered && (result.Pending || strings.TrimSpace(result.ProviderReceipt) == "")) ||
		(result.Pending && (strings.TrimSpace(result.ProviderReceipt) == "" || result.NextAttemptAt.IsZero())) {
		return ErrInvalidRequest
	}
	status := WorkflowNotificationFailed
	deliveredAt := any(nil)
	nextAttemptAt := optionalTimeToInt(result.NextAttemptAt)
	if result.Delivered {
		status = WorkflowNotificationDelivered
		deliveredAt = timeToInt(result.CompletedAt)
		nextAttemptAt = nil
	} else if result.Pending {
		status = WorkflowNotificationPending
	} else if result.Cancelled {
		status = WorkflowNotificationCancelled
		nextAttemptAt = nil
	} else if result.NeedsAttention {
		status = WorkflowNotificationNeedsAttention
		nextAttemptAt = nil
	}
	updateResult, err := r.store.Exec(ctx, `
UPDATE hil_workflow_notification_outbox
SET status = ?, provider_receipt = ?, last_error = ?, next_attempt_at = ?, updated_at = ?, delivered_at = ?
WHERE tenant_id = ? AND id = ? AND status = ?`, status, strings.TrimSpace(result.ProviderReceipt),
		strings.TrimSpace(result.Failure), nextAttemptAt, timeToInt(result.CompletedAt), deliveredAt,
		result.TenantID, result.NotificationID, WorkflowNotificationProcessing)
	if err != nil {
		return err
	}
	if affected, err := updateResult.RowsAffected(); err != nil || affected != 1 {
		return ErrConflict
	}
	return nil
}
