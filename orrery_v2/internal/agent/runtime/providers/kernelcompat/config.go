// Package kernelcompat adapts an explicitly supplied kernel to Orrery's
// canonical session contract. It does not select or launch a PI runtime.
package kernelcompat

import (
	"context"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

// Engine creates a session from an application-authorized workspace reference.
// Implementations resolve that opaque reference through their trusted host;
// SessionConfig is not authority to grant tools, credentials, or filesystem access.
type Engine interface {
	OpenSession(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error)
}

type EngineFunc func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error)

func (f EngineFunc) OpenSession(ctx context.Context, spec agentcore.SessionSpec, config agentcore.SessionConfig) (Runner, error) {
	return f(ctx, spec, config)
}

// Runner owns one kernel session. Run calls are serialized. Run must honor its
// context and return an explicit outcome; Close must release session resources.
// The adapter owns epochs, sequencing, and terminal outcomes. The canonical
// orchestrator owns turn_started and cancel lifecycle events.
type Runner interface {
	Run(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error)
	Close(context.Context) error
}

// PermissionRunner preserves server-only approval material and accepts a
// decision for the exact pending operation, without granting session authority.
type PermissionRunner interface {
	Runner
	PendingPermissionRequests() []agentcore.PendingPermissionView
	PendingRuntimeApprovalRequest(string) (runtimecore.ApprovalRequest, bool)
	AnswerPermission(context.Context, agentcore.AnswerPermissionCommand) error
}

// Emit accepts message deltas, tool calls/results, and usage deltas. Returning an
// error invalidates the turn even if the runner subsequently returns success.
type Emit func(agentcore.SessionEvent) error

type Config struct {
	Engine           Engine
	EventCapacity    int
	BackpressureWait time.Duration
	TurnTimeout      time.Duration
	MaxTurnTimeout   time.Duration
	CloseTimeout     time.Duration
}

func capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier: agentcore.CapabilityParsed,
		SupportedCommands: agentcore.CommandSet{
			Send: true, CancelTurn: true,
		},
		PromptBlocks:      agentcore.BlockSet{Text: true},
		PromptAccepted:    agentcore.PromptAcceptedSynthesized,
		Signals:           agentcore.SignalSet{ToolLifecycle: true, TerminalResult: true, Usage: true},
		SupportsMultiTurn: true,
		PendingLimit:      1,
		PayloadLimitBytes: 1024 * 1024,
		Transport:         agentcore.TransportEvidence{Provider: "orrery-kernel", Protocol: "in-process-session-v1", Version: "1"},
	}
}

var _ agentcore.BackendConnection = (*Connection)(nil)

func capabilitiesForEngine(engine Engine) agentcore.Capabilities {
	caps := capabilities()
	if images, ok := engine.(interface{ SupportsImageInputs() bool }); ok && images.SupportsImageInputs() {
		caps.PromptBlocks.Image = true
		caps.PayloadLimitBytes = 24 << 20
	}
	if resumable, ok := engine.(interface{ SupportsResume() bool }); ok {
		caps.SupportsResume = resumable.SupportsResume()
	}
	if permissions, ok := engine.(interface{ SupportsPermissionAnswers() bool }); ok {
		caps.SupportedCommands.AnswerPermission = permissions.SupportsPermissionAnswers()
	}
	return caps
}

var _ agentcore.SessionBackend = (*Session)(nil)
