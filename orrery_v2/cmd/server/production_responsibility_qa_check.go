package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/uow"
)

type productionQACheckCoordinator struct {
	unit             *uow.UnitOfWork
	tasks            missiontask.Repository
	lifecycle        qaPlanTaskLifecycle
	qa               *missiontask.QAService
	responsibilities *responsibility.Repository
	refresher        missionProjectionRefresher
	relations        *authorization.RelationProjector
}

var errInvalidQACheckVerdict = errors.New("invalid QA check verdict")

func (p productionQACheckCoordinator) ReconcileQACheck(ctx context.Context, assignment responsibility.AssignmentRecord, parent missiontask.TaskRecord) (responsibility.QACheckState, error) {
	if p.unit == nil || p.tasks == nil || p.lifecycle == nil || p.qa == nil || p.responsibilities == nil ||
		strings.TrimSpace(assignment.TenantID) == "" || strings.TrimSpace(assignment.MissionID) == "" || strings.TrimSpace(assignment.WorkID) == "" {
		return "", responsibility.ErrInvalidContract
	}
	if parent.Contract.ID.String() != assignment.WorkID || parent.Contract.MissionID.String() != assignment.MissionID ||
		parent.Status != missiontask.TaskStatusInReview || !parent.Contract.QACheckRequired {
		return "", responsibility.ErrInvalidContract
	}
	parentSubmission, found, err := p.latestSubmission(ctx, parent.Contract.ID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: parent task in review has no submission", responsibility.ErrConflict)
	}
	qaTaskID := missiontask.TaskID(responsibility.QACheckTaskID(assignment.WorkID, string(parentSubmission.ID)))
	child, err := p.getTask(ctx, qaTaskID)
	if errors.Is(err, missiontask.ErrNotFound) {
		if err := p.createAndAssignTask(ctx, assignment, parent, parentSubmission, qaTaskID); err != nil {
			return "", err
		}
		return responsibility.QACheckStatePending, nil
	}
	if err != nil {
		return "", err
	}
	if err := validateQACheckChild(assignment, parent, parentSubmission, child, qaTaskID); err != nil {
		return "", err
	}
	if child.Status == missiontask.TaskStatusDraft {
		if err := p.assignTask(ctx, assignment, qaTaskID); err != nil {
			return "", err
		}
		return responsibility.QACheckStatePending, nil
	}
	if child.Status == missiontask.TaskStatusApproved {
		if err := p.completeParentCheck(ctx, assignment, parent, parentSubmission, child); err != nil {
			if errors.Is(err, errInvalidQACheckVerdict) {
				return responsibility.QACheckStateEscalated, nil
			}
			return "", err
		}
		return responsibility.QACheckStateCompleted, nil
	}
	childAssignment, err := p.responsibilities.GetAssignmentForWork(ctx, assignment.TenantID, responsibility.WorkKindMissionTask, qaTaskID.String())
	if err != nil && !errors.Is(err, responsibility.ErrNotFound) {
		return "", err
	}
	if err == nil && childAssignment.State == responsibility.AssignmentStateEscalated {
		return responsibility.QACheckStateEscalated, nil
	}
	switch child.Status {
	case missiontask.TaskStatusAssigned, missiontask.TaskStatusAccepted, missiontask.TaskStatusInProgress,
		missiontask.TaskStatusBlocked, missiontask.TaskStatusSubmitted, missiontask.TaskStatusInReview:
		return responsibility.QACheckStatePending, nil
	case missiontask.TaskStatusRejected, missiontask.TaskStatusReworkRequested, missiontask.TaskStatusCancelled:
		return responsibility.QACheckStateEscalated, nil
	default:
		return "", fmt.Errorf("%w: unsupported QA check task status %s", responsibility.ErrInvalidContract, child.Status)
	}
}

