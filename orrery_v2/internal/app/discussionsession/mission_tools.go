package discussionsession

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionteam "orrery_v2/internal/mission/team"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type MissionCommandPort interface {
	CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error)
	UpdateMissionBrief(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.UpdateMissionBriefCommand) (missioncontrol.MissionDTO, error)
	CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error)
	AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error)
}

// MissionAgentRunCommandPort is optional so non-production Mission tooling can
// still plan tasks without a runtime queue. Production implementations use it
// to start coordinator lanes during the same tool application.
type MissionAgentRunCommandPort interface {
	StartAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.StartAgentRunCommand) (missioncontrol.CommandReceiptDTO, error)
}

type missionToolParentRunContextKey struct{}

// withMissionToolParentRun carries trusted runtime provenance outside model
// tool arguments. Mission Control still validates the referenced parent run.
func withMissionToolParentRun(ctx context.Context, runID string) context.Context {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, missionToolParentRunContextKey{}, runID)
}

func missionToolParentRunID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	runID, _ := ctx.Value(missionToolParentRunContextKey{}).(string)
	return strings.TrimSpace(runID)
}

// MissionConversationOriginCommandPort lets a production adapter derive a
// Mission's runtime provenance from the persisted source conversation. Tool
// arguments never carry that provenance.
type MissionConversationOriginCommandPort interface {
	CreateMissionFromDiscussion(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error)
}

type ResultCommandPort interface {
	SubmitAgentResult(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error)
}

const (
	MissionCloseReady     = "ready"
	MissionClosePending   = "pending"
	MissionCloseCompleted = "completed"
)

type MissionCloseReadiness struct {
	Status string
}

// MissionCloseReadinessPort prevents a coordinator from submitting another
// Agent's task result while the delegated run is still in flight.
type MissionCloseReadinessPort interface {
	CheckMissionCloseReadiness(ctx context.Context, viewer missioncontrol.ViewerRef, missionID, taskID string) (MissionCloseReadiness, error)
}

type AgentDirectoryPort interface {
	agentcore.AgentProfileRepository
}

type ToolCallResult struct {
	Name      string
	Arguments map[string]string
	Result    map[string]string
}

type MissionToolApplier struct {
	missions MissionCommandPort
	results  ResultCommandPort
}

func NewMissionToolApplier(missions MissionCommandPort) *MissionToolApplier {
	applier := &MissionToolApplier{missions: missions}
	if results, ok := missions.(ResultCommandPort); ok {
		applier.results = results
	}
	return applier
}

