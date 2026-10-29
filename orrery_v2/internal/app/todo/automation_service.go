package todo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/operations"
)

const (
	todoAutomationActorID       = "todo-automation-agent"
	todoAutomationActorName     = "Todo Automation Agent"
	todoHILExpiryActorID        = "hil:expiry"
	todoHILReplayActorID        = "hil:replay"
	todoAutomationNotReadyRetry = 6 * time.Hour
	todoAutomationFailureRetry  = 15 * time.Minute
)

func (s *Service) RunAutomationScan(ctx context.Context) error {
	_, err := s.RunScheduledAutomationScan(ctx, TodoAutomationScanRequest{})
	return err
}

func (s *Service) RunScheduledAutomationScan(ctx context.Context, request TodoAutomationScanRequest) (TodoAutomationScanResult, error) {
	if s == nil || s.repo == nil || s.clock == nil || s.ids == nil || s.automationDiscussion == nil || len(s.automationAgentIDs) == 0 {
		return TodoAutomationScanResult{}, ErrServiceDependency
	}
	result := TodoAutomationScanResult{}
	now := s.clock.Now().UTC()
	var sweepErrors []error
	if err := s.reconcileTodoAutomationAudits(ctx, s.automationBatchSize*8); err != nil {
		sweepErrors = append(sweepErrors, fmt.Errorf("reconcile Todo automation audits: %w", err))
	}
	if err := s.reconcileTodoHILDecisions(ctx, s.automationBatchSize); err != nil {
		sweepErrors = append(sweepErrors, fmt.Errorf("reconcile Todo HIL decisions: %w", err))
	}
	if err := s.reconcileTodoHILResolutions(ctx, s.automationBatchSize); err != nil {
		sweepErrors = append(sweepErrors, fmt.Errorf("reconcile Todo HIL resolutions: %w", err))
	}
	pauseRequests, err := s.repo.ListTodoMissionPauseRequests(ctx, s.automationBatchSize)
	if err != nil {
		return result, err
	}
	for _, pauseRequest := range pauseRequests {
		if err := s.pauseTodoMission(ctx, pauseRequest); err != nil {
			result.FailedCount++
			sweepErrors = append(sweepErrors, fmt.Errorf("pause Todo %s Mission %s: %w", pauseRequest.TodoID, pauseRequest.MissionID, err))
		} else {
			result.RecoveredCount++
		}
	}
	recoveryCandidates, err := s.repo.ListTodoAutomationExecutionRecoveryCandidates(ctx, now, s.automationBatchSize)
	if err != nil {
		return result, errors.Join(append(sweepErrors, err)...)
	}
	for _, item := range recoveryCandidates {
		state := *item.Automation
		expectedStatus := state.Status
		state.Status = TodoAutomationStatusExecuting
		state.NextCheckAt = time.Time{}
		state.UpdatedAt = now
		updated, _, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, expectedStatus)
		if err != nil {
			result.FailedCount++
			sweepErrors = append(sweepErrors, fmt.Errorf("claim Todo %s execution recovery: %w", item.TodoID, err))
			continue
		}
		if !updated {
			result.SkippedCount++
			continue
		}
		if _, err := s.executeApprovedTodo(ctx, item, state, "scheduled-recovery:"+firstNonEmptyText(request.ScheduledRunID, state.ProposalID), nil); err != nil {
			result.FailedCount++
			sweepErrors = append(sweepErrors, fmt.Errorf("recover Todo %s execution: %w", item.TodoID, err))
		} else {
			result.RecoveredCount++
		}
	}
	syncCandidates, err := s.repo.ListTodoAutomationDiscussionSyncCandidates(ctx, s.automationAgentIDs, s.executionSupportIDs, s.automationBatchSize)
	if err != nil {
		return result, errors.Join(append(sweepErrors, err)...)
	}
	for _, item := range syncCandidates {
		if err := s.syncTodoAutomationDiscussionMembers(ctx, item); err != nil {
			result.FailedCount++
			sweepErrors = append(sweepErrors, fmt.Errorf("sync todo %s discussion members: %w", item.TodoID, err))
		} else {
			result.DiscussionSyncCount++
		}
	}
	candidates, err := s.repo.ListTodoAutomationCandidates(ctx, now, s.automationBatchSize)
	if err != nil {
		return result, errors.Join(append(sweepErrors, err)...)
	}
	result.ScannedCount = len(candidates)
	result.EligibleCount = len(candidates)
	for _, item := range candidates {
		claimed, err := s.planTodo(ctx, item)
		if err != nil {
			result.FailedCount++
			sweepErrors = append(sweepErrors, fmt.Errorf("plan todo %s: %w", item.TodoID, err))
		} else if claimed {
			result.ClaimedCount++
			result.PlannedCount++
		} else {
			result.SkippedCount++
		}
	}
	return result, errors.Join(sweepErrors...)
}

// CheckTodoAutomation runs the same eligibility and claim-based planning path
// as the system Scheduler for exactly one authorized Todo.
func (s *Service) CheckTodoAutomation(ctx context.Context, viewer ViewerRef, request CheckTodoAutomationRequest) (TodoAutomationScanResult, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return TodoAutomationScanResult{}, err
	}
	request.TodoID = strings.TrimSpace(request.TodoID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.TodoID == "" || request.IdempotencyKey == "" {
		return TodoAutomationScanResult{}, ErrInvalidRequest
	}
	if s == nil || s.repo == nil || s.clock == nil || s.ids == nil || s.automationDiscussion == nil || len(s.automationAgentIDs) == 0 {
		return TodoAutomationScanResult{}, ErrServiceDependency
	}
	if err := s.reconcileTodoAutomationAudits(ctx, s.automationBatchSize*8); err != nil {
		return TodoAutomationScanResult{}, err
	}
	item, err := s.repo.GetTodoForTenant(ctx, viewer.TenantID, request.TodoID)
	if err != nil {
		return TodoAutomationScanResult{}, err
	}
	if err := s.authorizeTodo(ctx, viewer, item, "todo.edit"); err != nil {
		return TodoAutomationScanResult{}, err
	}
	result := TodoAutomationScanResult{ScannedCount: 1}
	if !todoAutomationPlanningCandidate(item, s.clock.Now().UTC()) {
		result.SkippedCount = 1
		return result, nil
	}
	result.EligibleCount = 1
	claimed, err := s.planTodo(ctx, item)
	if err != nil {
		result.FailedCount = 1
		return result, err
	}
	if !claimed {
		result.SkippedCount = 1
		return result, nil
	}
	result.ClaimedCount = 1
	result.PlannedCount = 1
	return result, nil
}

func (s *Service) ListTodoAutomationApprovals(ctx context.Context, viewer ViewerRef, query ListTodoAutomationApprovalsQuery) ([]TodoAutomationApproval, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return nil, err
	}
	if viewer.ViewerKind != ViewerKindUser {
		return []TodoAutomationApproval{}, nil
	}
	if s == nil || s.repo == nil {
		return nil, ErrServiceDependency
	}
	states, err := s.repo.ListTodoAutomationsForTenant(ctx, viewer.TenantID)
	if err != nil {
		return nil, err
	}
	out := make([]TodoAutomationApproval, 0, len(states))
	for _, state := range states {
		if state.ApprovalID == "" || (!viewerIdentityMatches(viewer, state.ApproverID) && !hasPermission(viewer, "todo.manage")) {
			continue
		}
		status := todoAutomationApprovalStatus(state.Status)
		if query.Status != "" && !automationApprovalStatusMatches(status, query.Status) {
			continue
		}
		item, err := s.repo.GetTodoForTenant(ctx, viewer.TenantID, state.TodoID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		if err := s.authorizeTodo(ctx, viewer, item, "todo.view"); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		if item.WorkKind == TodoWorkKindDevelopment {
			if s.developmentExecution == nil {
				return nil, ErrServiceDependency
			}
			if err := s.developmentExecution.EnsureTodoPlanApprovalAllowed(ctx, todoDevelopmentAuthorization(item, viewer, nil)); err != nil {
				if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
					continue
				}
				return nil, err
			}
		}
		if state.TodoVersion != item.Version && status == "pending" {
			status = "unavailable"
		}
		out = append(out, TodoAutomationApproval{
			ApprovalID:     state.ApprovalID,
			HILDecisionID:  state.HILDecisionID,
			Status:         status,
			TodoID:         state.TodoID,
			TodoTitle:      item.Title,
			TodoVersion:    state.TodoVersion,
			ConversationID: state.ConversationID,
			Proposal:       state.Proposal,
			Plan:           state.Plan,
			PlanDigest:     state.PlanDigest,
			AutomationMode: item.AutomationMode,
			RequestedBy:    state.RequestedBy,
			ApproverID:     state.ApproverID,
			CreatedAt:      state.CreatedAt,
			DecidedAt:      state.DecidedAt,
		})
	}
	return out, nil
}

