package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/agent/outputpolicy"
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	platformrealtime "orrery_v2/internal/platform/realtime"
)

type productionTodoProgressProjectionRepository struct {
	repo        operations.MissionProjectionRepository
	base        *missioncontrol.DBProjectionRepository
	todos       *todoapp.Repository
	clock       clock.Clock
	realtime    projectExecutionRealtimeAppender
	discussions *discussionsession.Service
	audit       todoapp.TodoAutomationAuditPort

	reconcileMu     sync.Mutex
	reconcileCursor string
}

type projectExecutionRealtimeAppender interface {
	Append(context.Context, platformrealtime.AppendEvent) (platformrealtime.Envelope, error)
}

// ReconcileTodoExecutionProgress repairs projection gaps caused by a Mission
// projection being committed before its Todo execution owner link.
func (r *productionTodoProgressProjectionRepository) ReconcileTodoExecutionProgress(ctx context.Context, limit int) (int, error) {
	if r == nil || r.base == nil || r.todos == nil {
		return 0, missioncontrol.ErrServiceDependency
	}
	if limit <= 0 {
		limit = 100
	}
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	models, hasMore, err := r.base.ListMissionCatalog(ctx, r.reconcileCursor, limit)
	if err != nil {
		return 0, err
	}
	if hasMore && len(models) == 0 {
		return 0, fmt.Errorf("%w: Todo progress reconciliation received an empty continuation page", missioncontrol.ErrServiceDependency)
	}
	nextCursor := r.reconcileCursor
	applied := 0
	for _, model := range models {
		tenantID := strings.TrimSpace(model.TenantID)
		if tenantID == "" {
			tenantID = strings.TrimSpace(model.Mission.TenantID)
		}
		if tenantID == "" {
			tenantID = "global"
		}
		nextCursor = tenantID + "/" + model.Mission.MissionID.String()
		if strings.TrimSpace(model.Mission.OriginTodoID) == "" {
			continue
		}
		changed, projectErr := r.projectTodoProgress(ctx, model)
		if projectErr != nil {
			return applied, projectErr
		}
		if changed {
			applied++
		}
		if noticeErr := r.reconcileTodoConversationRuntimeNotices(ctx, model); noticeErr != nil {
			return applied, noticeErr
		}
	}
	if hasMore {
		r.reconcileCursor = nextCursor
	} else {
		r.reconcileCursor = ""
	}
	return applied, nil
}

func newProductionTodoProgressProjectionRepository(repo operations.MissionProjectionRepository, base *missioncontrol.DBProjectionRepository, todos *todoapp.Repository, projectionClock clock.Clock) *productionTodoProgressProjectionRepository {
	return &productionTodoProgressProjectionRepository{repo: repo, base: base, todos: todos, clock: projectionClock}
}

func (r *productionTodoProgressProjectionRepository) SetRealtimeStore(store *platformrealtime.Store) {
	if r != nil {
		r.realtime = store
	}
}

func (r *productionTodoProgressProjectionRepository) SetDiscussionService(service *discussionsession.Service) {
	if r != nil {
		r.discussions = service
	}
}

func (r *productionTodoProgressProjectionRepository) SetAuditStore(audit todoapp.TodoAutomationAuditPort) {
	if r != nil {
		r.audit = audit
	}
}

func registerProductionTodoProgressProjectionSweeper(rt *platform.Runtime, repository *productionTodoProgressProjectionRepository) error {
	return rt.RegisterSweeper("todo.execution_progress_projection", platform.SweeperFunc(func(ctx context.Context) error {
		_, err := repository.ReconcileTodoExecutionProgress(ctx, rt.Config.Sweeper.BatchSize)
		return err
	}))
}

func (r *productionTodoProgressProjectionRepository) Save(ctx context.Context, model missionprojection.ReadModel) error {
	if r == nil || r.repo == nil {
		return missioncontrol.ErrServiceDependency
	}
	if err := r.repo.Save(ctx, model); err != nil {
		return err
	}
	_, err := r.projectTodoProgress(ctx, model)
	if err == nil {
		_ = r.reconcileTodoConversationRuntimeNotices(ctx, model)
	}
	return err
}