func (a *MissionToolApplier) Apply(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, calls []ToolCallResult, existingMissions ...LinkedMissionDTO) ([]LinkedMissionDTO, error) {
	if a == nil || a.missions == nil {
		return nil, ErrServiceDependency
	}
	var links []LinkedMissionDTO
	currentMissionID := firstExistingMissionID(existingMissions)
	var currentTaskID string
	for i := range calls {
		call := calls[i]
		switch normalizeToolName(call.Name) {
		case string(toolstd.MissionPrepare):
			mission, task, err := a.applyPrepareMission(ctx, viewer, conversationID, call, currentMissionID, existingMissions...)
			if err != nil {
				return nil, err
			}
			setToolResultValue(&call, "mission_id", mission.MissionID)
			setToolResultValue(&call, "operation", missionOperationResult(currentMissionID, mission))
			if task.TaskID != "" {
				setToolResultValue(&call, "task_id", task.TaskID)
				currentTaskID = firstNonBlank(task.TaskID, currentTaskID)
			}
			calls[i] = call
			links = append(links, mission)
			currentMissionID = firstNonBlank(mission.MissionID, currentMissionID)
		case string(toolstd.MissionBriefCreate):
			if currentMissionID != "" {
				mission, err := a.applyUpdateMissionBrief(ctx, viewer, conversationID, currentMissionID, call)
				if err != nil {
					return nil, err
				}
				setToolResultValue(&call, "mission_id", mission.MissionID)
				setToolResultValue(&call, "operation", "updated")
				calls[i] = call
				links = append(links, mission)
				continue
			}
			if isEmptyMissionBriefCreateTool(call) {
				continue
			}
			mission, err := a.applyCreateMission(ctx, viewer, conversationID, call)
			if err != nil {
				return nil, err
			}
			setToolResultValue(&call, "mission_id", mission.MissionID)
			setToolResultValue(&call, "operation", "created")
			calls[i] = call
			links = append(links, mission)
			currentMissionID = firstNonBlank(mission.MissionID, currentMissionID)
		case string(toolstd.MissionTaskCreateRoot):
			call = withDefaultMissionID(call, currentMissionID)
			task, err := a.applyCreateTask(ctx, viewer, conversationID, call)
			if err != nil {
				return nil, err
			}
			setToolResultValue(&call, "task_id", task.TaskID)
			setToolResultValue(&call, "mission_id", firstToolValue(call, "mission_id", "mission"))
			calls[i] = call
			currentMissionID = firstNonBlank(firstToolValue(call, "mission_id", "mission"), currentMissionID)
			currentTaskID = firstNonBlank(task.TaskID, currentTaskID)
		case string(toolstd.MissionTaskDelegate):
			call = withDefaultMissionID(call, currentMissionID)
			call = withDefaultTaskID(call, currentTaskID)
			if err := a.applyAssignTask(ctx, viewer, conversationID, call); err != nil {
				return nil, err
			}
			currentMissionID = firstNonBlank(firstToolValue(call, "mission_id", "mission"), currentMissionID)
		case string(toolstd.MissionCoordinatorPlanParallel):
			call = withDefaultMissionID(call, currentMissionID)
			call = withDefaultTaskID(call, currentTaskID)
			lanes, err := a.applyCoordinatorPlan(ctx, viewer, conversationID, call)
			if err != nil {
				return nil, err
			}
			currentMissionID = firstNonBlank(firstToolValue(call, "mission_id", "mission"), currentMissionID)
			if len(lanes) > 0 {
				currentTaskID = firstNonBlank(lanes[len(lanes)-1].Task.TaskID, currentTaskID)
			}
			taskIDs := make([]string, 0, len(lanes))
			runIDs := make([]string, 0, len(lanes))
			for laneIndex, lane := range lanes {
				setToolResultValue(&call, "task_id_"+fmt.Sprint(laneIndex+1), lane.Task.TaskID)
				if strings.TrimSpace(lane.Task.TaskID) != "" {
					taskIDs = append(taskIDs, strings.TrimSpace(lane.Task.TaskID))
				}
				if runID := strings.TrimSpace(lane.Run.CommandID); runID != "" {
					setToolResultValue(&call, "run_id_"+fmt.Sprint(laneIndex+1), runID)
					runIDs = append(runIDs, runID)
				}
			}
			setToolResultValue(&call, "task_ids", strings.Join(taskIDs, ","))
			setToolResultValue(&call, "run_ids", strings.Join(runIDs, ","))
			if len(runIDs) == len(lanes) {
				setToolResultValue(&call, "status", "accepted")
			} else {
				setToolResultValue(&call, "status", "planned")
			}
			calls[i] = call
		case string(toolstd.DiscussionClose):
			call = withDefaultMissionID(call, currentMissionID)
			call = withDefaultTaskID(call, currentTaskID)
			status, err := a.applyConversationClose(ctx, viewer, conversationID, call)
			if err != nil {
				return nil, err
			}
			setToolResultValue(&call, "status", status)
			setToolResultValue(&call, "mission_id", firstToolValue(call, "mission_id", "mission"))
			setToolResultValue(&call, "task_id", firstToolValue(call, "mission_task_id", "task_id", "root_task_id"))
			calls[i] = call
		}
	}
	return links, nil
}

func missionOperationResult(existingMissionID string, mission LinkedMissionDTO) string {
	if strings.TrimSpace(existingMissionID) != "" && strings.TrimSpace(mission.ReplacesMissionID) == "" {
		return "updated"
	}
	return "created"
}

func firstExistingMissionID(missions []LinkedMissionDTO) string {
	for _, mission := range missions {
		if id := strings.TrimSpace(mission.MissionID); id != "" {
			return id
		}
	}
	return ""
}

