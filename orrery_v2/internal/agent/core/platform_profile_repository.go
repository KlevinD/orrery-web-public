package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const (
	profileRecordTable       = "agent_profiles"
	availabilityRecordTable  = "agent_availability"
	profileDeletionTable     = "agent_profile_deletions"
	costProfileRecordTable   = "agent_cost_profiles"
	qualitySignalRecordTable = "agent_quality_signals"
	riskProfileRecordTable   = "agent_risk_profiles"
)

type PlatformProfileRepository struct {
	unit      *uow.UnitOfWork
	saveHooks []ProfileSaveHook
}

type ProfileSaveHook interface {
	BeforeProfileSave(ctx context.Context, tx uow.Tx, previous *AgentProfile, current AgentProfile) error
}

type ProfileMutation func(current AgentProfile) (updated AgentProfile, changed bool, err error)

func NewPlatformProfileRepository(unit *uow.UnitOfWork) *PlatformProfileRepository {
	return &PlatformProfileRepository{unit: unit}
}

func (r *PlatformProfileRepository) WithProfileSaveHook(hook ProfileSaveHook) *PlatformProfileRepository {
	if r != nil && hook != nil {
		r.saveHooks = append(r.saveHooks, hook)
	}
	return r
}

func (r *PlatformProfileRepository) GetProfile(ctx context.Context, id AgentProfileID) (AgentProfile, error) {
	out, found, err := r.FindProfile(ctx, id)
	if err != nil {
		return AgentProfile{}, err
	}
	if !found {
		return AgentProfile{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return out, nil
}

// FindProfile distinguishes a missing profile from database and decoding failures.
func (r *PlatformProfileRepository) FindProfile(ctx context.Context, id AgentProfileID) (AgentProfile, bool, error) {
	var out AgentProfile
	var found bool
	err := r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(profileRecordTable, id.String())
		found = ok
		if !ok {
			return nil
		}
		return decodeProfileRecord(record, &out)
	})
	return out, found && err == nil, err
}

func (r *PlatformProfileRepository) GetProfiles(ctx context.Context, ids []AgentProfileID) (map[AgentProfileID]AgentProfile, error) {
	wanted := make(map[AgentProfileID]struct{}, len(ids))
	for _, id := range ids {
		if !id.IsZero() {
			wanted[id] = struct{}{}
		}
	}
	profiles := make(map[AgentProfileID]AgentProfile, len(wanted))
	err := r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		for id := range wanted {
			record, ok := tx.Get(profileRecordTable, id.String())
			if !ok {
				continue
			}
			var profile AgentProfile
			if err := decodeProfileRecord(record, &profile); err != nil {
				return err
			}
			profiles[id] = profile
		}
		return nil
	})
	return profiles, err
}

func (r *PlatformProfileRepository) QueryDirectory(ctx context.Context, q AgentDirectoryQuery) (AgentDirectoryPage, error) {
	var page AgentDirectoryPage
	err := r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		entries, rejected, err := r.directoryEntries(ctx, tx, q)
		if err != nil {
			return err
		}
		sortDirectoryEntries(entries, q.Sort)
		total := len(entries)
		entries = paginateEntries(entries, q.Page)
		page = AgentDirectoryPage{
			Entries:  entries,
			Rejected: rejected,
			Total:    total,
			Limit:    q.Page.Limit,
			Offset:   q.Page.Offset,
		}
		return nil
	})
	return page, err
}

func (r *PlatformProfileRepository) GetAvailability(ctx context.Context, id AgentProfileID) (AgentAvailability, error) {
	var out AgentAvailability
	err := r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(availabilityRecordTable, id.String())
		if !ok {
			out = AgentAvailability{AgentProfileID: id, Status: AvailabilityUnknown}
			return nil
		}
		return decodeProfileRecord(record, &out)
	})
	return out, err
}

func (r *PlatformProfileRepository) GetDirectoryEntry(ctx context.Context, id AgentProfileID) (AgentDirectoryEntry, error) {
	var out AgentDirectoryEntry
	err := r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(profileRecordTable, id.String())
		if !ok {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		var profile AgentProfile
		if err := decodeProfileRecord(record, &profile); err != nil {
			return err
		}
		availability := profileRecordsByKeyTx(tx, availabilityRecordTable, id.String())
		costs := profileRecordsByKeyTx(tx, costProfileRecordTable, id.String())
		quality := profileRecordsByKeyTx(tx, qualitySignalRecordTable, id.String())
		risks := profileRecordsByKeyTx(tx, riskProfileRecordTable, id.String())
		entry, entryErr := r.entryForProfile(profile, availability, costs, quality, risks)
		if entryErr != nil {
			return entryErr
		}
		out = entry
		return nil
	})
	return out, err
}

