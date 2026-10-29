package contextgraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformStore struct {
	store *db.Store
	tx    db.Tx
}

func NewPlatformStore(store *db.Store) *PlatformStore {
	return &PlatformStore{store: store}
}

func NewPlatformStoreTx(tx db.Tx) *PlatformStore {
	return &PlatformStore{tx: tx}
}

func (s *PlatformStore) ready() bool {
	return s != nil && (s.store != nil || s.tx != nil)
}

func (s *PlatformStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if !s.ready() {
		return nil, fmt.Errorf("context graph platform store is required")
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if !s.ready() {
		return nil, fmt.Errorf("context graph platform store is required")
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformStore) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformStore) SaveObject(ctx context.Context, object Object) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if s.tx == nil {
		return s.store.WithTx(ctx, func(tx db.Tx) error {
			return NewPlatformStoreTx(tx).saveObject(ctx, object)
		})
	}
	return s.saveObject(ctx, object)
}

func (s *PlatformStore) EnsureObjectReference(ctx context.Context, object Object) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if s.tx == nil {
		return s.store.WithTx(ctx, func(tx db.Tx) error {
			return NewPlatformStoreTx(tx).ensureObjectReference(ctx, object)
		})
	}
	return s.ensureObjectReference(ctx, object)
}

func (s *PlatformStore) ensureObjectReference(ctx context.Context, object Object) error {
	if strings.TrimSpace(object.TenantID) == "" || strings.TrimSpace(object.ID) == "" || strings.TrimSpace(object.ObjectType) == "" {
		return fmt.Errorf("object reference tenant id, id, and type are required")
	}
	object.OwnerAccessRef = normalizedAccessRefOrEmpty(object.OwnerAccessRef)
	object.AccessState = accessStateFor(object.OwnerAccessRef)
	object.Temporal = canonicalTemporalMetadata(object.Temporal, object.UpdatedAt)
	_, err := s.exec(ctx, `
INSERT INTO context_graph_objects (
	id, tenant_id, object_type, canonical_key, label, properties_json,
	sensitivity, confidence, status, provenance_json, evidence_refs_json,
	owner_access_kind, owner_access_id, owner_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`,
		object.ID,
		object.TenantID,
		object.ObjectType,
		object.CanonicalKey,
		object.Label,
		mustGraphJSON(defaultGraphMap(object.Properties)),
		object.Sensitivity,
		object.Confidence,
		object.Status,
		mustGraphJSON(object.Provenance),
		mustGraphJSON(object.EvidenceRefs),
		object.OwnerAccessRef.Kind,
		object.OwnerAccessRef.ID,
		object.OwnerAccessRef.TenantID,
		object.AccessState,
		object.SourceStreamID,
		object.SourceVersion,
		mustGraphJSON(object.Temporal),
		timeToUnixNano(object.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("ensure context graph object reference: %w", err)
	}
	return nil
}

func (s *PlatformStore) saveObject(ctx context.Context, object Object) error {
	if strings.TrimSpace(object.TenantID) == "" || strings.TrimSpace(object.ID) == "" {
		return fmt.Errorf("object tenant id and id are required")
	}
	ownerAccessRef, accessState, err := s.accessRefForWrite(ctx, "context_graph_objects", object.TenantID, object.ID, object.OwnerAccessRef)
	if err != nil {
		return err
	}
	object.OwnerAccessRef = ownerAccessRef
	object.AccessState = accessState
	object.Temporal = canonicalTemporalMetadata(object.Temporal, object.UpdatedAt)
	result, err := s.exec(ctx, `
INSERT INTO context_graph_objects (
	id, tenant_id, object_type, canonical_key, label, properties_json,
	sensitivity, confidence, status, provenance_json, evidence_refs_json,
	owner_access_kind, owner_access_id, owner_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO UPDATE SET
	object_type = excluded.object_type,
	canonical_key = excluded.canonical_key,
	label = excluded.label,
	properties_json = excluded.properties_json,
	sensitivity = excluded.sensitivity,
	confidence = excluded.confidence,
	status = excluded.status,
	provenance_json = excluded.provenance_json,
	evidence_refs_json = excluded.evidence_refs_json,
	owner_access_kind = excluded.owner_access_kind,
	owner_access_id = excluded.owner_access_id,
	owner_access_tenant_id = excluded.owner_access_tenant_id,
	access_state = excluded.access_state,
	source_stream_id = excluded.source_stream_id,
	source_version = excluded.source_version,
	temporal_json = excluded.temporal_json,
	updated_at = excluded.updated_at
	WHERE (CASE
		WHEN excluded.source_stream_id <> '' AND excluded.source_stream_id = context_graph_objects.source_stream_id AND excluded.source_version > 0 AND context_graph_objects.source_version > 0
		THEN excluded.source_version >= context_graph_objects.source_version
		ELSE excluded.updated_at >= context_graph_objects.updated_at
	END) AND (
		context_graph_objects.access_state = 'unresolved' OR (
			context_graph_objects.access_state = 'resolved' AND
			context_graph_objects.owner_access_kind = excluded.owner_access_kind AND
			context_graph_objects.owner_access_id = excluded.owner_access_id AND
			context_graph_objects.owner_access_tenant_id = excluded.owner_access_tenant_id
		)
	)`,
		object.ID,
		object.TenantID,
		object.ObjectType,
		object.CanonicalKey,
		object.Label,
		mustGraphJSON(defaultGraphMap(object.Properties)),
		object.Sensitivity,
		object.Confidence,
		object.Status,
		mustGraphJSON(object.Provenance),
		mustGraphJSON(object.EvidenceRefs),
		object.OwnerAccessRef.Kind,
		object.OwnerAccessRef.ID,
		object.OwnerAccessRef.TenantID,
		object.AccessState,
		object.SourceStreamID,
		object.SourceVersion,
		mustGraphJSON(object.Temporal),
		timeToUnixNano(object.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save context graph object: %w", err)
	}
	if err := s.checkAccessProtectedWrite(ctx, result, "context_graph_objects", object.TenantID, object.ID, object.OwnerAccessRef); err != nil {
		return err
	}
	if !keepsTemporalHistory(object.Temporal) {
		return nil
	}
	return s.saveTemporalResourceVersion(ctx, objectTemporalVersion(object), object.OwnerAccessRef, object.AccessState)
}

func (s *PlatformStore) SaveLink(ctx context.Context, link Link) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if s.tx == nil {
		return s.store.WithTx(ctx, func(tx db.Tx) error {
			return NewPlatformStoreTx(tx).saveLink(ctx, link)
		})
	}
	return s.saveLink(ctx, link)
}

func (s *PlatformStore) DeleteLink(ctx context.Context, tenantID, linkID, sourceStreamID string) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	tenantID, linkID, sourceStreamID = strings.TrimSpace(tenantID), strings.TrimSpace(linkID), strings.TrimSpace(sourceStreamID)
	if tenantID == "" || linkID == "" || sourceStreamID == "" {
		return fmt.Errorf("link tenant id, id, and source stream id are required")
	}
	if s.tx == nil {
		return s.store.WithTx(ctx, func(tx db.Tx) error {
			return NewPlatformStoreTx(tx).DeleteLink(ctx, tenantID, linkID, sourceStreamID)
		})
	}
	_, err := s.exec(ctx, `DELETE FROM context_graph_links WHERE tenant_id = ? AND id = ? AND source_stream_id = ?`, tenantID, linkID, sourceStreamID)
	if err != nil {
		return fmt.Errorf("delete context graph link: %w", err)
	}
	return nil
}

