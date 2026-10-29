package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

const (
	scheduledRunConfigurationSnapshotVersion = 1
	maxScheduledArtifactAvailabilityChecks   = 100
	maxScheduledResultArtifacts              = 100
)

type ServiceOptions struct {
	Clock            clock.Clock
	IDGenerator      idgen.ErrorGenerator
	Repository       *Repository
	ArtifactStore    ArtifactStore
	Locker           Locker
	MissionLifecycle MissionLifecycle
}

type Locker interface {
	Acquire(context.Context, string) (release func(), err error)
}

type MissionLifecycle interface {
	EnsureScheduledMission(context.Context, ScheduledTask) (string, error)
}

type Service struct {
	clock            clock.Clock
	ids              idgen.ErrorGenerator
	repo             *Repository
	artifactStore    ArtifactStore
	locker           Locker
	missionLifecycle MissionLifecycle
}

func NewService(opts ServiceOptions) *Service {
	serviceClock := opts.Clock
	if serviceClock == nil {
		serviceClock = clock.Real{}
	}
	ids := opts.IDGenerator
	if ids == nil {
		ids = idgen.NewRandom("sched")
	}
	return &Service{
		clock:            serviceClock,
		ids:              ids,
		repo:             opts.Repository,
		artifactStore:    opts.ArtifactStore,
		locker:           opts.Locker,
		missionLifecycle: opts.MissionLifecycle,
	}
}

