package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/db"
)

const (
	profileTable   = "memory_profiles"
	candidateTable = "memory_candidates"
	recordTable    = "memory_records"
)

type PlatformStore struct {
	store *db.Store
}

func NewPlatformStore(store *db.Store) *PlatformStore {
	return &PlatformStore{store: store}
}

func (s *PlatformStore) SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error {
	payload, err := json.Marshal(profile.Clone())
	if err != nil {
		return fmt.Errorf("encode memory profile: %w", err)
	}
	return s.store.PutRecord(ctx, profileTable, string(profile.ID), db.Record{"payload": string(payload)})
}

func (s *PlatformStore) GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error) {
	record, ok, err := s.store.GetRecord(ctx, profileTable, string(id))
	if err != nil {
		return memcore.MemoryProfile{}, err
	}
	if !ok {
		return memcore.MemoryProfile{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return memcore.MemoryProfile{}, fmt.Errorf("%w: profile payload missing", memcore.ErrInvalid)
	}
	var profile memcore.MemoryProfile
	if err := json.Unmarshal([]byte(raw), &profile); err != nil {
		return memcore.MemoryProfile{}, fmt.Errorf("decode memory profile: %w", err)
	}
	return profile.Clone(), nil
}

func (s *PlatformStore) ListProfiles(ctx context.Context) ([]memcore.MemoryProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	table, err := s.store.ListRecordTable(ctx, profileTable)
	if err != nil {
		return nil, err
	}
	out := make([]memcore.MemoryProfile, 0, len(table))
	for _, record := range table {
		raw, ok := record["payload"].(string)
		if !ok || raw == "" {
			return nil, fmt.Errorf("%w: profile payload missing", memcore.ErrInvalid)
		}
		var profile memcore.MemoryProfile
		if err := json.Unmarshal([]byte(raw), &profile); err != nil {
			return nil, fmt.Errorf("decode memory profile: %w", err)
		}
		out = append(out, profile.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

type OrganizationScopeMigrationReport struct {
	Profiles   int
	Candidates int
	Records    int
}

type ProfileReassignmentReport struct {
	ProfileMoved  bool
	TargetExisted bool
	RecordsMoved  int
}

// ReassignProfile moves a personal memory stream to a canonical profile ID.
// Existing records on the canonical profile are preserved.
func (s *PlatformStore) ReassignProfile(ctx context.Context, fromID, toID agentcore.MemoryProfileID) (ProfileReassignmentReport, error) {
	if s == nil || s.store == nil {
		return ProfileReassignmentReport{}, fmt.Errorf("memory store is required")
	}
	if fromID == "" || toID == "" || fromID == toID {
		return ProfileReassignmentReport{}, nil
	}
	source, err := s.GetProfile(ctx, fromID)
	if err != nil {
		if errors.Is(err, memcore.ErrNotFound) {
			if _, targetErr := s.GetProfile(ctx, toID); targetErr == nil {
				return ProfileReassignmentReport{}, nil
			}
		}
		return ProfileReassignmentReport{}, err
	}
	report := ProfileReassignmentReport{ProfileMoved: true}
	targetExists := false
	if target, targetErr := s.GetProfile(ctx, toID); targetErr == nil && target.ID != "" {
		report.TargetExisted = true
		targetExists = true
	} else if targetErr != nil && !errors.Is(targetErr, memcore.ErrNotFound) {
		return report, targetErr
	}
	if !targetExists {
		source.ID = toID
		if err := s.SaveProfile(ctx, source); err != nil {
			return report, err
		}
	}
	records, err := s.FindRecords(ctx, RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{fromID}, IncludeArchived: true})
	if err != nil {
		return report, err
	}
	for _, record := range records {
		record.ProfileID = toID
		if err := s.saveRecordUnchecked(ctx, record); err != nil {
			return report, err
		}
		report.RecordsMoved++
	}
	if err := s.store.DeleteRecord(ctx, profileTable, string(fromID)); err != nil {
		return report, err
	}
	return report, nil
}

// ReassignOrganizationScope moves memory ownership and organization-scoped
// retrieval metadata without changing historical memory and candidate IDs.
func (s *PlatformStore) ReassignOrganizationScope(ctx context.Context, fromID, toID string) (OrganizationScopeMigrationReport, error) {
	if s == nil || s.store == nil {
		return OrganizationScopeMigrationReport{}, fmt.Errorf("memory store is required")
	}
	fromID, toID = strings.TrimSpace(fromID), strings.TrimSpace(toID)
	if fromID == "" || toID == "" || fromID == toID {
		return OrganizationScopeMigrationReport{}, nil
	}
	report := OrganizationScopeMigrationReport{}
	profiles, err := s.ListProfiles(ctx)
	if err != nil {
		return report, err
	}
	legacyProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: fromID})
	targetProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: toID})
	for _, profile := range profiles {
		changed := false
		if profile.ID == legacyProfileID {
			profile.ID = targetProfileID
			changed = true
		}
		if profile.OwnerRef.OrganizationID == fromID {
			profile.OwnerRef.OrganizationID = toID
			changed = true
		}
		if profile.DefaultScope.Kind == memcore.ScopeOrganization && profile.DefaultScope.ID == fromID {
			profile.DefaultScope.ID = toID
			changed = true
		}
		if profile.VisibilityPolicy.ScopeKind == memcore.ScopeOrganization && profile.VisibilityPolicy.ScopeID == fromID {
			profile.VisibilityPolicy.ScopeID = toID
			changed = true
		}
		if !changed {
			continue
		}
		if err := s.SaveProfile(ctx, profile); err != nil {
			return report, err
		}
		report.Profiles++
	}
	candidates, err := s.ListCandidates(ctx, CandidateQuery{})
	if err != nil {
		return report, err
	}
	for _, candidate := range candidates {
		changed := false
		if candidate.ProposedScope.Kind == memcore.ScopeOrganization && candidate.ProposedScope.ID == fromID {
			candidate.ProposedScope.ID = toID
			changed = true
		}
		candidate.Tags, changed = reassignTenantTag(candidate.Tags, fromID, toID, changed)
		if !changed {
			continue
		}
		if err := s.SaveCandidate(ctx, candidate); err != nil {
			return report, err
		}
		report.Candidates++
	}
	records, err := s.FindRecords(ctx, RecordQuery{IncludeArchived: true})
	if err != nil {
		return report, err
	}
	for _, record := range records {
		changed := false
		if record.ProfileID == legacyProfileID {
			record.ProfileID = targetProfileID
			changed = true
		}
		if record.Scope.Kind == memcore.ScopeOrganization && record.Scope.ID == fromID {
			record.Scope.ID = toID
			changed = true
		}
		if record.Visibility.ScopeKind == memcore.ScopeOrganization && record.Visibility.ScopeID == fromID {
			record.Visibility.ScopeID = toID
			changed = true
		}
		record.Tags, changed = reassignTenantTag(record.Tags, fromID, toID, changed)
		if !changed {
			continue
		}
		if err := s.saveRecordUnchecked(ctx, record); err != nil {
			return report, err
		}
		report.Records++
	}
	if legacyProfileID != targetProfileID {
		if err := s.store.DeleteRecord(ctx, profileTable, string(legacyProfileID)); err != nil {
			return report, err
		}
	}
	return report, nil
}

