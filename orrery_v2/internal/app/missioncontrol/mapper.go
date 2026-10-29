package missioncontrol

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/outputpolicy"
	"orrery_v2/internal/mission/projection"
)

func MapViewDTO(model projection.ReadModel, viewer ViewerRef) MissionControlViewDTO {
	return MissionControlViewDTO{
		Mission: MissionDTO{
			MissionID:                  model.Mission.MissionID.String(),
			Title:                      model.Mission.Title,
			FinalGoal:                  model.Mission.FinalGoal,
			Status:                     string(model.Mission.Status),
			Phase:                      deriveMissionPhase(model),
			PhaseUpdatedAt:             model.Mission.CreatedAt,
			RiskLevel:                  deriveMissionRisk(model),
			OriginKind:                 model.Mission.OriginKind,
			OriginID:                   model.Mission.OriginID,
			OriginTodoID:               model.Mission.OriginTodoID,
			ExecutionAttempt:           model.Mission.ExecutionAttempt,
			DiscussionMode:             model.Mission.DiscussionMode,
			ConversationID:             model.Discussion.ConversationID,
			OriginDevelopmentProjectID: model.Mission.OriginDevelopmentProjectID,
			CreatedAt:                  model.Mission.CreatedAt,
		},
		Summary:         mapSummary(model),
		Team:            mapTeam(model.Team),
		TaskBoard:       mapTaskBoard(model),
		Evidence:        mapEvidence(model.Evidence, viewer),
		QA:              mapQA(model.QA),
		Discussion:      mapDiscussion(model.Discussion, model.Team, viewer),
		InboxSummary:    mapInbox(model.InboxSummary, viewer),
		ProgressGraph:   mapProgress(model.ProgressGraph),
		LatestDecisions: mapDecisions(model.LatestDecisions),
		Blockers:        mapBlockers(model.Blockers),
		UpdatedAt:       model.UpdatedAt,
		Version:         model.Checkpoint.Version,
	}
}

func MapMissionSummaryDTO(model projection.ReadModel, viewer ViewerRef) MissionSummaryDTO {
	view := MapViewDTO(model, viewer)
	return MissionSummaryDTO{
		MissionID:            view.Mission.MissionID,
		Title:                view.Mission.Title,
		Status:               view.Mission.Status,
		Phase:                view.Mission.Phase,
		RiskLevel:            view.Mission.RiskLevel,
		TeamSize:             view.Summary.TeamSize,
		ActiveTaskCount:      view.Summary.ActiveTaskCount,
		ActiveRunCount:       activeRuntimeSessionCount(model.RuntimeSessions),
		PendingApprovalCount: view.Summary.PendingApprovalCount,
		BlockerCount:         view.Summary.BlockerCount,
		DeliveryRate:         view.Summary.DeliveryRate,
		UpdatedAt:            view.UpdatedAt,
	}
}

func activeRuntimeSessionCount(sessions []projection.RuntimeSessionView) int {
	count := 0
	for index := range sessions {
		if runtimeSessionIsActive(sessions, index) {
			count++
		}
	}
	return count
}

func runtimeSessionIsActive(sessions []projection.RuntimeSessionView, index int) bool {
	if index < 0 || index >= len(sessions) {
		return false
	}
	switch sessions[index].Status {
	case projection.RuntimeSessionQueued, projection.RuntimeSessionRunning:
		return !runtimeSessionSupersededForActivity(sessions, index)
	default:
		return false
	}
}

func runtimeSessionSupersededForActivity(sessions []projection.RuntimeSessionView, currentIndex int) bool {
	if currentIndex < 0 || currentIndex >= len(sessions) {
		return false
	}
	current := sessions[currentIndex]
	if current.TaskID.IsZero() {
		return false
	}
	for candidateIndex, candidate := range sessions {
		if candidateIndex == currentIndex || candidate.TaskID != current.TaskID || candidate.RunID == current.RunID {
			continue
		}
		if runtimeSessionFollows(candidate, current, candidateIndex, currentIndex) {
			return true
		}
	}
	return false
}

