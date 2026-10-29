package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

func TestProductionMemoryRecordIncludesCitationEvidenceFromContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-citation-evidence"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 20, 17, 30, 0, 0, time.UTC)
	knowledgeRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology knowledge runner: %v", err)
	}
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-document-metadata:kdoc-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.document.metadata.updated",
		StreamID: "kdoc-1",
		Version:  1,
		Payload: map[string]any{
			"id":                   "kdoc-1",
			"knowledge_base_id":    "kb-1",
			"title":                "release-handbook.md",
			"source_uri":           "pageindex://folder-123/pi-doc-kdoc-1",
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-kdoc-1",
			"content_type":         "text/markdown",
			"size_bytes":           int64(2048),
			"checksum":             "sha256:kdoc-1",
			"status":               "indexed",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge document metadata event: %v", err)
	}
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-chunk-metadata:kdoc-1:0:rev-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.chunk.metadata.updated",
		StreamID: "kdoc-1:0",
		Version:  2,
		Payload: map[string]any{
			"id":                "kdoc-1:0",
			"document_id":       "kdoc-1",
			"knowledge_base_id": "kb-1",
			"ordinal":           0,
			"heading_path":      []any{"Release"},
			"start_offset":      0,
			"end_offset":        128,
			"content_hash":      "sha256:chunk",
			"vector_id":         "kdoc-1:0",
			"token_estimate":    128,
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge chunk metadata event: %v", err)
	}
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-page-metadata:kdoc-1:page:1:rev-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.page.metadata.updated",
		StreamID: "kdoc-1",
		Version:  3,
		Payload: map[string]any{
			"id":                   "kdoc-1:page:1",
			"document_id":          "kdoc-1",
			"knowledge_base_id":    "kb-1",
			"page_number":          1,
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-kdoc-1",
			"heading_path":         []any{"Release"},
			"source_uri":           "pageindex://folder-123/pi-doc-kdoc-1#page=1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge page metadata event: %v", err)
	}
	anchorID := "kdoc-1:page:1:anchor:0007"
	if _, err := knowledgeRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "knowledge-anchor-metadata:" + anchorID + ":rev-1",
		TenantID: "mission:mission-1",
		Type:     "knowledge.anchor.metadata.updated",
		StreamID: "kdoc-1",
		Version:  4,
		Payload: map[string]any{
			"id":                   anchorID,
			"knowledge_base_id":    "kb-1",
			"document_id":          "kdoc-1",
			"page_id":              "kdoc-1:page:1",
			"page_number":          1,
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-kdoc-1",
			"provider_node_id":     "0007",
			"heading_path":         []any{"Release"},
			"structure_path":       []any{"Handbook", "Release"},
			"bbox":                 map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
			"image_ref":            "pageindex://folder-123/pi-doc-kdoc-1#page=1&image=preview",
			"content_hash":         "",
			"snippet_hash":         "sha256:anchor",
			"source_uri":           "pageindex://folder-123/pi-doc-kdoc-1#page=1&anchor=0007",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply knowledge anchor metadata event: %v", err)
	}

	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-citation-evidence-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "release",
		Summary:       "Release follows cited evidence.",
		EvidenceRefs:  []string{"knowledge_document:kdoc-1", "knowledge_chunk:kdoc-1:0", "knowledge_anchor:" + anchorID},
		Confidence:    0.92,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink).
		WithCitationEvidenceResolver(newProductionMemoryCitationEvidenceResolver(core))

	if _, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryReview},
		MissionIDs:      []string{"mission-1"},
	}, "candidate-citation-evidence-1", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-record-citation-evidence-1",
	}); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	record, err := memoryAdmin.GetRecord(ctx, memoryadmin.ViewerRef{
		ViewerID:        "reader-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryRead},
		MissionIDs:      []string{"mission-1"},
	}, "memory-record-citation-evidence-1")
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if len(record.CitationEvidence) != 3 {
		t.Fatalf("citation evidence = %+v", record.CitationEvidence)
	}
	if record.CitationEvidence[0].Kind != "knowledge_document" ||
		record.CitationEvidence[0].Title != "release-handbook.md" ||
		record.CitationEvidence[0].SourceURI != "pageindex://folder-123/pi-doc-kdoc-1" {
		t.Fatalf("document evidence = %+v", record.CitationEvidence[0])
	}
	if record.CitationEvidence[1].Kind != "knowledge_chunk" ||
		record.CitationEvidence[1].DocumentID != "kdoc-1" ||
		record.CitationEvidence[1].ContentHash != "sha256:chunk" {
		t.Fatalf("chunk evidence = %+v", record.CitationEvidence[1])
	}
	if record.CitationEvidence[2].Kind != "knowledge_anchor" ||
		record.CitationEvidence[2].DocumentID != "kdoc-1" ||
		record.CitationEvidence[2].PageID != "kdoc-1:page:1" ||
		record.CitationEvidence[2].PageNumber != 1 ||
		record.CitationEvidence[2].ProviderNodeID != "0007" ||
		record.CitationEvidence[2].SnippetHash != "sha256:anchor" ||
		record.CitationEvidence[2].SourceURI != "pageindex://folder-123/pi-doc-kdoc-1#page=1&anchor=0007" {
		t.Fatalf("anchor evidence = %+v", record.CitationEvidence[2])
	}
}