func (s *Service) DecideTodoAutomationApproval(ctx context.Context, viewer ViewerRef, req DecideTodoAutomationApprovalRequest) (TodoAutomationState, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return TodoAutomationState{}, err
	}
	if viewer.ViewerKind != ViewerKindUser {
		return TodoAutomationState{}, ErrForbidden
	}
	if s == nil || s.repo == nil || s.clock == nil || s.automationDiscussion == nil {
		return TodoAutomationState{}, ErrServiceDependency
	}
	req.ApprovalID = strings.TrimSpace(req.ApprovalID)
	req.ActionID = strings.ToLower(strings.TrimSpace(req.ActionID))
	req.PlanDigest = strings.TrimSpace(req.PlanDigest)
	if req.ApprovalID == "" || req.IdempotencyKey == "" || (req.ActionID != "approve" && req.ActionID != "reject") || (req.ActionID == "reject" && strings.TrimSpace(req.Message) == "") {
		return TodoAutomationState{}, ErrInvalidRequest
	}
	states, err := s.repo.ListTodoAutomationsForTenant(ctx, viewer.TenantID)
	if err != nil {
		return TodoAutomationState{}, err
	}
	var state TodoAutomationState
	for _, candidate := range states {
		if candidate.ApprovalID == req.ApprovalID {
			state = candidate
			break
		}
	}
	if state.ApprovalID == "" {
		return TodoAutomationState{}, ErrNotFound
	}
	if !viewerIdentityMatches(viewer, state.ApproverID) && !hasPermission(viewer, "todo.manage") {
		return TodoAutomationState{}, ErrForbidden
	}
	if req.PlanDigest == "" || req.PlanDigest != state.PlanDigest {
		return TodoAutomationState{}, ErrConflict
	}
	if state.Status != TodoAutomationStatusPendingApproval {
		if (req.ActionID == "approve" && todoAutomationWasApproved(state.Status)) || (req.ActionID == "reject" && state.Status == TodoAutomationStatusRejected) {
			return state, nil
		}
		return TodoAutomationState{}, ErrConflict
	}
	item, err := s.repo.GetTodoForTenant(ctx, viewer.TenantID, state.TodoID)
	if err != nil {
		return TodoAutomationState{}, err
	}
	if err := s.authorizeTodo(ctx, viewer, item, "todo.edit"); err != nil {
		return TodoAutomationState{}, err
	}
	selfApprovalOverrideUsed := false
	if item.WorkKind == TodoWorkKindDevelopment {
		if req.ActionID == "approve" && todoApprovalRequesterMatches(item, viewer) {
			if !s.allowOwnerSelfApproval {
				s.recordTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.automation.self_approval_denied", "denied")
				return TodoAutomationState{}, ErrForbidden
			}
			selfApprovalOverrideUsed = true
		}
		if s.developmentExecution == nil {
			return TodoAutomationState{}, ErrServiceDependency
		}
		if err := s.developmentExecution.EnsureTodoPlanApprovalAllowed(ctx, todoDevelopmentAuthorization(item, viewer, nil)); err != nil {
			return TodoAutomationState{}, err
		}
	}
	planCurrent, currentErr := s.todoExecutionPlanIsCurrent(ctx, item, state)
	if currentErr != nil {
		return TodoAutomationState{}, currentErr
	}
	if item.Version != state.TodoVersion || !todoEligibleForAutomation(item) || !planCurrent {
		state.Status = TodoAutomationStatusStale
		state.FailureReason = "Todo or approved execution resources changed after this proposal was prepared"
		state.UpdatedAt = s.clock.Now().UTC()
		updated, current, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, TodoAutomationStatusPendingApproval)
		if err != nil {
			return TodoAutomationState{}, err
		}
		if !updated {
			return current, ErrConflict
		}
		s.recordTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.automation.approval_stale", "denied")
		s.recordTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.execution_plan.stale", "denied")
		return TodoAutomationState{}, ErrConflict
	}
	now := s.clock.Now().UTC()
	recordTodoAutomationDecisionViewer(&state, viewer)
	if selfApprovalOverrideUsed {
		if err := s.recordRequiredTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.automation.owner_self_approval_override", "succeeded"); err != nil {
			return TodoAutomationState{}, err
		}
	}
	state.DecisionMessage = strings.TrimSpace(req.Message)
	state.DecidedAt = now
	state.UpdatedAt = now
	if req.ActionID == "reject" {
		state.Status = TodoAutomationStatusRejected
		updated, current, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, TodoAutomationStatusPendingApproval)
		if err != nil {
			return TodoAutomationState{}, err
		}
		if !updated {
			if current.Status == TodoAutomationStatusRejected {
				return current, nil
			}
			return current, ErrConflict
		}
		s.recordTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.automation.rejected", "succeeded")
		_ = s.postAutomationDecisionComment(ctx, item, state, false)
		return state, nil
	}

	state.ApprovedPlanDigest = state.PlanDigest
	if item.AutomationMode == TodoAutomationModeSuggest {
		state.Status = TodoAutomationStatusApproved
		updated, current, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, TodoAutomationStatusPendingApproval)
		if err != nil {
			return TodoAutomationState{}, err
		}
		if !updated {
			if todoAutomationWasApproved(current.Status) {
				return current, nil
			}
			return current, ErrConflict
		}
		s.recordTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.automation.plan_adopted", "succeeded")
		s.recordTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.execution_plan.approved", "succeeded")
		_ = s.postAutomationDecisionComment(ctx, item, state, true)
		return state, nil
	}
	recordTodoAutomationExecutionRequester(&state, viewer)
	state.Status = TodoAutomationStatusExecuting
	updated, current, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, TodoAutomationStatusPendingApproval)
	if err != nil {
		return TodoAutomationState{}, err
	}
	if !updated {
		if todoAutomationWasApproved(current.Status) {
			return current, nil
		}
		return current, ErrConflict
	}
	s.recordTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.automation.approved", "succeeded")
	s.recordTodoAutomationAudit(ctx, state, viewer.ViewerID, "todo.execution_plan.approved", "succeeded")
	_ = s.postAutomationDecisionComment(ctx, item, state, true)
	return s.executeApprovedTodo(ctx, item, state, req.IdempotencyKey, &viewer)
}

func (s *Service) StartTodoAutomationExecution(ctx context.Context, viewer ViewerRef, req StartTodoAutomationExecutionRequest) (TodoAutomationState, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return TodoAutomationState{}, err
	}
	if viewer.ViewerKind != ViewerKindUser {
		return TodoAutomationState{}, ErrForbidden
	}
	if s == nil || s.repo == nil || s.clock == nil || s.missionExecution == nil || s.automationDiscussion == nil {
		return TodoAutomationState{}, ErrServiceDependency
	}
	req.TodoID = strings.TrimSpace(req.TodoID)
	req.PlanID = strings.TrimSpace(req.PlanID)
	req.PlanDigest = strings.TrimSpace(req.PlanDigest)
	if req.TodoID == "" || req.PlanID == "" || req.PlanDigest == "" || req.TodoVersion <= 0 || strings.TrimSpace(req.IdempotencyKey) == "" {
		return TodoAutomationState{}, ErrInvalidRequest
	}
	item, err := s.repo.GetTodoForTenant(ctx, viewer.TenantID, req.TodoID)
	if err != nil {
		return TodoAutomationState{}, err
	}
	if err := s.authorizeTodo(ctx, viewer, item, "todo.edit"); err != nil {
		return TodoAutomationState{}, err
	}
	if item.AutomationMode != TodoAutomationModeSuggest || item.Automation == nil || item.Automation.ProposalID != req.PlanID ||
		item.Automation.ApprovedPlanDigest != req.PlanDigest || item.Automation.PlanDigest != req.PlanDigest ||
		item.Automation.TodoVersion != req.TodoVersion || item.Automation.TodoVersion != item.Version {
		return TodoAutomationState{}, ErrConflict
	}
	state := *item.Automation
	switch state.Status {
	case TodoAutomationStatusExecuting, TodoAutomationStatusProvisioningWorkspace, TodoAutomationStatusWaitingRuntimeApproval, TodoAutomationStatusExecuted:
		return state, nil
	case TodoAutomationStatusApproved, TodoAutomationStatusExecutionFailed:
	default:
		return TodoAutomationState{}, ErrConflict
	}
	planCurrent, err := s.todoExecutionPlanIsCurrent(ctx, item, state)
	if err != nil {
		return TodoAutomationState{}, err
	}
	if !planCurrent {
		expectedStatus := state.Status
		state.Status = TodoAutomationStatusStale
		state.FailureReason = "Todo or approved execution resources changed after this proposal was prepared"
		state.UpdatedAt = s.clock.Now().UTC()
		updated, current, saveErr := s.repo.CompareAndSaveTodoAutomation(ctx, state, expectedStatus)
		if saveErr != nil {
			return TodoAutomationState{}, saveErr
		}
		if !updated {
			return current, ErrConflict
		}
		return TodoAutomationState{}, ErrConflict
	}
	recordTodoAutomationExecutionRequester(&state, viewer)
	expectedStatus := state.Status
	state.Status = TodoAutomationStatusExecuting
	state.UpdatedAt = s.clock.Now().UTC()
	updated, current, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, expectedStatus)
	if err != nil {
		return TodoAutomationState{}, err
	}
	if !updated {
		if current.Status == TodoAutomationStatusExecuting || current.Status == TodoAutomationStatusExecuted || current.Status == TodoAutomationStatusWaitingRuntimeApproval {
			return current, nil
		}
		return current, ErrConflict
	}
	return s.executeApprovedTodo(ctx, item, state, req.IdempotencyKey, &viewer)
}

