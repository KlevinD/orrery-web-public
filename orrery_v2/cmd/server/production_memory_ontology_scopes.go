package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/memoryontology"
	"orrery_v2/internal/app/missioncontrol"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

const memoryOntologyScopeCatalogTTL = time.Minute

type memoryOntologyRecordReader interface {
	FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error)
}

type memoryOntologyProfileCatalog interface {
	ListProfiles(context.Context) ([]memcore.MemoryProfile, error)
}

type memoryOntologyMissionCatalog interface {
	ListMissionCatalogEntries(context.Context, string, int) ([]missioncontrol.MissionCatalogEntry, bool, error)
}

type productionMemoryOntologyReader struct {
	records memoryOntologyRecordReader
}

func newProductionMemoryOntologyReader(records memoryOntologyRecordReader) productionMemoryOntologyReader {
	return productionMemoryOntologyReader{records: records}
}

func (r productionMemoryOntologyReader) FindRecords(ctx context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error) {
	if r.records == nil {
		return nil, fmt.Errorf("memory ontology record reader is required")
	}
	records, err := r.records.FindRecords(ctx, query)
	if err != nil {
		return nil, err
	}
	filtered := make([]memcore.MemoryRecord, 0, len(records))
	for _, record := range records {
		if !isPersonalAssistantMemoryRecord(record) {
			filtered = append(filtered, record)
		}
	}
	return filtered, nil
}

func (r productionMemoryOntologyReader) ScanRecords(ctx context.Context, afterKey string, limit int) (memstore.RecordScanPage, error) {
	scanner, ok := r.records.(memstore.RecordScanner)
	if !ok {
		return memstore.RecordScanPage{}, fmt.Errorf("memory ontology requires a bounded record scanner")
	}
	page, err := scanner.ScanRecords(ctx, afterKey, limit)
	if err != nil {
		return memstore.RecordScanPage{}, err
	}
	filtered := make([]memstore.RecordScanItem, 0, len(page.Items))
	for _, item := range page.Items {
		if !isPersonalAssistantMemoryRecord(item.Record) {
			filtered = append(filtered, item)
		}
	}
	page.Items = filtered
	return page, nil
}

type memoryOntologyScopeCatalog struct {
	profileOrganizations map[string]string
	agentOrganizations   map[string]string
	missionTenants       map[string]string
	ambiguousMissions    map[string]struct{}
}

type productionMemoryOntologyScopeResolver struct {
	profiles memoryOntologyProfileCatalog
	missions memoryOntologyMissionCatalog
	now      func() time.Time

	mu       sync.Mutex
	loadedAt time.Time
	catalog  memoryOntologyScopeCatalog
}

func newProductionMemoryOntologyScopeResolver(profiles memoryOntologyProfileCatalog, missions memoryOntologyMissionCatalog, now func() time.Time) *productionMemoryOntologyScopeResolver {
	if now == nil {
		now = time.Now
	}
	return &productionMemoryOntologyScopeResolver{profiles: profiles, missions: missions, now: now}
}

func (r *productionMemoryOntologyScopeResolver) ResolutionTenantIDs(ctx context.Context, record memcore.MemoryRecord) ([]string, error) {
	if r == nil || r.profiles == nil {
		return nil, fmt.Errorf("memory ontology scope profile catalog is required")
	}
	catalog, err := r.currentCatalog(ctx)
	if err != nil {
		return nil, err
	}
	organizationID := firstNonBlank(
		catalog.profileOrganizations[strings.TrimSpace(string(record.ProfileID))],
		catalog.agentOrganizations[strings.TrimSpace(string(record.CreatedBy))],
	)
	if record.Scope.Kind == memcore.ScopeAgent {
		organizationID = firstNonBlank(organizationID, catalog.agentOrganizations[strings.TrimSpace(record.Scope.ID)])
	}

	tenantIDs := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || value == memoryontology.TenantIDForRecord(record) {
			return
		}
		for _, existing := range tenantIDs {
			if existing == value {
				return
			}
		}
		tenantIDs = append(tenantIDs, value)
	}

	scopeID := strings.TrimSpace(record.Scope.ID)
	switch record.Scope.Kind {
	case memcore.ScopeMission:
		missionTenantID := strings.TrimSpace(catalog.missionTenants[scopeID])
		_, ambiguousMission := catalog.ambiguousMissions[scopeID]
		if missionTenantID == "" && !ambiguousMission {
			missionTenantID = organizationID
		}
		if scopeID != "" && missionTenantID != "" {
			add(missionScopeTenantID(missionTenantID, scopeID))
			add(missionTenantID)
		}
		add(organizationID)
	case memcore.ScopeProject:
		if scopeID != "" && organizationID != "" {
			add(projectScopeTenantID(organizationID, scopeID))
		}
		add(organizationID)
	case memcore.ScopeAgent, memcore.ScopeDepartment, memcore.ScopeDiscussion:
		add(organizationID)
	}
	return tenantIDs, nil
}

