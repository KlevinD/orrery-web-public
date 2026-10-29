package realtime

import (
	"context"
	"fmt"
)

type PublishTarget interface {
	Publish(context.Context, Envelope) error
}

type DispatcherOptions struct {
	Store     *Store
	Target    PublishTarget
	WorkerID  string
	BatchSize int
}

type Dispatcher struct {
	store     *Store
	target    PublishTarget
	workerID  string
	batchSize int
}

type DispatchResult struct {
	Claimed   int
	Published int
	Failed    int
	Recovered int
}

func NewDispatcher(opts DispatcherOptions) (*Dispatcher, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("realtime dispatcher requires outbox store")
	}
	if opts.Target == nil {
		return nil, fmt.Errorf("realtime dispatcher requires publish target")
	}
	if opts.WorkerID == "" {
		return nil, fmt.Errorf("realtime dispatcher worker id is required")
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	return &Dispatcher{
		store:     opts.Store,
		target:    opts.Target,
		workerID:  opts.WorkerID,
		batchSize: batchSize,
	}, nil
}

func (d *Dispatcher) DispatchOnce(ctx context.Context) (DispatchResult, error) {
	if err := ctx.Err(); err != nil {
		return DispatchResult{}, err
	}
	result := DispatchResult{}
	recovered, err := d.store.RecoverExpired(ctx, d.batchSize)
	if err != nil {
		return DispatchResult{}, err
	}
	result.Recovered = recovered
	events, err := d.store.Claim(ctx, d.workerID, d.batchSize)
	if err != nil {
		return DispatchResult{}, err
	}
	result.Claimed = len(events)
	for _, event := range events {
		if err := d.target.Publish(ctx, event.Envelope); err != nil {
			if markErr := d.store.MarkFailed(ctx, event.ID, d.workerID, err.Error()); markErr != nil {
				return result, markErr
			}
			result.Failed++
			continue
		}
		if err := d.store.MarkPublished(ctx, event.ID, d.workerID); err != nil {
			return result, err
		}
		result.Published++
	}
	return result, nil
}