func (s *Service) planTodo(ctx context.Context, item TodoItem) (bool, error) {
	now := s.clock.Now().UTC()
	proposalID, err := s.ids.NewIDContext()
	if err != nil {
		return false, err
	}
	state := TodoAutomationState{
		TenantID:    item.TenantID,
		ProposalID:  proposalID,
		TodoID:      item.TodoID,
		TodoVersion: item.Version,
		Status:      TodoAutomationStatusDiscussing,
		RequestedBy: todoAutomationActorID,
		ApproverID:  todoAutomationApproverID(item),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	proposalRevision := 1
	if item.Automation != nil {
		state.ConversationID = strings.TrimSpace(item.Automation.ConversationID)
		if item.Automation.Plan.ProposalRevision > 0 {
			proposalRevision = item.Automation.Plan.ProposalRevision + 1
		}
	}
	if item.DiscussionBinding != nil {
		state.ConversationID = strings.TrimSpace(item.DiscussionBinding.ConversationID)
	}
	claimed, err := s.repo.ClaimTodoAutomation(ctx, item, state, now)
	if err != nil || !claimed {
		return false, err
	}
	s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.automation.claimed", "succeeded")
	planContext, err := s.resolveTodoDevelopmentPlanContext(ctx, item)
	if err != nil {
		return true, s.failTodoPlanning(ctx, state, now, err)
	}
	viewer := todoAutomationDiscussionViewer(item, state)
	memberIDs := todoAutomationDiscussionMemberIDs(item, state, s.automationAgentIDs, s.executionSupportIDs, viewer.ViewerID)
	managedSource := todoDiscussionManagedSource(item)
	ensured, err := s.automationDiscussion.EnsureTodoConversation(ctx, viewer, discussionsession.EnsureTodoConversationCommand{
		TenantID: item.TenantID, TodoID: item.TodoID, Title: "Todo: " + item.Title,
		ExistingConversationID: state.ConversationID, InitialMemberIDs: memberIDs, ManagedSource: managedSource,
	})
	if err != nil {
		return true, s.failTodoPlanning(ctx, state, now, err)
	}
	state.ConversationID = ensured.ConversationID
	if _, err := s.repo.SaveTodoDiscussionBinding(ctx, TodoDiscussionBinding{
		TenantID: item.TenantID, TodoID: item.TodoID, ConversationID: state.ConversationID,
		ManagedSource: managedSource, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return true, s.failTodoPlanning(ctx, state, now, err)
	}
	conversation, err := s.automationDiscussion.SendTodoAutomationMessage(ctx, todoAutomationSystemViewer(item), discussionsession.SendTodoAutomationMessageCommand{
		ConversationID: state.ConversationID,
		TodoID:         item.TodoID,
		ManagedSource:  managedSource,
		Content:        todoPlanningPrompt(item, planContext),
		Mentions:       todoAutomationMentions(s.automationAgentIDs),
		IdempotencyKey: "todo-plan:" + state.ProposalID,
	})
	if err != nil {
		return true, s.failTodoPlanning(ctx, state, now, err)
	}
	state.ConversationID = conversation.ConversationID
	state.discussionMembersFingerprint = todoAutomationDiscussionMembersFingerprint(item, state, s.automationAgentIDs, s.executionSupportIDs)
	state.Proposal = todoProposalFromConversation(conversation)
	state.UpdatedAt = s.clock.Now().UTC()
	if todoProposalIsNotReady(state.Proposal) {
		state.Status = TodoAutomationStatusNotReady
		state.NextCheckAt = state.UpdatedAt.Add(todoAutomationNotReadyRetry)
	} else {
		state.Plan, err = todoExecutionPlanFromProposal(state.Proposal, item, state, proposalRevision, planContext, s.automationAgentIDs)
		if err != nil {
			return true, s.failTodoPlanning(ctx, state, now, err)
		}
		state.PlanDigest, err = todoExecutionPlanDigest(state.Plan)
		if err != nil {
			return true, s.failTodoPlanning(ctx, state, now, err)
		}
		state.ApprovalID = "todo-execution-" + state.ProposalID + "-" + state.PlanDigest[:12]
		reviewEnabled, reviewErr := s.requiresHumanReview(ctx)
		if reviewErr != nil {
			return true, s.failTodoPlanning(ctx, state, now, reviewErr)
		}
		if reviewEnabled {
			state.Status = TodoAutomationStatusPendingApproval
		} else {
			authorizationSubjectID := firstNonEmptyText(todoHILApproverID(item, state), item.CreatedBySubjectID, item.CreatedBy, item.ReviewerID)
			if authorizationSubjectID == "" {
				return true, s.failTodoPlanning(ctx, state, now, fmt.Errorf("%w: Todo execution principal is required", ErrInvalidRequest))
			}
			decisionViewer := todoHILAssignedDecisionViewer(state, authorizationSubjectID)
			recordTodoAutomationDecisionViewer(&state, decisionViewer)
			state.ApprovedPlanDigest = state.PlanDigest
			state.DecidedAt = state.UpdatedAt
			if item.AutomationMode == TodoAutomationModeSuggest {
				state.Status = TodoAutomationStatusApproved
			} else {
				recordTodoAutomationExecutionRequester(&state, decisionViewer)
				state.Status = TodoAutomationStatusExecuting
			}
		}
	}
	pendingApproval := state.Status == TodoAutomationStatusPendingApproval
	autoApproved := state.ApprovedPlanDigest != "" && !pendingApproval && state.Status != TodoAutomationStatusNotReady
	if pendingApproval && s.hilDecisionService != nil {
		linked, linkErr := s.ensureTodoHILDecision(ctx, item, state)
		if linkErr != nil {
			// Keep the legacy approval durable even when HIL is temporarily
			// unavailable. The scheduled reconciliation pass will retry it.
			if _, _, persistErr := s.repo.CompareAndSaveTodoAutomation(ctx, state, TodoAutomationStatusDiscussing); persistErr != nil {
				return true, errors.Join(linkErr, fmt.Errorf("persist pending Todo approval: %w", persistErr))
			}
			s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.hil_decision_create_failed", "failed")
			_ = s.postAutomationProposalComment(ctx, item, state)
			return true, linkErr
		}
		state = linked
	}
	updated, current, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, TodoAutomationStatusDiscussing)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return true, nil
		}
		return true, err
	}
	if !updated {
		_ = current
		return true, nil
	}
	if pendingApproval {
		s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.execution_plan.created", "succeeded")
		if strings.TrimSpace(state.HILDecisionID) != "" {
			s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.hil_decision_created", "succeeded")
		}
	}
	var commentErr error
	if autoApproved {
		commentErr = s.postAutomationDecisionComment(ctx, item, state, true)
	} else {
		commentErr = s.postAutomationProposalComment(ctx, item, state)
	}
	if commentErr != nil {
		s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.automation.proposal_comment_failed", "failed")
		return true, commentErr
	}
	if autoApproved {
		s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.automation.auto_approved", "succeeded")
		s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.execution_plan.approved", "succeeded")
		if state.Status == TodoAutomationStatusExecuting {
			_, executeErr := s.executeApprovedTodo(ctx, item, state, "auto-approved:"+state.ProposalID, nil)
			return true, executeErr
		}
	}
	action := "todo.automation.proposal_requested"
	if state.Status == TodoAutomationStatusNotReady {
		action = "todo.automation.not_ready"
	}
	s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, action, "succeeded")
	return true, nil
}

// ensureTodoHILDecision creates the canonical fixed-approval record for a
// pending Todo plan. The legacy ApprovalID remains the Todo-domain identity;
// HILDecisionID is a durable link used by the common approval UI and resolver.
func (s *Service) ensureTodoHILDecision(ctx context.Context, item TodoItem, state TodoAutomationState) (TodoAutomationState, error) {
	if s == nil || s.repo == nil {
		return state, ErrServiceDependency
	}
	if current, err := s.repo.GetTodoAutomation(ctx, state.TenantID, state.TodoID); err == nil && current.ProposalID == state.ProposalID {
		if current.HILDecisionID != "" {
			return current, nil
		}
		// Planning claims are persisted as "discussing" before the proposal
		// is complete. Keep the in-flight state supplied by the planner until
		// it has produced a pending approval, rather than treating that claim
		// as a completed non-approval state.
		if current.Status == TodoAutomationStatusPendingApproval {
			state = current
		}
	}
	if state.Status != TodoAutomationStatusPendingApproval || strings.TrimSpace(state.HILDecisionID) != "" {
		return state, nil
	}
	service := s.hilDecisionService
	if service == nil {
		return state, nil
	}
	approverID := todoHILApproverID(item, state)
	if approverID == "" || strings.TrimSpace(state.ApprovalID) == "" || strings.TrimSpace(state.PlanDigest) == "" {
		return state, ErrInvalidRequest
	}
	argsHash := "sha256:" + strings.TrimSpace(state.PlanDigest)
	result, err := service.CreateDecision(ctx, hil.CreateDecisionInput{
		TenantID:         state.TenantID,
		Requester:        hil.ActorRef{Kind: hil.ActorAgent, ID: todoAutomationActorID},
		Action:           "todo.execution_plan.approve",
		ActionTypeID:     "todo.execution_plan.approve",
		Resource:         hil.ObjectRef{Kind: "todo", ID: state.TodoID},
		ObjectRefs:       []hil.ObjectRef{{Kind: "todo", ID: state.TodoID}, {Kind: "todo_execution_plan", ID: state.ProposalID}},
		ArgsHash:         argsHash,
		Purpose:          "Approve Todo execution plan",
		RiskLevel:        todoHILRiskLevel(item),
		PreferredMode:    hil.ModeApproval,
		ApprovalRequired: true,
		SubjectRefs:      []hil.ObjectRef{{Kind: "todo", ID: state.TodoID}},
		PayloadPreview:   todoHILPayloadPreview(item, state, approverID),
		IdempotencyKey:   todoHILIdempotencyKey(state),
	})
	if err != nil {
		return state, err
	}
	if result.Decision.Route.Mode != hil.ModeApproval {
		return state, fmt.Errorf("%w: Todo execution plan must use fixed approval", ErrConflict)
	}
	state.HILDecisionID = strings.TrimSpace(result.Decision.ID)
	if state.HILDecisionID == "" {
		return state, ErrServiceDependency
	}
	state.UpdatedAt = s.clock.Now().UTC()
	return state, nil
}

func todoHILPayloadPreview(item TodoItem, state TodoAutomationState, approverID string) map[string]any {
	stepCount := len(state.Plan.Steps)
	display := map[string]any{
		"title":            strings.TrimSpace(item.Title),
		"approval_summary": fmt.Sprintf("是否同意按下列方案执行 Todo「%s」？", strings.TrimSpace(item.Title)),
		"summary":          strings.TrimSpace(state.Plan.Summary),
		"impact_scope":     fmt.Sprintf("同意后系统将按方案执行 %d 个步骤；拒绝后不会开始执行。", stepCount),
		"risk_summary":     append([]string(nil), state.Plan.RiskSummary...),
		"steps":            todoHILStepPreviews(state.Plan.Steps),
	}
	if context := strings.TrimSpace(item.Description); context != "" {
		display["business_context"] = context
	}
	if expected := strings.TrimSpace(item.AcceptanceCriteria); expected != "" {
		display["expected_result"] = expected
	} else if len(state.Plan.AcceptanceChecks) > 0 {
		display["expected_result"] = append([]string(nil), state.Plan.AcceptanceChecks...)
	}
	return map[string]any{
		"source":                      "todo_automation",
		"todo_id":                     state.TodoID,
		"todo_version":                state.TodoVersion,
		"todo_approval_id":            state.ApprovalID,
		"approval_assigned_to":        approverID,
		"plan_id":                     state.ProposalID,
		"plan_digest":                 state.PlanDigest,
		"conversation_id":             state.ConversationID,
		hil.ApprovalDisplayPayloadKey: display,
	}
}

func todoHILStepPreviews(steps []TodoExecutionPlanStep) []map[string]any {
	previews := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		preview := map[string]any{"title": strings.TrimSpace(step.Title)}
		if description := strings.TrimSpace(step.Description); description != "" {
			preview["description"] = description
		}
		previews = append(previews, preview)
	}
	return previews
}

