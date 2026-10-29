package missioncontrol

import (
	"net/http"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/projection"
)

const (
	opsPermissionMissionWrite    = "mission.write"
	opsPermissionApprovalsDecide = "approvals.decide"
)

func MapOpsViewDTO(model projection.ReadModel, viewer ViewerRef) MissionOpsViewDTO {
	return MissionOpsViewDTO{
		MissionID:       model.Mission.MissionID.String(),
		Title:           model.Mission.Title,
		Status:          string(model.Mission.Status),
		Phase:           deriveMissionPhase(model),
		CurrentFocus:    mapOpsFocus(model, viewer),
		AgentLanes:      mapAgentLanes(model, viewer),
		RuntimeSessions: mapRuntimeSessions(model.RuntimeSessions),
		ActionCards:     mapActionCards(model, viewer),
		EvidenceGates:   mapEvidenceGates(model, viewer),
		ApprovalGates:   mapApprovalGates(model),
		AutonomyPolicy:  defaultAutonomyPolicy(),
		UpdatedAt:       model.UpdatedAt,
		Version:         model.Checkpoint.Version,
	}
}

func mapOpsFocus(model projection.ReadModel, viewer ViewerRef) []OpsFocusDTO {
	focus := make([]OpsFocusDTO, 0)
	missionID := model.Mission.MissionID.String()
	for _, check := range model.QA.Checks {
		if check.Status != projection.QAStatusRequested {
			continue
		}
		focus = append(focus, OpsFocusDTO{
			FocusID:    "focus:qa:" + check.QARef,
			Kind:       "approval",
			Priority:   "high",
			Title:      "Review QA",
			Summary:    publicMissionText(check.Message, "QA 检查结果已记录到 Orrery。"),
			MissionID:  missionID,
			TaskID:     check.TaskID.String(),
			SourceType: "qa",
			SourceID:   check.QARef,
			CreatedAt:  check.CreatedAt,
		})
	}
	for _, request := range model.ApprovalRequests {
		if request.Status != "pending" {
			continue
		}
		focus = append(focus, OpsFocusDTO{
			FocusID:    "focus:approval:" + request.RequestID,
			Kind:       "approval",
			Priority:   firstString(request.Priority, "normal"),
			Title:      firstString(request.Title, "Approval required"),
			Summary:    publicMissionText(request.Summary, "审批事项已记录到 Orrery。"),
			MissionID:  request.MissionID.String(),
			TaskID:     request.TaskID.String(),
			AgentID:    firstString(request.AssignedTo.String(), request.RequestedBy.String()),
			SourceType: request.SourceType,
			SourceID:   request.SourceID,
			CreatedAt:  request.CreatedAt,
		})
	}
	for _, blocker := range model.Blockers {
		focus = append(focus, OpsFocusDTO{
			FocusID:    "focus:blocker:" + blocker.BlockerID,
			Kind:       "blocker",
			Priority:   "high",
			Title:      "Resolve blocker",
			Summary:    publicMissionText(blocker.Summary, "阻塞原因已记录到 Orrery。"),
			MissionID:  missionID,
			TaskID:     blocker.TaskID.String(),
			AgentID:    blocker.RaisedBy.String(),
			SourceType: "blocker",
			SourceID:   blocker.BlockerID,
			CreatedAt:  blocker.RaisedAt,
		})
	}
	for _, item := range model.InboxSummary.Items {
		if item.Status != "open" || !viewerCanSeeInboxOwner(viewer, item.OwnerID.String()) {
			continue
		}
		focus = append(focus, OpsFocusDTO{
			FocusID:    "focus:inbox:" + item.InboxItemID,
			Kind:       "inbox",
			Priority:   "normal",
			Title:      "Reply in discussion",
			Summary:    publicMissionText(item.Reason, "待处理事项已记录到 Orrery。"),
			MissionID:  missionID,
			AgentID:    item.OwnerID.String(),
			SourceType: "inbox",
			SourceID:   item.InboxItemID,
			CreatedAt:  item.CreatedAt,
		})
	}
	if len(focus) > 0 {
		return focus
	}
	for _, task := range model.TaskBoard.Tasks {
		if !isActiveTaskStatus(task.Status) {
			continue
		}
		focus = append(focus, OpsFocusDTO{
			FocusID:    "focus:task:" + task.TaskID.String(),
			Kind:       "task",
			Priority:   firstString(task.Priority, "normal"),
			Title:      firstString(task.Goal, "Active task"),
			MissionID:  missionID,
			TaskID:     task.TaskID.String(),
			AgentID:    task.Assignee.String(),
			SourceType: "task",
			SourceID:   task.TaskID.String(),
			CreatedAt:  task.UpdatedAt,
		})
	}
	return focus
}

