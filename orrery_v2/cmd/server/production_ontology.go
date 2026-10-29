package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

func newProductionOntologyProjectionRunner(ctx context.Context, core *db.Store) (*ontology.MappingRuleProjectionRunner, error) {
	if core == nil {
		return nil, fmt.Errorf("core database is required for ontology projection runner")
	}
	ontologyRegistry := ontology.NewPlatformRegistry(core)
	gate := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry))
	projectionRegistry := graphprotocol.NewProjectionRegistry(gate)
	runner := ontology.NewMappingRuleProjectionRunner(
		projectionRegistry,
		gate,
		contextgraph.NewPlatformStore(core),
	)
	if err := runner.EnableRevisionRefresh(ctx, ontologyRegistry, ontology.NewPlatformMappingProjectionRefreshStore(core)); err != nil {
		return nil, err
	}
	return runner, nil
}

func newProductionOntologyMemoryRecordMetadataSink(ctx context.Context, core *db.Store) (memoryadmin.MemoryRecordMetadataSink, error) {
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		return nil, err
	}
	return newProductionOntologyMemoryRecordMetadataSinkWithRunner(core, runner)
}

func newProductionOntologyMemoryRecordMetadataSinkWithRunner(core *db.Store, runner *ontology.MappingRuleProjectionRunner) (memoryadmin.MemoryRecordMetadataSink, error) {
	if core == nil || runner == nil {
		return nil, fmt.Errorf("core database and shared ontology projection runner are required")
	}
	return productionMemoryRecordOntologySink{runner: runner, core: core, events: newProductionOntologyEventLog(core)}, nil
}

type productionMemoryRecordOntologySink struct {
	runner *ontology.MappingRuleProjectionRunner
	core   *db.Store
	events productionOntologyEventLog
}

type projectionAuditTarget struct {
	Field      string `json:"field,omitempty"`
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
}

func (s productionMemoryRecordOntologySink) PublishMemoryRecordMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	if s.runner == nil {
		return fmt.Errorf("%w: ontology metadata runner is required", memoryadmin.ErrServiceDependency)
	}
	if isPersonalAssistantMemoryEvent(event) {
		return nil
	}
	var err error
	event, err = s.canonicalizeLegacyMemoryScopeTenant(ctx, event)
	if err != nil {
		return err
	}
	event.SourceAccessRef = trustedOntologySourceRef("memory_record", event.StreamID, event.TenantID)
	if s.events.store != nil {
		if err := s.events.Record(ctx, event); err != nil {
			return err
		}
	}
	plan, err := s.prepareMemoryRecordMetadata(ctx, event)
	if err != nil {
		return err
	}
	return s.runner.ApplyPlan(ctx, plan)
}

func (s productionMemoryRecordOntologySink) canonicalizeLegacyMemoryScopeTenant(ctx context.Context, event graphprotocol.DomainEvent) (graphprotocol.DomainEvent, error) {
	if s.core == nil {
		return event, nil
	}
	tenantID := strings.TrimSpace(event.TenantID)
	parts := strings.SplitN(tenantID, ":", 2)
	if len(parts) != 2 || strings.Contains(parts[1], "/") {
		return event, nil
	}
	objectType := strings.TrimSpace(parts[0])
	objectID := strings.TrimSpace(parts[1])
	if (objectType != "project" && objectType != "mission") || objectID == "" {
		return event, nil
	}
	rows, err := s.core.Query(ctx, `
SELECT tenant_id
FROM context_graph_objects
WHERE object_type = ? AND id = ? AND access_state = 'resolved'
ORDER BY tenant_id ASC
LIMIT 2`, objectType, objectID)
	if err != nil {
		return graphprotocol.DomainEvent{}, err
	}
	defer rows.Close()
	candidates := make([]string, 0, 2)
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return graphprotocol.DomainEvent{}, err
		}
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	if err := rows.Err(); err != nil {
		return graphprotocol.DomainEvent{}, err
	}
	if len(candidates) > 1 {
		return graphprotocol.DomainEvent{}, fmt.Errorf("%w: legacy %s scope %s is ambiguous across tenants", memoryadmin.ErrInvalidRequest, objectType, objectID)
	}
	if len(candidates) == 1 {
		event.TenantID = candidates[0]
	}
	return event, nil
}