// linkTodoHILDecision creates the durable HIL record and then links its ID to
// an already-persisted Todo approval. The two stores cannot share a transaction;
// the deterministic idempotency key makes retries converge on one HIL record.
func (s *Service) linkTodoHILDecision(ctx context.Context, item TodoItem, state TodoAutomationState) (TodoAutomationState, error) {
	linked, err := s.ensureTodoHILDecision(ctx, item, state)
	if err != nil {
		return state, err
	}
	if strings.TrimSpace(linked.HILDecisionID) == "" || strings.EqualFold(strings.TrimSpace(linked.HILDecisionID), strings.TrimSpace(state.HILDecisionID)) {
		return linked, nil
	}
	return s.repo.LinkTodoAutomationHILDecision(ctx, linked)
}

// reconcileTodoHILDecisions repairs approvals created before the shared HIL
// service was available, or after a transient HIL write failure.
func (s *Service) reconcileTodoHILDecisions(ctx context.Context, limit int) error {
	if s == nil || s.repo == nil || s.hilDecisionService == nil {
		return nil
	}
	candidates, err := s.repo.ListTodoAutomationHILCandidates(ctx, limit)
	if err != nil {
		return err
	}
	var reconcileErrors []error
	for _, item := range candidates {
		if item.Automation == nil {
			continue
		}
		state, linkErr := s.linkTodoHILDecision(ctx, item, *item.Automation)
		if linkErr != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("Todo %s: %w", item.TodoID, linkErr))
			continue
		}
		if strings.TrimSpace(state.HILDecisionID) != "" {
			s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.hil_decision_created", "succeeded")
		}
	}
	return errors.Join(reconcileErrors...)
}

// reconcileTodoHILResolutions repairs a missed post-commit observer delivery.
// It reads only HIL records already linked to an unchanged, pending Todo
// approval and delegates the actual transition to Todo's existing approval
// state machine. A pending or missing decision remains untouched.
func (s *Service) reconcileTodoHILResolutions(ctx context.Context, limit int) error {
	if s == nil || s.repo == nil || s.hilDecisionService == nil {
		return nil
	}
	reader, ok := s.hilDecisionService.(TodoHILDecisionResolutionReader)
	if !ok || reader == nil {
		return nil
	}
	candidates, err := s.repo.ListTodoAutomationHILResolutionCandidates(ctx, limit)
	if err != nil {
		return err
	}
	var reconcileErrors []error
	for _, item := range candidates {
		if item.Automation == nil {
			continue
		}
		resolution, resolutionErr := reader.GetDecisionResolution(ctx, item.TenantID, item.Automation.HILDecisionID)
		if errors.Is(resolutionErr, hil.ErrConflict) || errors.Is(resolutionErr, hil.ErrNotFound) {
			continue
		}
		if resolutionErr != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("Todo %s HIL decision %s: %w", item.TodoID, item.Automation.HILDecisionID, resolutionErr))
			continue
		}
		if _, applyErr := s.applyTodoHILResolution(ctx, resolution, true); applyErr != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("Todo %s HIL decision %s: %w", item.TodoID, item.Automation.HILDecisionID, applyErr))
		}
	}
	return errors.Join(reconcileErrors...)
}

func todoHILIdempotencyKey(state TodoAutomationState) string {
	return "todo-execution-plan:" + strings.TrimSpace(state.TenantID) + ":" + strings.TrimSpace(state.TodoID) + ":" + strings.TrimSpace(state.ProposalID) + ":" + strings.TrimSpace(state.PlanDigest)
}

func todoHILRiskLevel(item TodoItem) hil.RiskLevel {
	if item.WorkKind == TodoWorkKindDevelopment || item.Priority == TodoPriorityUrgent {
		return hil.RiskHigh
	}
	return hil.RiskMedium
}

// DecisionResolved applies a durable HIL resolution through the existing Todo
// approval state machine. A human actor must be the canonical Todo approver.
// The only non-human exception is HIL's exact expiry service identity, which
// can deny its own expired decision and is mapped to the assigned approver for
// the legacy Todo state machine.
func (s *Service) DecisionResolved(ctx context.Context, resolution hil.DecisionResolution) {
	_, _ = s.applyTodoHILResolution(ctx, resolution, false)
}