func (a *MissionToolApplier) applyCreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, call ToolCallResult) (LinkedMissionDTO, error) {
	title := firstToolValue(call, "title", "mission_title", "name")
	finalGoal := firstToolValue(call, "final_goal", "goal", "user_request", "summary")
	if title == "" {
		title = finalGoal
	}
	if finalGoal == "" {
		finalGoal = title
	}
	cmd := missioncontrol.CreateMissionCommand{
		RequestID:               conversationID + ":" + normalizeToolName(call.Name),
		IdempotencyKey:          firstToolValue(call, "idempotency_key", "tool_call_id", "call_id", "request_id", "title", "final_goal", "goal", "summary"),
		Title:                   title,
		FinalGoal:               finalGoal,
		BusinessDirection:       firstToolValue(call, "business_direction", "direction"),
		RequesterID:             viewer.ViewerID,
		InitialHumanAgentID:     firstToolValue(call, "initial_human_agent_id", "human_agent_id", "user_id"),
		InitialHumanDisplayName: firstToolValue(call, "initial_human_display_name", "human_display_name", "user_display_name"),
	}
	var mission missioncontrol.MissionDTO
	var err error
	if originAware, ok := a.missions.(MissionConversationOriginCommandPort); ok {
		mission, err = originAware.CreateMissionFromDiscussion(ctx, viewer, conversationID, cmd)
	} else {
		mission, err = a.missions.CreateMission(ctx, viewer, cmd)
	}
	if err != nil {
		return LinkedMissionDTO{}, err
	}
	return LinkedMissionDTO{
		MissionID: mission.MissionID,
		Title:     firstNonBlank(mission.Title, title),
		Status:    mission.Status,
		CreatedAt: firstTime(mission.CreatedAt, time.Now().UTC()),
	}, nil
}

func (a *MissionToolApplier) applyUpdateMissionBrief(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, missionID string, call ToolCallResult) (LinkedMissionDTO, error) {
	title := firstToolValue(call, "title", "mission_title", "name")
	finalGoal := firstToolValue(call, "final_goal", "goal", "user_request", "summary")
	if title == "" && finalGoal == "" {
		return LinkedMissionDTO{MissionID: strings.TrimSpace(missionID)}, nil
	}
	mission, err := a.missions.UpdateMissionBrief(ctx, viewer, missioncontrol.UpdateMissionBriefCommand{
		RequestID:      conversationID + ":" + normalizeToolName(call.Name) + ":update",
		IdempotencyKey: firstToolValue(call, "idempotency_key", "tool_call_id", "call_id", "request_id", "title", "final_goal", "goal", "summary"),
		MissionID:      missioncore.MissionID(strings.TrimSpace(missionID)),
		Title:          title,
		FinalGoal:      finalGoal,
	})
	if err != nil {
		return LinkedMissionDTO{}, err
	}
	return LinkedMissionDTO{
		MissionID: mission.MissionID,
		Title:     firstNonBlank(mission.Title, title),
		Status:    mission.Status,
		CreatedAt: firstTime(mission.CreatedAt, time.Now().UTC()),
	}, nil
}

func isEmptyMissionBriefCreateTool(call ToolCallResult) bool {
	if normalizeToolName(call.Name) != string(toolstd.MissionBriefCreate) {
		return false
	}
	return firstToolValue(call, "title", "mission_title", "name", "final_goal", "goal", "user_request", "summary") == ""
}

