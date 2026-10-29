package kernelcompat

import (
	"context"
	"errors"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type sessionEntry struct {
	cancel  context.CancelFunc
	session *Session
}

type Connection struct {
	config   Config
	mu       sync.Mutex
	closed   bool
	sessions map[agentcore.SessionID]*sessionEntry
}

func NewConnection(config Config) (*Connection, error) {
	if config.Engine == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "managed kernel engine is not configured", false)
	}
	if config.EventCapacity <= 0 {
		config.EventCapacity = 128
	}
	if config.EventCapacity < 4 || config.EventCapacity > 4096 {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel event capacity must be between 4 and 4096", false)
	}
	if config.BackpressureWait <= 0 {
		config.BackpressureWait = time.Second
	}
	if config.TurnTimeout <= 0 {
		config.TurnTimeout = 2 * time.Minute
	}
	if config.MaxTurnTimeout == 0 {
		config.MaxTurnTimeout = config.TurnTimeout
	}
	if config.MaxTurnTimeout < config.TurnTimeout || config.MaxTurnTimeout > 90*time.Minute {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "managed kernel turn timeout limit is invalid", false)
	}
	if config.CloseTimeout <= 0 {
		config.CloseTimeout = 2 * time.Second
	}
	return &Connection{config: config, sessions: make(map[agentcore.SessionID]*sessionEntry)}, nil
}

func (c *Connection) OpenSession(ctx context.Context, spec agentcore.SessionSpec, config agentcore.SessionConfig) (agentcore.SessionBackend, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if spec.Mode != agentcore.SessionOpenFresh && !capabilitiesForEngine(c.config.Engine).SupportsResume {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "managed kernel resume is not supported", false)
	}
	openCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	entry := &sessionEntry{cancel: cancel}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, transportClosed()
	}
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "managed kernel session is already open", false)
	}
	c.sessions[spec.SessionID] = entry
	c.mu.Unlock()

	runner, err := openRunner(openCtx, c.config.Engine, spec, config.Clone())
	if err == nil && capabilitiesForEngine(c.config.Engine).SupportedCommands.AnswerPermission {
		if _, ok := runner.(PermissionRunner); !ok {
			err = errors.New("managed kernel runner has no permission protocol")
		}
	}
	c.mu.Lock()
	current := c.sessions[spec.SessionID] == entry
	if err != nil || runner == nil || c.closed || !current || openCtx.Err() != nil {
		if current {
			delete(c.sessions, spec.SessionID)
		}
		c.mu.Unlock()
		if runner != nil {
			_ = closeRunner(context.Background(), runner, c.config.CloseTimeout)
		}
		if openCtx.Err() != nil {
			return nil, openCtx.Err()
		}
		failure := agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "managed kernel session could not be opened", false)
		failure.Cause = err
		if classifier, ok := c.config.Engine.(interface {
			SessionOpenFailure(error) (agentcore.RuntimeBridgeErrorCode, string)
		}); ok && err != nil {
			failure.Code, failure.Message = classifier.SessionOpenFailure(err)
		}
		return nil, failure
	}
	sessionConfig := c.config
	if timed, ok := runner.(interface{ TurnTimeout() time.Duration }); ok {
		if timeout := timed.TurnTimeout(); timeout > 0 {
			sessionConfig.TurnTimeout = min(timeout, c.config.MaxTurnTimeout)
		}
	}
	entry.session = newSession(spec.SessionID, runner, sessionConfig)
	entry.cancel = nil
	c.mu.Unlock()
	return entry.session, nil
}

func (c *Connection) CloseSession(ctx context.Context, id agentcore.SessionID) error {
	c.mu.Lock()
	entry := c.sessions[id]
	delete(c.sessions, id)
	c.mu.Unlock()
	return closeEntry(ctx, entry)
}

func (c *Connection) Capabilities(context.Context) agentcore.Capabilities {
	return capabilitiesForEngine(c.config.Engine)
}

// Unknown engines retain the default fence: an adapter alone is not proof
// that an implementation checks task scopes or disables native tool surfaces.
func (c *Connection) SupportsExplicitToolScopeFence() bool {
	fenced, ok := c.config.Engine.(interface{ SupportsExplicitToolScopeFence() bool })
	return ok && fenced.SupportsExplicitToolScopeFence()
}

func (c *Connection) Close() error {
	c.mu.Lock()
	c.closed = true
	entries := c.sessions
	c.sessions = make(map[agentcore.SessionID]*sessionEntry)
	c.mu.Unlock()
	var failures []error
	for _, entry := range entries {
		failures = append(failures, closeEntry(context.Background(), entry))
	}
	return errors.Join(failures...)
}

func closeEntry(ctx context.Context, entry *sessionEntry) error {
	if entry == nil {
		return nil
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	if entry.session != nil {
		return entry.session.Close(ctx)
	}
	return nil
}

func openRunner(ctx context.Context, engine Engine, spec agentcore.SessionSpec, config agentcore.SessionConfig) (runner Runner, err error) {
	defer func() {
		if recover() != nil {
			err = agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "managed kernel session initialization panicked", false)
		}
	}()
	return engine.OpenSession(ctx, spec, config)
}

func closeRunner(ctx context.Context, runner Runner, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	closeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "managed kernel close failed", false)
			}
		}()
		err := runner.Close(closeCtx)
		if err != nil {
			err = agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "managed kernel close failed", false)
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-closeCtx.Done():
		return closeCtx.Err()
	}
}

func transportClosed() error {
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "managed kernel session is closed", false)
}