func (s productionMemoryRecordOntologySink) prepareMemoryRecordMetadata(ctx context.Context, event graphprotocol.DomainEvent) (ontology.MappingRuleProjectionPlan, error) {
	if event.Type == "memory.scope.linked" {
		filtered, ok, err := s.filterMemoryScopeLinkedEvent(ctx, event)
		if err != nil {
			return ontology.MappingRuleProjectionPlan{}, err
		}
		if !ok {
			return ontology.MappingRuleProjectionPlan{}, nil
		}
		event = filtered
	}
	if event.Type == "memory.lineage.linked" {
		ok, err := s.shouldApplyMemoryLineageLinkedEvent(ctx, event)
		if err != nil {
			return ontology.MappingRuleProjectionPlan{}, err
		}
		if !ok {
			return ontology.MappingRuleProjectionPlan{}, nil
		}
	}
	if isMemoryKnowledgeCitationEvent(event.Type) {
		ok, err := s.shouldApplyMemoryKnowledgeCitationEvent(ctx, event)
		if err != nil {
			return ontology.MappingRuleProjectionPlan{}, err
		}
		if !ok {
			return ontology.MappingRuleProjectionPlan{}, nil
		}
	}
	return s.runner.PrepareEvent(ctx, event)
}

func (s productionMemoryRecordOntologySink) computeProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit) (contextgraph.ProjectionAudit, error) {
	result, plan, err := s.prepareProjectionAuditReplay(ctx, audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	if err := s.runner.ApplyPlan(ctx, plan); err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	return result, nil
}

func (s productionMemoryRecordOntologySink) prepareProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit) (contextgraph.ProjectionAudit, ontology.MappingRuleProjectionPlan, error) {
	if s.core == nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, fmt.Errorf("core database is required for projection audit replay")
	}
	event, err := eventFromProjectionAudit(audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	resolved, err := s.projectionAuditTargetsResolved(ctx, audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	if !resolved {
		return audit, ontology.MappingRuleProjectionPlan{}, nil
	}
	plan, err := s.prepareMemoryRecordMetadata(ctx, event)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	audit.Status = "replayed"
	audit.Reason = "projection audit replay applied"
	audit.ObservedAt = time.Now().UTC()
	return audit, plan, nil
}

func (s productionMemoryRecordOntologySink) projectionAuditTargetsResolved(ctx context.Context, audit contextgraph.ProjectionAudit) (bool, error) {
	targets, err := projectionAuditReplayTargets(audit)
	if err != nil {
		return false, err
	}
	for _, target := range targets {
		exists, err := contextGraphObjectExists(ctx, s.core, audit.TenantID, target.ObjectType, target.ObjectID)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}
	return len(targets) > 0, nil
}

func projectionAuditReplayTargets(audit contextgraph.ProjectionAudit) ([]projectionAuditReplayTarget, error) {
	switch audit.SourceEventType {
	case "memory.scope.linked":
		memoryRecordID, ok := memoryRecordIDFromScopeAudit(audit)
		if !ok {
			return nil, fmt.Errorf("%w: projection audit %s missing memory_record_id", ontology.ErrInvalidDefinition, audit.ID)
		}
		targets := []projectionAuditReplayTarget{{ObjectType: "memory_record", ObjectID: memoryRecordID}}
		targets = append(targets, auditTargets(audit.Details["dropped_targets"])...)
		return targets, nil
	case "memory.lineage.linked":
		memoryRecordID, _ := audit.Details["memory_record_id"].(string)
		sourceMemoryRecordID, _ := audit.Details["source_memory_record_id"].(string)
		if memoryRecordID == "" || sourceMemoryRecordID == "" {
			return nil, fmt.Errorf("%w: projection audit %s missing lineage ids", ontology.ErrInvalidDefinition, audit.ID)
		}
		return []projectionAuditReplayTarget{
			{ObjectType: "memory_record", ObjectID: memoryRecordID},
			{ObjectType: "memory_record", ObjectID: sourceMemoryRecordID},
		}, nil
	case "memory.knowledge.citation.added", "memory.knowledge_chunk.citation.added", "memory.knowledge_anchor.citation.added":
		citation, ok := citationProjectionTarget(audit.SourceEventType)
		if !ok {
			return nil, fmt.Errorf("%w: projection audit %s event type %s is not replayable", ontology.ErrInvalidDefinition, audit.ID, audit.SourceEventType)
		}
		memoryRecordID, _ := audit.Details["memory_record_id"].(string)
		targetID, _ := audit.Details[citation.PayloadField].(string)
		if memoryRecordID == "" || targetID == "" {
			return nil, fmt.Errorf("%w: projection audit %s missing citation ids", ontology.ErrInvalidDefinition, audit.ID)
		}
		return []projectionAuditReplayTarget{
			{ObjectType: "memory_record", ObjectID: memoryRecordID},
			{ObjectType: citation.ObjectType, ObjectID: targetID},
		}, nil
	default:
		return nil, fmt.Errorf("%w: projection audit %s event type %s is not replayable", ontology.ErrInvalidDefinition, audit.ID, audit.SourceEventType)
	}
}

func eventFromProjectionAudit(audit contextgraph.ProjectionAudit) (graphprotocol.DomainEvent, error) {
	switch audit.SourceEventType {
	case "memory.scope.linked":
		memoryRecordID, ok := memoryRecordIDFromScopeAudit(audit)
		if !ok {
			return graphprotocol.DomainEvent{}, fmt.Errorf("%w: projection audit %s missing memory_record_id", ontology.ErrInvalidDefinition, audit.ID)
		}
		payload := map[string]any{"memory_record_id": memoryRecordID}
		for _, target := range auditTargets(audit.Details["dropped_targets"]) {
			switch target.Field {
			case "mission_id", "project_id", "task_id":
				payload[target.Field] = target.ObjectID
			}
		}
		return projectionAuditEvent(audit, memoryRecordID, payload), nil
	case "memory.lineage.linked":
		memoryRecordID, _ := audit.Details["memory_record_id"].(string)
		sourceMemoryRecordID, _ := audit.Details["source_memory_record_id"].(string)
		if memoryRecordID == "" || sourceMemoryRecordID == "" {
			return graphprotocol.DomainEvent{}, fmt.Errorf("%w: projection audit %s missing lineage ids", ontology.ErrInvalidDefinition, audit.ID)
		}
		return projectionAuditEvent(audit, memoryRecordID, map[string]any{
			"memory_record_id":        memoryRecordID,
			"source_memory_record_id": sourceMemoryRecordID,
		}), nil
	case "memory.knowledge.citation.added", "memory.knowledge_chunk.citation.added", "memory.knowledge_anchor.citation.added":
		citation, ok := citationProjectionTarget(audit.SourceEventType)
		if !ok {
			return graphprotocol.DomainEvent{}, fmt.Errorf("%w: projection audit %s event type %s is not replayable", ontology.ErrInvalidDefinition, audit.ID, audit.SourceEventType)
		}
		memoryRecordID, _ := audit.Details["memory_record_id"].(string)
		targetID, _ := audit.Details[citation.PayloadField].(string)
		if memoryRecordID == "" || targetID == "" {
			return graphprotocol.DomainEvent{}, fmt.Errorf("%w: projection audit %s missing citation ids", ontology.ErrInvalidDefinition, audit.ID)
		}
		return projectionAuditEvent(audit, memoryRecordID, map[string]any{
			"memory_record_id":    memoryRecordID,
			citation.PayloadField: targetID,
		}), nil
	default:
		return graphprotocol.DomainEvent{}, fmt.Errorf("%w: projection audit %s event type %s is not replayable", ontology.ErrInvalidDefinition, audit.ID, audit.SourceEventType)
	}
}

func projectionAuditEvent(audit contextgraph.ProjectionAudit, streamID string, payload map[string]any) graphprotocol.DomainEvent {
	occurredAt := audit.ObservedAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return graphprotocol.DomainEvent{
		ID:              audit.SourceEventID,
		TenantID:        audit.TenantID,
		Type:            audit.SourceEventType,
		StreamID:        streamID,
		Version:         1,
		OccurredAt:      occurredAt,
		Payload:         payload,
		PublishedBy:     graphprotocol.ActorRef{Kind: "service", ID: "projection-audit-replay"},
		SourceAccessRef: protocolAccessRef(audit.SourceAccessRef),
	}
}

func memoryRecordIDFromScopeAudit(audit contextgraph.ProjectionAudit) (string, bool) {
	const prefix = "memory-scope-linked:"
	if audit.SourceEventType != "memory.scope.linked" || len(audit.SourceEventID) <= len(prefix) || audit.SourceEventID[:len(prefix)] != prefix {
		return "", false
	}
	return audit.SourceEventID[len(prefix):], true
}

type projectionAuditReplayTarget struct {
	Field      string
	ObjectType string
	ObjectID   string
}

func auditTargets(raw any) []projectionAuditReplayTarget {
	rawTargets, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]projectionAuditReplayTarget, 0, len(rawTargets))
	for _, rawTarget := range rawTargets {
		target, ok := rawTarget.(map[string]any)
		if !ok {
			continue
		}
		field, _ := target["field"].(string)
		objectType, _ := target["object_type"].(string)
		objectID, _ := target["object_id"].(string)
		if objectID == "" {
			continue
		}
		out = append(out, projectionAuditReplayTarget{Field: field, ObjectType: objectType, ObjectID: objectID})
	}
	return out
}