func (s *PlatformStore) saveLink(ctx context.Context, link Link) error {
	if strings.TrimSpace(link.TenantID) == "" || strings.TrimSpace(link.ID) == "" {
		return fmt.Errorf("link tenant id and id are required")
	}
	sourceAccessRef, accessState, err := s.accessRefForWrite(ctx, "context_graph_links", link.TenantID, link.ID, link.SourceAccessRef)
	if err != nil {
		return err
	}
	link.SourceAccessRef = sourceAccessRef
	link.AccessState = accessState
	link.Temporal = canonicalTemporalMetadata(link.Temporal, link.UpdatedAt)
	result, err := s.exec(ctx, `
INSERT INTO context_graph_links (
	id, tenant_id, link_type, from_object_id, to_object_id, properties_json,
	confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO UPDATE SET
	link_type = excluded.link_type,
	from_object_id = excluded.from_object_id,
	to_object_id = excluded.to_object_id,
	properties_json = excluded.properties_json,
	confidence = excluded.confidence,
	status = excluded.status,
	provenance_json = excluded.provenance_json,
	evidence_refs_json = excluded.evidence_refs_json,
	source_access_kind = excluded.source_access_kind,
	source_access_id = excluded.source_access_id,
	source_access_tenant_id = excluded.source_access_tenant_id,
	access_state = excluded.access_state,
	source_stream_id = excluded.source_stream_id,
	source_version = excluded.source_version,
	temporal_json = excluded.temporal_json,
	updated_at = excluded.updated_at
	WHERE (CASE
		WHEN excluded.source_stream_id <> '' AND excluded.source_stream_id = context_graph_links.source_stream_id AND excluded.source_version > 0 AND context_graph_links.source_version > 0
		THEN excluded.source_version >= context_graph_links.source_version
		ELSE excluded.updated_at >= context_graph_links.updated_at
	END) AND (
		context_graph_links.access_state = 'unresolved' OR (
			context_graph_links.access_state = 'resolved' AND
			context_graph_links.source_access_kind = excluded.source_access_kind AND
			context_graph_links.source_access_id = excluded.source_access_id AND
			context_graph_links.source_access_tenant_id = excluded.source_access_tenant_id
		)
	)`,
		link.ID,
		link.TenantID,
		link.LinkType,
		link.FromObjectID,
		link.ToObjectID,
		mustGraphJSON(defaultGraphMap(link.Properties)),
		link.Confidence,
		link.Status,
		mustGraphJSON(link.Provenance),
		mustGraphJSON(link.EvidenceRefs),
		link.SourceAccessRef.Kind,
		link.SourceAccessRef.ID,
		link.SourceAccessRef.TenantID,
		link.AccessState,
		link.SourceStreamID,
		link.SourceVersion,
		mustGraphJSON(link.Temporal),
		timeToUnixNano(link.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save context graph link: %w", err)
	}
	if err := s.checkAccessProtectedWrite(ctx, result, "context_graph_links", link.TenantID, link.ID, link.SourceAccessRef); err != nil {
		return err
	}
	if !keepsTemporalHistory(link.Temporal) {
		return nil
	}
	return s.saveTemporalResourceVersion(ctx, linkTemporalVersion(link), link.SourceAccessRef, link.AccessState)
}

func (s *PlatformStore) SaveFact(ctx context.Context, fact Fact) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if s.tx == nil {
		return s.store.WithTx(ctx, func(tx db.Tx) error {
			return NewPlatformStoreTx(tx).saveFact(ctx, fact)
		})
	}
	return s.saveFact(ctx, fact)
}

