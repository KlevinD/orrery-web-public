package main

import "context"

func newProductionKnowledgeSettingsCache(ctx context.Context, settings productionSettingsStore) *knowledgeSettingsSnapshot {
	cache := &knowledgeSettingsSnapshot{}
	if initial, err := settings.GetSettings(ctx); err == nil {
		cache.Remember(initial)
	}
	return cache
}