func (s *Service) CreateTask(ctx context.Context, viewer ViewerRef, req CreateTaskRequest) (ScheduledTask, error) {
	if s == nil || s.clock == nil || s.ids == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	if err := ctx.Err(); err != nil {
		return ScheduledTask{}, err
	}
	if strings.TrimSpace(viewer.ViewerID) == "" || strings.TrimSpace(viewer.ProfileID) == "" || strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.IdempotencyKey) == "" {
		return ScheduledTask{}, ErrInvalidRequest
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	requestHash, err := schedulerRequestHash(req)
	if err != nil {
		return ScheduledTask{}, err
	}
	tenantID := normalizeTenantID(viewer.TenantID)
	release, err := s.acquire(ctx, "create:"+tenantID+":"+strings.TrimSpace(viewer.ViewerID)+":"+req.IdempotencyKey)
	if err != nil {
		return ScheduledTask{}, err
	}
	defer release()
	if s.repo != nil {
		if existing, ok, err := s.repo.FindTaskByIdempotency(ctx, tenantID, strings.TrimSpace(viewer.ViewerID), req.IdempotencyKey); err != nil {
			return ScheduledTask{}, err
		} else if ok {
			if existing.IdempotencyHash != "" && existing.IdempotencyHash != requestHash {
				return ScheduledTask{}, fmt.Errorf("%w: idempotency key was already used with a different request", ErrInvalidRequest)
			}
			return existing, nil
		}
	}
	schedule := normalizeSchedule(req.Schedule)
	target := normalizeTarget(req.Target)
	policy := normalizePolicy(req.Policy)
	execution := normalizeExecutionPolicy(req.Execution)
	delivery := normalizeDeliveryPolicy(req.Delivery)
	if isSystemManagedTargetMode(target.Mode) && normalizeViewerKind(viewer.ViewerKind) != ViewerKindSystem {
		return ScheduledTask{}, fmt.Errorf("%w: target mode %q is system-managed", ErrInvalidTarget, target.Mode)
	}
	if err := validatePolicy(policy); err != nil {
		return ScheduledTask{}, err
	}
	if err := validateExecutionPolicy(execution); err != nil {
		return ScheduledTask{}, err
	}
	if err := validateDeliveryPolicy(delivery); err != nil {
		return ScheduledTask{}, err
	}
	nextRunAt, err := NextOccurrence(schedule, s.clock.Now())
	if err != nil {
		return ScheduledTask{}, err
	}
	if err := validateTarget(target); err != nil {
		return ScheduledTask{}, err
	}
	if err := validateDeliveryContentArtifact(delivery, target); err != nil {
		return ScheduledTask{}, err
	}
	id, err := s.ids.NewIDContext()
	if err != nil {
		return ScheduledTask{}, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	status := TaskStatusEnabled
	if !enabled {
		status = TaskStatusDisabled
	}
	now := s.clock.Now().UTC()
	task := ScheduledTask{
		TenantID:         tenantID,
		ScheduledTaskID:  id,
		AgentTaskID:      AgentTaskIDForScheduledTask(id),
		Title:            strings.TrimSpace(req.Title),
		Description:      strings.TrimSpace(req.Description),
		Schedule:         schedule,
		Target:           target,
		Policy:           policy,
		Execution:        execution,
		Delivery:         delivery,
		Status:           status,
		Enabled:          enabled,
		NextRunAt:        nextRunAt,
		CreatedBy:        strings.TrimSpace(viewer.ViewerID),
		CreatedByKind:    normalizeViewerKind(viewer.ViewerKind),
		CreatedByName:    strings.TrimSpace(viewer.DisplayName),
		CreatorProfileID: strings.TrimSpace(viewer.ProfileID),
		IdempotencyKey:   req.IdempotencyKey,
		IdempotencyHash:  requestHash,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.ensureScheduledMission(ctx, &task); err != nil {
		return ScheduledTask{}, err
	}
	if s.repo != nil {
		if err := s.repo.SaveTask(ctx, task); err != nil {
			return ScheduledTask{}, err
		}
	}
	return task, nil
}

func AgentTaskIDForScheduledTask(scheduledTaskID string) string {
	scheduledTaskID = strings.TrimSpace(scheduledTaskID)
	if scheduledTaskID == "" {
		return ""
	}
	return "scheduled-task:" + scheduledTaskID
}

func normalizeViewerKind(kind ViewerKind) ViewerKind {
	kind = ViewerKind(strings.TrimSpace(string(kind)))
	if kind == "" {
		return ViewerKindUser
	}
	return kind
}

func normalizeTenantID(tenantID string) string {
	if tenantID = strings.TrimSpace(tenantID); tenantID != "" {
		return tenantID
	}
	return "system"
}

func (s *Service) ListTasks(ctx context.Context, query ListTasksQuery) (ScheduledTaskPage, error) {
	if s == nil || s.repo == nil {
		return ScheduledTaskPage{}, ErrServiceDependency
	}
	if err := validateListTasksQuery(query); err != nil {
		return ScheduledTaskPage{}, err
	}
	includeArchived := query.IncludeArchived || query.Status == TaskStatusArchived
	tasks, err := s.repo.ListTasks(ctx, includeArchived)
	if err != nil {
		return ScheduledTaskPage{}, err
	}
	query.TenantID = normalizeTenantID(query.TenantID)
	tasks = filterTasks(tasks, query)
	if query.Sort != "" && query.Sort != SortNextRunAsc {
		sortTasksForQuery(tasks, query.Sort)
	}
	total := len(tasks)
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := query.Limit
	if limit <= 0 || limit > total-offset {
		limit = total - offset
	}
	pageTasks := tasks[offset : offset+limit]
	taskIDs := make([]string, 0, len(pageTasks))
	for _, task := range pageTasks {
		taskIDs = append(taskIDs, task.ScheduledTaskID)
	}
	lastRuns, err := s.repo.LastRunsForTasks(ctx, taskIDs)
	if err != nil {
		return ScheduledTaskPage{}, err
	}
	summaries := make([]ScheduledTaskSummary, 0, len(pageTasks))
	for _, task := range pageTasks {
		summary := summarizeTask(task)
		if query.ExpandOccurrences {
			summary.ScheduleOccurrences = scheduleOccurrencesForWindow(task.Schedule, query.From, query.To, query.OccurrenceTimezone)
		}
		if run, ok := lastRuns[task.ScheduledTaskID]; ok {
			summary.LastRunStatus = run.Status
			summary.FailureReason = run.FailureReason
		}
		summaries = append(summaries, summary)
	}
	return ScheduledTaskPage{Tasks: summaries, Total: total, Limit: query.Limit, Offset: query.Offset}, nil
}

func (s *Service) GetTask(ctx context.Context, scheduledTaskID string) (ScheduledTask, error) {
	if s == nil || s.repo == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	return s.repo.GetTask(ctx, strings.TrimSpace(scheduledTaskID))
}

func (s *Service) ListRuns(ctx context.Context, query ListRunsQuery) (ScheduledRunPage, error) {
	if s == nil || s.repo == nil {
		return ScheduledRunPage{}, ErrServiceDependency
	}
	query.ScheduledTaskID = strings.TrimSpace(query.ScheduledTaskID)
	if query.ScheduledTaskID == "" {
		return ScheduledRunPage{}, ErrInvalidRequest
	}
	if query.Status != "" && !isRunStatus(query.Status) {
		return ScheduledRunPage{}, ErrInvalidRequest
	}
	if !query.CreatedFrom.IsZero() && !query.CreatedBefore.IsZero() && !query.CreatedBefore.After(query.CreatedFrom) {
		return ScheduledRunPage{}, ErrInvalidRequest
	}
	task, err := s.repo.GetTask(ctx, query.ScheduledTaskID)
	if err != nil {
		return ScheduledRunPage{}, err
	}
	if normalizeTenantID(task.TenantID) != normalizeTenantID(query.TenantID) {
		return ScheduledRunPage{}, ErrNotFound
	}
	runs, err := s.repo.ListRunsForTask(ctx, query.ScheduledTaskID)
	if err != nil {
		return ScheduledRunPage{}, err
	}
	if runID := strings.TrimSpace(query.ScheduledRunID); runID != "" {
		filtered := make([]ScheduledRun, 0, 1)
		for _, run := range runs {
			if run.ScheduledRunID == runID {
				filtered = append(filtered, run)
				break
			}
		}
		runs = filtered
	}
	if query.Status != "" {
		filtered := make([]ScheduledRun, 0, len(runs))
		for _, run := range runs {
			if run.Status == query.Status {
				filtered = append(filtered, run)
			}
		}
		runs = filtered
	}
	if !query.CreatedFrom.IsZero() || !query.CreatedBefore.IsZero() || query.ActiveOnly {
		filtered := make([]ScheduledRun, 0, len(runs))
		for _, run := range runs {
			if !query.CreatedFrom.IsZero() && run.CreatedAt.Before(query.CreatedFrom) {
				continue
			}
			if !query.CreatedBefore.IsZero() && !run.CreatedAt.Before(query.CreatedBefore) {
				continue
			}
			if query.ActiveOnly && run.Status != RunStatusQueued && run.Status != RunStatusRunning && !(run.Status == RunStatusFailed && run.CompletedAt.IsZero()) {
				continue
			}
			filtered = append(filtered, run)
		}
		runs = filtered
	}
	total := len(runs)
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := query.Limit
	if limit <= 0 || limit > total-offset {
		limit = total - offset
	}
	pageRuns := append([]ScheduledRun(nil), runs[offset:offset+limit]...)
	for index := range pageRuns {
		pageRuns[index].Result.Artifacts = append([]RunArtifact(nil), pageRuns[index].Result.Artifacts...)
	}
	s.annotateArtifactAvailability(ctx, task, pageRuns)
	return ScheduledRunPage{Runs: pageRuns, Total: total, Limit: query.Limit, Offset: query.Offset}, nil
}

func (s *Service) ListResponsibilityEvaluationItems(
	ctx context.Context,
	query ListResponsibilityEvaluationItemsQuery,
) (ResponsibilityEvaluationItemPage, error) {
	if s == nil || s.repo == nil {
		return ResponsibilityEvaluationItemPage{}, ErrServiceDependency
	}
	query.ScheduledTaskID = strings.TrimSpace(query.ScheduledTaskID)
	query.ScheduledRunID = strings.TrimSpace(query.ScheduledRunID)
	query.Status = strings.TrimSpace(query.Status)
	query.Cursor = strings.TrimSpace(query.Cursor)
	if query.ScheduledTaskID == "" || query.ScheduledRunID == "" ||
		(query.Status != "" && query.Status != ResponsibilityEvaluationItemCompleted && query.Status != ResponsibilityEvaluationItemFailed) {
		return ResponsibilityEvaluationItemPage{}, ErrInvalidRequest
	}
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 100 {
		return ResponsibilityEvaluationItemPage{}, ErrInvalidRequest
	}
	task, err := s.repo.GetTask(ctx, query.ScheduledTaskID)
	if err != nil {
		return ResponsibilityEvaluationItemPage{}, err
	}
	if normalizeTenantID(task.TenantID) != normalizeTenantID(query.TenantID) {
		return ResponsibilityEvaluationItemPage{}, ErrNotFound
	}
	run, err := s.repo.GetRun(ctx, query.ScheduledRunID)
	if err != nil {
		return ResponsibilityEvaluationItemPage{}, err
	}
	if run.ScheduledTaskID != query.ScheduledTaskID || run.TargetMode != TargetModeResponsibilityEvaluation ||
		run.ResponsibilityEvaluationBatch == nil {
		return ResponsibilityEvaluationItemPage{}, ErrNotFound
	}
	return s.repo.ListResponsibilityEvaluationItemsPage(ctx, query.ScheduledRunID, query.Status, query.Cursor, query.Limit)
}

func (s *Service) annotateArtifactAvailability(ctx context.Context, task ScheduledTask, runs []ScheduledRun) {
	checker, ok := s.artifactStore.(ArtifactAvailabilityChecker)
	if !ok {
		return
	}
	for runIndex := range runs {
		for artifactIndex := range runs[runIndex].Result.Artifacts {
			runs[runIndex].Result.Artifacts[artifactIndex].ContentAvailable = false
			runs[runIndex].Result.Artifacts[artifactIndex].ContentAvailabilityChecked = false
		}
	}
	checked := 0
	for runIndex := range runs {
		for artifactIndex := range runs[runIndex].Result.Artifacts {
			if ctx.Err() != nil || checked >= maxScheduledArtifactAvailabilityChecks {
				return
			}
			checked++
			artifact := runs[runIndex].Result.Artifacts[artifactIndex]
			runs[runIndex].Result.Artifacts[artifactIndex].ContentAvailable = checker.ArtifactAvailable(ctx, RunArtifactAccess{
				Task: task, Run: runs[runIndex], Artifact: artifact,
			})
			runs[runIndex].Result.Artifacts[artifactIndex].ContentAvailabilityChecked = true
		}
	}
}

func (s *Service) OpenRunArtifact(ctx context.Context, query OpenRunArtifactQuery) (RunArtifactContent, error) {
	if s == nil || s.repo == nil || s.artifactStore == nil {
		return RunArtifactContent{}, ErrServiceDependency
	}
	query.ScheduledTaskID = strings.TrimSpace(query.ScheduledTaskID)
	query.ScheduledRunID = strings.TrimSpace(query.ScheduledRunID)
	query.ArtifactID = strings.TrimSpace(query.ArtifactID)
	if query.ScheduledTaskID == "" || query.ScheduledRunID == "" || query.ArtifactID == "" {
		return RunArtifactContent{}, ErrInvalidRequest
	}
	task, err := s.repo.GetTask(ctx, query.ScheduledTaskID)
	if err != nil {
		return RunArtifactContent{}, err
	}
	if normalizeTenantID(task.TenantID) != normalizeTenantID(query.TenantID) {
		return RunArtifactContent{}, ErrNotFound
	}
	run, err := s.repo.GetRun(ctx, query.ScheduledRunID)
	if err != nil {
		return RunArtifactContent{}, err
	}
	if strings.TrimSpace(run.ScheduledTaskID) != query.ScheduledTaskID {
		return RunArtifactContent{}, ErrNotFound
	}
	for index, artifact := range run.Result.Artifacts {
		if PublicRunArtifactID(run.ScheduledRunID, index, artifact) != query.ArtifactID {
			continue
		}
		access := RunArtifactAccess{Task: task, Run: run, Artifact: artifact}
		content, openErr := s.artifactStore.OpenArtifact(ctx, access)
		if openErr == nil {
			if content.Reader == nil {
				return RunArtifactContent{}, ErrServiceDependency
			}
			return content, nil
		}
		if !errors.Is(openErr, ErrNotFound) {
			return RunArtifactContent{}, openErr
		}
		archiver, ok := s.artifactStore.(HistoricalArtifactArchiver)
		if !ok {
			return RunArtifactContent{}, openErr
		}
		archived, err := archiver.ArchiveHistoricalArtifact(ctx, access)
		if err != nil {
			return RunArtifactContent{}, err
		}
		if !validHistoricalArtifactReplacement(artifact, archived) {
			return RunArtifactContent{}, ErrServiceDependency
		}
		persisted, err := s.repo.MutateRunWithTask(ctx, task.ScheduledTaskID, run.ScheduledRunID, func(currentTask ScheduledTask, current *ScheduledRun) error {
			if !historicalArtifactTaskAuthorizationUnchanged(task, currentTask, run) {
				return ErrRevisionConflict
			}
			if !historicalArtifactRunAuthorizationUnchanged(run, *current) || index >= len(current.Result.Artifacts) {
				return ErrNotFound
			}
			currentArtifact := current.Result.Artifacts[index]
			if PublicRunArtifactID(current.ScheduledRunID, index, currentArtifact) != query.ArtifactID {
				return ErrNotFound
			}
			// A concurrent reader may already have committed the same migration.
			// Only replace the exact historical metadata observed above.
			switch {
			case currentArtifact == artifact:
				current.Result.Artifacts[index] = archived
			case currentArtifact == archived:
				return nil
			default:
				return ErrRevisionConflict
			}
			return nil
		})
		if err != nil {
			return RunArtifactContent{}, err
		}
		persistedArtifact := persisted.Result.Artifacts[index]
		content, err = s.artifactStore.OpenArtifact(ctx, RunArtifactAccess{Task: task, Run: persisted, Artifact: persistedArtifact})
		if err != nil {
			return RunArtifactContent{}, err
		}
		if content.Reader == nil {
			return RunArtifactContent{}, ErrServiceDependency
		}
		return content, nil
	}
	return RunArtifactContent{}, ErrNotFound
}

func validHistoricalArtifactReplacement(previous, archived RunArtifact) bool {
	if archived.ID != previous.ID || archived.Kind != previous.Kind || archived.EvidenceID != previous.EvidenceID ||
		strings.TrimSpace(archived.URI) == "" || strings.TrimSpace(archived.URI) == strings.TrimSpace(previous.URI) ||
		strings.TrimSpace(archived.FileName) == "" || archived.SizeBytes < 0 || strings.TrimSpace(archived.SHA256) == "" {
		return false
	}
	if previous.SizeBytes > 0 && archived.SizeBytes != previous.SizeBytes {
		return false
	}
	if previous.SHA256 != "" && !strings.EqualFold(strings.TrimSpace(previous.SHA256), strings.TrimSpace(archived.SHA256)) {
		return false
	}
	return true
}

func historicalArtifactTaskAuthorizationUnchanged(previous, current ScheduledTask, run ScheduledRun) bool {
	if strings.TrimSpace(previous.ScheduledTaskID) != strings.TrimSpace(current.ScheduledTaskID) ||
		normalizeTenantID(previous.TenantID) != normalizeTenantID(current.TenantID) {
		return false
	}
	if run.ConfigurationSnapshotVersion > 0 {
		return true
	}
	return previous.Target.Mode == current.Target.Mode &&
		strings.TrimSpace(previous.Target.AgentID) == strings.TrimSpace(current.Target.AgentID) &&
		agentResultContractsEqual(previous.Target.ResultContract, current.Target.ResultContract)
}

func historicalArtifactRunAuthorizationUnchanged(previous, current ScheduledRun) bool {
	return strings.TrimSpace(previous.ScheduledRunID) == strings.TrimSpace(current.ScheduledRunID) &&
		strings.TrimSpace(previous.ScheduledTaskID) == strings.TrimSpace(current.ScheduledTaskID) &&
		previous.Status == current.Status && previous.TargetMode == current.TargetMode &&
		previous.ConfigurationSnapshotVersion == current.ConfigurationSnapshotVersion &&
		reflect.DeepEqual(previous.ConfigurationSnapshot, current.ConfigurationSnapshot) &&
		normalizeTenantID(previous.TenantID) == normalizeTenantID(current.TenantID) &&
		strings.TrimSpace(previous.ScheduledTaskTitle) == strings.TrimSpace(current.ScheduledTaskTitle) &&
		agentResultContractsEqual(previous.ResultContract, current.ResultContract) && previous.Delivery == current.Delivery &&
		strings.TrimSpace(previous.AgentID) == strings.TrimSpace(current.AgentID) &&
		strings.TrimSpace(previous.RuntimeRunID) == strings.TrimSpace(current.RuntimeRunID) &&
		strings.TrimSpace(previous.MissionID) == strings.TrimSpace(current.MissionID) &&
		strings.TrimSpace(previous.TaskID) == strings.TrimSpace(current.TaskID) &&
		previous.StartedAt.Equal(current.StartedAt) && previous.CompletedAt.Equal(current.CompletedAt)
}

func (s *Service) UpdateTask(ctx context.Context, viewer ViewerRef, req UpdateTaskRequest) (ScheduledTask, error) {
	if s == nil || s.repo == nil || s.clock == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	req.ScheduledTaskID = strings.TrimSpace(req.ScheduledTaskID)
	if strings.TrimSpace(viewer.ViewerID) == "" || req.ScheduledTaskID == "" {
		return ScheduledTask{}, ErrInvalidRequest
	}
	release, err := s.acquire(ctx, "task-mutation:"+req.ScheduledTaskID)
	if err != nil {
		return ScheduledTask{}, err
	}
	defer release()
	task, err := s.repo.GetTask(ctx, req.ScheduledTaskID)
	if err != nil {
		return ScheduledTask{}, err
	}
	previous := task
	if normalizeTenantID(task.TenantID) != normalizeTenantID(viewer.TenantID) {
		return ScheduledTask{}, ErrNotFound
	}
	if lockedSystemManagedTargetMode(task.Target.Mode) && normalizeViewerKind(viewer.ViewerKind) != ViewerKindSystem {
		return ScheduledTask{}, fmt.Errorf("%w: schedule target is system-managed", ErrInvalidTarget)
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return ScheduledTask{}, ErrInvalidRequest
		}
		task.Title = title
	}
	if req.Description != nil {
		task.Description = strings.TrimSpace(*req.Description)
	}
	if req.Schedule != nil {
		schedule := normalizeSchedule(*req.Schedule)
		if _, err := NextOccurrence(schedule, s.clock.Now()); err != nil {
			return ScheduledTask{}, err
		}
		task.Schedule = schedule
	}
	if req.Target != nil {
		target := normalizeTarget(*req.Target)
		previousMode := task.Target.Mode
		viewerKind := normalizeViewerKind(viewer.ViewerKind)
		if viewerKind != ViewerKindSystem && (isSystemManagedTargetMode(target.Mode) || isSystemManagedTargetMode(task.Target.Mode)) && target.Mode != task.Target.Mode {
			return ScheduledTask{}, fmt.Errorf("%w: system-managed target modes cannot be added or removed", ErrInvalidTarget)
		}
		if err := validateTarget(target); err != nil {
			return ScheduledTask{}, err
		}
		task.Target = target
		if previousMode == TargetModeAgentTask && target.Mode == TargetModeResponsibilityEvaluation {
			task.MissionID = ""
		}
	}
	if req.Policy != nil {
		policy := normalizePolicy(*req.Policy)
		if err := validatePolicy(policy); err != nil {
			return ScheduledTask{}, err
		}
		task.Policy = policy
	}
	if req.Execution != nil {
		execution := normalizeExecutionPolicy(*req.Execution)
		if err := validateExecutionPolicy(execution); err != nil {
			return ScheduledTask{}, err
		}
		task.Execution = execution
	}
	if req.Delivery != nil {
		delivery := normalizeDeliveryPolicy(*req.Delivery)
		if err := validateDeliveryPolicy(delivery); err != nil {
			return ScheduledTask{}, err
		}
		task.Delivery = delivery
	}
	if err := validateDeliveryContentArtifact(task.Delivery, task.Target); err != nil {
		return ScheduledTask{}, err
	}
	if req.Enabled != nil {
		task.Enabled = *req.Enabled
		if task.Enabled {
			task.Status = TaskStatusEnabled
		} else {
			task.Status = TaskStatusDisabled
		}
	}
	next, err := NextOccurrence(task.Schedule, s.clock.Now())
	if err != nil {
		return ScheduledTask{}, err
	}
	task.NextRunAt = next
	task.UpdatedAt = nextScheduledTaskMutationTime(s.clock.Now(), previous.UpdatedAt)
	if err := s.ensureScheduledMission(ctx, &task); err != nil {
		return ScheduledTask{}, err
	}
	return s.replaceTaskIfUnchanged(ctx, previous, task)
}

func (s *Service) CancelTask(ctx context.Context, viewer ViewerRef, req CancelTaskRequest) (ScheduledTask, error) {
	if s == nil || s.repo == nil || s.clock == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	req.ScheduledTaskID = strings.TrimSpace(req.ScheduledTaskID)
	if strings.TrimSpace(viewer.ViewerID) == "" || req.ScheduledTaskID == "" {
		return ScheduledTask{}, ErrInvalidRequest
	}
	release, err := s.acquire(ctx, "task-mutation:"+req.ScheduledTaskID)
	if err != nil {
		return ScheduledTask{}, err
	}
	defer release()
	task, err := s.repo.GetTask(ctx, req.ScheduledTaskID)
	if err != nil {
		return ScheduledTask{}, err
	}
	previous := task
	if normalizeTenantID(task.TenantID) != normalizeTenantID(viewer.TenantID) {
		return ScheduledTask{}, ErrNotFound
	}
	if lockedSystemManagedTargetMode(task.Target.Mode) && normalizeViewerKind(viewer.ViewerKind) != ViewerKindSystem {
		return ScheduledTask{}, fmt.Errorf("%w: schedule target is system-managed", ErrInvalidTarget)
	}
	task.Enabled = false
	task.Status = TaskStatusArchived
	task.UpdatedAt = nextScheduledTaskMutationTime(s.clock.Now(), previous.UpdatedAt)
	if err := s.ensureScheduledMission(ctx, &task); err != nil {
		return ScheduledTask{}, err
	}
	return s.replaceTaskIfUnchanged(ctx, previous, task)
}

// ArchiveSystemManagedTask retires an internal schedule without invoking the
// Agent-task Mission lifecycle. It is intentionally unavailable to user-owned
// tasks and is used by startup migrations for superseded system schedules.
func (s *Service) ArchiveSystemManagedTask(ctx context.Context, viewer ViewerRef, req CancelTaskRequest) (ScheduledTask, error) {
	if s == nil || s.repo == nil || s.clock == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	req.ScheduledTaskID = strings.TrimSpace(req.ScheduledTaskID)
	if normalizeViewerKind(viewer.ViewerKind) != ViewerKindSystem || strings.TrimSpace(viewer.ViewerID) == "" || req.ScheduledTaskID == "" {
		return ScheduledTask{}, ErrInvalidRequest
	}
	release, err := s.acquire(ctx, "task-mutation:"+req.ScheduledTaskID)
	if err != nil {
		return ScheduledTask{}, err
	}
	defer release()
	return s.repo.MutateTask(ctx, req.ScheduledTaskID, func(task *ScheduledTask) error {
		if normalizeTenantID(task.TenantID) != normalizeTenantID(viewer.TenantID) {
			return ErrNotFound
		}
		if normalizeViewerKind(task.CreatedByKind) != ViewerKindSystem {
			return ErrInvalidRequest
		}
		if task.Status == TaskStatusArchived && !task.Enabled {
			return nil
		}
		task.Enabled = false
		task.Status = TaskStatusArchived
		task.UpdatedAt = nextScheduledTaskMutationTime(s.clock.Now(), task.UpdatedAt)
		return nil
	})
}

func (s *Service) replaceTaskIfUnchanged(ctx context.Context, previous, replacement ScheduledTask) (ScheduledTask, error) {
	scheduledTaskID := strings.TrimSpace(previous.ScheduledTaskID)
	if scheduledTaskID == "" || strings.TrimSpace(replacement.ScheduledTaskID) != scheduledTaskID {
		return ScheduledTask{}, ErrInvalidRequest
	}
	updated, err := s.repo.MutateTask(ctx, scheduledTaskID, func(current *ScheduledTask) error {
		if !scheduledTaskRevisionsEqual(*current, previous) {
			return ErrRevisionConflict
		}
		*current = replacement
		return nil
	})
	if !errors.Is(err, ErrRevisionConflict) {
		return updated, err
	}
	latest, latestErr := s.repo.GetTask(ctx, scheduledTaskID)
	if latestErr != nil {
		return updated, fmt.Errorf("%w: load latest task for Mission compensation: %v", err, latestErr)
	}
	if compensateErr := s.ensureScheduledMission(ctx, &latest); compensateErr != nil {
		return latest, fmt.Errorf("%w: restore latest Mission state: %v", err, compensateErr)
	}
	return latest, err
}

func scheduledTaskRevisionsEqual(first, second ScheduledTask) bool {
	first.TenantID = normalizeTenantID(first.TenantID)
	second.TenantID = normalizeTenantID(second.TenantID)
	if first.AgentTaskID == "" {
		first.AgentTaskID = AgentTaskIDForScheduledTask(first.ScheduledTaskID)
	}
	if second.AgentTaskID == "" {
		second.AgentTaskID = AgentTaskIDForScheduledTask(second.ScheduledTaskID)
	}
	return reflect.DeepEqual(first, second)
}

func nextScheduledTaskMutationTime(now, previous time.Time) time.Time {
	now = now.UTC()
	previous = previous.UTC()
	if !now.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return now
}

func (s *Service) ensureScheduledMission(ctx context.Context, task *ScheduledTask) error {
	if task == nil || task.Target.Mode != TargetModeAgentTask {
		return nil
	}
	if s == nil || s.missionLifecycle == nil {
		return ErrServiceDependency
	}
	missionID, err := s.missionLifecycle.EnsureScheduledMission(ctx, *task)
	if err != nil {
		return err
	}
	missionID = strings.TrimSpace(missionID)
	if missionID == "" {
		return ErrServiceDependency
	}
	if task.MissionID != "" && task.MissionID != missionID {
		return fmt.Errorf("%w: scheduled Mission identity changed", ErrInvalidRequest)
	}
	task.MissionID = missionID
	return nil
}

func (s *Service) PrepareManualRun(ctx context.Context, viewer ViewerRef, req RunNowRequest) (ScheduledRun, error) {
	if s == nil || s.repo == nil || s.clock == nil || s.ids == nil {
		return ScheduledRun{}, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" || strings.TrimSpace(req.ScheduledTaskID) == "" {
		return ScheduledRun{}, ErrInvalidRequest
	}
	req.ScheduledTaskID = strings.TrimSpace(req.ScheduledTaskID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	requestHash, err := schedulerRequestHash(req)
	if err != nil {
		return ScheduledRun{}, err
	}
	release, err := s.acquire(ctx, "run:"+req.ScheduledTaskID)
	if err != nil {
		return ScheduledRun{}, err
	}
	defer release()
	if req.IdempotencyKey != "" {
		if existing, ok, err := s.repo.FindManualRunByIdempotency(ctx, req.ScheduledTaskID, strings.TrimSpace(viewer.ViewerID), req.IdempotencyKey); err != nil {
			return ScheduledRun{}, err
		} else if ok {
			if existing.IdempotencyHash != "" && existing.IdempotencyHash != requestHash {
				return ScheduledRun{}, fmt.Errorf("%w: idempotency key was already used with a different request", ErrInvalidRequest)
			}
			return existing, nil
		}
	}
	task, err := s.repo.GetTask(ctx, req.ScheduledTaskID)
	if err != nil {
		return ScheduledRun{}, err
	}
	if normalizeTenantID(task.TenantID) != normalizeTenantID(viewer.TenantID) {
		return ScheduledRun{}, ErrNotFound
	}
	if !req.Force && task.Policy.Concurrency != ConcurrencyAllowParallel {
		active, err := s.repo.HasActiveRun(ctx, task.ScheduledTaskID)
		if err != nil {
			return ScheduledRun{}, err
		}
		if active {
			return s.createRun(ctx, task, RunTriggerManual, RunStatusSkipped, "active run already exists", strings.TrimSpace(viewer.ViewerID), req.IdempotencyKey, requestHash)
		}
	}
	return s.createRun(ctx, task, RunTriggerManual, RunStatusQueued, "", strings.TrimSpace(viewer.ViewerID), req.IdempotencyKey, requestHash)
}

func (s *Service) PrepareDueRun(ctx context.Context, task ScheduledTask) (ScheduledRun, error) {
	if s == nil || s.repo == nil || s.clock == nil || s.ids == nil {
		return ScheduledRun{}, ErrServiceDependency
	}
	if strings.TrimSpace(task.ScheduledTaskID) == "" {
		return ScheduledRun{}, ErrInvalidRequest
	}
	release, err := s.acquire(ctx, "run:"+strings.TrimSpace(task.ScheduledTaskID))
	if err != nil {
		return ScheduledRun{}, err
	}
	defer release()
	return s.prepareDueRunLocked(ctx, task)
}

func (s *Service) prepareDueRunLocked(ctx context.Context, task ScheduledTask) (ScheduledRun, error) {
	if task.Policy.Concurrency != ConcurrencyAllowParallel {
		active, err := s.repo.HasActiveRun(ctx, task.ScheduledTaskID)
		if err != nil {
			return ScheduledRun{}, err
		}
		if active {
			return s.createRun(ctx, task, RunTriggerDue, RunStatusSkipped, "active run already exists", "", "", "")
		}
	}
	return s.createRun(ctx, task, RunTriggerDue, RunStatusQueued, "", "", "", "")
}

func (s *Service) PrepareMisfireSkip(ctx context.Context, task ScheduledTask) (ScheduledRun, error) {
	release, err := s.acquire(ctx, "run:"+strings.TrimSpace(task.ScheduledTaskID))
	if err != nil {
		return ScheduledRun{}, err
	}
	defer release()
	return s.createRun(ctx, task, RunTriggerDue, RunStatusSkipped, "scheduled run skipped after misfire grace period", "", "", "")
}

func (s *Service) ProcessDueTask(ctx context.Context, taskID string, dispatcher RunDispatcher, now time.Time, misfireGrace time.Duration) error {
	if s == nil || s.repo == nil || dispatcher == nil {
		return ErrServiceDependency
	}
	taskID = strings.TrimSpace(taskID)
	release, err := s.acquire(ctx, "run:"+taskID)
	if err != nil {
		return err
	}
	defer release()
	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !task.Enabled || task.Status != TaskStatusEnabled || task.NextRunAt.IsZero() || task.NextRunAt.After(now) {
		return nil
	}
	if misfireGrace <= 0 {
		misfireGrace = time.Minute
	}
	if task.Policy.Misfire == MisfireSkip && task.NextRunAt.Before(now.Add(-misfireGrace)) {
		if _, err := s.createRun(ctx, task, RunTriggerDue, RunStatusSkipped, "scheduled run skipped after misfire grace period", "", "", ""); err != nil {
			return err
		}
		_, err = s.AdvanceAfterDueRun(ctx, task)
		return err
	}
	run, err := s.prepareDueRunLocked(ctx, task)
	if err != nil {
		return err
	}
	if run.Status == RunStatusQueued {
		if _, err := dispatcher.DispatchRun(ctx, task, run); err != nil {
			return err
		}
	}
	_, err = s.AdvanceAfterDueRun(ctx, task)
	return err
}

func (s *Service) AdvanceAfterDueRun(ctx context.Context, task ScheduledTask) (ScheduledTask, error) {
	if s == nil || s.repo == nil || s.clock == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	task.ScheduledTaskID = strings.TrimSpace(task.ScheduledTaskID)
	if task.ScheduledTaskID == "" {
		return ScheduledTask{}, ErrInvalidRequest
	}
	task.TenantID = normalizeTenantID(task.TenantID)
	if task.AgentTaskID == "" {
		task.AgentTaskID = AgentTaskIDForScheduledTask(task.ScheduledTaskID)
	}
	release, err := s.acquire(ctx, "task-mutation:"+task.ScheduledTaskID)
	if err != nil {
		return ScheduledTask{}, err
	}
	defer release()
	previous := task
	task.UpdatedAt = nextScheduledTaskMutationTime(s.clock.Now(), previous.UpdatedAt)
	switch task.Schedule.Type {
	case ScheduleTypeOnce:
		task.Enabled = false
		task.Status = TaskStatusDisabled
		task.NextRunAt = time.Time{}
	case ScheduleTypeCron:
		next, err := NextOccurrence(task.Schedule, s.clock.Now())
		if err != nil {
			return ScheduledTask{}, err
		}
		task.Enabled = true
		task.Status = TaskStatusEnabled
		task.NextRunAt = next
	default:
		return ScheduledTask{}, fmt.Errorf("%w: unsupported schedule type %q", ErrInvalidSchedule, task.Schedule.Type)
	}
	if err := s.ensureScheduledMission(ctx, &task); err != nil {
		return ScheduledTask{}, err
	}
	return s.replaceTaskIfUnchanged(ctx, previous, task)
}

func (s *Service) createRun(ctx context.Context, task ScheduledTask, trigger RunTriggerType, status RunStatus, failureReason, requestedBy, idempotencyKey, idempotencyHash string) (ScheduledRun, error) {
	id, err := s.ids.NewIDContext()
	if err != nil {
		return ScheduledRun{}, err
	}
	configurationSnapshot, err := cloneScheduledTaskConfiguration(task)
	if err != nil {
		return ScheduledRun{}, fmt.Errorf("snapshot scheduled task configuration: %w", err)
	}
	now := s.clock.Now().UTC()
	run := ScheduledRun{
		ScheduledRunID:               id,
		ScheduledTaskID:              task.ScheduledTaskID,
		TriggerType:                  trigger,
		Status:                       status,
		TargetMode:                   task.Target.Mode,
		ConfigurationSnapshotVersion: scheduledRunConfigurationSnapshotVersion,
		ConfigurationSnapshot:        &configurationSnapshot,
		TenantID:                     task.TenantID,
		ScheduledTaskTitle:           task.Title,
		ResultContract:               cloneAgentResultContract(task.Target.ResultContract),
		Delivery:                     task.Delivery,
		ConversationID:               task.Target.ConversationID,
		AgentID:                      task.Target.AgentID,
		Attempt:                      1,
		FailureReason:                failureReason,
		RequestedBy:                  requestedBy,
		IdempotencyKey:               idempotencyKey,
		IdempotencyHash:              idempotencyHash,
		CreatedAt:                    now,
	}
	if status == RunStatusRunning {
		run.StartedAt = now
	}
	if status == RunStatusSkipped || status == RunStatusFailed || status == RunStatusCompleted {
		run.CompletedAt = now
	}
	if err := s.repo.SaveRun(ctx, run); err != nil {
		return ScheduledRun{}, err
	}
	return run, nil
}

func cloneAgentResultContract(contract AgentResultContract) AgentResultContract {
	cloned := contract
	cloned.Artifacts = append([]ArtifactRequirement(nil), contract.Artifacts...)
	return cloned
}

func cloneScheduledTaskConfiguration(task ScheduledTask) (ScheduledTask, error) {
	payload, err := json.Marshal(task)
	if err != nil {
		return ScheduledTask{}, err
	}
	var cloned ScheduledTask
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return ScheduledTask{}, err
	}
	return cloned, nil
}

func schedulerRequestHash(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("hash scheduler request: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) acquire(ctx context.Context, key string) (func(), error) {
	if s.locker == nil {
		return func() {}, nil
	}
	return s.locker.Acquire(ctx, key)
}

func summarizeTask(task ScheduledTask) ScheduledTaskSummary {
	return ScheduledTaskSummary{
		ScheduledTaskID: task.ScheduledTaskID,
		Title:           task.Title,
		Status:          task.Status,
		Enabled:         task.Enabled,
		ScheduleType:    task.Schedule.Type,
		TargetMode:      task.Target.Mode,
		TargetSummary:   targetSummary(task.Target),
		ExecutorAgentID: executorAgentID(task.Target),
		NextRunAt:       task.NextRunAt,
	}
}

func executorAgentID(target Target) string {
	switch target.Mode {
	case TargetModeDiscussionWorkflow:
		return strings.TrimSpace(target.LeadAgentID)
	case TargetModeAgentTask, TargetModeResponsibilityEvaluation:
		return strings.TrimSpace(target.AgentID)
	default:
		return ""
	}
}

func validateListTasksQuery(query ListTasksQuery) error {
	if query.Status != "" && !isTaskStatus(query.Status) {
		return ErrInvalidRequest
	}
	if query.Sort != "" && !isTaskSort(query.Sort) {
		return ErrInvalidRequest
	}
	if query.ExpandOccurrences {
		if query.From.IsZero() || query.To.IsZero() || !query.To.After(query.From) || query.To.Sub(query.From) > 31*24*time.Hour {
			return ErrInvalidRequest
		}
		if query.OccurrenceTimezone != "" {
			if _, err := time.LoadLocation(query.OccurrenceTimezone); err != nil {
				return ErrInvalidRequest
			}
		}
	}
	return nil
}

func filterTasks(tasks []ScheduledTask, query ListTasksQuery) []ScheduledTask {
	q := strings.ToLower(strings.TrimSpace(query.Q))
	out := make([]ScheduledTask, 0, len(tasks))
	for _, task := range tasks {
		if normalizeTenantID(task.TenantID) != normalizeTenantID(query.TenantID) {
			continue
		}
		if query.Status != "" && task.Status != query.Status {
			continue
		}
		if !matchesTaskWindow(task, query) {
			continue
		}
		if q != "" && !strings.Contains(taskSearchText(task), q) {
			continue
		}
		out = append(out, task)
	}
	return out
}

func matchesTaskWindow(task ScheduledTask, query ListTasksQuery) bool {
	from, to := query.From, query.To
	if from.IsZero() && to.IsZero() {
		return true
	}
	if query.ExpandOccurrences {
		return len(scheduleOccurrencesForWindow(task.Schedule, from, to, query.OccurrenceTimezone)) > 0
	}
	if task.NextRunAt.IsZero() {
		return false
	}
	next := task.NextRunAt.UTC()
	if !from.IsZero() && next.Before(from.UTC()) {
		return false
	}
	if !to.IsZero() && !next.Before(to.UTC()) {
		return false
	}
	return true
}

func scheduleOccurrencesForWindow(schedule Schedule, from time.Time, to time.Time, timezone string) []time.Time {
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return nil
	}
	location := time.UTC
	if timezone != "" {
		loaded, err := time.LoadLocation(timezone)
		if err != nil {
			return nil
		}
		location = loaded
	}

	occurrences := make([]time.Time, 0, 7)
	cursor := from.Add(-time.Nanosecond)
	for {
		next, err := NextOccurrence(schedule, cursor)
		if err != nil || next.IsZero() || !next.Before(to) {
			return occurrences
		}
		if !next.Before(from) {
			occurrences = append(occurrences, next.UTC())
		}
		local := next.In(location)
		nextDay := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
		cursor = nextDay.Add(-time.Nanosecond)
	}
}

func taskSearchText(task ScheduledTask) string {
	return strings.ToLower(strings.Join([]string{
		task.ScheduledTaskID,
		task.Title,
		task.Description,
		task.Target.ConversationID,
		task.Target.LeadAgentID,
		task.Target.Message,
		task.Target.AgentID,
		task.Target.TaskGoal,
		task.Target.Prompt,
		task.Target.ToolID,
		targetSummary(task.Target),
		string(task.Schedule.Type),
		string(task.Target.Mode),
	}, " "))
}

func sortTasksForQuery(tasks []ScheduledTask, taskSort TaskSort) {
	switch taskSort {
	case "", SortNextRunAsc:
		sortTasks(tasks)
	case SortNextRunDesc:
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].NextRunAt.Equal(tasks[j].NextRunAt) {
				return tasks[i].ScheduledTaskID < tasks[j].ScheduledTaskID
			}
			if tasks[i].NextRunAt.IsZero() {
				return false
			}
			if tasks[j].NextRunAt.IsZero() {
				return true
			}
			return tasks[i].NextRunAt.After(tasks[j].NextRunAt)
		})
	case SortCreatedAsc:
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
				return tasks[i].ScheduledTaskID < tasks[j].ScheduledTaskID
			}
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		})
	case SortCreatedDesc:
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
				return tasks[i].ScheduledTaskID < tasks[j].ScheduledTaskID
			}
			return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
		})
	case SortUpdatedAsc:
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].UpdatedAt.Equal(tasks[j].UpdatedAt) {
				return tasks[i].ScheduledTaskID < tasks[j].ScheduledTaskID
			}
			return tasks[i].UpdatedAt.Before(tasks[j].UpdatedAt)
		})
	case SortUpdatedDesc:
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].UpdatedAt.Equal(tasks[j].UpdatedAt) {
				return tasks[i].ScheduledTaskID < tasks[j].ScheduledTaskID
			}
			return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
		})
	}
}

