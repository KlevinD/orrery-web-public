package memorydiary

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	toolstd "orrery_v2/internal/resource/tool/std"
)

const (
	ToolModeAllAgents = "all_agents"
	ToolModeAgent     = "agent"

	DiaryTagRunSource = "agent_result"
	DiaryTagDailyNote = "daily_note"
	DiaryTagAgent     = "agent_diary"
	DiaryTagSummary   = "daily_diary"
)

type Repository interface {
	SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error
	GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error)
	ListProfiles(ctx context.Context) ([]memcore.MemoryProfile, error)
	SaveCandidate(ctx context.Context, candidate memcore.MemoryCandidate) error
	GetCandidate(ctx context.Context, id string) (memcore.MemoryCandidate, error)
	ListCandidates(ctx context.Context, query memstore.CandidateQuery) ([]memcore.MemoryCandidate, error)
	UpdateCandidateStatus(ctx context.Context, id string, status memcore.CandidateStatus) error
	SaveRecord(ctx context.Context, record memcore.MemoryRecord) error
	GetRecord(ctx context.Context, id string) (memcore.MemoryRecord, error)
	FindRecords(ctx context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error)
	UpdateRecordLifecycle(ctx context.Context, id string, status memcore.MemoryLifecycleStatus) error
}

type EnabledFunc func(context.Context) (bool, error)

type ToolExecutorOptions struct {
	Repository          Repository
	Enabled             EnabledFunc
	State               FlushStateStore
	Audit               memaudit.Recorder
	Metadata            MemoryRecordMetadataSink
	ClassifySensitivity func(string) memcore.Sensitivity
	Now                 func() time.Time
}

type ToolExecutor struct {
	repository          Repository
	enabled             EnabledFunc
	state               FlushStateStore
	audit               memaudit.Recorder
	metadata            MemoryRecordMetadataSink
	classifySensitivity func(string) memcore.Sensitivity
	now                 func() time.Time
}

type ToolRequest struct {
	TenantID          string
	PrincipalID       string
	AgentID           string
	Mode              string
	TargetAgentID     string
	Date              string
	Force             bool
	Limit             int
	IdempotencyKey    string
	Path              string
	Content           string
	SessionID         string
	ContextRevision   string
	Trigger           string
	MissionID         string
	TaskID            string
	RunID             string
	Sensitivity       string
	SourceSensitivity string
	EvidenceRefs      []string
	LineageRefs       []string
}

type ToolResult struct {
	Output map[string]any
}

type diaryWriteResult struct {
	AgentID     string
	MemoryID    string
	CandidateID string
	DiaryPath   string
	Status      string
	SourceCount int
	Summary     string
}

type diarySource struct {
	Key                string
	ID                 string
	Summary            string
	Content            string
	Narrative          string
	SourceEvidenceRefs []string
	CreatedAt          time.Time
}

func NewToolExecutor(opts ToolExecutorOptions) *ToolExecutor {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &ToolExecutor{
		repository: opts.Repository, enabled: opts.Enabled, state: opts.State, audit: opts.Audit, metadata: opts.Metadata,
		classifySensitivity: opts.ClassifySensitivity, now: now,
	}
}