func (p productionQACheckCoordinator) createAndAssignTask(ctx context.Context, assignment responsibility.AssignmentRecord, parent missiontask.TaskRecord, submission missiontask.TaskSubmission, qaTaskID missiontask.TaskID) error {
	if p.relations == nil {
		return responsibility.ErrInvalidContract
	}
	if err := p.relations.WriteMissionAutomation(ctx, authorization.SubjectRef{Kind: string(missioncontrol.ViewerKindSystem), ID: qaPlanSystemViewerID}, assignment.MissionID, assignment.TenantID); err != nil && !errors.Is(err, authorization.ErrUnsupported) {
		return err
	}
	goal := qaCheckTaskGoal(parent, submission)
	executionScopes, allowedTools, executionEnvelope, envelopeErr := qaChildExecutionContract(
		parent, assignment.TenantID, assignment.MissionID, qaTaskID.String(), defaultQACheckAgentID, "qa_check",
	)
	if envelopeErr != nil {
		return envelopeErr
	}
	_, err := p.lifecycle.CreateTask(ctx, qaPlanSystemViewer(assignment.TenantID), missioncontrol.CreateTaskCommand{
		RequestID: "qa-check-create|" + string(submission.ID), IdempotencyKey: "qa-check-create|" + string(submission.ID),
		MissionID: parent.Contract.MissionID, TaskID: qaTaskID, AssigneeID: defaultQACheckAgentID,
		Goal: goal, WorkerPersonalGoal: goal, Scope: "Independently verify Mission Task " + assignment.WorkID + " submission " + string(submission.ID),
		Priority:             parent.Contract.Priority,
		ExpectedDeliverables: []missioncontrol.ExpectedDeliverableCommand{{ID: "qa-check-report", Title: "Structured QA check result"}},
		AcceptanceCriteria: []string{
			"The result explicitly returns pass or fail as structured JSON.",
			"The result evaluates the parent acceptance criteria and traceable evidence.",
			"A failed result includes concrete issues and a revision instruction.",
		},
		SourceKind: "qa_check", SourceID: assignment.WorkID, SourceStepID: string(submission.ID),
		ExecutionPermissionScopes: executionScopes, ExecutionAllowedToolIDs: allowedTools, ExecutionEnvelope: executionEnvelope,
	})
	if err != nil {
		if child, getErr := p.getTask(ctx, qaTaskID); getErr != nil || validateQACheckChild(assignment, parent, submission, child, qaTaskID) != nil {
			return err
		}
	}
	return p.assignTask(ctx, assignment, qaTaskID)
}

func (p productionQACheckCoordinator) assignTask(ctx context.Context, assignment responsibility.AssignmentRecord, qaTaskID missiontask.TaskID) error {
	_, err := p.lifecycle.AssignTask(ctx, qaPlanSystemViewer(assignment.TenantID), missioncontrol.AssignTaskCommand{
		RequestID: "qa-check-assign|" + qaTaskID.String(), IdempotencyKey: "qa-check-assign|" + qaTaskID.String(),
		MissionID: missioncore.MissionID(assignment.MissionID), TaskID: qaTaskID, AssigneeID: defaultQACheckAgentID,
		Reason: "Independent QA check required before accepting the worker submission",
	})
	if err == nil {
		return nil
	}
	child, getErr := p.getTask(ctx, qaTaskID)
	if getErr == nil && child.Status != missiontask.TaskStatusDraft && child.Contract.Assignee.AgentProfileRef != nil && child.Contract.Assignee.AgentProfileRef.ID == defaultQACheckAgentID {
		return nil
	}
	return err
}

func (p productionQACheckCoordinator) completeParentCheck(ctx context.Context, assignment responsibility.AssignmentRecord, parent missiontask.TaskRecord, parentSubmission missiontask.TaskSubmission, child missiontask.TaskRecord) error {
	childSubmission, found, err := p.latestSubmission(ctx, child.Contract.ID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: approved QA check task has no submission", responsibility.ErrConflict)
	}
	verdict, err := parseQACheckVerdict(childSubmission.Summary.Text)
	if err != nil {
		return err
	}
	result := missiontask.QAResultRecord{
		TaskID: parent.Contract.ID, SubmissionID: string(parentSubmission.ID), CheckedBy: defaultQACheckAgentID,
		Status: verdict.Status, RejectionReasons: append([]string(nil), verdict.Issues...),
	}
	if verdict.Status == missiontask.QAStatusRejected {
		reason := strings.TrimSpace(strings.Join(append(append([]string(nil), verdict.Issues...), verdict.RevisionInstruction), "; "))
		if reason == "" {
			reason = "QA check rejected the submission"
		}
		for _, criterion := range parent.Contract.AcceptanceCriteria {
			result.CheckedCriteria = append(result.CheckedCriteria, missiontask.CheckedCriterion{CriterionID: criterion.ID, Status: missiontask.CheckStatusFailed, Reason: reason})
		}
		result.RejectionReasons = append(result.RejectionReasons, verdict.RevisionInstruction)
	}
	if _, err := p.qa.SubmitQAResult(ctx, parent.Contract.ID, result, missiontask.QASubmitOptions{
		IdempotencyKey: "qa-check-complete|" + assignment.WorkID + "|" + string(parentSubmission.ID),
	}); err != nil {
		return err
	}
	if p.refresher != nil {
		return p.refresher.refreshMissionProjection(ctx, assignment.TenantID, parent.Contract.MissionID)
	}
	return nil
}

