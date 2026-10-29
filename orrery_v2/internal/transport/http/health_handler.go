package httptransport

import (
	"context"
	"net/http"
)

type ReadinessProbe func(context.Context) ReadinessReport

type ReadinessReport struct {
	OK     bool
	Checks []ReadinessCheck
}

type ReadinessCheck struct {
	Name string
	OK   bool
}

type RuntimeHealthCheckDTO struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type HealthDTO struct {
	Status  string                  `json:"status"`
	Service string                  `json:"service"`
	Checks  []RuntimeHealthCheckDTO `json:"checks"`
}

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	report := ReadinessReport{
		Checks: []ReadinessCheck{{Name: "platform.health", OK: false}},
	}
	if r != nil && r.readiness != nil {
		report = r.readiness(req.Context())
	}
	checks := make([]RuntimeHealthCheckDTO, 0, len(report.Checks))
	for _, check := range report.Checks {
		status := "not_ready"
		if check.OK {
			status = "ready"
		}
		checks = append(checks, RuntimeHealthCheckDTO{Name: check.Name, Status: status})
	}
	status := "not_ready"
	code := http.StatusServiceUnavailable
	if report.OK {
		status = "ready"
		code = http.StatusOK
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, code, HealthDTO{Status: status, Service: "orrery-v2-api", Checks: checks})
}