func runtimeSessionFollows(candidate, current projection.RuntimeSessionView, candidateIndex, currentIndex int) bool {
	candidateStartedAt := candidate.QueuedAt
	if candidateStartedAt.IsZero() {
		candidateStartedAt = candidate.StartedAt
	}
	if candidateStartedAt.IsZero() {
		candidateStartedAt = candidate.UpdatedAt
	}
	currentStartedAt := current.QueuedAt
	if currentStartedAt.IsZero() {
		currentStartedAt = current.StartedAt
	}
	if currentStartedAt.IsZero() {
		currentStartedAt = current.UpdatedAt
	}
	if !candidateStartedAt.Equal(currentStartedAt) {
		return candidateStartedAt.After(currentStartedAt)
	}
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	return candidateIndex > currentIndex
}

func mapSummary(model projection.ReadModel) MissionControlSummaryDTO {
	summary := MissionControlSummaryDTO{
		TeamSize:     len(model.Team.Members),
		BlockerCount: len(model.Blockers),
		UpdatedAt:    model.Mission.CreatedAt,
	}
	approved := 0
	riskRefs := map[string]struct{}{}
	for _, task := range model.TaskBoard.Tasks {
		switch task.Status {
		case projection.TaskStatusStarted, projection.TaskStatusAssigned, projection.TaskStatusAccepted, projection.TaskStatusSubmitted, projection.TaskStatusReworkRequested:
			summary.ActiveTaskCount++
		case projection.TaskStatusApproved:
			approved++
		}
		if task.QAStatus == projection.QAStatusRequested {
			summary.PendingApprovalCount++
		}
		if task.Status == projection.TaskStatusBlocked || len(task.BlockerRefs) > 0 {
			if len(task.BlockerRefs) == 0 {
				summary.RiskCount++
			}
			for _, ref := range task.BlockerRefs {
				riskRefs[ref] = struct{}{}
			}
		}
	}
	for _, blocker := range model.Blockers {
		riskRefs[blocker.BlockerID] = struct{}{}
	}
	summary.RiskCount += len(riskRefs)
	if total := len(model.TaskBoard.Tasks); total > 0 {
		summary.DeliveryRate = float64(approved) / float64(total)
	}
	return summary
}

func deriveMissionPhase(model projection.ReadModel) string {
	if model.Mission.Status == projection.MissionStatusCompleted || model.Mission.Status == projection.MissionStatusCancelled {
		return "closed"
	}
	if len(model.QA.Checks) > 0 || len(model.QA.Approvals) > 0 || len(model.QA.Rejections) > 0 {
		return "qa"
	}
	if len(model.TaskBoard.Tasks) > 0 {
		return "execution"
	}
	return "intake"
}

func deriveMissionRisk(model projection.ReadModel) string {
	if len(model.Blockers) > 0 {
		return "high"
	}
	for _, task := range model.TaskBoard.Tasks {
		if task.Status == projection.TaskStatusBlocked || len(task.BlockerRefs) > 0 {
			return "high"
		}
	}
	return "normal"
}

func mapTeam(team projection.TeamView) TeamDTO {
	dto := TeamDTO{}
	for _, member := range team.Members {
		dto.Members = append(dto.Members, TeamMemberDTO{
			AgentID:         member.AgentID.String(),
			AgentProfileID:  member.AgentProfileID.String(),
			AgentInstanceID: member.AgentInstanceID.String(),
			AgentKind:       string(member.AgentKind),
			DisplayName:     member.DisplayName,
			RealName:        member.RealName,
			RoleName:        member.RoleName,
			Department:      member.Department,
			Position:        member.Position,
			MissionRole:     member.MissionRole,
			Status:          string(member.Status),
			JoinedAt:        member.JoinedAt,
		})
	}
	for _, assignment := range team.Assignments {
		dto.Assignments = append(dto.Assignments, AssignmentDTO{
			AssignmentID:    assignment.AssignmentID,
			AgentID:         assignment.AgentID.String(),
			AgentProfileID:  assignment.AgentProfileID.String(),
			AgentInstanceID: assignment.AgentInstanceID.String(),
			AgentKind:       string(assignment.AgentKind),
			TaskID:          assignment.TaskID.String(),
			MissionRole:     assignment.MissionRole,
			ChangedAt:       assignment.ChangedAt,
		})
	}
	for _, request := range team.StaffingRequests {
		dto.StaffingRequests = append(dto.StaffingRequests, StaffingRequestDTO{
			RequestID: request.RequestID,
			RoleName:  request.RoleName,
			Reason:    request.Reason,
			CreatedAt: request.CreatedAt,
		})
	}
	return dto
}