func (e *ToolExecutor) executeMaintenanceTool(ctx context.Context, toolID string, req ToolRequest) (ToolResult, error) {
	service := NewDiaryService(DiaryServiceOptions{
		Repository: e.repository, Enabled: e.enabled, State: e.state,
		Audit: e.audit, Metadata: e.metadata, ClassifySensitivity: e.classifySensitivity, Now: e.now,
	})
	readReq := ReadRequest{
		TenantID: strings.TrimSpace(req.TenantID),
		AgentID:  strings.TrimSpace(req.AgentID),
		Date:     strings.TrimSpace(req.Date),
		Path:     strings.TrimSpace(req.Path),
	}
	if toolID == string(toolstd.MemoryDiaryRead) {
		result, err := service.Read(ctx, readReq)
		if err != nil {
			return ToolResult{}, err
		}
		status := "not_found"
		if result.Exists {
			status = "found"
		}
		return ToolResult{Output: map[string]any{
			"status":    status,
			"path":      result.Path,
			"memory_id": result.RecordID,
			"content":   result.Content,
		}}, nil
	}
	result, err := service.Append(ctx, AppendRequest{
		TenantID:          readReq.TenantID,
		AgentID:           readReq.AgentID,
		Date:              readReq.Date,
		Path:              readReq.Path,
		Content:           req.Content,
		SessionID:         req.SessionID,
		ContextRevision:   req.ContextRevision,
		Trigger:           FlushTrigger(req.Trigger),
		IdempotencyKey:    req.IdempotencyKey,
		MissionID:         req.MissionID,
		TaskID:            req.TaskID,
		RunID:             req.RunID,
		EvidenceRefs:      append([]string(nil), req.EvidenceRefs...),
		LineageRefs:       append([]string(nil), req.LineageRefs...),
		SourceSensitivity: memcore.Sensitivity(strings.TrimSpace(req.SourceSensitivity)),
		Sensitivity:       memcore.Sensitivity(strings.TrimSpace(req.Sensitivity)),
	})
	if err != nil {
		return ToolResult{}, err
	}
	status := "written"
	if result.Replayed {
		status = "replayed"
	}
	return ToolResult{Output: map[string]any{
		"status":           status,
		"path":             result.Path,
		"memory_id":        result.RecordID,
		"candidate_id":     result.CandidateID,
		"context_revision": result.ContextRevision,
	}}, nil
}

