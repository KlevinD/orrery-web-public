package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/uow"
)

type qaPlanTaskLifecycle interface {
	CreateTask(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error)
	AssignTask(context.Context, missioncontrol.ViewerRef, missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error)
}

type productionQAPlanCoordinator struct {
	unit             *uow.UnitOfWork
	tasks            missiontask.Repository
	lifecycle        qaPlanTaskLifecycle
	qa               *missiontask.QAService
	responsibilities *responsibility.Repository
	refresher        missionProjectionRefresher
	relations        *authorization.RelationProjector
}

func (p productionQAPlanCoordinator) ReconcileQAPlan(ctx context.Context, assignment responsibility.AssignmentRecord, parent missiontask.TaskRecord) (responsibility.QAPlanState, error) {
	if p.unit == nil || p.tasks == nil || p.lifecycle == nil || p.qa == nil || p.responsibilities == nil ||
		strings.TrimSpace(assignment.TenantID) == "" || strings.TrimSpace(assignment.MissionID) == "" || strings.TrimSpace(assignment.WorkID) == "" {
		return "", responsibility.ErrInvalidContract
	}
	if parent.Contract.ID.String() != assignment.WorkID || parent.Contract.MissionID.String() != assignment.MissionID ||
		!parent.Contract.QAPlanRequired || parent.Contract.QAPlanCompleted {
		return "", responsibility.ErrInvalidContract
	}
	qaTaskID := missiontask.TaskID(responsibility.QAPlanTaskID(assignment.WorkID))
	child, err := p.getTask(ctx, qaTaskID)
	if errors.Is(err, missiontask.ErrNotFound) {
		if err := p.createAndAssignTask(ctx, assignment, parent, qaTaskID); err != nil {
			return "", err
		}
		return responsibility.QAPlanStatePending, nil
	}
	if err != nil {
		return "", err
	}
	if err := validateQAPlanChild(assignment, parent, child, qaTaskID); err != nil {
		return "", err
	}
	if child.Status == missiontask.TaskStatusDraft {
		if err := p.assignTask(ctx, assignment, qaTaskID); err != nil {
			return "", err
		}
		return responsibility.QAPlanStatePending, nil
	}
	if child.Status == missiontask.TaskStatusApproved {
		if err := p.completeParentPlan(ctx, assignment, parent, child); err != nil {
			return "", err
		}
		return responsibility.QAPlanStateCompleted, nil
	}
	childAssignment, err := p.responsibilities.GetAssignmentForWork(ctx, assignment.TenantID, responsibility.WorkKindMissionTask, qaTaskID.String())
	if err != nil && !errors.Is(err, responsibility.ErrNotFound) {
		return "", err
	}
	if err == nil && childAssignment.State == responsibility.AssignmentStateEscalated {
		return responsibility.QAPlanStateEscalated, nil
	}
	switch child.Status {
	case missiontask.TaskStatusAssigned, missiontask.TaskStatusAccepted, missiontask.TaskStatusInProgress,
		missiontask.TaskStatusBlocked, missiontask.TaskStatusSubmitted, missiontask.TaskStatusInReview:
		return responsibility.QAPlanStatePending, nil
	case missiontask.TaskStatusRejected, missiontask.TaskStatusReworkRequested, missiontask.TaskStatusCancelled:
		return responsibility.QAPlanStateEscalated, nil
	default:
		return "", fmt.Errorf("%w: unsupported QA plan task status %s", responsibility.ErrInvalidContract, child.Status)
	}
}

func (p productionQAPlanCoordinator) createAndAssignTask(ctx context.Context, assignment responsibility.AssignmentRecord, parent missiontask.TaskRecord, qaTaskID missiontask.TaskID) error {
	if p.relations == nil {
		return responsibility.ErrInvalidContract
	}
	if err := p.relations.WriteMissionAutomation(ctx, authorization.SubjectRef{
		Kind: string(missioncontrol.ViewerKindSystem), ID: qaPlanSystemViewerID,
	}, assignment.MissionID, assignment.TenantID); err != nil && !errors.Is(err, authorization.ErrUnsupported) {
		return err
	}
	viewer := qaPlanSystemViewer(assignment.TenantID)
	goal := qaPlanTaskGoal(parent)
	executionScopes, allowedTools, executionEnvelope, envelopeErr := qaChildExecutionContract(
		parent, assignment.TenantID, assignment.MissionID, qaTaskID.String(), defaultQAPlanAgentID, "qa_plan",
	)
	if envelopeErr != nil {
		return envelopeErr
	}
	_, err := p.lifecycle.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		RequestID: "qa-plan-create|" + assignment.WorkID, IdempotencyKey: "qa-plan-create|" + assignment.WorkID,
		MissionID: parent.Contract.MissionID, TaskID: qaTaskID, AssigneeID: defaultQAPlanAgentID,
		Goal: goal, WorkerPersonalGoal: goal, Scope: "Produce the QA contract for Mission Task " + assignment.WorkID,
		Priority:             parent.Contract.Priority,
		ExpectedDeliverables: []missioncontrol.ExpectedDeliverableCommand{{ID: "qa-plan-artifact", Title: "BDD, TDD, and evidence QA plan"}},
		AcceptanceCriteria: []string{
			"The plan includes BDD scenarios, negative cases, and acceptance rules.",
			"The plan includes a developer handoff contract and TDD or non-code verification plan.",
			"The plan identifies required evidence, must-fail conditions, and revision triggers.",
		},
		SourceKind: "qa_plan", SourceID: assignment.WorkID,
		ExecutionPermissionScopes: executionScopes, ExecutionAllowedToolIDs: allowedTools, ExecutionEnvelope: executionEnvelope,
	})
	if err != nil {
		if child, getErr := p.getTask(ctx, qaTaskID); getErr != nil || validateQAPlanChild(assignment, parent, child, qaTaskID) != nil {
			return err
		}
	}
	return p.assignTask(ctx, assignment, qaTaskID)
}

