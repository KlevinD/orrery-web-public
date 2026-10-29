package operations

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
)

type AuditStorePort interface {
	ListAuditEvents(ctx context.Context, query AuditLogQuery) (AuditEventPageDTO, error)
}

type MissionProjectionRepository interface {
	Save(ctx context.Context, model projection.ReadModel) error
	GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (projection.ReadModel, error)
}

type EvaluationProjectionStore interface {
	UpsertEvaluationItems(ctx context.Context, items []EvaluationItemDTO) error
	ListEvaluationItems(ctx context.Context, query EvaluationQuery) (EvaluationPageDTO, error)
}

type EvaluationReviewStore interface {
	UpsertEvaluationReview(ctx context.Context, review EvaluationReviewDTO) error
}

type EvaluationReviewReader interface {
	GetEvaluationReview(ctx context.Context, reviewID string) (EvaluationReviewDTO, error)
}

type AuditLogQuery struct {
	ActorID    string
	Action     string
	Result     string
	EntityType string
	EntityID   string
	MissionID  string
	Severity   string
	Limit      int
	Offset     int
}

const (
	defaultAuditLogPageLimit = 100
	maximumAuditLogPageLimit = 500
)

type AuditEventPageDTO struct {
	Events []AuditEventDTO `json:"events"`
	Page   PageDTO         `json:"page"`
}

