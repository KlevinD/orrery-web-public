package main

import (
	"context"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/logging"
)

func newProductionGatewayCleanup(
	rt *platform.Runtime,
	cancel context.CancelFunc,
	stopGateway func(context.Context),
	realtimeCleanup func(),
	closeDiscussionRuntimeRegistry func() error,
) func() {
	return func() {
		cancel()
		stopGateway(context.Background())
		realtimeCleanup()
		if err := closeDiscussionRuntimeRegistry(); err != nil {
			rt.Logger.Warn(context.Background(), "close discussion runtime registry", logging.Any("error", err))
		}
	}
}
