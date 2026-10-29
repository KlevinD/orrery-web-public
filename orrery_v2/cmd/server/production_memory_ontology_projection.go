package main

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/db"
)

type productionMaterializedMemoryMetadataSink struct {
	sink  memoryadmin.MemoryRecordMetadataSink
	graph contextgraph.WriterStore
}

func newProductionMaterializedMemoryMetadataSink(core *db.Store, sink memoryadmin.MemoryRecordMetadataSink) productionMaterializedMemoryMetadataSink {
	return productionMaterializedMemoryMetadataSink{sink: sink, graph: contextgraph.NewPlatformStore(core)}
}

func newProductionMaterializedMemoryMetadataSinks(core *db.Store, runner *ontology.MappingRuleProjectionRunner) (memoryadmin.MemoryRecordMetadataSink, productionMaterializedMemoryMetadataSink, error) {
	sink, err := newProductionOntologyMemoryRecordMetadataSinkWithRunner(core, runner)
	return sink, newProductionMaterializedMemoryMetadataSink(core, sink), err
}

func (s productionMaterializedMemoryMetadataSink) PublishMaterializedMemoryRecord(ctx context.Context, record memcore.MemoryRecord) error {
	if s.sink == nil || s.graph == nil {
		return fmt.Errorf("memory ontology metadata sink is required")
	}
	record = record.Normalized().Clone()
	if isPersonalAssistantMemoryRecord(record) {
		return nil
	}
	event := memoryadmin.MemoryRecordAcceptedMetadataEvent(record)
	if err := s.sink.PublishMemoryRecordMetadata(ctx, event); err != nil {
		return err
	}
	observedAt := record.UpdatedAt.UTC()
	if observedAt.IsZero() {
		observedAt = record.CreatedAt.UTC()
	}
	if observedAt.IsZero() {
		observedAt = record.ValidFrom.UTC()
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	access := contextGraphAccessRef(trustedOntologySourceRef("memory_record", record.ID, event.TenantID))
	return s.graph.SaveObject(ctx, contextgraph.Object{
		ID: record.ID, TenantID: event.TenantID, ObjectType: "memory_record",
		CanonicalKey: "memory_record:" + record.ID,
		Label:        record.Summary,
		Properties: map[string]any{
			"summary": record.Summary, "layer": string(record.Layer), "topic": record.Topic,
			"scope_kind": string(record.Scope.Kind), "scope_id": record.Scope.ID,
		},
		Sensitivity: string(record.Sensitivity), Confidence: record.Confidence,
		Status: string(record.LifecycleStatus), Provenance: contextgraph.Ref{Kind: "event", ID: event.ID},
		OwnerAccessRef: access, SourceStreamID: record.ID, SourceVersion: event.Version, UpdatedAt: observedAt,
	})
}