func isTaskStatus(status TaskStatus) bool {
	switch status {
	case TaskStatusEnabled, TaskStatusDisabled, TaskStatusArchived:
		return true
	default:
		return false
	}
}

func isRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusQueued, RunStatusRunning, RunStatusCompleted, RunStatusFailed, RunStatusSkipped:
		return true
	default:
		return false
	}
}

func isTaskSort(sort TaskSort) bool {
	switch sort {
	case SortNextRunAsc, SortNextRunDesc, SortCreatedAsc, SortCreatedDesc, SortUpdatedAsc, SortUpdatedDesc:
		return true
	default:
		return false
	}
}

func targetSummary(target Target) string {
	switch target.Mode {
	case TargetModeDiscussionWorkflow:
		if target.ConversationID != "" {
			return "discussion " + target.ConversationID
		}
		return "new discussion workflow"
	case TargetModeAgentTask:
		if target.AgentID != "" {
			return "agent " + target.AgentID
		}
	case TargetModeToolCall:
		return "tool " + target.ToolID
	case TargetModeMemoryExperienceDistillation:
		return "experience distillation " + target.MemoryTopic + " to " + target.TargetScopeKind + " " + target.TargetScopeID
	case TargetModeMemoryWorldConsolidation:
		return "world consolidation " + target.MemoryTopic + " to " + target.TargetScopeKind + " " + target.TargetScopeID
	case TargetModeMemoryLifecycleSweep:
		return "memory lifecycle sweep " + target.TargetScopeKind + " " + target.TargetScopeID
	case TargetModeTodoAutomationScan:
		return "Todo Agent automation scan"
	case TargetModeResponsibilityEvaluation:
		if target.SourceScopeKind == ResponsibilityEvaluationPeoplePortfoliosScope {
			return "responsibility evaluation for People tenant " + target.SourceScopeID
		}
		return "responsibility evaluation for agent " + target.AgentID
	case TargetModeResponsibilityReconcile:
		return "Agent responsibility reconciliation"
	}
	return string(target.Mode)
}