func (a *MissionToolApplier) applyPrepareMission(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, call ToolCallResult, currentMissionID string, _ ...LinkedMissionDTO) (LinkedMissionDTO, missioncontrol.TaskCardDTO, error) {
	var mission LinkedMissionDTO
	var err error
	if strings.TrimSpace(currentMissionID) != "" {
		mission, err = a.applyUpdateMissionBrief(ctx, viewer, conversationID, currentMissionID, call)
		if errors.Is(err, missioncontrol.ErrProjectionNotFound) {
			mission, err = a.applyCreateMission(ctx, viewer, conversationID, call)
			if err == nil {
				mission.ReplacesMissionID = strings.TrimSpace(currentMissionID)
			}
		}
		if err != nil {
			return LinkedMissionDTO{}, missioncontrol.TaskCardDTO{}, err
		}
		if mission.MissionID == "" {
			mission.MissionID = strings.TrimSpace(currentMissionID)
		}
	} else {
		mission, err = a.applyCreateMission(ctx, viewer, conversationID, call)
		if err != nil {
			return LinkedMissionDTO{}, missioncontrol.TaskCardDTO{}, err
		}
	}
	missionID := firstNonBlank(mission.MissionID, currentMissionID)
	if missionID == "" {
		return mission, missioncontrol.TaskCardDTO{}, nil
	}
	taskCall := prepareRootTaskCall(call, missionID)
	task, err := a.applyCreateTask(ctx, viewer, conversationID, taskCall)
	if err != nil {
		return LinkedMissionDTO{}, missioncontrol.TaskCardDTO{}, err
	}
	return mission, task, nil
}

func prepareRootTaskCall(call ToolCallResult, missionID string) ToolCallResult {
	next := call
	next.Arguments = cloneStringMap(call.Arguments)
	if next.Arguments == nil {
		next.Arguments = map[string]string{}
	}
	next.Arguments["mission_id"] = missionID
	if strings.TrimSpace(next.Arguments["task_id"]) == "" && strings.TrimSpace(next.Arguments["root_task_id"]) == "" {
		next.Arguments["task_id"] = rootTaskIDForMission(missionID)
	}
	if strings.TrimSpace(firstNonBlank(next.Arguments["goal"], next.Arguments["task_goal"], next.Arguments["summary"])) == "" {
		next.Arguments["goal"] = firstToolValue(call, "root_goal", "final_goal", "title", "mission_title", "name")
	}
	next.Arguments["idempotency_key"] = rootTaskIdempotencyKey(next, missionID)
	return next
}

func rootTaskIdempotencyKey(call ToolCallResult, missionID string) string {
	deliverables := expectedDeliverablesFromTool(call)
	deliverableTitles := make([]string, 0, len(deliverables))
	for _, deliverable := range deliverables {
		deliverableTitles = append(deliverableTitles, strings.TrimSpace(deliverable.Title))
	}
	payload, _ := json.Marshal(struct {
		MissionID            string   `json:"mission_id"`
		TaskID               string   `json:"task_id"`
		AssigneeID           string   `json:"assignee_id"`
		Goal                 string   `json:"goal"`
		WorkerPersonalGoal   string   `json:"worker_personal_goal"`
		Scope                string   `json:"scope"`
		Priority             string   `json:"priority"`
		ExpectedDeliverables []string `json:"expected_deliverables"`
		AcceptanceCriteria   []string `json:"acceptance_criteria"`
		RequiredEvidence     []string `json:"required_evidence"`
	}{
		MissionID:            strings.TrimSpace(missionID),
		TaskID:               firstToolValue(call, "task_id", "root_task_id"),
		AssigneeID:           firstToolValue(call, "assignee_id", "agent_id", "worker_id"),
		Goal:                 firstToolValue(call, "goal", "title", "task_goal", "summary"),
		WorkerPersonalGoal:   firstToolValue(call, "worker_personal_goal", "personal_goal", "instructions"),
		Scope:                firstToolValue(call, "scope", "boundary", "task_boundary"),
		Priority:             firstToolValue(call, "priority"),
		ExpectedDeliverables: deliverableTitles,
		AcceptanceCriteria:   stringListFromTool(call, "acceptance_criteria"),
		RequiredEvidence:     stringListFromTool(call, "required_evidence", "evidence_requirements"),
	})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("root:%s:%x", strings.TrimSpace(missionID), digest[:16])
}

func rootTaskIDForMission(missionID string) string {
	missionID = strings.TrimSpace(missionID)
	if missionID == "" {
		return ""
	}
	return "root-" + missionID
}

