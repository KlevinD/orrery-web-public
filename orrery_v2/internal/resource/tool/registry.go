package tool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
)

type Registry interface {
	Register(ctx context.Context, definition ToolDefinition) error
	UpdateDefinition(ctx context.Context, definition ToolDefinition) error
	Disable(ctx context.Context, id agentcore.ToolID) error
	Delete(ctx context.Context, id agentcore.ToolID) error
	IsDeleted(ctx context.Context, id agentcore.ToolID) (bool, error)
	Get(ctx context.Context, id agentcore.ToolID) (ToolDefinition, error)
	List(ctx context.Context) ([]ToolDefinition, error)
	ListByKind(ctx context.Context, kind Kind) ([]ToolDefinition, error)
	ListByTag(ctx context.Context, tag string) ([]ToolDefinition, error)
	CheckPermission(ctx context.Context, id agentcore.ToolID, granted []string) error
}

type MemoryRegistry struct {
	mu      sync.RWMutex
	tools   map[agentcore.ToolID]ToolDefinition
	deleted map[agentcore.ToolID]bool
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{tools: map[agentcore.ToolID]ToolDefinition{}, deleted: map[agentcore.ToolID]bool{}}
}

func (r *MemoryRegistry) Register(ctx context.Context, definition ToolDefinition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDefinition(definition); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[definition.ID]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, definition.ID)
	}
	delete(r.deleted, definition.ID)
	r.tools[definition.ID] = definition.Clone()
	return nil
}

func (r *MemoryRegistry) UpdateDefinition(ctx context.Context, definition ToolDefinition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDefinition(definition); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[definition.ID]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, definition.ID)
	}
	r.tools[definition.ID] = definition.Clone()
	return nil
}

func (r *MemoryRegistry) Disable(ctx context.Context, id agentcore.ToolID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	definition, exists := r.tools[id]
	if !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	definition.Enabled = false
	r.tools[id] = definition
	return nil
}

func (r *MemoryRegistry) Delete(ctx context.Context, id agentcore.ToolID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[id]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(r.tools, id)
	r.deleted[id] = true
	return nil
}

func (r *MemoryRegistry) IsDeleted(ctx context.Context, id agentcore.ToolID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deleted[id], nil
}

func (r *MemoryRegistry) Get(ctx context.Context, id agentcore.ToolID) (ToolDefinition, error) {
	if err := ctx.Err(); err != nil {
		return ToolDefinition{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	definition, exists := r.tools[id]
	if !exists {
		return ToolDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return definition.Clone(), nil
}

func (r *MemoryRegistry) List(ctx context.Context) ([]ToolDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolDefinition, 0, len(r.tools))
	for _, definition := range r.tools {
		out = append(out, definition.Clone())
	}
	sortDefinitions(out)
	return out, nil
}

func (r *MemoryRegistry) ListByKind(ctx context.Context, kind Kind) ([]ToolDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ToolDefinition
	for _, definition := range r.tools {
		if definition.Kind == kind {
			out = append(out, definition.Clone())
		}
	}
	sortDefinitions(out)
	return out, nil
}

func (r *MemoryRegistry) ListByTag(ctx context.Context, tag string) ([]ToolDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tag = strings.TrimSpace(tag)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ToolDefinition
	for _, definition := range r.tools {
		if hasString(definition.Tags, tag) {
			out = append(out, definition.Clone())
		}
	}
	sortDefinitions(out)
	return out, nil
}

func (r *MemoryRegistry) CheckPermission(ctx context.Context, id agentcore.ToolID, granted []string) error {
	definition, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	return checkPermissions(definition.Permissions, granted)
}

func validateDefinition(definition ToolDefinition) error {
	if definition.ID == "" {
		return fmt.Errorf("%w: tool_id is required", ErrInvalidDefinition)
	}
	if !definition.Kind.IsValid() {
		return fmt.Errorf("%w: kind %q is invalid", ErrInvalidDefinition, definition.Kind)
	}
	if strings.TrimSpace(definition.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidDefinition)
	}
	if strings.TrimSpace(definition.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidDefinition)
	}
	if definition.Kind == KindMCPServer && definition.MCP == nil {
		return fmt.Errorf("%w: mcp definition is required", ErrInvalidDefinition)
	}
	if definition.Kind == KindCLITool && definition.CLI == nil {
		return fmt.Errorf("%w: cli definition is required", ErrInvalidDefinition)
	}
	if (definition.Kind == KindApprovalTool || definition.Kind == KindHumanConfirmationTool) && definition.Approval == nil {
		return fmt.Errorf("%w: approval definition is required", ErrInvalidDefinition)
	}
	return nil
}

func sortDefinitions(definitions []ToolDefinition) {
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].ID < definitions[j].ID
	})
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