func (p productionQAPlanCoordinator) assignTask(ctx context.Context, assignment responsibility.AssignmentRecord, qaTaskID missiontask.TaskID) error {
	_, err := p.lifecycle.AssignTask(ctx, qaPlanSystemViewer(assignment.TenantID), missioncontrol.AssignTaskCommand{
		RequestID: "qa-plan-assign|" + assignment.WorkID, IdempotencyKey: "qa-plan-assign|" + assignment.WorkID,
		MissionID: missioncore.MissionID(assignment.MissionID), TaskID: qaTaskID, AssigneeID: defaultQAPlanAgentID,
		Reason: "QA plan required before worker execution",
	})
	if err == nil {
		return nil
	}
	child, getErr := p.getTask(ctx, qaTaskID)
	if getErr == nil && child.Status != missiontask.TaskStatusDraft && child.Contract.Assignee.AgentProfileRef != nil && child.Contract.Assignee.AgentProfileRef.ID == defaultQAPlanAgentID {
		return nil
	}
	return err
}

func (p productionQAPlanCoordinator) completeParentPlan(ctx context.Context, assignment responsibility.AssignmentRecord, parent, child missiontask.TaskRecord) error {
	var submission missiontask.TaskSubmission
	var deliverables []missiontask.DeliverableSubmission
	err := p.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var found bool
		var err error
		submission, found, err = p.tasks.LatestSubmission(ctx, tx, child.Contract.ID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: approved QA plan task has no submission", responsibility.ErrConflict)
		}
		deliverables, err = p.tasks.ListDeliverableSubmissions(ctx, tx, submission.ID)
		return err
	})
	if err != nil {
		return err
	}
	artifact, ok := qaPlanArtifact(deliverables)
	if !ok {
		return fmt.Errorf("%w: approved QA plan task has no traceable artifact", responsibility.ErrConflict)
	}
	creator := submission.AgentID
	if creator == "" && child.Contract.Assignee.AgentProfileRef != nil {
		creator = agentcore.AgentID(child.Contract.Assignee.AgentProfileRef.ID)
	}
	_, err = p.qa.SubmitQAPlan(ctx, parent.Contract.ID, missiontask.QAPlanRecord{
		ID: responsibility.QAPlanID(parent.Contract.ID.String()), TaskID: parent.Contract.ID, CreatedBy: creator,
		ArtifactRef: artifact, Summary: submission.Summary.Text, CompletedAt: submission.SubmittedAt,
	}, missiontask.QAPlanSubmitOptions{IdempotencyKey: "qa-plan-complete|" + assignment.WorkID + "|" + string(submission.ID)})
	if err != nil {
		return err
	}
	if p.refresher != nil {
		return p.refresher.refreshMissionProjection(ctx, assignment.TenantID, parent.Contract.MissionID)
	}
	return nil
}

func (p productionQAPlanCoordinator) getTask(ctx context.Context, taskID missiontask.TaskID) (missiontask.TaskRecord, error) {
	var out missiontask.TaskRecord
	err := p.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		out, err = p.tasks.GetTask(ctx, tx, taskID)
		return err
	})
	return out, err
}

func qaPlanSystemViewer(tenantID string) missioncontrol.ViewerRef {
	return missioncontrol.ViewerRef{
		ViewerID: qaPlanSystemViewerID, ViewerKind: missioncontrol.ViewerKindSystem, TenantID: tenantID,
		AuthorizationSubjectID: qaPlanSystemViewerID, PermissionScope: []string{"mission.read", "mission.write"},
	}
}

const qaPlanSystemViewerID = "responsibility-coordinator"

func validateQAPlanChild(assignment responsibility.AssignmentRecord, parent, child missiontask.TaskRecord, qaTaskID missiontask.TaskID) error {
	if child.Contract.ID != qaTaskID || child.Contract.MissionID != parent.Contract.MissionID || child.Contract.SourceKind != "qa_plan" || child.Contract.SourceID != assignment.WorkID ||
		child.Contract.Assignee.AgentProfileRef == nil || child.Contract.Assignee.AgentProfileRef.ID != defaultQAPlanAgentID {
		return fmt.Errorf("%w: QA plan task identity or ownership mismatch", responsibility.ErrConflict)
	}
	return nil
}

func qaPlanTaskGoal(parent missiontask.TaskRecord) string {
	parts := []string{"Create a complete BDD/TDD QA plan for Mission Task " + parent.Contract.ID.String() + ".", "Worker goal: " + strings.TrimSpace(parent.Contract.Goal)}
	if value := strings.TrimSpace(parent.Contract.Scope); value != "" {
		parts = append(parts, "Scope: "+value)
	}
	for _, criterion := range parent.Contract.AcceptanceCriteria {
		if value := strings.TrimSpace(criterion.Description); value != "" {
			parts = append(parts, "Acceptance criterion: "+value)
		}
	}
	for _, requirement := range parent.Contract.EvidenceRequirements {
		if value := strings.TrimSpace(requirement.Description); value != "" {
			parts = append(parts, "Evidence requirement: "+value)
		}
	}
	return strings.Join(parts, "\n")
}

func qaPlanArtifact(deliverables []missiontask.DeliverableSubmission) (agentcore.ArtifactRef, bool) {
	for _, item := range deliverables {
		if item.Deliverable.ArtifactRef.ID != "" && item.Deliverable.ArtifactRef.URI != "" {
			return item.Deliverable.ArtifactRef, true
		}
	}
	return agentcore.ArtifactRef{}, false
}