func (a *MissionToolApplier) applyCreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, call ToolCallResult) (missioncontrol.TaskCardDTO, error) {
	assigneeID := firstToolValue(call, "assignee_id", "agent_id", "worker_id")
	if missionteam.IsWorkerInternalQARoleIdentifier(assigneeID) {
		return missioncontrol.TaskCardDTO{}, fmt.Errorf("%w: assignee_id %s is worker-internal QA and must be attached to a worker, not assigned as an independent task", ErrInvalidRequest, assigneeID)
	}
	goal := firstToolValue(call, "goal", "title", "task_goal", "summary")
	acceptanceCriteria := stringListFromTool(call, "acceptance_criteria")
	if len(acceptanceCriteria) == 0 && goal != "" {
		acceptanceCriteria = []string{"The task goal is complete and its deliverable is verifiable: " + goal}
	}
	task, err := a.missions.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		RequestID:            conversationID + ":" + normalizeToolName(call.Name),
		IdempotencyKey:       firstToolValue(call, "idempotency_key", "tool_call_id", "call_id", "request_id", "goal"),
		MissionID:            missioncore.MissionID(firstToolValue(call, "mission_id", "mission")),
		TaskID:               missioncore.TaskID(firstToolValue(call, "task_id", "root_task_id")),
		AssigneeID:           assigneeID,
		Goal:                 goal,
		WorkerPersonalGoal:   firstToolValue(call, "worker_personal_goal", "personal_goal", "instructions"),
		Scope:                firstToolValue(call, "scope", "boundary", "task_boundary"),
		Priority:             firstToolValue(call, "priority"),
		ExpectedDeliverables: expectedDeliverablesFromTool(call),
		AcceptanceCriteria:   acceptanceCriteria,
		EvidenceRequirements: evidenceRequirementsFromTool(call),
	})
	return task, err
}

func (a *MissionToolApplier) applyAssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, call ToolCallResult) error {
	assigneeID := firstToolValue(call, "assignee_id", "agent_id", "worker_id")
	if missionteam.IsWorkerInternalQARoleIdentifier(assigneeID) {
		return fmt.Errorf("%w: assignee_id %s is worker-internal QA and must be attached to a worker, not assigned as an independent task", ErrInvalidRequest, assigneeID)
	}
	_, err := a.missions.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
		RequestID:      conversationID + ":" + normalizeToolName(call.Name),
		IdempotencyKey: firstToolValue(call, "idempotency_key", "tool_call_id", "call_id", "request_id", "task_id"),
		MissionID:      missioncore.MissionID(firstToolValue(call, "mission_id", "mission")),
		TaskID:         missioncore.TaskID(firstToolValue(call, "task_id", "root_task_id")),
		AssigneeID:     assigneeID,
		Reason:         firstToolValue(call, "reason", "rationale", "assignment_reason"),
	})
	return err
}

type coordinatorPlannedLane struct {
	Task missioncontrol.TaskCardDTO
	Run  missioncontrol.CommandReceiptDTO
}

