package memorydiary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcapture "orrery_v2/internal/memory/capture"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/graphprotocol"
)

type FlushTrigger string

const (
	TriggerPreCompaction  FlushTrigger = "pre_compaction"
	TriggerRunTerminal    FlushTrigger = "run_terminal"
	diaryTagRecordCreated              = "diary_record_created"
)

type DiaryServiceOptions struct {
	Repository          Repository
	Enabled             EnabledFunc
	State               FlushStateStore
	Audit               memaudit.Recorder
	Metadata            MemoryRecordMetadataSink
	ClassifySensitivity func(string) memcore.Sensitivity
	Now                 func() time.Time
}

type MemoryRecordMetadataSink interface {
	PublishMemoryRecordMetadata(ctx context.Context, event graphprotocol.DomainEvent) error
}

type DiaryService struct {
	repository          Repository
	enabled             EnabledFunc
	state               FlushStateStore
	audit               memaudit.Recorder
	metadata            MemoryRecordMetadataSink
	classifySensitivity func(string) memcore.Sensitivity
	now                 func() time.Time
}

type ReadRequest struct {
	TenantID string
	AgentID  string
	Date     string
	Path     string
}

type ReadResult struct {
	TenantID string
	AgentID  string
	Date     string
	Path     string
	RecordID string
	Content  string
	Exists   bool
}

type AppendRequest struct {
	TenantID          string
	AgentID           string
	Date              string
	Path              string
	Content           string
	SessionID         string
	ContextRevision   string
	Trigger           FlushTrigger
	IdempotencyKey    string
	MissionID         string
	TaskID            string
	RunID             string
	EvidenceRefs      []string
	LineageRefs       []string
	SourceSensitivity memcore.Sensitivity
	Sensitivity       memcore.Sensitivity
}

type AppendReceipt struct {
	TenantID        string
	AgentID         string
	Date            string
	Path            string
	RecordID        string
	CandidateID     string
	Content         string
	ContextRevision string
	Trigger         FlushTrigger
	Replayed        bool
}

// ResolvePersonalMemoryTenant is used by the trusted runtime coordinator to
// bind diary writes to the tenant that owns the Agent's personal profile.
// Public read and append operations still enforce the caller-provided tenant.
func (s *DiaryService) ResolvePersonalMemoryTenant(ctx context.Context, agentID string) (string, error) {
	if s == nil || s.repository == nil {
		return "", ErrServiceDependency
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("%w: agent_id is required", ErrInvalidRequest)
	}
	profiles, err := s.repository.ListProfiles(ctx)
	if err != nil {
		return "", err
	}
	matchedAgent := false
	matchedTenant := ""
	matchedProfiles := 0
	for _, profile := range profiles {
		if strings.TrimSpace(profile.OwnerRef.AgentID.String()) != agentID {
			continue
		}
		matchedAgent = true
		if !profile.Enabled || profile.MemoryKind != agentcore.MemoryKindPersonal {
			continue
		}
		tenantID := strings.TrimSpace(profile.OwnerRef.OrganizationID)
		if tenantID == "" {
			return "", fmt.Errorf("%w: personal Agent memory tenant is empty", ErrInvalidRequest)
		}
		matchedProfiles++
		if matchedTenant == "" {
			matchedTenant = tenantID
		}
		if tenantID != matchedTenant {
			return "", fmt.Errorf("%w: Agent has personal memory profiles in multiple tenants", ErrForbidden)
		}
	}
	if matchedProfiles == 1 {
		return matchedTenant, nil
	}
	if matchedProfiles > 1 {
		return "", fmt.Errorf("%w: Agent has multiple enabled personal memory profiles", ErrForbidden)
	}
	if matchedAgent {
		return "", fmt.Errorf("%w: personal Agent memory is disabled", ErrForbidden)
	}
	return "", fmt.Errorf("%w: Agent personal memory profile", ErrNotFound)
}

type diaryAppendStore interface {
	GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error)
	GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error)
	SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error
	SaveRecord(ctx context.Context, record memcore.MemoryRecord) error
}

func NewDiaryService(opts DiaryServiceOptions) *DiaryService {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	classifier := opts.ClassifySensitivity
	if classifier == nil {
		classifier = defaultDiarySensitivity
	}
	return &DiaryService{
		repository: opts.Repository, enabled: opts.Enabled, state: opts.State, audit: opts.Audit, metadata: opts.Metadata,
		classifySensitivity: classifier, now: now,
	}
}

