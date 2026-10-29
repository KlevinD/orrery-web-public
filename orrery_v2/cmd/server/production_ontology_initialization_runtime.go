package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	schedulerapp "orrery_v2/internal/app/scheduler"
	knowledgeStore "orrery_v2/internal/knowledge/store"
	memoryStore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func newOntologyInitializationProjector(ctx context.Context, core *db.Store, mode ontologyInitializationMode) (*ontology.MappingRuleProjectionRunner, error) {
	switch mode {
	case ontologyInitializationApply:
		return newProductionOntologyProjectionRunner(ctx, core)
	case ontologyInitializationDryRun:
		registry := ontology.NewMemoryRegistry()
		if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
			return nil, fmt.Errorf("install preview ontology definitions: %w", err)
		}
		gate := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry))
		runner := ontology.NewMappingRuleProjectionRunner(
			graphprotocol.NewProjectionRegistry(gate),
			gate,
			contextgraph.NewMemoryStore(),
		)
		if err := runner.RefreshProjections(ctx, registry); err != nil {
			return nil, fmt.Errorf("refresh preview ontology projections: %w", err)
		}
		return runner, nil
	default:
		return nil, fmt.Errorf("ontology initialization mode %q is not supported", mode)
	}
}

func runProductionOntologyInitialization(ctx context.Context, cfg config.Config, request ontologyInitializationRequest, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	runtime, err := platform.OpenWithModules(ctx, cfg, OntologyModule())
	if err != nil {
		return err
	}
	defer runtime.Close()
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		return err
	}
	before, err := readOntologyInitializationInventory(ctx, core)
	if err != nil {
		return fmt.Errorf("read ontology initialization inventory before run: %w", err)
	}
	if request.Mode == ontologyInitializationApply {
		if err := runtime.Initialize(ctx); err != nil {
			return err
		}
	}
	runner, err := newOntologyInitializationProjector(ctx, core, request.Mode)
	if err != nil {
		return err
	}
	graph := contextgraph.NewPlatformStore(core)
	coordinator := newOntologyInitializationCoordinator([]ontologyInitializationAdapter{
		newOntologyInitializationBaselineAdapter(cfg, runner),
		newOntologyInitializationWorkforceAdapter(
			operations.NewOrganizationCatalogStore(runtime.DBRouter),
			agentcore.NewPlatformProfileRepository(runtime.UnitOfWork),
			runner,
		),
		newOntologyInitializationResourceAdapter(toolres.NewPlatformRegistry(core), skillres.NewPlatformRegistry(core), runner),
		newOntologyInitializationKnowledgeAdapter(
			knowledgeStore.NewPlatformStore(core), runner,
			operations.NewOrganizationCatalogStore(runtime.DBRouter),
		),
		newOntologyInitializationMemoryAdapter(memoryStore.NewPlatformStore(core), runner),
		newOntologyInitializationSchedulerAdapter(schedulerapp.NewRepository(runtime.DBRouter), runner),
		newOntologyInitializationActionAdapter(actioncontract.NewPlatformRepository(core), runner, graph),
	})
	report, runErr := coordinator.Run(ctx, request)
	report.Before = before
	if request.Mode == ontologyInitializationApply {
		report.After, err = readOntologyInitializationInventory(ctx, core)
		if err != nil {
			return fmt.Errorf("read ontology initialization inventory after run: %w", err)
		}
	} else {
		report.After = before
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	if request.Mode == ontologyInitializationApply {
		if ontologyInitializationDefinitionsMissing(report.After.Definitions) {
			return fmt.Errorf("ontology initialization left managed definitions missing")
		}
		if report.After.DuplicateCanonicalKeys > 0 {
			return fmt.Errorf("ontology initialization found duplicate canonical keys")
		}
	}
	return nil
}

func readOntologyInitializationInventory(ctx context.Context, core *db.Store) (ontologyInitializationInventory, error) {
	if core == nil {
		return ontologyInitializationInventory{}, fmt.Errorf("core database is required for ontology initialization inventory")
	}
	definitions, err := ontology.AuditDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core))
	if err != nil {
		return ontologyInitializationInventory{}, err
	}
	objects, err := ontologyInitializationGroupedCounts(ctx, core, "context_graph_objects", "object_type")
	if err != nil {
		return ontologyInitializationInventory{}, err
	}
	links, err := ontologyInitializationGroupedCounts(ctx, core, "context_graph_links", "link_type")
	if err != nil {
		return ontologyInitializationInventory{}, err
	}
	var duplicateCanonicalKeys int
	if err := core.QueryRow(ctx, `
SELECT COUNT(*)
FROM (
    SELECT tenant_id, object_type, canonical_key
    FROM context_graph_objects
    WHERE canonical_key <> ''
    GROUP BY tenant_id, object_type, canonical_key
    HAVING COUNT(*) > 1
) duplicates`).Scan(&duplicateCanonicalKeys); err != nil {
		return ontologyInitializationInventory{}, err
	}
	return ontologyInitializationInventory{
		Definitions: definitions, Objects: objects, Links: links, DuplicateCanonicalKeys: duplicateCanonicalKeys,
	}, nil
}

func ontologyInitializationGroupedCounts(ctx context.Context, core *db.Store, table, column string) (map[string]int, error) {
	query := "SELECT " + column + ", COUNT(*) FROM " + table + " GROUP BY " + column + " ORDER BY " + column
	rows, err := core.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		counts[key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

func ontologyInitializationDefinitionsMissing(audit ontology.DefaultDefinitionAudit) bool {
	return audit.MissingObjectTypes > 0 || audit.MissingLinkTypes > 0 || audit.MissingProperties > 0 || audit.MissingMappingRules > 0
}
