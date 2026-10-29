// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package session

import (
	"context"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

// Reference: aionui-session/backend/suspend.rs@v0.1.52.

// SuspendBinding owns every process-bound resource for one live provider
// binding. Stop must terminate and join its reader before it returns.
type SuspendBinding interface {
	Stop(context.Context) error
}

type WakeBinding func(context.Context) (SuspendBinding, error)

type SuspendController struct {
	mu           sync.Mutex
	binding      SuspendBinding
	idleTTL      time.Duration
	lastActivity time.Time
	closed       bool
}

func NewSuspendController(binding SuspendBinding, idleTTL time.Duration, now time.Time) (*SuspendController, error) {
	if idleTTL < 0 {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "idle suspend ttl cannot be negative", false)
	}
	if now.IsZero() {
		now = time.Now()
	}
	return &SuspendController{binding: binding, idleTTL: idleTTL, lastActivity: now}, nil
}

func (c *SuspendController) IdleTTL() time.Duration { return c.idleTTL }

func (c *SuspendController) LastActivity() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastActivity
}

func (c *SuspendController) IsActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.binding != nil
}

// EnsureAwake and SuspendIfIdle share the same lock, making wake and suspend a
// single lifecycle actor. A failed wake leaves the controller dormant.
func (c *SuspendController) EnsureAwake(ctx context.Context, now time.Time, wake WakeBinding) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "suspend controller is closed", false)
	}
	if c.binding == nil {
		if wake == nil {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "provider binding is dormant", true)
		}
		binding, err := wake(ctx)
		if err != nil {
			return err
		}
		if binding == nil {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "wake returned no provider binding", false)
		}
		c.binding = binding
	}
	c.lastActivity = now
	return nil
}

// SuspendIfIdle is disabled when idleTTL is zero, never stops an active turn,
// and holds the lifecycle lock through Stop so a concurrent wake cannot race it.
func (c *SuspendController) SuspendIfIdle(ctx context.Context, now time.Time, turnActive bool) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.idleTTL == 0 || turnActive || c.binding == nil || now.Sub(c.lastActivity) < c.idleTTL {
		return false, nil
	}
	binding := c.binding
	c.binding = nil
	if err := binding.Stop(ctx); err != nil {
		return true, err
	}
	return true, nil
}

// RunIdleTimer checks until ctx is canceled. A nil callback is allowed. The
// current turn state must come from the orchestrator snapshot supplied by caller.
func (c *SuspendController) RunIdleTimer(ctx context.Context, interval time.Duration, now func() time.Time, turnActive func() bool, onSuspend func()) {
	if c.idleTTL == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = c.idleTTL / 2
		if interval <= 0 {
			interval = time.Millisecond
		}
	}
	if now == nil {
		now = time.Now
	}
	if turnActive == nil {
		turnActive = func() bool { return false }
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			suspended, _ := c.SuspendIfIdle(ctx, now(), turnActive())
			if suspended && onSuspend != nil {
				onSuspend()
			}
		}
	}
}

func (c *SuspendController) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	binding := c.binding
	c.binding = nil
	if binding != nil {
		return binding.Stop(ctx)
	}
	return nil
}
