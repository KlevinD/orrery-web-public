package main

import (
	"context"
	"io"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	missioncore "orrery_v2/internal/mission/core"
	missiondiscussion "orrery_v2/internal/mission/discussion"
	wstransport "orrery_v2/internal/transport/websocket"
)

type demoDiscussionMissionMessenger struct {
	api *demoAPI
}

func (m demoDiscussionMissionMessenger) PostDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, mission discussionsession.LinkedMissionDTO, content string, idempotencyKey string) error {
	if m.api == nil {
		return missioncontrol.ErrServiceDependency
	}
	view, err := m.api.GetMissionControlView(ctx, viewer, missioncore.MissionID(strings.TrimSpace(mission.MissionID)))
	if err != nil {
		return err
	}
	if strings.TrimSpace(view.Discussion.DiscussionGroupID) == "" {
		return missioncontrol.ErrProjectionNotFound
	}
	_, err = m.api.PostDiscussionMessage(ctx, viewer, missioncontrol.PostDiscussionMessageCommand{
		RequestID:         firstNonBlank(conversationID, idempotencyKey),
		IdempotencyKey:    firstNonBlank(idempotencyKey, conversationID),
		MissionID:         missioncore.MissionID(strings.TrimSpace(mission.MissionID)),
		DiscussionGroupID: missiondiscussion.DiscussionGroupID(view.Discussion.DiscussionGroupID),
		Content:           strings.TrimSpace(content),
		MessageKind:       "request",
		Visibility:        "mission",
		RequiredResponse:  true,
	})
	return err
}

