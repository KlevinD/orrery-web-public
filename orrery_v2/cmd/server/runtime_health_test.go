package main

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/testkit"
	httptransport "orrery_v2/internal/transport/http"
)

func TestRequestOnlyReadinessProbeRejectsMissingRuntime(t *testing.T) {
	report := requestOnlyReadinessProbe(nil)(context.Background())
	if report.OK {
		t.Fatalf("request-only readiness OK without runtime: %+v", report)
	}
	assertHTTPReadinessCheck(t, report, "runtime.request_only", true)
	assertHTTPReadinessCheck(t, report, "db.core", false)
}

func TestRequestOnlyReadinessProbeChecksCoreDatabaseWithoutWorkers(t *testing.T) {
	rt := testkit.NewInitializedTestRuntime(t)
	report := requestOnlyReadinessProbe(rt)(context.Background())
	if !report.OK {
		t.Fatalf("request-only readiness not OK: %+v", report)
	}
	assertHTTPReadinessCheck(t, report, "runtime.request_only", true)
	assertHTTPReadinessCheck(t, report, "db.core", true)
	for _, check := range report.Checks {
		if check.Name == "runtime.workers" {
			t.Fatalf("request-only readiness must not include worker readiness: %+v", report)
		}
	}
}

func assertHTTPReadinessCheck(t *testing.T, report httptransport.ReadinessReport, name string, want bool) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.OK != want {
				t.Fatalf("readiness check %q OK = %v, want %v; report=%+v", name, check.OK, want, report)
			}
			return
		}
	}
	t.Fatalf("readiness check %q missing: %+v", name, report)
}