func (s *DiaryService) Read(ctx context.Context, req ReadRequest) (ReadResult, error) {
	if s == nil || s.repository == nil || s.now == nil {
		return ReadResult{}, ErrServiceDependency
	}
	req = normalizeReadRequest(req)
	if err := validateDiaryTarget(req.TenantID, req.AgentID, req.Date, req.Path); err != nil {
		return ReadResult{}, err
	}
	if err := s.requireEnabled(ctx); err != nil {
		return ReadResult{}, err
	}
	profile, err := s.ownedProfile(ctx, req.TenantID, req.AgentID)
	if err != nil {
		return ReadResult{}, err
	}
	record, found, err := s.findReadableDailyRecord(ctx, profile, req.TenantID, req.AgentID, req.Path, req.Date)
	if err != nil {
		return ReadResult{}, err
	}
	result := ReadResult{TenantID: req.TenantID, AgentID: req.AgentID, Date: req.Date, Path: req.Path, Exists: found}
	if found {
		result.RecordID = record.ID
		result.Content = firstNonBlank(record.Content, record.Narrative)
	}
	return result, nil
}

func (s *DiaryService) Append(ctx context.Context, req AppendRequest) (AppendReceipt, error) {
	if s == nil || s.repository == nil || s.now == nil {
		return AppendReceipt{}, ErrServiceDependency
	}
	req = normalizeAppendRequest(req)
	if err := validateDiaryTarget(req.TenantID, req.AgentID, req.Date, req.Path); err != nil {
		return AppendReceipt{}, err
	}
	if req.Trigger != TriggerPreCompaction && req.Trigger != TriggerRunTerminal {
		return AppendReceipt{}, fmt.Errorf("%w: unsupported trigger %q", ErrInvalidRequest, req.Trigger)
	}
	if req.SessionID == "" || req.ContextRevision == "" || req.IdempotencyKey == "" {
		return AppendReceipt{}, fmt.Errorf("%w: session_id, context_revision, and idempotency_key are required", ErrInvalidRequest)
	}
	if req.Content == "" || strings.EqualFold(req.Content, "NO_REPLY") {
		return AppendReceipt{}, fmt.Errorf("%w: diary content is required and cannot be NO_REPLY", ErrInvalidRequest)
	}
	if err := s.requireEnabled(ctx); err != nil {
		return AppendReceipt{}, err
	}
	profile, err := s.ownedProfile(ctx, req.TenantID, req.AgentID)
	if err != nil {
		return AppendReceipt{}, err
	}
	now := s.now().UTC()
	record, candidate, replayed, created, err := s.persistAppend(ctx, profile, req, now)
	if err != nil {
		return AppendReceipt{}, err
	}
	if err := s.publishAppend(ctx, req, candidate, record, created, now); err != nil {
		return AppendReceipt{}, err
	}
	if err := s.advanceFlushCursor(ctx, req, now); err != nil {
		return AppendReceipt{}, err
	}
	return appendReceipt(req, record, candidate.ID, replayed), nil
}

func (s *DiaryService) persistAppend(ctx context.Context, profile memcore.MemoryProfile, req AppendRequest, now time.Time) (memcore.MemoryRecord, memcore.MemoryCandidate, bool, bool, error) {
	var record memcore.MemoryRecord
	var candidate memcore.MemoryCandidate
	var replayed bool
	var created bool
	write := func(store diaryAppendStore) error {
		var err error
		record, candidate, replayed, created, err = s.persistAppendWithStore(ctx, store, profile, req, now)
		return err
	}
	if transactional, ok := s.repository.(memstore.TransactionalRecordWriter); ok {
		err := transactional.WithRecordWriteTx(ctx, appendRecordID(req), func(tx memstore.RecordWriteTx) error {
			return write(tx)
		})
		return record, candidate, replayed, created, err
	}
	err := write(s.repository)
	return record, candidate, replayed, created, err
}