type AuditEventDTO struct {
	AuditID       string            `json:"audit_id"`
	TenantID      string            `json:"tenant_id,omitempty"`
	ActorID       string            `json:"actor_id"`
	ViewerID      string            `json:"viewer_id,omitempty"`
	MissionID     string            `json:"mission_id,omitempty"`
	EntityType    string            `json:"entity_type"`
	EntityID      string            `json:"entity_id"`
	Action        string            `json:"action"`
	Result        string            `json:"result"`
	Severity      string            `json:"severity"`
	CorrelationID string            `json:"correlation_id"`
	Summary       string            `json:"summary,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	OccurredAt    time.Time         `json:"occurred_at"`
}

type EvaluationQuery struct {
	MissionID  string
	EntityType string
	EntityID   string
}

type EvaluationPageDTO struct {
	Items []EvaluationItemDTO `json:"items"`
	Page  PageDTO             `json:"page"`
}

type EvaluationItemDTO struct {
	EntityType             string                     `json:"entity_type"`
	EntityID               string                     `json:"entity_id"`
	DisplayName            string                     `json:"display_name,omitempty"`
	MissionID              string                     `json:"mission_id,omitempty"`
	RoleName               string                     `json:"role_name,omitempty"`
	Department             string                     `json:"department,omitempty"`
	Position               string                     `json:"position,omitempty"`
	RuntimeKind            string                     `json:"runtime_kind,omitempty"`
	AssignedTasks          int                        `json:"assigned_tasks"`
	CompletedTasks         int                        `json:"completed_tasks"`
	ReworkCount            int                        `json:"rework_count"`
	BlockerCount           int                        `json:"blocker_count"`
	ResourceUsageCount     int                        `json:"resource_usage_count"`
	DiscussionReplyCount   int                        `json:"discussion_reply_count"`
	AverageDurationSeconds int                        `json:"average_duration_seconds,omitempty"`
	TokenConsumed          int64                      `json:"token_consumed,omitempty"`
	QualityScore           float64                    `json:"quality_score"`
	OnTimeRate             float64                    `json:"on_time_rate"`
	CollaborationScore     float64                    `json:"collaboration_score"`
	EfficiencyScore        float64                    `json:"efficiency_score,omitempty"`
	StabilityScore         float64                    `json:"stability_score,omitempty"`
	Summary                string                     `json:"summary,omitempty"`
	Capabilities           []EvaluationCapabilityDTO  `json:"capabilities,omitempty"`
	Investment             EvaluationInvestmentDTO    `json:"investment,omitempty"`
	LatestReview           EvaluationReviewDTO        `json:"latest_review,omitempty"`
	TaskHistory            []EvaluationTaskHistoryDTO `json:"task_history,omitempty"`
	Trend                  []EvaluationTrendPointDTO  `json:"trend,omitempty"`
	RepresentativeCases    []EvaluationCaseDTO        `json:"representative_cases,omitempty"`
	Suggestions            []string                   `json:"suggestions,omitempty"`
	Advantages             []string                   `json:"advantages,omitempty"`
	Risks                  []string                   `json:"risks,omitempty"`
	Scenarios              []string                   `json:"scenarios,omitempty"`
	LastActivityAt         time.Time                  `json:"last_activity_at,omitempty"`
}

type EvaluationCapabilityDTO struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

type EvaluationInvestmentDTO struct {
	TokenConsumed      int64   `json:"token_consumed"`
	EstimatedValueYuan float64 `json:"estimated_value_yuan,omitempty"`
	EstimatedHumanDays float64 `json:"estimated_human_days,omitempty"`
	TokenValuePer10K   float64 `json:"token_value_per_10k,omitempty"`
	ReturnMultiple     float64 `json:"return_multiple"`
	Summary            string  `json:"summary,omitempty"`
}

type EvaluationReviewDTO struct {
	ReviewID                string    `json:"review_id"`
	EntityType              string    `json:"entity_type"`
	EntityID                string    `json:"entity_id"`
	ScopeKind               string    `json:"scope_kind,omitempty"`
	ScopeID                 string    `json:"scope_id,omitempty"`
	EvaluatorAgentID        string    `json:"evaluator_agent_id,omitempty"`
	ConversationID          string    `json:"conversation_id,omitempty"`
	ScheduledRunID          string    `json:"scheduled_run_id,omitempty"`
	WindowDays              int       `json:"window_days,omitempty"`
	Summary                 string    `json:"summary,omitempty"`
	EstimatedHumanDays      float64   `json:"estimated_human_days,omitempty"`
	EvaluationTokenConsumed int64     `json:"evaluation_token_consumed,omitempty"`
	CreatedAt               time.Time `json:"created_at,omitempty"`
}

type EvaluationTaskHistoryDTO struct {
	Goal            string    `json:"goal"`
	Responsibility  string    `json:"responsibility"`
	Type            string    `json:"type"`
	Result          string    `json:"result"`
	Quality         float64   `json:"quality"`
	TokenConsumed   int64     `json:"token_consumed"`
	DurationSeconds int       `json:"duration_seconds"`
	Risk            string    `json:"risk,omitempty"`
	Time            time.Time `json:"time,omitempty"`
}

type EvaluationTrendPointDTO struct {
	Date          string  `json:"date"`
	Tasks         int     `json:"tasks"`
	Score         float64 `json:"score"`
	TokenConsumed int64   `json:"token_consumed,omitempty"`
}

type EvaluationCaseDTO struct {
	Title   string    `json:"title"`
	Result  string    `json:"result"`
	Summary string    `json:"summary"`
	Date    time.Time `json:"date,omitempty"`
}

type MemoryMissionProjectionRepository struct {
	mu     sync.RWMutex
	models map[missioncore.MissionID]projection.ReadModel
}

func NewMemoryMissionProjectionRepository() *MemoryMissionProjectionRepository {
	return &MemoryMissionProjectionRepository{models: map[missioncore.MissionID]projection.ReadModel{}}
}

type MemoryEvaluationProjectionStore struct {
	mu      sync.RWMutex
	items   []EvaluationItemDTO
	reviews []EvaluationReviewDTO
}

const evaluationProjectionTable = "observability.evaluation_projection"
const evaluationReviewTable = "observability.evaluation_reviews"

func NewMemoryEvaluationProjectionStore() *MemoryEvaluationProjectionStore {
	return &MemoryEvaluationProjectionStore{}
}

type PlatformEvaluationProjectionStore struct {
	router *db.Router
}

func NewPlatformEvaluationProjectionStore(router *db.Router) *PlatformEvaluationProjectionStore {
	return &PlatformEvaluationProjectionStore{router: router}
}

func (s *MemoryEvaluationProjectionStore) UpsertEvaluationItems(ctx context.Context, items []EvaluationItemDTO) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		replaced := false
		for i, existing := range s.items {
			if existing.EntityType == item.EntityType && existing.EntityID == item.EntityID && existing.MissionID == item.MissionID {
				s.items[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			s.items = append(s.items, item)
		}
	}
	return nil
}

func (s *MemoryEvaluationProjectionStore) ProjectMission(ctx context.Context, model projection.ReadModel) error {
	return s.UpsertEvaluationItems(ctx, evaluationsFromMission(model, EvaluationQuery{}))
}

func (s *MemoryEvaluationProjectionStore) ListEvaluationItems(ctx context.Context, query EvaluationQuery) (EvaluationPageDTO, error) {
	if err := ctx.Err(); err != nil {
		return EvaluationPageDTO{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	grouped := query.MissionID == ""
	byEntity := map[string]*EvaluationItemDTO{}
	items := make([]EvaluationItemDTO, 0, len(s.items))
	for _, item := range s.items {
		if query.MissionID != "" && item.MissionID != query.MissionID {
			continue
		}
		if query.EntityType != "" && item.EntityType != query.EntityType {
			continue
		}
		if query.EntityID != "" && item.EntityID != query.EntityID {
			continue
		}
		if !grouped {
			items = append(items, item)
			continue
		}
		key := item.EntityType + "\x00" + item.EntityID
		aggregate := byEntity[key]
		if aggregate == nil {
			copy := EvaluationItemDTO{
				EntityType:   item.EntityType,
				EntityID:     item.EntityID,
				DisplayName:  item.DisplayName,
				RoleName:     item.RoleName,
				Department:   item.Department,
				Position:     item.Position,
				RuntimeKind:  item.RuntimeKind,
				Summary:      item.Summary,
				Suggestions:  append([]string{}, item.Suggestions...),
				Advantages:   append([]string{}, item.Advantages...),
				Risks:        append([]string{}, item.Risks...),
				Scenarios:    append([]string{}, item.Scenarios...),
				Capabilities: append([]EvaluationCapabilityDTO{}, item.Capabilities...),
				Investment:   item.Investment,
			}
			aggregate = &copy
			byEntity[key] = aggregate
		}
		mergeEvaluationItem(aggregate, item)
	}
	if grouped {
		for _, item := range byEntity {
			finalizeEvaluationScores(item)
			items = append(items, *item)
		}
	}
	items = applyEvaluationReviews(items, s.reviews, query, grouped)
	return EvaluationPageDTO{Items: items, Page: PageDTO{Total: len(items), Returned: len(items)}}, nil
}

func (s *PlatformEvaluationProjectionStore) UpsertEvaluationItems(ctx context.Context, items []EvaluationItemDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	for _, item := range items {
		key := evaluationProjectionKey(item)
		if err := s.router.PutRecord(ctx, evaluationProjectionTable, key, evaluationRecord(item)); err != nil {
			return err
		}
	}
	return nil
}

func (s *PlatformEvaluationProjectionStore) ProjectMission(ctx context.Context, model projection.ReadModel) error {
	return s.UpsertEvaluationItems(ctx, evaluationsFromMission(model, EvaluationQuery{}))
}

func (s *PlatformEvaluationProjectionStore) ListEvaluationItems(ctx context.Context, query EvaluationQuery) (EvaluationPageDTO, error) {
	if s == nil || s.router == nil {
		return EvaluationPageDTO{}, ErrServiceDependency
	}
	store, table, err := s.router.StoreForTable(evaluationProjectionTable)
	if err != nil {
		return EvaluationPageDTO{}, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return EvaluationPageDTO{}, err
	}
	items := make([]EvaluationItemDTO, 0)
	for _, record := range records {
		item := evaluationFromRecord(record)
		if query.MissionID != "" && item.MissionID != query.MissionID {
			continue
		}
		if query.EntityType != "" && item.EntityType != query.EntityType {
			continue
		}
		if query.EntityID != "" && item.EntityID != query.EntityID {
			continue
		}
		items = append(items, item)
	}
	reviews, err := s.listEvaluationReviews(ctx)
	if err != nil {
		return EvaluationPageDTO{}, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastActivityAt.After(items[j].LastActivityAt) })
	if query.MissionID == "" {
		page := aggregateEvaluationItems(items)
		page.Items = applyEvaluationReviews(page.Items, reviews, query, true)
		page.Page = PageDTO{Total: len(page.Items), Returned: len(page.Items)}
		return page, nil
	}
	items = applyEvaluationReviews(items, reviews, query, false)
	return EvaluationPageDTO{Items: items, Page: PageDTO{Total: len(items), Returned: len(items)}}, nil
}

func (s *PlatformEvaluationProjectionStore) listEvaluationReviews(ctx context.Context) ([]EvaluationReviewDTO, error) {
	store, table, err := s.router.StoreForTable(evaluationReviewTable)
	if err != nil {
		return nil, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return nil, err
	}
	reviews := make([]EvaluationReviewDTO, 0)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reviews = append(reviews, evaluationReviewFromRecord(record))
	}
	return reviews, nil
}

func (r *MemoryMissionProjectionRepository) Save(ctx context.Context, model projection.ReadModel) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[model.Mission.MissionID] = model
	return nil
}

func (r *MemoryMissionProjectionRepository) GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (projection.ReadModel, error) {
	if err := ctx.Err(); err != nil {
		return projection.ReadModel{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	model, ok := r.models[missionID]
	if !ok {
		return projection.ReadModel{}, ErrNotFound
	}
	return model, nil
}

func (s *QueryService) SetAuditStore(store AuditStorePort) {
	s.audit = store
}

func (s *QueryService) SetOrganizationCatalogStore(store OrganizationCatalogPort) {
	s.orgs = store
}

func (s *QueryService) SetMissionProjectionRepository(repo MissionProjectionRepository) {
	s.missions = repo
}

func (s *QueryService) SetEvaluationProjectionStore(store EvaluationProjectionStore) {
	s.evals = store
}

func (s *QueryService) ListAuditLog(ctx context.Context, viewer ViewerRef, query AuditLogQuery) (AuditEventPageDTO, error) {
	if s == nil || s.audit == nil {
		return AuditEventPageDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionAuditRead); err != nil {
		return AuditEventPageDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionAuditRead); err != nil {
		return AuditEventPageDTO{}, err
	}
	if query.Limit <= 0 {
		query.Limit = defaultAuditLogPageLimit
	} else if query.Limit > maximumAuditLogPageLimit {
		query.Limit = maximumAuditLogPageLimit
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	return s.audit.ListAuditEvents(ctx, query)
}

func (s *QueryService) ListEvaluations(ctx context.Context, viewer ViewerRef, query EvaluationQuery) (EvaluationPageDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionOperationsRead); err != nil {
		return EvaluationPageDTO{}, err
	}
	if s != nil && s.evals != nil {
		return s.evals.ListEvaluationItems(ctx, query)
	}
	if s == nil || s.missions == nil {
		return EvaluationPageDTO{}, ErrServiceDependency
	}
	model, err := s.missions.GetMissionControl(ctx, missioncore.MissionID(query.MissionID))
	if err != nil {
		return EvaluationPageDTO{}, err
	}
	items := evaluationsFromMission(model, query)
	return EvaluationPageDTO{Items: items, Page: PageDTO{Total: len(items), Returned: len(items)}}, nil
}

func evaluationProjectionKey(item EvaluationItemDTO) string {
	return strings.Join([]string{recordKeySegment(item.MissionID), recordKeySegment(item.EntityType), recordKeySegment(item.EntityID)}, "|")
}

func evaluationRecord(item EvaluationItemDTO) db.Record {
	return db.Record{
		"mission_id":               item.MissionID,
		"entity_type":              item.EntityType,
		"entity_id":                item.EntityID,
		"display_name":             item.DisplayName,
		"role_name":                item.RoleName,
		"department":               item.Department,
		"position":                 item.Position,
		"runtime_kind":             item.RuntimeKind,
		"assigned_tasks":           item.AssignedTasks,
		"completed_tasks":          item.CompletedTasks,
		"rework_count":             item.ReworkCount,
		"blocker_count":            item.BlockerCount,
		"resource_usage_count":     item.ResourceUsageCount,
		"discussion_reply_count":   item.DiscussionReplyCount,
		"average_duration_seconds": item.AverageDurationSeconds,
		"token_consumed":           item.TokenConsumed,
		"quality_score":            item.QualityScore,
		"on_time_rate":             item.OnTimeRate,
		"collaboration_score":      item.CollaborationScore,
		"efficiency_score":         item.EfficiencyScore,
		"stability_score":          item.StabilityScore,
		"summary":                  item.Summary,
		"capabilities":             item.Capabilities,
		"investment":               item.Investment,
		"latest_review":            item.LatestReview,
		"task_history":             item.TaskHistory,
		"trend":                    item.Trend,
		"representative_cases":     item.RepresentativeCases,
		"suggestions":              item.Suggestions,
		"advantages":               item.Advantages,
		"risks":                    item.Risks,
		"scenarios":                item.Scenarios,
		"last_activity_at":         item.LastActivityAt.UTC().Format(time.RFC3339Nano),
	}
}

func evaluationReviewRecord(review EvaluationReviewDTO) db.Record {
	return db.Record{
		"review_id":                 review.ReviewID,
		"entity_type":               review.EntityType,
		"entity_id":                 review.EntityID,
		"scope_kind":                review.ScopeKind,
		"scope_id":                  review.ScopeID,
		"evaluator_agent_id":        review.EvaluatorAgentID,
		"conversation_id":           review.ConversationID,
		"scheduled_run_id":          review.ScheduledRunID,
		"window_days":               review.WindowDays,
		"summary":                   review.Summary,
		"estimated_human_days":      review.EstimatedHumanDays,
		"evaluation_token_consumed": review.EvaluationTokenConsumed,
		"created_at":                review.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func evaluationFromRecord(record db.Record) EvaluationItemDTO {
	item := EvaluationItemDTO{
		MissionID:              recordString(record, "mission_id"),
		EntityType:             recordString(record, "entity_type"),
		EntityID:               recordString(record, "entity_id"),
		DisplayName:            recordString(record, "display_name"),
		RoleName:               recordString(record, "role_name"),
		Department:             recordString(record, "department"),
		Position:               recordString(record, "position"),
		RuntimeKind:            recordString(record, "runtime_kind"),
		AssignedTasks:          recordInt(record, "assigned_tasks"),
		CompletedTasks:         recordInt(record, "completed_tasks"),
		ReworkCount:            recordInt(record, "rework_count"),
		BlockerCount:           recordInt(record, "blocker_count"),
		ResourceUsageCount:     recordInt(record, "resource_usage_count"),
		DiscussionReplyCount:   recordInt(record, "discussion_reply_count"),
		AverageDurationSeconds: recordInt(record, "average_duration_seconds"),
		TokenConsumed:          recordInt64(record, "token_consumed"),
		QualityScore:           recordFloat(record, "quality_score"),
		OnTimeRate:             recordFloat(record, "on_time_rate"),
		CollaborationScore:     recordFloat(record, "collaboration_score"),
		EfficiencyScore:        recordFloat(record, "efficiency_score"),
		StabilityScore:         recordFloat(record, "stability_score"),
		Summary:                recordString(record, "summary"),
	}
	decodeRecordValue(record, "capabilities", &item.Capabilities)
	decodeRecordValue(record, "investment", &item.Investment)
	decodeRecordValue(record, "latest_review", &item.LatestReview)
	decodeRecordValue(record, "task_history", &item.TaskHistory)
	decodeRecordValue(record, "trend", &item.Trend)
	decodeRecordValue(record, "representative_cases", &item.RepresentativeCases)
	decodeRecordValue(record, "suggestions", &item.Suggestions)
	decodeRecordValue(record, "advantages", &item.Advantages)
	decodeRecordValue(record, "risks", &item.Risks)
	decodeRecordValue(record, "scenarios", &item.Scenarios)
	if at, err := time.Parse(time.RFC3339Nano, recordString(record, "last_activity_at")); err == nil {
		item.LastActivityAt = at
	}
	return item
}

func evaluationReviewFromRecord(record db.Record) EvaluationReviewDTO {
	review := EvaluationReviewDTO{
		ReviewID:                recordString(record, "review_id"),
		EntityType:              recordString(record, "entity_type"),
		EntityID:                recordString(record, "entity_id"),
		ScopeKind:               recordString(record, "scope_kind"),
		ScopeID:                 recordString(record, "scope_id"),
		EvaluatorAgentID:        recordString(record, "evaluator_agent_id"),
		ConversationID:          recordString(record, "conversation_id"),
		ScheduledRunID:          recordString(record, "scheduled_run_id"),
		WindowDays:              recordInt(record, "window_days"),
		Summary:                 recordString(record, "summary"),
		EstimatedHumanDays:      recordFloat(record, "estimated_human_days"),
		EvaluationTokenConsumed: recordInt64(record, "evaluation_token_consumed"),
	}
	if at, err := time.Parse(time.RFC3339Nano, recordString(record, "created_at")); err == nil {
		review.CreatedAt = at
	}
	return normalizeEvaluationReview(review)
}

func normalizeEvaluationReview(review EvaluationReviewDTO) EvaluationReviewDTO {
	review.ReviewID = strings.TrimSpace(review.ReviewID)
	review.EntityType = strings.TrimSpace(review.EntityType)
	review.EntityID = strings.TrimSpace(review.EntityID)
	review.ScopeKind = strings.TrimSpace(review.ScopeKind)
	review.ScopeID = strings.TrimSpace(review.ScopeID)
	review.EvaluatorAgentID = strings.TrimSpace(review.EvaluatorAgentID)
	review.ConversationID = strings.TrimSpace(review.ConversationID)
	review.ScheduledRunID = strings.TrimSpace(review.ScheduledRunID)
	review.Summary = strings.TrimSpace(review.Summary)
	if review.EntityType == "" {
		review.EntityType = review.ScopeKind
	}
	if review.EntityID == "" {
		review.EntityID = review.ScopeID
	}
	if review.ScopeKind == "" {
		review.ScopeKind = review.EntityType
	}
	if review.ScopeID == "" {
		review.ScopeID = review.EntityID
	}
	if review.EstimatedHumanDays < 0 {
		review.EstimatedHumanDays = 0
	}
	if review.EvaluationTokenConsumed < 0 {
		review.EvaluationTokenConsumed = 0
	}
	return review
}

func applyEvaluationReviews(items []EvaluationItemDTO, reviews []EvaluationReviewDTO, query EvaluationQuery, grouped bool) []EvaluationItemDTO {
	latest := latestEvaluationReviews(reviews, query)
	if len(latest) == 0 {
		return items
	}
	index := map[string]int{}
	for i := range items {
		index[evaluationReviewKey(items[i].EntityType, items[i].EntityID)] = i
	}
	for key, review := range latest {
		if query.MissionID != "" && !grouped {
			continue
		}
		if i, ok := index[key]; ok {
			finalizeEvaluationScores(&items[i])
			applyEvaluationReviewToItem(&items[i], review)
			continue
		}
		item := evaluationItemFromReview(review)
		if item.EntityType == "" || item.EntityID == "" {
			continue
		}
		finalizeEvaluationScores(&item)
		applyEvaluationReviewToItem(&item, review)
		index[key] = len(items)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastActivityAt.After(items[j].LastActivityAt) })
	return items
}

func latestEvaluationReviews(reviews []EvaluationReviewDTO, query EvaluationQuery) map[string]EvaluationReviewDTO {
	latest := map[string]EvaluationReviewDTO{}
	for _, review := range reviews {
		review = normalizeEvaluationReview(review)
		if review.EntityType == "" || review.EntityID == "" {
			continue
		}
		if query.EntityType != "" && review.EntityType != query.EntityType {
			continue
		}
		if query.EntityID != "" && review.EntityID != query.EntityID {
			continue
		}
		key := evaluationReviewKey(review.EntityType, review.EntityID)
		existing, ok := latest[key]
		if !ok || review.CreatedAt.After(existing.CreatedAt) {
			latest[key] = review
		}
	}
	return latest
}

func applyEvaluationReviewToItem(item *EvaluationItemDTO, review EvaluationReviewDTO) {
	if item == nil {
		return
	}
	item.LatestReview = review
	if review.Summary != "" {
		item.Summary = review.Summary
	}
	if review.EstimatedHumanDays > 0 {
		item.Investment.EstimatedHumanDays = review.EstimatedHumanDays
		item.Investment.Summary = "价值产出来自评价 Agent 的人天估算；Token 消耗来自运行时真实记录。"
	}
	if review.CreatedAt.After(item.LastActivityAt) {
		item.LastActivityAt = review.CreatedAt
	}
}

func evaluationItemFromReview(review EvaluationReviewDTO) EvaluationItemDTO {
	name := review.EntityID
	if review.EntityType == "discussion" {
		name = "讨论组 " + review.EntityID
	}
	return EvaluationItemDTO{
		EntityType:     review.EntityType,
		EntityID:       review.EntityID,
		DisplayName:    name,
		LastActivityAt: review.CreatedAt,
		LatestReview:   review,
		Summary:        review.Summary,
		Investment: EvaluationInvestmentDTO{
			EstimatedHumanDays: review.EstimatedHumanDays,
			Summary:            "价值产出来自评价 Agent 的人天估算；Token 消耗来自运行时真实记录。",
		},
	}
}

func evaluationReviewKey(entityType, entityID string) string {
	return strings.Join([]string{recordKeySegment(entityType), recordKeySegment(entityID)}, "|")
}

func recordKeySegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\x00", "")
	return strings.ReplaceAll(value, "|", "_")
}

func recordInt(record db.Record, key string) int {
	switch value := record[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func recordInt64(record db.Record, key string) int64 {
	switch value := record[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func recordFloat(record db.Record, key string) float64 {
	switch value := record[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func decodeRecordValue(record db.Record, key string, dst any) {
	value, ok := record[key]
	if !ok || value == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, dst)
}