func (a *MissionToolApplier) applyCoordinatorPlan(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, call ToolCallResult) ([]coordinatorPlannedLane, error) {
	missionID := firstToolValue(call, "mission_id", "mission")
	lanes, err := coordinatorLanesFromTool(call)
	if err != nil {
		return nil, err
	}
	for i := range lanes {
		lanes[i] = lanes[i].withFallbacks(call)
		assigneeID := strings.TrimSpace(lanes[i].assigneeID())
		if assigneeID == "" {
			return nil, fmt.Errorf("%w: coordinator worker lane %d assignee_id is required", ErrInvalidRequest, i+1)
		}
		if missionteam.IsWorkerInternalQARoleIdentifier(assigneeID) {
			return nil, fmt.Errorf("%w: coordinator worker lane %d assignee_id %s is worker-internal QA and must be attached to a worker, not planned as an independent lane", ErrInvalidRequest, i+1, assigneeID)
		}
	}
	planned := make([]coordinatorPlannedLane, 0, len(lanes))
	for i, lane := range lanes {
		assigneeID := strings.TrimSpace(lane.assigneeID())
		task, err := a.missions.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
			RequestID:            conversationID + ":" + normalizeToolName(call.Name) + ":" + fmt.Sprint(i+1),
			IdempotencyKey:       lane.idempotencyKey(call, i),
			MissionID:            missioncore.MissionID(missionID),
			AssigneeID:           assigneeID,
			Goal:                 lane.goal(),
			WorkerPersonalGoal:   lane.workerPersonalGoal(),
			Scope:                lane.scope(call),
			Priority:             lane.priority(),
			ExpectedDeliverables: lane.expectedDeliverables(),
			AcceptanceCriteria:   lane.acceptanceCriteria(),
			EvidenceRequirements: lane.evidenceRequirements(),
		})
		if err != nil {
			return planned, err
		}
		planned = append(planned, coordinatorPlannedLane{Task: task})
		if assigneeID == "" || strings.TrimSpace(task.TaskID) == "" {
			continue
		}
		if _, err := a.missions.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
			RequestID:      conversationID + ":" + normalizeToolName(call.Name) + ":assign:" + fmt.Sprint(i+1),
			IdempotencyKey: lane.idempotencyKey(call, i) + ":assign",
			MissionID:      missioncore.MissionID(missionID),
			TaskID:         missioncore.TaskID(task.TaskID),
			AssigneeID:     assigneeID,
			Reason:         firstNonBlank(lane.Reason, "Coordinator parallel lane: "+lane.role()),
		}); err != nil {
			return planned, err
		}
	}
	runner, ok := a.missions.(MissionAgentRunCommandPort)
	if !ok {
		return planned, nil
	}
	for i, lane := range lanes {
		assigneeID := strings.TrimSpace(lane.assigneeID())
		taskID := strings.TrimSpace(planned[i].Task.TaskID)
		receipt, err := runner.StartAgentRun(ctx, viewer, missioncontrol.StartAgentRunCommand{
			RequestID:      conversationID + ":" + normalizeToolName(call.Name) + ":run:" + fmt.Sprint(i+1),
			IdempotencyKey: lane.idempotencyKey(call, i) + ":run",
			MissionID:      missioncore.MissionID(missionID),
			TaskID:         missioncore.TaskID(taskID),
			AgentID:        assigneeID,
			RuntimeKind:    lane.runtimeKind(call),
			Prompt:         lane.workerPersonalGoal(),
			QueueID:        missionAgentQueueID(missionID, assigneeID),
			ParentRunID:    missionToolParentRunID(ctx),
		})
		if err != nil {
			return planned, fmt.Errorf("start coordinator worker lane %d: %w", i+1, err)
		}
		planned[i].Run = receipt
	}
	return planned, nil
}

func missionAgentQueueID(missionID, agentID string) string {
	return "mission:" + strings.TrimSpace(missionID) + ":agent:" + strings.TrimSpace(agentID)
}

func (a *MissionToolApplier) applyConversationClose(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, call ToolCallResult) (string, error) {
	if a.results == nil {
		return MissionCloseCompleted, nil
	}
	missionID := firstToolValue(call, "mission_id", "mission")
	taskID := firstToolValue(call, "mission_task_id", "task_id", "root_task_id")
	if missionID == "" || taskID == "" {
		return MissionCloseCompleted, nil
	}
	if readinessPort, ok := a.missions.(MissionCloseReadinessPort); ok {
		readiness, err := readinessPort.CheckMissionCloseReadiness(ctx, viewer, missionID, taskID)
		if err != nil {
			return "", err
		}
		switch strings.TrimSpace(strings.ToLower(readiness.Status)) {
		case MissionClosePending:
			return MissionClosePending, nil
		case MissionCloseCompleted:
			return MissionCloseCompleted, nil
		case MissionCloseReady:
		default:
			return "", fmt.Errorf("%w: unsupported Mission close readiness %q", ErrInvalidRequest, readiness.Status)
		}
	}
	note := firstToolValue(call, "note", "summary", "final_answer", "answer", "content")
	if note == "" {
		note = "Discussion answer closed."
	}
	_, err := a.results.SubmitAgentResult(ctx, viewer, missioncontrol.SubmitResultCommand{
		RequestID:      conversationID + ":" + normalizeToolName(call.Name),
		IdempotencyKey: firstNonBlank(firstToolValue(call, "idempotency_key", "tool_call_id", "call_id", "request_id"), conversationID+":"+normalizeToolName(call.Name)),
		MissionID:      missioncore.MissionID(missionID),
		TaskID:         missioncore.TaskID(taskID),
		Result: agentcore.AgentResult{
			ID:      agentcore.AgentResultID(firstNonBlank(firstToolValue(call, "result_id"), "result-"+conversationID+"-"+normalizeToolName(call.Name))),
			RunID:   agentcore.AgentRunID(firstToolValue(call, "run_id")),
			Summary: agentcore.Summary{Text: note},
			Deliverables: []agentcore.Deliverable{{
				ID:    agentcore.DeliverableID(firstNonBlank(firstToolValue(call, "deliverable_id"), "deliverable-"+conversationID+"-"+normalizeToolName(call.Name))),
				Title: note,
			}},
		},
	})
	if err != nil {
		return "", err
	}
	return MissionCloseCompleted, nil
}

