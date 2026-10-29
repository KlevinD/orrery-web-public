// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package core

import (
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Reference: aionui-session/backend/types.rs and backend/mod.rs@v0.1.52.

type SessionID string

type SessionOpenMode string

const (
	SessionOpenFresh  SessionOpenMode = "fresh"
	SessionOpenResume SessionOpenMode = "resume"
)

type SessionSpec struct {
	Mode             SessionOpenMode `json:"mode"`
	SessionID        SessionID       `json:"session_id"`
	BackendSessionID string          `json:"backend_session_id,omitempty"`
}

type SessionConfig struct {
	WorkspaceRef  string            `json:"workspace_ref"`
	Model         string            `json:"model,omitempty"`
	Mode          string            `json:"mode,omitempty"`
	ConfigOptions map[string]string `json:"config_options,omitempty"`
	MCPServers    []MCPServerSpec   `json:"mcp_servers,omitempty"`
	Preset        string            `json:"preset,omitempty"`
	IdleTTL       time.Duration     `json:"-"`
}

type MCPTransportKind string

const (
	MCPTransportStdio MCPTransportKind = "stdio"
	MCPTransportHTTP  MCPTransportKind = "http"
	MCPTransportSSE   MCPTransportKind = "sse"
)

type MCPServerSpec struct {
	Name       string            `json:"name"`
	Transport  MCPTransportKind  `json:"transport"`
	Endpoint   string            `json:"endpoint,omitempty"`
	CommandRef string            `json:"command_ref,omitempty"`
	HeaderRefs map[string]string `json:"header_refs,omitempty"`
}

type Tier2Checkpoint struct {
	SessionID        SessionID `json:"session_id"`
	BackendSessionID string    `json:"backend_session_id,omitempty"`
	TurnGeneration   uint64    `json:"turn_generation"`
}

func (s SessionSpec) Validate() error {
	if strings.TrimSpace(string(s.SessionID)) == "" {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "session_id is required", false)
	}
	switch s.Mode {
	case SessionOpenFresh:
		if strings.TrimSpace(s.BackendSessionID) != "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "fresh session cannot carry backend_session_id", false)
		}
	case SessionOpenResume:
	default:
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid session open mode", false)
	}
	return nil
}

func (c SessionConfig) Validate() error {
	if strings.TrimSpace(c.WorkspaceRef) == "" {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "workspace_ref is required", false)
	}
	if isAbsoluteLocalPath(c.WorkspaceRef) || strings.ContainsAny(c.WorkspaceRef, "/\\ \t\r\n") {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "workspace_ref must be an opaque server reference, not a local path", false)
	}
	if c.IdleTTL < 0 {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "idle_ttl cannot be negative", false)
	}
	for key, value := range c.ConfigOptions {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "session config options cannot contain empty names or values", false)
		}
	}
	seen := make(map[string]struct{}, len(c.MCPServers))
	for _, server := range c.MCPServers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "mcp server name is required", false)
		}
		if _, exists := seen[name]; exists {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "duplicate mcp server name", false)
		}
		seen[name] = struct{}{}
		switch server.Transport {
		case MCPTransportStdio:
			if strings.TrimSpace(server.CommandRef) == "" || isAbsoluteLocalPath(server.CommandRef) || strings.ContainsAny(server.CommandRef, "/\\ \t\r\n") || server.Endpoint != "" || len(server.HeaderRefs) != 0 {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "stdio mcp requires only command_ref", false)
			}
		case MCPTransportHTTP, MCPTransportSSE:
			if strings.TrimSpace(server.Endpoint) == "" || server.CommandRef != "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "network mcp requires endpoint and no command_ref", false)
			}
			parsed, err := url.Parse(server.Endpoint)
			if err != nil || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "network mcp endpoint must be an HTTP URL without embedded credentials", false)
			}
			for header, ref := range server.HeaderRefs {
				if strings.TrimSpace(header) == "" || strings.TrimSpace(ref) == "" || isAbsoluteLocalPath(ref) || strings.ContainsAny(ref, "/\\ \t\r\n") {
					return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "mcp headers must use opaque credential references", false)
				}
			}
		default:
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid mcp transport", false)
		}
	}
	return nil
}

func isAbsoluteLocalPath(value string) bool {
	value = strings.TrimSpace(value)
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func (c SessionConfig) Clone() SessionConfig {
	out := c
	out.ConfigOptions = cloneRuntimeBridgeStringMap(c.ConfigOptions)
	out.MCPServers = make([]MCPServerSpec, len(c.MCPServers))
	for i, server := range c.MCPServers {
		out.MCPServers[i] = server
		out.MCPServers[i].HeaderRefs = cloneRuntimeBridgeStringMap(server.HeaderRefs)
	}
	return out
}

func cloneRuntimeBridgeStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
