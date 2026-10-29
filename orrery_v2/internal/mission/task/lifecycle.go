package task

import (
	"fmt"
	"time"
)

type TaskRecord struct {
	Contract                     TaskContract
	Status                       TaskStatus
	Version                      int64
	StatusChangedAt              time.Time
	LastTransitionReason         string
	CreationIdempotencyKey       string
	CreationRequestFingerprint   string
	AssignmentIdempotencyKey     string
	AssignmentRequestFingerprint string
}

type TransitionOptions struct {
	ExpectedVersion int64
	Reason          string
	Now             time.Time
}

func (r TaskRecord) Transition(to TaskStatus, opts TransitionOptions) (TaskRecord, error) {
	if opts.ExpectedVersion != 0 && r.Version != opts.ExpectedVersion {
		return TaskRecord{}, NewConflictError("task_version_conflict", "version", "task version changed")
	}
	if !CanTransition(r.Status, to) {
		return TaskRecord{}, NewConflictError("task_status_transition_forbidden", "status", fmt.Sprintf("cannot transition task from %s to %s", r.Status, to))
	}
	updated := r
	updated.Status = to
	updated.Version++
	updated.LastTransitionReason = opts.Reason
	if opts.Now.IsZero() {
		updated.StatusChangedAt = time.Now().UTC()
	} else {
		updated.StatusChangedAt = opts.Now.UTC()
	}
	return updated, nil
}

// Reassign changes the authoritative task assignee and returns executable work
// to the assigned state. Review and terminal states require their own explicit
// workflow and cannot be bypassed by an operational reassignment.
func (r TaskRecord) Reassign(assignee TaskAssigneeRef, opts TransitionOptions) (TaskRecord, error) {
	if opts.ExpectedVersion != 0 && r.Version != opts.ExpectedVersion {
		return TaskRecord{}, NewConflictError("task_version_conflict", "version", "task version changed")
	}
	if !assignee.IsAgentAssignable() || assignee.HasToolRef() {
		return TaskRecord{}, NewConflictError("task_assignee_invalid", "assignee", "task reassignment requires exactly one Agent assignee")
	}
	switch r.Status {
	case TaskStatusDraft, TaskStatusAssigned, TaskStatusAccepted, TaskStatusInProgress,
		TaskStatusBlocked, TaskStatusRejected, TaskStatusReworkRequested:
		// These states can be returned to the assignment boundary.
	default:
		return TaskRecord{}, NewConflictError("task_reassignment_forbidden", "status", fmt.Sprintf("cannot reassign task from %s", r.Status))
	}
	updated := r
	updated.Contract.Assignee = assignee
	updated.Status = TaskStatusAssigned
	updated.Version++
	updated.LastTransitionReason = opts.Reason
	if opts.Now.IsZero() {
		updated.StatusChangedAt = time.Now().UTC()
	} else {
		updated.StatusChangedAt = opts.Now.UTC()
	}
	return updated, nil
}