func (d *demoAPI) ListMissions(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ListMissionsQuery) (missioncontrol.MissionListDTO, error) {
	if err := ctx.Err(); err != nil {
		return missioncontrol.MissionListDTO{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	views := make([]missioncontrol.MissionControlViewDTO, 0, len(d.missions))
	for _, view := range d.missions {
		if query.Status != "" && view.Mission.Status != query.Status {
			continue
		}
		haystack := strings.ToLower(view.Mission.Title + " " + view.Mission.FinalGoal)
		if query.Search != "" && !strings.Contains(haystack, strings.ToLower(query.Search)) {
			continue
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Mission.CreatedAt.After(views[j].Mission.CreatedAt) })
	total := len(views)
	offset := clampOffset(query.Offset, total)
	limit := query.Limit
	if limit <= 0 || offset+limit > total {
		limit = total - offset
	}
	out := missioncontrol.MissionListDTO{Page: missioncontrol.PageDTO{Limit: query.Limit, Offset: query.Offset, Total: total, Returned: limit, HasMore: offset+limit < total}}
	for _, view := range views[offset : offset+limit] {
		out.Missions = append(out.Missions, view.Mission)
		out.Summaries = append(out.Summaries, missioncontrol.MissionSummaryDTO{
			MissionID: view.Mission.MissionID, Title: view.Mission.Title, Status: view.Mission.Status, Phase: view.Mission.Phase, RiskLevel: view.Mission.RiskLevel,
			TeamSize: view.Summary.TeamSize, ActiveTaskCount: view.Summary.ActiveTaskCount, PendingApprovalCount: view.Summary.PendingApprovalCount, BlockerCount: view.Summary.BlockerCount, DeliveryRate: view.Summary.DeliveryRate, UpdatedAt: view.UpdatedAt,
		})
	}
	return out, nil
}

func (d *demoAPI) GetMissionControlView(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) (missioncontrol.MissionControlViewDTO, error) {
	if err := ctx.Err(); err != nil {
		return missioncontrol.MissionControlViewDTO{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	view, ok := d.missions[missionID.String()]
	if !ok {
		return missioncontrol.MissionControlViewDTO{}, missioncontrol.ErrProjectionNotFound
	}
	return view, nil
}

func (d *demoAPI) GetMissionOpsView(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) (missioncontrol.MissionOpsViewDTO, error) {
	view, err := d.GetMissionControlView(ctx, viewer, missionID)
	if err != nil {
		return missioncontrol.MissionOpsViewDTO{}, err
	}
	return missioncontrol.MissionOpsViewDTO{
		MissionID:      view.Mission.MissionID,
		Title:          view.Mission.Title,
		Status:         view.Mission.Status,
		Phase:          view.Mission.Phase,
		AutonomyPolicy: missioncontrol.AutonomyPolicyDTO{Mode: "manual", MaxRiskWithoutApproval: "low", RequiredApprovals: []string{"tool_access", "external_side_effect", "high_risk"}},
		UpdatedAt:      view.UpdatedAt,
		Version:        view.Version,
	}, nil
}

func (d *demoAPI) Snapshot(ctx context.Context, missionID string, req wstransport.SubscribeRequest) (missioncontrol.MissionControlViewDTO, error) {
	return d.GetMissionControlView(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncore.MissionID(missionID))
}

func (d *demoAPI) Patches(ctx context.Context, missionID string, req wstransport.SubscribeRequest, afterVersion int) ([]missioncontrol.MissionControlPatchDTO, error) {
	return nil, ctx.Err()
}

func (d *demoAPI) CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UTC()
	id, err := operations.NewPrefixedIDGenerator().NewID("mission")
	if err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	view := missioncontrol.MissionControlViewDTO{Mission: missioncontrol.MissionDTO{MissionID: id, Title: firstNonBlank(cmd.Title, cmd.FinalGoal), FinalGoal: cmd.FinalGoal, Status: "active", Phase: "intake", RiskLevel: "normal", CreatedAt: now}, Summary: missioncontrol.MissionControlSummaryDTO{UpdatedAt: now}, Discussion: missioncontrol.DiscussionDTO{DiscussionGroupID: "DISC-" + id}, UpdatedAt: now, Version: 1}
	d.missions[id] = view
	if err := d.persistLocked(ctx); err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	return view.Mission, nil
}

func (d *demoAPI) UpdateMissionBrief(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.UpdateMissionBriefCommand) (missioncontrol.MissionDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	view, ok := d.missions[cmd.MissionID.String()]
	if !ok {
		return missioncontrol.MissionDTO{}, missioncontrol.ErrProjectionNotFound
	}
	if title := strings.TrimSpace(cmd.Title); title != "" {
		view.Mission.Title = title
	}
	if finalGoal := strings.TrimSpace(cmd.FinalGoal); finalGoal != "" {
		view.Mission.FinalGoal = finalGoal
	}
	now := time.Now().UTC()
	view.Mission.PhaseUpdatedAt = now
	view.Summary.UpdatedAt = now
	view.UpdatedAt = now
	view.Version++
	d.missions[cmd.MissionID.String()] = view
	if err := d.persistLocked(ctx); err != nil {
		return missioncontrol.MissionDTO{}, err
	}
	return view.Mission, nil
}

func (d *demoAPI) DeleteMission(ctx context.Context, viewer missioncontrol.ViewerRef, missionID missioncore.MissionID) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.missions[missionID.String()]; !ok {
		return missioncontrol.ErrProjectionNotFound
	}
	delete(d.missions, missionID.String())
	d.audit = filterAuditByMission(d.audit, missionID.String())
	d.evaluations.Items = filterEvaluationsByMission(d.evaluations.Items, missionID.String())
	d.evaluations.Page.Total = len(d.evaluations.Items)
	d.evaluations.Page.Returned = len(d.evaluations.Items)
	return d.persistLocked(ctx)
}

func (d *demoAPI) CancelMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelMissionCommand) (missioncontrol.CommandReceiptDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	view, ok := d.missions[cmd.MissionID.String()]
	if !ok {
		return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrProjectionNotFound
	}
	if view.Mission.Status == "completed" {
		return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrConflict
	}
	if view.Mission.Status == "cancelled" {
		return missioncontrol.CommandReceiptDTO{
			CommandID: cmd.IdempotencyKey, Status: "accepted", MissionID: cmd.MissionID.String(), Version: view.Version, AcceptedAt: view.UpdatedAt,
		}, nil
	}
	now := time.Now().UTC()
	view.Mission.Status = "cancelled"
	view.Mission.Phase = "closed"
	view.Mission.PhaseUpdatedAt = now
	for i := range view.TaskBoard.Tasks {
		switch view.TaskBoard.Tasks[i].Status {
		case "approved", "rejected", "cancelled":
			continue
		}
		view.TaskBoard.Tasks[i].Status = "cancelled"
		view.TaskBoard.Tasks[i].UpdatedAt = now
	}
	view.UpdatedAt = now
	view.Version++
	d.missions[cmd.MissionID.String()] = view
	if err := d.persistLocked(ctx); err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	return missioncontrol.CommandReceiptDTO{
		CommandID: cmd.IdempotencyKey, Status: "accepted", MissionID: cmd.MissionID.String(), Version: view.Version, AcceptedAt: now,
	}, nil
}

func (d *demoAPI) CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	view, ok := d.missions[cmd.MissionID.String()]
	if !ok {
		return missioncontrol.TaskCardDTO{}, missioncontrol.ErrProjectionNotFound
	}
	now := time.Now().UTC()
	taskID, err := operations.NewPrefixedIDGenerator().NewID("task")
	if err != nil {
		return missioncontrol.TaskCardDTO{}, err
	}
	task := missioncontrol.TaskCardDTO{TaskID: taskID, Title: cmd.Goal, Assignee: cmd.AssigneeID, AssigneeDisplayName: cmd.AssigneeID, AssigneeType: "agent", Goal: cmd.Goal, WorkerPersonalGoalSummary: cmd.WorkerPersonalGoal, Status: "created", Priority: "medium", ExpectedDeliverables: []missioncontrol.ExpectedDeliverableDTO{{ID: "D-" + taskID, Title: cmd.Goal}}, UpdatedAt: now}
	view.TaskBoard.Tasks = append(view.TaskBoard.Tasks, task)
	view.Summary.ActiveTaskCount++
	view.UpdatedAt = now
	view.Version++
	d.missions[cmd.MissionID.String()] = view
	if err := d.persistLocked(ctx); err != nil {
		return missioncontrol.TaskCardDTO{}, err
	}
	return task, nil
}