func mapTaskBoard(model projection.ReadModel) TaskBoardDTO {
	dto := TaskBoardDTO{}
	members := teamMemberLookup(model.Team)
	for _, task := range model.TaskBoard.Tasks {
		member := members[task.Assignee.String()]
		card := TaskCardDTO{
			TaskID:                    task.TaskID.String(),
			Title:                     task.Goal,
			Assignee:                  task.Assignee.String(),
			AssigneeDisplayName:       member.DisplayName,
			AssigneeType:              teamMemberKind(member),
			Goal:                      task.Goal,
			SourceKind:                task.SourceKind,
			SourceID:                  task.SourceID,
			SourceStepID:              task.SourceStepID,
			ExecutionPermissionScopes: append([]string(nil), task.ExecutionPermissionScopes...),
			WorkerPersonalGoalSummary: publicMissionText(task.WorkerPersonalGoalSummary, "任务目标已记录到 Orrery。"),
			Status:                    string(task.Status),
			Priority:                  task.Priority,
			DueAt:                     task.DueAt,
			LatestResultRef:           task.LatestResultRef,
			QAStatus:                  string(task.QAStatus),
			BlockerRefs:               append([]string(nil), task.BlockerRefs...),
			BlockedReason:             firstBlockerSummary(task.BlockerRefs, model.Blockers),
			UpdatedAt:                 task.UpdatedAt,
		}
		for _, criterion := range task.AcceptanceCriteria {
			card.AcceptanceCriteria = append(card.AcceptanceCriteria, AcceptanceCriterionDTO{ID: criterion.ID, Description: criterion.Description})
		}
		for _, deliverable := range task.ExpectedDeliverables {
			card.ExpectedDeliverables = append(card.ExpectedDeliverables, ExpectedDeliverableDTO{ID: deliverable.ID, Title: deliverable.Title})
		}
		for _, evidence := range task.RequiredEvidence {
			card.RequiredEvidence = append(card.RequiredEvidence, RequiredEvidenceDTO{ID: evidence.ID, Description: evidence.Description, Kind: evidence.Kind, Required: evidence.Required})
		}
		dto.Tasks = append(dto.Tasks, card)
	}
	for _, dep := range model.TaskBoard.Dependencies {
		dto.Dependencies = append(dto.Dependencies, TaskDependencyDTO{TaskID: dep.TaskID.String(), DependsOn: dep.DependsOn.String()})
	}
	return dto
}

func mapEvidence(evidence projection.EvidenceView, viewer ViewerRef) EvidenceDTO {
	dto := EvidenceDTO{}
	for _, item := range evidence.EvidenceItems {
		if !viewer.CanSeeAny(item.VisibleToPermissionKeys) {
			continue
		}
		dto.EvidenceItems = append(dto.EvidenceItems, EvidenceItemDTO{
			EvidenceID:            string(item.EvidenceID),
			TaskID:                item.TaskID.String(),
			AgentID:               item.AgentID.String(),
			EvidenceRequirementID: item.EvidenceRequirementID,
			Kind:                  string(item.Kind),
			ArtifactID:            item.ArtifactRef.ID,
			ArtifactURI:           item.ArtifactRef.URI,
			ToolLogRef:            string(item.ToolLogRef.ID),
			ExternalURL:           item.ExternalURL,
			SubmittedAt:           item.SubmittedAt,
		})
	}
	for _, requirement := range evidence.MissingRequirements {
		dto.MissingRequirements = append(dto.MissingRequirements, RequiredEvidenceDTO{ID: requirement.ID, Description: requirement.Description, Kind: requirement.Kind, Required: requirement.Required})
	}
	return dto
}

