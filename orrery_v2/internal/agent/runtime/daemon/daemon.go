package daemon

import (
	"context"
	"time"
)

type Daemon struct {
	Worker       *Worker
	PollInterval time.Duration
}

func (d Daemon) Run(ctx context.Context) error {
	interval := d.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		claimed, err := d.Worker.RunOnce(ctx)
		if err != nil {
			return err
		}
		if !claimed {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
}