func (s *PlatformStore) saveFact(ctx context.Context, fact Fact) error {
	if strings.TrimSpace(fact.TenantID) == "" || strings.TrimSpace(fact.ID) == "" {
		return fmt.Errorf("fact tenant id and id are required")
	}
	sourceAccessRef, accessState, err := s.accessRefForWrite(ctx, "context_graph_facts", fact.TenantID, fact.ID, fact.SourceAccessRef)
	if err != nil {
		return err
	}
	fact.SourceAccessRef = sourceAccessRef
	fact.AccessState = accessState
	fact.Temporal = canonicalTemporalMetadata(fact.Temporal, fact.UpdatedAt)
	if err := s.saveFactAssertion(ctx, fact); err != nil {
		return err
	}
	result, err := s.exec(ctx, `
INSERT INTO context_graph_facts (
	id, tenant_id, subject_kind, subject_id, predicate, value_json, properties_json,
	confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO UPDATE SET
	subject_kind = excluded.subject_kind,
	subject_id = excluded.subject_id,
	predicate = excluded.predicate,
	value_json = excluded.value_json,
	properties_json = excluded.properties_json,
	confidence = excluded.confidence,
	status = excluded.status,
	provenance_json = excluded.provenance_json,
	evidence_refs_json = excluded.evidence_refs_json,
	source_access_kind = excluded.source_access_kind,
	source_access_id = excluded.source_access_id,
	source_access_tenant_id = excluded.source_access_tenant_id,
	access_state = excluded.access_state,
	source_stream_id = excluded.source_stream_id,
	source_version = excluded.source_version,
	temporal_json = excluded.temporal_json,
	updated_at = excluded.updated_at
	WHERE (CASE
		WHEN excluded.source_stream_id <> '' AND excluded.source_stream_id = context_graph_facts.source_stream_id AND excluded.source_version > 0 AND context_graph_facts.source_version > 0
		THEN excluded.source_version >= context_graph_facts.source_version
		ELSE excluded.updated_at >= context_graph_facts.updated_at
	END) AND (
		context_graph_facts.access_state = 'unresolved' OR (
			context_graph_facts.access_state = 'resolved' AND
			context_graph_facts.source_access_kind = excluded.source_access_kind AND
			context_graph_facts.source_access_id = excluded.source_access_id AND
			context_graph_facts.source_access_tenant_id = excluded.source_access_tenant_id
		)
	)`,
		fact.ID,
		fact.TenantID,
		fact.Subject.Kind,
		fact.Subject.ID,
		fact.Predicate,
		mustGraphJSON(fact.Value),
		mustGraphJSON(defaultGraphMap(fact.Properties)),
		fact.Confidence,
		fact.Status,
		mustGraphJSON(fact.Provenance),
		mustGraphJSON(fact.EvidenceRefs),
		fact.SourceAccessRef.Kind,
		fact.SourceAccessRef.ID,
		fact.SourceAccessRef.TenantID,
		fact.AccessState,
		fact.SourceStreamID,
		fact.SourceVersion,
		mustGraphJSON(fact.Temporal),
		timeToUnixNano(fact.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save context graph fact: %w", err)
	}
	if err := s.checkAccessProtectedWrite(ctx, result, "context_graph_facts", fact.TenantID, fact.ID, fact.SourceAccessRef); err != nil {
		return err
	}
	if !keepsTemporalHistory(fact.Temporal) {
		return nil
	}
	return s.saveTemporalResourceVersion(ctx, factTemporalVersion(fact), fact.SourceAccessRef, fact.AccessState)
}

func (s *PlatformStore) saveFactAssertion(ctx context.Context, fact Fact) error {
	staticClaim := !keepsTemporalHistory(fact.Temporal)
	assertionID := ""
	if staticClaim {
		var err error
		assertionID, err = staticFactClaimIdentity(fact)
		if err != nil {
			return err
		}
	} else {
		identity := strings.Join([]string{fact.TenantID, fact.ID, fact.Provenance.Kind, fact.Provenance.ID, fact.SourceStreamID, fmt.Sprintf("%d", fact.SourceVersion), fact.UpdatedAt.UTC().Format(time.RFC3339Nano)}, "\x1f")
		assertionID = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(identity)))
	}
	conflictClause := `ON CONFLICT(tenant_id, assertion_id) DO NOTHING`
	if staticClaim {
		conflictClause = `ON CONFLICT(tenant_id, assertion_id) DO UPDATE SET
		fact_id = excluded.fact_id,
		subject_kind = excluded.subject_kind,
		subject_id = excluded.subject_id,
		predicate = excluded.predicate,
		value_json = excluded.value_json,
		properties_json = excluded.properties_json,
		confidence = excluded.confidence,
		status = excluded.status,
		provenance_json = excluded.provenance_json,
		evidence_refs_json = excluded.evidence_refs_json,
		source_access_kind = excluded.source_access_kind,
		source_access_id = excluded.source_access_id,
		source_access_tenant_id = excluded.source_access_tenant_id,
		access_state = excluded.access_state,
		source_stream_id = excluded.source_stream_id,
		source_version = excluded.source_version,
		temporal_json = excluded.temporal_json,
		observed_at = excluded.observed_at
	WHERE CASE
		WHEN excluded.source_stream_id <> ''
			AND excluded.source_stream_id = context_graph_fact_assertions.source_stream_id
			AND excluded.source_version > 0
			AND context_graph_fact_assertions.source_version > 0
		THEN excluded.source_version > context_graph_fact_assertions.source_version
		ELSE excluded.observed_at > context_graph_fact_assertions.observed_at
	END`
	}
	_, err := s.exec(ctx, `
INSERT INTO context_graph_fact_assertions (
	assertion_id, tenant_id, fact_id, subject_kind, subject_id, predicate, value_json,
	properties_json, confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`+conflictClause,
		assertionID,
		fact.TenantID,
		fact.ID,
		fact.Subject.Kind,
		fact.Subject.ID,
		fact.Predicate,
		mustGraphJSON(fact.Value),
		mustGraphJSON(defaultGraphMap(fact.Properties)),
		fact.Confidence,
		fact.Status,
		mustGraphJSON(fact.Provenance),
		mustGraphJSON(fact.EvidenceRefs),
		fact.SourceAccessRef.Kind,
		fact.SourceAccessRef.ID,
		fact.SourceAccessRef.TenantID,
		fact.AccessState,
		fact.SourceStreamID,
		fact.SourceVersion,
		mustGraphJSON(fact.Temporal),
		timeToUnixNano(fact.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save context graph fact assertion: %w", err)
	}
	return nil
}

func staticFactClaimIdentity(fact Fact) (string, error) {
	sourceStreamID := strings.TrimSpace(fact.SourceStreamID)
	provenanceKind, provenanceID := "", ""
	if sourceStreamID == "" {
		provenanceKind = strings.TrimSpace(fact.Provenance.Kind)
		provenanceID = strings.TrimSpace(fact.Provenance.ID)
		if provenanceKind == "" || provenanceID == "" {
			return "", fmt.Errorf("static fact %q requires a source stream or provenance source", fact.ID)
		}
	}
	identity := strings.Join([]string{
		"context-graph-static-fact-claim:v2",
		fact.TenantID,
		fact.ID,
		sourceStreamID,
		provenanceKind,
		provenanceID,
		fact.SourceAccessRef.Kind,
		fact.SourceAccessRef.ID,
		fact.SourceAccessRef.TenantID,
	}, "\x1f")
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(identity))), nil
}