func mapAgentLanes(model projection.ReadModel, viewer ViewerRef) []AgentLaneDTO {
	laneIndex := map[string]int{}
	lanes := make([]AgentLaneDTO, 0, len(model.Team.Members))
	for _, member := range model.Team.Members {
		agentID := member.AgentID.String()
		if agentID == "" {
			agentID = firstString(member.AgentProfileID.String(), member.AgentInstanceID.String())
		}
		if agentID == "" {
			continue
		}
		laneIndex[agentID] = len(lanes)
		lane := AgentLaneDTO{
			AgentID:         agentID,
			AgentProfileID:  member.AgentProfileID.String(),
			AgentInstanceID: member.AgentInstanceID.String(),
			AgentKind:       teamMemberKind(member),
			DisplayName:     firstString(member.DisplayName, member.RealName, agentID),
			MissionRole:     member.MissionRole,
			Status:          laneStatusFromMember(member),
		}
		lanes = append(lanes, lane)
	}
	for _, task := range model.TaskBoard.Tasks {
		agentID := task.Assignee.String()
		if agentID == "" {
			continue
		}
		index, ok := laneIndex[agentID]
		if !ok {
			index = len(lanes)
			laneIndex[agentID] = index
			lanes = append(lanes, AgentLaneDTO{AgentID: agentID, DisplayName: agentID, AgentKind: "agent", Status: "available"})
		}
		switch {
		case isActiveTaskStatus(task.Status):
			lanes[index].ActiveTaskIDs = append(lanes[index].ActiveTaskIDs, task.TaskID.String())
			lanes[index].Status = "working"
		case task.Status == projection.TaskStatusCreated:
			lanes[index].WaitingTaskIDs = append(lanes[index].WaitingTaskIDs, task.TaskID.String())
		}
	}
	runtimeSessionsByAgent := map[string][]projection.RuntimeSessionView{}
	activeRuntimeAgents := map[string]bool{}
	for sessionIndex, session := range model.RuntimeSessions {
		agentID := session.AgentID.String()
		if agentID == "" {
			continue
		}
		runtimeSessionsByAgent[agentID] = append(runtimeSessionsByAgent[agentID], session)
		if runtimeSessionIsActive(model.RuntimeSessions, sessionIndex) {
			activeRuntimeAgents[agentID] = true
		}
	}
	for _, item := range model.InboxSummary.Items {
		if item.Status != "open" {
			continue
		}
		agentID := item.OwnerID.String()
		if agentID == "" {
			continue
		}
		index, ok := laneIndex[agentID]
		if !ok {
			continue
		}
		if viewerCanSeeInboxOwner(viewer, agentID) || viewer.ViewerKind == ViewerKindSystem {
			lanes[index].InboxOpenCount++
		}
	}
	for agentID, sessions := range runtimeSessionsByAgent {
		index, ok := laneIndex[agentID]
		if !ok {
			continue
		}
		lanes[index].RuntimeSessions = append(lanes[index].RuntimeSessions, mapRuntimeSessions(sessions)...)
		if activeRuntimeAgents[agentID] {
			lanes[index].Status = "running"
		}
	}
	return lanes
}