func (e *ToolExecutor) ExecuteMemoryDiaryTool(ctx context.Context, toolID string, req ToolRequest) (ToolResult, error) {
	if e == nil || e.repository == nil || e.now == nil {
		return ToolResult{}, ErrServiceDependency
	}
	toolID = strings.TrimSpace(toolID)
	if toolID != string(toolstd.MemoryDiaryWrite) && toolID != string(toolstd.MemoryDiaryRead) && toolID != string(toolstd.MemoryDiaryAppend) {
		return ToolResult{}, fmt.Errorf("%w: unsupported memory diary tool %q", ErrInvalidRequest, toolID)
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	if toolID == string(toolstd.MemoryDiaryRead) || toolID == string(toolstd.MemoryDiaryAppend) {
		return e.executeMaintenanceTool(ctx, toolID, req)
	}
	req = normalizeToolRequest(req, e.now().UTC())
	if req.Mode != ToolModeAllAgents && req.Mode != ToolModeAgent {
		return ToolResult{}, fmt.Errorf("%w: unsupported diary mode %q", ErrInvalidRequest, req.Mode)
	}
	if req.Mode == ToolModeAgent && req.TargetAgentID == "" {
		return ToolResult{}, fmt.Errorf("%w: agent_id is required", ErrInvalidRequest)
	}
	if _, err := time.Parse(time.DateOnly, req.Date); err != nil {
		return ToolResult{}, fmt.Errorf("%w: date must use YYYY-MM-DD", ErrInvalidRequest)
	}
	if e.enabled != nil {
		enabled, err := e.enabled(ctx)
		if err != nil {
			return ToolResult{}, err
		}
		if !enabled {
			return e.disabledResult(req), nil
		}
	}
	profiles, err := e.repository.ListProfiles(ctx)
	if err != nil {
		return ToolResult{}, err
	}
	targets := personalProfilesForRequest(profiles, req)
	if req.Mode == ToolModeAgent && len(targets) == 0 {
		return ToolResult{}, fmt.Errorf("%w: agent_id %q has no personal memory profile", ErrNotFound, req.TargetAgentID)
	}

	results := make([]diaryWriteResult, 0, len(targets))
	for _, profile := range targets {
		result, err := e.writeAgentDiary(ctx, profile, req)
		if err != nil {
			return ToolResult{}, err
		}
		results = append(results, result)
	}
	return buildToolResult(req, results), nil
}

func (e *ToolExecutor) disabledResult(req ToolRequest) ToolResult {
	if req.Mode == ToolModeAllAgents {
		output := buildToolResult(req, nil).Output
		output["status"] = "disabled"
		return ToolResult{Output: output}
	}
	item := diaryWriteResult{
		AgentID:   firstNonBlank(req.TargetAgentID, req.AgentID),
		DiaryPath: diaryPath(req.Date),
		Status:    "disabled",
	}
	output := buildToolResult(req, []diaryWriteResult{item}).Output
	output["status"] = "disabled"
	return ToolResult{Output: output}
}

func (e *ToolExecutor) writeAgentDiary(ctx context.Context, profile memcore.MemoryProfile, req ToolRequest) (diaryWriteResult, error) {
	agentID := strings.TrimSpace(profile.OwnerRef.AgentID.String())
	result := diaryWriteResult{
		AgentID:     agentID,
		DiaryPath:   diaryPath(req.Date),
		MemoryID:    dailyRecordID(agentID, req.Date),
		CandidateID: dailyCandidateID(agentID, req.Date),
	}
	existing, err := e.repository.FindRecords(ctx, memstore.RecordQuery{
		ProfileIDs:      []agentcore.MemoryProfileID{profile.ID},
		ScopeKinds:      []memcore.ScopeKind{memcore.ScopeAgent},
		ScopeIDs:        []string{agentID},
		Topics:          []string{result.DiaryPath},
		Tags:            []string{DiaryTagSummary, "diary_date:" + req.Date},
		IncludeArchived: true,
		Limit:           req.Limit,
	})
	if err != nil {
		return result, err
	}
	if hasExistingDailyDiary(existing, result.MemoryID) && !req.Force {
		result.Status = "exists"
		result.SourceCount = sourceCountFromDailyDiary(existing, result.MemoryID)
		result.Summary = summaryFromDailyDiary(existing, result.MemoryID)
		return result, nil
	}

	sources, err := e.sourceRecords(ctx, profile, req)
	if err != nil {
		return result, err
	}
	if len(sources) == 0 {
		result.Status = "no_material"
		return result, nil
	}
	now := e.now().UTC()
	candidate := buildDailyCandidate(profile, req, sources, now)
	record := buildDailyRecord(profile, candidate, req, sources, now)
	if err := e.repository.SaveCandidate(ctx, candidate); err != nil {
		return result, err
	}
	if err := e.repository.SaveRecord(ctx, record); err != nil {
		return result, err
	}
	result.Status = "written"
	result.SourceCount = len(sources)
	result.Summary = record.Summary
	return result, nil
}

func (e *ToolExecutor) sourceRecords(ctx context.Context, profile memcore.MemoryProfile, req ToolRequest) ([]diarySource, error) {
	candidates, err := e.repository.ListCandidates(ctx, memstore.CandidateQuery{
		ScopeKind: memcore.ScopeAgent,
		ScopeID:   profile.OwnerRef.AgentID.String(),
		Layer:     memcore.MemoryLayerFact,
		Topic:     diaryPath(req.Date),
		Status:    memcore.CandidateAccepted,
		Limit:     req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]diarySource, 0, len(candidates))
	for _, candidate := range candidates {
		if !isRunDiaryCandidate(candidate) {
			continue
		}
		out = append(out, diarySourceFromCandidate(candidate))
	}

	records, err := e.repository.FindRecords(ctx, memstore.RecordQuery{
		ProfileIDs: []agentcore.MemoryProfileID{profile.ID},
		ScopeKinds: []memcore.ScopeKind{memcore.ScopeAgent},
		ScopeIDs:   []string{profile.OwnerRef.AgentID.String()},
		Layers:     []memcore.MemoryLayer{memcore.MemoryLayerFact},
		Topics:     []string{diaryPath(req.Date)},
		Tags:       []string{"diary_date:" + req.Date},
		Limit:      req.Limit,
	})
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if isDailyDiaryRecord(record) || !isRunDiaryRecord(record) {
			continue
		}
		out = append(out, diarySourceFromRecord(record))
	}
	out = uniqueDiarySources(out)
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].CreatedAt, out[j].CreatedAt
		if left.Equal(right) {
			return out[i].ID < out[j].ID
		}
		if left.IsZero() {
			return false
		}
		if right.IsZero() {
			return true
		}
		return left.Before(right)
	})
	if req.Limit > 0 && len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