func (s productionMemoryRecordOntologySink) filterMemoryScopeLinkedEvent(ctx context.Context, event graphprotocol.DomainEvent) (graphprotocol.DomainEvent, bool, error) {
	if s.core == nil {
		return event, false, fmt.Errorf("core database is required for memory scope ontology projection")
	}
	payload := cloneEventPayload(event.Payload)
	var droppedTargets []projectionAuditTarget
	for key, objectType := range map[string]string{
		"mission_id": "mission",
		"project_id": "project",
		"task_id":    "task",
	} {
		id, ok := payload[key].(string)
		if !ok || id == "" {
			delete(payload, key)
			continue
		}
		exists, err := contextGraphObjectExists(ctx, s.core, event.TenantID, objectType, id)
		if err != nil {
			return event, false, err
		}
		if !exists {
			droppedTargets = append(droppedTargets, projectionAuditTarget{Field: key, ObjectType: objectType, ObjectID: id})
			delete(payload, key)
		}
	}
	if len(droppedTargets) > 0 {
		if err := s.recordProjectionSkippedAudit(ctx, event, "missing-scope-targets", "missing_scope_target", "memory scope target object is not projected", map[string]any{
			"dropped_targets": projectionAuditTargets(droppedTargets),
		}); err != nil {
			return event, false, err
		}
	}
	if _, ok := payload["mission_id"]; !ok {
		if _, ok := payload["project_id"]; !ok {
			if _, ok := payload["task_id"]; !ok {
				return event, false, nil
			}
		}
	}
	event.Payload = payload
	return event, true, nil
}