func (s *DiaryService) persistAppendWithStore(ctx context.Context, store diaryAppendStore, profile memcore.MemoryProfile, req AppendRequest, now time.Time) (memcore.MemoryRecord, memcore.MemoryCandidate, bool, bool, error) {
	candidateID := appendCandidateID(req)
	candidate, candidateFound, err := getAppendCandidate(ctx, store, candidateID)
	if err != nil {
		return memcore.MemoryRecord{}, memcore.MemoryCandidate{}, false, false, err
	}
	existing, found, err := findCanonicalDailyRecord(ctx, store, profile, req)
	if err != nil {
		return memcore.MemoryRecord{}, memcore.MemoryCandidate{}, false, false, err
	}
	if candidateFound {
		if found && diaryRecordContainsCandidate(existing, candidate) {
			return existing, candidate, true, diaryRecordCreatedByCandidate(existing, candidate), nil
		}
		content := firstNonBlank(candidate.Narrative, req.Content)
		sensitivity := highestSensitivity(existing.Sensitivity, candidate.Sensitivity)
		record := buildAppendRecord(profile, req, candidate, existing, found, content, sensitivity, now)
		if err := store.SaveRecord(ctx, record); err != nil {
			return memcore.MemoryRecord{}, memcore.MemoryCandidate{}, false, false, err
		}
		return record, candidate, true, !found, nil
	}
	content := appendMarkdown(existing.Content, req.Content)
	classifiedSensitivity := memcore.SensitivityNormal
	if s.classifySensitivity != nil {
		classifiedSensitivity = s.classifySensitivity(req.Content)
	}
	sensitivity := highestSensitivity(existing.Sensitivity, req.SourceSensitivity, req.Sensitivity, classifiedSensitivity)
	candidate = buildAppendCandidate(profile, req, candidateID, content, sensitivity, !found, now)
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		return memcore.MemoryRecord{}, memcore.MemoryCandidate{}, false, false, err
	}
	record := buildAppendRecord(profile, req, candidate, existing, found, content, sensitivity, now)
	if err := store.SaveRecord(ctx, record); err != nil {
		return memcore.MemoryRecord{}, memcore.MemoryCandidate{}, false, false, err
	}
	return record, candidate, false, !found, nil
}

func diaryRecordContainsCandidate(record memcore.MemoryRecord, candidate memcore.MemoryCandidate) bool {
	candidateID := strings.TrimSpace(candidate.ID)
	if candidateID == "" {
		return false
	}
	if strings.TrimSpace(record.CandidateID) == candidateID {
		return true
	}
	candidateContent := strings.TrimSpace(candidate.Narrative)
	recordContent := strings.TrimSpace(firstNonBlank(record.Content, record.Narrative))
	if candidateContent != "" && (recordContent == candidateContent || strings.HasPrefix(recordContent, candidateContent+"\n\n")) {
		return true
	}
	return false
}

func diaryRecordCreatedByCandidate(record memcore.MemoryRecord, candidate memcore.MemoryCandidate) bool {
	if containsString(candidate.Tags, diaryTagRecordCreated) {
		return true
	}
	return !record.CreatedAt.IsZero() && record.CreatedAt.Equal(candidate.CreatedAt)
}

func (s *DiaryService) publishAppend(ctx context.Context, req AppendRequest, candidate memcore.MemoryCandidate, record memcore.MemoryRecord, created bool, now time.Time) error {
	if s.audit != nil {
		if err := s.audit.RecordMemoryAudit(ctx, memaudit.Event{
			Type: memaudit.EventMemoryCandidateCreated, SubjectID: candidate.ID, ActorID: req.AgentID, MissionID: req.MissionID,
			Summary: candidate.Summary, SourceRefs: append([]string(nil), candidate.EvidenceRefs...), OccurredAt: now,
		}); err != nil {
			return err
		}
		recordEvent := memaudit.EventMemoryRecordUpdated
		if created {
			recordEvent = memaudit.EventMemoryRecordCreated
		}
		if err := s.audit.RecordMemoryAudit(ctx, memaudit.Event{
			Type: recordEvent, SubjectID: record.ID, IdempotencyKey: recordEvent.String() + "|" + record.ID + "|" + candidate.ID,
			ActorID: req.AgentID, MissionID: req.MissionID,
			Summary: record.Summary, SourceRefs: append([]string(nil), record.SourceRefs...), Reason: candidate.ID, OccurredAt: now,
		}); err != nil {
			return err
		}
	}
	if s.metadata == nil {
		return nil
	}
	payload := map[string]any{
		"id": record.ID, "summary": record.Summary, "layer": string(record.Layer), "topic": record.Topic,
		"scope_kind": string(record.Scope.Kind), "scope_id": record.Scope.ID, "lifecycle_status": string(record.LifecycleStatus),
		"confidence": record.Confidence, "source_refs": append([]string(nil), record.SourceRefs...),
		"evidence_refs": append([]string(nil), record.SourceEvidenceRefs...),
	}
	return s.metadata.PublishMemoryRecordMetadata(ctx, graphprotocol.DomainEvent{
		ID: "memory-record-diary-append:" + candidate.ID, TenantID: req.TenantID, Type: "memory.record.accepted",
		StreamID: record.ID, Version: 1, OccurredAt: now, Payload: payload,
		PublishedBy: graphprotocol.ActorRef{Kind: "agent", ID: req.AgentID},
	})
}