func reassignTenantTag(tags []string, fromID, toID string, changed bool) ([]string, bool) {
	fromTag := "tenant_id:" + fromID
	toTag := "tenant_id:" + toID
	for index, tag := range tags {
		if strings.TrimSpace(tag) != fromTag {
			continue
		}
		tags[index] = toTag
		changed = true
	}
	return tags, changed
}

func (s *PlatformStore) SaveRecord(ctx context.Context, record memcore.MemoryRecord) error {
	if err := s.requireAcceptedCandidate(ctx, record); err != nil {
		return err
	}
	return s.saveRecordUnchecked(ctx, record)
}

func (s *PlatformStore) saveRecordUnchecked(ctx context.Context, record memcore.MemoryRecord) error {
	if !memcore.ValidIdentifier(record.ID) {
		return fmt.Errorf("%w: memory_id must be between 1 and %d bytes", memcore.ErrInvalid, memcore.MaxIdentifierBytes)
	}
	record = record.Normalized()
	payload, err := json.Marshal(record.Clone())
	if err != nil {
		return fmt.Errorf("encode memory record: %w", err)
	}
	return s.store.PutRecord(ctx, recordTable, record.ID, db.Record{
		"candidate_id":       record.CandidateID,
		"profile_id":         string(record.ProfileID),
		"scope_kind":         string(record.Scope.Kind),
		"scope_id":           record.Scope.ID,
		"layer":              string(record.Layer),
		"topic":              record.Topic,
		"lifecycle_status":   string(record.LifecycleStatus),
		"visibility_mode":    string(record.Visibility.Mode),
		"sensitivity":        string(record.Sensitivity),
		"created_at":         record.CreatedAt,
		"updated_at":         record.UpdatedAt,
		"last_referenced_at": record.LastReferencedAt,
		"payload":            string(payload),
	})
}