func (s *Service) applyTodoHILResolution(ctx context.Context, resolution hil.DecisionResolution, allowReplay bool) (bool, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(resolution.Decision.ID) == "" {
		return false, nil
	}
	isExpiry := todoHILExpiryResolution(resolution)
	isReplay := allowReplay && todoHILReplayResolution(resolution)
	if !todoHILTerminalResolution(resolution) || (resolution.Actor.Kind != hil.ActorHuman && !isExpiry && !isReplay) {
		return false, nil
	}
	preview := resolution.Decision.PayloadPreview
	if strings.TrimSpace(stringValue(preview, "source")) != "todo_automation" {
		return false, nil
	}
	tenantID := strings.TrimSpace(resolution.Decision.TenantID)
	todoID := strings.TrimSpace(stringValue(preview, "todo_id"))
	approvalID := strings.TrimSpace(stringValue(preview, "todo_approval_id"))
	planDigest := strings.TrimSpace(stringValue(preview, "plan_digest"))
	assignedTo := strings.TrimSpace(stringValue(preview, "approval_assigned_to"))
	if tenantID == "" || todoID == "" || approvalID == "" || planDigest == "" || assignedTo == "" ||
		(!isExpiry && !isReplay && !strings.EqualFold(assignedTo, strings.TrimSpace(resolution.Actor.ID))) {
		return false, nil
	}
	state, err := s.repo.GetTodoAutomation(ctx, tenantID, todoID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if state.Status != TodoAutomationStatusPendingApproval ||
		!strings.EqualFold(strings.TrimSpace(state.HILDecisionID), strings.TrimSpace(resolution.Decision.ID)) ||
		!strings.EqualFold(strings.TrimSpace(state.ApprovalID), approvalID) ||
		!strings.EqualFold(strings.TrimSpace(state.PlanDigest), planDigest) {
		return false, nil
	}
	item, err := s.repo.GetTodoForTenant(ctx, tenantID, todoID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if !strings.EqualFold(todoHILApproverID(item, state), assignedTo) {
		return false, nil
	}
	viewer := todoHILDecisionViewer(item, state, resolution.Actor)
	if isExpiry || isReplay {
		viewer = todoHILAssignedDecisionViewer(state, assignedTo)
	}
	actionID := "reject"
	if resolution.Decision.Status == hil.DecisionResolved && resolution.Receipt.Effect == hil.EffectAuthorize {
		actionID = "approve"
	}
	reason := strings.TrimSpace(resolution.Reason)
	if actionID == "reject" && reason == "" {
		reason = "HIL decision rejected"
	}
	_, err = s.DecideTodoAutomationApproval(ctx, viewer, DecideTodoAutomationApprovalRequest{
		ApprovalID: approvalID, ActionID: actionID, Message: reason, PlanDigest: planDigest,
		IdempotencyKey: "hil-resolution:" + strings.TrimSpace(resolution.Decision.ID),
	})
	if err != nil {
		// A concurrent legacy approval is authoritative. Avoid surfacing a
		// recoverable race as a perpetual reconciliation failure once the
		// pending row has become terminal.
		if errors.Is(err, ErrConflict) {
			if current, currentErr := s.repo.GetTodoAutomation(ctx, tenantID, todoID); currentErr == nil && current.Status != TodoAutomationStatusPendingApproval {
				return false, nil
			}
		}
		return false, err
	}
	return true, nil
}

func todoHILExpiryResolution(resolution hil.DecisionResolution) bool {
	return resolution.Actor.Kind == hil.ActorService &&
		strings.EqualFold(strings.TrimSpace(resolution.Actor.ID), todoHILExpiryActorID) &&
		resolution.Decision.Status == hil.DecisionDenied &&
		resolution.Receipt.Effect == hil.EffectDeny
}

func todoHILReplayResolution(resolution hil.DecisionResolution) bool {
	return resolution.Actor.Kind == hil.ActorService &&
		strings.EqualFold(strings.TrimSpace(resolution.Actor.ID), todoHILReplayActorID)
}

func todoHILTerminalResolution(resolution hil.DecisionResolution) bool {
	return (resolution.Decision.Status == hil.DecisionResolved && resolution.Receipt.Effect == hil.EffectAuthorize) ||
		(resolution.Decision.Status == hil.DecisionDenied && resolution.Receipt.Effect == hil.EffectDeny)
}

func todoHILDecisionViewer(_ TodoItem, state TodoAutomationState, actor hil.ActorRef) ViewerRef {
	viewerID := firstNonEmptyText(state.ApproverID, actor.ID)
	actorID := strings.TrimSpace(actor.ID)
	return ViewerRef{TenantID: state.TenantID, ViewerID: viewerID, ViewerKind: ViewerKindUser, AuthorizationSubjectID: actorID, DisplayName: viewerID}
}

func todoHILExpiryDecisionViewer(state TodoAutomationState, assignedTo string) ViewerRef {
	return todoHILAssignedDecisionViewer(state, assignedTo)
}

func todoHILAssignedDecisionViewer(state TodoAutomationState, assignedTo string) ViewerRef {
	viewerID := firstNonEmptyText(state.ApproverID, assignedTo)
	return ViewerRef{TenantID: state.TenantID, ViewerID: viewerID, ViewerKind: ViewerKindUser, AuthorizationSubjectID: assignedTo, DisplayName: viewerID}
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func (s *Service) failTodoPlanning(ctx context.Context, state TodoAutomationState, now time.Time, cause error) error {
	expectedStatus := state.Status
	state.Status = TodoAutomationStatusPlanningFailed
	state.FailureReason = cause.Error()
	state.NextCheckAt = now.Add(todoAutomationFailureRetry)
	state.UpdatedAt = s.clock.Now().UTC()
	updated, _, err := s.repo.CompareAndSaveTodoAutomation(context.WithoutCancel(ctx), state, expectedStatus)
	if updated {
		s.recordTodoAutomationAudit(ctx, state, todoAutomationActorID, "todo.automation.planning_failed", "failed")
	}
	if err != nil && !errors.Is(err, ErrConflict) {
		return errors.Join(cause, fmt.Errorf("persist Todo planning failure: %w", err))
	}
	return cause
}

func (s *Service) executeApprovedTodo(ctx context.Context, item TodoItem, state TodoAutomationState, idempotencyKey string, currentViewer *ViewerRef) (TodoAutomationState, error) {
	if s.missionExecution == nil {
		return s.failTodoExecution(ctx, state, ErrServiceDependency)
	}
	decisionViewer, err := todoAutomationDecisionViewer(state)
	if err != nil {
		return s.failTodoExecution(ctx, state, err)
	}
	executionRequester, err := s.resolveTodoAutomationExecutionRequester(ctx, state, currentViewer)
	if err != nil {
		return s.failTodoExecution(ctx, state, err)
	}
	if strings.TrimSpace(state.ExecutionRequestedBy) == "" {
		recordTodoAutomationExecutionRequester(&state, executionRequester)
	}
	if err := s.authorizeTodo(ctx, executionRequester, item, "todo.edit"); err != nil {
		return s.failTodoExecution(ctx, state, err)
	}
	if item.WorkKind == TodoWorkKindDevelopment && strings.TrimSpace(item.DevelopmentProjectID) == "" {
		return s.failTodoExecution(ctx, state, fmt.Errorf("%w: development Todo requires a development project before execution", ErrInvalidRequest))
	}
	if item.WorkKind == TodoWorkKindDevelopment {
		if s.developmentExecution == nil {
			return s.failTodoExecution(ctx, state, ErrServiceDependency)
		}
		if err := s.developmentExecution.EnsureTodoExecutionAllowed(ctx, todoDevelopmentAuthorization(item, executionRequester, todoExecutionPlanAgentIDs(state.Plan))); err != nil {
			return s.failTodoExecution(ctx, state, err)
		}
	}
	if state.PlanDigest == "" || state.ApprovedPlanDigest != state.PlanDigest || len(state.Plan.Steps) == 0 {
		return s.failTodoExecution(ctx, state, ErrConflict)
	}
	current, err := s.todoExecutionPlanIsCurrent(ctx, item, state)
	if err != nil {
		return s.failTodoExecution(ctx, state, err)
	}
	if !current {
		expectedStatus := state.Status
		state.Status = TodoAutomationStatusStale
		state.FailureReason = "Todo or approved execution resources changed after this proposal was prepared"
		state.UpdatedAt = s.clock.Now().UTC()
		updated, latest, saveErr := s.repo.CompareAndSaveTodoAutomation(ctx, state, expectedStatus)
		if saveErr != nil {
			if errors.Is(saveErr, ErrConflict) {
				return latestTodoAutomationState(ctx, s.repo, state, latest), ErrConflict
			}
			return TodoAutomationState{}, saveErr
		}
		if !updated {
			return latest, ErrConflict
		}
		return state, ErrConflict
	}
	now := s.clock.Now().UTC()
	state.Status = TodoAutomationStatusExecuting
	state.UpdatedAt = now
	membershipViewer := todoAutomationMembershipViewer(item)
	if _, err := s.automationDiscussion.ReconcileConversationMembers(ctx, membershipViewer, discussionsession.ReconcileConversationMembersCommand{
		ConversationID: state.ConversationID,
		TargetIDs:      todoAutomationDiscussionMemberIDs(item, state, s.automationAgentIDs, s.executionSupportIDs, membershipViewer.ViewerID),
		ManagedSource:  todoDiscussionManagedSource(item),
	}); err != nil {
		return s.failTodoExecution(ctx, state, err)
	}
	state.discussionMembersFingerprint = todoAutomationDiscussionMembersFingerprint(item, state, s.automationAgentIDs, s.executionSupportIDs)
	binding, acquired, err := s.repo.BeginTodoMissionExecution(ctx, item, state, state.ExecutionRequestedBy, strings.TrimSpace(idempotencyKey), now)
	if err != nil {
		return s.failTodoExecution(ctx, state, err)
	}
	if !acquired {
		if binding.Status == TodoMissionBindingStatusExecuted {
			state.Status = TodoAutomationStatusExecuted
			state.ExecutedAt = binding.UpdatedAt
			state.FailureReason = ""
			state.UpdatedAt = s.clock.Now().UTC()
			updated, current, saveErr := s.repo.CompareAndSaveTodoAutomation(context.WithoutCancel(ctx), state, TodoAutomationStatusExecuting)
			if saveErr != nil {
				if errors.Is(saveErr, ErrConflict) {
					return latestTodoAutomationState(ctx, s.repo, state, current), saveErr
				}
				return TodoAutomationState{}, saveErr
			}
			if !updated {
				return current, nil
			}
		}
		return state, nil
	}
	if binding.Version == 1 {
		s.recordTodoAutomationAudit(ctx, state, state.ExecutionRequestedBy, "todo.mission.binding_created", "succeeded")
		if binding.ExecutionAttempt > 1 {
			s.recordTodoAutomationAudit(ctx, state, state.ExecutionRequestedBy, "todo.mission.attempt_reopened", "succeeded")
		}
	}
	receipt, err := s.missionExecution.CreateMissionFromTodo(ctx, TodoMissionExecutionRequest{
		TenantID: item.TenantID, TodoID: item.TodoID, TodoTitle: item.Title, TodoDescription: item.Description,
		AcceptanceCriteria: item.AcceptanceCriteria, DevelopmentProjectID: item.DevelopmentProjectID,
		ConversationID: state.ConversationID, Plan: state.Plan, PlanDigest: state.PlanDigest,
		ExecutionAttempt: binding.ExecutionAttempt, ApprovedBy: decisionViewer.ViewerID, ApproverAuthorizationSubjectID: decisionViewer.AuthorizationSubjectID, ApproverDisplayName: firstNonEmptyText(decisionViewer.DisplayName, decisionViewer.ViewerID),
		IdempotencyKey: binding.IdempotencyKey,
	})
	if err != nil {
		binding, err = mergeTodoMissionExecutionReceipt(binding, receipt, err)
		return s.failTodoExecutionWithBinding(ctx, state, binding, err)
	}
	binding, err = mergeTodoMissionExecutionReceipt(binding, receipt, nil)
	if err != nil || strings.TrimSpace(receipt.MissionID) == "" || len(receipt.Tasks) != len(state.Plan.Steps) || len(binding.MissionTaskIDs) != len(state.Plan.Steps) {
		if err == nil {
			err = fmt.Errorf("%w: Todo Mission materialization returned an inconsistent receipt", ErrServiceDependency)
		}
		return s.failTodoExecutionWithBinding(ctx, state, binding, err)
	}
	for index := range receipt.Tasks {
		receipt.Tasks[index].MissionID = receipt.MissionID
	}
	binding.Status = TodoMissionBindingStatusMaterialized
	binding.UpdatedAt = s.clock.Now().UTC()
	if err := s.repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		return s.failTodoExecutionWithBinding(ctx, state, binding, err)
	}
	s.recordTodoAutomationAudit(ctx, state, state.ExecutionRequestedBy, "todo.mission.bound", "succeeded")
	expectedAutomationStatus := TodoAutomationStatusExecuting
	if receipt.RequiresWorkspace {
		binding.Status = TodoMissionBindingStatusProvisioning
		binding.UpdatedAt = s.clock.Now().UTC()
		if err := s.repo.SaveTodoMissionBinding(ctx, binding); err != nil {
			return s.failTodoExecutionWithBinding(ctx, state, binding, err)
		}
		state.Status = TodoAutomationStatusProvisioningWorkspace
		state.FailureReason = ""
		state.UpdatedAt = binding.UpdatedAt
		updated, current, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, TodoAutomationStatusExecuting)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return latestTodoAutomationState(ctx, s.repo, state, current), err
			}
			return TodoAutomationState{}, err
		}
		if !updated {
			return current, ErrConflict
		}
		if len(receipt.Tasks) > 0 {
			firstTask := receipt.Tasks[0]
			if _, err := s.automationDiscussion.AppendNotice(ctx, discussionsession.ViewerRef{
				ViewerID: executionRequester.AuthorizationSubjectID, ViewerKind: discussionsession.ViewerKindUser,
				DisplayName: firstNonEmptyText(executionRequester.DisplayName, executionRequester.ViewerID), TenantID: state.TenantID,
			}, discussionsession.AppendNoticeCommand{
				ConversationID: state.ConversationID, MissionID: receipt.MissionID, MissionTaskID: firstTask.TaskID,
				Content:        "开发任务已物化到 Mission，正在等待受管代码工作区准备完成。工作区就绪前不会派发 Agent 执行。",
				Metadata:       map[string]string{"kind": "todo_workspace_provisioning_required"},
				IdempotencyKey: fmt.Sprintf("todo-workspace:%s:%s", state.ProposalID, strings.TrimSpace(idempotencyKey)),
			}); err != nil {
				return s.failTodoExecutionWithBinding(ctx, state, binding, err)
			}
		}
		s.recordTodoAutomationAudit(ctx, state, state.ExecutionRequestedBy, "todo.automation.workspace_provisioning_required", "succeeded")
		if s.workspaceExecution == nil {
			return state, nil
		}
		workspaceReceipt, provisionErr := s.workspaceExecution.ProvisionTodoExecution(ctx, TodoWorkspaceExecutionRequest{
			TenantID: item.TenantID, DevelopmentProjectID: item.DevelopmentProjectID, TodoID: item.TodoID, TodoTitle: item.Title,
			PlanID: state.Plan.PlanID, PlanDigest: state.PlanDigest, ApprovalID: state.ApprovalID,
			ResourceSnapshotDigest: state.Plan.ResourceSnapshotDigest,
			RepositoryBindingID:    todoExecutionPlanPrimaryBindingID(state.Plan),
			BaseCommit:             todoExecutionPlanBaseCommit(state.Plan, todoExecutionPlanPrimaryBindingID(state.Plan)),
			AllowedFileScopes:      append([]string(nil), state.Plan.AllowedFileScopes...),
			PlannedCommands:        append([]string(nil), state.Plan.PlannedCommands...),
			NetworkMode:            state.Plan.NetworkPolicy.Mode,
			AllowedHosts:           append([]string(nil), state.Plan.NetworkPolicy.AllowedHosts...),
			RequiredSecretRefs:     append([]string(nil), state.Plan.RequiredSecretRefs...),
			MissionID:              receipt.MissionID, ConversationID: state.ConversationID, MissionTasks: append([]TodoMissionTaskExecution(nil), receipt.Tasks...),
			LeaseOwnerRunID: receipt.MissionID,
		})
		if provisionErr != nil {
			return s.failTodoExecutionWithBinding(ctx, state, binding, provisionErr)
		}
		if strings.TrimSpace(workspaceReceipt.WorkspaceID) == "" || len(workspaceReceipt.ExecutionRunIDs) != len(receipt.Tasks) {
			return s.failTodoExecutionWithBinding(ctx, state, binding, fmt.Errorf("%w: workspace provisioner returned an inconsistent receipt", ErrServiceDependency))
		}
		binding.WorkspaceID = strings.TrimSpace(workspaceReceipt.WorkspaceID)
		binding.ExecutionRunIDs = normalizeTags(workspaceReceipt.ExecutionRunIDs)
		binding.UpdatedAt = s.clock.Now().UTC()
		if err := s.repo.SaveTodoMissionBinding(ctx, binding); err != nil {
			return s.failTodoExecutionWithBinding(ctx, state, binding, err)
		}
		state.Status = TodoAutomationStatusExecuting
		state.UpdatedAt = binding.UpdatedAt
		expectedAutomationStatus = TodoAutomationStatusProvisioningWorkspace
		if len(receipt.Tasks) > 0 {
			firstTask := receipt.Tasks[0]
			if _, err := s.automationDiscussion.AppendNotice(ctx, discussionsession.ViewerRef{
				ViewerID: executionRequester.AuthorizationSubjectID, ViewerKind: discussionsession.ViewerKindUser,
				DisplayName: firstNonEmptyText(executionRequester.DisplayName, executionRequester.ViewerID), TenantID: state.TenantID,
			}, discussionsession.AppendNoticeCommand{
				ConversationID: state.ConversationID, MissionID: receipt.MissionID, MissionTaskID: firstTask.TaskID,
				Content:        "受管代码工作区已就绪，Mission 任务已绑定到隔离分支并开始执行。",
				Metadata:       map[string]string{"kind": "todo_workspace_ready", "workspace_id": binding.WorkspaceID},
				IdempotencyKey: fmt.Sprintf("todo-workspace-ready:%s:%s", state.ProposalID, strings.TrimSpace(idempotencyKey)),
			}); err != nil {
				return s.failTodoExecutionWithBinding(ctx, state, binding, err)
			}
		}
	}
	binding.Status = TodoMissionBindingStatusDispatching
	binding.UpdatedAt = s.clock.Now().UTC()
	if err := s.repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		return s.failTodoExecutionWithBinding(ctx, state, binding, err)
	}
	executionViewer := discussionsession.ViewerRef{
		ViewerID: executionRequester.AuthorizationSubjectID, ViewerKind: discussionsession.ViewerKindUser,
		DisplayName: firstNonEmptyText(executionRequester.DisplayName, executionRequester.ViewerID), TenantID: state.TenantID,
	}
	for index, task := range receipt.Tasks {
		_, appendErr := s.automationDiscussion.AppendNotice(ctx, executionViewer, discussionsession.AppendNoticeCommand{
			ConversationID: state.ConversationID, MissionID: receipt.MissionID, MissionTaskID: task.TaskID,
			Content:        todoExecutionTaskPrompt(item, state, task, index+1, len(receipt.Tasks)),
			Metadata:       map[string]string{"kind": "todo_execution_dispatched", "agent_id": strings.TrimSpace(task.AgentID)},
			IdempotencyKey: fmt.Sprintf("todo-execute:%s:%s:%d", state.ProposalID, strings.TrimSpace(idempotencyKey), index+1),
		})
		if appendErr != nil {
			return s.failTodoExecutionWithBinding(ctx, state, binding, appendErr)
		}
	}
	state.FailureReason = ""
	state.UpdatedAt = s.clock.Now().UTC()
	binding.Status = TodoMissionBindingStatusExecuting
	binding.UpdatedAt = state.UpdatedAt
	if err := s.repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		return s.failTodoExecutionWithBinding(ctx, state, binding, err)
	}
	updated, currentState, err := s.repo.CompareAndSaveTodoAutomation(ctx, state, expectedAutomationStatus)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return latestTodoAutomationState(ctx, s.repo, state, currentState), err
		}
		return TodoAutomationState{}, err
	}
	if !updated {
		return currentState, ErrConflict
	}
	s.recordTodoAutomationAudit(ctx, state, state.ExecutionRequestedBy, "todo.automation.execution_dispatched", "succeeded")
	s.recordTodoAutomationAudit(ctx, state, state.ExecutionRequestedBy, "todo.execution.started", "succeeded")
	return state, nil
}