func getAppendCandidate(ctx context.Context, store diaryAppendStore, candidateID string) (memcore.MemoryCandidate, bool, error) {
	candidate, err := store.GetCandidate(ctx, candidateID)
	if err == nil {
		return candidate, true, nil
	}
	if errors.Is(err, memcore.ErrNotFound) {
		return memcore.MemoryCandidate{}, false, nil
	}
	return memcore.MemoryCandidate{}, false, err
}

func defaultDiarySensitivity(content string) memcore.Sensitivity {
	decision := (memcapture.DefaultCapturePolicy{}).Decide(memcapture.CandidateDraft{
		SourceType:    memcore.SourceAgentRunDiary,
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent},
		ProposedKind:  agentcore.MemoryKindPersonal,
		Layer:         memcore.MemoryLayerFact,
		Summary:       content,
	})
	return decision.Sensitivity
}

func (s *DiaryService) advanceFlushCursor(ctx context.Context, req AppendRequest, now time.Time) error {
	if s.state == nil {
		return nil
	}
	_, err := s.state.MutateFlushState(ctx, req.TenantID, req.AgentID, req.SessionID, func(state *FlushState, _ bool) error {
		state.CurrentRevision = req.ContextRevision
		state.LastSuccessfulRevision = req.ContextRevision
		state.LastSuccessfulAt = now
		state.LastSuccessfulDate = req.Date
		state.AttemptRevision = req.ContextRevision
		state.AttemptTrigger = req.Trigger
		state.Exhausted = false
		state.LastFailure = ""
		return nil
	})
	return err
}

func (s *DiaryService) requireEnabled(ctx context.Context) error {
	if s.enabled == nil {
		return nil
	}
	enabled, err := s.enabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("%w: memory is disabled", ErrForbidden)
	}
	return nil
}

func (s *DiaryService) ownedProfile(ctx context.Context, tenantID, agentID string) (memcore.MemoryProfile, error) {
	profiles, err := s.repository.ListProfiles(ctx)
	if err != nil {
		return memcore.MemoryProfile{}, err
	}
	for _, profile := range profiles {
		if strings.TrimSpace(profile.OwnerRef.AgentID.String()) != agentID {
			continue
		}
		if strings.TrimSpace(profile.OwnerRef.OrganizationID) != tenantID {
			return memcore.MemoryProfile{}, fmt.Errorf("%w: tenant does not own Agent memory", ErrForbidden)
		}
		if !profile.Enabled || profile.MemoryKind != agentcore.MemoryKindPersonal {
			return memcore.MemoryProfile{}, fmt.Errorf("%w: personal Agent memory is disabled", ErrForbidden)
		}
		return profile.Clone(), nil
	}
	return memcore.MemoryProfile{}, fmt.Errorf("%w: Agent personal memory profile", ErrNotFound)
}

func (s *DiaryService) findReadableDailyRecord(ctx context.Context, profile memcore.MemoryProfile, tenantID, agentID, path, date string) (memcore.MemoryRecord, bool, error) {
	req := AppendRequest{TenantID: tenantID, AgentID: agentID, Date: date, Path: path}
	record, found, err := findCanonicalDailyRecord(ctx, s.repository, profile, req)
	if err != nil || found {
		return record, found, err
	}
	legacy, err := s.repository.GetRecord(ctx, dailyRecordID(agentID, date))
	if errors.Is(err, memcore.ErrNotFound) {
		return memcore.MemoryRecord{}, false, nil
	}
	if err != nil {
		return memcore.MemoryRecord{}, false, err
	}
	if !dailyRecordMatches(legacy, profile, agentID, path) {
		return memcore.MemoryRecord{}, false, nil
	}
	return legacy.Clone(), true, nil
}

