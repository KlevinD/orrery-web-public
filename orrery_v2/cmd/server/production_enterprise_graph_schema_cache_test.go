package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

func TestProductionEnterpriseGraphSchemaCacheUsesTTLAndLRU(t *testing.T) {
	now := time.Date(2026, 8, 22, 4, 0, 0, 0, time.UTC)
	provider := &countingSchemaProvider{}
	revisions := &fixedSchemaRevision{}
	cache := newProductionEnterpriseGraphSchemaCache(provider, func() time.Time { return now }, revisions)
	if _, err := cache.SchemaDictionary(context.Background(), "tenant-0"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.SchemaDictionary(context.Background(), "tenant-0"); err != nil {
		t.Fatal(err)
	}
	if provider.calls["tenant-0"] != 1 {
		t.Fatalf("tenant-0 calls = %d", provider.calls["tenant-0"])
	}
	revisions.value = 1
	if _, err := cache.SchemaDictionary(context.Background(), "tenant-0"); err != nil {
		t.Fatal(err)
	}
	if provider.calls["tenant-0"] != 2 {
		t.Fatalf("revision-invalidated tenant-0 calls = %d", provider.calls["tenant-0"])
	}
	now = now.Add(5*time.Minute + time.Second)
	if _, err := cache.SchemaDictionary(context.Background(), "tenant-0"); err != nil {
		t.Fatal(err)
	}
	if provider.calls["tenant-0"] != 3 {
		t.Fatalf("expired tenant-0 calls = %d", provider.calls["tenant-0"])
	}
	for index := 1; index <= 128; index++ {
		if _, err := cache.SchemaDictionary(context.Background(), fmt.Sprintf("tenant-%d", index)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cache.SchemaDictionary(context.Background(), "tenant-0"); err != nil {
		t.Fatal(err)
	}
	if provider.calls["tenant-0"] != 4 {
		t.Fatalf("LRU tenant-0 calls = %d", provider.calls["tenant-0"])
	}
}

type fixedSchemaRevision struct {
	value int64
}

func (r *fixedSchemaRevision) MappingProjectionRevision(context.Context) (int64, error) {
	return r.value, nil
}

type countingSchemaProvider struct {
	calls map[string]int
}

func (p *countingSchemaProvider) SchemaDictionary(_ context.Context, tenantID string) (contextgraph.SchemaDictionary, error) {
	if p.calls == nil {
		p.calls = make(map[string]int)
	}
	p.calls[tenantID]++
	return contextgraph.SchemaDictionary{
		ObjectTypes: []contextgraph.SchemaObjectType{{ID: tenantID}}, VersionHash: fmt.Sprintf("%s-v%d", tenantID, p.calls[tenantID]),
	}, nil
}