func assertContextGraphObject(t *testing.T, objects []contextgraph.Object, objectType string, id string) contextgraph.Object {
	t.Helper()
	for _, object := range objects {
		if object.ObjectType == objectType && object.ID == id {
			return object
		}
	}
	t.Fatalf("object %s/%s missing in %+v", objectType, id, objects)
	return contextgraph.Object{}
}

func assertNoContextGraphEvidence(t *testing.T, evidence []contextgraph.Evidence, id string) {
	t.Helper()
	for _, item := range evidence {
		if item.ID == id {
			t.Fatalf("evidence %s unexpectedly present in %+v", id, evidence)
		}
	}
}

func assertNoContextGraphObject(t *testing.T, objects []contextgraph.Object, objectType string, id string) {
	t.Helper()
	for _, object := range objects {
		if object.ObjectType == objectType && object.ID == id {
			t.Fatalf("object %s/%s unexpectedly present in %+v", objectType, id, objects)
		}
	}
}

func assertContextGraphLink(t *testing.T, links []contextgraph.Link, linkType string, fromID string, toID string) contextgraph.Link {
	t.Helper()
	for _, link := range links {
		if link.LinkType == linkType && link.FromObjectID == fromID && link.ToObjectID == toID {
			return link
		}
	}
	t.Fatalf("link %s %s -> %s missing in %+v", linkType, fromID, toID, links)
	return contextgraph.Link{}
}

func assertNoContextGraphLink(t *testing.T, links []contextgraph.Link, linkType string, fromID string, toID string) {
	t.Helper()
	for _, link := range links {
		if link.LinkType == linkType && link.FromObjectID == fromID && link.ToObjectID == toID {
			t.Fatalf("link %s %s -> %s unexpectedly present in %+v", linkType, fromID, toID, links)
		}
	}
}

func assertContextGraphFact(t *testing.T, facts []contextgraph.Fact, subjectKind string, subjectID string, predicate string, want any) {
	t.Helper()
	for _, fact := range facts {
		if fact.Subject.Kind == subjectKind && fact.Subject.ID == subjectID && fact.Predicate == predicate {
			if !contextGraphFactValueEqual(fact.Value, want) {
				t.Fatalf("fact %s/%s %s = %#v, want %#v", subjectKind, subjectID, predicate, fact.Value, want)
			}
			return
		}
	}
	t.Fatalf("fact %s/%s %s missing in %+v", subjectKind, subjectID, predicate, facts)
}

func assertContextGraphEvidence(t *testing.T, evidence []contextgraph.Evidence, wantID string) {
	t.Helper()
	for _, item := range evidence {
		if item.ID == wantID {
			return
		}
	}
	t.Fatalf("evidence %s missing in %+v", wantID, evidence)
}

func assertProjectionAudit(t testing.TB, ctx context.Context, graph *contextgraph.PlatformStore, tenantID string, wantID string, wantEventType string, wantReasonCode string) contextgraph.ProjectionAudit {
	t.Helper()
	audit, err := graph.GetProjectionAudit(ctx, tenantID, wantID)
	if err != nil {
		t.Fatalf("get projection audit %s/%s: %v", tenantID, wantID, err)
	}
	if audit.SourceEventType != wantEventType {
		t.Fatalf("projection audit %s event type = %q, want %q", wantID, audit.SourceEventType, wantEventType)
	}
	if audit.Status != "skipped" {
		t.Fatalf("projection audit %s status = %q, want skipped", wantID, audit.Status)
	}
	if audit.ReasonCode != wantReasonCode {
		t.Fatalf("projection audit %s reason code = %q, want %q", wantID, audit.ReasonCode, wantReasonCode)
	}
	return audit
}

func assertProjectionAuditTarget(t *testing.T, audit contextgraph.ProjectionAudit, key string, objectType string, id string) {
	t.Helper()
	rawTargets, ok := audit.Details[key].([]any)
	if !ok {
		t.Fatalf("projection audit %s details[%s] = %#v, want target list", audit.ID, key, audit.Details[key])
	}
	for _, raw := range rawTargets {
		target, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if target["object_type"] == objectType && target["object_id"] == id {
			return
		}
	}
	t.Fatalf("projection audit %s missing %s target %s/%s in %#v", audit.ID, key, objectType, id, rawTargets)
}

func contextGraphFactValueEqual(got any, want any) bool {
	if reflect.DeepEqual(got, want) {
		return true
	}
	gotNumber, gotOK := numericFactValue(got)
	wantNumber, wantOK := numericFactValue(want)
	return gotOK && wantOK && gotNumber == wantNumber
}

func numericFactValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}
