package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	missionprojection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/uow"
)

type productionRuntimeTaskProjectionReader struct {
	unit  *uow.UnitOfWork
	tasks interface {
		GetTask(context.Context, uow.Tx, missiontask.TaskID) (missiontask.TaskRecord, error)
		LatestSubmission(context.Context, uow.Tx, missiontask.TaskID) (missiontask.TaskSubmission, bool, error)
	}
}

func (r productionRuntimeTaskProjectionReader) ResolveRuntimeTask(ctx context.Context, _ string, missionID missioncore.MissionID, taskID missioncore.TaskID) (missionprojection.TaskCardView, error) {
	if r.unit == nil || r.tasks == nil {
		return missionprojection.TaskCardView{}, missioncontrol.ErrServiceDependency
	}
	var record missiontask.TaskRecord
	var submission missiontask.TaskSubmission
	var hasSubmission bool
	if err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		record, err = r.tasks.GetTask(ctx, tx, missiontask.TaskID(taskID))
		if err != nil {
			return err
		}
		submission, hasSubmission, err = r.tasks.LatestSubmission(ctx, tx, missiontask.TaskID(taskID))
		return err
	}); err != nil {
		return missionprojection.TaskCardView{}, err
	}
	if record.Contract.MissionID != missionID {
		return missionprojection.TaskCardView{}, missioncontrol.ErrProjectionNotFound
	}
	return runtimeTaskProjectionFromRecord(record, submission, hasSubmission), nil
}

func runtimeTaskProjectionFromRecord(record missiontask.TaskRecord, submission missiontask.TaskSubmission, hasSubmission bool) missionprojection.TaskCardView {
	contract := record.Contract
	card := missionprojection.TaskCardView{
		TaskID: contract.ID, Goal: contract.Goal, SourceKind: contract.SourceKind, SourceID: contract.SourceID,
		SourceStepID: contract.SourceStepID, ExecutionPermissionScopes: append([]string(nil), contract.ExecutionPermissionScopes...),
		ExecutionAllowedToolIDs:   append([]string(nil), contract.ExecutionAllowedToolIDs...),
		ExecutionEnvelope:         agentcore.CloneExecutionEnvelope(contract.ExecutionEnvelope),
		WorkerPersonalGoalSummary: contract.WorkerPersonalGoal, Priority: contract.Priority, DueAt: contract.DuePolicy.DueAt,
		Status: runtimeTaskProjectionStatus(record.Status), UpdatedAt: record.StatusChangedAt,
	}
	if ref := contract.Assignee.AgentProfileRef; ref != nil && !ref.ID.IsZero() {
		card.Assignee = missionprojectionAgentID(ref.ID.String())
	}
	if hasSubmission {
		card.LatestResultRef = submission.AgentResultRef.String()
		if submission.SubmittedAt.After(card.UpdatedAt) {
			card.UpdatedAt = submission.SubmittedAt
		}
	}
	switch record.Status {
	case missiontask.TaskStatusApproved:
		card.QAStatus = missionprojection.QAStatusApproved
	case missiontask.TaskStatusRejected, missiontask.TaskStatusReworkRequested:
		card.QAStatus = missionprojection.QAStatusRejected
	}
	for _, item := range contract.AcceptanceCriteria {
		card.AcceptanceCriteria = append(card.AcceptanceCriteria, missionprojection.AcceptanceCriterionView{ID: string(item.ID), Description: item.Description})
	}
	for _, item := range contract.ExpectedDeliverables {
		card.ExpectedDeliverables = append(card.ExpectedDeliverables, missionprojection.ExpectedDeliverableView{ID: string(item.ID), Title: item.Title})
	}
	for _, item := range contract.EvidenceRequirements {
		card.RequiredEvidence = append(card.RequiredEvidence, missionprojection.RequiredEvidenceView{ID: string(item.ID), Description: item.Description, Kind: string(item.Kind), Required: item.Required})
	}
	return card
}

