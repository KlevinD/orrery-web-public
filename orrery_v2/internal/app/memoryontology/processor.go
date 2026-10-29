package memoryontology

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/contextgraph"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

var retryBackoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}

const failedRunPersistenceTimeout = 5 * time.Second

type Processor struct {
	sweepMu   sync.Mutex
	afterKeys map[bool]string
	memory    MemoryReader
	runs      RunStore
	extractor Extractor
	applier   *GraphApplier
	projector MemoryRecordProjector
	scopes    ResolutionScopeProvider
	now       func() time.Time
}

type tenantGraphSnapshot struct {
	err     error
	objects []contextgraph.Object
	links   []contextgraph.Link
}

func NewProcessor(memory MemoryReader, runs RunStore, extractor Extractor, applier *GraphApplier, now func() time.Time, projectors ...MemoryRecordProjector) *Processor {
	var projector MemoryRecordProjector
	if len(projectors) > 0 {
		projector = projectors[0]
	}
	return &Processor{memory: memory, runs: runs, extractor: extractor, applier: applier, projector: projector, now: now}
}

func (p *Processor) WithResolutionScopes(scopes ResolutionScopeProvider) *Processor {
	if p != nil {
		p.scopes = scopes
	}
	return p
}

func (p *Processor) Sweep(ctx context.Context, limit int) (SweepReport, error) {
	return p.sweep(ctx, limit, true)
}

// ReprojectExisting re-resolves durable extracted mentions after the visible
// ontology dictionary changes. It never invokes the model.
func (p *Processor) ReprojectExisting(ctx context.Context, limit int) (SweepReport, error) {
	return p.sweep(ctx, limit, false)
}

func (p *Processor) sweep(ctx context.Context, limit int, allowExtraction bool) (SweepReport, error) {
	if p == nil || p.memory == nil || p.runs == nil || p.extractor == nil || p.applier == nil {
		return SweepReport{}, fmt.Errorf("memory ontology processor dependencies are required")
	}
	if limit <= 0 {
		limit = DefaultProcessingLimit
	}
	if !p.sweepMu.TryLock() {
		return SweepReport{}, nil
	}
	defer p.sweepMu.Unlock()
	if p.afterKeys == nil {
		p.afterKeys = make(map[bool]string)
	}
	page, err := p.scanRecords(ctx, p.afterKeys[allowExtraction], min(200, max(20, limit*20)))
	if err != nil {
		return SweepReport{}, err
	}
	report := SweepReport{Scanned: page.Scanned}
	snapshots := map[string]tenantGraphSnapshot{}
	var resultErr error
	consumed := 0
	for _, item := range page.Items {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(resultErr, err)
		}
		if report.Processed >= limit {
			break
		}
		p.afterKeys[allowExtraction] = item.Key
		consumed++
		record := item.Record
		if record.LifecycleStatus == memcore.MemoryLifecycleArchived || record.LifecycleStatus == memcore.MemoryLifecycleDeleted {
			report.Skipped++
			continue
		}
		processed, linked, processErr := p.processRecordWithSnapshotsMode(ctx, record, snapshots, allowExtraction)
		report.Linked += linked
		if processErr != nil {
			report.Processed++
			report.Failed++
			resultErr = errors.Join(resultErr, fmt.Errorf("memory %s: %w", record.ID, processErr))
			if errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded) {
				break
			}
			continue
		}
		if !processed {
			report.Skipped++
			continue
		}
		report.Processed++
		report.Succeeded++
	}
	if consumed == len(page.Items) {
		p.afterKeys[allowExtraction] = page.NextKey
		if !page.HasMore {
			p.afterKeys[allowExtraction] = ""
		}
	}
	return report, resultErr
}

func (p *Processor) scanRecords(ctx context.Context, afterKey string, limit int) (memstore.RecordScanPage, error) {
	if scanner, ok := p.memory.(memstore.RecordScanner); ok {
		return scanner.ScanRecords(ctx, afterKey, limit)
	}
	// Compatibility for readers without keyset support. Production uses the
	// bounded repository scanner through its authorization-aware adapter.
	records, err := p.memory.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: false})
	if err != nil {
		return memstore.RecordScanPage{}, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	page := memstore.RecordScanPage{}
	for _, record := range records {
		if record.ID <= afterKey {
			continue
		}
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, memstore.RecordScanItem{Key: record.ID, Record: record})
		page.NextKey = record.ID
	}
	page.Scanned = len(page.Items)
	return page, nil
}