func (d *demoAPI) AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	now := time.Now().UTC()
	return missioncontrol.CommandReceiptDTO{CommandID: firstNonBlank(cmd.IdempotencyKey, cmd.RequestID), Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), AcceptedAt: now}, nil
}

func (d *demoAPI) StartAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.StartAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrServiceDependency
}

func (d *demoAPI) CancelAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CancelAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrServiceDependency
}

func (d *demoAPI) RetryAgentRun(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.RetryAgentRunCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrServiceDependency
}

func (d *demoAPI) SubmitAgentResult(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error) {
	now := time.Now().UTC()
	return missioncontrol.CommandReceiptDTO{CommandID: firstNonBlank(cmd.IdempotencyKey, "submit-result"), Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), AcceptedAt: now}, nil
}

func (d *demoAPI) SubmitQADecision(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.QADecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	now := time.Now().UTC()
	return missioncontrol.CommandReceiptDTO{CommandID: firstNonBlank(cmd.IdempotencyKey, cmd.QARef), Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: cmd.TaskID.String(), AcceptedAt: now}, nil
}

func (d *demoAPI) PostDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.PostDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	view, ok := d.missions[cmd.MissionID.String()]
	if !ok {
		return missioncontrol.CommandReceiptDTO{}, missioncontrol.ErrProjectionNotFound
	}
	now := time.Now().UTC()
	id, err := operations.NewPrefixedIDGenerator().NewID("message")
	if err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	displayName := viewer.ViewerID
	view.Discussion.Messages = append(view.Discussion.Messages, missioncontrol.MessageDTO{MessageID: id, DiscussionGroupID: string(cmd.DiscussionGroupID), Kind: firstNonBlank(cmd.MessageKind, "message"), AuthorID: viewer.ViewerID, AuthorDisplayName: displayName, AuthorType: string(viewer.ViewerKind), Body: cmd.Content, PostedAt: now})
	view.UpdatedAt = now
	view.Version++
	d.missions[cmd.MissionID.String()] = view
	if err := d.persistLocked(ctx); err != nil {
		return missioncontrol.CommandReceiptDTO{}, err
	}
	return missioncontrol.CommandReceiptDTO{CommandID: id, Status: "accepted", MissionID: cmd.MissionID.String(), AcceptedAt: now}, nil
}

