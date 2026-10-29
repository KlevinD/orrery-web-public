package httptransport

import "time"

const (
	missionStreamActivePollInterval = 100 * time.Millisecond
	missionStreamIdlePollInterval   = time.Second
)

func nextMissionStreamPollInterval(current time.Duration, changed bool) time.Duration {
	if changed {
		return missionStreamActivePollInterval
	}
	return min(current*2, missionStreamIdlePollInterval)
}
