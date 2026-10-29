package resourceadmin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	toolres "orrery_v2/internal/resource/tool"
)

var ErrPartialMCPSync = errors.New("partial mcp tool cache sync")

type MCPToolCacheSyncerOptions struct {
	Registry      toolres.Registry
	Runtime       MCPRuntimePort
	BatchSize     int
	ServerTimeout time.Duration
}

type MCPToolCacheSyncer struct {
	registry      toolres.Registry
	runtime       MCPRuntimePort
	batchSize     int
	serverTimeout time.Duration

	cursorMu sync.Mutex
	cursor   int
}

func NewMCPToolCacheSyncer(opts MCPToolCacheSyncerOptions) *MCPToolCacheSyncer {
	return &MCPToolCacheSyncer{
		registry:      opts.Registry,
		runtime:       opts.Runtime,
		batchSize:     opts.BatchSize,
		serverTimeout: opts.ServerTimeout,
	}
}

func (s *MCPToolCacheSyncer) Sweep(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.registry == nil || s.runtime == nil {
		return ErrServiceDependency
	}
	definitions, err := s.registry.ListByKind(ctx, toolres.KindMCPServer)
	if err != nil {
		return err
	}
	eligible := make([]toolres.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if !definition.Enabled || definition.MCP == nil {
			continue
		}
		if isSystemMCPServerDefinition(definition) || isLockedMCPToolSurfaceDefinition(definition) {
			continue
		}
		eligible = append(eligible, definition)
	}
	sort.Slice(eligible, func(i, j int) bool {
		return string(eligible[i].ID) < string(eligible[j].ID)
	})

	var failures []string
	for _, definition := range s.nextBatch(eligible) {
		serverCtx := ctx
		cancel := func() {}
		if s.serverTimeout > 0 {
			serverCtx, cancel = context.WithTimeout(ctx, s.serverTimeout)
		}
		listed, err := s.runtime.ListTools(serverCtx, definition)
		cancel()
		if err != nil {
			definition.MCP.Availability = toolres.MCPAvailability{
				Status:        toolres.MCPAvailabilityUnavailable,
				FailureReason: err.Error(),
				LatencyMS:     listed.Latency.Milliseconds(),
				CheckedAt:     time.Now().UTC(),
			}
			if updateErr := s.registry.UpdateDefinition(ctx, definition); updateErr != nil {
				failures = append(failures, string(definition.ID)+": "+updateErr.Error())
				continue
			}
			failures = append(failures, string(definition.ID)+": "+err.Error())
			continue
		}
		definition.MCP.ExposedTools = nonEmptyUniqueStrings(listed.Tools)
		definition.MCP.ExposedToolDetails = nonEmptyUniqueToolDetails(listed.ToolDetails)
		definition.MCP.Availability = toolres.MCPAvailability{
			Status:    toolres.MCPAvailabilityAvailable,
			Message:   "MCP tool cache sync succeeded",
			LatencyMS: listed.Latency.Milliseconds(),
			CheckedAt: time.Now().UTC(),
		}
		if err := s.registry.UpdateDefinition(ctx, definition); err != nil {
			failures = append(failures, string(definition.ID)+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w: %s", ErrPartialMCPSync, strings.Join(failures, "; "))
	}
	return nil
}

func (s *MCPToolCacheSyncer) nextBatch(definitions []toolres.ToolDefinition) []toolres.ToolDefinition {
	if len(definitions) == 0 {
		return nil
	}
	if s.batchSize <= 0 || s.batchSize >= len(definitions) {
		return definitions
	}

	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	start := s.cursor % len(definitions)
	batch := make([]toolres.ToolDefinition, 0, s.batchSize)
	for offset := 0; offset < s.batchSize; offset++ {
		batch = append(batch, definitions[(start+offset)%len(definitions)])
	}
	s.cursor = (start + s.batchSize) % len(definitions)
	return batch
}

func nonEmptyUniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func nonEmptyUniqueToolDetails(values []toolres.MCPExposedToolDefinition) []toolres.MCPExposedToolDefinition {
	out := make([]toolres.MCPExposedToolDefinition, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		value.Name = name
		value.Description = strings.TrimSpace(value.Description)
		out = append(out, value)
	}
	return out
}
