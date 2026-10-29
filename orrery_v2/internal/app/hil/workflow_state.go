package hil

func ResolveApprovalStage(strategy ApprovalStrategy, threshold int, decisions []WorkflowTaskStatus) StageOutcome {
	if len(decisions) == 0 {
		return StageOpen
	}
	switch strategy {
	case ApprovalSingle:
		if len(decisions) != 1 {
			return StageOpen
		}
		return terminalApprovalTaskOutcome(decisions[0])
	case ApprovalAny:
		for _, decision := range decisions {
			if outcome := terminalApprovalTaskOutcome(decision); outcome != StageOpen {
				return outcome
			}
		}
		return StageOpen
	case ApprovalAll:
		approved := 0
		for _, decision := range decisions {
			if decision == TaskRejected {
				return StageFailed
			}
			if decision == TaskApproved {
				approved++
			}
		}
		if approved == len(decisions) {
			return StagePassed
		}
		return StageOpen
	case ApprovalThreshold:
		if threshold <= 0 || threshold > len(decisions) {
			return StageOpen
		}
		approved := 0
		available := 0
		for _, decision := range decisions {
			switch decision {
			case TaskApproved:
				approved++
			case TaskPending, TaskScheduled:
				available++
			}
		}
		if approved >= threshold {
			return StagePassed
		}
		if approved+available < threshold {
			return StageFailed
		}
		return StageOpen
	default:
		return StageOpen
	}
}

func terminalApprovalTaskOutcome(status WorkflowTaskStatus) StageOutcome {
	switch status {
	case TaskApproved:
		return StagePassed
	case TaskRejected:
		return StageFailed
	default:
		return StageOpen
	}
}