func (s productionMemoryRecordOntologySink) shouldApplyMemoryLineageLinkedEvent(ctx context.Context, event graphprotocol.DomainEvent) (bool, error) {
	if s.core == nil {
		return false, fmt.Errorf("core database is required for memory lineage ontology projection")
	}
	memoryRecordID, ok := event.Payload["memory_record_id"].(string)
	if !ok || memoryRecordID == "" {
		return false, nil
	}
	sourceMemoryRecordID, ok := event.Payload["source_memory_record_id"].(string)
	if !ok || sourceMemoryRecordID == "" {
		return false, nil
	}
	var missingTargets []projectionAuditTarget
	for _, id := range []string{memoryRecordID, sourceMemoryRecordID} {
		exists, err := contextGraphObjectExists(ctx, s.core, event.TenantID, "memory_record", id)
		if err != nil {
			return false, err
		}
		if !exists {
			missingTargets = append(missingTargets, projectionAuditTarget{ObjectType: "memory_record", ObjectID: id})
		}
	}
	if len(missingTargets) > 0 {
		if err := s.recordProjectionSkippedAudit(ctx, event, "missing-lineage-targets", "missing_lineage_target", "memory lineage target object is not projected", map[string]any{
			"memory_record_id":        memoryRecordID,
			"source_memory_record_id": sourceMemoryRecordID,
			"missing_targets":         projectionAuditTargets(missingTargets),
		}); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

type citationProjectionTargetSpec struct {
	PayloadField string
	ObjectType   string
}

func isMemoryKnowledgeCitationEvent(eventType string) bool {
	_, ok := citationProjectionTarget(eventType)
	return ok
}

func citationProjectionTarget(eventType string) (citationProjectionTargetSpec, bool) {
	switch eventType {
	case "memory.knowledge.citation.added":
		return citationProjectionTargetSpec{PayloadField: "knowledge_document_id", ObjectType: "knowledge_document"}, true
	case "memory.knowledge_chunk.citation.added":
		return citationProjectionTargetSpec{PayloadField: "knowledge_chunk_id", ObjectType: "knowledge_chunk"}, true
	case "memory.knowledge_anchor.citation.added":
		return citationProjectionTargetSpec{PayloadField: "knowledge_anchor_id", ObjectType: "knowledge_anchor"}, true
	default:
		return citationProjectionTargetSpec{}, false
	}
}

func (s productionMemoryRecordOntologySink) shouldApplyMemoryKnowledgeCitationEvent(ctx context.Context, event graphprotocol.DomainEvent) (bool, error) {
	if s.core == nil {
		return false, fmt.Errorf("core database is required for memory citation ontology projection")
	}
	citation, ok := citationProjectionTarget(event.Type)
	if !ok {
		return false, nil
	}
	memoryRecordID, ok := event.Payload["memory_record_id"].(string)
	if !ok || memoryRecordID == "" {
		return false, nil
	}
	targetID, ok := event.Payload[citation.PayloadField].(string)
	if !ok || targetID == "" {
		return false, nil
	}
	targets := []projectionAuditTarget{
		{ObjectType: "memory_record", ObjectID: memoryRecordID},
		{ObjectType: citation.ObjectType, ObjectID: targetID},
	}
	var missingTargets []projectionAuditTarget
	for _, target := range targets {
		exists, err := contextGraphObjectExists(ctx, s.core, event.TenantID, target.ObjectType, target.ObjectID)
		if err != nil {
			return false, err
		}
		if !exists {
			missingTargets = append(missingTargets, target)
		}
	}
	if len(missingTargets) > 0 {
		if err := s.recordProjectionSkippedAudit(ctx, event, "missing-citation-target", "missing_citation_target", "memory citation target object is not projected", map[string]any{
			"memory_record_id":    memoryRecordID,
			citation.PayloadField: targetID,
			"missing_targets":     projectionAuditTargets(missingTargets),
		}); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func (s productionMemoryRecordOntologySink) recordProjectionSkippedAudit(ctx context.Context, event graphprotocol.DomainEvent, suffix string, reasonCode string, reason string, details map[string]any) error {
	if s.core == nil {
		return fmt.Errorf("core database is required for projection audit")
	}
	observedAt := event.OccurredAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return contextgraph.NewPlatformStore(s.core).SaveProjectionAudit(ctx, contextgraph.ProjectionAudit{
		ID:              "projection-skipped:" + event.ID + ":" + suffix,
		TenantID:        event.TenantID,
		SourceEventID:   event.ID,
		SourceEventType: event.Type,
		Status:          "skipped",
		ReasonCode:      reasonCode,
		Reason:          reason,
		Details:         details,
		SourceAccessRef: contextGraphAccessRef(event.SourceAccessRef),
		ObservedAt:      observedAt,
	})
}

func projectionAuditTargets(targets []projectionAuditTarget) []any {
	out := make([]any, 0, len(targets))
	for _, target := range targets {
		item := map[string]any{
			"object_type": target.ObjectType,
			"object_id":   target.ObjectID,
		}
		if target.Field != "" {
			item["field"] = target.Field
		}
		out = append(out, item)
	}
	return out
}

func cloneEventPayload(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		out[key] = value
	}
	return out
}

func contextGraphObjectExists(ctx context.Context, store *db.Store, tenantID string, objectType string, id string) (bool, error) {
	var found string
	err := store.QueryRow(ctx, `SELECT id FROM context_graph_objects WHERE tenant_id = ? AND object_type = ? AND id = ?`, tenantID, objectType, id).Scan(&found)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("check context graph object %s %s:%s: %w", tenantID, objectType, id, err)
}

type productionDocumentOntologySink struct {
	runner         *ontology.MappingRuleProjectionRunner
	core           *db.Store
	events         productionOntologyEventLog
	tenantResolver knowledgeOntologyEventTenantResolver
}

func newProductionOntologyDocumentMetadataSink(ctx context.Context, core *db.Store) (productionDocumentOntologySink, error) {
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		return productionDocumentOntologySink{}, err
	}
	return newProductionOntologyDocumentMetadataSinkWithRunner(core, runner)
}

func newProductionOntologyDocumentMetadataSinkWithRunner(core *db.Store, runner *ontology.MappingRuleProjectionRunner) (productionDocumentOntologySink, error) {
	if core == nil || runner == nil {
		return productionDocumentOntologySink{}, fmt.Errorf("core database and shared ontology projection runner are required")
	}
	return productionDocumentOntologySink{runner: runner, core: core, events: newProductionOntologyEventLog(core)}, nil
}

func (s productionDocumentOntologySink) PublishKnowledgeDocumentMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.publishTrustedOntologyEvent(ctx, event, "knowledge_document")
}

func (s productionDocumentOntologySink) PublishKnowledgeDocumentStatus(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.publishTrustedOntologyEvent(ctx, event, "knowledge_document")
}

func (s productionDocumentOntologySink) PublishKnowledgeChunkMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.publishTrustedOntologyEvent(ctx, event, "knowledge_chunk")
}