func (r *productionMemoryOntologyScopeResolver) currentCatalog(ctx context.Context) (memoryOntologyScopeCatalog, error) {
	now := r.now().UTC()
	r.mu.Lock()
	if !r.loadedAt.IsZero() && now.Sub(r.loadedAt) < memoryOntologyScopeCatalogTTL {
		catalog := r.catalog
		r.mu.Unlock()
		return catalog, nil
	}
	r.mu.Unlock()

	catalog, err := r.loadCatalog(ctx)
	if err != nil {
		return memoryOntologyScopeCatalog{}, err
	}
	r.mu.Lock()
	r.catalog = catalog
	r.loadedAt = now
	r.mu.Unlock()
	return catalog, nil
}

func (r *productionMemoryOntologyScopeResolver) loadCatalog(ctx context.Context) (memoryOntologyScopeCatalog, error) {
	profiles, err := r.profiles.ListProfiles(ctx)
	if err != nil {
		return memoryOntologyScopeCatalog{}, fmt.Errorf("list memory profiles for ontology scope resolution: %w", err)
	}
	catalog := memoryOntologyScopeCatalog{
		profileOrganizations: map[string]string{},
		agentOrganizations:   map[string]string{},
		missionTenants:       map[string]string{},
		ambiguousMissions:    map[string]struct{}{},
	}
	for _, profile := range profiles {
		organizationID := strings.TrimSpace(profile.OwnerRef.OrganizationID)
		if organizationID == "" {
			continue
		}
		catalog.profileOrganizations[strings.TrimSpace(string(profile.ID))] = organizationID
		if agentID := strings.TrimSpace(string(profile.OwnerRef.AgentID)); agentID != "" {
			catalog.agentOrganizations[agentID] = organizationID
		}
	}
	if r.missions == nil {
		return catalog, nil
	}
	after := ""
	for {
		entries, hasMore, err := r.missions.ListMissionCatalogEntries(ctx, after, 500)
		if err != nil {
			return memoryOntologyScopeCatalog{}, fmt.Errorf("list missions for ontology scope resolution: %w", err)
		}
		for _, entry := range entries {
			missionID := strings.TrimSpace(string(entry.MissionID))
			tenantID := strings.TrimSpace(entry.TenantID)
			if missionID == "" || tenantID == "" {
				continue
			}
			if existing := catalog.missionTenants[missionID]; existing != "" && existing != tenantID {
				delete(catalog.missionTenants, missionID)
				catalog.ambiguousMissions[missionID] = struct{}{}
				continue
			}
			if _, conflict := catalog.ambiguousMissions[missionID]; !conflict {
				catalog.missionTenants[missionID] = tenantID
			}
		}
		if !hasMore {
			break
		}
		if len(entries) == 0 || strings.TrimSpace(entries[len(entries)-1].Seek) == "" || entries[len(entries)-1].Seek == after {
			return memoryOntologyScopeCatalog{}, fmt.Errorf("mission ontology scope catalog cursor did not advance")
		}
		after = entries[len(entries)-1].Seek
	}
	return catalog, nil
}

var _ memoryOntologyProfileCatalog = (memstore.Repository)(nil)
