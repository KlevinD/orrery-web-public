package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	toolstd "orrery_v2/internal/resource/tool/std"
)

type ToolExecutorOptions struct {
	Service    *Service
	Dispatcher RunDispatcher
}

type ToolExecutor struct {
	service    *Service
	dispatcher RunDispatcher
}

type ToolExecutionRequest struct {
	ToolID    string
	Viewer    ViewerRef
	Arguments map[string]any
}

type ToolExecutionResult struct {
	Output map[string]any
}

func NewToolExecutor(opts ToolExecutorOptions) *ToolExecutor {
	return &ToolExecutor{service: opts.Service, dispatcher: opts.Dispatcher}
}

func (e *ToolExecutor) Execute(ctx context.Context, req ToolExecutionRequest) (ToolExecutionResult, error) {
	if e == nil || e.service == nil {
		return ToolExecutionResult{}, ErrServiceDependency
	}
	switch strings.TrimSpace(req.ToolID) {
	case string(toolstd.ScheduledTaskCreate):
		var create CreateTaskRequest
		if err := decodeToolArguments(req.Arguments, &create); err != nil {
			return ToolExecutionResult{}, err
		}
		task, err := e.service.CreateTask(ctx, req.Viewer, create)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: taskOutput(task)}, nil
	case string(toolstd.ScheduledTaskUpdate):
		var update UpdateTaskRequest
		if err := decodeToolArguments(req.Arguments, &update); err != nil {
			return ToolExecutionResult{}, err
		}
		task, err := e.service.UpdateTask(ctx, req.Viewer, update)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: taskOutput(task)}, nil
	case string(toolstd.ScheduledTaskCancel):
		var cancel CancelTaskRequest
		if err := decodeToolArguments(req.Arguments, &cancel); err != nil {
			return ToolExecutionResult{}, err
		}
		task, err := e.service.CancelTask(ctx, req.Viewer, cancel)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: taskOutput(task)}, nil
	case string(toolstd.ScheduledTaskList):
		return e.listScheduled(ctx, req)
	case string(toolstd.ScheduledTaskRunNow):
		var runNow RunNowRequest
		if err := decodeToolArguments(req.Arguments, &runNow); err != nil {
			return ToolExecutionResult{}, err
		}
		run, err := e.runNow(ctx, req.Viewer, runNow)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: runOutput(run)}, nil
	default:
		return ToolExecutionResult{}, fmt.Errorf("%w: unsupported scheduler tool %q", ErrInvalidRequest, req.ToolID)
	}
}

func (e *ToolExecutor) listScheduled(ctx context.Context, req ToolExecutionRequest) (ToolExecutionResult, error) {
	var args struct {
		View string `json:"view"`
		ListTasksQuery
		ScheduledTaskID string    `json:"scheduled_task_id"`
		ScheduledRunID  string    `json:"scheduled_run_id"`
		Status          RunStatus `json:"status"`
		CreatedFrom     time.Time `json:"created_from"`
		CreatedBefore   time.Time `json:"created_before"`
		ArtifactID      string    `json:"artifact_id"`
	}
	if err := decodeToolArguments(req.Arguments, &args); err != nil {
		return ToolExecutionResult{}, err
	}
	switch strings.TrimSpace(args.View) {
	case "", "tasks":
		if args.ScheduledTaskID != "" || args.ScheduledRunID != "" || args.Status != "" || !args.CreatedFrom.IsZero() || !args.CreatedBefore.IsZero() || args.ArtifactID != "" {
			return ToolExecutionResult{}, fmt.Errorf("%w: historical filters require view=runs or view=artifact", ErrInvalidRequest)
		}
		args.ListTasksQuery.TenantID = req.Viewer.TenantID
		page, err := e.service.ListTasks(ctx, args.ListTasksQuery)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: listOutput(page)}, nil
	case "runs":
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		offset := args.Offset
		if offset < 0 {
			offset = 0
		}
		page, err := e.service.ListRuns(ctx, ListRunsQuery{
			TenantID: req.Viewer.TenantID, ScheduledTaskID: args.ScheduledTaskID,
			ScheduledRunID: args.ScheduledRunID, Status: args.Status,
			CreatedFrom: args.CreatedFrom, CreatedBefore: args.CreatedBefore,
			Limit: limit, Offset: offset,
		})
		if err != nil {
			return ToolExecutionResult{}, err
		}
		// Active blockers can predate the requested day by weeks. Include them
		// independently of the history window, using the same tenant check.
		active, err := e.service.ListRuns(ctx, ListRunsQuery{
			TenantID: req.Viewer.TenantID, ScheduledTaskID: args.ScheduledTaskID,
			ActiveOnly: true, Limit: 20,
		})
		if err != nil {
			return ToolExecutionResult{}, err
		}
		out := map[string]any{
			"view": "runs", "scheduled_task_id": strings.TrimSpace(args.ScheduledTaskID),
			"runs": historyRunOutputs(page.Runs), "total": page.Total,
			"limit": limit, "offset": offset, "has_more": offset+len(page.Runs) < page.Total,
			"active_runs": historyRunOutputs(active.Runs), "active_total": active.Total,
			"active_has_more": len(active.Runs) < active.Total,
		}
		if !args.CreatedFrom.IsZero() {
			out["created_from"] = args.CreatedFrom.Format(time.RFC3339Nano)
		}
		if !args.CreatedBefore.IsZero() {
			out["created_before"] = args.CreatedBefore.Format(time.RFC3339Nano)
		}
		return ToolExecutionResult{Output: out}, nil
	case "artifact":
		content, err := e.service.OpenRunArtifact(ctx, OpenRunArtifactQuery{
			TenantID: req.Viewer.TenantID, ScheduledTaskID: args.ScheduledTaskID,
			ScheduledRunID: args.ScheduledRunID, ArtifactID: args.ArtifactID,
		})
		if err != nil {
			return ToolExecutionResult{}, err
		}
		defer content.Reader.Close()
		mediaType, _, err := mime.ParseMediaType(content.ContentType)
		if err != nil || !(strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")) {
			return ToolExecutionResult{}, fmt.Errorf("%w: artifact is not readable text", ErrInvalidRequest)
		}
		const maxBytes = 32 * 1024
		raw, err := io.ReadAll(io.LimitReader(content.Reader, maxBytes+1))
		if err != nil {
			return ToolExecutionResult{}, err
		}
		truncated := len(raw) > maxBytes
		if truncated {
			raw = raw[:maxBytes]
		}
		return ToolExecutionResult{Output: map[string]any{
			"view": "artifact", "scheduled_task_id": strings.TrimSpace(args.ScheduledTaskID),
			"scheduled_run_id": strings.TrimSpace(args.ScheduledRunID), "artifact_id": args.ArtifactID,
			"content_type": content.ContentType, "size_bytes": content.SizeBytes,
			"content": strings.ToValidUTF8(string(raw), ""), "truncated": truncated,
		}}, nil
	default:
		return ToolExecutionResult{}, fmt.Errorf("%w: view must be tasks, runs, or artifact", ErrInvalidRequest)
	}
}