func (d *demoAPI) ReplyDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ReplyDiscussionMessageCommand) (missioncontrol.CommandReceiptDTO, error) {
	return d.PostDiscussionMessage(ctx, viewer, missioncontrol.PostDiscussionMessageCommand{RequestID: cmd.RequestID, IdempotencyKey: cmd.IdempotencyKey, MissionID: cmd.MissionID, DiscussionGroupID: cmd.DiscussionGroupID, Content: cmd.Content, Summary: cmd.Summary, MessageKind: "reply"})
}

func (d *demoAPI) ListInbox(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ListInboxCommand) (missioncontrol.InboxPageDTO, error) {
	view, err := d.GetMissionControlView(ctx, viewer, cmd.MissionID)
	if err != nil {
		return missioncontrol.InboxPageDTO{}, err
	}
	return missioncontrol.InboxPageDTO{Items: view.InboxSummary.Items}, nil
}

func (d *demoAPI) MarkInboxRead(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	return d.inboxReceipt(cmd)
}

func (d *demoAPI) CloseInboxItem(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	return d.inboxReceipt(cmd)
}

func (d *demoAPI) inboxReceipt(cmd missioncontrol.InboxItemCommand) (missioncontrol.CommandReceiptDTO, error) {
	now := time.Now().UTC()
	return missioncontrol.CommandReceiptDTO{CommandID: firstNonBlank(cmd.IdempotencyKey, string(cmd.InboxItemID)), Status: "accepted", AcceptedAt: now}, nil
}

func (d *demoAPI) DecideBusinessApproval(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.BusinessApprovalDecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	now := time.Now().UTC()
	return missioncontrol.CommandReceiptDTO{CommandID: firstNonBlank(cmd.IdempotencyKey, cmd.ApprovalRef), Status: "accepted", MissionID: cmd.MissionID.String(), AcceptedAt: now}, nil
}

func (d *demoAPI) ListApprovals(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ApprovalListQuery) (missioncontrol.ApprovalPageDTO, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var items []missioncontrol.ApprovalItemDTO
	for _, view := range d.missions {
		if !query.MissionID.IsZero() && view.Mission.MissionID != query.MissionID.String() {
			continue
		}
		for _, check := range view.QA.Checks {
			if check.Status != "requested" {
				continue
			}
			id := "qa:" + view.Mission.MissionID + ":" + check.QARef
			items = append(items, missioncontrol.ApprovalItemDTO{ApprovalID: id, ApprovalType: "qa", Status: "pending", Priority: "normal", MissionID: view.Mission.MissionID, TaskID: check.TaskID, SourceType: "qa", SourceID: check.QARef, Title: "QA decision for " + check.TaskID, Summary: check.Message, RequestedBy: "qa", AssignedTo: viewer.ViewerID, AvailableActions: []missioncontrol.ApprovalActionDTO{{ActionID: "approve", Label: "Approve", Method: "POST", Path: "/api/approvals/" + id + "/decision", RequiresBody: true, ConfirmationRequired: true}, {ActionID: "reject", Label: "Reject", Method: "POST", Path: "/api/approvals/" + id + "/decision", RequiresBody: true, ConfirmationRequired: true, ReasonRequired: true}}, CreatedAt: check.CreatedAt})
		}
	}
	return missioncontrol.ApprovalPageDTO{Items: items, Page: missioncontrol.PageDTO{Total: len(items), Returned: len(items)}}, nil
}