func mapRuntimeSessions(sessions []projection.RuntimeSessionView) []RuntimeSessionDTO {
	dto := make([]RuntimeSessionDTO, 0, len(sessions))
	for _, session := range sessions {
		dto = append(dto, RuntimeSessionDTO{
			RunID:                       session.RunID.String(),
			AgentID:                     session.AgentID.String(),
			TaskID:                      session.TaskID.String(),
			RuntimeKind:                 session.RuntimeKind,
			QueueID:                     session.QueueID,
			Status:                      string(session.Status),
			LastEvent:                   session.LastEvent,
			ErrorSummary:                publicRuntimeSessionSummary(session),
			LastHeartbeatAt:             session.LastHeartbeatAt,
			LeaseExpiresAt:              session.LeaseExpiresAt,
			IsAlive:                     session.IsAlive,
			IsStale:                     session.IsStale,
			LaneHealth:                  runtimeLaneHealth(session),
			StartedAt:                   session.StartedAt,
			UpdatedAt:                   session.UpdatedAt,
			ResponsibilityContextID:     responsibilityContextValue(session, func(value *agentcore.ResponsibilityContext) string { return value.ContextID }),
			ResponsibilityID:            responsibilityContextValue(session, func(value *agentcore.ResponsibilityContext) string { return value.ResponsibilityID }),
			ResponsibilityActorMemberID: responsibilityContextValue(session, func(value *agentcore.ResponsibilityContext) string { return value.ActorMemberID }),
			ResponsibilityVersion:       responsibilityContextVersion(session),
		})
	}
	return dto
}

func responsibilityContextValue(session projection.RuntimeSessionView, read func(*agentcore.ResponsibilityContext) string) string {
	if session.ResponsibilityContext == nil {
		return ""
	}
	return strings.TrimSpace(read(session.ResponsibilityContext))
}

func responsibilityContextVersion(session projection.RuntimeSessionView) int64 {
	if session.ResponsibilityContext == nil {
		return 0
	}
	return session.ResponsibilityContext.ResponsibilityVersion
}

func runtimeLaneHealth(session projection.RuntimeSessionView) string {
	switch session.Status {
	case projection.RuntimeSessionQueued, projection.RuntimeSessionRunning:
		if session.IsStale {
			return "stale"
		}
		if session.IsAlive {
			return "healthy"
		}
		return "unknown"
	case projection.RuntimeSessionCompleted:
		return "completed"
	case projection.RuntimeSessionFailed, projection.RuntimeSessionTimedOut, projection.RuntimeSessionCancelled, projection.RuntimeSessionBlocked:
		return string(session.Status)
	default:
		return ""
	}
}