func mapQA(qa projection.QAView) QADTO {
	dto := QADTO{}
	for _, check := range qa.Checks {
		dto.Checks = append(dto.Checks, QACheckDTO{QARef: check.QARef, TaskID: check.TaskID.String(), Status: string(check.Status), Message: publicMissionText(check.Message, "QA 检查结果已记录到 Orrery。"), CreatedAt: check.CreatedAt})
	}
	for _, decision := range qa.Approvals {
		dto.Approvals = append(dto.Approvals, QADecisionDTO{QARef: decision.QARef, TaskID: decision.TaskID.String(), Reason: publicMissionText(decision.Reason, "审批原因已记录到 Orrery。"), Message: publicMissionText(decision.Message, "审批结果已记录到 Orrery。"), DecidedAt: decision.DecidedAt})
	}
	for _, decision := range qa.Rejections {
		dto.Rejections = append(dto.Rejections, QADecisionDTO{QARef: decision.QARef, TaskID: decision.TaskID.String(), Reason: publicMissionText(decision.Reason, "拒绝原因已记录到 Orrery。"), Message: publicMissionText(decision.Message, "审批结果已记录到 Orrery。"), DecidedAt: decision.DecidedAt})
	}
	for _, request := range qa.ReworkRequests {
		dto.ReworkRequests = append(dto.ReworkRequests, ReworkRequestDTO{TaskID: request.TaskID.String(), QARef: request.QARef, Reason: publicMissionText(request.Reason, "返工原因已记录到 Orrery。"), Message: publicMissionText(request.Message, "返工要求已记录到 Orrery。"), Status: string(request.Status), CreatedAt: request.CreatedAt, ResolvedAt: request.ResolvedAt})
	}
	return dto
}

func mapDiscussion(discussion projection.DiscussionView, team projection.TeamView, viewer ViewerRef) DiscussionDTO {
	dto := DiscussionDTO{DiscussionGroupID: discussion.DiscussionGroupID, ConversationID: discussion.ConversationID, DiscussionDigestRef: discussion.DiscussionDigestRef}
	members := teamMemberLookup(team)
	for _, message := range discussion.Messages {
		if !viewer.CanSeeAny(message.Visibility) {
			continue
		}
		member := members[message.AuthorID.String()]
		authorType := messageAuthorType(message, member)
		body := message.Body
		if strings.EqualFold(strings.TrimSpace(authorType), "agent") {
			body = publicMissionText(body, "任务进展已记录到 Orrery。")
		}
		dto.Messages = append(dto.Messages, MessageDTO{
			MessageID:         message.MessageID,
			DiscussionGroupID: message.DiscussionGroupID,
			Kind:              "message",
			AuthorID:          message.AuthorID.String(),
			AuthorDisplayName: firstString(member.DisplayName, message.AuthorID.String()),
			AuthorType:        authorType,
			Body:              body,
			Mentions:          agentIDs(message.Mentions),
			ReplyToMessageID:  message.ReplyToMessageID,
			ReplyPreview:      replyPreview(message.ReplyToMessageID, discussion.Messages),
			PostedAt:          message.PostedAt,
		})
	}
	for _, mention := range discussion.PendingMentions {
		dto.PendingMentions = append(dto.PendingMentions, MentionDTO{MessageID: mention.MessageID, AgentID: mention.AgentID.String(), CreatedAt: mention.CreatedAt})
	}
	dto.Decisions = mapDecisions(discussion.Decisions)
	dto.Blockers = mapBlockers(discussion.Blockers)
	return dto
}

func messageAuthorType(message projection.MessageView, member projection.TeamMemberView) string {
	if message.AuthorType != "" {
		return message.AuthorType
	}
	if member.AgentID != "" {
		return "agent"
	}
	if message.AuthorID != "" {
		return "agent"
	}
	return ""
}

func mapInbox(inbox projection.InboxSummaryView, viewer ViewerRef) InboxSummaryDTO {
	dto := InboxSummaryDTO{}
	for _, item := range inbox.Items {
		if item.OwnerID.String() != viewer.ViewerID && viewer.ViewerKind != ViewerKindSystem {
			continue
		}
		dto.Items = append(dto.Items, InboxItemDTO{
			InboxItemID:      item.InboxItemID,
			OwnerID:          item.OwnerID.String(),
			MessageID:        item.MessageID,
			ItemType:         "mention",
			Priority:         "normal",
			SourceType:       "message",
			SourceID:         item.MessageID,
			Reason:           publicMissionText(item.Reason, "待处理事项已记录到 Orrery。"),
			Status:           item.Status,
			AvailableActions: inboxActions(item.InboxItemID),
			CreatedAt:        item.CreatedAt,
			ClosedAt:         item.ClosedAt,
		})
	}
	dto.OpenCount = 0
	for _, item := range dto.Items {
		if item.Status == "open" {
			dto.OpenCount++
		}
	}
	return dto
}