func (d *demoAPI) DecideApproval(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.ApprovalDecisionCommand) (missioncontrol.CommandReceiptDTO, error) {
	now := time.Now().UTC()
	return missioncontrol.CommandReceiptDTO{CommandID: firstNonBlank(cmd.IdempotencyKey, cmd.ApprovalID), Status: "accepted", AcceptedAt: now}, nil
}

func (d *demoAPI) ListResourcesFiltered(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ListResourcesRequest) (resourceadmin.ResourceAdminViewDTO, error) {
	if strings.TrimSpace(req.Kind) == "memory_profile" {
		return resourceadmin.ResourceAdminViewDTO{}, resourceadmin.ErrServiceDependency
	}
	out := d.resources
	out.Page = resourceadmin.PageDTO{Limit: req.Limit, Offset: req.Offset, Total: len(out.Tools) + len(out.MCPServers) + len(out.Skills), Returned: len(out.Tools) + len(out.MCPServers) + len(out.Skills)}
	return out, nil
}

func (d *demoAPI) ListSkillBundles(ctx context.Context, viewer resourceadmin.ViewerRef) (resourceadmin.SkillBundleListDTO, error) {
	return resourceadmin.SkillBundleListDTO{}, resourceadmin.ErrServiceDependency
}

func (d *demoAPI) GetMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerDTO, error) {
	return resourceadmin.MCPServerDTO{}, resourceadmin.ErrServiceDependency
}

func (d *demoAPI) GetSkillDetail(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) (resourceadmin.SkillDetailDTO, error) {
	return resourceadmin.SkillDetailDTO{}, resourceadmin.ErrServiceDependency
}

func (d *demoAPI) GetMemoryProfile(ctx context.Context, viewer resourceadmin.ViewerRef, profileID string) (resourceadmin.MemoryProfileDTO, error) {
	return resourceadmin.MemoryProfileDTO{}, resourceadmin.ErrServiceDependency
}

func (d *demoAPI) GetPersonalMemoryProfileByAgent(ctx context.Context, viewer resourceadmin.ViewerRef, agentID string) (resourceadmin.MemoryProfileDTO, error) {
	return resourceadmin.MemoryProfileDTO{}, resourceadmin.ErrServiceDependency
}

func (d *demoAPI) BuildMemoryDigest(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.BuildMemoryDigestRequest) (resourceadmin.MemoryDigestDTO, error) {
	return resourceadmin.MemoryDigestDTO{}, resourceadmin.ErrServiceDependency
}

func (d *demoAPI) DiscoverMCPServers(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.DiscoverMCPRequest) (resourceadmin.DiscoverMCPResponseDTO, error) {
	return resourceadmin.DiscoverMCPResponseDTO{}, resourceadmin.ErrServiceDependency
}

func (d *demoAPI) RegisterMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.RegisterMCPServerRequest) (resourceadmin.MCPServerDTO, error) {
	return resourceadmin.MCPServerDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) UpdateMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.RegisterMCPServerRequest) (resourceadmin.MCPServerDTO, error) {
	return resourceadmin.MCPServerDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) RegisterTool(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ToolDefinitionRequest) (resourceadmin.ToolDTO, error) {
	return resourceadmin.ToolDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) UpdateTool(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ToolDefinitionRequest) (resourceadmin.ToolDTO, error) {
	return resourceadmin.ToolDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) SyncMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerSyncDTO, error) {
	// Demo mode has no MCP transport adapter; keep
	// this explicit instead of returning fake synced tools.
	return resourceadmin.MCPServerSyncDTO{ToolID: toolID, Status: "not_connected", FailureReason: "demo MCP transport adapter is not configured", SyncedAt: time.Now().UTC()}, nil
}

func (d *demoAPI) GetMCPServerHealth(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerHealthDTO, error) {
	// Demo mode has no MCP transport adapter; keep
	// this explicit instead of reporting fake availability.
	return resourceadmin.MCPServerHealthDTO{ToolID: toolID, Status: "not_connected", Mode: "startup", Message: "MCP transport adapter is not connected in demo mode", FailureReason: "demo MCP transport adapter is not configured", CheckedAt: time.Now().UTC()}, nil
}