func (p *Processor) processRecord(ctx context.Context, record memcore.MemoryRecord) (bool, int, error) {
	return p.processRecordWithSnapshots(ctx, record, map[string]tenantGraphSnapshot{})
}

func (p *Processor) processRecordWithSnapshots(ctx context.Context, record memcore.MemoryRecord, snapshots map[string]tenantGraphSnapshot) (bool, int, error) {
	return p.processRecordWithSnapshotsMode(ctx, record, snapshots, true)
}

func (p *Processor) processRecordWithSnapshotsMode(ctx context.Context, record memcore.MemoryRecord, snapshots map[string]tenantGraphSnapshot, allowExtraction bool) (bool, int, error) {
	tenantID := TenantIDForRecord(record)
	snapshot, err := p.snapshot(ctx, tenantID, snapshots)
	if err != nil {
		return false, 0, err
	}
	if _, projected := findObject(snapshot.objects, record.ID, "memory_record"); !projected && p.projector != nil {
		if err := p.projector.PublishMaterializedMemoryRecord(ctx, record); err != nil {
			return true, 0, fmt.Errorf("project memory ontology source object: %w", err)
		}
		objects, links, err := p.graphSnapshot(ctx, tenantID)
		if err != nil {
			return true, 0, err
		}
		snapshot = tenantGraphSnapshot{
			objects: objects,
			links:   links,
		}
		snapshots[tenantID] = snapshot
		if _, projected = findObject(snapshot.objects, record.ID, "memory_record"); !projected {
			return true, 0, fmt.Errorf("memory ontology source object %s is not projected after repair", record.ID)
		}
	}
	resolutionObjects, err := p.resolutionObjects(ctx, record, tenantID, snapshot.objects, snapshots)
	if err != nil {
		return true, 0, err
	}
	key := RunKey{
		TenantID: tenantID, MemoryID: record.ID,
		ContentHash: ContentHash(record), ExtractorVersion: ExtractorVersion + ":" + EntityDictionaryVersion(resolutionObjects),
	}
	now := p.currentTime()
	eligible, err := p.prepareRun(ctx, key, now)
	if err != nil {
		return true, 0, err
	}
	if !eligible {
		return false, 0, nil
	}
	var extraction Extraction
	if !allowExtraction {
		var exists bool
		extraction, exists, err = p.latestExtraction(ctx, key)
		if err != nil {
			return true, 0, err
		}
		if !exists {
			return false, 0, nil
		}
	}
	run, claimed, err := p.runs.ClaimRun(ctx, key, p.extractor.ModelID(), now)
	if err != nil || !claimed {
		return false, 0, err
	}
	if allowExtraction {
		var reused bool
		extraction, reused, err = p.latestExtraction(ctx, key)
		if err != nil {
			return true, 0, p.failRun(ctx, run, err)
		}
		if !reused {
			extraction, err = p.extractor.Extract(ctx, record)
			if err != nil {
				return true, 0, p.failRun(ctx, run, err)
			}
		}
	}
	projections := ProjectMentions(tenantID, extraction, resolutionObjects)
	linked, err := p.applier.Apply(ctx, tenantID, key.ContentHash, record, projections, resolutionObjects, snapshot.links)
	if err != nil {
		return true, linked, p.failRun(ctx, run, err)
	}
	completedAt := p.currentTime()
	if err := p.runs.ReplaceMentions(ctx, key, projections, completedAt); err != nil {
		return true, linked, p.failRun(ctx, run, err)
	}
	run.Status = RunSucceeded
	run.NextRetryAt = time.Time{}
	run.LastError = ""
	run.CompletedAt = completedAt
	run.UpdatedAt = completedAt
	if err := p.runs.SaveRun(ctx, run); err != nil {
		return true, linked, err
	}
	return true, linked, nil
}

type priorExtractionReader interface {
	LatestExtraction(context.Context, RunKey) (Extraction, bool, error)
}

func (p *Processor) latestExtraction(ctx context.Context, key RunKey) (Extraction, bool, error) {
	reader, ok := p.runs.(priorExtractionReader)
	if !ok {
		return Extraction{}, false, nil
	}
	return reader.LatestExtraction(ctx, key)
}

