package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type Resolver struct {
	registry Registry
	recorder AuditRecorder
	now      func() time.Time
}

type ResolverOptions struct {
	AuditRecorder AuditRecorder
	Now           func() time.Time
}

type ResolveRequest struct {
	Ref       agentcore.ToolRef
	Principal Principal
}

func NewResolver(registry Registry, options ResolverOptions) *Resolver {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Resolver{
		registry: registry,
		recorder: options.AuditRecorder,
		now:      now,
	}
}

func (r *Resolver) Resolve(ctx context.Context, req ResolveRequest) (ResolvedTool, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedTool{}, err
	}
	if req.Ref.IsZero() {
		return ResolvedTool{}, fmt.Errorf("%w: tool ref is required", ErrNotFound)
	}
	if r == nil || r.registry == nil {
		return ResolvedTool{}, fmt.Errorf("%w: tool registry is not configured", ErrNotFound)
	}
	definition, err := r.registry.Get(ctx, req.Ref.ToolID)
	if err != nil {
		originalErr := err
		var resolved bool
		definition, resolved, err = r.resolveMCPDownstreamToolDefinition(ctx, req.Ref.ToolID)
		if err != nil {
			return ResolvedTool{}, err
		}
		if !resolved {
			return ResolvedTool{}, originalErr
		}
	}
	if !definition.Enabled {
		r.record(ctx, AuditToolBindingDenied, req, "disabled")
		return ResolvedTool{}, fmt.Errorf("%w: %s", ErrDisabled, req.Ref.ToolID)
	}
	if !versionMatches(definition.Version, req.Ref.VersionConstraint) {
		r.record(ctx, AuditToolBindingDenied, req, "version_mismatch")
		return ResolvedTool{}, versionMismatch(definition.Version, req.Ref.VersionConstraint)
	}
	if err := checkPermissions(definition.Permissions, req.Principal.Permissions); err != nil {
		r.record(ctx, AuditToolBindingDenied, req, "permission_denied")
		return ResolvedTool{}, err
	}
	resolved := ResolvedTool{
		Ref:                req.Ref,
		Definition:         definition.Clone(),
		GrantedPermissions: grantedPermissions(definition.Permissions, req.Principal.Permissions),
		SecretBindingRefs:  secretBindingRefs(definition),
	}
	r.record(ctx, AuditToolResolved, req, "")
	return resolved, nil
}

func (r *Resolver) resolveMCPDownstreamToolDefinition(ctx context.Context, refID agentcore.ToolID) (ToolDefinition, bool, error) {
	ref := strings.TrimSpace(string(refID))
	if ref == "" || !strings.Contains(ref, ".") || r == nil || r.registry == nil {
		return ToolDefinition{}, false, nil
	}
	parts := strings.Split(ref, ".")
	for i := len(parts) - 1; i > 0; i-- {
		serverID := agentcore.ToolID(strings.Join(parts[:i], "."))
		downstream := strings.TrimSpace(strings.TrimPrefix(ref, string(serverID)+"."))
		if downstream == "" {
			continue
		}
		definition, err := r.registry.Get(ctx, serverID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return ToolDefinition{}, true, err
		}
		if definition.Kind != KindMCPServer || definition.MCP == nil {
			continue
		}
		if !mcpDefinitionExposesTool(definition, downstream) {
			return ToolDefinition{}, true, fmt.Errorf("%w: %s", ErrNotFound, refID)
		}
		return definition, true, nil
	}
	return ToolDefinition{}, false, nil
}

func mcpDefinitionExposesTool(definition ToolDefinition, name string) bool {
	if definition.MCP == nil {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, exposed := range definition.MCP.ExposedToolDetails {
		if strings.TrimSpace(exposed.Name) == name {
			return true
		}
	}
	for _, exposed := range definition.MCP.ExposedTools {
		if strings.TrimSpace(exposed) == name {
			return true
		}
	}
	return false
}

func secretBindingRefs(definition ToolDefinition) []SecretBindingRef {
	refs := make([]SecretBindingRef, 0, len(definition.SecretRefs))
	for _, ref := range definition.SecretRefs {
		refs = append(refs, SecretBindingRef{SecretName: ref.Name, Env: ref.Env})
	}
	return refs
}

func (r *Resolver) record(ctx context.Context, eventType AuditEventType, req ResolveRequest, reason string) {
	if r.recorder == nil {
		return
	}
	_ = r.recorder.RecordToolAudit(ctx, AuditEvent{
		Type:       eventType,
		ToolID:     req.Ref.ToolID,
		AgentID:    req.Principal.AgentID,
		Reason:     reason,
		OccurredAt: r.now(),
	})
}