func mapActionCards(model projection.ReadModel, viewer ViewerRef) []ActionCardDTO {
	cards := make([]ActionCardDTO, 0)
	missionID := model.Mission.MissionID.String()
	for _, check := range model.QA.Checks {
		if check.Status != projection.QAStatusRequested {
			continue
		}
		taskID := check.TaskID.String()
		cards = append(cards, ActionCardDTO{
			ActionID:   "action:approve-qa:" + check.QARef,
			Kind:       "approve_qa",
			Priority:   "high",
			Title:      "Approve QA",
			Summary:    check.Message,
			MissionID:  missionID,
			TaskID:     taskID,
			SourceType: "qa",
			SourceID:   check.QARef,
			Status:     "open",
			Commands: []CommandLinkDTO{
				{ActionID: "approve", Label: "Approve", Method: http.MethodPost, Path: "/api/missions/" + missionID + "/qa/" + check.QARef + "/decision", RequiredPermission: opsPermissionMissionWrite},
				{ActionID: "reject_with_rework", Label: "Request rework", Method: http.MethodPost, Path: "/api/missions/" + missionID + "/qa/" + check.QARef + "/decision", RequiredPermission: opsPermissionMissionWrite},
			},
			CreatedAt: check.CreatedAt,
		})
	}
	for _, blocker := range model.Blockers {
		cards = append(cards, ActionCardDTO{
			ActionID:   "action:resolve-blocker:" + blocker.BlockerID,
			Kind:       "resolve_blocker",
			Priority:   "high",
			Title:      "Resolve blocker",
			Summary:    blocker.Summary,
			MissionID:  missionID,
			TaskID:     blocker.TaskID.String(),
			AgentID:    blocker.RaisedBy.String(),
			SourceType: "blocker",
			SourceID:   blocker.BlockerID,
			Status:     "open",
			Commands: []CommandLinkDTO{
				{ActionID: "reply", Label: "Reply", Method: http.MethodPost, Path: "/api/missions/" + missionID + "/discussion/messages", RequiredPermission: opsPermissionMissionWrite},
				{ActionID: "create_task", Label: "Create task", Method: http.MethodPost, Path: "/api/missions/" + missionID + "/tasks", RequiredPermission: opsPermissionMissionWrite},
			},
			CreatedAt: blocker.RaisedAt,
		})
	}
	for _, task := range model.TaskBoard.Tasks {
		if task.Assignee == "" && task.Status == projection.TaskStatusCreated {
			taskID := task.TaskID.String()
			cards = append(cards, ActionCardDTO{
				ActionID:  "action:assign-task:" + taskID,
				Kind:      "assign_task",
				Priority:  firstString(task.Priority, "normal"),
				Title:     "Assign task",
				Summary:   task.Goal,
				MissionID: missionID,
				TaskID:    taskID,
				Status:    "open",
				Commands: []CommandLinkDTO{
					{ActionID: "assign", Label: "Assign", Method: http.MethodPost, Path: "/api/missions/" + missionID + "/tasks/" + taskID + "/assign", RequiredPermission: opsPermissionMissionWrite},
				},
				CreatedAt: task.UpdatedAt,
			})
		}
	}
	for _, item := range model.InboxSummary.Items {
		if item.Status != "open" || !viewerCanSeeInboxOwner(viewer, item.OwnerID.String()) {
			continue
		}
		cards = append(cards, ActionCardDTO{
			ActionID:   "action:reply-inbox:" + item.InboxItemID,
			Kind:       "reply_to_agent",
			Priority:   "normal",
			Title:      "Reply in discussion",
			Summary:    item.Reason,
			MissionID:  missionID,
			AgentID:    item.OwnerID.String(),
			SourceType: "inbox",
			SourceID:   item.InboxItemID,
			Status:     "open",
			Commands: []CommandLinkDTO{
				{ActionID: "reply", Label: "Reply", Method: http.MethodPost, Path: "/api/missions/" + missionID + "/discussion/messages/" + item.MessageID + "/reply", RequiredPermission: opsPermissionMissionWrite},
				{ActionID: "close", Label: "Close", Method: http.MethodPost, Path: "/api/inbox/" + item.InboxItemID + "/close", RequiredPermission: opsPermissionMissionWrite},
			},
			CreatedAt: item.CreatedAt,
		})
	}
	return cards
}

func mapApprovalGates(model projection.ReadModel) []ApprovalGateDTO {
	gates := make([]ApprovalGateDTO, 0)
	missionID := model.Mission.MissionID.String()
	for _, check := range model.QA.Checks {
		if check.Status != projection.QAStatusRequested {
			continue
		}
		gates = append(gates, ApprovalGateDTO{
			ApprovalID:   "qa:" + missionID + ":" + check.QARef,
			ApprovalType: "qa",
			Status:       "pending",
			Priority:     "high",
			MissionID:    missionID,
			TaskID:       check.TaskID.String(),
			SourceType:   "qa",
			SourceID:     check.QARef,
			Title:        "Review QA",
			Summary:      check.Message,
			Commands: []CommandLinkDTO{
				{ActionID: "approve", Label: "Approve", Method: http.MethodPost, Path: "/api/missions/" + missionID + "/qa/" + check.QARef + "/decision", RequiredPermission: opsPermissionMissionWrite},
				{ActionID: "reject_with_rework", Label: "Request rework", Method: http.MethodPost, Path: "/api/missions/" + missionID + "/qa/" + check.QARef + "/decision", RequiredPermission: opsPermissionMissionWrite},
			},
			CreatedAt: check.CreatedAt,
		})
	}
	for _, request := range model.ApprovalRequests {
		if request.Status != "pending" {
			continue
		}
		gates = append(gates, ApprovalGateDTO{
			ApprovalID:   request.RequestID,
			ApprovalType: request.ApprovalType,
			Status:       request.Status,
			Priority:     firstString(request.Priority, "normal"),
			MissionID:    request.MissionID.String(),
			TaskID:       request.TaskID.String(),
			SourceType:   request.SourceType,
			SourceID:     request.SourceID,
			Title:        firstString(request.Title, "Approval required"),
			Summary:      request.Summary,
			RequestedBy:  request.RequestedBy.String(),
			AssignedTo:   request.AssignedTo.String(),
			EvidenceRefs: append([]string(nil), request.EvidenceRefs...),
			Commands: []CommandLinkDTO{
				{ActionID: "approve", Label: "Approve", Method: http.MethodPost, Path: "/api/approvals/" + request.RequestID + "/decision", RequiredPermission: opsPermissionApprovalsDecide},
				{ActionID: "reject", Label: "Reject", Method: http.MethodPost, Path: "/api/approvals/" + request.RequestID + "/decision", RequiredPermission: opsPermissionApprovalsDecide},
			},
			CreatedAt: request.CreatedAt,
			DecidedAt: request.DecidedAt,
		})
	}
	return gates
}