func profileRecordsByKeyTx(tx uow.Tx, table, key string) map[string]db.Record {
	if record, ok := tx.Get(table, key); ok {
		return map[string]db.Record{key: record}
	}
	return nil
}

func (r *PlatformProfileRepository) SaveProfile(ctx context.Context, profile AgentProfile) error {
	if errs := ValidateAgentProfile(profile); len(errs) > 0 {
		return AgentProfileValidationError{Errors: errs}
	}
	return r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		var previous *AgentProfile
		if record, ok := tx.Get(profileRecordTable, profile.ID.String()); ok {
			var stored AgentProfile
			if err := decodeProfileRecord(record, &stored); err != nil {
				return err
			}
			previous = &stored
		}
		return r.saveProfileTx(ctx, tx, previous, profile)
	})
}

// MutateProfile serializes bounded mutations of the same profile and loads
// current state inside the transaction before applying the callback.
func (r *PlatformProfileRepository) MutateProfile(ctx context.Context, id AgentProfileID, mutation ProfileMutation) (AgentProfile, bool, error) {
	if r == nil || id.IsZero() || mutation == nil {
		return AgentProfile{}, false, fmt.Errorf("profile mutation requires a repository, profile id, and mutation")
	}
	var out AgentProfile
	changed := false
	err := r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, profileRecordTable, id.String()); err != nil {
			return err
		}
		record, ok := tx.Get(profileRecordTable, id.String())
		if !ok {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		var current AgentProfile
		if err := decodeProfileRecord(record, &current); err != nil {
			return err
		}
		updated, shouldSave, err := mutation(current)
		if err != nil {
			return err
		}
		if !shouldSave {
			out = current
			return nil
		}
		if updated.ID != id {
			return fmt.Errorf("profile mutation cannot change profile id")
		}
		if errs := ValidateAgentProfile(updated); len(errs) > 0 {
			return AgentProfileValidationError{Errors: errs}
		}
		previous := current
		if err := r.saveProfileTx(ctx, tx, &previous, updated); err != nil {
			return err
		}
		out, changed = updated, true
		return nil
	})
	return out, changed, err
}

func (r *PlatformProfileRepository) saveProfileTx(ctx context.Context, tx uow.Tx, previous *AgentProfile, profile AgentProfile) error {
	for _, hook := range r.saveHooks {
		if err := hook.BeforeProfileSave(ctx, tx, previous, profile); err != nil {
			return err
		}
	}
	payload, err := encodeProfilePayload(profile)
	if err != nil {
		return err
	}
	tx.Put(profileRecordTable, profile.ID.String(), db.Record{
		"profile_id": profile.ID.String(),
		"enabled":    profile.Enabled,
		"kind":       string(profile.AgentKind),
		"payload":    payload,
	})
	return deletePlatformRecord(ctx, tx, profileDeletionTable, profile.ID.String())
}

type AgentProfileValidationError struct {
	Errors []ValidationError
}

func (e AgentProfileValidationError) Error() string {
	return "agent profile validation failed"
}

func (r *PlatformProfileRepository) SaveAvailability(ctx context.Context, availability AgentAvailability) error {
	return r.saveSignal(ctx, availabilityRecordTable, availability.AgentProfileID.String(), availability)
}

func (r *PlatformProfileRepository) SaveCostProfile(ctx context.Context, cost AgentCostProfile) error {
	return r.saveSignal(ctx, costProfileRecordTable, cost.AgentProfileID.String(), cost)
}

func (r *PlatformProfileRepository) SaveQualitySignal(ctx context.Context, quality AgentQualitySignal) error {
	return r.saveSignal(ctx, qualitySignalRecordTable, quality.AgentProfileID.String(), quality)
}

func (r *PlatformProfileRepository) SaveRiskProfile(ctx context.Context, risk AgentRiskProfile) error {
	return r.saveSignal(ctx, riskProfileRecordTable, risk.AgentProfileID.String(), risk)
}

