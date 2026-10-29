package schema

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/ontology"
	contract "orrery_v2/internal/app/ontologyview/contract"
)

func TestSummaryOriginRetainsVisibilityAndSeparatesSharedDefinitions(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinition("global", "system", "Global"), objectDefinition("local", "tenant-1", "Local"),
		objectDefinition("hidden", "tenant-1", "Hidden"), objectDefinition("foreign", "tenant-2", "Foreign"),
	}}
	s := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{hidden: map[string]bool{"object:hidden": true}, masked: map[string][]string{}})
	for _, origin := range []DefinitionOrigin{OriginSystem, OriginTenant} {
		value, err := s.SummaryOrigin(context.Background(), schemaPrincipal("user-1"), "tenant-1", origin)
		if err != nil || value.Data.ObjectTypes.Value == nil || *value.Data.ObjectTypes.Value != 1 {
			t.Fatalf("origin=%s value=%+v err=%v", origin, value, err)
		}
	}
	s.budgets, _ = contract.NewBudgetPolicy(map[contract.QueryClass]contract.QueryBudget{contract.QuerySchemaSummary: {StoreReads: 1}})
	value, err := s.Summary(context.Background(), schemaPrincipal("user-1"), "tenant-1")
	if err != nil || value.Data.ObjectTypes.Status != contract.MetricUnavailable || value.Data.ObjectTypes.Value != nil {
		t.Fatalf("incomplete count was exact: %+v %v", value, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Summary(ctx, schemaPrincipal("user-1"), "tenant-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation hidden: %v", err)
	}
}
