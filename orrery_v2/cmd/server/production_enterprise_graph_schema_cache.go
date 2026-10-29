package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

const (
	productionEnterpriseGraphSchemaCacheTTL = 5 * time.Minute
	productionEnterpriseGraphSchemaCacheMax = 128
)

type productionEnterpriseGraphSchemaCache struct {
	mu        sync.Mutex
	provider  contextgraph.SchemaProvider
	now       func() time.Time
	sequence  uint64
	entries   map[string]productionEnterpriseGraphSchemaEntry
	revisions productionEnterpriseGraphSchemaRevisionReader
}

type productionEnterpriseGraphSchemaRevisionReader interface {
	MappingProjectionRevision(context.Context) (int64, error)
}

type productionEnterpriseGraphSchemaEntry struct {
	dictionary contextgraph.SchemaDictionary
	expiresAt  time.Time
	lastUsed   uint64
	revision   int64
}

func newProductionEnterpriseGraphSchemaCache(provider contextgraph.SchemaProvider, now func() time.Time, revisions ...productionEnterpriseGraphSchemaRevisionReader) *productionEnterpriseGraphSchemaCache {
	if now == nil {
		now = time.Now
	}
	cache := &productionEnterpriseGraphSchemaCache{provider: provider, now: now, entries: make(map[string]productionEnterpriseGraphSchemaEntry)}
	if len(revisions) > 0 {
		cache.revisions = revisions[0]
	}
	return cache
}

func (c *productionEnterpriseGraphSchemaCache) SchemaDictionary(ctx context.Context, tenantID string) (contextgraph.SchemaDictionary, error) {
	tenantID = strings.TrimSpace(tenantID)
	if c == nil || c.provider == nil || tenantID == "" {
		return contextgraph.SchemaDictionary{}, nil
	}
	now := c.now().UTC()
	revision := int64(0)
	if c.revisions != nil {
		var err error
		revision, err = c.revisions.MappingProjectionRevision(ctx)
		if err != nil {
			return contextgraph.SchemaDictionary{}, err
		}
	}
	c.mu.Lock()
	if entry, ok := c.entries[tenantID]; ok && now.Before(entry.expiresAt) && entry.revision == revision {
		c.sequence++
		entry.lastUsed = c.sequence
		c.entries[tenantID] = entry
		dictionary := cloneEnterpriseGraphSchemaDictionary(entry.dictionary)
		c.mu.Unlock()
		return dictionary, nil
	}
	c.mu.Unlock()

	dictionary, err := c.provider.SchemaDictionary(ctx, tenantID)
	if err != nil {
		return contextgraph.SchemaDictionary{}, err
	}
	dictionary = cloneEnterpriseGraphSchemaDictionary(dictionary)
	c.mu.Lock()
	c.sequence++
	c.entries[tenantID] = productionEnterpriseGraphSchemaEntry{
		dictionary: cloneEnterpriseGraphSchemaDictionary(dictionary),
		expiresAt:  now.Add(productionEnterpriseGraphSchemaCacheTTL),
		lastUsed:   c.sequence,
		revision:   revision,
	}
	if len(c.entries) > productionEnterpriseGraphSchemaCacheMax {
		oldestTenant := ""
		oldestUse := ^uint64(0)
		for key, entry := range c.entries {
			if entry.lastUsed < oldestUse {
				oldestTenant, oldestUse = key, entry.lastUsed
			}
		}
		delete(c.entries, oldestTenant)
	}
	c.mu.Unlock()
	return dictionary, nil
}

func cloneEnterpriseGraphSchemaDictionary(src contextgraph.SchemaDictionary) contextgraph.SchemaDictionary {
	dst := src
	dst.ObjectTypes = append([]contextgraph.SchemaObjectType(nil), src.ObjectTypes...)
	for index := range dst.ObjectTypes {
		dst.ObjectTypes[index].AllowedLinkTypes = append([]string(nil), dst.ObjectTypes[index].AllowedLinkTypes...)
		dst.ObjectTypes[index].AllowedActions = append([]string(nil), dst.ObjectTypes[index].AllowedActions...)
	}
	dst.LinkTypes = append([]contextgraph.SchemaLinkType(nil), src.LinkTypes...)
	for index := range dst.LinkTypes {
		dst.LinkTypes[index].AllowedActions = append([]string(nil), dst.LinkTypes[index].AllowedActions...)
	}
	dst.PropertyTypes = append([]contextgraph.SchemaPropertyType(nil), src.PropertyTypes...)
	for index := range dst.PropertyTypes {
		dst.PropertyTypes[index].AllowedActions = append([]string(nil), dst.PropertyTypes[index].AllowedActions...)
	}
	dst.ActionTypes = append([]contextgraph.SchemaActionType(nil), src.ActionTypes...)
	for index := range dst.ActionTypes {
		dst.ActionTypes[index].ObjectTypeRefs = append([]string(nil), dst.ActionTypes[index].ObjectTypeRefs...)
		dst.ActionTypes[index].Verbs = append([]string(nil), dst.ActionTypes[index].Verbs...)
		dst.ActionTypes[index].Roles = append([]contextgraph.SchemaActionRole(nil), dst.ActionTypes[index].Roles...)
		dst.ActionTypes[index].Preconditions = append([]string(nil), dst.ActionTypes[index].Preconditions...)
		dst.ActionTypes[index].Effects = append([]string(nil), dst.ActionTypes[index].Effects...)
		dst.ActionTypes[index].DecomposesInto = append([]string(nil), dst.ActionTypes[index].DecomposesInto...)
		dst.ActionTypes[index].KnowledgeTerms = append([]string(nil), dst.ActionTypes[index].KnowledgeTerms...)
		dst.ActionTypes[index].MemoryTerms = append([]string(nil), dst.ActionTypes[index].MemoryTerms...)
	}
	return dst
}