func normalizeToolRequest(req ToolRequest, now time.Time) ToolRequest {
	req.PrincipalID = strings.TrimSpace(req.PrincipalID)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.Mode = strings.TrimSpace(req.Mode)
	req.TargetAgentID = strings.TrimSpace(req.TargetAgentID)
	req.Date = strings.TrimSpace(req.Date)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.Date == "" {
		req.Date = reqDefaultDate(now)
	}
	if req.Limit <= 0 {
		req.Limit = 200
	}
	if req.Limit > 1000 {
		req.Limit = 1000
	}
	return req
}

func reqDefaultDate(now time.Time) string {
	return now.UTC().AddDate(0, 0, -1).Format(time.DateOnly)
}

func personalProfilesForRequest(profiles []memcore.MemoryProfile, req ToolRequest) []memcore.MemoryProfile {
	out := make([]memcore.MemoryProfile, 0, len(profiles))
	for _, profile := range profiles {
		if !profile.Enabled || profile.MemoryKind != agentcore.MemoryKindPersonal || strings.TrimSpace(profile.OwnerRef.AgentID.String()) == "" {
			continue
		}
		if req.Mode == ToolModeAgent && profile.OwnerRef.AgentID.String() != req.TargetAgentID {
			continue
		}
		out = append(out, profile.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].OwnerRef.AgentID < out[j].OwnerRef.AgentID
	})
	return out
}

