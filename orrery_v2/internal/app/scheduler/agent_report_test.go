package scheduler

import (
	"encoding/json"
	"testing"
)

func TestPublicAgentReportRequiresExecutingIdentityAndSnapshot(t *testing.T) {
	base := ScheduledRun{AgentID: "agent-a", RuntimeRunID: "runtime-a", TargetMode: TargetModeAgentTask, Result: RunResult{Summary: "internal receipt", AgentReport: &AgentReport{AgentID: "agent-a", RuntimeRunID: "runtime-a", Body: "Original Agent report"}}}
	for name, change := range map[string]func(*ScheduledRun){
		"missing": func(r *ScheduledRun) { r.Result.AgentReport = nil },
		"native":  func(r *ScheduledRun) { r.TargetMode = TargetModeResponsibilityReconcile },
		"agent":   func(r *ScheduledRun) { r.AgentID = "agent-b" },
		"runtime": func(r *ScheduledRun) { r.RuntimeRunID = "runtime-b" },
		"snapshot": func(r *ScheduledRun) {
			r.ConfigurationSnapshot = &ScheduledTask{Target: Target{Mode: TargetModeAgentTask, AgentID: "agent-b"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			change(&r)
			if _, ok := PublicAgentReport(r); ok {
				t.Fatal("foreign or unowned report accepted")
			}
		})
	}
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var recovered ScheduledRun
	if err := json.Unmarshal(encoded, &recovered); err != nil {
		t.Fatal(err)
	}
	if report, ok := PublicAgentReport(recovered); !ok || report != *base.Result.AgentReport {
		t.Fatalf("report lost: %+v %v", report, ok)
	}
}