func mergeTodoMissionExecutionReceipt(binding TodoMissionBinding, receipt TodoMissionExecutionReceipt, cause error) (TodoMissionBinding, error) {
	if conversationID := strings.TrimSpace(receipt.ConversationID); conversationID != "" && conversationID != strings.TrimSpace(binding.ConversationID) {
		return binding, errors.Join(cause, fmt.Errorf("%w: Todo Mission receipt conversation does not match the canonical discussion", ErrServiceDependency))
	}
	if missionID := strings.TrimSpace(receipt.MissionID); missionID != "" {
		if binding.MissionID != "" && binding.MissionID != missionID {
			return binding, errors.Join(cause, fmt.Errorf("%w: Todo Mission receipt changed Mission identity", ErrServiceDependency))
		}
		binding.MissionID = missionID
	}
	taskIDs := append([]string(nil), binding.MissionTaskIDs...)
	for _, task := range receipt.Tasks {
		if taskMissionID := strings.TrimSpace(task.MissionID); taskMissionID != "" && binding.MissionID != "" && taskMissionID != binding.MissionID {
			return binding, errors.Join(cause, fmt.Errorf("%w: Todo Mission task receipt has a different Mission identity", ErrServiceDependency))
		}
		if taskID := strings.TrimSpace(task.TaskID); taskID != "" {
			taskIDs = append(taskIDs, taskID)
		}
	}
	binding.MissionTaskIDs = normalizeTags(taskIDs)
	return binding, cause
}

func recordTodoAutomationDecisionViewer(state *TodoAutomationState, viewer ViewerRef) {
	if state == nil {
		return
	}
	state.DecisionBy = viewer.ViewerID
	state.decisionViewerKind = viewer.ViewerKind
	state.decisionAuthorizationSubjectID = viewer.AuthorizationSubjectID
	state.decisionPermissionScope = append([]string(nil), viewer.PermissionScope...)
	state.decisionDisplayName = firstNonEmptyText(viewer.DisplayName, viewer.ViewerID)
}

func recordTodoAutomationExecutionRequester(state *TodoAutomationState, viewer ViewerRef) {
	if state == nil {
		return
	}
	state.ExecutionRequestedBy = viewer.ViewerID
	state.executionRequesterViewerKind = viewer.ViewerKind
	state.executionRequesterAuthorizationSubjectID = viewer.AuthorizationSubjectID
	state.executionRequesterPermissionScope = append([]string(nil), viewer.PermissionScope...)
	state.executionRequesterDisplayName = firstNonEmptyText(viewer.DisplayName, viewer.ViewerID)
}

func todoAutomationDecisionViewer(state TodoAutomationState) (ViewerRef, error) {
	viewer := ViewerRef{
		TenantID: state.TenantID, ViewerID: state.DecisionBy, ViewerKind: state.decisionViewerKind,
		AuthorizationSubjectID: state.decisionAuthorizationSubjectID, DisplayName: state.decisionDisplayName,
	}
	viewer, err := normalizeViewer(viewer)
	if err != nil || viewer.ViewerKind != ViewerKindUser || strings.TrimSpace(state.decisionAuthorizationSubjectID) == "" {
		return ViewerRef{}, fmt.Errorf("%w: approved execution principal is missing or is not a user", ErrConflict)
	}
	return viewer, nil
}

func todoAutomationExecutionRequester(state TodoAutomationState) (ViewerRef, error) {
	if strings.TrimSpace(state.ExecutionRequestedBy) == "" {
		return todoAutomationDecisionViewer(state)
	}
	viewer := ViewerRef{
		TenantID: state.TenantID, ViewerID: state.ExecutionRequestedBy, ViewerKind: state.executionRequesterViewerKind,
		AuthorizationSubjectID: state.executionRequesterAuthorizationSubjectID, DisplayName: state.executionRequesterDisplayName,
	}
	viewer, err := normalizeViewer(viewer)
	if err != nil || viewer.ViewerKind != ViewerKindUser || strings.TrimSpace(state.executionRequesterAuthorizationSubjectID) == "" {
		return ViewerRef{}, fmt.Errorf("%w: execution requester principal is missing or is not a user", ErrConflict)
	}
	return viewer, nil
}

func (s *Service) resolveTodoAutomationExecutionRequester(ctx context.Context, state TodoAutomationState, currentViewer *ViewerRef) (ViewerRef, error) {
	persisted, err := todoAutomationExecutionRequester(state)
	if err != nil {
		return ViewerRef{}, err
	}
	if currentViewer != nil {
		viewer, normalizeErr := normalizeViewer(*currentViewer)
		if normalizeErr != nil || !sameTodoDecisionPrincipal(persisted, viewer) {
			return ViewerRef{}, fmt.Errorf("%w: current execution principal does not match the recorded execution requester", ErrConflict)
		}
		return viewer, nil
	}
	if s == nil || s.decisionPrincipal == nil {
		return ViewerRef{}, ErrServiceDependency
	}
	resolved, err := s.decisionPrincipal.ResolveTodoDecisionPrincipal(ctx, persisted)
	if err != nil {
		return ViewerRef{}, err
	}
	resolved, err = normalizeViewer(resolved)
	if err != nil || !sameTodoDecisionPrincipal(persisted, resolved) {
		return ViewerRef{}, fmt.Errorf("%w: resolved execution principal does not match the recorded execution requester", ErrConflict)
	}
	return resolved, nil
}