func (p productionQACheckCoordinator) latestSubmission(ctx context.Context, taskID missiontask.TaskID) (missiontask.TaskSubmission, bool, error) {
	var submission missiontask.TaskSubmission
	var found bool
	err := p.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		submission, found, err = p.tasks.LatestSubmission(ctx, tx, taskID)
		return err
	})
	return submission, found, err
}

func (p productionQACheckCoordinator) getTask(ctx context.Context, taskID missiontask.TaskID) (missiontask.TaskRecord, error) {
	var task missiontask.TaskRecord
	err := p.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		task, err = p.tasks.GetTask(ctx, tx, taskID)
		return err
	})
	return task, err
}

func validateQACheckChild(assignment responsibility.AssignmentRecord, parent missiontask.TaskRecord, submission missiontask.TaskSubmission, child missiontask.TaskRecord, qaTaskID missiontask.TaskID) error {
	if child.Contract.ID != qaTaskID || child.Contract.MissionID != parent.Contract.MissionID || child.Contract.SourceKind != "qa_check" ||
		child.Contract.SourceID != assignment.WorkID || child.Contract.SourceStepID != string(submission.ID) ||
		child.Contract.Assignee.AgentProfileRef == nil || child.Contract.Assignee.AgentProfileRef.ID != defaultQACheckAgentID {
		return fmt.Errorf("%w: QA check task identity or ownership mismatch", responsibility.ErrConflict)
	}
	return nil
}

func qaCheckTaskGoal(parent missiontask.TaskRecord, submission missiontask.TaskSubmission) string {
	parts := []string{
		"Independently verify the submitted result for Mission Task " + parent.Contract.ID.String() + ".",
		"Worker goal: " + strings.TrimSpace(parent.Contract.Goal),
		"Submission ID: " + string(submission.ID),
		"Submission summary: " + strings.TrimSpace(submission.Summary.Text),
	}
	if parent.Contract.QAPlanRef != "" {
		parts = append(parts, "QA plan: "+string(parent.Contract.QAPlanRef), "QA plan artifact: "+parent.Contract.QAPlanArtifactRef.ID+" "+parent.Contract.QAPlanArtifactRef.URI)
	}
	for _, criterion := range parent.Contract.AcceptanceCriteria {
		parts = append(parts, "Acceptance criterion "+string(criterion.ID)+": "+strings.TrimSpace(criterion.Description))
	}
	for _, requirement := range parent.Contract.EvidenceRequirements {
		parts = append(parts, "Evidence requirement "+string(requirement.ID)+": "+strings.TrimSpace(requirement.Description))
	}
	parts = append(parts, `Return one JSON object in the final summary: {"status":"pass|fail","issues":["..."],"revision_instruction":"...","evidence_summary":"..."}.`)
	return strings.Join(parts, "\n")
}

type qaCheckVerdict struct {
	Status              missiontask.QAStatus
	Issues              []string
	RevisionInstruction string
}

func parseQACheckVerdict(summary string) (qaCheckVerdict, error) {
	type payload struct {
		Status              string   `json:"status"`
		Issues              []string `json:"issues"`
		RevisionInstruction string   `json:"revision_instruction"`
	}
	for _, candidate := range qaCheckJSONCandidates(summary) {
		var value payload
		if err := json.Unmarshal([]byte(candidate), &value); err != nil {
			continue
		}
		verdict := qaCheckVerdict{Issues: normalizeQACheckStrings(value.Issues), RevisionInstruction: strings.TrimSpace(value.RevisionInstruction)}
		switch strings.ToLower(strings.TrimSpace(value.Status)) {
		case "pass", "passed", "approved":
			verdict.Status = missiontask.QAStatusApproved
			return verdict, nil
		case "fail", "failed", "rejected":
			if len(verdict.Issues) == 0 || verdict.RevisionInstruction == "" {
				return qaCheckVerdict{}, fmt.Errorf("%w: failed QA check requires issues and revision_instruction", errInvalidQACheckVerdict)
			}
			verdict.Status = missiontask.QAStatusRejected
			return verdict, nil
		}
	}
	return qaCheckVerdict{}, fmt.Errorf("%w: QA check Agent did not return the required structured verdict", errInvalidQACheckVerdict)
}

func qaCheckJSONCandidates(value string) []string {
	value = strings.TrimSpace(value)
	out := []string{value}
	for _, part := range strings.Split(value, "```") {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "json"))
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			out = append(out, part)
		}
	}
	return out
}

func normalizeQACheckStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

var _ responsibility.QACheckCoordinator = productionQACheckCoordinator{}