func historyRunOutputs(runs []ScheduledRun) []map[string]any {
	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		item := runOutput(run)
		item["title"] = run.ScheduledTaskTitle
		item["created_at"] = run.CreatedAt.UTC().Format(time.RFC3339Nano)
		if !run.StartedAt.IsZero() {
			item["started_at"] = run.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if !run.CompletedAt.IsZero() {
			item["completed_at"] = run.CompletedAt.UTC().Format(time.RFC3339Nano)
		}
		item["runtime_run_id"] = run.RuntimeRunID
		item["mission_id"] = run.MissionID
		item["task_id"] = run.TaskID
		item["responsibility_id"] = run.ResponsibilityID
		item["agent_id"] = run.AgentID
		item["delivery_status"] = run.DeliveryStatus
		item["delivery_error"] = run.DeliveryError
		item["external_message_id"] = run.ExternalMessageID
		item["trace_available"] = run.TraceAvailable
		item["trace_status"] = run.TraceStatus
		summary := []rune(run.Result.Summary)
		item["summary_truncated"] = len(summary) > 8000
		if len(summary) > 8000 {
			summary = summary[:8000]
		}
		item["result_summary"] = string(summary)
		artifacts := make([]map[string]any, 0, len(run.Result.Artifacts))
		for index, artifact := range run.Result.Artifacts {
			artifacts = append(artifacts, map[string]any{
				"artifact_id": PublicRunArtifactID(run.ScheduledRunID, index, artifact),
				"kind":        artifact.Kind, "file_name": artifact.FileName,
				"content_type": artifact.ContentType, "size_bytes": artifact.SizeBytes,
				"content_available": artifact.ContentAvailable,
			})
		}
		item["artifacts"] = artifacts
		out = append(out, item)
	}
	return out
}

func (e *ToolExecutor) runNow(ctx context.Context, viewer ViewerRef, req RunNowRequest) (ScheduledRun, error) {
	if e.service.repo == nil {
		return ScheduledRun{}, ErrServiceDependency
	}
	task, err := e.service.repo.GetTask(ctx, strings.TrimSpace(req.ScheduledTaskID))
	if err != nil {
		return ScheduledRun{}, err
	}
	run, err := e.service.PrepareManualRun(ctx, viewer, req)
	if err != nil {
		return ScheduledRun{}, err
	}
	if run.Status != RunStatusQueued {
		return run, nil
	}
	if e.dispatcher == nil {
		return ScheduledRun{}, ErrServiceDependency
	}
	return e.dispatcher.DispatchRun(ctx, task, run)
}

func decodeToolArguments(arguments map[string]any, out any) error {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("%w: encode tool arguments", ErrInvalidRequest)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: decode tool arguments", ErrInvalidRequest)
	}
	return nil
}

func taskOutput(task ScheduledTask) map[string]any {
	out := map[string]any{
		"scheduled_task_id": task.ScheduledTaskID,
		"status":            string(task.Status),
		"enabled":           task.Enabled,
	}
	if !task.NextRunAt.IsZero() {
		out["next_run_at"] = task.NextRunAt.UTC().Format(time.RFC3339)
	}
	return out
}

func listOutput(page ScheduledTaskPage) map[string]any {
	return map[string]any{
		"tasks":  page.Tasks,
		"total":  page.Total,
		"limit":  page.Limit,
		"offset": page.Offset,
	}
}

func runOutput(run ScheduledRun) map[string]any {
	out := map[string]any{
		"scheduled_task_id": run.ScheduledTaskID,
		"scheduled_run_id":  run.ScheduledRunID,
		"status":            string(run.Status),
	}
	if run.FailureReason != "" {
		out["failure_reason"] = run.FailureReason
	}
	if run.ConversationID != "" {
		out["discussion_id"] = run.ConversationID
	}
	if run.CommandID != "" {
		out["command_id"] = run.CommandID
	}
	return out
}