func (s *Service) resolveTodoAutomationDecisionViewer(ctx context.Context, state TodoAutomationState, currentViewer *ViewerRef) (ViewerRef, error) {
	persisted, err := todoAutomationDecisionViewer(state)
	if err != nil {
		return ViewerRef{}, err
	}
	if currentViewer != nil {
		viewer, normalizeErr := normalizeViewer(*currentViewer)
		if normalizeErr != nil || !sameTodoDecisionPrincipal(persisted, viewer) {
			return ViewerRef{}, fmt.Errorf("%w: current execution principal does not match the approved principal", ErrConflict)
		}
		return viewer, nil
	}
	if s == nil || s.decisionPrincipal == nil {
		return ViewerRef{}, ErrServiceDependency
	}
	resolved, err := s.decisionPrincipal.ResolveTodoDecisionPrincipal(ctx, persisted)
	if err != nil {
		return ViewerRef{}, err
	}
	resolved, err = normalizeViewer(resolved)
	if err != nil || !sameTodoDecisionPrincipal(persisted, resolved) {
		return ViewerRef{}, fmt.Errorf("%w: resolved execution principal does not match the approved principal", ErrConflict)
	}
	return resolved, nil
}

func sameTodoDecisionPrincipal(expected ViewerRef, actual ViewerRef) bool {
	return expected.ViewerKind == ViewerKindUser && actual.ViewerKind == ViewerKindUser &&
		strings.EqualFold(strings.TrimSpace(expected.TenantID), strings.TrimSpace(actual.TenantID)) &&
		strings.EqualFold(strings.TrimSpace(expected.ViewerID), strings.TrimSpace(actual.ViewerID)) &&
		strings.EqualFold(strings.TrimSpace(expected.AuthorizationSubjectID), strings.TrimSpace(actual.AuthorizationSubjectID))
}

func (s *Service) failTodoExecutionWithBinding(ctx context.Context, state TodoAutomationState, binding TodoMissionBinding, cause error) (TodoAutomationState, error) {
	var bindingSaveErr error
	if binding.BindingID != "" {
		binding.Status = TodoMissionBindingStatusFailed
		binding.FailureReason = cause.Error()
		binding.UpdatedAt = s.clock.Now().UTC()
		bindingSaveErr = s.repo.SaveTodoMissionBinding(context.WithoutCancel(ctx), binding)
		if bindingSaveErr == nil {
			s.recordTodoAutomationAudit(ctx, state, firstNonEmptyText(state.ExecutionRequestedBy, state.DecisionBy), "todo.mission.binding_failed", "failed")
		}
		if errors.Is(bindingSaveErr, ErrConflict) {
			current, getErr := s.repo.GetTodoMissionBinding(context.WithoutCancel(ctx), binding.TenantID, binding.TodoID)
			if getErr == nil && current.BindingID == binding.BindingID && current.leaseToken != binding.leaseToken {
				latest, stateErr := s.repo.GetTodoAutomation(context.WithoutCancel(ctx), state.TenantID, state.TodoID)
				if stateErr == nil {
					return latest, cause
				}
				return state, cause
			}
		}
	}
	failed, failErr := s.failTodoExecution(ctx, state, cause)
	if bindingSaveErr != nil {
		return failed, errors.Join(failErr, fmt.Errorf("persist Todo Mission binding failure: %w", bindingSaveErr))
	}
	return failed, failErr
}

func (s *Service) failTodoExecution(ctx context.Context, state TodoAutomationState, cause error) (TodoAutomationState, error) {
	expectedStatus := state.Status
	state.Status = TodoAutomationStatusExecutionFailed
	state.FailureReason = cause.Error()
	state.NextCheckAt = s.clock.Now().UTC().Add(todoAutomationFailureRetry)
	state.UpdatedAt = s.clock.Now().UTC()
	updated, current, err := s.repo.CompareAndSaveTodoAutomation(context.WithoutCancel(ctx), state, expectedStatus)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return latestTodoAutomationState(ctx, s.repo, state, current), cause
		}
		return state, errors.Join(cause, fmt.Errorf("persist Todo execution failure: %w", err))
	}
	if !updated {
		return current, cause
	}
	s.recordTodoAutomationAudit(ctx, state, firstNonEmptyText(state.ExecutionRequestedBy, state.DecisionBy), "todo.automation.execution_failed", "failed")
	s.recordTodoAutomationAudit(ctx, state, firstNonEmptyText(state.ExecutionRequestedBy, state.DecisionBy), "todo.execution.failed", "failed")
	return state, cause
}

func latestTodoAutomationState(ctx context.Context, repo *Repository, fallback, current TodoAutomationState) TodoAutomationState {
	if strings.TrimSpace(current.ProposalID) != "" {
		return current
	}
	if repo != nil {
		if latest, err := repo.GetTodoAutomation(context.WithoutCancel(ctx), fallback.TenantID, fallback.TodoID); err == nil {
			return latest
		}
	}
	return fallback
}

func (s *Service) postAutomationProposalComment(ctx context.Context, item TodoItem, state TodoAutomationState) error {
	status := "等待用户审批"
	if state.Status == TodoAutomationStatusNotReady {
		status = "暂不具备执行条件，将定时复查"
	}
	body := "### Agent 自动评估\n\n状态：" + status + "\n\n" + state.Proposal
	if state.ConversationID != "" {
		body += "\n\n[查看方案讨论](/discussions/" + state.ConversationID + ")"
	}
	return s.saveAutomationComment(ctx, item, body)
}

func (s *Service) postAutomationDecisionComment(ctx context.Context, item TodoItem, state TodoAutomationState, approved bool) error {
	decision := "方案已拒绝"
	if approved {
		if item.AutomationMode == TodoAutomationModeSuggest {
			decision = "方案已批准，等待用户明确开始执行"
		} else {
			decision = "方案已批准，Agent 将在原群聊中落实"
		}
	}
	body := "### Agent 方案审批\n\n" + decision
	if state.DecisionMessage != "" {
		body += "\n\n审批意见：" + state.DecisionMessage
	}
	if state.ConversationID != "" {
		body += "\n\n[查看执行群聊](/discussions/" + state.ConversationID + ")"
	}
	return s.saveAutomationComment(ctx, item, body)
}