func (s *PlatformStore) saveTemporalResourceVersion(ctx context.Context, version temporalResourceVersion, accessRef AccessRef, accessState AccessState) error {
	var tenantID, sourceStreamID string
	var sourceVersion int64
	switch {
	case version.Object != nil:
		tenantID, sourceStreamID, sourceVersion = version.Object.TenantID, version.Object.SourceStreamID, version.Object.SourceVersion
	case version.Link != nil:
		tenantID, sourceStreamID, sourceVersion = version.Link.TenantID, version.Link.SourceStreamID, version.Link.SourceVersion
	case version.Fact != nil:
		tenantID, sourceStreamID, sourceVersion = version.Fact.TenantID, version.Fact.SourceStreamID, version.Fact.SourceVersion
	default:
		return fmt.Errorf("unsupported temporal context graph resource %q", version.Kind)
	}
	assertionID, payloadJSON, err := temporalResourceVersionIdentity(version, accessRef, accessState)
	if err != nil {
		return err
	}
	version.AssertionID = assertionID
	_, err = s.exec(ctx, `
INSERT INTO context_graph_resource_versions (
	tenant_id, resource_kind, resource_id, assertion_id, payload_json,
	temporal_mode, business_valid_from, business_valid_to,
	source_recorded_at, source_superseded_at, orrery_ingested_at,
	access_kind, access_id, access_tenant_id, access_state,
	source_stream_id, source_version, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, resource_kind, assertion_id) DO NOTHING`,
		tenantID,
		version.Kind,
		version.ID,
		version.AssertionID,
		payloadJSON,
		version.Temporal.Mode,
		timeToUnixNano(version.Temporal.BusinessValidFrom),
		optionalTimeToUnixNano(version.Temporal.BusinessValidTo),
		timeToUnixNano(version.Temporal.SourceRecordedAt),
		optionalTimeToUnixNano(version.Temporal.SourceSupersededAt),
		timeToUnixNano(version.Temporal.OrreryIngestedAt),
		accessRef.Kind,
		accessRef.ID,
		accessRef.TenantID,
		accessState,
		sourceStreamID,
		sourceVersion,
		timeToUnixNano(version.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save context graph temporal resource version: %w", err)
	}
	return nil
}

func (s *PlatformStore) SaveEvidence(ctx context.Context, evidence Evidence) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if strings.TrimSpace(evidence.TenantID) == "" || strings.TrimSpace(evidence.ID) == "" {
		return fmt.Errorf("evidence tenant id and id are required")
	}
	sourceAccessRef, accessState, err := s.accessRefForWrite(ctx, "context_graph_evidence", evidence.TenantID, evidence.ID, evidence.SourceAccessRef)
	if err != nil {
		return err
	}
	evidence.SourceAccessRef = sourceAccessRef
	evidence.AccessState = accessState
	result, err := s.exec(ctx, `
INSERT INTO context_graph_evidence (
	id, tenant_id, source_kind, source_id, summary, confidence,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO UPDATE SET
	source_kind = excluded.source_kind,
	source_id = excluded.source_id,
	summary = excluded.summary,
	confidence = excluded.confidence,
	source_access_kind = excluded.source_access_kind,
	source_access_id = excluded.source_access_id,
	source_access_tenant_id = excluded.source_access_tenant_id,
	access_state = excluded.access_state,
	observed_at = excluded.observed_at
WHERE excluded.observed_at >= context_graph_evidence.observed_at AND (
	context_graph_evidence.access_state = 'unresolved' OR (
		context_graph_evidence.access_state = 'resolved' AND
		context_graph_evidence.source_access_kind = excluded.source_access_kind AND
		context_graph_evidence.source_access_id = excluded.source_access_id AND
		context_graph_evidence.source_access_tenant_id = excluded.source_access_tenant_id
	)
)`,
		evidence.ID,
		evidence.TenantID,
		evidence.SourceRef.Kind,
		evidence.SourceRef.ID,
		evidence.Summary,
		evidence.Confidence,
		evidence.SourceAccessRef.Kind,
		evidence.SourceAccessRef.ID,
		evidence.SourceAccessRef.TenantID,
		evidence.AccessState,
		timeToUnixNano(evidence.ObservedAt),
	)
	if err != nil {
		return fmt.Errorf("save context graph evidence: %w", err)
	}
	return s.checkAccessProtectedWrite(ctx, result, "context_graph_evidence", evidence.TenantID, evidence.ID, evidence.SourceAccessRef)
}

func (s *PlatformStore) DeleteEvidence(ctx context.Context, tenantID, evidenceID string, sourceRef Ref) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	tenantID, evidenceID = strings.TrimSpace(tenantID), strings.TrimSpace(evidenceID)
	sourceRef.Kind, sourceRef.ID = strings.TrimSpace(sourceRef.Kind), strings.TrimSpace(sourceRef.ID)
	if tenantID == "" || evidenceID == "" || sourceRef.Kind == "" || sourceRef.ID == "" {
		return fmt.Errorf("evidence tenant id, id, and source ref are required")
	}
	_, err := s.exec(ctx, `DELETE FROM context_graph_evidence WHERE tenant_id = ? AND id = ? AND source_kind = ? AND source_id = ?`,
		tenantID, evidenceID, sourceRef.Kind, sourceRef.ID)
	if err != nil {
		return fmt.Errorf("delete context graph evidence: %w", err)
	}
	return nil
}

