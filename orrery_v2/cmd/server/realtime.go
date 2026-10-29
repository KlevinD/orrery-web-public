package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge"

	developmentproject "orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	platformrealtime "orrery_v2/internal/platform/realtime"
	httptransport "orrery_v2/internal/transport/http"
)

func newProductionRealtime(ctx context.Context, rt *platform.Runtime, discussions httptransport.RealtimeDiscussionAccess, projects *developmentproject.Service) (http.Handler, func(), error) {
	store, err := newProductionRealtimeStore(ctx, rt)
	if err != nil {
		return nil, func() {}, err
	}
	return newProductionRealtimeWithStore(ctx, rt, discussions, projects, store)
}

func newProductionRealtimeStore(ctx context.Context, rt *platform.Runtime) (*platformrealtime.Store, error) {
	if rt == nil || rt.Databases == nil {
		return nil, fmt.Errorf("production runtime is not fully configured")
	}
	jobs, err := rt.Databases.Store(db.RoleJobs)
	if err != nil {
		return nil, err
	}
	return platformrealtime.NewStore(ctx, jobs, platformrealtime.StoreOptions{
		Now:         rt.Clock.Now,
		LeaseTTL:    30 * time.Second,
		MaxAttempts: 5,
	})
}

func newProductionRealtimeWithStore(ctx context.Context, rt *platform.Runtime, discussions httptransport.RealtimeDiscussionAccess, projects *developmentproject.Service, store *platformrealtime.Store) (http.Handler, func(), error) {
	if store == nil {
		return nil, func() {}, fmt.Errorf("production realtime store is not configured")
	}
	node, err := platformrealtime.NewNode(platformrealtime.NodeOptions{
		Name: "orrery-realtime",
		SubscriptionAuthorizer: httptransport.RealtimeSubscriptionAuthorizer{
			Discussions: discussions, DevelopmentProjects: projects,
		},
		Websocket: centrifuge.WebsocketConfig{
			CheckOrigin: productionRealtimeWebsocketCheckOrigin,
		},
	})
	if err != nil {
		return nil, func() {}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
			defer shutdownCancel()
			_ = node.Shutdown(shutdownCtx)
		})
	}
	if err := node.Run(runCtx); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	dispatcher, err := platformrealtime.NewDispatcher(platformrealtime.DispatcherOptions{
		Store:     store,
		Target:    node,
		WorkerID:  "orrery-realtime-dispatcher",
		BatchSize: 100,
	})
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	go runRealtimeDispatcher(runCtx, dispatcher, 250*time.Millisecond)
	return node.Handler(), cleanup, nil
}

func runRealtimeDispatcher(ctx context.Context, dispatcher *platformrealtime.Dispatcher, pollInterval time.Duration) {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		result, err := dispatcher.DispatchOnce(ctx)
		delay := time.Duration(0)
		if err != nil || result.Claimed == 0 {
			delay = pollInterval
		}
		timer.Reset(delay)
	}
}

var _ httptransport.RealtimeDiscussionAccess = (*discussionsession.Service)(nil)

func productionRealtimeWebsocketCheckOrigin(r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := parsed.Hostname()
	requestHost := hostnameFromHost(r.Host)
	if originHost == "" || requestHost == "" {
		return false
	}
	if originHost == requestHost {
		return true
	}
	return isLoopbackHost(originHost) && isLoopbackHost(requestHost)
}

func hostnameFromHost(host string) string {
	parsed, err := url.Parse("//" + host)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	default:
		return false
	}
}
