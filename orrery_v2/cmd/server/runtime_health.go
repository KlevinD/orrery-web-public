package main

import (
	"context"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	httptransport "orrery_v2/internal/transport/http"
)

func productionReadinessProbe(rt *platform.Runtime) httptransport.ReadinessProbe {
	return func(ctx context.Context) httptransport.ReadinessReport {
		report := rt.Ready(ctx)
		checks := make([]httptransport.ReadinessCheck, len(report.Checks))
		for i, check := range report.Checks {
			checks[i] = httptransport.ReadinessCheck{Name: check.Name, OK: check.OK}
		}
		return httptransport.ReadinessReport{OK: report.OK, Checks: checks}
	}
}

func runtimeReadinessProbe(rt *platform.Runtime, requestOnly bool) httptransport.ReadinessProbe {
	if requestOnly {
		return requestOnlyReadinessProbe(rt)
	}
	return productionReadinessProbe(rt)
}

func requestOnlyReadinessProbe(rt *platform.Runtime) httptransport.ReadinessProbe {
	return func(ctx context.Context) httptransport.ReadinessReport {
		checks := []httptransport.ReadinessCheck{{Name: "runtime.request_only", OK: true}}
		if rt == nil || rt.Databases == nil {
			checks = append(checks, httptransport.ReadinessCheck{Name: "db.core", OK: false})
			return httptransport.ReadinessReport{OK: false, Checks: checks}
		}
		core, err := rt.Databases.Store(db.RoleCore)
		if err != nil || core == nil {
			checks = append(checks, httptransport.ReadinessCheck{Name: "db.core", OK: false})
			return httptransport.ReadinessReport{OK: false, Checks: checks}
		}
		databaseOK := core.Ping(ctx) == nil
		checks = append(checks, httptransport.ReadinessCheck{Name: "db.core", OK: databaseOK})
		return httptransport.ReadinessReport{OK: databaseOK, Checks: checks}
	}
}
