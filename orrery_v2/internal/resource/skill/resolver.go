package skill

import (
	"context"
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
	Ref          agentcore.SkillRef
	TaskTypeTags []string
}

func NewResolver(registry Registry, options ResolverOptions) *Resolver {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Resolver{registry: registry, recorder: options.AuditRecorder, now: now}
}

func (r *Resolver) Resolve(ctx context.Context, req ResolveRequest) (ResolvedSkill, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedSkill{}, err
	}
	if req.Ref.IsZero() {
		return ResolvedSkill{}, fmt.Errorf("%w: skill ref is required", ErrNotFound)
	}
	if r == nil || r.registry == nil {
		return ResolvedSkill{}, fmt.Errorf("%w: skill registry is not configured", ErrNotFound)
	}
	definition, err := r.resolveDefinition(ctx, req.Ref)
	if err != nil {
		return ResolvedSkill{}, err
	}
	if !definition.Enabled {
		return ResolvedSkill{}, fmt.Errorf("%w: %s", ErrDisabled, req.Ref.SkillID)
	}
	if !versionMatches(definition.Version, req.Ref.VersionConstraint) {
		return ResolvedSkill{}, versionMismatch(definition.Version, req.Ref.VersionConstraint)
	}
	resolved := ResolvedSkill{
		Ref:            req.Ref,
		Definition:     definition.Clone(),
		MatchedVersion: definition.Version,
	}
	if len(req.TaskTypeTags) > 0 && len(definition.TaskTypeTags) > 0 && !intersects(req.TaskTypeTags, definition.TaskTypeTags) {
		resolved.BindingWarnings = append(resolved.BindingWarnings, BindingWarning{
			Code:    WarningTaskTagMismatch,
			Message: "skill task_type_tags do not match current task tags",
		})
	}
	reason := ""
	if exact := exactVersionConstraint(req.Ref.VersionConstraint); exact != "" {
		reason = "explicit_version:" + exact
	}
	r.record(ctx, req.Ref.SkillID, reason)
	return resolved, nil
}

func (r *Resolver) resolveDefinition(ctx context.Context, ref agentcore.SkillRef) (SkillDefinition, error) {
	if version := exactVersionConstraint(ref.VersionConstraint); version != "" {
		if versioned, ok := r.registry.(VersionedRegistry); ok {
			return versioned.GetVersion(ctx, ref.SkillID, version)
		}
	}
	return r.registry.Get(ctx, ref.SkillID)
}

func exactVersionConstraint(constraint string) string {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" || strings.ContainsAny(constraint, "<>^") || strings.HasSuffix(constraint, ".x") {
		return ""
	}
	if len(parseVersion(constraint)) == 0 {
		return ""
	}
	return constraint
}

func (r *Resolver) record(ctx context.Context, id agentcore.SkillID, reason string) {
	if r.recorder == nil {
		return
	}
	_ = r.recorder.RecordSkillAudit(ctx, AuditEvent{
		Type:       AuditSkillResolved,
		SkillID:    id,
		Reason:     reason,
		OccurredAt: r.now(),
	})
}

func intersects(left, right []string) bool {
	rightSet := map[string]struct{}{}
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	for _, value := range left {
		if _, ok := rightSet[value]; ok {
			return true
		}
	}
	return false
}
