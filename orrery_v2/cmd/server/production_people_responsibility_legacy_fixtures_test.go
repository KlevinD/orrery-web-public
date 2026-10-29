package main

// Historical schedule builders are test fixtures only. Production startup uses
// the background responsibility runtime and never creates these schedules.
import (
	"context"
	"encoding/json"
	"fmt"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"sort"
	"strings"
)

type peopleResponsibilityBatchScheduler interface {
	CreateTask(context.Context, schedulerapp.ViewerRef, schedulerapp.CreateTaskRequest) (schedulerapp.ScheduledTask, error)
	GetTask(context.Context, string) (schedulerapp.ScheduledTask, error)
	UpdateTask(context.Context, schedulerapp.ViewerRef, schedulerapp.UpdateTaskRequest) (schedulerapp.ScheduledTask, error)
	CancelTask(context.Context, schedulerapp.ViewerRef, schedulerapp.CancelTaskRequest) (schedulerapp.ScheduledTask, error)
	ArchiveSystemManagedTask(context.Context, schedulerapp.ViewerRef, schedulerapp.CancelTaskRequest) (schedulerapp.ScheduledTask, error)
}

type peopleResponsibilityBatchTaskStore interface {
	FindTaskByIdempotency(context.Context, string, string, string) (schedulerapp.ScheduledTask, bool, error)
	ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error)
}

type productionResponsibilityBatchSchedule struct {
	scheduler      peopleResponsibilityBatchScheduler
	tasks          peopleResponsibilityBatchTaskStore
	portfolios     *responsibility.Repository
	orreryTenantID string
	peopleTenantID string
	cron           string
	timezone       string
}

func (p productionResponsibilityBatchSchedule) EnsureAndMigrate(ctx context.Context) (schedulerapp.ScheduledTask, error) {
	if p.scheduler == nil || p.tasks == nil || p.portfolios == nil {
		return schedulerapp.ScheduledTask{}, responsibility.ErrInvalidContract
	}
	viewer := p.viewer()
	request := p.createRequest(false)
	task, found, err := p.tasks.FindTaskByIdempotency(ctx, viewer.TenantID, viewer.ViewerID, request.IdempotencyKey)
	if err != nil {
		return schedulerapp.ScheduledTask{}, err
	}
	if !found {
		task, err = p.scheduler.CreateTask(ctx, viewer, request)
		if err != nil {
			return schedulerapp.ScheduledTask{}, err
		}
	} else if task.Target.Mode != schedulerapp.TargetModeResponsibilityEvaluation ||
		task.Target.SourceScopeKind != schedulerapp.ResponsibilityEvaluationPeoplePortfoliosScope ||
		strings.TrimSpace(task.Target.SourceScopeID) != strings.TrimSpace(p.peopleTenantID) ||
		task.CreatedBy != viewer.ViewerID || task.CreatorProfileID != viewer.ProfileID || task.CreatedByKind != schedulerapp.ViewerKindSystem ||
		strings.TrimSpace(task.IdempotencyKey) != request.IdempotencyKey {
		return schedulerapp.ScheduledTask{}, fmt.Errorf("%w: responsibility batch schedule ownership mismatch", schedulerapp.ErrInvalidRequest)
	}
	if err := p.archiveOtherManagedBatchTasks(ctx, task); err != nil {
		return schedulerapp.ScheduledTask{}, err
	}
	if _, err := p.archiveLegacyTasks(ctx, viewer); err != nil {
		return schedulerapp.ScheduledTask{}, err
	}
	if task.Status == schedulerapp.TaskStatusArchived {
		return task, nil
	}
	enabledRequest := p.createRequest(true)
	if productionResponsibilityBatchTaskMatches(task, enabledRequest) {
		return task, nil
	}
	return p.scheduler.UpdateTask(ctx, viewer, schedulerapp.UpdateTaskRequest{
		ScheduledTaskID: task.ScheduledTaskID, Title: &enabledRequest.Title, Description: &enabledRequest.Description,
		Schedule: &enabledRequest.Schedule, Target: &enabledRequest.Target, Policy: &enabledRequest.Policy,
		Execution: &enabledRequest.Execution, Delivery: &enabledRequest.Delivery, Enabled: enabledRequest.Enabled,
	})
}

func (p productionResponsibilityBatchSchedule) archiveOtherManagedBatchTasks(ctx context.Context, desired schedulerapp.ScheduledTask) error {
	tasks, err := p.tasks.ListTasks(ctx, true)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.ScheduledTaskID == desired.ScheduledTaskID || strings.TrimSpace(task.TenantID) != strings.TrimSpace(p.orreryTenantID) ||
			!productionResponsibilityBatchTaskIdentity(task) || task.Status == schedulerapp.TaskStatusArchived {
			continue
		}
		if _, err := p.scheduler.ArchiveSystemManagedTask(ctx, managedTaskViewer(task), schedulerapp.CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID}); err != nil {
			return err
		}
	}
	return nil
}