func (r *productionTodoProgressProjectionRepository) SaveForTenant(ctx context.Context, tenantID string, model missionprojection.ReadModel) error {
	if r == nil || r.repo == nil {
		return missioncontrol.ErrServiceDependency
	}
	repo, ok := r.repo.(interface {
		SaveForTenant(context.Context, string, missionprojection.ReadModel) error
	})
	if !ok {
		return missioncontrol.ErrServiceDependency
	}
	if err := repo.SaveForTenant(ctx, tenantID, model); err != nil {
		return err
	}
	_, err := r.projectTodoProgress(ctx, model)
	if err == nil {
		_ = r.reconcileTodoConversationRuntimeNotices(ctx, model)
	}
	return err
}

func (r *productionTodoProgressProjectionRepository) GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (missionprojection.ReadModel, error) {
	if r == nil || r.repo == nil {
		return missionprojection.ReadModel{}, missioncontrol.ErrServiceDependency
	}
	return r.repo.GetMissionControl(ctx, missionID)
}

func (r *productionTodoProgressProjectionRepository) GetMissionControlForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) (missionprojection.ReadModel, error) {
	if r == nil || r.repo == nil {
		return missionprojection.ReadModel{}, missioncontrol.ErrServiceDependency
	}
	repo, ok := r.repo.(interface {
		GetMissionControlForTenant(context.Context, string, missioncore.MissionID) (missionprojection.ReadModel, error)
	})
	if !ok {
		return missionprojection.ReadModel{}, missioncontrol.ErrServiceDependency
	}
	return repo.GetMissionControlForTenant(ctx, tenantID, missionID)
}

func (r *productionTodoProgressProjectionRepository) ListMissionControls(ctx context.Context) ([]missionprojection.ReadModel, error) {
	if r == nil || r.repo == nil {
		return nil, missioncontrol.ErrServiceDependency
	}
	repo, ok := r.repo.(interface {
		ListMissionControls(context.Context) ([]missionprojection.ReadModel, error)
	})
	if !ok {
		return nil, missioncontrol.ErrServiceDependency
	}
	return repo.ListMissionControls(ctx)
}

func (r *productionTodoProgressProjectionRepository) ListMissionControlsForTenant(ctx context.Context, tenantID string) ([]missionprojection.ReadModel, error) {
	if r == nil || r.repo == nil {
		return nil, missioncontrol.ErrServiceDependency
	}
	repo, ok := r.repo.(interface {
		ListMissionControlsForTenant(context.Context, string) ([]missionprojection.ReadModel, error)
	})
	if !ok {
		return nil, missioncontrol.ErrServiceDependency
	}
	return repo.ListMissionControlsForTenant(ctx, tenantID)
}

func (r *productionTodoProgressProjectionRepository) Delete(ctx context.Context, missionID missioncore.MissionID) error {
	if r == nil || r.base == nil {
		return missioncontrol.ErrServiceDependency
	}
	return r.base.Delete(ctx, missionID)
}

func (r *productionTodoProgressProjectionRepository) DeleteForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) error {
	if r == nil || r.base == nil {
		return missioncontrol.ErrServiceDependency
	}
	return r.base.DeleteForTenant(ctx, tenantID, missionID)
}