func (s *PlatformStore) SaveProjectionAudit(ctx context.Context, audit ProjectionAudit) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if strings.TrimSpace(audit.TenantID) == "" || strings.TrimSpace(audit.ID) == "" {
		return fmt.Errorf("projection audit tenant id and id are required")
	}
	sourceAccessRef, accessState, err := s.accessRefForWrite(ctx, "context_graph_projection_audits", audit.TenantID, audit.ID, audit.SourceAccessRef)
	if err != nil {
		return err
	}
	audit.SourceAccessRef = sourceAccessRef
	audit.AccessState = accessState
	result, err := s.exec(ctx, `
	INSERT INTO context_graph_projection_audits (
		id, tenant_id, source_event_id, source_event_type, status, reason_code, reason, details_json,
		source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, id) DO UPDATE SET
		source_event_id = excluded.source_event_id,
		source_event_type = excluded.source_event_type,
		status = excluded.status,
		reason_code = excluded.reason_code,
		reason = excluded.reason,
		details_json = excluded.details_json,
		source_access_kind = excluded.source_access_kind,
		source_access_id = excluded.source_access_id,
		source_access_tenant_id = excluded.source_access_tenant_id,
		access_state = excluded.access_state,
		observed_at = excluded.observed_at,
		revision = context_graph_projection_audits.revision + 1
	WHERE context_graph_projection_audits.access_state = 'unresolved' OR (
		context_graph_projection_audits.access_state = 'resolved' AND
		context_graph_projection_audits.source_access_kind = excluded.source_access_kind AND
		context_graph_projection_audits.source_access_id = excluded.source_access_id AND
		context_graph_projection_audits.source_access_tenant_id = excluded.source_access_tenant_id
	)`,
		audit.ID,
		audit.TenantID,
		audit.SourceEventID,
		audit.SourceEventType,
		audit.Status,
		audit.ReasonCode,
		audit.Reason,
		mustGraphJSON(defaultGraphMap(audit.Details)),
		audit.SourceAccessRef.Kind,
		audit.SourceAccessRef.ID,
		audit.SourceAccessRef.TenantID,
		audit.AccessState,
		timeToUnixNano(audit.ObservedAt),
	)
	if err != nil {
		return fmt.Errorf("save context graph projection audit: %w", err)
	}
	return s.checkAccessProtectedWrite(ctx, result, "context_graph_projection_audits", audit.TenantID, audit.ID, audit.SourceAccessRef)
}

func (s *PlatformStore) GetProjectionAudit(ctx context.Context, tenantID string, auditID string) (ProjectionAudit, error) {
	if !s.ready() {
		return ProjectionAudit{}, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	auditID = strings.TrimSpace(auditID)
	if tenantID == "" || auditID == "" {
		return ProjectionAudit{}, fmt.Errorf("tenant id and projection audit id are required")
	}
	var audit ProjectionAudit
	var detailsJSON string
	var observedAt int64
	err := s.queryRow(ctx, `
SELECT id, tenant_id, source_event_id, source_event_type, status, reason_code, reason, details_json,
		source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at, revision
FROM context_graph_projection_audits
WHERE tenant_id = ? AND id = ?`, tenantID, auditID).Scan(
		&audit.ID, &audit.TenantID, &audit.SourceEventID, &audit.SourceEventType, &audit.Status,
		&audit.ReasonCode, &audit.Reason, &detailsJSON,
		&audit.SourceAccessRef.Kind, &audit.SourceAccessRef.ID, &audit.SourceAccessRef.TenantID, &audit.AccessState, &observedAt, &audit.Revision,
	)
	if err != nil {
		return ProjectionAudit{}, err
	}
	decodeGraphJSON(detailsJSON, &audit.Details)
	audit.ObservedAt = unixNanoToTime(observedAt)
	return audit, nil
}

func (s *PlatformStore) SaveProjectionAuditReplayHistory(ctx context.Context, history ProjectionAuditReplayHistory) error {
	if !s.ready() {
		return fmt.Errorf("context graph platform store is required")
	}
	if strings.TrimSpace(history.TenantID) == "" || strings.TrimSpace(history.ID) == "" {
		return fmt.Errorf("projection audit replay history tenant id and id are required")
	}
	sourceAccessRef, accessState, err := s.accessRefForWrite(ctx, "context_graph_projection_audit_replay_history", history.TenantID, history.ID, history.SourceAccessRef)
	if err != nil {
		return err
	}
	history.SourceAccessRef = sourceAccessRef
	history.AccessState = accessState
	result, err := s.exec(ctx, `
	INSERT INTO context_graph_projection_audit_replay_history (
		id, audit_id, tenant_id, source_event_id, source_event_type, status, error, details_json,
		source_access_kind, source_access_id, source_access_tenant_id, access_state, attempted_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, id) DO UPDATE SET
		audit_id = excluded.audit_id,
		source_event_id = excluded.source_event_id,
		source_event_type = excluded.source_event_type,
		status = excluded.status,
		error = excluded.error,
		details_json = excluded.details_json,
		source_access_kind = excluded.source_access_kind,
		source_access_id = excluded.source_access_id,
		source_access_tenant_id = excluded.source_access_tenant_id,
		access_state = excluded.access_state,
		attempted_at = excluded.attempted_at
	WHERE context_graph_projection_audit_replay_history.access_state = 'unresolved' OR (
		context_graph_projection_audit_replay_history.access_state = 'resolved' AND
		context_graph_projection_audit_replay_history.source_access_kind = excluded.source_access_kind AND
		context_graph_projection_audit_replay_history.source_access_id = excluded.source_access_id AND
		context_graph_projection_audit_replay_history.source_access_tenant_id = excluded.source_access_tenant_id
	)`,
		history.ID,
		history.AuditID,
		history.TenantID,
		history.SourceEventID,
		history.SourceEventType,
		history.Status,
		history.Error,
		mustGraphJSON(defaultGraphMap(history.Details)),
		history.SourceAccessRef.Kind,
		history.SourceAccessRef.ID,
		history.SourceAccessRef.TenantID,
		history.AccessState,
		timeToUnixNano(history.AttemptedAt),
	)
	if err != nil {
		return fmt.Errorf("save context graph projection audit replay history: %w", err)
	}
	return s.checkAccessProtectedWrite(ctx, result, "context_graph_projection_audit_replay_history", history.TenantID, history.ID, history.SourceAccessRef)
}

func (s *PlatformStore) ClearTenantProjection(ctx context.Context, tenantID string) (ProjectionClearReport, error) {
	if !s.ready() {
		return ProjectionClearReport{}, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ProjectionClearReport{}, fmt.Errorf("tenant id is required")
	}
	report := ProjectionClearReport{}
	if err := s.store.WithTx(ctx, func(tx db.Tx) error {
		return s.clearTenantProjectionTx(ctx, tx, tenantID, &report)
	}); err != nil {
		return ProjectionClearReport{}, err
	}
	return report, nil
}

func (s *PlatformStore) clearTenantProjectionTx(ctx context.Context, tx db.Tx, tenantID string, report *ProjectionClearReport) error {
	counts := []struct {
		name string
		dst  *int
	}{
		{name: "context_graph_links", dst: &report.Links},
		{name: "context_graph_facts", dst: &report.Facts},
		{name: "context_graph_evidence", dst: &report.Evidence},
		{name: "context_graph_objects", dst: &report.Objects},
	}
	for _, item := range counts {
		count, err := countTenantRowsTx(ctx, tx, item.name, tenantID)
		if err != nil {
			return err
		}
		*item.dst = count
	}
	deletes := []string{
		"context_graph_links",
		"context_graph_facts",
		"context_graph_evidence",
		"context_graph_objects",
	}
	for _, table := range deletes {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE tenant_id = ?", tenantID); err != nil {
			return fmt.Errorf("clear %s tenant projection: %w", table, err)
		}
	}
	return nil
}