func (s productionDocumentOntologySink) PublishKnowledgePageMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.publishTrustedOntologyEvent(ctx, event, "knowledge_document")
}

func (s productionDocumentOntologySink) PublishKnowledgeAnchorMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.publishTrustedOntologyEvent(ctx, event, "knowledge_document")
}

func (s productionDocumentOntologySink) publishTrustedOntologyEvent(ctx context.Context, event graphprotocol.DomainEvent, sourceKind string) error {
	if s.tenantResolver != nil {
		tenantID, supported, err := s.tenantResolver.ResolveEventTenant(ctx, event)
		if err != nil {
			return err
		}
		if supported {
			event.TenantID = tenantID
		}
	}
	event.SourceAccessRef = trustedOntologySourceRef(sourceKind, event.StreamID, event.TenantID)
	return s.publishOntologyEvent(ctx, event)
}

func (s productionDocumentOntologySink) computeProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit) (contextgraph.ProjectionAudit, error) {
	result, plan, err := s.prepareProjectionAuditReplay(ctx, audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	if err := s.runner.ApplyPlan(ctx, plan); err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	return result, nil
}

func (s productionDocumentOntologySink) prepareProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit) (contextgraph.ProjectionAudit, ontology.MappingRuleProjectionPlan, error) {
	if s.core == nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, fmt.Errorf("core database is required for projection audit replay")
	}
	if s.events.store == nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, fmt.Errorf("ontology event store is required for projection audit replay")
	}
	if audit.ReasonCode != "missing_knowledge_projection_target" {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, fmt.Errorf("%w: projection audit %s reason code %s is not replayable for knowledge", ontology.ErrInvalidDefinition, audit.ID, audit.ReasonCode)
	}
	event, ok, err := s.knowledgeEventFromProjectionAudit(ctx, audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	if !ok {
		return audit, ontology.MappingRuleProjectionPlan{}, nil
	}
	resolved, err := s.knowledgeProjectionAuditTargetResolved(ctx, audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	if !resolved {
		return audit, ontology.MappingRuleProjectionPlan{}, nil
	}
	plan, ok, err := s.prepareKnowledgeOntologyEvent(ctx, event)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	if !ok {
		return audit, ontology.MappingRuleProjectionPlan{}, nil
	}
	audit.Status = "replayed"
	audit.Reason = "knowledge projection audit replay applied"
	audit.ObservedAt = time.Now().UTC()
	return audit, plan, nil
}

func (s productionDocumentOntologySink) publishOntologyEvent(ctx context.Context, event graphprotocol.DomainEvent) error {
	if s.runner == nil {
		return fmt.Errorf("%w: ontology metadata runner is required", knowledgeadmin.ErrServiceDependency)
	}
	if s.events.store != nil {
		if err := s.events.Record(ctx, event); err != nil {
			return err
		}
	}
	plan, ok, err := s.prepareKnowledgeOntologyEvent(ctx, event)
	if err != nil || !ok {
		return err
	}
	return s.runner.ApplyPlan(ctx, plan)
}

func (s productionDocumentOntologySink) prepareKnowledgeOntologyEvent(ctx context.Context, event graphprotocol.DomainEvent) (ontology.MappingRuleProjectionPlan, bool, error) {
	ok, err := s.shouldApplyKnowledgeMetadataEvent(ctx, event)
	if err != nil {
		return ontology.MappingRuleProjectionPlan{}, false, err
	}
	if !ok {
		return ontology.MappingRuleProjectionPlan{}, false, nil
	}
	plan, err := s.runner.PrepareEvent(ctx, event)
	return plan, err == nil, err
}

func (s productionDocumentOntologySink) knowledgeEventFromProjectionAudit(ctx context.Context, audit contextgraph.ProjectionAudit) (graphprotocol.DomainEvent, bool, error) {
	event, err := s.events.Get(ctx, audit.SourceEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return graphprotocol.DomainEvent{}, false, nil
	}
	if err != nil {
		return graphprotocol.DomainEvent{}, false, err
	}
	eventRef := contextgraph.NormalizeAccessRef(contextgraph.AccessRef{
		Kind: event.SourceAccessRef.Kind, ID: event.SourceAccessRef.ID, TenantID: event.SourceAccessRef.TenantID,
	})
	auditRef := contextgraph.NormalizeAccessRef(audit.SourceAccessRef)
	if event.ID != audit.SourceEventID || event.TenantID != audit.TenantID || event.Type != audit.SourceEventType ||
		!eventRef.Resolved() || eventRef != auditRef {
		return graphprotocol.DomainEvent{}, false, nil
	}
	return event, true, nil
}

func (s productionDocumentOntologySink) knowledgeProjectionAuditTargetResolved(ctx context.Context, audit contextgraph.ProjectionAudit) (bool, error) {
	targets := auditTargets(audit.Details["missing_targets"])
	for _, target := range targets {
		exists, err := contextGraphObjectExists(ctx, s.core, audit.TenantID, target.ObjectType, target.ObjectID)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}
	return len(targets) > 0, nil
}

type knowledgeProjectionTargetSpec struct {
	PayloadField string
	ObjectType   string
}

func knowledgeProjectionTarget(eventType string) (knowledgeProjectionTargetSpec, bool) {
	switch eventType {
	case "knowledge.document.status.updated":
		return knowledgeProjectionTargetSpec{PayloadField: "id", ObjectType: "knowledge_document"}, true
	case "knowledge.chunk.metadata.updated", "knowledge.page.metadata.updated":
		return knowledgeProjectionTargetSpec{PayloadField: "document_id", ObjectType: "knowledge_document"}, true
	case "knowledge.anchor.metadata.updated":
		return knowledgeProjectionTargetSpec{PayloadField: "page_id", ObjectType: "knowledge_page"}, true
	default:
		return knowledgeProjectionTargetSpec{}, false
	}
}

func (s productionDocumentOntologySink) shouldApplyKnowledgeMetadataEvent(ctx context.Context, event graphprotocol.DomainEvent) (bool, error) {
	target, ok := knowledgeProjectionTarget(event.Type)
	if !ok {
		return true, nil
	}
	if s.core == nil {
		return false, fmt.Errorf("core database is required for knowledge ontology projection")
	}
	objectID, _ := event.Payload["id"].(string)
	targetID, ok := event.Payload[target.PayloadField].(string)
	if !ok || targetID == "" {
		return true, nil
	}
	exists, err := contextGraphObjectExists(ctx, s.core, event.TenantID, target.ObjectType, targetID)
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}
	if err := s.recordKnowledgeProjectionSkippedAudit(ctx, event, map[string]any{
		"object_id":         objectID,
		target.PayloadField: targetID,
		"missing_targets":   projectionAuditTargets([]projectionAuditTarget{{Field: target.PayloadField, ObjectType: target.ObjectType, ObjectID: targetID}}),
	}); err != nil {
		return false, err
	}
	return false, nil
}

func (s productionDocumentOntologySink) recordKnowledgeProjectionSkippedAudit(ctx context.Context, event graphprotocol.DomainEvent, details map[string]any) error {
	if s.core == nil {
		return fmt.Errorf("core database is required for projection audit")
	}
	observedAt := event.OccurredAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return contextgraph.NewPlatformStore(s.core).SaveProjectionAudit(ctx, contextgraph.ProjectionAudit{
		ID:              "projection-skipped:" + event.ID + ":missing-knowledge-projection-target",
		TenantID:        event.TenantID,
		SourceEventID:   event.ID,
		SourceEventType: event.Type,
		Status:          "skipped",
		ReasonCode:      "missing_knowledge_projection_target",
		Reason:          "knowledge projection target object is not projected",
		Details:         details,
		SourceAccessRef: contextGraphAccessRef(event.SourceAccessRef),
		ObservedAt:      observedAt,
	})
}