func (r *productionTodoProgressProjectionRepository) projectTodoProgress(ctx context.Context, model missionprojection.ReadModel) (bool, error) {
	if r.todos == nil || model.Mission.OriginTodoID == "" {
		return false, nil
	}
	progress, changed, err := r.todos.ProjectTodoExecutionProgress(ctx, model, r.now())
	if errors.Is(err, todoapp.ErrNotFound) {
		// Mission creation precedes the durable Todo-Mission owner link. The
		// next projection update or the reconciler will materialize the summary.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if r.audit != nil {
		if err := r.recordTodoProgressAudit(context.WithoutCancel(ctx), operations.AuditEventDTO{
			AuditID:  todoExecutionProgressAuditID(progress),
			TenantID: progress.TenantID,
			ActorID:  "mission-projection", ViewerID: "mission-projection", EntityType: "todo_execution_progress", EntityID: progress.TodoID,
			Action: "todo.execution_progress.projected", Result: "succeeded", Severity: "info",
			CorrelationID: fmt.Sprintf("todo-progress:%s:%d", progress.MissionID, progress.MissionProjectionVersion),
			Metadata: map[string]string{
				"tenant_id": progress.TenantID, "todo_id": progress.TodoID, "mission_id": progress.MissionID,
				"execution_attempt": fmt.Sprint(progress.ExecutionAttempt), "projection_version": fmt.Sprint(progress.MissionProjectionVersion),
			},
			OccurredAt: r.now(),
		}); err != nil {
			return false, err
		}
		state, stateErr := r.todos.GetTodoAutomation(ctx, progress.TenantID, progress.TodoID)
		if stateErr != nil && !errors.Is(stateErr, todoapp.ErrNotFound) {
			return false, stateErr
		}
		if stateErr == nil && state.Status == todoapp.TodoAutomationStatusExecuted {
			if err := r.recordTodoProgressAudit(context.WithoutCancel(ctx), operations.AuditEventDTO{
				AuditID: todoExecutionCompletedAuditID(progress), TenantID: progress.TenantID,
				ActorID: "mission-projection", ViewerID: "mission-projection", EntityType: "todo_execution", EntityID: progress.TodoID,
				Action: "todo.execution.completed", Result: "succeeded", Severity: "info",
				CorrelationID: fmt.Sprintf("todo-execution:%s:%d", progress.MissionID, progress.ExecutionAttempt),
				Metadata: map[string]string{
					"tenant_id": progress.TenantID, "todo_id": progress.TodoID, "mission_id": progress.MissionID,
					"execution_attempt": fmt.Sprint(progress.ExecutionAttempt), "projection_version": fmt.Sprint(progress.MissionProjectionVersion),
				},
				OccurredAt: r.now(),
			}); err != nil {
				return false, err
			}
		}
	}
	if err := r.appendProjectExecutionSignal(ctx, model, progress); err != nil {
		return false, err
	}
	return changed, nil
}

func (r *productionTodoProgressProjectionRepository) recordTodoProgressAudit(ctx context.Context, event operations.AuditEventDTO) error {
	return r.audit.RecordAuditEvent(ctx, event)
}

func todoExecutionProgressAuditID(progress todoapp.TodoExecutionProgressProjection) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(progress.TenantID), strings.TrimSpace(progress.TodoID), strings.TrimSpace(progress.MissionID),
		fmt.Sprint(progress.ExecutionAttempt), fmt.Sprint(progress.MissionProjectionVersion),
	}, "\x00")))
	return fmt.Sprintf("todo-progress-%x", digest[:16])
}

func todoExecutionCompletedAuditID(progress todoapp.TodoExecutionProgressProjection) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(progress.TenantID), strings.TrimSpace(progress.TodoID), strings.TrimSpace(progress.MissionID),
		fmt.Sprint(progress.ExecutionAttempt), "completed",
	}, "\x00")))
	return fmt.Sprintf("todo-execution-completed-%x", digest[:16])
}