func buildDailyCandidate(profile memcore.MemoryProfile, req ToolRequest, sources []diarySource, now time.Time) memcore.MemoryCandidate {
	agentID := profile.OwnerRef.AgentID.String()
	summary := dailySummary(agentID, req.Date, sources)
	return memcore.MemoryCandidate{
		ID:            dailyCandidateID(agentID, req.Date),
		SourceType:    memcore.SourceSummary,
		SourceRef:     sourceRefForDailyDiary(req, agentID),
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: agentID},
		ProposedKind:  agentcore.MemoryKindPersonal,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         diaryPath(req.Date),
		Summary:       summary,
		Narrative:     dailyDiaryContent(agentID, req.Date, sources),
		Tags:          dailyDiaryTags(req.Date),
		EvidenceRefs:  sourceEvidenceRefs(sources),
		LineageRefs:   sourceLineageRefs(sources),
		Sensitivity:   memcore.SensitivityNormal,
		DeIDLevel:     memcore.DeIDRaw,
		Consent:       memcore.ConsentNotRequired,
		Confidence:    1,
		CreatedBy:     agentcore.AgentID(firstNonBlank(req.PrincipalID, req.AgentID, agentID)),
		Status:        memcore.CandidateAccepted,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func buildDailyRecord(profile memcore.MemoryProfile, candidate memcore.MemoryCandidate, req ToolRequest, sources []diarySource, now time.Time) memcore.MemoryRecord {
	agentID := profile.OwnerRef.AgentID.String()
	content := firstNonBlank(candidate.Narrative, candidate.Summary)
	sourceRefs := []string{candidateSourceRef(candidate)}
	sourceRefs = appendNonBlank(sourceRefs, sourceLineageRefs(sources)...)
	return memcore.MemoryRecord{
		ID:                 dailyRecordID(agentID, req.Date),
		ProfileID:          profile.ID,
		CandidateID:        candidate.ID,
		Scope:              candidate.ProposedScope,
		Layer:              memcore.MemoryLayerFact,
		Topic:              diaryPath(req.Date),
		SourceRefs:         sourceRefs,
		SourceEvidenceRefs: sourceEvidenceRefs(sources),
		Content:            content,
		Summary:            candidate.Summary,
		Narrative:          content,
		Facts:              []string{candidate.Summary},
		Tags:               dailyDiaryTags(req.Date),
		LineageRefs:        sourceLineageRefs(sources),
		Sensitivity:        memcore.SensitivityNormal,
		DeIDLevel:          memcore.DeIDRaw,
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Confidence:         1,
		Visibility: memcore.Visibility{
			Mode:      memcore.VisibilityOwnerOnly,
			ScopeKind: memcore.ScopeAgent,
			ScopeID:   agentID,
		},
		ValidFrom: now,
		CreatedBy: agentcore.AgentID(firstNonBlank(req.PrincipalID, req.AgentID, agentID)),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func buildToolResult(req ToolRequest, results []diaryWriteResult) ToolResult {
	written := 0
	skipped := 0
	items := make([]map[string]any, 0, len(results))
	skippedAgents := make([]string, 0)
	for _, result := range results {
		if result.Status == "written" {
			written++
		} else {
			skipped++
			if result.AgentID != "" {
				skippedAgents = append(skippedAgents, result.AgentID)
			}
		}
		item := map[string]any{
			"agent_id":     result.AgentID,
			"memory_id":    result.MemoryID,
			"candidate_id": result.CandidateID,
			"diary_path":   result.DiaryPath,
			"status":       result.Status,
			"source_count": result.SourceCount,
			"summary":      result.Summary,
		}
		items = append(items, item)
	}
	status := "completed"
	if written == 0 && allDiaryResultsNoMaterial(results) {
		status = "no_material"
	}
	return ToolResult{Output: map[string]any{
		"status":         status,
		"date":           req.Date,
		"diary_path":     diaryPath(req.Date),
		"written_count":  written,
		"skipped_count":  skipped,
		"diaries":        items,
		"skipped_agents": skippedAgents,
	}}
}

func allDiaryResultsNoMaterial(results []diaryWriteResult) bool {
	if len(results) == 0 {
		return true
	}
	for _, result := range results {
		if result.Status != "no_material" {
			return false
		}
	}
	return true
}

func hasExistingDailyDiary(records []memcore.MemoryRecord, memoryID string) bool {
	for _, record := range records {
		if record.ID == memoryID || isDailyDiaryRecord(record) {
			return true
		}
	}
	return false
}

func sourceCountFromDailyDiary(records []memcore.MemoryRecord, memoryID string) int {
	for _, record := range records {
		if record.ID == memoryID || isDailyDiaryRecord(record) {
			return len(record.LineageRefs)
		}
	}
	return 0
}

func summaryFromDailyDiary(records []memcore.MemoryRecord, memoryID string) string {
	for _, record := range records {
		if record.ID == memoryID || isDailyDiaryRecord(record) {
			return record.Summary
		}
	}
	return ""
}

func dailyDiaryContent(agentID, date string, sources []diarySource) string {
	var b strings.Builder
	b.WriteString("# " + diaryPath(date) + "\n\n")
	b.WriteString("## Summary\n")
	b.WriteString(dailySummary(agentID, date, sources) + "\n\n")
	b.WriteString("## Entries\n")
	for _, source := range sources {
		timeLabel := "unknown time"
		if !source.CreatedAt.IsZero() {
			timeLabel = source.CreatedAt.UTC().Format("15:04")
		}
		b.WriteString("- " + timeLabel + " - " + firstNonBlank(source.Summary, source.Content, "Agent run completed.") + "\n")
		for _, line := range compactSourceLines(source) {
			b.WriteString("  - " + line + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func dailySummary(agentID, date string, sources []diarySource) string {
	if len(sources) == 0 {
		return "Agent " + agentID + " had no diary material on " + date + "."
	}
	return fmt.Sprintf("Agent %s completed %d recorded run(s) on %s.", agentID, len(sources), date)
}

func compactSourceLines(record diarySource) []string {
	text := firstNonBlank(record.Narrative, record.Content)
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, 4)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Diary:") || strings.HasPrefix(line, "Summary:") {
			continue
		}
		out = append(out, line)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func isRunDiaryRecord(record memcore.MemoryRecord) bool {
	return containsString(record.Tags, DiaryTagAgent) && containsString(record.Tags, DiaryTagRunSource)
}

func isRunDiaryCandidate(candidate memcore.MemoryCandidate) bool {
	return candidate.SourceType == memcore.SourceAgentRunDiary &&
		containsString(candidate.Tags, DiaryTagAgent) &&
		containsString(candidate.Tags, DiaryTagRunSource)
}

func isDailyDiaryRecord(record memcore.MemoryRecord) bool {
	return containsString(record.Tags, DiaryTagSummary)
}

func diarySourceFromCandidate(candidate memcore.MemoryCandidate) diarySource {
	return diarySource{
		Key:                "candidate:" + candidate.ID,
		ID:                 "candidate:" + candidate.ID,
		Summary:            candidate.Summary,
		Content:            firstNonBlank(candidate.Narrative, candidate.Summary),
		Narrative:          candidate.Narrative,
		SourceEvidenceRefs: append([]string(nil), candidate.EvidenceRefs...),
		CreatedAt:          candidate.CreatedAt,
	}
}

func diarySourceFromRecord(record memcore.MemoryRecord) diarySource {
	key := "memory:" + record.ID
	if strings.TrimSpace(record.CandidateID) != "" {
		key = "candidate:" + strings.TrimSpace(record.CandidateID)
	}
	return diarySource{
		Key:                key,
		ID:                 "memory:" + record.ID,
		Summary:            record.Summary,
		Content:            firstNonBlank(record.Content, record.Narrative, record.Summary),
		Narrative:          record.Narrative,
		SourceEvidenceRefs: append([]string(nil), record.SourceEvidenceRefs...),
		CreatedAt:          record.CreatedAt,
	}
}

func uniqueDiarySources(sources []diarySource) []diarySource {
	seen := map[string]bool{}
	out := make([]diarySource, 0, len(sources))
	for _, source := range sources {
		key := strings.TrimSpace(source.Key)
		if key == "" {
			key = strings.TrimSpace(source.ID)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, source)
	}
	return out
}

func dailyDiaryTags(date string) []string {
	path := diaryPath(date)
	return []string{DiaryTagAgent, DiaryTagDailyNote, DiaryTagSummary, "diary_date:" + date, "diary_path:" + path}
}

func sourceLineageRefs(sources []diarySource) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		out = append(out, source.ID)
	}
	return out
}

func sourceEvidenceRefs(sources []diarySource) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, source := range sources {
		for _, ref := range source.SourceEvidenceRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

func sourceRefForDailyDiary(req ToolRequest, agentID string) string {
	if req.IdempotencyKey != "" {
		return req.IdempotencyKey + ":" + agentID
	}
	return "memory_diary_write:" + req.Date + ":" + agentID
}

func candidateSourceRef(candidate memcore.MemoryCandidate) string {
	sourceType := strings.TrimSpace(string(candidate.SourceType))
	sourceRef := strings.TrimSpace(candidate.SourceRef)
	if sourceType == "" || sourceRef == "" {
		return ""
	}
	return sourceType + ":" + sourceRef
}

func dailyCandidateID(agentID, date string) string {
	return "candidate.agent_daily_diary." + idSegment(agentID) + "." + idSegment(date)
}

func dailyRecordID(agentID, date string) string {
	return "memory.record.agent_daily_diary." + idSegment(agentID) + "." + idSegment(date)
}

func diaryPath(date string) string {
	return "memory/" + strings.TrimSpace(date) + ".md"
}

func appendNonBlank(values []string, more ...string) []string {
	for _, value := range more {
		value = strings.TrimSpace(value)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target && target != "" {
			return true
		}
	}
	return false
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func idSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_").Replace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