func mapEvidenceGates(model projection.ReadModel, viewer ViewerRef) []EvidenceGateDTO {
	itemsByTask := map[string][]projection.EvidenceItemView{}
	for _, item := range model.Evidence.EvidenceItems {
		if !viewer.CanSeeAny(item.VisibleToPermissionKeys) {
			continue
		}
		taskID := item.TaskID.String()
		itemsByTask[taskID] = append(itemsByTask[taskID], item)
	}
	gates := make([]EvidenceGateDTO, 0, len(model.TaskBoard.Tasks))
	for _, task := range model.TaskBoard.Tasks {
		taskID := task.TaskID.String()
		submitted := itemsByTask[taskID]
		missing := missingEvidenceRequirementIDs(task.RequiredEvidence, submitted)
		if len(task.RequiredEvidence) == 0 && len(submitted) == 0 && len(missing) == 0 {
			continue
		}
		gate := EvidenceGateDTO{
			TaskID:                taskID,
			Status:                evidenceGateStatus(len(task.RequiredEvidence), len(missing)),
			RequiredCount:         len(task.RequiredEvidence),
			SubmittedCount:        len(submitted),
			MissingRequirementIDs: missing,
		}
		for _, item := range submitted {
			gate.SubmittedEvidenceIDs = append(gate.SubmittedEvidenceIDs, string(item.EvidenceID))
			if item.SubmittedAt.After(gate.LastSubmittedAt) {
				gate.LastSubmittedAt = item.SubmittedAt
			}
		}
		gates = append(gates, gate)
	}
	return gates
}

func missingEvidenceRequirementIDs(required []projection.RequiredEvidenceView, submitted []projection.EvidenceItemView) []string {
	if len(required) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, item := range submitted {
		if item.EvidenceRequirementID != "" {
			seen[item.EvidenceRequirementID] = struct{}{}
		}
	}
	missing := make([]string, 0)
	for _, requirement := range required {
		if requirement.ID == "" {
			continue
		}
		if _, ok := seen[requirement.ID]; !ok {
			missing = append(missing, requirement.ID)
		}
	}
	return missing
}

func evidenceGateStatus(requiredCount, missingCount int) string {
	if requiredCount == 0 {
		return "submitted"
	}
	if missingCount == 0 {
		return "complete"
	}
	return "missing"
}

func laneStatusFromMember(member projection.TeamMemberView) string {
	switch member.Status {
	case projection.TeamMemberInvited:
		return "invited"
	case projection.TeamMemberReleased:
		return "released"
	default:
		return "available"
	}
}

func isActiveTaskStatus(status projection.TaskStatus) bool {
	switch status {
	case projection.TaskStatusAssigned, projection.TaskStatusAccepted, projection.TaskStatusStarted, projection.TaskStatusBlocked, projection.TaskStatusSubmitted, projection.TaskStatusReworkRequested:
		return true
	default:
		return false
	}
}

func viewerCanSeeInboxOwner(viewer ViewerRef, ownerID string) bool {
	return viewer.ViewerKind == ViewerKindSystem || viewer.ViewerID == ownerID
}

func defaultAutonomyPolicy() AutonomyPolicyDTO {
	return AutonomyPolicyDTO{
		Mode:                   "manual",
		AutoStartAllowed:       false,
		MaxRiskWithoutApproval: "low",
		RequiredApprovals:      []string{"tool_access", "external_side_effect", "high_risk"},
	}
}
