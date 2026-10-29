package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	lineageview "orrery_v2/internal/app/ontologyview/lineage"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
)

type productionSafeOntologyEventReader struct {
	store *eventstore.Store
}

func (r productionSafeOntologyEventReader) GetSafeOntologyEvent(ctx context.Context, tenantID, eventID string, expected contextgraph.AccessRef) (lineageview.SafeEvent, error) {
	if r.store == nil {
		return lineageview.SafeEvent{}, fmt.Errorf("safe ontology event store is required")
	}
	tenantID, eventID = strings.TrimSpace(tenantID), strings.TrimSpace(eventID)
	expected = contextgraph.NormalizeAccessRef(expected)
	if !validProductionLineageID(tenantID) || !validProductionLineageID(eventID) || !expected.Resolved() {
		return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
	}
	envelope, err := r.store.GetByID(ctx, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
	}
	if err != nil {
		return lineageview.SafeEvent{}, err
	}
	if !strings.HasPrefix(envelope.EventType, ontologyEventTypePrefix) {
		return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
	}
	eventType := strings.TrimSpace(strings.TrimPrefix(envelope.EventType, ontologyEventTypePrefix))
	if !validProductionLineageID(eventType) {
		return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
	}
	var metadata ontologyEventMetadata
	if len(envelope.Metadata) == 0 || json.Unmarshal(envelope.Metadata, &metadata) != nil {
		return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
	}
	if strings.TrimSpace(metadata.TenantID) != tenantID {
		return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
	}
	accessRef := contextgraph.NormalizeAccessRef(contextgraph.AccessRef{
		Kind: metadata.SourceAccessRef.Kind, ID: metadata.SourceAccessRef.ID, TenantID: metadata.SourceAccessRef.TenantID,
	})
	if !validProductionLineageAccessRef(accessRef) || !validProductionLineageAccessRef(expected) || accessRef != expected {
		return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
	}
	streamID := strings.TrimSpace(metadata.StreamID)
	if streamID == "" {
		streamID = strings.TrimPrefix(strings.TrimSpace(envelope.StreamID), ontologyEventTypePrefix)
	}
	version := metadata.Version
	if version == 0 {
		version = int64(envelope.StreamVersion)
	}
	if version <= 0 {
		return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
	}
	occurredAt := envelope.CreatedAt.UTC()
	if metadata.OccurredAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, metadata.OccurredAt)
		if err != nil {
			return lineageview.SafeEvent{}, lineageview.ErrSafeEventUnavailable
		}
		occurredAt = parsed.UTC()
	}
	return lineageview.SafeEvent{
		TenantID: tenantID, SourceAccessRef: accessRef,
		Metadata: lineageview.SafeEventMetadata{
			ID: envelope.EventID, Type: eventType, StreamID: streamID, Version: version,
			SchemaVersion: strings.TrimSpace(metadata.SchemaVersion), OccurredAt: visibleLineageTime(occurredAt),
		},
	}, nil
}

func validProductionLineageID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func validProductionLineageAccessRef(ref contextgraph.AccessRef) bool {
	ref = contextgraph.NormalizeAccessRef(ref)
	return ref.Resolved() && validProductionLineageID(ref.Kind) && validProductionLineageID(ref.ID) && validProductionLineageID(ref.TenantID)
}

func visibleLineageTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func newProductionOntologyLineageAPI(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	cursors *productionOntologyCursorState,
	concurrency *ontologycontract.ConcurrencyLimiter,
	policy *datapolicy.PlatformStore,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
) (*lineageview.Service, error) {
	if core == nil || records == nil || scopes == nil || scopes.Gate == nil || scopes.Resolver == nil || cursors == nil || cursors.Stateful == nil || concurrency == nil || policy == nil || authorizer == nil || knowledge == nil || memory == nil {
		return nil, fmt.Errorf("ontology lineage API dependencies are required")
	}
	access := productionContextAccessPolicy{
		scopes: scopes, authorizer: authorizer, knowledge: knowledge, memory: memory,
		knowledgeACL: knowledgeadmin.NewAccessPolicy(knowledge), records: records, actions: actioncontract.NewPlatformRepository(core), snapshots: scopes.Snapshots,
	}
	data := productionContextDataPolicy{provider: policy, revisions: authorization.NewPolicyRevisionSource(policy)}
	registrations := make([]datavisibility.Registration, 0, 12)
	for _, kind := range []datavisibility.ResourceKind{
		datavisibility.ResourceObject, datavisibility.ResourceFact, datavisibility.ResourceEvidence,
		datavisibility.ResourceEvent, datavisibility.ResourceAudit,
	} {
		registrations = append(registrations, datavisibility.Register(kind, access), datavisibility.Register(kind, data))
	}
	registrations = append(registrations,
		datavisibility.Register(datavisibility.ResourceDefinition, productionSchemaDefinitionPolicy{}),
		datavisibility.Register(datavisibility.ResourceDefinition, productionSchemaDataPolicy{provider: policy, revisions: authorization.NewPolicyRevisionSource(policy)}),
	)
	return lineageview.NewService(
		scopes.Gate,
		contextgraph.NewPlatformStore(core),
		productionSafeOntologyEventReader{store: eventstore.New(core)},
		ontology.NewPlatformRegistry(core),
		datavisibility.NewService(registrations...),
		lineageview.CursorSet{Encrypted: cursors.Encrypted, Stateful: cursors.Stateful},
		nil,
		concurrency,
	)
}