func runtimeTaskProjectionStatus(status missiontask.TaskStatus) missionprojection.TaskStatus {
	switch status {
	case missiontask.TaskStatusDraft:
		return missionprojection.TaskStatusCreated
	case missiontask.TaskStatusAssigned:
		return missionprojection.TaskStatusAssigned
	case missiontask.TaskStatusAccepted:
		return missionprojection.TaskStatusAccepted
	case missiontask.TaskStatusInProgress:
		return missionprojection.TaskStatusStarted
	case missiontask.TaskStatusBlocked:
		return missionprojection.TaskStatusBlocked
	case missiontask.TaskStatusSubmitted, missiontask.TaskStatusInReview:
		return missionprojection.TaskStatusSubmitted
	case missiontask.TaskStatusApproved:
		return missionprojection.TaskStatusApproved
	case missiontask.TaskStatusRejected:
		return missionprojection.TaskStatusRejected
	case missiontask.TaskStatusReworkRequested:
		return missionprojection.TaskStatusReworkRequested
	case missiontask.TaskStatusCancelled:
		return missionprojection.TaskStatusCancelled
	default:
		return ""
	}
}

func missionprojectionAgentID(value string) agentcore.AgentID {
	return agentcore.AgentID(strings.TrimSpace(value))
}

func (p productionAPI) refreshMissionProjection(ctx context.Context, tenantID string, missionID missioncore.MissionID) error {
	if missionID.IsZero() {
		return missioncontrol.ErrInvalidRequest
	}
	if p.missionRepo == nil || p.eventReader == nil {
		return missioncontrol.ErrServiceDependency
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		tenantID = "global"
	}
	for attempt := 0; attempt < 8; attempt++ {
		model, err := loadProductionMissionProjection(ctx, p.missionRepo, tenantID, missionID)
		if err != nil {
			return err
		}
		events, err := loadProductionMissionEvents(ctx, p.eventReader, tenantID, missionID)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		replayed := missionprojection.NewReadModel(missionID)
		replayed.TenantID = tenantID
		replayed.Mission.TenantID = tenantID
		projector := missionprojection.NewProjector()
		for _, event := range events {
			if err := projector.Apply(ctx, replayed, event); err != nil {
				return err
			}
		}
		model, changed, err := mergeReplayedDiscussionProjection(model, *replayed)
		if err != nil {
			return err
		}
		if p.runtimeTasks != nil {
			var taskChanged bool
			model, taskChanged, err = reconcileCanonicalMissionTasks(ctx, p.runtimeTasks, tenantID, model)
			if err != nil {
				return err
			}
			changed = changed || taskChanged
		}
		if changed {
			at := events[len(events)-1].OccurredAt
			if at.IsZero() {
				at = time.Now().UTC()
			}
			if model.UpdatedAt.After(at) {
				at = model.UpdatedAt
			}
			for _, task := range model.TaskBoard.Tasks {
				if task.UpdatedAt.After(at) {
					at = task.UpdatedAt
				}
			}
			model.UpdatedAt = at
			model.Checkpoint.Version++
			model.Checkpoint.UpdatedAt = at
			if model.Checkpoint.MissionID.IsZero() {
				model.Checkpoint.MissionID = missionID
			}
		}
		if err := saveProductionMissionProjection(ctx, p.missionRepo, tenantID, model); err != nil {
			if errors.Is(err, missioncontrol.ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return missioncontrol.ErrConflict
}

func reconcileCanonicalMissionTasks(ctx context.Context, reader missioncontrol.RuntimeTaskProjectionReaderPort, tenantID string, model missionprojection.ReadModel) (missionprojection.ReadModel, bool, error) {
	beforeTasks := append([]missionprojection.TaskCardView(nil), model.TaskBoard.Tasks...)
	beforeBlockers := append([]missionprojection.BlockerView(nil), model.Blockers...)
	beforeDiscussionBlockers := append([]missionprojection.BlockerView(nil), model.Discussion.Blockers...)
	approved := make(map[missioncore.TaskID]struct{})
	for index := range model.TaskBoard.Tasks {
		current := model.TaskBoard.Tasks[index]
		canonical, err := reader.ResolveRuntimeTask(ctx, tenantID, model.Mission.MissionID, current.TaskID)
		if err != nil {
			if errors.Is(err, missiontask.ErrNotFound) {
				continue
			}
			return model, false, err
		}
		model.TaskBoard.Tasks[index] = mergeCanonicalTaskCard(current, canonical)
		if canonical.Status == missionprojection.TaskStatusApproved {
			approved[current.TaskID] = struct{}{}
		}
	}
	if len(approved) > 0 {
		model.Blockers = withoutApprovedTaskRuntimeBlockers(model.Blockers, approved)
		model.Discussion.Blockers = withoutApprovedTaskRuntimeBlockers(model.Discussion.Blockers, approved)
	}
	changed := !reflect.DeepEqual(beforeTasks, model.TaskBoard.Tasks) ||
		!reflect.DeepEqual(beforeBlockers, model.Blockers) ||
		!reflect.DeepEqual(beforeDiscussionBlockers, model.Discussion.Blockers)
	return model, changed, nil
}

func mergeCanonicalTaskCard(current, canonical missionprojection.TaskCardView) missionprojection.TaskCardView {
	merged := current
	if canonical.Assignee != "" {
		merged.Assignee = canonical.Assignee
	}
	merged.Goal = canonical.Goal
	merged.SourceKind = canonical.SourceKind
	merged.SourceID = canonical.SourceID
	merged.SourceStepID = canonical.SourceStepID
	merged.ExecutionPermissionScopes = append([]string(nil), canonical.ExecutionPermissionScopes...)
	merged.ExecutionAllowedToolIDs = append([]string(nil), canonical.ExecutionAllowedToolIDs...)
	merged.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(canonical.ExecutionEnvelope)
	merged.WorkerPersonalGoalSummary = canonical.WorkerPersonalGoalSummary
	merged.Priority = canonical.Priority
	merged.DueAt = canonical.DueAt
	merged.AcceptanceCriteria = append([]missionprojection.AcceptanceCriterionView(nil), canonical.AcceptanceCriteria...)
	merged.ExpectedDeliverables = append([]missionprojection.ExpectedDeliverableView(nil), canonical.ExpectedDeliverables...)
	merged.RequiredEvidence = append([]missionprojection.RequiredEvidenceView(nil), canonical.RequiredEvidence...)
	if canonical.LatestResultRef != "" {
		merged.LatestResultRef = canonical.LatestResultRef
	}
	if canonical.QAStatus != "" {
		merged.QAStatus = canonical.QAStatus
	}
	switch canonical.Status {
	case missionprojection.TaskStatusSubmitted,
		missionprojection.TaskStatusApproved,
		missionprojection.TaskStatusRejected,
		missionprojection.TaskStatusReworkRequested,
		missionprojection.TaskStatusCancelled:
		merged.Status = canonical.Status
	}
	if canonical.Status == missionprojection.TaskStatusApproved {
		merged.BlockerRefs = withoutRuntimeFailureRefs(merged.BlockerRefs)
	}
	if canonical.UpdatedAt.After(merged.UpdatedAt) {
		merged.UpdatedAt = canonical.UpdatedAt
	}
	return merged
}

func withoutRuntimeFailureRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if !strings.HasPrefix(strings.TrimSpace(ref), "runtime-failure:") {
			out = append(out, ref)
		}
	}
	return out
}

func withoutApprovedTaskRuntimeBlockers(blockers []missionprojection.BlockerView, approved map[missioncore.TaskID]struct{}) []missionprojection.BlockerView {
	out := make([]missionprojection.BlockerView, 0, len(blockers))
	for _, blocker := range blockers {
		_, taskApproved := approved[blocker.TaskID]
		if taskApproved && strings.HasPrefix(strings.TrimSpace(blocker.BlockerID), "runtime-failure:") {
			continue
		}
		out = append(out, blocker)
	}
	return out
}

func loadProductionMissionEvents(ctx context.Context, reader missioncontrol.MissionEventReaderPort, tenantID string, missionID missioncore.MissionID) ([]missionevent.Envelope, error) {
	if tenantReader, ok := reader.(interface {
		LoadMissionForTenant(context.Context, string, missioncore.MissionID) ([]missionevent.Envelope, error)
	}); ok {
		return tenantReader.LoadMissionForTenant(ctx, tenantID, missionID)
	}
	if tenantID != "global" {
		return nil, missioncontrol.ErrServiceDependency
	}
	return reader.LoadMission(ctx, missionID)
}

func mergeReplayedDiscussionProjection(model missionprojection.ReadModel, replayed missionprojection.ReadModel) (missionprojection.ReadModel, bool, error) {
	model, originChanged, err := mergeReplayedPersonalAssistantMissionOrigin(model, replayed)
	if err != nil {
		return model, false, err
	}
	beforeTaskBoard := model.TaskBoard
	beforeEvidence := model.Evidence
	beforeQA := model.QA
	beforeApprovals := append([]missionprojection.ApprovalRequestView(nil), model.ApprovalRequests...)
	beforeRuntime := append([]missionprojection.RuntimeSessionView(nil), model.RuntimeSessions...)
	beforeDiscussion := model.Discussion
	beforeInbox := model.InboxSummary
	beforeDecisions := append([]missionprojection.DecisionView(nil), model.LatestDecisions...)
	beforeBlockers := append([]missionprojection.BlockerView(nil), model.Blockers...)
	beforeProgress := model.ProgressGraph

	model.TaskBoard = mergeReplayedTaskBoard(model.TaskBoard, replayed.TaskBoard)
	if len(replayed.Evidence.EvidenceItems) > 0 || len(replayed.Evidence.MissingRequirements) > 0 {
		model.Evidence = replayed.Evidence
	}
	if len(replayed.QA.Checks) > 0 || len(replayed.QA.Approvals) > 0 || len(replayed.QA.Rejections) > 0 || len(replayed.QA.ReworkRequests) > 0 {
		model.QA = replayed.QA
	}
	// Approval requests are event-sourced runtime state. Replaying this stream
	// is authoritative, including the populated-to-empty transition after a
	// request has been decided.
	model.ApprovalRequests = replayed.ApprovalRequests
	if len(replayed.RuntimeSessions) > 0 {
		model.RuntimeSessions = replayed.RuntimeSessions
	}
	if replayed.Discussion.DiscussionGroupID != "" {
		model.Discussion.DiscussionGroupID = replayed.Discussion.DiscussionGroupID
	}
	if len(replayed.Discussion.Messages) > 0 {
		model.Discussion.Messages = replayed.Discussion.Messages
	}
	if replayed.Discussion.DiscussionGroupID != "" || len(replayed.InboxSummary.Items) > 0 {
		model.Discussion.PendingMentions = replayed.Discussion.PendingMentions
	}
	if len(replayed.Discussion.Decisions) > 0 {
		model.Discussion.Decisions = replayed.Discussion.Decisions
		model.LatestDecisions = mergeDecisionViews(model.LatestDecisions, replayed.Discussion.Decisions)
	}
	if len(replayed.Discussion.Blockers) > 0 {
		model.Discussion.Blockers = replayed.Discussion.Blockers
		model.Blockers = mergeBlockerViews(model.Blockers, replayed.Discussion.Blockers)
	}
	if replayed.Discussion.DiscussionDigestRef != "" {
		model.Discussion.DiscussionDigestRef = replayed.Discussion.DiscussionDigestRef
	}
	if len(replayed.InboxSummary.Items) > 0 || replayed.InboxSummary.OpenCount != 0 {
		model.InboxSummary = replayed.InboxSummary
	}
	if len(replayed.ProgressGraph.Nodes) > 0 {
		model.ProgressGraph = mergeProgressGraph(model.ProgressGraph, replayed.ProgressGraph)
	}

	changed := originChanged ||
		!reflect.DeepEqual(beforeTaskBoard, model.TaskBoard) ||
		!reflect.DeepEqual(beforeEvidence, model.Evidence) ||
		!reflect.DeepEqual(beforeQA, model.QA) ||
		!reflect.DeepEqual(beforeApprovals, model.ApprovalRequests) ||
		!reflect.DeepEqual(beforeRuntime, model.RuntimeSessions) ||
		!reflect.DeepEqual(beforeDiscussion, model.Discussion) ||
		!reflect.DeepEqual(beforeInbox, model.InboxSummary) ||
		!reflect.DeepEqual(beforeDecisions, model.LatestDecisions) ||
		!reflect.DeepEqual(beforeBlockers, model.Blockers) ||
		!reflect.DeepEqual(beforeProgress, model.ProgressGraph)
	return model, changed, nil
}

func mergeReplayedTaskBoard(existing, replayed missionprojection.TaskBoardView) missionprojection.TaskBoardView {
	out := missionprojection.TaskBoardView{
		Tasks:        append([]missionprojection.TaskCardView(nil), existing.Tasks...),
		Dependencies: append([]missionprojection.TaskDependencyView(nil), existing.Dependencies...),
	}
	for _, incoming := range replayed.Tasks {
		found := false
		for index := range out.Tasks {
			if out.Tasks[index].TaskID != incoming.TaskID {
				continue
			}
			out.Tasks[index] = mergeReplayedTaskCard(out.Tasks[index], incoming)
			found = true
			break
		}
		if !found {
			out.Tasks = append(out.Tasks, incoming)
		}
	}
	for _, incoming := range replayed.Dependencies {
		found := false
		for _, current := range out.Dependencies {
			if current == incoming {
				found = true
				break
			}
		}
		if !found {
			out.Dependencies = append(out.Dependencies, incoming)
		}
	}
	return out
}

func mergeReplayedTaskCard(existing, replayed missionprojection.TaskCardView) missionprojection.TaskCardView {
	merged := replayed
	if merged.Assignee == "" {
		merged.Assignee = existing.Assignee
	}
	if strings.TrimSpace(merged.Goal) == "" {
		merged.Goal = existing.Goal
	}
	if strings.TrimSpace(merged.SourceKind) == "" {
		merged.SourceKind = existing.SourceKind
	}
	if strings.TrimSpace(merged.SourceID) == "" {
		merged.SourceID = existing.SourceID
	}
	if strings.TrimSpace(merged.SourceStepID) == "" {
		merged.SourceStepID = existing.SourceStepID
	}
	if merged.ExecutionPermissionScopes == nil {
		merged.ExecutionPermissionScopes = append([]string(nil), existing.ExecutionPermissionScopes...)
	}
	if strings.TrimSpace(merged.WorkerPersonalGoalSummary) == "" {
		merged.WorkerPersonalGoalSummary = existing.WorkerPersonalGoalSummary
	}
	if strings.TrimSpace(merged.Priority) == "" {
		merged.Priority = existing.Priority
	}
	if merged.DueAt.IsZero() {
		merged.DueAt = existing.DueAt
	}
	if len(merged.AcceptanceCriteria) == 0 {
		merged.AcceptanceCriteria = append([]missionprojection.AcceptanceCriterionView(nil), existing.AcceptanceCriteria...)
	}
	if len(merged.ExpectedDeliverables) == 0 {
		merged.ExpectedDeliverables = append([]missionprojection.ExpectedDeliverableView(nil), existing.ExpectedDeliverables...)
	}
	if len(merged.RequiredEvidence) == 0 {
		merged.RequiredEvidence = append([]missionprojection.RequiredEvidenceView(nil), existing.RequiredEvidence...)
	}
	if strings.TrimSpace(merged.LatestResultRef) == "" {
		merged.LatestResultRef = existing.LatestResultRef
	}
	if merged.QAStatus == "" {
		merged.QAStatus = existing.QAStatus
	}
	if existing.Status == missionprojection.TaskStatusApproved {
		merged.Status = existing.Status
		merged.LatestResultRef = existing.LatestResultRef
		merged.QAStatus = existing.QAStatus
		merged.BlockerRefs = append([]string(nil), existing.BlockerRefs...)
		merged.UpdatedAt = existing.UpdatedAt
	}
	return merged
}

func mergeReplayedPersonalAssistantMissionOrigin(model missionprojection.ReadModel, replayed missionprojection.ReadModel) (missionprojection.ReadModel, bool, error) {
	modelOwnerID := strings.TrimSpace(model.Mission.PersonalAssistantOriginOwnerID)
	modelSubjectID := strings.TrimSpace(model.Mission.PersonalAssistantOriginSubjectID)
	replayedOwnerID := strings.TrimSpace(replayed.Mission.PersonalAssistantOriginOwnerID)
	replayedSubjectID := strings.TrimSpace(replayed.Mission.PersonalAssistantOriginSubjectID)
	if (modelOwnerID == "") != (modelSubjectID == "") || (replayedOwnerID == "") != (replayedSubjectID == "") {
		return model, false, missioncontrol.ErrInvalidRequest
	}
	if modelOwnerID != "" && replayedOwnerID != "" && (modelOwnerID != replayedOwnerID || modelSubjectID != replayedSubjectID) {
		return model, false, missioncontrol.ErrInvalidRequest
	}
	if modelOwnerID == "" && replayedOwnerID != "" {
		model.Mission.PersonalAssistantOriginOwnerID = replayedOwnerID
		model.Mission.PersonalAssistantOriginSubjectID = replayedSubjectID
		return model, true, nil
	}
	return model, false, nil
}

func mergeDecisionViews(existing []missionprojection.DecisionView, incoming []missionprojection.DecisionView) []missionprojection.DecisionView {
	out := append([]missionprojection.DecisionView(nil), existing...)
	for _, next := range incoming {
		found := false
		for i := range out {
			if out[i].DecisionID == next.DecisionID {
				out[i] = next
				found = true
				break
			}
		}
		if !found {
			out = append(out, next)
		}
	}
	return out
}

func mergeBlockerViews(existing []missionprojection.BlockerView, incoming []missionprojection.BlockerView) []missionprojection.BlockerView {
	out := append([]missionprojection.BlockerView(nil), existing...)
	for _, next := range incoming {
		found := false
		for i := range out {
			if out[i].BlockerID == next.BlockerID {
				out[i] = next
				found = true
				break
			}
		}
		if !found {
			out = append(out, next)
		}
	}
	return out
}

func mergeProgressGraph(existing missionprojection.ProgressGraphView, incoming missionprojection.ProgressGraphView) missionprojection.ProgressGraphView {
	out := existing
	nodeSeen := make(map[string]struct{}, len(out.Nodes))
	for _, node := range out.Nodes {
		if node.EventID != "" {
			nodeSeen[node.EventID] = struct{}{}
		}
	}
	for _, node := range incoming.Nodes {
		if node.EventID == "" {
			continue
		}
		if _, ok := nodeSeen[node.EventID]; ok {
			continue
		}
		if len(out.Nodes) > 0 {
			prev := out.Nodes[len(out.Nodes)-1]
			out.Edges = append(out.Edges, missionprojection.ProgressEdgeView{From: prev.ID, To: node.ID, Kind: "observed_after"})
		}
		out.Nodes = append(out.Nodes, node)
		nodeSeen[node.EventID] = struct{}{}
	}
	if len(incoming.CurrentFocus) > 0 {
		out.CurrentFocus = append([]string(nil), incoming.CurrentFocus...)
	}
	return out
}
