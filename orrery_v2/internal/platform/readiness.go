package platform

import (
	"context"

	"orrery_v2/internal/platform/health"
)

type ReadyReport struct {
	OK     bool
	Checks []ReadyCheck
}

type ReadyCheck struct {
	Name    string
	OK      bool
	Message string
}

func (r *Runtime) Ready(ctx context.Context) ReadyReport {
	if r == nil || r.Health == nil {
		return ReadyReport{OK: false, Checks: []ReadyCheck{{Name: "platform.health", OK: false, Message: "health checker is not configured"}}}
	}
	return readyReportFromHealth(r.Health.Ready(ctx))
}

func readyReportFromHealth(report health.Report) ReadyReport {
	checks := make([]ReadyCheck, len(report.Checks))
	for i, check := range report.Checks {
		checks[i] = ReadyCheck{Name: check.Name, OK: check.OK, Message: check.Message}
	}
	return ReadyReport{OK: report.OK, Checks: checks}
}
