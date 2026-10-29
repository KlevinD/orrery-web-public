package lifecycle

import "time"

type FailurePolicy string

const (
	FailurePolicyStop     FailurePolicy = "Stop"
	FailurePolicyContinue FailurePolicy = "Continue"
	FailurePolicyDegrade  FailurePolicy = "Degrade"
	FailurePolicyRetry    FailurePolicy = "Retry"
)

type RetryPolicy struct {
	MaxAttempts int
	Backoff     time.Duration
	Then        FailurePolicy
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	if p.Then == "" || p.Then == FailurePolicyRetry {
		p.Then = FailurePolicyStop
	}
	return p
}