type coordinatorLane struct {
	Role               string   `json:"role"`
	AgentID            string   `json:"agent_id"`
	AssigneeID         string   `json:"assignee_id"`
	WorkerID           string   `json:"worker_id"`
	GoalText           string   `json:"goal"`
	TaskGoal           string   `json:"task_goal"`
	Summary            string   `json:"summary"`
	ScopeText          string   `json:"scope"`
	Ownership          string   `json:"ownership"`
	Dependencies       []string `json:"dependencies"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	RequiredEvidence   []string `json:"required_evidence"`
	ConflictStrategy   string   `json:"conflict_strategy"`
	PriorityText       string   `json:"priority"`
	WorkerPersonalText string   `json:"worker_personal_goal"`
	Instructions       string   `json:"instructions"`
	Reason             string   `json:"reason"`
	IdempotencyKey     string   `json:"idempotency_key"`
	RuntimeKind        string   `json:"runtime_kind"`
}

func coordinatorLanesFromTool(call ToolCallResult) ([]coordinatorLane, error) {
	raw := firstToolValue(call, "worker_lanes_json", "worker_lanes", "lanes_json", "lanes", "workers")
	if strings.TrimSpace(raw) != "" {
		var lanes []coordinatorLane
		if err := json.Unmarshal([]byte(raw), &lanes); err == nil && len(lanes) > 0 {
			return lanes, nil
		}
		var lane coordinatorLane
		if err := json.Unmarshal([]byte(raw), &lane); err == nil && !lane.isZero() {
			return []coordinatorLane{lane}, nil
		}
		return nil, fmt.Errorf("%w: worker_lanes_json must be a JSON object or array", ErrInvalidRequest)
	}
	return []coordinatorLane{{
		Role:               firstNonBlank(firstToolValue(call, "role"), "Coordinator"),
		AgentID:            firstToolValue(call, "agent_id", "assignee_id", "worker_id"),
		GoalText:           firstToolValue(call, "goal", "summary"),
		ScopeText:          firstToolValue(call, "scope"),
		WorkerPersonalText: firstToolValue(call, "worker_personal_goal", "instructions"),
		PriorityText:       firstToolValue(call, "priority"),
		IdempotencyKey:     firstToolValue(call, "idempotency_key", "tool_call_id", "call_id", "request_id"),
	}}, nil
}

func (l coordinatorLane) withFallbacks(call ToolCallResult) coordinatorLane {
	if l.Role == "" {
		l.Role = firstNonBlank(firstToolValue(call, "role"), "Coordinator")
	}
	if l.WorkerPersonalText == "" && l.Instructions == "" {
		l.WorkerPersonalText = firstToolValue(call, "worker_personal_goal", "instructions")
	}
	if l.PriorityText == "" {
		l.PriorityText = firstToolValue(call, "priority")
	}
	return l
}

func (l coordinatorLane) isZero() bool {
	return strings.TrimSpace(strings.Join([]string{l.Role, l.AgentID, l.AssigneeID, l.WorkerID, l.GoalText, l.TaskGoal, l.Summary, l.ScopeText}, "")) == ""
}

func (l coordinatorLane) assigneeID() string {
	return firstNonBlank(l.AssigneeID, l.AgentID, l.WorkerID)
}

func (l coordinatorLane) role() string {
	return firstNonBlank(l.Role, "Coordinator")
}

func (l coordinatorLane) goal() string {
	goal := firstNonBlank(l.GoalText, l.TaskGoal, l.Summary, l.ScopeText)
	if goal == "" {
		return l.role() + " parallel Mission lane"
	}
	return l.role() + ": " + goal
}

func (l coordinatorLane) workerPersonalGoal() string {
	return firstNonBlank(l.WorkerPersonalText, l.Instructions, l.goal())
}

func (l coordinatorLane) scope(call ToolCallResult) string {
	parts := []string{firstNonBlank(l.ScopeText, firstToolValue(call, "goal", "summary"))}
	if parentTaskID := firstToolValue(call, "mission_task_id", "task_id", "root_task_id"); parentTaskID != "" {
		parts = append(parts, "Parent Mission Task: "+parentTaskID)
	}
	if l.Ownership != "" {
		parts = append(parts, "Ownership: "+strings.TrimSpace(l.Ownership))
	}
	if len(nonEmptyStrings(l.Dependencies)) > 0 {
		parts = append(parts, "Dependencies: "+strings.Join(nonEmptyStrings(l.Dependencies), "; "))
	}
	if l.ConflictStrategy != "" {
		parts = append(parts, "Conflict strategy: "+strings.TrimSpace(l.ConflictStrategy))
	}
	if join := firstToolValue(call, "join_condition"); join != "" {
		parts = append(parts, "Join condition: "+join)
	}
	return strings.Join(nonEmptyStrings(parts), "\n\n")
}

func (l coordinatorLane) priority() string {
	return strings.TrimSpace(l.PriorityText)
}

func (l coordinatorLane) runtimeKind(call ToolCallResult) string {
	return firstNonBlank(l.RuntimeKind, firstToolValue(call, "runtime_kind"), "pi")
}

func (l coordinatorLane) idempotencyKey(call ToolCallResult, index int) string {
	if l.IdempotencyKey != "" {
		return strings.TrimSpace(l.IdempotencyKey)
	}
	base := firstToolValue(call, "idempotency_key", "tool_call_id", "call_id", "request_id")
	if base == "" {
		base = firstNonBlank(l.goal(), normalizeToolName(call.Name))
	}
	return base + ":lane:" + fmt.Sprint(index+1)
}

func normalizeToolName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, ":", "_")
	name = strings.ReplaceAll(name, "-", "_")
	return name
}

func firstToolValue(call ToolCallResult, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(call.Arguments[key]); value != "" {
			return value
		}
		if value := strings.TrimSpace(call.Result[key]); value != "" {
			return value
		}
	}
	return ""
}

func setToolResultValue(call *ToolCallResult, key string, value string) {
	if call == nil {
		return
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	if call.Result == nil {
		call.Result = map[string]string{}
	}
	call.Result[key] = value
}

func withDefaultMissionID(call ToolCallResult, missionID string) ToolCallResult {
	missionID = strings.TrimSpace(missionID)
	if missionID == "" || firstToolValue(call, "mission_id", "mission") != "" {
		return call
	}
	call.Arguments = cloneStringMap(call.Arguments)
	if call.Arguments == nil {
		call.Arguments = map[string]string{}
	}
	call.Arguments["mission_id"] = missionID
	return call
}

func withDefaultTaskID(call ToolCallResult, taskID string) ToolCallResult {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || firstToolValue(call, "task_id", "root_task_id") != "" {
		return call
	}
	call.Arguments = cloneStringMap(call.Arguments)
	if call.Arguments == nil {
		call.Arguments = map[string]string{}
	}
	call.Arguments["task_id"] = taskID
	return call
}

func expectedDeliverablesFromTool(call ToolCallResult) []missioncontrol.ExpectedDeliverableCommand {
	parts := stringListFromTool(call, "expected_deliverables", "deliverables")
	out := make([]missioncontrol.ExpectedDeliverableCommand, 0, len(parts))
	for _, part := range parts {
		title := strings.TrimSpace(part)
		if title == "" {
			continue
		}
		out = append(out, missioncontrol.ExpectedDeliverableCommand{Title: title})
	}
	return out
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
