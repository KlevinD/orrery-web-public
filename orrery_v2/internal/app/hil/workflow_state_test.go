package hil

import "testing"

func TestResolveApprovalStagePolicies(t *testing.T) {
	tests := []struct {
		name      string
		strategy  ApprovalStrategy
		threshold int
		decisions []WorkflowTaskStatus
		want      StageOutcome
	}{
		{name: "single waits", strategy: ApprovalSingle, decisions: []WorkflowTaskStatus{TaskPending}, want: StageOpen},
		{name: "single approve", strategy: ApprovalSingle, decisions: []WorkflowTaskStatus{TaskApproved}, want: StagePassed},
		{name: "single reject", strategy: ApprovalSingle, decisions: []WorkflowTaskStatus{TaskRejected}, want: StageFailed},
		{name: "any waits", strategy: ApprovalAny, decisions: []WorkflowTaskStatus{TaskPending, TaskPending}, want: StageOpen},
		{name: "any first approve", strategy: ApprovalAny, decisions: []WorkflowTaskStatus{TaskApproved, TaskPending}, want: StagePassed},
		{name: "any first reject", strategy: ApprovalAny, decisions: []WorkflowTaskStatus{TaskRejected, TaskPending}, want: StageFailed},
		{name: "all waits", strategy: ApprovalAll, decisions: []WorkflowTaskStatus{TaskApproved, TaskPending}, want: StageOpen},
		{name: "all approve", strategy: ApprovalAll, decisions: []WorkflowTaskStatus{TaskApproved, TaskApproved}, want: StagePassed},
		{name: "all first reject", strategy: ApprovalAll, decisions: []WorkflowTaskStatus{TaskApproved, TaskRejected}, want: StageFailed},
		{name: "threshold waits", strategy: ApprovalThreshold, threshold: 2, decisions: []WorkflowTaskStatus{TaskApproved, TaskPending, TaskRejected}, want: StageOpen},
		{name: "threshold passes", strategy: ApprovalThreshold, threshold: 2, decisions: []WorkflowTaskStatus{TaskApproved, TaskApproved, TaskPending}, want: StagePassed},
		{name: "threshold impossible", strategy: ApprovalThreshold, threshold: 2, decisions: []WorkflowTaskStatus{TaskRejected, TaskRejected, TaskPending}, want: StageFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ResolveApprovalStage(test.strategy, test.threshold, test.decisions); got != test.want {
				t.Fatalf("ResolveApprovalStage() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestResolveApprovalStageReturnsOpenForInvalidInput(t *testing.T) {
	for _, test := range []struct {
		strategy  ApprovalStrategy
		threshold int
		decisions []WorkflowTaskStatus
	}{
		{strategy: "unknown", decisions: []WorkflowTaskStatus{TaskApproved}},
		{strategy: ApprovalSingle, decisions: nil},
		{strategy: ApprovalSingle, decisions: []WorkflowTaskStatus{TaskApproved, TaskPending}},
		{strategy: ApprovalThreshold, threshold: 0, decisions: []WorkflowTaskStatus{TaskApproved}},
	} {
		if got := ResolveApprovalStage(test.strategy, test.threshold, test.decisions); got != StageOpen {
			t.Fatalf("ResolveApprovalStage(%s, %d, %v) = %s, want open", test.strategy, test.threshold, test.decisions, got)
		}
	}
}
