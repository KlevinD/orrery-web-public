package contract

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type ConcurrencyClass string

const (
	ConcurrencySummary      ConcurrencyClass = "summary"
	ConcurrencySchemaGraph  ConcurrencyClass = "schema_graph"
	ConcurrencyNeighborhood ConcurrencyClass = "neighborhood"
	ConcurrencyPlan         ConcurrencyClass = "governance_plan"
	ConcurrencyInference    ConcurrencyClass = "inference"
)

const (
	minRetryAfter = 100 * time.Millisecond
	maxRetryAfter = time.Minute
)

type ConcurrencyLimit struct {
	Global       int
	PerPrincipal int
	RetryAfter   time.Duration
}

var defaultConcurrencyLimits = map[ConcurrencyClass]ConcurrencyLimit{
	ConcurrencySummary:      {Global: 8, PerPrincipal: 2, RetryAfter: time.Second},
	ConcurrencySchemaGraph:  {Global: 4, PerPrincipal: 1, RetryAfter: time.Second},
	ConcurrencyNeighborhood: {Global: 4, PerPrincipal: 1, RetryAfter: time.Second},
	ConcurrencyPlan:         {Global: 2, PerPrincipal: 1, RetryAfter: 2 * time.Second},
	ConcurrencyInference:    {Global: 2, PerPrincipal: 1, RetryAfter: 2 * time.Second},
}

type ConcurrencyLimiter struct {
	mu        sync.Mutex
	limits    map[ConcurrencyClass]ConcurrencyLimit
	global    map[ConcurrencyClass]int
	principal map[ConcurrencyClass]map[string]int
}

func NewConcurrencyLimiter(overrides map[ConcurrencyClass]ConcurrencyLimit) (*ConcurrencyLimiter, error) {
	limits := make(map[ConcurrencyClass]ConcurrencyLimit, len(defaultConcurrencyLimits))
	for class, limit := range defaultConcurrencyLimits {
		limits[class] = limit
	}
	for class, override := range overrides {
		base, ok := defaultConcurrencyLimits[class]
		if !ok || !validConcurrencyOverride(base, override) {
			return nil, fmt.Errorf("%w: concurrency override for %s is invalid or less restrictive", ErrInvalidRequest, class)
		}
		limits[class] = mergeConcurrencyOverride(base, override)
	}
	return &ConcurrencyLimiter{limits: limits, global: map[ConcurrencyClass]int{}, principal: map[ConcurrencyClass]map[string]int{}}, nil
}

func (l *ConcurrencyLimiter) Acquire(ctx context.Context, class ConcurrencyClass, principalKey string) (func(), error) {
	principalKey = strings.TrimSpace(principalKey)
	if l == nil || ctx == nil || principalKey == "" || len(principalKey) > 256 {
		return nil, fmt.Errorf("%w: concurrency limiter and principal key are required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	limit, ok := l.limits[class]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("%w: unsupported concurrency class %s", ErrInvalidRequest, class)
	}
	principalCounts := l.principal[class]
	if principalCounts == nil {
		principalCounts = map[string]int{}
		l.principal[class] = principalCounts
	}
	if l.global[class] >= limit.Global || principalCounts[principalKey] >= limit.PerPrincipal {
		l.mu.Unlock()
		return nil, RateLimitError{RetryAfter: limit.RetryAfter}
	}
	l.global[class]++
	principalCounts[principalKey]++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.global[class] > 0 {
				l.global[class]--
			}
			principalCounts := l.principal[class]
			if principalCounts != nil && principalCounts[principalKey] > 1 {
				principalCounts[principalKey]--
			} else if principalCounts != nil {
				delete(principalCounts, principalKey)
			}
			l.mu.Unlock()
		})
	}, nil
}

func validConcurrencyOverride(base, override ConcurrencyLimit) bool {
	return (override.Global == 0 || override.Global > 0 && override.Global <= base.Global) &&
		(override.PerPrincipal == 0 || override.PerPrincipal > 0 && override.PerPrincipal <= base.PerPrincipal) &&
		(override.RetryAfter == 0 || override.RetryAfter >= minRetryAfter && override.RetryAfter <= maxRetryAfter)
}

func mergeConcurrencyOverride(base, override ConcurrencyLimit) ConcurrencyLimit {
	if override.Global > 0 {
		base.Global = override.Global
	}
	if override.PerPrincipal > 0 {
		base.PerPrincipal = override.PerPrincipal
	}
	if override.RetryAfter > 0 {
		base.RetryAfter = override.RetryAfter
	}
	return base
}