func (s *Service) saveAutomationComment(ctx context.Context, item TodoItem, body string) error {
	commentID, err := s.ids.NewIDContext()
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	return s.repo.SaveIssueComment(ctx, IssueComment{
		TenantID:   item.TenantID,
		CommentID:  commentID,
		IssueKind:  IssueKindTodo,
		IssueID:    item.TodoID,
		Body:       body,
		AuthorID:   todoAutomationActorID,
		AuthorName: todoAutomationActorName,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}

func (s *Service) recordTodoAutomationAudit(ctx context.Context, state TodoAutomationState, actorID, action, result string) {
	if s == nil || s.repo == nil || s.automationAudit == nil {
		return
	}
	event := todoAutomationAuditEvent(s, state, actorID, action, result)
	if err := s.repo.enqueueTodoAutomationAudit(context.WithoutCancel(ctx), state, event); err != nil {
		_ = s.automationAudit.RecordAuditEvent(context.WithoutCancel(ctx), event)
		return
	}
	_ = s.flushTodoAutomationAudit(context.WithoutCancel(ctx), todoAutomationAuditIntent{
		TenantID: state.TenantID, TodoID: state.TodoID, ProposalID: state.ProposalID, Event: event,
	})
}

func (s *Service) recordRequiredTodoAutomationAudit(ctx context.Context, state TodoAutomationState, actorID, action, result string) error {
	if s == nil || s.repo == nil || s.automationAudit == nil {
		return ErrServiceDependency
	}
	event := todoAutomationAuditEvent(s, state, actorID, action, result)
	if err := s.repo.enqueueTodoAutomationAudit(context.WithoutCancel(ctx), state, event); err != nil {
		return err
	}
	return s.flushTodoAutomationAudit(context.WithoutCancel(ctx), todoAutomationAuditIntent{
		TenantID: state.TenantID, TodoID: state.TodoID, ProposalID: state.ProposalID, Event: event,
	})
}

func (s *Service) reconcileTodoAutomationAudits(ctx context.Context, limit int) error {
	if s == nil || s.repo == nil || s.automationAudit == nil {
		return nil
	}
	intents, err := s.repo.listTodoAutomationAuditIntents(ctx, limit)
	if err != nil {
		return err
	}
	var reconcileErrors []error
	for _, intent := range intents {
		if err := s.flushTodoAutomationAudit(ctx, intent); err != nil {
			reconcileErrors = append(reconcileErrors, err)
		}
	}
	return errors.Join(reconcileErrors...)
}

func (s *Service) flushTodoAutomationAudit(ctx context.Context, intent todoAutomationAuditIntent) error {
	if err := s.automationAudit.RecordAuditEvent(ctx, cloneTodoAutomationAuditEvent(intent.Event)); err != nil {
		return err
	}
	return s.repo.clearTodoAutomationAuditIntent(ctx, intent)
}

func todoAutomationAuditEvent(s *Service, state TodoAutomationState, actorID, action, result string) operations.AuditEventDTO {
	auditSeed := normalizedTenantID(state.TenantID) + "\x00" + state.TodoID + "\x00" + state.ProposalID + "\x00" + action + "\x00" + result + "\x00" + state.UpdatedAt.UTC().Format(time.RFC3339Nano)
	auditDigest := sha256.Sum256([]byte(auditSeed))
	return operations.AuditEventDTO{
		AuditID:       "todo-automation-" + hex.EncodeToString(auditDigest[:16]),
		TenantID:      normalizedTenantID(state.TenantID),
		ActorID:       actorID,
		ViewerID:      state.ApproverID,
		EntityType:    "todo_automation",
		EntityID:      state.ProposalID,
		Action:        action,
		Result:        result,
		Severity:      "info",
		CorrelationID: state.ProposalID,
		Summary:       action + " " + result,
		Metadata: map[string]string{
			"tenant_id":                 state.TenantID,
			"todo_id":                   state.TodoID,
			"todo_version":              fmt.Sprint(state.TodoVersion),
			"conversation_id":           state.ConversationID,
			"approval_id":               state.ApprovalID,
			"approved_by":               state.DecisionBy,
			"execution_requested_by":    state.ExecutionRequestedBy,
			"allow_owner_self_approval": fmt.Sprint(s != nil && s.allowOwnerSelfApproval),
		},
		OccurredAt: s.clock.Now().UTC(),
	}
}

func todoEligibleForAutomation(item TodoItem) bool {
	if item.AutomationMode != TodoAutomationModeSuggest && item.AutomationMode != TodoAutomationModeManaged {
		return false
	}
	switch item.Status {
	case TodoStatusInbox, TodoStatusPlanned, TodoStatusInProgress, TodoStatusBlocked:
		return todoAutomationApproverID(item) != "" && item.Version > 0
	default:
		return false
	}
}

func todoExecutionPrompt(item TodoItem, state TodoAutomationState) string {
	message := "用户已经批准 Todo " + item.TodoID + " 的执行方案。现在可以在此群聊中分工并落实方案；所有操作必须保持在 Todo 范围内，遵守现有权限和运行时审批。完成后更新 Todo 状态并给出可核验结果。"
	if state.DecisionMessage != "" {
		message += "\n\n审批意见：" + state.DecisionMessage
	}
	message += "\n\n已批准方案：\n" + state.Proposal
	return message
}

func todoExecutionTaskPrompt(item TodoItem, state TodoAutomationState, task TodoMissionTaskExecution, index, total int) string {
	message := fmt.Sprintf("用户已经批准 Todo %s 的执行方案。现在执行 Mission %s 的第 %d/%d 项任务。所有实际工作、结果、证据和阻塞必须归档到 MissionTask %s，并继续使用当前 Todo 群聊。", item.TodoID, task.MissionID, index, total, task.TaskID)
	if strings.TrimSpace(task.Goal) != "" {
		message += "\n\n任务目标：" + strings.TrimSpace(task.Goal)
	}
	if state.DecisionMessage != "" {
		message += "\n\n审批意见：" + state.DecisionMessage
	}
	message += "\n\n批准方案摘要：" + state.Plan.Summary
	return message
}

func todoProposalFromConversation(conversation discussionsession.ConversationDTO) string {
	start := 0
	for index := len(conversation.Messages) - 1; index >= 0; index-- {
		if strings.EqualFold(conversation.Messages[index].Role, "user") {
			start = index + 1
			break
		}
	}
	parts := make([]string, 0)
	for _, message := range conversation.Messages[start:] {
		if strings.EqualFold(message.Role, "user") || strings.TrimSpace(message.Content) == "" {
			continue
		}
		name := firstNonEmptyText(message.AuthorName, message.AuthorID, "Agent")
		parts = append(parts, "#### "+name+"\n\n"+strings.TrimSpace(message.Content))
	}
	if len(parts) == 0 {
		return "TODO_AUTOMATION_NOT_READY\n\n群聊未产生可审批方案。"
	}
	proposal := strings.Join(parts, "\n\n")
	if len(proposal) > 20000 {
		proposal = proposal[:20000]
	}
	return proposal
}

func todoProposalIsNotReady(proposal string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(proposal))
	return strings.Contains(normalized, "TODO_AUTOMATION_NOT_READY") && !strings.Contains(normalized, "TODO_AUTOMATION_PROPOSAL")
}

func todoAutomationMentions(agentIDs []string) []discussionsession.MessageMentionDTO {
	out := make([]discussionsession.MessageMentionDTO, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		if agentID = strings.TrimSpace(agentID); agentID != "" {
			out = append(out, discussionsession.MessageMentionDTO{TargetKind: "agent", TargetID: agentID})
		}
	}
	return out
}

func todoAutomationDiscussionViewer(item TodoItem, state TodoAutomationState) discussionsession.ViewerRef {
	viewerID := todoAutomationDiscussionApproverID(item, state)
	displayName := viewerID
	switch viewerID {
	case todoProposerSubjectID(item):
		displayName = firstNonEmptyText(item.ProposerName, viewerID)
	case strings.TrimSpace(item.ReviewerID):
		displayName = firstNonEmptyText(item.ReviewerName, viewerID)
	case strings.TrimSpace(item.AssigneeID):
		displayName = firstNonEmptyText(item.AssigneeName, viewerID)
	}
	return discussionsession.ViewerRef{
		ViewerID:       viewerID,
		AgentProfileID: viewerID,
		ViewerKind:     discussionsession.ViewerKindUser,
		DisplayName:    displayName,
		TenantID:       item.TenantID,
	}
}

func todoAutomationSystemViewer(item TodoItem) discussionsession.ViewerRef {
	return discussionsession.ViewerRef{
		ViewerID:    todoAutomationActorID,
		ViewerKind:  discussionsession.ViewerKindSystem,
		DisplayName: "Orrery",
		TenantID:    item.TenantID,
	}
}

func todoAutomationDiscussionApproverID(item TodoItem, state TodoAutomationState) string {
	approverID := strings.TrimSpace(state.ApproverID)
	if approverID == strings.TrimSpace(item.CreatedBy) || approverID == strings.TrimSpace(item.CreatedBySubjectID) {
		return todoProposerSubjectID(item)
	}
	return approverID
}

func todoProposerSubjectID(item TodoItem) string {
	return firstNonEmptyText(item.CreatedBySubjectID, item.CreatedBy)
}

func todoAutomationDiscussionMemberIDs(item TodoItem, state TodoAutomationState, agentIDs []string, executionSupportIDs []string, viewerID string) []string {
	values := append([]string(nil), agentIDs...)
	values = append(values, executionSupportIDs...)
	values = append(values, todoExecutionPlanAgentIDs(state.Plan)...)
	values = append(values, state.decisionAuthorizationSubjectID)
	if item.CreatedByKind != ViewerKindSystem {
		values = append(values, todoProposerSubjectID(item))
	}
	values = append(values, item.AssigneeID, item.ReviewerID)
	values = normalizeTags(values)
	viewerID = strings.TrimSpace(viewerID)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), viewerID) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func todoAutomationDiscussionMembersFingerprint(item TodoItem, state TodoAutomationState, agentIDs []string, executionSupportIDs []string) string {
	memberIDs := todoAutomationDiscussionMemberIDs(item, state, agentIDs, executionSupportIDs, "")
	for index := range memberIDs {
		memberIDs[index] = strings.ToLower(strings.TrimSpace(memberIDs[index]))
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(state.ConversationID) + "\x00" + strings.Join(memberIDs, "\x00")))
	return hex.EncodeToString(hash[:])
}

func (s *Service) syncTodoAutomationDiscussionMembers(ctx context.Context, item TodoItem) error {
	if item.Automation == nil || strings.TrimSpace(item.Automation.ConversationID) == "" {
		return nil
	}
	state := *item.Automation
	viewer := todoAutomationMembershipViewer(item)
	if _, err := s.automationDiscussion.ReconcileConversationMembers(ctx, viewer, discussionsession.ReconcileConversationMembersCommand{
		ConversationID: state.ConversationID,
		TargetIDs:      todoAutomationDiscussionMemberIDs(item, state, s.automationAgentIDs, s.executionSupportIDs, viewer.ViewerID),
		ManagedSource:  todoDiscussionManagedSource(item),
	}); err != nil {
		return err
	}
	fingerprint := todoAutomationDiscussionMembersFingerprint(item, state, s.automationAgentIDs, s.executionSupportIDs)
	return s.repo.MarkTodoAutomationDiscussionMembersSynced(ctx, state.TenantID, state.TodoID, state.ProposalID, fingerprint)
}

func todoAutomationMembershipViewer(item TodoItem) discussionsession.ViewerRef {
	return discussionsession.ViewerRef{
		ViewerID: todoAutomationActorID, ViewerKind: discussionsession.ViewerKindSystem,
		DisplayName: todoAutomationActorName, TenantID: item.TenantID,
	}
}

func todoDiscussionManagedSource(item TodoItem) string {
	return "todo:" + normalizedTenantID(item.TenantID) + "/" + strings.TrimSpace(item.TodoID)
}

func todoAutomationApprovalStatus(status TodoAutomationStatus) string {
	switch status {
	case TodoAutomationStatusPendingApproval:
		return "pending"
	case TodoAutomationStatusApproved, TodoAutomationStatusProvisioningWorkspace, TodoAutomationStatusExecuting, TodoAutomationStatusWaitingRuntimeApproval, TodoAutomationStatusExecuted, TodoAutomationStatusExecutionFailed:
		return "approved"
	case TodoAutomationStatusRejected:
		return "denied"
	case TodoAutomationStatusStale:
		return "unavailable"
	default:
		return "closed"
	}
}

func automationApprovalStatusMatches(status, requested string) bool {
	requested = strings.ToLower(strings.TrimSpace(requested))
	status = strings.ToLower(strings.TrimSpace(status))
	if requested == "rejected" {
		requested = "denied"
	}
	return status == requested
}

func todoAutomationWasApproved(status TodoAutomationStatus) bool {
	switch status {
	case TodoAutomationStatusApproved, TodoAutomationStatusProvisioningWorkspace, TodoAutomationStatusExecuting, TodoAutomationStatusWaitingRuntimeApproval, TodoAutomationStatusExecuted, TodoAutomationStatusExecutionFailed:
		return true
	default:
		return false
	}
}

func todoAutomationApproverID(item TodoItem) string {
	if item.WorkKind == TodoWorkKindDevelopment {
		return strings.TrimSpace(item.ReviewerID)
	}
	createdBy := strings.TrimSpace(item.CreatedBy)
	if createdBy != "" && (item.CreatedByKind == "" || item.CreatedByKind == ViewerKindUser || item.CreatedByKind == ViewerKindHuman) {
		return createdBy
	}
	return strings.TrimSpace(item.ReviewerID)
}

// todoHILApproverID converts Todo's legacy approver identity to the canonical
// HIL human actor identity. Todo stored creator login names before subject IDs
// were introduced, while HIL resolution uses AuthorizationSubjectID.
func todoHILApproverID(item TodoItem, state TodoAutomationState) string {
	approverID := strings.TrimSpace(state.ApproverID)
	if approverID == "" {
		return ""
	}
	if strings.EqualFold(approverID, strings.TrimSpace(item.CreatedBy)) ||
		strings.EqualFold(approverID, strings.TrimSpace(item.CreatedBySubjectID)) {
		return todoProposerSubjectID(item)
	}
	return approverID
}

func todoApprovalRequesterMatches(item TodoItem, viewer ViewerRef) bool {
	return viewerIdentityMatches(viewer, strings.TrimSpace(item.CreatedBy)) ||
		viewerIdentityMatches(viewer, strings.TrimSpace(item.CreatedBySubjectID))
}

func boolPointer(value bool) *bool {
	return &value
}