func (r *PlatformProfileRepository) DeleteProfile(ctx context.Context, id AgentProfileID, reason string) error {
	if id.IsZero() {
		return fmt.Errorf("agent profile id is required")
	}
	return r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		key := id.String()
		for _, table := range []string{
			profileRecordTable,
			availabilityRecordTable,
			costProfileRecordTable,
			qualitySignalRecordTable,
			riskProfileRecordTable,
		} {
			if err := deletePlatformRecord(ctx, tx, table, key); err != nil {
				return err
			}
		}
		tx.Put(profileDeletionTable, key, db.Record{
			"profile_id": id.String(),
			"reason":     strings.TrimSpace(reason),
			"deleted_at": time.Now().UTC().Format(time.RFC3339Nano),
		})
		return nil
	})
}

func (r *PlatformProfileRepository) IsProfileDeleted(ctx context.Context, id AgentProfileID) (bool, error) {
	if id.IsZero() {
		return false, nil
	}
	var deleted bool
	err := r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		key := id.String()
		if _, ok := tx.Get(profileDeletionTable, key); ok {
			deleted = true
			return nil
		}
		return nil
	})
	return deleted, err
}

func (r *PlatformProfileRepository) ListDeletedProfileIDs(ctx context.Context) ([]AgentProfileID, error) {
	var out []AgentProfileID
	err := r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := listProfileRecordsTx(ctx, tx, profileDeletionTable)
		if err != nil {
			return err
		}
		out = make([]AgentProfileID, 0, len(records))
		for id := range records {
			if profileID := AgentProfileID(strings.TrimSpace(id)); !profileID.IsZero() {
				out = append(out, profileID)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
		return nil
	})
	return out, err
}

func (r *PlatformProfileRepository) saveSignal(ctx context.Context, table, key string, value any) error {
	return r.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		payload, err := encodeProfilePayload(value)
		if err != nil {
			return err
		}
		tx.Put(table, key, db.Record{"profile_id": key, "payload": payload})
		return nil
	})
}

func (r *PlatformProfileRepository) directoryEntries(ctx context.Context, tx uow.Tx, q AgentDirectoryQuery) ([]AgentDirectoryEntry, []DirectoryRejection, error) {
	profiles, err := listProfileRecordsTx(ctx, tx, profileRecordTable)
	if err != nil {
		return nil, nil, err
	}
	availability, err := listProfileRecordsTx(ctx, tx, availabilityRecordTable)
	if err != nil {
		return nil, nil, err
	}
	costs, err := listProfileRecordsTx(ctx, tx, costProfileRecordTable)
	if err != nil {
		return nil, nil, err
	}
	quality, err := listProfileRecordsTx(ctx, tx, qualitySignalRecordTable)
	if err != nil {
		return nil, nil, err
	}
	risks, err := listProfileRecordsTx(ctx, tx, riskProfileRecordTable)
	if err != nil {
		return nil, nil, err
	}
	entries := make([]AgentDirectoryEntry, 0, len(profiles))
	rejected := make([]DirectoryRejection, 0)
	for _, record := range profiles {
		var profile AgentProfile
		if err := decodeProfileRecord(record, &profile); err != nil {
			return nil, nil, err
		}
		if profile.IsPersonalAssistant() && !q.IncludePersonalAssistants {
			continue
		}
		entry, err := r.entryForProfile(profile, availability, costs, quality, risks)
		if err != nil {
			return nil, nil, err
		}
		if reason, ok := directoryRejectReason(entry, q); ok {
			rejected = append(rejected, reason)
			continue
		}
		entries = append(entries, entry)
	}
	return entries, rejected, nil
}