func findCanonicalDailyRecord(ctx context.Context, store interface {
	GetRecord(context.Context, string) (memcore.MemoryRecord, error)
}, profile memcore.MemoryProfile, req AppendRequest) (memcore.MemoryRecord, bool, error) {
	record, err := store.GetRecord(ctx, appendRecordID(req))
	if errors.Is(err, memcore.ErrNotFound) {
		return memcore.MemoryRecord{}, false, nil
	}
	if err != nil {
		return memcore.MemoryRecord{}, false, err
	}
	if !dailyRecordMatches(record, profile, req.AgentID, req.Path) {
		return memcore.MemoryRecord{}, false, fmt.Errorf("%w: canonical diary record ownership mismatch", ErrForbidden)
	}
	return record.Clone(), true, nil
}

func dailyRecordMatches(record memcore.MemoryRecord, profile memcore.MemoryProfile, agentID, path string) bool {
	return record.ProfileID == profile.ID && record.Topic == path && record.Scope.Kind == memcore.ScopeAgent && record.Scope.ID == agentID
}

func normalizeReadRequest(req ReadRequest) ReadRequest {
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.Date = strings.TrimSpace(req.Date)
	req.Path = strings.TrimSpace(req.Path)
	return req
}

func normalizeAppendRequest(req AppendRequest) AppendRequest {
	read := normalizeReadRequest(ReadRequest{TenantID: req.TenantID, AgentID: req.AgentID, Date: req.Date, Path: req.Path})
	req.TenantID, req.AgentID, req.Date, req.Path = read.TenantID, read.AgentID, read.Date, read.Path
	req.Content = strings.TrimSpace(req.Content)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ContextRevision = strings.TrimSpace(req.ContextRevision)
	req.Trigger = FlushTrigger(strings.TrimSpace(string(req.Trigger)))
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.MissionID = strings.TrimSpace(req.MissionID)
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.RunID = strings.TrimSpace(req.RunID)
	return req
}

func validateDiaryTarget(tenantID, agentID, date, path string) error {
	if tenantID == "" || agentID == "" {
		return fmt.Errorf("%w: tenant_id and agent_id are required", ErrInvalidRequest)
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return fmt.Errorf("%w: date must use YYYY-MM-DD", ErrInvalidRequest)
	}
	expected := diaryPath(date)
	if path != expected {
		return fmt.Errorf("%w: diary path must be exactly %s", ErrForbidden, expected)
	}
	return nil
}

func appendMarkdown(existing, addition string) string {
	existing = strings.TrimSpace(existing)
	addition = strings.TrimSpace(addition)
	if existing == "" {
		return addition
	}
	return existing + "\n\n" + addition
}

func appendCandidateID(req AppendRequest) string {
	return "candidate.agent_daily_diary_append.v2." + stableDiaryHash(req.TenantID, req.AgentID, req.Date, req.SessionID, req.ContextRevision, string(req.Trigger), req.IdempotencyKey)
}

func appendRecordID(req AppendRequest) string {
	return "memory.record.agent_daily_diary.v2." + idSegment(req.TenantID) + "." + idSegment(req.AgentID) + "." + idSegment(req.Date)
}

