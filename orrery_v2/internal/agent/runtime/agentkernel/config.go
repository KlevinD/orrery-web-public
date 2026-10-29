// Package agentkernel implements Orrery's opt-in model/tool loop. Eino supplies
// model and message interfaces; Orrery owns execution and authorization rules.
package agentkernel

import (
	"context"
	"errors"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/agent/runtime/session"
)

var (
	ErrConfiguration = errors.New("agent kernel is not configured")
	ErrClosed        = errors.New("agent kernel session is closed or failed")
	ErrBusy          = errors.New("agent kernel session is running")
	ErrBudget        = errors.New("agent kernel execution budget exceeded")
	ErrProtocol      = errors.New("agent kernel received invalid model output")
	ErrInput         = errors.New("agent kernel input is invalid or unsupported")
	ErrModel         = errors.New("agent kernel model request failed")
	ErrTool          = errors.New("agent kernel tool execution failed; result may require confirmation")
	ErrToolDenied    = errors.New("agent kernel tool request was denied")
	ErrJournal       = errors.New("agent kernel tool journal is unavailable")
	ErrToolRecorded  = errors.New("agent kernel tool call was already recorded; automatic replay is blocked")
	ErrToolConflict  = errors.New("agent kernel tool call identity conflicts with a recorded operation")
	ErrRunUnresolved = errors.New("agent kernel run has an unresolved tool execution; confirmation is required")
	ErrCapacity      = errors.New("agent kernel callback capacity exhausted")
	ErrCheckpoint    = errors.New("agent kernel checkpoint is unavailable or conflicts with the current session")
	ErrUnsafeResume  = errors.New("agent kernel checkpoint has an unfinished tool batch; confirmation is required")
)

// ToolPolicy must check each call's arguments against the trusted tenant, agent,
// principal and data scopes, including knowledge-base IDs. A tool name alone is
// not authorization. Approval never replaces this check or the host gateway.
type ToolPolicy func(context.Context, runtimecore.RuntimeToolCallRequest) error

type Config struct {
	Workspaces    *session.WorkspaceBroker
	Models        profilemodel.ModelFactory
	Tools         runtimecore.RuntimeToolDispatcher
	AuthorizeTool ToolPolicy
	ToolJournal   ToolJournal
	// ExplicitToolScopeFence is a host assertion that policy and dispatcher
	// enforce the responsibility snapshot on every call, without native tools.
	ExplicitToolScopeFence bool
	MaxParallelTools       int
	ParallelToolIDs        []string
	ApprovalToolIDs        []string
	ApprovalTimeout        time.Duration
	Checkpoints            CheckpointStore
	AutoCompact            bool
	PromptCache            bool
	CompactAtBytes         int
	CompactKeepMessages    int
	CompactSummaryBytes    int
	LongTaskMaxModelCalls  int
	LongTaskMaxToolCalls   int
	LongTaskTimeout        time.Duration
	MaxConcurrentCallbacks int
	MaxModelCalls          int
	MaxToolCalls           int
	MaxContextBytes        int
	MaxResponseBytes       int
	ModelTimeout           time.Duration
	ToolTimeout            time.Duration
	JournalTimeout         time.Duration
}

func (c Config) normalized() (Config, error) {
	if c.Workspaces == nil || c.Models == nil {
		return Config{}, ErrConfiguration
	}
	if c.MaxModelCalls == 0 {
		c.MaxModelCalls = 8
	}
	if c.MaxConcurrentCallbacks == 0 {
		c.MaxConcurrentCallbacks = 32
	}
	if c.MaxToolCalls == 0 {
		c.MaxToolCalls = 16
	}
	if c.MaxParallelTools == 0 {
		c.MaxParallelTools = 1
	}
	if c.MaxParallelTools < 1 || c.MaxParallelTools > 16 {
		return Config{}, ErrConfiguration
	}
	c.ParallelToolIDs = append([]string(nil), c.ParallelToolIDs...)
	c.ApprovalToolIDs = append([]string(nil), c.ApprovalToolIDs...)
	if c.ApprovalTimeout == 0 {
		c.ApprovalTimeout = 10 * time.Minute
	}
	if c.ApprovalTimeout < 0 || c.ApprovalTimeout > 30*time.Minute || (len(c.ApprovalToolIDs) > 0 && (c.Tools == nil || c.AuthorizeTool == nil || c.ToolJournal == nil)) {
		return Config{}, ErrConfiguration
	}
	if c.MaxParallelTools > 1 && len(c.ParallelToolIDs) > 0 {
		if _, ok := c.ToolJournal.(BatchToolJournal); !ok {
			return Config{}, ErrConfiguration
		}
	}
	if c.MaxContextBytes == 0 {
		c.MaxContextBytes = 512 * 1024
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 256 * 1024
	}
	if c.ModelTimeout == 0 {
		c.ModelTimeout = 45 * time.Second
	}
	if c.ToolTimeout == 0 {
		c.ToolTimeout = 30 * time.Second
	}
	if c.JournalTimeout == 0 {
		c.JournalTimeout = 2 * time.Second
	}
	if c.CompactAtBytes == 0 {
		c.CompactAtBytes = c.MaxContextBytes * 3 / 4
	}
	if c.CompactKeepMessages == 0 {
		c.CompactKeepMessages = 8
	}
	if c.CompactSummaryBytes == 0 {
		c.CompactSummaryBytes = 16 * 1024
	}
	if c.LongTaskMaxModelCalls == 0 {
		c.LongTaskMaxModelCalls = max(64, c.MaxModelCalls)
	}
	if c.LongTaskMaxToolCalls == 0 {
		c.LongTaskMaxToolCalls = max(128, c.MaxToolCalls)
	}
	if c.LongTaskTimeout == 0 {
		c.LongTaskTimeout = 15 * time.Minute
	}
	if c.MaxConcurrentCallbacks < 1 || c.MaxModelCalls < 1 || c.MaxToolCalls < 1 || c.MaxContextBytes < 1 || c.MaxResponseBytes < 1 || c.ModelTimeout < 0 || c.ToolTimeout < 0 || c.JournalTimeout < 0 {
		return Config{}, ErrConfiguration
	}
	if (c.AutoCompact && (c.CompactAtBytes < 1 || c.CompactAtBytes >= c.MaxContextBytes || c.CompactKeepMessages < 1 || c.CompactSummaryBytes < 1)) || c.LongTaskMaxModelCalls < c.MaxModelCalls || c.LongTaskMaxToolCalls < c.MaxToolCalls || c.LongTaskTimeout <= 0 || c.LongTaskTimeout > 30*time.Minute {
		return Config{}, ErrConfiguration
	}
	if c.AutoCompact && c.Checkpoints == nil {
		return Config{}, ErrConfiguration
	}
	return c, nil
}

var _ kernelcompat.Engine = (*Engine)(nil)
var _ kernelcompat.Runner = (*Runner)(nil)
