package main

import (
	"context"
	"errors"
	"fmt"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryontology"
	memaudit "orrery_v2/internal/memory/audit"
	memcapture "orrery_v2/internal/memory/capture"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

type memoryOntologySourceReconciliationReport struct {
	Scanned   int
	Projected int
	Failed    int
}

type memoryOntologySourceRecordReader interface {
	FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error)
}

func reconcileProductionMemoryState(
	ctx context.Context,
	rt *platform.Runtime,
	core *db.Store,
	settings productionSettingsStore,
	memory *memstore.PlatformStore,
	audit *memaudit.EventRecorder,
	metadata memcapture.RecordMetadataSink,
) error {
	if err := reconcilePrivateMemoryOntologyState(ctx, rt, core, memory); err != nil {
		return err
	}
	reconcileProductionMemoryOntologySources(ctx, rt, core, newProductionMemoryOntologyReader(memory), metadata)
	return reconcilePendingAgentResultMemories(ctx, rt, settings, memory, audit, metadata)
}

func reconcilePrivateMemoryOntologyState(ctx context.Context, rt *platform.Runtime, core *db.Store, memory memoryOntologySourceRecordReader) error {
	if rt == nil || core == nil || memory == nil {
		return fmt.Errorf("private memory ontology reconciliation dependencies are required")
	}
	records, err := memory.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true})
	if err != nil {
		return fmt.Errorf("list private memories for ontology reconciliation: %w", err)
	}
	memoryIDs := make([]string, 0)
	for _, record := range records {
		if isPersonalAssistantMemoryRecord(record) {
			memoryIDs = append(memoryIDs, record.ID)
		}
	}
	deleted, err := memoryontology.NewPlatformStore(core).DeleteDerivedStateForMemories(ctx, memoryIDs)
	if err != nil {
		return err
	}
	rt.Logger.Info(ctx, "private memory ontology derived state reconciled",
		logging.Int("private_memories", len(memoryIDs)), logging.Int("deleted_rows", deleted))
	return nil
}

type memoryOntologySnapshotReader interface {
	Snapshot(context.Context, string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error)
}

func reconcileMemoryOntologySourceObjects(ctx context.Context, memory memoryOntologySourceRecordReader, projector memcapture.RecordMetadataSink) (memoryOntologySourceReconciliationReport, error) {
	report := memoryOntologySourceReconciliationReport{}
	if memory == nil || projector == nil {
		return report, fmt.Errorf("memory ontology source reconciliation dependencies are required")
	}
	records, err := memory.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: false})
	if err != nil {
		return report, err
	}
	report.Scanned = len(records)
	var resultErr error
	for _, record := range records {
		if err := projector.PublishMaterializedMemoryRecord(ctx, record); err != nil {
			report.Failed++
			resultErr = errors.Join(resultErr, fmt.Errorf("memory %s: %w", record.ID, err))
			continue
		}
		report.Projected++
	}
	return report, resultErr
}

func currentMemoryOntologyRunKeys(ctx context.Context, memory memoryOntologySourceRecordReader, graph memoryOntologySnapshotReader) ([]memoryontology.RunKey, error) {
	if memory == nil || graph == nil {
		return nil, fmt.Errorf("memory ontology run key dependencies are required")
	}
	records, err := memory.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: false})
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	keys := make([]memoryontology.RunKey, 0, len(records))
	for _, record := range records {
		tenantID := memoryontology.TenantIDForRecord(record)
		version, ok := versions[tenantID]
		if !ok {
			objects, _, _, _, err := graph.Snapshot(ctx, tenantID)
			if err != nil {
				return nil, fmt.Errorf("snapshot memory ontology tenant %s: %w", tenantID, err)
			}
			version = memoryontology.ExtractorVersion + ":" + memoryontology.EntityDictionaryVersion(objects)
			versions[tenantID] = version
		}
		keys = append(keys, memoryontology.RunKey{
			TenantID: tenantID, MemoryID: record.ID,
			ContentHash: memoryontology.ContentHash(record), ExtractorVersion: version,
		})
	}
	return keys, nil
}

func reconcileProductionMemoryOntologySources(ctx context.Context, rt *platform.Runtime, core *db.Store, memory memoryOntologySourceRecordReader, projector memcapture.RecordMetadataSink) {
	report, err := reconcileMemoryOntologySourceObjects(ctx, memory, projector)
	if err != nil {
		rt.Logger.Warn(ctx, "memory ontology source reconciliation completed with failures",
			logging.Int("scanned", report.Scanned), logging.Int("projected", report.Projected),
			logging.Int("failed", report.Failed), logging.String("error", err.Error()))
		return
	}
	currentKeys, err := currentMemoryOntologyRunKeys(ctx, memory, contextgraph.NewPlatformStore(core))
	if err != nil {
		rt.Logger.Warn(ctx, "memory ontology current run key recovery failed", logging.String("error", err.Error()))
		return
	}
	recovery, err := memoryontology.NewPlatformStore(core).RecoverSourceProjectionFailures(ctx, currentKeys, rt.Clock.Now())
	if err != nil {
		rt.Logger.Warn(ctx, "memory ontology source failure recovery failed", logging.String("error", err.Error()))
		return
	}
	rt.Logger.Info(ctx, "memory ontology source reconciliation completed",
		logging.Int("scanned", report.Scanned), logging.Int("projected", report.Projected),
		logging.Int("reset_runs", recovery.Reset), logging.Int("superseded_runs", recovery.Superseded))
}

func reconcilePendingAgentResultMemories(
	ctx context.Context,
	rt *platform.Runtime,
	settings productionSettingsStore,
	store *memstore.PlatformStore,
	audit *memaudit.EventRecorder,
	metadata memcapture.RecordMetadataSink,
) error {
	enabled, err := settings.MemoryEnabled(ctx)
	if err != nil {
		return fmt.Errorf("resolve memory setting for agent-result reconciliation: %w", err)
	}
	if !enabled {
		return nil
	}
	writer := memcapture.NewEpisodicWriter(memcapture.EpisodicWriterOptions{
		Repository: store,
		Audit:      audit,
		Metadata:   metadata,
		Now:        rt.Clock.Now,
	})
	report, err := writer.ReconcilePendingAgentResults(ctx, 1000)
	if err != nil {
		return fmt.Errorf("reconcile pending agent-result memories: %w", err)
	}
	rt.Logger.Info(ctx, "agent-result memory reconciliation completed",
		logging.Int("scanned", report.Scanned),
		logging.Int("accepted", report.Accepted),
		logging.Int("materialized", report.Materialized),
	)
	return nil
}