func (p *Processor) snapshot(ctx context.Context, tenantID string, snapshots map[string]tenantGraphSnapshot) (tenantGraphSnapshot, error) {
	if snapshot, ok := snapshots[tenantID]; ok {
		return snapshot, snapshot.err
	}
	objects, links, err := p.graphSnapshot(ctx, tenantID)
	if err != nil {
		snapshots[tenantID] = tenantGraphSnapshot{err: err}
		return tenantGraphSnapshot{}, err
	}
	snapshot := tenantGraphSnapshot{objects: objects, links: links}
	snapshots[tenantID] = snapshot
	return snapshot, nil
}

func (p *Processor) graphSnapshot(ctx context.Context, tenantID string) ([]contextgraph.Object, []contextgraph.Link, error) {
	if reader, ok := p.applier.graph.(interface {
		SnapshotObjectsLinks(context.Context, string) ([]contextgraph.Object, []contextgraph.Link, error)
	}); ok {
		return reader.SnapshotObjectsLinks(ctx, tenantID)
	}
	objects, links, _, _, err := p.applier.graph.Snapshot(ctx, tenantID)
	return objects, links, err
}

func (p *Processor) referenceSnapshot(ctx context.Context, tenantID string, snapshots map[string]tenantGraphSnapshot) (tenantGraphSnapshot, error) {
	if snapshot, ok := snapshots[tenantID]; ok {
		return snapshot, snapshot.err
	}
	key := "\x00dictionary:" + tenantID
	if snapshot, ok := snapshots[key]; ok {
		return snapshot, snapshot.err
	}
	if reader, ok := p.applier.graph.(interface {
		Objects(context.Context, string) ([]contextgraph.Object, error)
	}); ok {
		objects, err := reader.Objects(ctx, tenantID)
		snapshot := tenantGraphSnapshot{objects: objects, err: err}
		snapshots[key] = snapshot
		return snapshot, err
	}
	return p.snapshot(ctx, tenantID, snapshots)
}

func (p *Processor) resolutionObjects(ctx context.Context, record memcore.MemoryRecord, tenantID string, source []contextgraph.Object, snapshots map[string]tenantGraphSnapshot) ([]contextgraph.Object, error) {
	objects := append([]contextgraph.Object(nil), source...)
	if p.scopes == nil {
		return objects, nil
	}
	tenantIDs, err := p.scopes.ResolutionTenantIDs(ctx, record)
	if err != nil {
		return nil, err
	}
	seenTenants := map[string]struct{}{tenantID: {}}
	seenObjects := make(map[string]struct{}, len(objects))
	for _, object := range objects {
		seenObjects[strings.TrimSpace(object.ID)] = struct{}{}
	}
	for _, candidateTenantID := range tenantIDs {
		candidateTenantID = strings.TrimSpace(candidateTenantID)
		if candidateTenantID == "" {
			continue
		}
		if _, exists := seenTenants[candidateTenantID]; exists {
			continue
		}
		seenTenants[candidateTenantID] = struct{}{}
		snapshot, err := p.referenceSnapshot(ctx, candidateTenantID, snapshots)
		if err != nil {
			return nil, err
		}
		for _, object := range snapshot.objects {
			if _, supported := LinkTypeForObjectType(strings.ToLower(strings.TrimSpace(object.ObjectType))); !supported {
				continue
			}
			objectID := strings.TrimSpace(object.ID)
			if objectID == "" {
				continue
			}
			if _, exists := seenObjects[objectID]; exists {
				continue
			}
			seenObjects[objectID] = struct{}{}
			object.Properties = cloneObjectProperties(object.Properties)
			object.Properties["_orrery_reference_source_tenant"] = candidateTenantID
			object.TenantID = tenantID
			objects = append(objects, object)
		}
	}
	return objects, nil
}

func cloneObjectProperties(properties map[string]any) map[string]any {
	clone := make(map[string]any, len(properties)+1)
	for key, value := range properties {
		clone[key] = value
	}
	return clone
}

func (p *Processor) prepareRun(ctx context.Context, key RunKey, now time.Time) (bool, error) {
	run, exists, err := p.runs.GetRun(ctx, key)
	if err != nil || !exists {
		return !exists, err
	}
	if run.Status != RunFailed || !strings.Contains(run.LastError, "memory ontology source object") || !strings.Contains(run.LastError, "is not projected") {
		if run.Attempts >= MaximumAttempts {
			return false, nil
		}
		switch run.Status {
		case RunPending:
			return true, nil
		case RunFailed:
			return !run.NextRetryAt.After(now), nil
		case RunRunning:
			return !run.UpdatedAt.After(now.Add(-10 * time.Minute)), nil
		default:
			return false, nil
		}
	}
	run.Status = RunPending
	run.Attempts = 0
	run.NextRetryAt = time.Time{}
	run.LastError = ""
	run.CompletedAt = time.Time{}
	run.UpdatedAt = now
	return true, p.runs.SaveRun(ctx, run)
}

