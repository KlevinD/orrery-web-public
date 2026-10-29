package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
)

// ConnectionRegistry is the canonical provider registry.
type ConnectionRegistry struct {
	mu          sync.RWMutex
	connections map[agentcore.RuntimeKind]agentcore.BackendConnection
	unavailable map[agentcore.RuntimeKind]string
	workspaces  *WorkspaceBroker
	authorizer  RuntimeAuthorizer
	models      RuntimeModelResolver
	permissions agentcore.SessionPermissionHandler
	auth        agentcore.SessionAuthHandler
	defaultEnv  map[string]string
}

func NewConnectionRegistry() *ConnectionRegistry {
	return NewConnectionRegistryWithOptions(ConnectionRegistryOptions{})

}

type ConnectionRegistryOptions struct {
	Workspaces         *WorkspaceBroker
	Authorizer         RuntimeAuthorizer
	ModelResolver      RuntimeModelResolver
	PermissionHandler  agentcore.SessionPermissionHandler
	AuthHandler        agentcore.SessionAuthHandler
	DefaultEnvironment map[string]string
}

func NewConnectionRegistryWithOptions(options ConnectionRegistryOptions) *ConnectionRegistry {
	if options.Workspaces == nil {
		options.Workspaces = NewWorkspaceBroker()
	}
	return &ConnectionRegistry{
		connections: make(map[agentcore.RuntimeKind]agentcore.BackendConnection),
		unavailable: make(map[agentcore.RuntimeKind]string),
		workspaces:  options.Workspaces,
		authorizer:  options.Authorizer,
		models:      options.ModelResolver,
		permissions: options.PermissionHandler,
		auth:        options.AuthHandler,
		defaultEnv:  cloneStringMap(options.DefaultEnvironment),
	}
}

func (r *ConnectionRegistry) Register(kind agentcore.RuntimeKind, connection agentcore.BackendConnection) error {
	if r == nil || !kind.IsValid() || connection == nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime connection registration is invalid", false)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.connections[kind]; exists {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "runtime connection is already registered", false)
	}
	if _, exists := r.unavailable[kind]; exists {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "runtime connection is already marked unavailable", false)
	}
	r.connections[kind] = connection
	return nil
}

func (r *ConnectionRegistry) RegisterUnavailable(kind agentcore.RuntimeKind, reason string) error {
	if r == nil || !kind.IsValid() {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime unavailable registration is invalid", false)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("runtime connection %q is not available", kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.connections[kind]; exists {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "runtime connection is already registered", false)
	}
	if _, exists := r.unavailable[kind]; exists {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "runtime connection is already marked unavailable", false)
	}
	r.unavailable[kind] = reason
	return nil
}

func (r *ConnectionRegistry) Resolve(kind agentcore.RuntimeKind) (agentcore.BackendConnection, error) {
	if r == nil || !kind.IsValid() {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime connection kind is invalid", false)
	}
	r.mu.RLock()
	connection := r.connections[kind]
	reason := r.unavailable[kind]
	r.mu.RUnlock()
	if reason != "" {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, reason, false)
	}
	if connection == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, fmt.Sprintf("runtime connection %q is not enabled", kind), false)
	}
	return connection, nil
}

func (r *ConnectionRegistry) ResolveRuntime(ctx context.Context, spec agentcore.RuntimeSpec) (agentcore.SessionRuntime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection, err := r.Resolve(spec.Kind)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defaultEnv := cloneStringMap(r.defaultEnv)
	r.mu.RUnlock()
	return NewRuntime(RuntimeOptions{Kind: spec.Kind, Connection: connection, Workspaces: r.workspaces, Authorizer: r.authorizer, ModelResolver: r.models, PermissionHandler: r.permissions, AuthHandler: r.auth, DefaultEnvironment: defaultEnv})
}

func (r *ConnectionRegistry) SetDefaultEnvironment(environment map[string]string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.defaultEnv = cloneStringMap(environment)
	r.mu.Unlock()
}

func (r *ConnectionRegistry) BoundExecutionEnvelope(runID agentcore.AgentRunID) (*agentcore.ExecutionEnvelope, *agentcore.ResponsibilityContext, bool) {
	if r == nil {
		return nil, nil, false
	}
	return r.workspaces.BoundExecutionEnvelope(runID)
}

func (r *ConnectionRegistry) Capabilities(ctx context.Context, kind agentcore.RuntimeKind) (agentcore.Capabilities, bool) {
	connection, err := r.Resolve(kind)
	if err != nil {
		return agentcore.Capabilities{}, false
	}
	return connection.Capabilities(ctx), true
}

func (r *ConnectionRegistry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	connections := make([]agentcore.BackendConnection, 0, len(r.connections))
	for _, connection := range r.connections {
		connections = append(connections, connection)
	}
	r.connections = make(map[agentcore.RuntimeKind]agentcore.BackendConnection)
	r.unavailable = make(map[agentcore.RuntimeKind]string)
	r.mu.Unlock()
	var closeErrors []error
	for _, connection := range connections {
		if closer, ok := connection.(interface{ Close() error }); ok {
			closeErrors = append(closeErrors, closer.Close())
		}
	}
	return errors.Join(closeErrors...)
}

func (r *ConnectionRegistry) Workspaces() *WorkspaceBroker {
	if r == nil {
		return nil
	}
	return r.workspaces
}

func (r *ConnectionRegistry) ReleaseRuntimeWorkspace(ctx context.Context, root string) error {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	connections := make([]agentcore.BackendConnection, 0, len(r.connections))
	for _, connection := range r.connections {
		connections = append(connections, connection)
	}
	r.mu.RUnlock()
	var releaseErrors []error
	for _, connection := range connections {
		if releaser, ok := connection.(interface {
			ReleaseRuntimeWorkspace(context.Context, string) error
		}); ok {
			releaseErrors = append(releaseErrors, releaser.ReleaseRuntimeWorkspace(ctx, root))
		}
	}
	return errors.Join(releaseErrors...)
}

var _ agentcore.SessionRuntimeRegistry = (*ConnectionRegistry)(nil)