func (d *demoAPI) ImportSkillPackage(ctx context.Context, viewer resourceadmin.ViewerRef, reader io.ReaderAt, size int64) (resourceadmin.SkillDTO, error) {
	return resourceadmin.SkillDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) CreateSkill(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.CreateSkillRequest) (resourceadmin.SkillDetailDTO, error) {
	return resourceadmin.SkillDetailDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) RegisterSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillVersionCandidateRequest) (resourceadmin.SkillVersionDTO, error) {
	return resourceadmin.SkillVersionDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) PromoteSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, version string, req resourceadmin.PromoteSkillVersionRequest) (resourceadmin.SkillVersionDTO, error) {
	return resourceadmin.SkillVersionDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) RollbackSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, version string, req resourceadmin.RollbackSkillVersionRequest) (resourceadmin.SkillVersionDTO, error) {
	return resourceadmin.SkillVersionDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) CreateSkillBundle(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.CreateSkillBundleRequest) (resourceadmin.SkillBundleDTO, error) {
	return resourceadmin.SkillBundleDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) AddSkillsToBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, req resourceadmin.AddSkillsToBundleRequest) (resourceadmin.SkillBundleDTO, error) {
	return resourceadmin.SkillBundleDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) ImportSkillIntoBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, reader io.ReaderAt, size int64) (resourceadmin.SkillBundleDTO, error) {
	return resourceadmin.SkillBundleDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) RemoveSkillFromBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID, skillID string) (resourceadmin.SkillBundleDTO, error) {
	return resourceadmin.SkillBundleDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) DeleteSkillBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, req resourceadmin.DeleteSkillBundleRequest) error {
	return demoResourceCommandUnavailable()
}

func (d *demoAPI) SaveSkillMarkdown(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, raw string) (resourceadmin.SkillDetailDTO, error) {
	return resourceadmin.SkillDetailDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) GetSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) (resourceadmin.SkillFileDTO, error) {
	return resourceadmin.SkillFileDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) SaveSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileWriteRequest) (resourceadmin.SkillFileDTO, error) {
	return resourceadmin.SkillFileDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) UploadSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileUploadRequest) (resourceadmin.SkillFileDTO, error) {
	return resourceadmin.SkillFileDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) DownloadSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) (resourceadmin.SkillFileDownloadDTO, error) {
	return resourceadmin.SkillFileDownloadDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) DeleteSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) error {
	return demoResourceCommandUnavailable()
}

func (d *demoAPI) CreateSkillFolder(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) error {
	return demoResourceCommandUnavailable()
}

func (d *demoAPI) DisableTool(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	return demoResourceCommandUnavailable()
}

func (d *demoAPI) DisableSkill(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) error {
	return demoResourceCommandUnavailable()
}

func (d *demoAPI) DeleteMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	return demoResourceCommandUnavailable()
}

func (d *demoAPI) DeleteTool(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	return demoResourceCommandUnavailable()
}

func (d *demoAPI) DeleteSkill(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) error {
	return demoResourceCommandUnavailable()
}

func (d *demoAPI) ExportSkillPackage(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) (resourceadmin.SkillPackageDTO, error) {
	return resourceadmin.SkillPackageDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) BindResourceToAgent(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.BindResourceRequest) (resourceadmin.ResourceBindingDTO, error) {
	return resourceadmin.ResourceBindingDTO{}, demoResourceCommandUnavailable()
}

func (d *demoAPI) UnbindResourceFromAgent(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.UnbindResourceRequest) error {
	return demoResourceCommandUnavailable()
}

func demoResourceCommandUnavailable() error {
	// TODO(resource-demo-registry): wire demo mode to the same resource command
	// services as production, or keep returning this dependency error instead of
	// pretending registry/package/binding mutations succeeded.
	return resourceadmin.ErrServiceDependency
}
