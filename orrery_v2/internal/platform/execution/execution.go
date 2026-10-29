package execution

import (
	"context"
	"fmt"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
)

type Stores struct {
	Jobs    *db.Store
	Runtime *db.Store
}

type Config struct {
	Queue   QueueConfig
	Sweeper SweeperConfig
	Clock   clock.Clock
}

type Infra struct {
	Queue    *Queue
	Claims   *ClaimStore
	Liveness *LivenessStore
	Sweepers *SweeperRunner
}

func Open(ctx context.Context, stores Stores, cfg Config) (*Infra, error) {
	if stores.Jobs == nil {
		return nil, fmt.Errorf("execution jobs database is required")
	}
	if stores.Runtime == nil {
		return nil, fmt.Errorf("execution runtime database is required")
	}
	queueStore, err := NewQueue(ctx, stores.Jobs, cfg.Queue)
	if err != nil {
		return nil, err
	}
	claims, err := NewClaimStoreWithOptions(ctx, stores.Runtime, ClaimStoreOptions{Clock: cfg.Clock})
	if err != nil {
		return nil, err
	}
	live, err := NewLivenessStore(ctx, stores.Runtime)
	if err != nil {
		return nil, err
	}
	return &Infra{
		Queue:    queueStore,
		Claims:   claims,
		Liveness: live,
		Sweepers: NewSweeperRunnerWithConfig(cfg.Sweeper),
	}, nil
}