// EntityDictionaryVersion invalidates extraction runs when the ontology's
// entity language changes, including labels and aliases.
func EntityDictionaryVersion(objects []contextgraph.Object) string {
	type entity struct {
		ID           string   `json:"id"`
		ObjectType   string   `json:"object_type"`
		CanonicalKey string   `json:"canonical_key"`
		Label        string   `json:"label"`
		Aliases      []string `json:"aliases,omitempty"`
	}
	entities := make([]entity, 0, len(objects))
	for _, object := range objects {
		objectType := strings.ToLower(strings.TrimSpace(object.ObjectType))
		if _, supported := LinkTypeForObjectType(objectType); !supported {
			continue
		}
		entities = append(entities, entity{
			ID: strings.TrimSpace(object.ID), ObjectType: objectType,
			CanonicalKey: strings.TrimSpace(object.CanonicalKey), Label: strings.TrimSpace(object.Label),
			Aliases: normalizedAliases(object.Properties["aliases"]),
		})
	}
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].ObjectType == entities[j].ObjectType {
			return entities[i].ID < entities[j].ID
		}
		return entities[i].ObjectType < entities[j].ObjectType
	})
	payload, _ := json.Marshal(entities)
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:8])
}

func normalizedAliases(value any) []string {
	aliases := []string{}
	switch current := value.(type) {
	case []any:
		for _, candidate := range current {
			if text, ok := candidate.(string); ok {
				aliases = append(aliases, strings.TrimSpace(text))
			}
		}
	case []string:
		for _, candidate := range current {
			aliases = append(aliases, strings.TrimSpace(candidate))
		}
	case string:
		aliases = append(aliases, strings.TrimSpace(current))
	}
	filtered := aliases[:0]
	for _, alias := range aliases {
		if alias != "" {
			filtered = append(filtered, alias)
		}
	}
	sort.Strings(filtered)
	return filtered
}

func (p *Processor) failRun(ctx context.Context, run Run, cause error) error {
	now := p.currentTime()
	run.Status = RunFailed
	run.LastError = boundedError(cause)
	run.CompletedAt = now
	run.UpdatedAt = now
	if run.Attempts < MaximumAttempts {
		index := max(0, min(run.Attempts-1, len(retryBackoff)-1))
		run.NextRetryAt = now.Add(retryBackoff[index])
	} else {
		run.NextRetryAt = time.Time{}
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failedRunPersistenceTimeout)
	defer cancel()
	if saveErr := p.runs.SaveRun(persistCtx, run); saveErr != nil {
		return errors.Join(cause, saveErr)
	}
	return cause
}

func TenantIDForRecord(record memcore.MemoryRecord) string {
	scopeID := strings.TrimSpace(record.Scope.ID)
	switch record.Scope.Kind {
	case memcore.ScopeOrganization:
		if scopeID != "" {
			return scopeID
		}
	case memcore.ScopeDepartment, memcore.ScopeProject, memcore.ScopeMission, memcore.ScopeDiscussion, memcore.ScopeAgent:
		if scopeID != "" {
			return string(record.Scope.Kind) + ":" + scopeID
		}
	}
	if scopeID != "" {
		return scopeID
	}
	if record.ProfileID != "" {
		return string(record.ProfileID)
	}
	return record.ID
}

func ContentHash(record memcore.MemoryRecord) string {
	payload, _ := json.Marshal(struct {
		Topic     string            `json:"topic"`
		Content   string            `json:"content"`
		Summary   string            `json:"summary"`
		Narrative string            `json:"narrative"`
		Facts     []string          `json:"facts"`
		Lessons   []string          `json:"lessons"`
		Decisions []string          `json:"decisions"`
		Tags      []string          `json:"tags"`
		Entities  map[string]string `json:"entities"`
	}{record.Topic, record.Content, record.Summary, record.Narrative, record.Facts, record.Lessons, record.Decisions, record.Tags, record.Entities})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:])
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	runes := []rune(value)
	if len(runes) > 4000 {
		value = string(runes[:4000])
	}
	return value
}

func (p *Processor) currentTime() time.Time {
	if p.now != nil {
		return p.now().UTC()
	}
	return time.Now().UTC()
}
