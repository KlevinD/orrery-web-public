// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Reference: aionui-session/backend/mod.rs@v0.1.52.

type BackendConnection interface {
	OpenSession(context.Context, SessionSpec, SessionConfig) (SessionBackend, error)
	CloseSession(context.Context, SessionID) error
	Capabilities(context.Context) Capabilities
}

// SessionConfigPolicy lets a connection narrow canonical configuration before
// it crosses the provider boundary. It is used by remote/local-owned runtimes
// that must not receive cloud model, preset, MCP, or provider settings.
type SessionConfigPolicy interface {
	PrepareSessionConfig(context.Context, SessionSpec, SessionConfig) (SessionConfig, error)
}

type SessionBackend interface {
	Dispatch(context.Context, RuntimeCommand) (CommandReceipt, error)
	Events() <-chan SessionEnvelope
	Capabilities() Capabilities
	PendingPermissionRequests() []PendingPermissionView
}

// SessionRuntime is the application-facing canonical runtime. Implementations
// own ConversationSession/Orchestrator construction and expose the single
// ordered event stream to business consumers while deriving the terminal
// RuntimeResponse from that same stream.
type SessionRuntime interface {
	RunSession(context.Context, RuntimeRequest, SessionEventObserver) (RuntimeResponse, error)
	CancelSession(context.Context, AgentRunID, string) error
	SteerSession(context.Context, AgentRunID, string) error
	PrewarmSession(context.Context, RuntimeRequest) error
	ReleaseRuntimeWorkspace(context.Context, string) error
	Capabilities(context.Context) Capabilities
}

type SessionEventObserver func(context.Context, SessionEnvelope) error

type SessionPermissionRequest struct {
	TenantID    string
	RunID       AgentRunID
	AgentID     AgentID
	RuntimeKind RuntimeKind
	Envelope    SessionEnvelope
}

type SessionPermissionResponder func(AnswerPermissionCommand) error

// SessionPermissionHandler is an application port. Providers only emit the
// canonical permission event; the application owns persistence and decisions.
type SessionPermissionHandler interface {
	RequestSessionPermission(context.Context, SessionPermissionRequest, SessionPermissionResponder) error
}

type SessionPermissionHandlerFunc func(context.Context, SessionPermissionRequest, SessionPermissionResponder) error

func (f SessionPermissionHandlerFunc) RequestSessionPermission(ctx context.Context, request SessionPermissionRequest, respond SessionPermissionResponder) error {
	return f(ctx, request, respond)
}

type SessionAuthRequest struct {
	TenantID      string
	RunID         AgentRunID
	AgentID       AgentID
	RuntimeKind   RuntimeKind
	Envelope      SessionEnvelope
	MethodID      string
	CredentialRef string
}

type SessionAuthResponder func(AnswerAuthCommand) error

// SessionAuthHandler is the application-owned credential boundary. Providers
// request auth through canonical events but never receive a credential ref.
type SessionAuthHandler interface {
	RequestSessionAuth(context.Context, SessionAuthRequest, SessionAuthResponder) error
}

type SessionAuthHandlerFunc func(context.Context, SessionAuthRequest, SessionAuthResponder) error

func (f SessionAuthHandlerFunc) RequestSessionAuth(ctx context.Context, request SessionAuthRequest, respond SessionAuthResponder) error {
	return f(ctx, request, respond)
}

type SessionAuthCredential struct {
	MethodID    string
	Credentials json.RawMessage
}

// SessionRuntimeRegistry is the primary provider entry point used by the
// application and agent assembly layers.
type SessionRuntimeRegistry interface {
	ResolveRuntime(context.Context, RuntimeSpec) (SessionRuntime, error)
	Capabilities(context.Context, RuntimeKind) (Capabilities, bool)
}

// SessionRuntimeRecordReader is implemented by canonical runtimes that retain
// a bounded application projection for discussion streaming and diagnostics.
// The provider remains unaware of these records.
type SessionRuntimeRecordReader interface {
	RuntimeRequest(AgentRunID) (RuntimeRequest, bool)
	RuntimeResponse(AgentRunID) (RuntimeResponse, bool)
	RuntimeEvents(AgentRunID) []SessionEnvelope
	SubscribeRuntimeEvents(AgentRunID) (<-chan SessionEnvelope, func())
	DeleteRuntimeRecord(context.Context, AgentRunID) error
}

type RuntimeBridgeErrorCode string

const (
	RuntimeBridgeInvalidCommand           RuntimeBridgeErrorCode = "invalid_command"
	RuntimeBridgeInvalidPayload           RuntimeBridgeErrorCode = "invalid_payload"
	RuntimeBridgeUnsupportedCommand       RuntimeBridgeErrorCode = "unsupported_command"
	RuntimeBridgeCommandConflict          RuntimeBridgeErrorCode = "command_conflict"
	RuntimeBridgeSessionMismatch          RuntimeBridgeErrorCode = "session_mismatch"
	RuntimeBridgeGenerationMismatch       RuntimeBridgeErrorCode = "generation_mismatch"
	RuntimeBridgeQueueFull                RuntimeBridgeErrorCode = "queue_full"
	RuntimeBridgeHandshakeTimeout         RuntimeBridgeErrorCode = "handshake_timeout"
	RuntimeBridgeWorkspaceUnavailable     RuntimeBridgeErrorCode = "workspace_unavailable"
	RuntimeBridgeTransportClosed          RuntimeBridgeErrorCode = "transport_closed"
	RuntimeBridgeProtocolError            RuntimeBridgeErrorCode = "protocol_error"
	RuntimeBridgeBackendRejected          RuntimeBridgeErrorCode = "backend_rejected"
	RuntimeBridgeUnknownPermissionRequest RuntimeBridgeErrorCode = "unknown_permission_request"
	RuntimeBridgeUnknownAuthRequest       RuntimeBridgeErrorCode = "unknown_auth_request"
	RuntimeBridgeCancelled                RuntimeBridgeErrorCode = "cancelled"
	RuntimeBridgeDetachedCleanNoResult    RuntimeBridgeErrorCode = "detached_clean_no_result"
	RuntimeBridgeDetachedCrash            RuntimeBridgeErrorCode = "detached_crash"
)

type RuntimeBridgeError struct {
	Code      RuntimeBridgeErrorCode
	Message   string
	Retryable bool
	Cause     error
}

func NewRuntimeBridgeError(code RuntimeBridgeErrorCode, message string, retryable bool) *RuntimeBridgeError {
	return &RuntimeBridgeError{Code: code, Message: message, Retryable: retryable}
}

func (e *RuntimeBridgeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *RuntimeBridgeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsRuntimeBridgeError(err error, code RuntimeBridgeErrorCode) bool {
	var bridgeErr *RuntimeBridgeError
	return errors.As(err, &bridgeErr) && bridgeErr.Code == code
}

type PendingPermissionView struct {
	RequestID string               `json:"request_id"`
	ToolName  string               `json:"tool_name,omitempty"`
	Questions []PermissionQuestion `json:"questions,omitempty"`
}