func stableDiaryHash(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func buildAppendCandidate(profile memcore.MemoryProfile, req AppendRequest, candidateID, content string, sensitivity memcore.Sensitivity, recordCreated bool, now time.Time) memcore.MemoryCandidate {
	tags := appendDiaryTags(req)
	if recordCreated {
		tags = append(tags, diaryTagRecordCreated)
	}
	return memcore.MemoryCandidate{
		ID:            candidateID,
		MissionID:     req.MissionID,
		SourceType:    memcore.SourceAgentRunDiary,
		SourceRef:     req.IdempotencyKey,
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: req.AgentID},
		ProposedKind:  agentcore.MemoryKindPersonal,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         req.Path,
		Summary:       appendSummary(req.Content),
		Narrative:     content,
		Tags:          tags,
		LineageRefs:   append([]string(nil), req.LineageRefs...),
		Sensitivity:   sensitivity,
		DeIDLevel:     memcore.DeIDRaw,
		Consent:       memcore.ConsentNotRequired,
		Confidence:    1,
		EvidenceRefs:  append([]string(nil), req.EvidenceRefs...),
		CreatedBy:     agentcore.AgentID(req.AgentID),
		Status:        memcore.CandidateAccepted,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func buildAppendRecord(profile memcore.MemoryProfile, req AppendRequest, candidate memcore.MemoryCandidate, existing memcore.MemoryRecord, found bool, content string, sensitivity memcore.Sensitivity, now time.Time) memcore.MemoryRecord {
	record := existing.Clone()
	if !found {
		record = memcore.MemoryRecord{
			ID:              appendRecordID(req),
			ProfileID:       profile.ID,
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: req.AgentID},
			Layer:           memcore.MemoryLayerFact,
			Topic:           req.Path,
			LifecycleStatus: memcore.MemoryLifecycleActive,
			Confidence:      1,
			Visibility: memcore.Visibility{
				Mode:      memcore.VisibilityOwnerOnly,
				ScopeKind: memcore.ScopeAgent,
				ScopeID:   req.AgentID,
			},
			ValidFrom: now,
			CreatedBy: agentcore.AgentID(req.AgentID),
			CreatedAt: now,
		}
	}
	record.CandidateID = candidate.ID
	record.Content = content
	record.Narrative = content
	record.Summary = appendSummary(req.Content)
	record.Facts = appendUnique(record.Facts, candidate.Summary)
	record.Tags = appendUnique(record.Tags, appendDiaryTags(req)...)
	record.SourceRefs = appendUnique(record.SourceRefs, "agent_run_diary:"+candidate.SourceRef)
	record.SourceEvidenceRefs = appendUnique(record.SourceEvidenceRefs, req.EvidenceRefs...)
	record.LineageRefs = appendUnique(record.LineageRefs, req.LineageRefs...)
	record.Sensitivity = sensitivity
	record.DeIDLevel = memcore.DeIDRaw
	record.UpdatedAt = now
	return record
}

func appendDiaryTags(req AppendRequest) []string {
	tags := []string{
		DiaryTagAgent,
		DiaryTagDailyNote,
		DiaryTagSummary,
		"agent_authored",
		"diary_date:" + req.Date,
		"diary_path:" + req.Path,
		"tenant_id:" + req.TenantID,
		"diary_trigger:" + string(req.Trigger),
		"context_revision:" + req.ContextRevision,
	}
	if req.MissionID != "" {
		tags = append(tags, "mission:"+req.MissionID)
	}
	if req.TaskID != "" {
		tags = append(tags, "task:"+req.TaskID)
	}
	if req.RunID != "" {
		tags = append(tags, "run:"+req.RunID)
	}
	return tags
}

func appendReceipt(req AppendRequest, record memcore.MemoryRecord, candidateID string, replayed bool) AppendReceipt {
	return AppendReceipt{
		TenantID:        req.TenantID,
		AgentID:         req.AgentID,
		Date:            req.Date,
		Path:            req.Path,
		RecordID:        record.ID,
		CandidateID:     candidateID,
		Content:         firstNonBlank(record.Content, record.Narrative),
		ContextRevision: req.ContextRevision,
		Trigger:         req.Trigger,
		Replayed:        replayed,
	}
}

func appendSummary(content string) string {
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#-* "))
		if line == "" {
			continue
		}
		if len(line) > 240 {
			return line[:240]
		}
		return line
	}
	return "Agent diary updated."
}

func appendUnique(values []string, more ...string) []string {
	seen := make(map[string]bool, len(values)+len(more))
	out := make([]string, 0, len(values)+len(more))
	for _, value := range append(append([]string(nil), values...), more...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func highestSensitivity(values ...memcore.Sensitivity) memcore.Sensitivity {
	best := memcore.SensitivityNormal
	bestRank := sensitivityRank(best)
	for _, value := range values {
		value = memcore.NormalizeSensitivity(value)
		if rank := sensitivityRank(value); rank > bestRank {
			best, bestRank = value, rank
		}
	}
	return best
}

func sensitivityRank(value memcore.Sensitivity) int {
	switch memcore.NormalizeSensitivity(value) {
	case memcore.SensitivitySecret:
		return 7
	case memcore.SensitivityExecutive:
		return 6
	case memcore.SensitivityLegal:
		return 5
	case memcore.SensitivityCompensation:
		return 4
	case memcore.SensitivityCandidate:
		return 3
	case memcore.SensitivityPerformance:
		return 2
	case memcore.SensitivityPersonal:
		return 1
	default:
		return 0
	}
}