func (p productionResponsibilityBatchSchedule) DisableAndArchiveLegacy(ctx context.Context) error {
	if p.scheduler == nil || p.tasks == nil {
		return responsibility.ErrInvalidContract
	}
	return p.disableManagedSchedules(ctx)
}

func (p productionResponsibilityBatchSchedule) disableManagedSchedules(ctx context.Context) error {
	tasks, err := p.tasks.ListTasks(ctx, true)
	if err != nil {
		return err
	}
	portfolioByTaskID := make(map[string]responsibility.PeopleResponsibilityPortfolio)
	if p.portfolios != nil {
		portfolios, err := p.portfolios.ListPeopleResponsibilityPortfolios(ctx, "")
		if err != nil {
			return err
		}
		for _, portfolio := range portfolios {
			taskID := strings.TrimSpace(portfolio.PulseScheduledTaskID)
			if taskID != "" {
				portfolioByTaskID[taskID] = portfolio
			}
		}
	}
	orreryTenantID := strings.TrimSpace(p.orreryTenantID)
	for _, task := range tasks {
		if (orreryTenantID != "" && strings.TrimSpace(task.TenantID) != orreryTenantID) || task.Status == schedulerapp.TaskStatusArchived {
			continue
		}
		if productionResponsibilityBatchTaskIdentity(task) {
			if !task.Enabled && task.Status == schedulerapp.TaskStatusDisabled {
				continue
			}
			disabled := false
			if _, err := p.scheduler.UpdateTask(ctx, managedTaskViewer(task), schedulerapp.UpdateTaskRequest{
				ScheduledTaskID: task.ScheduledTaskID,
				Enabled:         &disabled,
			}); err != nil {
				return err
			}
			continue
		}
		portfolio, linked := portfolioByTaskID[task.ScheduledTaskID]
		if legacyResponsibilityPulseIdentity(task) || (linked && legacyResponsibilityPulseMatches(task, portfolio)) {
			if _, err := p.scheduler.ArchiveSystemManagedTask(ctx, managedTaskViewer(task), schedulerapp.CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID}); err != nil {
				return err
			}
		}
	}
	return nil
}

func productionResponsibilityBatchTaskMatches(task schedulerapp.ScheduledTask, request schedulerapp.CreateTaskRequest) bool {
	return task.Status == schedulerapp.TaskStatusEnabled && task.Enabled && task.Title == request.Title && task.Description == request.Description &&
		productionSchedulerConfigurationEqual(task.Schedule, request.Schedule) && productionSchedulerConfigurationEqual(task.Target, request.Target) &&
		productionSchedulerConfigurationEqual(task.Policy, request.Policy) && productionSchedulerConfigurationEqual(task.Execution, request.Execution) &&
		productionSchedulerConfigurationEqual(task.Delivery, request.Delivery)
}

func productionSchedulerConfigurationEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func (p productionResponsibilityBatchSchedule) archiveLegacyTasks(ctx context.Context, viewer schedulerapp.ViewerRef) (int, error) {
	portfolios, err := p.portfolios.ListPeopleResponsibilityPortfolios(ctx, strings.TrimSpace(p.orreryTenantID))
	if err != nil {
		return 0, err
	}
	portfolioByBinding := make(map[string]responsibility.PeopleResponsibilityPortfolio, len(portfolios))
	for _, portfolio := range portfolios {
		portfolioByBinding[portfolio.BindingID] = portfolio
	}
	tasks, err := p.tasks.ListTasks(ctx, true)
	if err != nil {
		return 0, err
	}
	taskByID := make(map[string]schedulerapp.ScheduledTask, len(tasks))
	for _, task := range tasks {
		if strings.TrimSpace(task.TenantID) == strings.TrimSpace(p.orreryTenantID) {
			taskByID[task.ScheduledTaskID] = task
		}
	}
	legacyByID := make(map[string]schedulerapp.ScheduledTask)
	for _, portfolio := range portfolioByBinding {
		if strings.TrimSpace(portfolio.PulseScheduledTaskID) == "" {
			continue
		}
		task, ok := taskByID[portfolio.PulseScheduledTaskID]
		if !ok || !legacyResponsibilityPulseMatches(task, portfolio) {
			return 0, fmt.Errorf("%w: legacy responsibility schedule %q is not safely attributable", schedulerapp.ErrInvalidRequest, portfolio.PulseScheduledTaskID)
		}
		legacyByID[task.ScheduledTaskID] = task
	}
	for _, task := range tasks {
		if strings.TrimSpace(task.TenantID) != strings.TrimSpace(p.orreryTenantID) || !legacyResponsibilityPulseIdentity(task) {
			continue
		}
		portfolio, ok := portfolioByBinding[task.Target.SourceScopeID]
		if !ok {
			continue
		}
		if !legacyResponsibilityPulseMatches(task, portfolio) {
			return 0, fmt.Errorf("%w: legacy responsibility schedule %q has inconsistent portfolio links", schedulerapp.ErrInvalidRequest, task.ScheduledTaskID)
		}
		legacyByID[task.ScheduledTaskID] = task
	}
	legacyTasks := make([]schedulerapp.ScheduledTask, 0, len(legacyByID))
	for _, task := range legacyByID {
		legacyTasks = append(legacyTasks, task)
	}
	sort.Slice(legacyTasks, func(i, j int) bool { return legacyTasks[i].ScheduledTaskID < legacyTasks[j].ScheduledTaskID })
	archived := 0
	for _, task := range legacyTasks {
		if task.Status == schedulerapp.TaskStatusArchived && !task.Enabled {
			continue
		}
		if _, err := p.scheduler.ArchiveSystemManagedTask(ctx, viewer, schedulerapp.CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID}); err != nil {
			return archived, err
		}
		archived++
	}
	return archived, nil
}

