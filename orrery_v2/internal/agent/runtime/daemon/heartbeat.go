package daemon

import "time"

type HeartbeatPolicy struct {
	Interval time.Duration
	TTL      time.Duration
}

func (p HeartbeatPolicy) EffectiveTTL() time.Duration {
	if p.TTL > 0 {
		return p.TTL
	}
	if p.Interval > 0 {
		return p.Interval * 3
	}
	return time.Minute
}
