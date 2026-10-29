package lifecycle

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type PreHook interface {
	Name() string
	BeforeAgentStep(ctx context.Context, state *AgentRunState) error
}

type PostHook interface {
	Name() string
	AfterAgentStep(ctx context.Context, state *AgentRunState) error
}

type PreHookFunc struct {
	name string
	fn   func(context.Context, *AgentRunState) error
}

func NewPreHook(name string, fn func(context.Context, *AgentRunState) error) PreHook {
	return PreHookFunc{name: name, fn: fn}
}

func (h PreHookFunc) Name() string { return h.name }

func (h PreHookFunc) BeforeAgentStep(ctx context.Context, state *AgentRunState) error {
	return h.fn(ctx, state)
}

type PostHookFunc struct {
	name string
	fn   func(context.Context, *AgentRunState) error
}

func NewPostHook(name string, fn func(context.Context, *AgentRunState) error) PostHook {
	return PostHookFunc{name: name, fn: fn}
}

func (h PostHookFunc) Name() string { return h.name }

func (h PostHookFunc) AfterAgentStep(ctx context.Context, state *AgentRunState) error {
	return h.fn(ctx, state)
}

type HookRegistry struct {
	pre   []PreHookRegistration
	post  []PostHookRegistration
	names map[string]struct{}
}

func NewHookRegistry() *HookRegistry {
	return &HookRegistry{names: map[string]struct{}{}}
}

func (r *HookRegistry) RegisterPreHook(hook PreHook) {
	_ = r.RegisterPreHookE(hook, HookOptions{})
}

func (r *HookRegistry) RegisterPostHook(hook PostHook) {
	_ = r.RegisterPostHookE(hook, HookOptions{})
}

type HookOptions struct {
	Order             int
	Disabled          bool
	FailurePolicy     FailurePolicy
	DegradeCapability string
}

func (o HookOptions) normalized() HookOptions {
	if o.FailurePolicy == "" {
		o.FailurePolicy = FailurePolicyStop
	}
	return o
}

type PreHookRegistration struct {
	Hook    PreHook
	Options HookOptions
}

type PostHookRegistration struct {
	Hook    PostHook
	Options HookOptions
}

func (r *HookRegistry) RegisterPreHookE(hook PreHook, opts HookOptions) error {
	if hook == nil {
		return fmt.Errorf("register pre hook: nil hook")
	}
	if err := r.registerName(hook.Name()); err != nil {
		return err
	}
	r.pre = append(r.pre, PreHookRegistration{Hook: hook, Options: opts.normalized()})
	sort.SliceStable(r.pre, func(i, j int) bool {
		return r.pre[i].Options.Order < r.pre[j].Options.Order
	})
	return nil
}

func (r *HookRegistry) RegisterPostHookE(hook PostHook, opts HookOptions) error {
	if hook == nil {
		return fmt.Errorf("register post hook: nil hook")
	}
	if err := r.registerName(hook.Name()); err != nil {
		return err
	}
	r.post = append(r.post, PostHookRegistration{Hook: hook, Options: opts.normalized()})
	sort.SliceStable(r.post, func(i, j int) bool {
		return r.post[i].Options.Order < r.post[j].Options.Order
	})
	return nil
}

func (r *HookRegistry) registerName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("register hook: empty hook name")
	}
	if r.names == nil {
		r.names = map[string]struct{}{}
	}
	if _, exists := r.names[name]; exists {
		return fmt.Errorf("register hook: duplicate hook name %q", name)
	}
	r.names[name] = struct{}{}
	return nil
}

func (r *HookRegistry) PreHooks() []PreHook {
	registrations := r.PreHookRegistrations()
	out := make([]PreHook, 0, len(registrations))
	for _, reg := range registrations {
		out = append(out, reg.Hook)
	}
	return out
}

func (r *HookRegistry) PostHooks() []PostHook {
	registrations := r.PostHookRegistrations()
	out := make([]PostHook, 0, len(registrations))
	for _, reg := range registrations {
		out = append(out, reg.Hook)
	}
	return out
}

func (r *HookRegistry) PreHookRegistrations() []PreHookRegistration {
	out := make([]PreHookRegistration, 0, len(r.pre))
	for _, reg := range r.pre {
		if !reg.Options.Disabled {
			out = append(out, reg)
		}
	}
	return out
}

func (r *HookRegistry) PostHookRegistrations() []PostHookRegistration {
	out := make([]PostHookRegistration, 0, len(r.post))
	for _, reg := range r.post {
		if !reg.Options.Disabled {
			out = append(out, reg)
		}
	}
	return out
}
