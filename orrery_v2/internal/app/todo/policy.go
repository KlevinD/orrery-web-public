package todo

import "strings"

func normalizeTodoStatus(status TodoStatus) TodoStatus {
	return TodoStatus(strings.TrimSpace(string(status)))
}

func isTodoStatus(status TodoStatus) bool {
	switch status {
	case TodoStatusInbox, TodoStatusPlanned, TodoStatusInProgress, TodoStatusBlocked, TodoStatusReview, TodoStatusDone, TodoStatusCanceled:
		return true
	default:
		return false
	}
}

func isTodoPriority(priority TodoPriority) bool {
	switch priority {
	case TodoPriorityLow, TodoPriorityMedium, TodoPriorityHigh, TodoPriorityUrgent:
		return true
	default:
		return false
	}
}

func normalizeTodoWorkKind(value TodoWorkKind) TodoWorkKind {
	return TodoWorkKind(strings.ToLower(strings.TrimSpace(string(value))))
}

func isTodoWorkKind(value TodoWorkKind) bool {
	return value == TodoWorkKindGeneral || value == TodoWorkKindDevelopment
}

func normalizeTodoAutomationMode(value TodoAutomationMode) TodoAutomationMode {
	return TodoAutomationMode(strings.ToLower(strings.TrimSpace(string(value))))
}

func isTodoAutomationMode(value TodoAutomationMode) bool {
	return value == TodoAutomationModeManual || value == TodoAutomationModeSuggest || value == TodoAutomationModeManaged
}

func isAssigneeKind(kind AssigneeKind) bool {
	return kind == "" || kind == AssigneeKindHuman || kind == AssigneeKindAgent
}

func isTerminalTodoStatus(status TodoStatus) bool {
	return status == TodoStatusDone || status == TodoStatusCanceled
}

func isInitialTodoStatus(status TodoStatus) bool {
	return status == TodoStatusInbox || status == TodoStatusPlanned || status == TodoStatusInProgress
}

func canTransitionTodo(from, to TodoStatus) bool {
	if from == to {
		return true
	}
	allowed := map[TodoStatus]map[TodoStatus]bool{
		TodoStatusInbox: {
			TodoStatusPlanned: true, TodoStatusInProgress: true, TodoStatusCanceled: true,
		},
		TodoStatusPlanned: {
			TodoStatusInProgress: true, TodoStatusBlocked: true, TodoStatusDone: true, TodoStatusCanceled: true,
		},
		TodoStatusInProgress: {
			TodoStatusBlocked: true, TodoStatusReview: true, TodoStatusDone: true, TodoStatusCanceled: true,
		},
		TodoStatusBlocked: {
			TodoStatusPlanned: true, TodoStatusInProgress: true, TodoStatusCanceled: true,
		},
		TodoStatusReview: {
			TodoStatusInProgress: true, TodoStatusBlocked: true, TodoStatusDone: true, TodoStatusCanceled: true,
		},
		TodoStatusDone: {
			TodoStatusPlanned: true,
		},
		TodoStatusCanceled: {
			TodoStatusInbox: true,
		},
	}
	return allowed[from][to]
}

func normalizeIdeaStatus(status IdeaStatus) IdeaStatus {
	return IdeaStatus(strings.TrimSpace(string(status)))
}

func isIdeaStatus(status IdeaStatus) bool {
	switch status {
	case IdeaStatusDraft, IdeaStatusSubmitted, IdeaStatusUnderReview, IdeaStatusAccepted, IdeaStatusExperiment, IdeaStatusConverted, IdeaStatusRejected, IdeaStatusArchived:
		return true
	default:
		return false
	}
}

func isIdeaVisibility(value IdeaVisibility) bool {
	return value == IdeaVisibilityOrganization || value == IdeaVisibilityDepartment || value == IdeaVisibilityPrivate
}

func isIdeaImpact(value IdeaImpact) bool {
	return value == IdeaImpactLow || value == IdeaImpactMedium || value == IdeaImpactHigh
}

func isIdeaEffort(value IdeaEffort) bool {
	return value == IdeaEffortSmall || value == IdeaEffortMedium || value == IdeaEffortLarge
}

func canTransitionIdea(from, to IdeaStatus) bool {
	if from == to {
		return true
	}
	allowed := map[IdeaStatus]map[IdeaStatus]bool{
		IdeaStatusDraft: {
			IdeaStatusSubmitted: true, IdeaStatusArchived: true,
		},
		IdeaStatusSubmitted: {
			IdeaStatusUnderReview: true, IdeaStatusAccepted: true, IdeaStatusExperiment: true, IdeaStatusRejected: true, IdeaStatusArchived: true,
		},
		IdeaStatusUnderReview: {
			IdeaStatusSubmitted: true, IdeaStatusAccepted: true, IdeaStatusExperiment: true, IdeaStatusRejected: true, IdeaStatusArchived: true,
		},
		IdeaStatusAccepted: {
			IdeaStatusExperiment: true, IdeaStatusRejected: true, IdeaStatusArchived: true,
		},
		IdeaStatusExperiment: {
			IdeaStatusAccepted: true, IdeaStatusRejected: true, IdeaStatusArchived: true,
		},
		IdeaStatusRejected: {
			IdeaStatusSubmitted: true, IdeaStatusArchived: true,
		},
		IdeaStatusConverted: {},
		IdeaStatusArchived: {
			IdeaStatusSubmitted: true,
		},
	}
	return allowed[from][to]
}

func ideaTransitionRequiresDecision(status IdeaStatus) bool {
	return status == IdeaStatusAccepted || status == IdeaStatusRejected
}

func isConvertibleIdeaStatus(status IdeaStatus) bool {
	return status == IdeaStatusAccepted || status == IdeaStatusExperiment
}

func isIssueKind(kind IssueKind) bool {
	return kind == IssueKindTodo || kind == IssueKindIdea
}