func normalizeSchedule(schedule Schedule) Schedule {
	schedule.Type = ScheduleType(strings.TrimSpace(string(schedule.Type)))
	schedule.Cron = strings.Join(strings.Fields(schedule.Cron), " ")
	schedule.Timezone = strings.TrimSpace(schedule.Timezone)
	if !schedule.RunAt.IsZero() {
		schedule.RunAt = schedule.RunAt.UTC()
	}
	return schedule
}

func normalizeTarget(target Target) Target {
	target.Mode = TargetMode(strings.TrimSpace(string(target.Mode)))
	target.LeadAgentID = strings.TrimSpace(target.LeadAgentID)
	target.ConversationID = strings.TrimSpace(target.ConversationID)
	target.InitialMemberIDs = cleanStringList(target.InitialMemberIDs)
	target.Message = strings.TrimSpace(target.Message)
	target.AgentID = strings.TrimSpace(target.AgentID)
	target.ApprovalAssigneeID = strings.TrimSpace(target.ApprovalAssigneeID)
	for index := range target.ResultContract.Artifacts {
		artifact := &target.ResultContract.Artifacts[index]
		artifact.ID = strings.TrimSpace(artifact.ID)
		artifact.Kind = strings.TrimSpace(artifact.Kind)
		artifact.PathPattern = strings.TrimSpace(artifact.PathPattern)
	}
	target.TaskGoal = strings.TrimSpace(target.TaskGoal)
	target.Prompt = strings.TrimSpace(target.Prompt)
	target.ToolID = strings.TrimSpace(target.ToolID)
	if target.ToolArguments == nil {
		target.ToolArguments = map[string]any{}
	}
	target.MemoryTopic = strings.TrimSpace(target.MemoryTopic)
	target.SourceScopeKind = strings.TrimSpace(target.SourceScopeKind)
	target.SourceScopeID = strings.TrimSpace(target.SourceScopeID)
	target.TargetScopeKind = strings.TrimSpace(target.TargetScopeKind)
	target.TargetScopeID = strings.TrimSpace(target.TargetScopeID)
	if target.MemoryRecordLimit < 0 {
		target.MemoryRecordLimit = 0
	}
	return target
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func normalizePolicy(policy Policy) Policy {
	policy.Misfire = MisfirePolicy(strings.TrimSpace(string(policy.Misfire)))
	if policy.Misfire == "" {
		policy.Misfire = MisfireRunOnce
	}
	policy.Concurrency = ConcurrencyPolicy(strings.TrimSpace(string(policy.Concurrency)))
	if policy.Concurrency == "" {
		policy.Concurrency = ConcurrencyAllowParallel
	}
	policy.RetryDelay = strings.TrimSpace(policy.RetryDelay)
	policy.FailureMode = strings.TrimSpace(policy.FailureMode)
	if policy.MaxRetries < 0 {
		policy.MaxRetries = 0
	}
	return policy
}

func normalizeExecutionPolicy(policy ExecutionPolicy) ExecutionPolicy {
	policy.LLMProfileID = strings.TrimSpace(policy.LLMProfileID)
	policy.SourceModel = strings.TrimSpace(policy.SourceModel)
	policy.FallbackModels = cleanStringList(policy.FallbackModels)
	policy.Thinking = strings.TrimSpace(policy.Thinking)
	policy.AllowedToolIDs = cleanStringList(policy.AllowedToolIDs)
	return policy
}

func normalizeDeliveryPolicy(policy DeliveryPolicy) DeliveryPolicy {
	policy.Mode = strings.TrimSpace(policy.Mode)
	policy.Channel = strings.TrimSpace(policy.Channel)
	policy.RecipientID = strings.TrimSpace(policy.RecipientID)
	policy.AccountID = strings.TrimSpace(policy.AccountID)
	policy.ContentArtifactID = strings.TrimSpace(policy.ContentArtifactID)
	policy.Format = strings.ToLower(strings.TrimSpace(policy.Format))
	return policy
}

func validateExecutionPolicy(policy ExecutionPolicy) error {
	if policy.TimeoutSeconds < 0 || policy.TimeoutSeconds > 24*60*60 {
		return fmt.Errorf("%w: timeout_seconds must be between 0 and 86400", ErrInvalidRequest)
	}
	return nil
}

func validateDeliveryPolicy(policy DeliveryPolicy) error {
	if policy.Mode == "" || policy.Mode == "none" {
		return nil
	}
	if policy.Mode != "announce" {
		return fmt.Errorf("%w: unsupported delivery mode %q", ErrInvalidRequest, policy.Mode)
	}
	if policy.Channel == "" || policy.RecipientID == "" {
		return fmt.Errorf("%w: delivery channel and recipient_id are required", ErrInvalidRequest)
	}
	if policy.Format != "" && policy.Format != "text" && policy.Format != "markdown" {
		return fmt.Errorf("%w: unsupported delivery format %q", ErrInvalidRequest, policy.Format)
	}
	return nil
}

func validateDeliveryContentArtifact(policy DeliveryPolicy, target Target) error {
	if policy.ContentArtifactID == "" {
		return nil
	}
	if target.Mode != TargetModeAgentTask {
		return fmt.Errorf("%w: delivery content artifacts require an agent task target", ErrInvalidRequest)
	}
	for _, artifact := range target.ResultContract.Artifacts {
		if artifact.ID == policy.ContentArtifactID {
			return nil
		}
	}
	return fmt.Errorf("%w: delivery content artifact %q is not declared in the result contract", ErrInvalidRequest, policy.ContentArtifactID)
}

func validatePolicy(policy Policy) error {
	if policy.MaxRetries > 20 {
		return fmt.Errorf("%w: max_retries cannot exceed 20", ErrInvalidRequest)
	}
	if policy.RetryDelay != "" {
		delay, err := time.ParseDuration(policy.RetryDelay)
		if err != nil || delay < 0 || delay > 24*time.Hour {
			return fmt.Errorf("%w: retry_delay must be a duration between 0 and 24h", ErrInvalidRequest)
		}
	}
	if policy.FailureMode != "" && policy.FailureMode != "record_failure" {
		return fmt.Errorf("%w: unsupported failure_mode %q", ErrInvalidRequest, policy.FailureMode)
	}
	return nil
}

func validateTarget(target Target) error {
	if target.ApprovalAssigneeID != "" && target.Mode != TargetModeAgentTask && target.Mode != TargetModeResponsibilityEvaluation {
		return fmt.Errorf("%w: approval_assignee_id requires an agent task or responsibility evaluation target", ErrInvalidTarget)
	}
	switch target.Mode {
	case TargetModeDiscussionWorkflow:
		if target.Message == "" {
			return fmt.Errorf("%w: message is required for discussion workflow targets", ErrInvalidTarget)
		}
	case TargetModeAgentTask:
		if target.AgentID == "" {
			return fmt.Errorf("%w: agent_id is required for agent task targets", ErrInvalidTarget)
		}
		if target.TaskGoal == "" {
			return fmt.Errorf("%w: task_goal is required for agent task targets", ErrInvalidTarget)
		}
		if err := validateAgentResultContract(target.ResultContract); err != nil {
			return err
		}
	case TargetModeToolCall:
		if target.ToolID == "" {
			return fmt.Errorf("%w: tool_id is required for tool call targets", ErrInvalidTarget)
		}
	case TargetModeMemoryExperienceDistillation:
		if target.MemoryTopic == "" {
			return fmt.Errorf("%w: memory_topic is required for memory distillation targets", ErrInvalidTarget)
		}
		if (target.SourceScopeKind == "") != (target.SourceScopeID == "") {
			return fmt.Errorf("%w: source scope kind and id must be provided together for memory distillation targets", ErrInvalidTarget)
		}
		if target.SourceScopeKind != "" && !isMemoryExperienceSourceScope(target.SourceScopeKind) {
			return fmt.Errorf("%w: source scope must be mission, discussion, project, department, or organization", ErrInvalidTarget)
		}
		if target.TargetScopeKind == "" || target.TargetScopeID == "" {
			return fmt.Errorf("%w: target scope is required for memory distillation targets", ErrInvalidTarget)
		}
		if !isMemoryExperienceTargetScope(target.TargetScopeKind) {
			return fmt.Errorf("%w: target scope must be project, department, or organization", ErrInvalidTarget)
		}
	case TargetModeMemoryWorldConsolidation:
		if target.MemoryTopic == "" {
			return fmt.Errorf("%w: memory_topic is required for world consolidation targets", ErrInvalidTarget)
		}
		if (target.SourceScopeKind == "") != (target.SourceScopeID == "") {
			return fmt.Errorf("%w: source scope kind and id must be provided together for world consolidation targets", ErrInvalidTarget)
		}
		if target.SourceScopeKind != "" && !isMemoryWorldSourceScope(target.SourceScopeKind) {
			return fmt.Errorf("%w: source scope must be project, department, or organization", ErrInvalidTarget)
		}
		if target.TargetScopeKind == "" || target.TargetScopeID == "" {
			return fmt.Errorf("%w: target scope is required for world consolidation targets", ErrInvalidTarget)
		}
		if !isMemoryWorldTargetScope(target.TargetScopeKind) {
			return fmt.Errorf("%w: target scope must be organization", ErrInvalidTarget)
		}
	case TargetModeMemoryLifecycleSweep:
		if target.TargetScopeKind == "" || target.TargetScopeID == "" {
			return fmt.Errorf("%w: target scope is required for memory lifecycle sweep targets", ErrInvalidTarget)
		}
		if !isMemoryLifecycleTargetScope(target.TargetScopeKind) {
			return fmt.Errorf("%w: target scope must be agent, discussion, mission, project, department, or organization", ErrInvalidTarget)
		}
	case TargetModeResponsibilityEvaluation:
		if target.TaskGoal != "" || target.Prompt != "" {
			return fmt.Errorf("%w: responsibility evaluation targets cannot contain an Agent task goal or prompt", ErrInvalidTarget)
		}
		switch target.SourceScopeKind {
		case ResponsibilityEvaluationPeoplePortfolioScope:
			if target.AgentID == "" || target.SourceScopeID == "" {
				return fmt.Errorf("%w: agent_id and a People responsibility portfolio are required for responsibility evaluation targets", ErrInvalidTarget)
			}
		case ResponsibilityEvaluationPeoplePortfoliosScope:
			if target.AgentID != "" || target.ApprovalAssigneeID != "" || target.SourceScopeID == "" ||
				targetHasExecutionArguments(target) {
				return fmt.Errorf("%w: batch responsibility evaluation requires only a People tenant source", ErrInvalidTarget)
			}
		default:
			return fmt.Errorf("%w: a People responsibility source is required for responsibility evaluation targets", ErrInvalidTarget)
		}
	case TargetModeTodoAutomationScan, TargetModeResponsibilityReconcile:
		return nil
	case TargetModeOntologyStateRefresh:
		if ontologyStateRefreshTargetHasArguments(target) {
			return fmt.Errorf("%w: ontology state refresh targets cannot contain arguments", ErrInvalidTarget)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported target mode %q", ErrInvalidTarget, target.Mode)
	}
	return nil
}

func targetHasExecutionArguments(target Target) bool {
	return target.LeadAgentID != "" || target.ConversationID != "" || len(target.InitialMemberIDs) > 0 ||
		target.AllowMemberAdditions != nil || target.Message != "" || target.TaskGoal != "" || target.Prompt != "" ||
		target.ToolID != "" || len(target.ToolArguments) > 0 || target.MemoryTopic != "" ||
		target.TargetScopeKind != "" || target.TargetScopeID != "" || target.MemoryRecordLimit != 0 ||
		len(target.ResultContract.Artifacts) > 0
}

func isSystemManagedTargetMode(mode TargetMode) bool {
	switch mode {
	case TargetModeTodoAutomationScan, TargetModeResponsibilityEvaluation, TargetModeResponsibilityReconcile, TargetModeOntologyStateRefresh:
		return true
	default:
		return false
	}
}

func lockedSystemManagedTargetMode(mode TargetMode) bool {
	return mode == TargetModeResponsibilityEvaluation || mode == TargetModeResponsibilityReconcile || mode == TargetModeOntologyStateRefresh
}

func ontologyStateRefreshTargetHasArguments(target Target) bool {
	return target.LeadAgentID != "" || target.ConversationID != "" || len(target.InitialMemberIDs) > 0 ||
		target.AllowMemberAdditions != nil || target.Message != "" || target.AgentID != "" ||
		target.ApprovalAssigneeID != "" || target.TaskGoal != "" || target.Prompt != "" ||
		target.ToolID != "" || len(target.ToolArguments) > 0 || target.MemoryTopic != "" ||
		target.SourceScopeKind != "" || target.SourceScopeID != "" || target.TargetScopeKind != "" ||
		target.TargetScopeID != "" || target.MemoryRecordLimit != 0 || len(target.ResultContract.Artifacts) > 0
}

func validateAgentResultContract(contract AgentResultContract) error {
	if len(contract.Artifacts) > maxScheduledResultArtifacts {
		return fmt.Errorf("%w: result contract exceeds %d artifacts", ErrInvalidTarget, maxScheduledResultArtifacts)
	}
	seen := make(map[string]bool, len(contract.Artifacts))
	for _, artifact := range contract.Artifacts {
		if artifact.ID == "" || seen[artifact.ID] {
			return fmt.Errorf("%w: result artifact id is required and must be unique", ErrInvalidTarget)
		}
		seen[artifact.ID] = true
		switch artifact.Kind {
		case "code", "log", "report", "visual":
		default:
			return fmt.Errorf("%w: unsupported result artifact kind %q", ErrInvalidTarget, artifact.Kind)
		}
		if !isAbsoluteArtifactPathPattern(artifact.PathPattern) || containsParentPathSegment(artifact.PathPattern) {
			return fmt.Errorf("%w: result artifact path_pattern must be absolute without parent traversal", ErrInvalidTarget)
		}
		if _, err := filepath.Match(artifact.PathPattern, artifact.PathPattern); err != nil {
			return fmt.Errorf("%w: invalid result artifact path_pattern: %v", ErrInvalidTarget, err)
		}
		if artifact.MinBytes < 0 {
			return fmt.Errorf("%w: result artifact min_bytes must not be negative", ErrInvalidTarget)
		}
	}
	return nil
}

func isAbsoluteArtifactPathPattern(value string) bool {
	if filepath.IsAbs(value) {
		return true
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") {
		return true
	}
	return len(normalized) >= 3 &&
		((normalized[0] >= 'A' && normalized[0] <= 'Z') || (normalized[0] >= 'a' && normalized[0] <= 'z')) &&
		normalized[1] == ':' && normalized[2] == '/'
}

func containsParentPathSegment(value string) bool {
	normalized := strings.ReplaceAll(filepath.ToSlash(value), `\`, "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func isMemoryExperienceTargetScope(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "project", "department", "organization":
		return true
	default:
		return false
	}
}

func isMemoryLifecycleTargetScope(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "agent", "discussion", "mission", "project", "department", "organization":
		return true
	default:
		return false
	}
}

func isMemoryExperienceSourceScope(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "mission", "discussion", "project", "department", "organization":
		return true
	default:
		return false
	}
}

func isMemoryWorldTargetScope(kind string) bool {
	return strings.TrimSpace(kind) == "organization"
}

func isMemoryWorldSourceScope(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "project", "department", "organization":
		return true
	default:
		return false
	}
}
