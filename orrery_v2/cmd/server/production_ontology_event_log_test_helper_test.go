package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/problemhub"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

// List is test-only so production code cannot scan the complete ontology event log.
func (l productionOntologyEventLog) List(ctx context.Context) ([]graphprotocol.DomainEvent, error) {
	if l.store == nil {
		return nil, fmt.Errorf("ontology event store is required")
	}
	envelopes, err := l.store.List(ctx, eventstore.ListQuery{EventTypePrefix: ontologyEventTypePrefix})
	if err != nil {
		return nil, err
	}
	events := make([]graphprotocol.DomainEvent, 0, len(envelopes))
	for _, envelope := range envelopes {
		event, err := ontologyEventFromEnvelope(envelope)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func newOntologyEventLogTestCore(t *testing.T, name string) *db.Store {
	t.Helper()
	databases, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: name},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() { _ = databases.Close() })
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatal("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := reliability.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register reliability migrations: %v", err)
	}
	if err := problemhub.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register problem hub migrations: %v", err)
	}
	if err := coordinator.UpRole(context.Background(), db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(context.Background(), ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	return core
}

func newProductionAgentExecutionOntologyTestSink(core *db.Store, runner *ontology.MappingRuleProjectionRunner, now func() time.Time, recorders ...ontologyMetricRecorder) *productionAgentExecutionOntologySink {
	var metrics ontologyMetricRecorder
	if len(recorders) > 0 {
		metrics = recorders[0]
	}
	return &productionAgentExecutionOntologySink{
		runner: runner,
		state: productionOntologyStateProjector{
			runner:  runner,
			store:   ontology.NewPlatformStateProjectionStore(core),
			now:     now,
			metrics: metrics,
		},
		events: newProductionOntologyEventLog(core),
	}
}
