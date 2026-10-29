package main

import (
	"context"
	"testing"
	"time"
)

type blockingProductionGatewayStarter struct {
	started chan struct{}
	release chan struct{}
}

func (s blockingProductionGatewayStarter) Start(context.Context) {
	close(s.started)
	<-s.release
}

func TestProductionGatewayChannelsStartWithoutBlockingHTTPAssembly(t *testing.T) {
	starter := blockingProductionGatewayStarter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	startedAt := time.Now()
	startProductionGatewayChannels(context.Background(), starter)
	if elapsed := time.Since(startedAt); elapsed > 50*time.Millisecond {
		t.Fatalf("gateway startup blocked HTTP assembly for %s", elapsed)
	}
	select {
	case <-starter.started:
	case <-time.After(time.Second):
		t.Fatal("gateway startup was not launched")
	}
	close(starter.release)
}