func (r *PlatformProfileRepository) entryForProfile(profile AgentProfile, availabilityRecords, costRecords, qualityRecords, riskRecords map[string]db.Record) (AgentDirectoryEntry, error) {
	availability := AgentAvailability{AgentProfileID: profile.ID, Status: AvailabilityUnknown}
	if record, ok := availabilityRecords[profile.ID.String()]; ok {
		if err := decodeProfileRecord(record, &availability); err != nil {
			return AgentDirectoryEntry{}, err
		}
	}
	instanceID := AgentInstanceID("")
	if availability.Status != AvailabilityUnknown || !availability.AgentInstanceID.IsZero() {
		instanceID = AgentInstanceID(profile.ID)
	}
	cost := AgentCostProfile{AgentProfileID: profile.ID}
	if record, ok := costRecords[profile.ID.String()]; ok {
		if err := decodeProfileRecord(record, &cost); err != nil {
			return AgentDirectoryEntry{}, err
		}
	}
	quality := AgentQualitySignal{AgentProfileID: profile.ID}
	if record, ok := qualityRecords[profile.ID.String()]; ok {
		if err := decodeProfileRecord(record, &quality); err != nil {
			return AgentDirectoryEntry{}, err
		}
	}
	risk := AgentRiskProfile{AgentProfileID: profile.ID, RiskLevel: RiskLevelUnknown}
	if record, ok := riskRecords[profile.ID.String()]; ok {
		if err := decodeProfileRecord(record, &risk); err != nil {
			return AgentDirectoryEntry{}, err
		}
	}
	return AgentDirectoryEntry{
		AgentProfileID:            profile.ID,
		PersonalAssistantOwnerID:  strings.TrimSpace(profile.PersonalAssistantOwnerID),
		PersonalAssistantTenantID: strings.TrimSpace(profile.PersonalAssistantTenantID),
		AgentInstanceID:           instanceID,
		RealName:                  profile.RealName,
		DisplayName:               profile.DisplayName,
		RoleName:                  profile.RoleName,
		Email:                     strings.TrimSpace(profile.Email),
		AgentKind:                 profile.AgentKind,
		OrgIdentity:               profile.OrgIdentity,
		Placement:                 profile.Placement,
		PersonaSummary:            personaSummary(profile.Persona),
		BehaviorBoundary:          strings.TrimSpace(profile.Persona.BehaviorBoundary),
		SkillRefs:                 append([]SkillRef(nil), profile.SkillRefs...),
		ToolRefs:                  append([]ToolRef(nil), profile.ToolRefs...),
		RuntimeSpecSummary:        RuntimeSpecSummary{Kind: profile.RuntimeSpec.Kind, ConfigRef: profile.RuntimeSpec.ConfigRef},
		BaseAgentSummary:          baseAgentSummary(profile.BaseAgent),
		KnowledgeBaseID:           profile.PersonalKnowledgeBaseID,
		MemoryProfileRef:          profile.PersonalMemoryProfileRef,
		Availability:              availability,
		CostProfile:               cost,
		QualitySignal:             quality,
		RiskProfile:               risk,
		QAEnabled:                 profile.QAEnabled,
		Enabled:                   profile.Enabled,
		UpdatedAt:                 availability.UpdatedAt,
	}, nil
}

func listProfileRecordsTx(ctx context.Context, tx uow.Tx, table string) (map[string]db.Record, error) {
	records := make(map[string]db.Record)
	afterKey := ""
	for {
		page, err := db.ListRecordsTx(ctx, tx, table, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			records[item.Key] = item.Record
			afterKey = item.Key
		}
		if !page.HasMore {
			return records, nil
		}
	}
}

func baseAgentSummary(spec BaseAgentSpec) BaseAgentSummary {
	return BaseAgentSummary{
		InheritsBaseAgent: spec.InheritsBaseAgent,
		AuditRequired:     spec.AuditRequired,
		HasSystemPrompt:   strings.TrimSpace(spec.SystemPrompt) != "",
		HasLLMAPIKeyRef:   strings.TrimSpace(spec.LLMAPIKeyRef) != "",
		LLMAPIKeyRef:      spec.LLMAPIKeyRef,
		SkillScope:        spec.SkillScope,
		ToolScope:         spec.ToolScope,
		MCPScope:          spec.MCPScope,
	}
}

func (r *PlatformProfileRepository) withTx(ctx context.Context, fn func(context.Context, uow.Tx) error) error {
	if r == nil || r.unit == nil {
		return fmt.Errorf("agent profile repository requires unit of work")
	}
	return r.unit.Do(ctx, fn)
}