func teamMemberLookup(team projection.TeamView) map[string]projection.TeamMemberView {
	members := make(map[string]projection.TeamMemberView, len(team.Members))
	for _, member := range team.Members {
		addTeamMemberLookup(members, member.AgentID.String(), member)
		addTeamMemberLookup(members, member.AgentProfileID.String(), member)
	}
	return members
}

func addTeamMemberLookup(members map[string]projection.TeamMemberView, id string, member projection.TeamMemberView) {
	if id == "" {
		return
	}
	members[id] = member
}

func teamMemberKind(member projection.TeamMemberView) string {
	if member.AgentKind != "" {
		return string(member.AgentKind)
	}
	if member.AgentID != "" {
		return "agent"
	}
	return ""
}

func firstBlockerSummary(refs []string, blockers []projection.BlockerView) string {
	if len(refs) == 0 {
		return ""
	}
	for _, ref := range refs {
		for _, blocker := range blockers {
			if blocker.BlockerID == ref {
				return publicMissionText(blocker.Summary, "阻塞原因已记录到 Orrery。")
			}
		}
	}
	return ""
}

func replyPreview(replyToMessageID string, messages []projection.MessageView) string {
	if replyToMessageID == "" {
		return ""
	}
	for _, message := range messages {
		if message.MessageID == replyToMessageID {
			return publicMissionText(message.Body, "回复内容已记录到 Orrery。")
		}
	}
	return ""
}

func inboxActions(inboxItemID string) []InboxActionDTO {
	return []InboxActionDTO{
		{ActionID: "read", Label: "Mark read", Method: "POST", Path: "/api/inbox/" + inboxItemID + "/read"},
		{ActionID: "close", Label: "Close", Method: "POST", Path: "/api/inbox/" + inboxItemID + "/close"},
	}
}

func mapDecisions(decisions []projection.DecisionView) []DecisionDTO {
	dto := make([]DecisionDTO, 0, len(decisions))
	for _, decision := range decisions {
		dto = append(dto, DecisionDTO{DecisionID: decision.DecisionID, Summary: publicMissionText(decision.Summary, "决策已记录到 Orrery。"), SourceRefs: append([]string(nil), decision.SourceRefs...), RecordedAt: decision.RecordedAt})
	}
	return dto
}

func mapBlockers(blockers []projection.BlockerView) []BlockerDTO {
	dto := make([]BlockerDTO, 0, len(blockers))
	for _, blocker := range blockers {
		dto = append(dto, BlockerDTO{BlockerID: blocker.BlockerID, TaskID: blocker.TaskID.String(), RaisedBy: blocker.RaisedBy.String(), Summary: publicMissionText(blocker.Summary, "阻塞原因已记录到 Orrery。"), RaisedAt: blocker.RaisedAt})
	}
	return dto
}

func mapProgress(graph projection.ProgressGraphView) ProgressGraphDTO {
	dto := ProgressGraphDTO{CurrentFocus: append([]string(nil), graph.CurrentFocus...)}
	for _, node := range graph.Nodes {
		dto.Nodes = append(dto.Nodes, ProgressNodeDTO{ID: node.ID, EventID: node.EventID, EventType: node.EventType, Label: publicMissionText(node.Label, "进度节点"), Status: node.Status, Details: publicMissionText(node.Details, "进度详情已记录到 Orrery。"), OccurredAt: node.OccurredAt, RefType: node.RefType, RefID: node.RefID})
	}
	for _, edge := range graph.Edges {
		dto.Edges = append(dto.Edges, ProgressEdgeDTO{From: edge.From, To: edge.To, Kind: edge.Kind})
	}
	return dto
}

// publicMissionText is the final read boundary for mission-control text. It
// keeps normal human prose while removing reasoning blocks and machine-only
// run metadata from historical projections.
func publicMissionText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return outputpolicy.PublicText(value, fallback)
}

func publicRuntimeSessionSummary(session projection.RuntimeSessionView) string {
	return outputpolicy.PublicRuntimeText(firstString(session.ErrorSummary, session.LastMessage), string(session.Status))
}

func agentIDs(ids []agentcore.AgentID) []string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, id.String())
	}
	return values
}