func countTenantRowsTx(ctx context.Context, tx db.Tx, table string, tenantID string) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE tenant_id = ?", tenantID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s tenant projection: %w", table, err)
	}
	return count, nil
}

func (s *PlatformStore) Snapshot(ctx context.Context, tenantID string) ([]Object, []Link, []Fact, []Evidence, error) {
	if !s.ready() {
		return nil, nil, nil, nil, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, nil, nil, nil, fmt.Errorf("tenant id is required")
	}
	if s.tx == nil {
		var objects []Object
		var links []Link
		var facts []Fact
		var evidence []Evidence
		err := s.store.WithReadTx(ctx, func(tx db.Tx) error {
			var err error
			objects, links, facts, evidence, err = NewPlatformStoreTx(tx).snapshot(ctx, tenantID)
			return err
		})
		return objects, links, facts, evidence, err
	}
	return s.snapshot(ctx, tenantID)
}

func (s *PlatformStore) SnapshotAsOf(ctx context.Context, tenantID string, businessAsOf, knownAt time.Time) ([]Object, []Link, []Fact, []Evidence, error) {
	if !s.ready() {
		return nil, nil, nil, nil, fmt.Errorf("context graph platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, nil, nil, nil, fmt.Errorf("tenant id is required")
	}
	if s.tx == nil {
		var objects []Object
		var links []Link
		var facts []Fact
		var evidence []Evidence
		err := s.store.WithTx(ctx, func(tx db.Tx) error {
			var err error
			objects, links, facts, evidence, err = NewPlatformStoreTx(tx).snapshotAsOf(ctx, tenantID, businessAsOf, knownAt)
			return err
		})
		return objects, links, facts, evidence, err
	}
	return s.snapshotAsOf(ctx, tenantID, businessAsOf, knownAt)
}

func (s *PlatformStore) snapshotAsOf(ctx context.Context, tenantID string, businessAsOf, knownAt time.Time) ([]Object, []Link, []Fact, []Evidence, error) {
	objects, links, facts, evidence, err := s.snapshot(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	versions, err := s.listTemporalResourceVersions(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return temporalSnapshot(objects, links, facts, evidence, versions, businessAsOf, knownAt)
}

func (s *PlatformStore) listTemporalResourceVersions(ctx context.Context, tenantID string) ([]temporalResourceVersion, error) {
	rows, err := s.query(ctx, `
SELECT resource_kind, resource_id, assertion_id, payload_json,
	temporal_mode, business_valid_from, business_valid_to,
	source_recorded_at, source_superseded_at, orrery_ingested_at,
	access_kind, access_id, access_tenant_id, access_state,
	source_stream_id, source_version, updated_at
FROM context_graph_resource_versions
WHERE tenant_id = ?
ORDER BY resource_kind, resource_id, business_valid_from, source_recorded_at, assertion_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list context graph temporal resource versions: %w", err)
	}
	defer rows.Close()
	var out []temporalResourceVersion
	for rows.Next() {
		var version temporalResourceVersion
		var payloadJSON, accessKind, accessID, accessTenantID string
		var accessState AccessState
		var businessValidFrom, sourceRecordedAt, orreryIngestedAt, updatedAt int64
		var businessValidTo, sourceSupersededAt sql.NullInt64
		if err := rows.Scan(
			&version.Kind, &version.ID, &version.AssertionID, &payloadJSON,
			&version.Temporal.Mode, &businessValidFrom, &businessValidTo,
			&sourceRecordedAt, &sourceSupersededAt, &orreryIngestedAt,
			&accessKind, &accessID, &accessTenantID, &accessState,
			&version.SourceStreamID, &version.SourceVersion, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan context graph temporal resource version: %w", err)
		}
		version.Temporal.BusinessValidFrom = unixNanoToTime(businessValidFrom)
		version.Temporal.BusinessValidTo = nullableUnixNanoToTime(businessValidTo)
		version.Temporal.SourceRecordedAt = unixNanoToTime(sourceRecordedAt)
		version.Temporal.SourceSupersededAt = nullableUnixNanoToTime(sourceSupersededAt)
		version.Temporal.OrreryIngestedAt = unixNanoToTime(orreryIngestedAt)
		version.UpdatedAt = unixNanoToTime(updatedAt)
		accessRef := AccessRef{Kind: accessKind, ID: accessID, TenantID: accessTenantID}
		switch version.Kind {
		case temporalResourceObject:
			var value Object
			decodeGraphJSON(payloadJSON, &value)
			value.OwnerAccessRef, value.AccessState = accessRef, accessState
			value.SourceStreamID, value.SourceVersion = version.SourceStreamID, version.SourceVersion
			version.Temporal.BusinessTimeUnspecified = value.Temporal.BusinessTimeUnspecified
			value.Temporal, value.UpdatedAt = version.Temporal, version.UpdatedAt
			version.Object = &value
		case temporalResourceLink:
			var value Link
			decodeGraphJSON(payloadJSON, &value)
			value.SourceAccessRef, value.AccessState = accessRef, accessState
			value.SourceStreamID, value.SourceVersion = version.SourceStreamID, version.SourceVersion
			version.Temporal.BusinessTimeUnspecified = value.Temporal.BusinessTimeUnspecified
			value.Temporal, value.UpdatedAt = version.Temporal, version.UpdatedAt
			version.Link = &value
		case temporalResourceFact:
			var value Fact
			decodeGraphJSON(payloadJSON, &value)
			value.SourceAccessRef, value.AccessState = accessRef, accessState
			value.SourceStreamID, value.SourceVersion = version.SourceStreamID, version.SourceVersion
			version.Temporal.BusinessTimeUnspecified = value.Temporal.BusinessTimeUnspecified
			value.Temporal, value.UpdatedAt = version.Temporal, version.UpdatedAt
			version.Fact = &value
		default:
			continue
		}
		out = append(out, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list context graph temporal resource versions: %w", err)
	}
	return out, nil
}

func (s *PlatformStore) snapshot(ctx context.Context, tenantID string) ([]Object, []Link, []Fact, []Evidence, error) {
	objects, err := s.listObjects(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	links, err := s.listLinks(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	facts, err := s.listFacts(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	evidence, err := s.listEvidence(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return objects, links, facts, evidence, nil
}

func (s *PlatformStore) listObjects(ctx context.Context, tenantID string) ([]Object, error) {
	return s.listSelectedObjects(ctx, tenantID, nil)
}

func (s *PlatformStore) listSelectedObjects(ctx context.Context, tenantID string, ids []string) ([]Object, error) {
	query := `
SELECT id, tenant_id, object_type, canonical_key, label, properties_json, sensitivity,
	confidence, status, provenance_json, evidence_refs_json,
	owner_access_kind, owner_access_id, owner_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
FROM context_graph_objects
WHERE tenant_id = ?`
	args := []any{tenantID}
	if ids != nil {
		if len(ids) == 0 {
			return nil, nil
		}
		query += " AND id IN (" + strings.TrimRight(strings.Repeat("?,", len(ids)), ",") + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	query += " ORDER BY id"
	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list context graph objects: %w", err)
	}
	defer rows.Close()
	var out []Object
	for rows.Next() {
		var object Object
		var propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
		var updatedAt int64
		if err := rows.Scan(&object.ID, &object.TenantID, &object.ObjectType, &object.CanonicalKey, &object.Label, &propertiesJSON, &object.Sensitivity, &object.Confidence, &object.Status, &provenanceJSON, &evidenceRefsJSON, &object.OwnerAccessRef.Kind, &object.OwnerAccessRef.ID, &object.OwnerAccessRef.TenantID, &object.AccessState, &object.SourceStreamID, &object.SourceVersion, &temporalJSON, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan context graph object: %w", err)
		}
		decodeGraphJSON(propertiesJSON, &object.Properties)
		decodeGraphJSON(provenanceJSON, &object.Provenance)
		decodeGraphJSON(evidenceRefsJSON, &object.EvidenceRefs)
		decodeGraphJSON(temporalJSON, &object.Temporal)
		object.UpdatedAt = unixNanoToTime(updatedAt)
		out = append(out, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list context graph objects: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *PlatformStore) listLinks(ctx context.Context, tenantID string) ([]Link, error) {
	return s.listSelectedLinks(ctx, tenantID, "", nil)
}

func (s *PlatformStore) listSelectedLinks(ctx context.Context, tenantID, filter string, extraArgs []any) ([]Link, error) {
	query := `
SELECT id, tenant_id, link_type, from_object_id, to_object_id, properties_json,
	confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
FROM context_graph_links
WHERE tenant_id = ?`
	args := []any{tenantID}
	if filter != "" {
		query += " AND " + filter
		args = append(args, extraArgs...)
	}
	rows, err := s.query(ctx, query+" ORDER BY id", args...)
	if err != nil {
		return nil, fmt.Errorf("list context graph links: %w", err)
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var link Link
		var propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
		var updatedAt int64
		if err := rows.Scan(&link.ID, &link.TenantID, &link.LinkType, &link.FromObjectID, &link.ToObjectID, &propertiesJSON, &link.Confidence, &link.Status, &provenanceJSON, &evidenceRefsJSON, &link.SourceAccessRef.Kind, &link.SourceAccessRef.ID, &link.SourceAccessRef.TenantID, &link.AccessState, &link.SourceStreamID, &link.SourceVersion, &temporalJSON, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan context graph link: %w", err)
		}
		decodeGraphJSON(propertiesJSON, &link.Properties)
		decodeGraphJSON(provenanceJSON, &link.Provenance)
		decodeGraphJSON(evidenceRefsJSON, &link.EvidenceRefs)
		decodeGraphJSON(temporalJSON, &link.Temporal)
		link.UpdatedAt = unixNanoToTime(updatedAt)
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list context graph links: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *PlatformStore) listFacts(ctx context.Context, tenantID string) ([]Fact, error) {
	return s.listSelectedFacts(ctx, tenantID, "")
}

func (s *PlatformStore) listSelectedFacts(ctx context.Context, tenantID, filter string) ([]Fact, error) {
	query := `
SELECT id, tenant_id, subject_kind, subject_id, predicate, value_json, properties_json,
	confidence, status, provenance_json, evidence_refs_json,
	source_access_kind, source_access_id, source_access_tenant_id, access_state,
	source_stream_id, source_version, temporal_json, updated_at
FROM context_graph_facts
WHERE tenant_id = ?`
	if filter != "" {
		query += " AND " + filter
	}
	rows, err := s.query(ctx, query+" ORDER BY id", tenantID)
	if err != nil {
		return nil, fmt.Errorf("list context graph facts: %w", err)
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		var fact Fact
		var subjectKind, subjectID, valueJSON, propertiesJSON, provenanceJSON, evidenceRefsJSON, temporalJSON string
		var updatedAt int64
		if err := rows.Scan(&fact.ID, &fact.TenantID, &subjectKind, &subjectID, &fact.Predicate, &valueJSON, &propertiesJSON, &fact.Confidence, &fact.Status, &provenanceJSON, &evidenceRefsJSON, &fact.SourceAccessRef.Kind, &fact.SourceAccessRef.ID, &fact.SourceAccessRef.TenantID, &fact.AccessState, &fact.SourceStreamID, &fact.SourceVersion, &temporalJSON, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan context graph fact: %w", err)
		}
		fact.Subject = Ref{Kind: subjectKind, ID: subjectID}
		decodeGraphJSON(valueJSON, &fact.Value)
		decodeGraphJSON(propertiesJSON, &fact.Properties)
		decodeGraphJSON(provenanceJSON, &fact.Provenance)
		decodeGraphJSON(evidenceRefsJSON, &fact.EvidenceRefs)
		decodeGraphJSON(temporalJSON, &fact.Temporal)
		fact.UpdatedAt = unixNanoToTime(updatedAt)
		out = append(out, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list context graph facts: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *PlatformStore) listEvidence(ctx context.Context, tenantID string) ([]Evidence, error) {
	return s.listSelectedEvidence(ctx, tenantID, "", nil)
}

func (s *PlatformStore) listSelectedEvidence(ctx context.Context, tenantID, filter string, extraArgs []any) ([]Evidence, error) {
	query := `
SELECT id, tenant_id, source_kind, source_id, summary, confidence,
	source_access_kind, source_access_id, source_access_tenant_id, access_state, observed_at
FROM context_graph_evidence
WHERE tenant_id = ?`
	args := []any{tenantID}
	if filter != "" {
		query += " AND " + filter
		args = append(args, extraArgs...)
	}
	rows, err := s.query(ctx, query+" ORDER BY id", args...)
	if err != nil {
		return nil, fmt.Errorf("list context graph evidence: %w", err)
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		var evidence Evidence
		var sourceKind, sourceID string
		var observedAt int64
		if err := rows.Scan(&evidence.ID, &evidence.TenantID, &sourceKind, &sourceID, &evidence.Summary, &evidence.Confidence, &evidence.SourceAccessRef.Kind, &evidence.SourceAccessRef.ID, &evidence.SourceAccessRef.TenantID, &evidence.AccessState, &observedAt); err != nil {
			return nil, fmt.Errorf("scan context graph evidence: %w", err)
		}
		evidence.SourceRef = Ref{Kind: sourceKind, ID: sourceID}
		evidence.ObservedAt = unixNanoToTime(observedAt)
		out = append(out, evidence)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list context graph evidence: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *PlatformStore) accessRefForWrite(ctx context.Context, table string, tenantID string, resourceID string, incoming AccessRef) (AccessRef, AccessState, error) {
	incoming = normalizedAccessRefOrEmpty(incoming)
	query := ""
	switch table {
	case "context_graph_objects":
		query = `SELECT owner_access_kind, owner_access_id, owner_access_tenant_id, access_state FROM context_graph_objects WHERE tenant_id = ? AND id = ?`
	case "context_graph_links", "context_graph_facts", "context_graph_evidence", "context_graph_projection_audits", "context_graph_projection_audit_replay_history":
		query = `SELECT source_access_kind, source_access_id, source_access_tenant_id, access_state FROM ` + table + ` WHERE tenant_id = ? AND id = ?`
	default:
		return AccessRef{}, AccessStateUnresolved, fmt.Errorf("unsupported context graph access table %q", table)
	}
	var existing AccessRef
	var state AccessState
	err := s.queryRow(ctx, query, tenantID, resourceID).Scan(&existing.Kind, &existing.ID, &existing.TenantID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return incoming, accessStateFor(incoming), nil
	}
	if err != nil {
		return AccessRef{}, AccessStateUnresolved, err
	}
	existing = normalizedAccessRefOrEmpty(existing)
	if state == AccessStateResolved {
		if incoming.Resolved() && !sameAccessRef(existing, incoming) {
			return AccessRef{}, AccessStateUnresolved, fmt.Errorf("%w: %s %s/%s cannot be rebound", ErrAccessRefConflict, table, tenantID, resourceID)
		}
		if !existing.Resolved() {
			return AccessRef{}, AccessStateUnresolved, fmt.Errorf("%w: resolved row has an incomplete reference", ErrAccessRefConflict)
		}
		return existing, AccessStateResolved, nil
	}
	if state != "" && state != AccessStateUnresolved {
		return AccessRef{}, AccessStateUnresolved, fmt.Errorf("%w: unsupported access state %q", ErrAccessRefConflict, state)
	}
	return incoming, accessStateFor(incoming), nil
}

func (s *PlatformStore) checkAccessProtectedWrite(ctx context.Context, result sql.Result, table, tenantID, resourceID string, incoming AccessRef) error {
	if result == nil {
		return fmt.Errorf("save %s %s/%s returned no result", table, tenantID, resourceID)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected > 0 {
		return nil
	}
	_, _, err = s.accessRefForWrite(ctx, table, tenantID, resourceID, incoming)
	return err
}

func mustGraphJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func decodeGraphJSON(raw string, out any) {
	if raw == "" {
		raw = "{}"
	}
	_ = json.Unmarshal([]byte(raw), out)
}

func defaultGraphMap(src map[string]any) map[string]any {
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

func optionalTimeToUnixNano(value *time.Time) any {
	if value == nil {
		return nil
	}
	return timeToUnixNano(*value)
}

func nullableUnixNanoToTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	resolved := unixNanoToTime(value.Int64)
	return &resolved
}

func unixNanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

var _ Store = (*PlatformStore)(nil)
var _ WriterStore = (*PlatformStore)(nil)