func deletePlatformRecord(ctx context.Context, tx uow.Tx, table, key string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`, table, key)
	if err != nil {
		return fmt.Errorf("delete platform record %s/%s: %w", table, key, err)
	}
	return nil
}

func directoryRejectReason(entry AgentDirectoryEntry, q AgentDirectoryQuery) (DirectoryRejection, bool) {
	reject := func(code, reason string) (DirectoryRejection, bool) {
		return DirectoryRejection{AgentProfileID: entry.AgentProfileID, AgentInstanceID: entry.AgentInstanceID, Code: code, Reason: reason}, true
	}
	switch q.Placement {
	case TalentPlacementFilterAll:
	case TalentPlacementFilterMarket:
		if !entry.Placement.IsMarket(entry.OrgIdentity) {
			return reject("placement_mismatch", "talent placement does not match query")
		}
	default:
		if !entry.Placement.IsInternal(entry.OrgIdentity) {
			return reject("placement_mismatch", "talent placement does not match query")
		}
	}
	if !entry.AgentKind.IsValid() {
		return reject("candidate_not_agent", "directory candidate is not an agent profile")
	}
	if q.EnabledOnly && !entry.Enabled {
		return reject("profile_disabled", "profile is disabled")
	}
	if q.Organization != "" && entry.OrgIdentity.OrganizationRef.ID != q.Organization {
		return reject("organization_mismatch", "organization does not match query")
	}
	if q.Department != "" && entry.OrgIdentity.DepartmentRef.ID != q.Department {
		return reject("department_mismatch", "department does not match query")
	}
	if q.Position != "" && entry.OrgIdentity.PositionRef.ID != q.Position {
		return reject("position_mismatch", "position does not match query")
	}
	if len(q.Roles) > 0 && !hasAnyRole(entry.OrgIdentity.RoleRefs, q.Roles) {
		return reject("role_mismatch", "role does not match query")
	}
	if q.Manager != "" && entry.OrgIdentity.ManagerAgentRef.ID != q.Manager {
		return reject("manager_mismatch", "manager does not match query")
	}
	if q.ReportingLine != "" && entry.OrgIdentity.ReportingLineRef.ID != q.ReportingLine {
		return reject("reporting_line_mismatch", "reporting line does not match query")
	}
	if q.AgentKind != "" && entry.AgentKind != q.AgentKind {
		return reject("agent_kind_mismatch", "agent kind does not match query")
	}
	if q.EmploymentType != "" && entry.OrgIdentity.EmploymentType != q.EmploymentType {
		return reject("employment_type_mismatch", "employment type does not match query")
	}
	if q.IncludeAI && !q.IncludeHumans && entry.AgentKind != AgentKindAI {
		return reject("agent_kind_mismatch", "query only includes AI agents")
	}
	if q.IncludeHumans && !q.IncludeAI && entry.AgentKind != AgentKindHuman {
		return reject("agent_kind_mismatch", "query only includes human agents")
	}
	if len(q.SkillRefs) > 0 && !hasSkillRefs(entry.SkillRefs, q.SkillRefs) {
		return reject("skill_missing", "required skill refs are missing")
	}
	if len(q.ToolRefs) > 0 && !hasToolRefs(entry.ToolRefs, q.ToolRefs) {
		return reject("tool_missing", "required tool refs are missing")
	}
	if q.RuntimeKind != "" && entry.RuntimeSpecSummary.Kind != q.RuntimeKind {
		return reject("runtime_not_available", "runtime kind does not match query")
	}
	if q.MemoryKind != "" && entry.MemoryProfileRef.MemoryKind != q.MemoryKind {
		return reject("memory_scope_denied", "memory kind does not match query")
	}
	if q.AuthorityScope != "" && !authorityAllows(entry.OrgIdentity.AuthorityScope, q.AuthorityScope) {
		return reject("authority_denied", "authority scope does not satisfy query")
	}
	if !q.AvailabilityWindow.From.IsZero() || !q.AvailabilityWindow.Until.IsZero() {
		if !availabilityCovers(entry.Availability, q.AvailabilityWindow) {
			return reject("availability_denied", "availability window is not covered")
		}
	}
	if q.MaxCost > 0 && entry.CostProfile.EstimatedCostPerHour > q.MaxCost {
		return reject("budget_denied", "candidate cost exceeds max cost")
	}
	if q.MinQualityScore > 0 && entry.QualitySignal.Score < q.MinQualityScore {
		return reject("quality_denied", "candidate quality score is below minimum")
	}
	if len(q.AllowedRiskLevels) > 0 && !riskAllowed(entry.RiskProfile.RiskLevel, q.AllowedRiskLevels) {
		return reject("risk_denied", "candidate risk level is not allowed")
	}
	if profileExcluded(entry.AgentProfileID, q.ExcludeAgentProfiles) {
		return reject("profile_excluded", "agent profile is excluded")
	}
	return DirectoryRejection{}, false
}

func hasAnyRole(have []RoleRef, want []RoleID) bool {
	set := map[RoleID]struct{}{}
	for _, role := range have {
		set[role.ID] = struct{}{}
	}
	for _, role := range want {
		if _, ok := set[role]; ok {
			return true
		}
	}
	return false
}

func hasSkillRefs(have []SkillRef, want []SkillRef) bool {
	set := map[SkillID]struct{}{}
	for _, ref := range have {
		set[ref.SkillID] = struct{}{}
	}
	for _, ref := range want {
		if _, ok := set[ref.SkillID]; !ok {
			return false
		}
	}
	return true
}

func hasToolRefs(have []ToolRef, want []ToolRef) bool {
	set := map[ToolID]struct{}{}
	for _, ref := range have {
		set[ref.ToolID] = struct{}{}
	}
	for _, ref := range want {
		if _, ok := set[ref.ToolID]; !ok {
			return false
		}
	}
	return true
}

func authorityAllows(have, want string) bool {
	if strings.TrimSpace(want) == "" {
		return true
	}
	for _, part := range strings.Split(have, ",") {
		if strings.TrimSpace(part) == strings.TrimSpace(want) {
			return true
		}
	}
	return strings.TrimSpace(have) == strings.TrimSpace(want)
}

func availabilityCovers(availability AgentAvailability, window QueryAvailabilityWindow) bool {
	if availability.Status != AvailabilityAvailable {
		return false
	}
	if !window.From.IsZero() && !availability.AvailableFrom.IsZero() && availability.AvailableFrom.After(window.From) {
		return false
	}
	if !window.Until.IsZero() && !availability.AvailableUntil.IsZero() && availability.AvailableUntil.Before(window.Until) {
		return false
	}
	if availability.Capacity > 0 && availability.CurrentLoad >= availability.Capacity {
		return false
	}
	return true
}

func riskAllowed(level RiskLevel, allowed []RiskLevel) bool {
	for _, candidate := range allowed {
		if candidate == level {
			return true
		}
	}
	return false
}

func profileExcluded(id AgentProfileID, excluded []AgentProfileID) bool {
	for _, item := range excluded {
		if item == id {
			return true
		}
	}
	return false
}

func sortDirectoryEntries(entries []AgentDirectoryEntry, sortMode DirectorySort) {
	sort.SliceStable(entries, func(i, j int) bool {
		cmp := compareDirectoryEntries(entries[i], entries[j])
		switch sortMode {
		case DirectorySortScoreAsc:
			if entries[i].QualitySignal.Score != entries[j].QualitySignal.Score {
				return entries[i].QualitySignal.Score < entries[j].QualitySignal.Score
			}
			return cmp < 0
		case DirectorySortUpdated:
			if !entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
				return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
			}
			return cmp < 0
		default:
			if entries[i].QualitySignal.Score != entries[j].QualitySignal.Score {
				return entries[i].QualitySignal.Score > entries[j].QualitySignal.Score
			}
			return cmp < 0
		}
	})
}

func compareDirectoryEntries(left AgentDirectoryEntry, right AgentDirectoryEntry) int {
	leftKey := []string{
		strings.TrimSpace(left.DisplayName),
		strings.TrimSpace(left.RealName),
		left.AgentProfileID.String(),
		left.AgentInstanceID.String(),
	}
	rightKey := []string{
		strings.TrimSpace(right.DisplayName),
		strings.TrimSpace(right.RealName),
		right.AgentProfileID.String(),
		right.AgentInstanceID.String(),
	}
	for i := range leftKey {
		if leftKey[i] < rightKey[i] {
			return -1
		}
		if leftKey[i] > rightKey[i] {
			return 1
		}
	}
	return 0
}

func paginateEntries(entries []AgentDirectoryEntry, page Page) []AgentDirectoryEntry {
	if page.Offset >= len(entries) {
		return nil
	}
	start := page.Offset
	if start < 0 {
		start = 0
	}
	end := len(entries)
	if page.Limit > 0 && start+page.Limit < end {
		end = start + page.Limit
	}
	return append([]AgentDirectoryEntry(nil), entries[start:end]...)
}

func personaSummary(persona AgentPersona) string {
	if persona.ResponsibilityBoundary != "" {
		return persona.ResponsibilityBoundary
	}
	return persona.Backstory
}

func encodeProfilePayload(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode agent profile payload: %w", err)
	}
	return string(payload), nil
}

func decodeProfileRecord(record db.Record, out any) error {
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("agent profile repository payload missing")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode agent profile payload: %w", err)
	}
	if profile, ok := out.(*AgentProfile); ok {
		normalizeProfileRuntimeSpec(profile)
	}
	return nil
}

func normalizeProfileRuntimeSpec(profile *AgentProfile) {
	if profile == nil {
		return
	}
	if strings.TrimSpace(string(profile.RuntimeSpec.Config)) == "null" {
		profile.RuntimeSpec.Config = nil
	}
}