func (r *productionTodoProgressProjectionRepository) appendProjectExecutionSignal(ctx context.Context, model missionprojection.ReadModel, progress todoapp.TodoExecutionProgressProjection) error {
	projectID := strings.TrimSpace(model.Mission.OriginDevelopmentProjectID)
	if r == nil || r.realtime == nil || projectID == "" {
		return nil
	}
	tenantID := strings.TrimSpace(progress.TenantID)
	channel, err := platformrealtime.ProjectChannel(tenantID, projectID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(developmentProjectExecutionRealtimePayload{
		TodoID: progress.TodoID, MissionID: progress.MissionID, ProjectionVersion: progress.MissionProjectionVersion,
	})
	if err != nil {
		return err
	}
	_, err = r.realtime.Append(ctx, platformrealtime.AppendEvent{
		EventID: projectExecutionRealtimeEventID(tenantID, projectID, progress.TodoID, progress.MissionID, progress.MissionProjectionVersion),
		Type:    platformrealtime.EventProjectExecutionUpdated, Channel: channel,
		AggregateType: platformrealtime.AggregateProject, AggregateID: projectID, Payload: payload,
	})
	return err
}

func (r *productionTodoProgressProjectionRepository) reconcileTodoConversationRuntimeNotices(ctx context.Context, model missionprojection.ReadModel) error {
	if r == nil || r.discussions == nil || strings.TrimSpace(model.Mission.OriginTodoID) == "" {
		return nil
	}
	conversationID := strings.TrimSpace(model.Discussion.ConversationID)
	tenantID := strings.TrimSpace(model.TenantID)
	if tenantID == "" {
		tenantID = strings.TrimSpace(model.Mission.TenantID)
	}
	missionID := model.Mission.MissionID.String()
	if conversationID == "" || tenantID == "" || missionID == "" {
		return missioncontrol.ErrServiceDependency
	}
	for _, session := range model.RuntimeSessions {
		content, kind, terminal := todoRuntimeTerminalNotice(session)
		if !terminal {
			continue
		}
		viewer := discussionsession.ViewerRef{
			ViewerID: session.AgentID.String(), ViewerKind: discussionsession.ViewerKindAgent, DisplayName: session.AgentID.String(), TenantID: tenantID,
		}
		_, err := r.discussions.AppendNotice(ctx, viewer, discussionsession.AppendNoticeCommand{
			ConversationID: conversationID, MissionID: missionID, MissionTaskID: session.TaskID.String(), Content: content,
			Metadata: map[string]string{
				"kind": kind, "run_id": session.RunID.String(), "agent_id": session.AgentID.String(), "status": string(session.Status),
			},
			IdempotencyKey: "todo-runtime:" + tenantID + ":" + missionID + ":" + session.RunID.String() + ":" + string(session.Status),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func todoRuntimeTerminalNotice(session missionprojection.RuntimeSessionView) (string, string, bool) {
	status := string(session.Status)
	var headline string
	switch session.Status {
	case missionprojection.RuntimeSessionCompleted:
		headline = "Agent 已完成任务"
	case missionprojection.RuntimeSessionBlocked:
		headline = "Agent 执行受阻"
	case missionprojection.RuntimeSessionFailed:
		headline = "Agent 执行失败"
	case missionprojection.RuntimeSessionTimedOut:
		headline = "Agent 执行超时"
	case missionprojection.RuntimeSessionCancelled:
		headline = "Agent 执行已取消"
	default:
		return "", "", false
	}
	detail := strings.TrimSpace(session.LastMessage)
	if detail == "" {
		detail = strings.TrimSpace(session.ErrorSummary)
	}
	// IDs remain in the notice metadata for routing and audit. They are not
	// useful in the human-facing message and make the conversation read like a
	// worker log.
	content := headline
	if detail != "" {
		detail = outputpolicy.PublicText(detail, "任务已结束，详细结果已记录到 Orrery。")
		content += "。" + truncateConversationDigestText(detail, 600)
	}
	return content, "todo_execution_run_" + status, true
}

func projectExecutionRealtimeEventID(tenantID, projectID, todoID, missionID string, projectionVersion int) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(todoID) + "\x00" + strings.TrimSpace(missionID) + "\x00" + fmt.Sprintf("%d", projectionVersion)))
	return fmt.Sprintf("development-project-execution-%x", sum[:])
}

type developmentProjectExecutionRealtimePayload struct {
	TodoID            string `json:"todo_id"`
	MissionID         string `json:"mission_id"`
	ProjectionVersion int    `json:"projection_version"`
}

func (r *productionTodoProgressProjectionRepository) now() time.Time {
	if r != nil && r.clock != nil {
		return r.clock.Now().UTC()
	}
	return time.Now().UTC()
}

var _ productionMissionProjectionRepository = (*productionTodoProgressProjectionRepository)(nil)
