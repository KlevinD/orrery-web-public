package scheduler

import "strings"

// PublicAgentReport only exposes a report belonging to this execution. Native
// maintenance receipts and summaries from older runs are not Agent reports.
func PublicAgentReport(run ScheduledRun) (AgentReport, bool) {
	report := run.Result.AgentReport
	if run.TargetMode != TargetModeAgentTask || report == nil ||
		strings.TrimSpace(run.AgentID) == "" || strings.TrimSpace(run.RuntimeRunID) == "" ||
		report.AgentID != run.AgentID || report.RuntimeRunID != run.RuntimeRunID ||
		strings.TrimSpace(report.Body) == "" {
		return AgentReport{}, false
	}
	if snapshot := run.ConfigurationSnapshot; snapshot != nil &&
		(snapshot.Target.Mode != TargetModeAgentTask || snapshot.Target.AgentID != report.AgentID) {
		return AgentReport{}, false
	}
	return *report, true
}