func (p productionResponsibilityBatchSchedule) createRequest(enabled bool) schedulerapp.CreateTaskRequest {
	return schedulerapp.CreateTaskRequest{
		Title:       "Responsibility evaluation: all active People profiles",
		Description: "Evaluate every active People responsibility portfolio discovered at run time.",
		Schedule:    schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: strings.TrimSpace(p.cron), Timezone: strings.TrimSpace(p.timezone)},
		Target: schedulerapp.Target{
			Mode:             schedulerapp.TargetModeResponsibilityEvaluation,
			SourceScopeKind:  schedulerapp.ResponsibilityEvaluationPeoplePortfoliosScope,
			SourceScopeID:    strings.TrimSpace(p.peopleTenantID),
			InitialMemberIDs: []string{},
			ToolArguments:    map[string]any{},
		},
		Policy: schedulerapp.Policy{
			Misfire: schedulerapp.MisfireRunOnce, Concurrency: schedulerapp.ConcurrencySkipIfRunning,
			MaxRetries: 1, RetryDelay: "5m", FailureMode: "record_failure",
		},
		Execution: schedulerapp.ExecutionPolicy{FallbackModels: []string{}, AllowedToolIDs: []string{}},
		Enabled:   &enabled,
		IdempotencyKey: "people-responsibility-evaluation-batch:" + strings.TrimSpace(p.orreryTenantID) + ":" +
			strings.TrimSpace(p.peopleTenantID) + ":" + peopleResponsibilityBatchVersion,
	}
}

func (p productionResponsibilityBatchSchedule) viewer() schedulerapp.ViewerRef {
	return schedulerapp.ViewerRef{
		TenantID:   strings.TrimSpace(p.orreryTenantID),
		ViewerID:   "system:people-responsibility-batch:" + strings.TrimSpace(p.peopleTenantID),
		ViewerKind: schedulerapp.ViewerKindSystem, DisplayName: "People responsibility batch scheduler",
		ProfileID: "system:people-responsibility-batch",
	}
}

func peopleResponsibilityPulseIdempotencyKey(portfolio responsibility.PeopleResponsibilityPortfolio) string {
	return "people-responsibility-pulse:" + strings.TrimSpace(portfolio.BindingID) + ":" +
		strings.TrimSpace(portfolio.AgentProfileID) + ":" + peopleResponsibilityPulseVersion
}

func legacyResponsibilityPulseMatches(task schedulerapp.ScheduledTask, portfolio responsibility.PeopleResponsibilityPortfolio) bool {
	if strings.TrimSpace(task.TenantID) != strings.TrimSpace(portfolio.TenantID) ||
		strings.TrimSpace(task.Target.AgentID) != strings.TrimSpace(portfolio.AgentProfileID) ||
		strings.TrimSpace(task.ScheduledTaskID) != strings.TrimSpace(portfolio.PulseScheduledTaskID) {
		return false
	}
	if legacyResponsibilityPulseIdentity(task) {
		return strings.TrimSpace(task.Target.SourceScopeID) == strings.TrimSpace(portfolio.BindingID)
	}
	bindingID := strings.TrimSpace(portfolio.BindingID)
	agentID := strings.TrimSpace(portfolio.AgentProfileID)
	return task.Target.Mode == schedulerapp.TargetModeAgentTask && task.Target.SourceScopeKind == "" &&
		strings.TrimSpace(task.Target.SourceScopeID) == "" && strings.TrimSpace(task.IdempotencyKey) == "" &&
		task.CreatedByKind == schedulerapp.ViewerKindSystem && strings.TrimSpace(task.CreatedBy) == "system:people-responsibility:"+bindingID+":"+agentID &&
		strings.TrimSpace(task.CreatorProfileID) == agentID && strings.HasPrefix(strings.TrimSpace(task.Title), "Responsibility pulse:")
}
