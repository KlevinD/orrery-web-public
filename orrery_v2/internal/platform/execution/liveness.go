package execution

import (
	"context"
	"time"

	"orrery_v2/internal/platform/logging"
)

type LivenessBackend interface {
	Touch(ctx context.Context, runtimeID string, ttl time.Duration, now time.Time)
	IsAliveBatch(ctx context.Context, runtimeIDs []string, now time.Time) map[string]bool
	Forget(ctx context.Context, runtimeID string)
}

type Fallback struct {
	primary   LivenessBackend
	secondary LivenessBackend
	logger    logging.Logger
}

func NewFallback(primary, secondary LivenessBackend) *Fallback {
	return &Fallback{primary: primary, secondary: secondary}
}

func (f *Fallback) WithLogger(logger logging.Logger) *Fallback {
	f.logger = logger
	return f
}

func (f *Fallback) Touch(ctx context.Context, runtimeID string, ttl time.Duration, now time.Time) {
	f.primary.Touch(ctx, runtimeID, ttl, now)
	f.secondary.Touch(ctx, runtimeID, ttl, now)
}

func (f *Fallback) IsAliveBatch(ctx context.Context, runtimeIDs []string, now time.Time) map[string]bool {
	alive := f.primary.IsAliveBatch(ctx, runtimeIDs, now)
	for _, id := range runtimeIDs {
		if alive[id] {
			return alive
		}
	}
	fallbackAlive := f.secondary.IsAliveBatch(ctx, runtimeIDs, now)
	for _, id := range runtimeIDs {
		if fallbackAlive[id] {
			f.logFallback(ctx)
			break
		}
	}
	return fallbackAlive
}

func (f *Fallback) Forget(ctx context.Context, runtimeID string) {
	f.primary.Forget(ctx, runtimeID)
	f.secondary.Forget(ctx, runtimeID)
}

func (f *Fallback) logFallback(ctx context.Context) {
	if f.logger == nil {
		return
	}
	f.logger.Warn(ctx, "liveness primary unavailable, using secondary",
		logging.String("operation", "liveness.fallback"),
		logging.String("error_code", "liveness_primary_unavailable"),
	)
}