func (s *PlatformStore) WithRecordWriteTx(ctx context.Context, recordKey string, fn func(RecordWriteTx) error) error {
	if s == nil || s.store == nil || fn == nil {
		return fmt.Errorf("memory record transaction is not configured")
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, recordTable, recordKey); err != nil {
			return err
		}
		return fn(platformRecordWriteTx{tx: tx})
	})
}

type platformRecordWriteTx struct {
	tx db.Tx
}

func (t platformRecordWriteTx) GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error) {
	record, ok, err := db.GetRecordTx(ctx, t.tx, candidateTable, strings.TrimSpace(id))
	if err != nil {
		return memcore.MemoryCandidate{}, err
	}
	if !ok {
		return memcore.MemoryCandidate{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	return decodeCandidate(record)
}

func (t platformRecordWriteTx) GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error) {
	record, ok, err := db.GetRecordTx(ctx, t.tx, recordTable, strings.TrimSpace(id))
	if err != nil {
		return memcore.MemoryRecord{}, err
	}
	if !ok {
		return memcore.MemoryRecord{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return memcore.MemoryRecord{}, fmt.Errorf("%w: record payload missing", memcore.ErrInvalid)
	}
	var memoryRecord memcore.MemoryRecord
	if err := json.Unmarshal([]byte(raw), &memoryRecord); err != nil {
		return memcore.MemoryRecord{}, fmt.Errorf("decode memory record: %w", err)
	}
	return memoryRecord, nil
}

func (t platformRecordWriteTx) SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error {
	if !memcore.ValidIdentifier(candidate.ID) {
		return fmt.Errorf("%w: candidate_id must be between 1 and %d bytes", memcore.ErrInvalid, memcore.MaxIdentifierBytes)
	}
	candidate.ProposedLayer = memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
	candidate.Sensitivity = memcore.NormalizeSensitivity(candidate.Sensitivity)
	candidate.DeIDLevel = memcore.NormalizeDeIDLevel(candidate.DeIDLevel)
	candidate.Consent = memcore.NormalizeConsentState(candidate.Consent)
	payload, err := json.Marshal(cloneCandidate(candidate))
	if err != nil {
		return fmt.Errorf("encode memory candidate: %w", err)
	}
	return db.PutRecordTx(ctx, t.tx, candidateTable, candidate.ID, db.Record{
		"mission_id": candidate.MissionID, "status": string(candidate.Status), "scope_kind": string(candidate.ProposedScope.Kind),
		"scope_id": candidate.ProposedScope.ID, "layer": string(candidate.ProposedLayer), "topic": candidate.Topic,
		"review_required": candidate.ReviewRequired, "payload": string(payload),
	})
}

func (t platformRecordWriteTx) SaveRecord(ctx context.Context, record memcore.MemoryRecord) error {
	if !memcore.ValidIdentifier(record.ID) {
		return fmt.Errorf("%w: memory_id must be between 1 and %d bytes", memcore.ErrInvalid, memcore.MaxIdentifierBytes)
	}
	candidate, err := t.GetCandidate(ctx, record.CandidateID)
	if err != nil {
		return err
	}
	if !candidate.CanCreateRecordDirectly() {
		return fmt.Errorf("%w: candidate %s status %s", memcore.ErrCandidateRequired, candidate.ID, candidate.Status)
	}
	record = record.Normalized()
	payload, err := json.Marshal(record.Clone())
	if err != nil {
		return fmt.Errorf("encode memory record: %w", err)
	}
	return db.PutRecordTx(ctx, t.tx, recordTable, record.ID, db.Record{
		"candidate_id": record.CandidateID, "profile_id": string(record.ProfileID), "scope_kind": string(record.Scope.Kind),
		"scope_id": record.Scope.ID, "layer": string(record.Layer), "topic": record.Topic,
		"lifecycle_status": string(record.LifecycleStatus), "visibility_mode": string(record.Visibility.Mode),
		"sensitivity": string(record.Sensitivity), "created_at": record.CreatedAt, "updated_at": record.UpdatedAt,
		"last_referenced_at": record.LastReferencedAt, "payload": string(payload),
	})
}

func (s *PlatformStore) requireAcceptedCandidate(ctx context.Context, record memcore.MemoryRecord) error {
	if record.CandidateID == "" {
		return fmt.Errorf("%w: record %s has no candidate_id", memcore.ErrCandidateRequired, record.ID)
	}
	candidate, err := s.GetCandidate(ctx, record.CandidateID)
	if err != nil {
		return err
	}
	if !candidate.CanCreateRecordDirectly() {
		return fmt.Errorf("%w: candidate %s status %s", memcore.ErrCandidateRequired, candidate.ID, candidate.Status)
	}
	return nil
}

func (s *PlatformStore) SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error {
	if !memcore.ValidIdentifier(candidate.ID) {
		return fmt.Errorf("%w: candidate_id must be between 1 and %d bytes", memcore.ErrInvalid, memcore.MaxIdentifierBytes)
	}
	candidate.ProposedLayer = memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
	candidate.Sensitivity = memcore.NormalizeSensitivity(candidate.Sensitivity)
	candidate.DeIDLevel = memcore.NormalizeDeIDLevel(candidate.DeIDLevel)
	candidate.Consent = memcore.NormalizeConsentState(candidate.Consent)
	payload, err := json.Marshal(cloneCandidate(candidate))
	if err != nil {
		return fmt.Errorf("encode memory candidate: %w", err)
	}
	return s.store.PutRecord(ctx, candidateTable, candidate.ID, db.Record{
		"mission_id":      candidate.MissionID,
		"status":          string(candidate.Status),
		"scope_kind":      string(candidate.ProposedScope.Kind),
		"scope_id":        candidate.ProposedScope.ID,
		"layer":           string(candidate.ProposedLayer),
		"topic":           candidate.Topic,
		"review_required": candidate.ReviewRequired,
		"payload":         string(payload),
	})
}

func (s *PlatformStore) GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error) {
	record, ok, err := s.store.GetRecord(ctx, candidateTable, id)
	if err != nil {
		return memcore.MemoryCandidate{}, err
	}
	if !ok {
		return memcore.MemoryCandidate{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	return decodeCandidate(record)
}

func (s *PlatformStore) ListCandidates(ctx context.Context, query CandidateQuery) ([]memcore.MemoryCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	table, err := s.store.ListRecordTable(ctx, candidateTable)
	if err != nil {
		return nil, err
	}
	out := make([]memcore.MemoryCandidate, 0, len(table))
	for _, record := range table {
		candidate, err := decodeCandidate(record)
		if err != nil {
			return nil, err
		}
		if query.MissionID != "" && candidate.MissionID != query.MissionID {
			continue
		}
		if query.Status != "" && candidate.Status != query.Status {
			continue
		}
		if query.ScopeKind != "" && candidate.ProposedScope.Kind != query.ScopeKind {
			continue
		}
		if query.ScopeID != "" && candidate.ProposedScope.ID != query.ScopeID {
			continue
		}
		candidate.ProposedLayer = memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
		if query.Layer != "" && candidate.ProposedLayer != query.Layer {
			continue
		}
		if query.Topic != "" && candidate.Topic != query.Topic {
			continue
		}
		if query.ReviewRequired != nil && candidate.ReviewRequired != *query.ReviewRequired {
			continue
		}
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func (s *PlatformStore) UpdateCandidateStatus(ctx context.Context, id string, status memcore.CandidateStatus) error {
	candidate, err := s.GetCandidate(ctx, id)
	if err != nil {
		return err
	}
	candidate.Status = status
	return s.SaveCandidate(ctx, candidate)
}

func (s *PlatformStore) UpdateRecordLifecycle(ctx context.Context, id string, status memcore.MemoryLifecycleStatus) error {
	record, ok, err := s.store.GetRecord(ctx, recordTable, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("%w: record payload missing", memcore.ErrInvalid)
	}
	var memoryRecord memcore.MemoryRecord
	if err := json.Unmarshal([]byte(raw), &memoryRecord); err != nil {
		return fmt.Errorf("decode memory record: %w", err)
	}
	memoryRecord.LifecycleStatus = memcore.NormalizeMemoryLifecycleStatus(status)
	memoryRecord.UpdatedAt = time.Now().UTC()
	return s.SaveRecord(ctx, memoryRecord)
}

func (s *PlatformStore) GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return memcore.MemoryRecord{}, err
	}
	record, ok, err := s.store.GetRecord(ctx, recordTable, strings.TrimSpace(id))
	if err != nil {
		return memcore.MemoryRecord{}, err
	}
	if !ok {
		return memcore.MemoryRecord{}, fmt.Errorf("%w: %s", memcore.ErrNotFound, id)
	}
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return memcore.MemoryRecord{}, fmt.Errorf("%w: record payload missing", memcore.ErrInvalid)
	}
	var memoryRecord memcore.MemoryRecord
	if err := json.Unmarshal([]byte(raw), &memoryRecord); err != nil {
		return memcore.MemoryRecord{}, fmt.Errorf("decode memory record: %w", err)
	}
	return memoryRecord, nil
}

func (s *PlatformStore) FindRecords(ctx context.Context, query RecordQuery) ([]memcore.MemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	table, err := s.store.ListRecordTable(ctx, recordTable)
	if err != nil {
		return nil, err
	}
	profileSet := map[agentcore.MemoryProfileID]struct{}{}
	for _, id := range query.ProfileIDs {
		profileSet[id] = struct{}{}
	}
	scopeKindSet := scopeKindSet(query.ScopeKinds)
	scopeIDSet := stringSet(query.ScopeIDs)
	layerSet := memoryLayerSet(query.Layers)
	topicSet := stringSet(query.Topics)
	tagSet := stringSet(query.Tags)
	lifecycleStatusSet := lifecycleStatusSet(query.LifecycleStatuses)
	out := make([]memcore.MemoryRecord, 0, len(table))
	for _, record := range table {
		raw, ok := record["payload"].(string)
		if !ok || raw == "" {
			return nil, fmt.Errorf("%w: record payload missing", memcore.ErrInvalid)
		}
		var memoryRecord memcore.MemoryRecord
		if err := json.Unmarshal([]byte(raw), &memoryRecord); err != nil {
			return nil, fmt.Errorf("decode memory record: %w", err)
		}
		memoryRecord = memoryRecord.Normalized()
		if len(profileSet) > 0 {
			if _, ok := profileSet[memoryRecord.ProfileID]; !ok {
				continue
			}
		}
		if len(scopeKindSet) > 0 {
			if _, ok := scopeKindSet[memoryRecord.Scope.Kind]; !ok {
				continue
			}
		}
		if len(scopeIDSet) > 0 {
			if _, ok := scopeIDSet[memoryRecord.Scope.ID]; !ok {
				continue
			}
		}
		if len(layerSet) > 0 {
			if _, ok := layerSet[memoryRecord.Layer]; !ok {
				continue
			}
		}
		if (len(topicSet) > 0 || len(tagSet) > 0) && !matchesTopicOrTag(memoryRecord, topicSet, tagSet) {
			continue
		}
		if len(lifecycleStatusSet) > 0 {
			if _, ok := lifecycleStatusSet[memoryRecord.LifecycleStatus]; !ok {
				continue
			}
		}
		if !query.IncludeArchived && excludedFromMainRetrieval(memoryRecord.LifecycleStatus) {
			continue
		}
		if query.MinConfidence > 0 && memoryRecord.Confidence < query.MinConfidence {
			continue
		}
		out = append(out, memoryRecord.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func excludedFromMainRetrieval(status memcore.MemoryLifecycleStatus) bool {
	switch memcore.NormalizeMemoryLifecycleStatus(status) {
	case memcore.MemoryLifecycleArchived, memcore.MemoryLifecycleDeleted:
		return true
	default:
		return false
	}
}

func matchesTopicOrTag(record memcore.MemoryRecord, topicSet, tagSet map[string]struct{}) bool {
	if len(topicSet) > 0 {
		if _, ok := topicSet[record.Topic]; ok {
			return true
		}
	}
	if len(tagSet) > 0 {
		for _, tag := range record.Tags {
			if _, ok := tagSet[tag]; ok {
				return true
			}
		}
	}
	return false
}

func scopeKindSet(values []memcore.ScopeKind) map[memcore.ScopeKind]struct{} {
	out := make(map[memcore.ScopeKind]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func memoryLayerSet(values []memcore.MemoryLayer) map[memcore.MemoryLayer]struct{} {
	out := make(map[memcore.MemoryLayer]struct{}, len(values))
	for _, value := range values {
		out[memcore.NormalizeMemoryLayer(value)] = struct{}{}
	}
	return out
}

func lifecycleStatusSet(values []memcore.MemoryLifecycleStatus) map[memcore.MemoryLifecycleStatus]struct{} {
	out := make(map[memcore.MemoryLifecycleStatus]struct{}, len(values))
	for _, value := range values {
		out[memcore.NormalizeMemoryLifecycleStatus(value)] = struct{}{}
	}
	return out
}

func decodeCandidate(record db.Record) (memcore.MemoryCandidate, error) {
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return memcore.MemoryCandidate{}, fmt.Errorf("%w: candidate payload missing", memcore.ErrInvalid)
	}
	var candidate memcore.MemoryCandidate
	if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
		return memcore.MemoryCandidate{}, fmt.Errorf("decode memory candidate: %w", err)
	}
	candidate.ProposedLayer = memcore.NormalizeMemoryLayer(candidate.ProposedLayer)
	candidate.Sensitivity = memcore.NormalizeSensitivity(candidate.Sensitivity)
	candidate.DeIDLevel = memcore.NormalizeDeIDLevel(candidate.DeIDLevel)
	candidate.Consent = memcore.NormalizeConsentState(candidate.Consent)
	return cloneCandidate(candidate), nil
}
