package platform_test

import (
	"orrery_v2/internal/platform"
)

func hasPlatformReadyCheck(report platform.ReadyReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.OK {
			return true
		}
	}
	return false
}
