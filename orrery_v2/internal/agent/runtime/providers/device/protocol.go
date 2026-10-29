package device

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	devicebrowser "orrery_v2/internal/device/browser"
)

const (
	ProtocolVersion       = "1.0"
	ProtocolMaxFrameBytes = 1024 * 1024
	maxSafeInteger        = int64(9007199254740991)
)

type MessageType string

const (
	MessageDeviceWelcome     MessageType = "device.welcome"
	MessageWorkOrderOffer    MessageType = "work_order.offer"
	MessageWorkOrderCancel   MessageType = "work_order.cancel"
	MessageDeviceHello       MessageType = "device.hello"
	MessageDeviceHeartbeat   MessageType = "device.heartbeat"
	MessageWorkOrderAccepted MessageType = "work_order.accepted"
	MessageWorkOrderRejected MessageType = "work_order.rejected"
	MessageWorkOrderFrame    MessageType = "work_order.frame"
	MessageWorkOrderTerminal MessageType = "work_order.terminal"
	MessageArtifactManifest  MessageType = "artifact.manifest"
	MessageBrowserRequest    MessageType = "browser.request"
	MessageBrowserCancel     MessageType = "browser.cancel"
	MessageBrowserState      MessageType = "browser.state"
	MessageBrowserResult     MessageType = "browser.result"
)

type Direction uint8

const (
	CloudToDevice Direction = iota + 1
	DeviceToCloud
)

type ProtocolErrorCode string

const (
	ProtocolFrameNotText      ProtocolErrorCode = "frame_not_text"
	ProtocolFrameTooLarge     ProtocolErrorCode = "frame_too_large"
	ProtocolInvalidJSON       ProtocolErrorCode = "invalid_json"
	ProtocolInvalidSchema     ProtocolErrorCode = "invalid_schema"
	ProtocolUnknownType       ProtocolErrorCode = "unknown_message_type"
	ProtocolWrongDirection    ProtocolErrorCode = "wrong_direction"
	ProtocolProhibitedField   ProtocolErrorCode = "prohibited_field"
	ProtocolFutureTimestamp   ProtocolErrorCode = "future_timestamp"
	ProtocolStaleEpoch        ProtocolErrorCode = "stale_epoch"
	ProtocolTenantMismatch    ProtocolErrorCode = "tenant_mismatch"
	ProtocolDeviceMismatch    ProtocolErrorCode = "device_mismatch"
	ProtocolSequenceGap       ProtocolErrorCode = "sequence_gap"
	ProtocolTerminalConflict  ProtocolErrorCode = "terminal_conflict"
	ProtocolDuplicateConflict ProtocolErrorCode = "duplicate_conflict"
)

type ProtocolError struct {
	Code    ProtocolErrorCode
	Message string
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

func IsProtocolError(err error, code ProtocolErrorCode) bool {
	var protocolErr *ProtocolError
	return errors.As(err, &protocolErr) && protocolErr.Code == code
}

type Envelope struct {
	ProtocolVersion string      `json:"protocol_version"`
	MessageID       string      `json:"message_id"`
	ConnectionEpoch string      `json:"connection_epoch"`
	Type            MessageType `json:"type"`
	SentAt          time.Time   `json:"sent_at"`
	Payload         any         `json:"payload"`
}

type WorkOrder struct {
	WorkOrderID              string          `json:"work_order_id"`
	IdempotencyKey           string          `json:"idempotency_key"`
	ProjectID                string          `json:"project_id"`
	OriginActorID            string          `json:"origin_actor_id"`
	Task                     WorkOrderTask   `json:"task"`
	WorkspaceGrantID         string          `json:"workspace_grant_id"`
	RequiredCapabilities     []Capability    `json:"required_capabilities"`
	RequestedArtifactClasses []ArtifactClass `json:"requested_artifact_classes"`
	Deadline                 time.Time       `json:"deadline"`
}

type WorkOrderTask struct {
	Kind        string             `json:"kind"`
	Instruction string             `json:"instruction,omitempty"`
	ToolCall    *WorkspaceToolCall `json:"tool_call,omitempty"`
}

type Capability string

const (
	CapabilityWorkspaceRead    Capability = "workspace.read"
	CapabilityWorkspaceWrite   Capability = "workspace.write"
	CapabilityProcessShell     Capability = "process.shell"
	CapabilityNetworkAccess    Capability = "network.access"
	CapabilityArtifactPatch    Capability = "artifact.patch"
	CapabilityArtifactSummary  Capability = "artifact.summary"
	CapabilityArtifactSelected Capability = "artifact.selected"
)

type ArtifactClass string

const (
	ArtifactPatch    ArtifactClass = "patch"
	ArtifactSummary  ArtifactClass = "summary"
	ArtifactSelected ArtifactClass = "selected"
)

type ActiveWorkOrder struct {
	WorkOrderID  string `json:"work_order_id"`
	State        string `json:"state"`
	LastSequence int64  `json:"last_sequence"`
}

type DeviceWelcomePayload struct {
	SelectedProtocolVersion string    `json:"selected_protocol_version"`
	ServerTime              time.Time `json:"server_time"`
	ReplayCursor            string    `json:"replay_cursor"`
	HeartbeatIntervalMS     int64     `json:"heartbeat_interval_ms"`
	LeaseTimeoutMS          int64     `json:"lease_timeout_ms"`
	RemoteExecutionEnabled  *bool     `json:"remote_execution_enabled,omitempty"`
	BrowserProtocol         string    `json:"browser_protocol,omitempty"`
}

type WorkOrderCancelPayload struct {
	WorkOrderID string `json:"work_order_id"`
	Reason      string `json:"reason"`
}

type DeviceHelloPayload struct {
	WorkspaceToolsVersion int               `json:"workspace_tools_version,omitempty"`
	DeviceID              string            `json:"device_id"`
	ProtocolMin           string            `json:"protocol_min"`
	ProtocolMax           string            `json:"protocol_max"`
	ClientVersion         string            `json:"client_version"`
	AionCoreVersion       string            `json:"aioncore_version"`
	CapabilityDigest      string            `json:"capability_digest"`
	ActiveWorkOrders      []ActiveWorkOrder `json:"active_work_orders"`
	BrowserProtocol       string            `json:"browser_protocol,omitempty"`
}

type DeviceHeartbeatPayload struct {
	ActiveWorkOrders []ActiveWorkOrder `json:"active_work_orders"`
	LastReplayCursor string            `json:"last_replay_cursor"`
}

type WorkOrderAcceptedPayload struct {
	WorkOrderID string    `json:"work_order_id"`
	AcceptedAt  time.Time `json:"accepted_at"`
}

type WorkOrderRejectedPayload struct {
	WorkOrderID string       `json:"work_order_id"`
	ReasonCode  RejectReason `json:"reason_code"`
	Detail      string       `json:"detail,omitempty"`
}

type RejectReason string

const (
	RejectProtocolIncompatible    RejectReason = "protocol_incompatible"
	RejectInvalidPayload          RejectReason = "invalid_payload"
	RejectPayloadTooLarge         RejectReason = "payload_too_large"
	RejectDuplicateConflict       RejectReason = "duplicate_conflict"
	RejectWorkspaceGrantMissing   RejectReason = "workspace_grant_missing"
	RejectWorkspaceGrantRevoked   RejectReason = "workspace_grant_revoked"
	RejectWorkspaceGrantExpired   RejectReason = "workspace_grant_expired"
	RejectWorkspaceScopeMismatch  RejectReason = "workspace_scope_mismatch"
	RejectCapabilityDenied        RejectReason = "capability_denied"
	RejectPolicyDenied            RejectReason = "policy_denied"
	RejectDeadlineExpired         RejectReason = "deadline_expired"
	RejectDeviceBusy              RejectReason = "device_busy"
	RejectSpoolFull               RejectReason = "spool_full"
	RejectLocalRuntimeUnavailable RejectReason = "local_runtime_unavailable"
)

type WorkOrderFramePayload struct {
	WorkOrderID string       `json:"work_order_id"`
	Sequence    int64        `json:"sequence"`
	Frame       RuntimeFrame `json:"frame"`
}

type RuntimeFrame struct {
	Kind         string   `json:"kind"`
	Channel      string   `json:"channel,omitempty"`
	Content      string   `json:"content,omitempty"`
	Phase        string   `json:"phase,omitempty"`
	CallID       string   `json:"call_id,omitempty"`
	ToolName     string   `json:"tool_name,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Category     string   `json:"category,omitempty"`
	Decision     string   `json:"decision,omitempty"`
	InputTokens  *int64   `json:"input_tokens,omitempty"`
	OutputTokens *int64   `json:"output_tokens,omitempty"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

type WorkOrderTerminalPayload struct {
	WorkOrderID string         `json:"work_order_id"`
	Sequence    int64          `json:"sequence"`
	Status      TerminalStatus `json:"status"`
	ReasonCode  TerminalReason `json:"reason_code,omitempty"`
	Summary     string         `json:"summary,omitempty"`
}

type TerminalStatus string

const (
	TerminalSucceeded TerminalStatus = "succeeded"
	TerminalFailed    TerminalStatus = "failed"
	TerminalCancelled TerminalStatus = "cancelled"
)

type TerminalReason string

const (
	TerminalCancelledByCloud          TerminalReason = "cancelled_by_cloud"
	TerminalCancelledByLocal          TerminalReason = "cancelled_by_local"
	TerminalDeadlineExceeded          TerminalReason = "deadline_exceeded"
	TerminalLocalRuntimeError         TerminalReason = "local_runtime_error"
	TerminalLocalRuntimeCrashed       TerminalReason = "local_runtime_crashed"
	TerminalLocalRuntimeProtocolError TerminalReason = "local_runtime_protocol_error"
	TerminalWorkspaceGrantRevoked     TerminalReason = "workspace_grant_revoked"
	TerminalConnectorShutdown         TerminalReason = "connector_shutdown"
	TerminalArtifactPolicyFailed      TerminalReason = "artifact_policy_failed"
	TerminalSpoolOverflow             TerminalReason = "spool_overflow"
)

type ArtifactManifestPayload struct {
	WorkOrderID string        `json:"work_order_id"`
	ArtifactID  string        `json:"artifact_id"`
	Class       ArtifactClass `json:"class"`
	DisplayName string        `json:"display_name"`
	MediaType   string        `json:"media_type"`
	SizeBytes   int64         `json:"size_bytes"`
	SHA256      string        `json:"sha256"`
}

var (
	identifierPattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	sha256Pattern        = regexp.MustCompile(`^[a-f0-9]{64}$`)
	timestampPattern     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?Z$`)
	prohibitedRemoteKeys = map[string]struct{}{
		"__proto__": {}, "argv": {}, "command": {}, "constructor": {}, "cwd": {}, "env": {},
		"executable": {}, "install_url": {}, "method": {}, "mcp": {}, "path": {}, "prototype": {},
		"provider_config": {}, "route": {}, "secret": {}, "shell": {}, "system_prompt": {}, "tool_definition": {},
	}
)

type wireEnvelope struct {
	ProtocolVersion string          `json:"protocol_version"`
	MessageID       string          `json:"message_id"`
	ConnectionEpoch string          `json:"connection_epoch"`
	Type            MessageType     `json:"type"`
	SentAt          string          `json:"sent_at"`
	Payload         json.RawMessage `json:"payload"`
}

func ParseEnvelope(raw []byte, direction Direction, now time.Time) (Envelope, error) {
	if len(raw) > ProtocolMaxFrameBytes {
		return Envelope{}, &ProtocolError{Code: ProtocolFrameTooLarge, Message: "Device Protocol frame exceeds 1 MiB"}
	}
	if !utf8.Valid(raw) {
		return Envelope{}, &ProtocolError{Code: ProtocolInvalidJSON, Message: "Device Protocol frame is not valid UTF-8 JSON"}
	}
	var generic any
	if err := decodeStrict(raw, &generic); err != nil {
		return Envelope{}, &ProtocolError{Code: ProtocolInvalidJSON, Message: "Device Protocol frame is not valid JSON"}
	}
	// A browser result is untrusted data, never a remote command. Its DOM
	// metadata may legitimately contain keys such as "method" or "path".
	object, _ := generic.(map[string]any)
	dataOnly := direction == DeviceToCloud && object["type"] == string(MessageBrowserResult)
	if key := findProhibitedKey(generic); key != "" && !dataOnly {
		return Envelope{}, &ProtocolError{Code: ProtocolProhibitedField, Message: "remote payload contains prohibited field: " + key}
	}
	var wire wireEnvelope
	if err := decodeStrict(raw, &wire); err != nil {
		return Envelope{}, &ProtocolError{Code: ProtocolInvalidSchema, Message: "Device Protocol envelope does not match v1 schema"}
	}
	if wire.ProtocolVersion != ProtocolVersion || !validIdentifier(wire.MessageID) || !validIdentifier(wire.ConnectionEpoch) || !timestampPattern.MatchString(wire.SentAt) {
		return Envelope{}, &ProtocolError{Code: ProtocolInvalidSchema, Message: "Device Protocol envelope identity or version is invalid"}
	}
	sentAt, err := time.Parse(time.RFC3339Nano, wire.SentAt)
	if err != nil || sentAt.Location() != time.UTC {
		return Envelope{}, &ProtocolError{Code: ProtocolInvalidSchema, Message: "Device Protocol sent_at is invalid"}
	}
	if !now.IsZero() && sentAt.After(now.Add(5*time.Minute)) {
		return Envelope{}, &ProtocolError{Code: ProtocolFutureTimestamp, Message: "Device Protocol sent_at is too far in the future"}
	}
	if !knownMessageType(wire.Type) {
		return Envelope{}, &ProtocolError{Code: ProtocolUnknownType, Message: "unknown Device Protocol message type"}
	}
	if !typeAllowed(wire.Type, direction) {
		return Envelope{}, &ProtocolError{Code: ProtocolWrongDirection, Message: fmt.Sprintf("message %s has the wrong direction", wire.Type)}
	}
	payload, err := decodePayload(wire.Type, wire.Payload)
	if err != nil {
		var protocolErr *ProtocolError
		if errors.As(err, &protocolErr) {
			return Envelope{}, err
		}
		return Envelope{}, &ProtocolError{Code: ProtocolInvalidSchema, Message: "Device Protocol payload does not match v1 schema"}
	}
	envelope := Envelope{ProtocolVersion: wire.ProtocolVersion, MessageID: wire.MessageID, ConnectionEpoch: wire.ConnectionEpoch, Type: wire.Type, SentAt: sentAt.UTC(), Payload: payload}
	if encoded, marshalErr := json.Marshal(envelope); marshalErr != nil || len(encoded) > ProtocolMaxFrameBytes {
		return Envelope{}, &ProtocolError{Code: ProtocolFrameTooLarge, Message: "Device Protocol frame exceeds 1 MiB"}
	}
	return envelope, nil
}

func MarshalEnvelope(envelope Envelope, direction Direction, now time.Time) ([]byte, error) {
	if envelope.ProtocolVersion == "" {
		envelope.ProtocolVersion = ProtocolVersion
	}
	if envelope.SentAt.IsZero() {
		envelope.SentAt = now.UTC()
	}
	if envelope.ProtocolVersion != ProtocolVersion || !validIdentifier(envelope.MessageID) || !validIdentifier(envelope.ConnectionEpoch) || !knownMessageType(envelope.Type) || !typeAllowed(envelope.Type, direction) {
		return nil, &ProtocolError{Code: ProtocolInvalidSchema, Message: "Device Protocol envelope is invalid"}
	}
	payload, err := normalizePayload(envelope.Type, envelope.Payload)
	if err != nil {
		return nil, err
	}
	wire := struct {
		ProtocolVersion string      `json:"protocol_version"`
		MessageID       string      `json:"message_id"`
		ConnectionEpoch string      `json:"connection_epoch"`
		Type            MessageType `json:"type"`
		SentAt          string      `json:"sent_at"`
		Payload         any         `json:"payload"`
	}{envelope.ProtocolVersion, envelope.MessageID, envelope.ConnectionEpoch, envelope.Type, envelope.SentAt.UTC().Format(time.RFC3339Nano), payload}
	data, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	if len(data) > ProtocolMaxFrameBytes {
		return nil, &ProtocolError{Code: ProtocolFrameTooLarge, Message: "Device Protocol frame exceeds 1 MiB"}
	}
	parsed, err := ParseEnvelope(data, direction, now)
	if err != nil {
		return nil, err
	}
	_ = parsed
	return data, nil
}

func decodePayload(kind MessageType, raw json.RawMessage) (any, error) {
	if isBrowserMessage(kind) {
		return decodeBrowserPayload(kind, raw)
	}
	if err := validateRawPayloadTimestamps(kind, raw); err != nil {
		return nil, err
	}
	var payload any
	switch kind {
	case MessageDeviceWelcome:
		payload = &DeviceWelcomePayload{}
	case MessageWorkOrderOffer:
		payload = &WorkOrder{}
	case MessageWorkOrderCancel:
		payload = &WorkOrderCancelPayload{}
	case MessageDeviceHello:
		payload = &DeviceHelloPayload{}
	case MessageDeviceHeartbeat:
		payload = &DeviceHeartbeatPayload{}
	case MessageWorkOrderAccepted:
		payload = &WorkOrderAcceptedPayload{}
	case MessageWorkOrderRejected:
		payload = &WorkOrderRejectedPayload{}
	case MessageWorkOrderFrame:
		payload = &WorkOrderFramePayload{}
	case MessageWorkOrderTerminal:
		payload = &WorkOrderTerminalPayload{}
	case MessageArtifactManifest:
		payload = &ArtifactManifestPayload{}
	default:
		return nil, &ProtocolError{Code: ProtocolUnknownType, Message: "unknown Device Protocol message type"}
	}
	if err := decodeStrict(raw, payload); err != nil {
		return nil, err
	}
	if err := validatePayload(kind, payload); err != nil {
		return nil, err
	}
	return dereferencePayload(payload), nil
}

func validateRawPayloadTimestamps(kind MessageType, raw json.RawMessage) error {
	field := ""
	switch kind {
	case MessageDeviceWelcome:
		field = "server_time"
	case MessageWorkOrderOffer:
		field = "deadline"
	case MessageWorkOrderAccepted:
		field = "accepted_at"
	default:
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	var value string
	encoded, found := object[field]
	if !found || json.Unmarshal(encoded, &value) != nil || !timestampPattern.MatchString(value) {
		return &ProtocolError{Code: ProtocolInvalidSchema, Message: "Device Protocol " + field + " must be an RFC3339 UTC timestamp"}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC {
		return &ProtocolError{Code: ProtocolInvalidSchema, Message: "Device Protocol " + field + " must be an RFC3339 UTC timestamp"}
	}
	return nil
}

func normalizePayload(kind MessageType, value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, &ProtocolError{Code: ProtocolInvalidSchema, Message: "Device Protocol payload cannot be encoded"}
	}
	return decodePayload(kind, data)
}

func dereferencePayload(value any) any {
	switch payload := value.(type) {
	case *DeviceWelcomePayload:
		return *payload
	case *WorkOrder:
		return *payload
	case *WorkOrderCancelPayload:
		return *payload
	case *DeviceHelloPayload:
		return *payload
	case *DeviceHeartbeatPayload:
		return *payload
	case *WorkOrderAcceptedPayload:
		return *payload
	case *WorkOrderRejectedPayload:
		return *payload
	case *WorkOrderFramePayload:
		return *payload
	case *WorkOrderTerminalPayload:
		return *payload
	case *ArtifactManifestPayload:
		return *payload
	default:
		return value
	}
}

func validatePayload(kind MessageType, value any) error {
	invalid := func(message string) error { return &ProtocolError{Code: ProtocolInvalidSchema, Message: message} }
	switch payload := value.(type) {
	case *DeviceWelcomePayload:
		if payload.BrowserProtocol != "" && payload.BrowserProtocol != devicebrowser.Version {
			return invalid("unsupported browser protocol")
		}
		if kind != MessageDeviceWelcome || payload.SelectedProtocolVersion != ProtocolVersion || payload.ServerTime.IsZero() || !bounded(payload.ReplayCursor, 1, 512) || payload.HeartbeatIntervalMS < 5000 || payload.HeartbeatIntervalMS > 60000 || payload.LeaseTimeoutMS < 15000 || payload.LeaseTimeoutMS > 180000 {
			return invalid("device welcome payload is invalid")
		}
	case *WorkOrder:
		return validateWorkOrder(*payload)
	case *WorkOrderCancelPayload:
		if !validIdentifier(payload.WorkOrderID) || !bounded(payload.Reason, 1, 512) {
			return invalid("work order cancel payload is invalid")
		}
	case *DeviceHelloPayload:
		if payload.WorkspaceToolsVersion < 0 || payload.WorkspaceToolsVersion > 1 {
			return invalid("unsupported workspace tools version")
		}
		if payload.BrowserProtocol != "" && payload.BrowserProtocol != devicebrowser.Version {
			return invalid("unsupported browser protocol")
		}
		if !validIdentifier(payload.DeviceID) || payload.ProtocolMin != ProtocolVersion || payload.ProtocolMax != ProtocolVersion || !bounded(payload.ClientVersion, 1, 64) || !bounded(payload.AionCoreVersion, 1, 64) || !sha256Pattern.MatchString(payload.CapabilityDigest) || len(payload.ActiveWorkOrders) > 64 || validateActiveWorkOrders(payload.ActiveWorkOrders) != nil {
			return invalid("device hello payload is invalid")
		}
	case *DeviceHeartbeatPayload:
		if len(payload.ActiveWorkOrders) > 64 || validateActiveWorkOrders(payload.ActiveWorkOrders) != nil || !bounded(payload.LastReplayCursor, 1, 512) {
			return invalid("device heartbeat payload is invalid")
		}
	case *WorkOrderAcceptedPayload:
		if !validIdentifier(payload.WorkOrderID) || payload.AcceptedAt.IsZero() {
			return invalid("work order accepted payload is invalid")
		}
	case *WorkOrderRejectedPayload:
		if !validIdentifier(payload.WorkOrderID) || !validRejectReason(payload.ReasonCode) || !bounded(payload.Detail, 0, 512) {
			return invalid("work order rejected payload is invalid")
		}
	case *WorkOrderFramePayload:
		if !validIdentifier(payload.WorkOrderID) || payload.Sequence < 1 || payload.Sequence > maxSafeInteger {
			return invalid("work order frame identity is invalid")
		}
		return validateRuntimeFrame(payload.Frame)
	case *WorkOrderTerminalPayload:
		if !validIdentifier(payload.WorkOrderID) || payload.Sequence < 1 || payload.Sequence > maxSafeInteger || !validTerminalStatus(payload.Status) || (payload.ReasonCode != "" && !validTerminalReason(payload.ReasonCode)) || !bounded(payload.Summary, 0, 16384) {
			return invalid("work order terminal payload is invalid")
		}
	case *ArtifactManifestPayload:
		if !validIdentifier(payload.WorkOrderID) || !validIdentifier(payload.ArtifactID) || !validArtifactClass(payload.Class) || !bounded(payload.DisplayName, 1, 255) || !bounded(payload.MediaType, 3, 128) || payload.SizeBytes < 0 || payload.SizeBytes > 1073741824 || !sha256Pattern.MatchString(payload.SHA256) {
			return invalid("artifact manifest payload is invalid")
		}
	default:
		return invalid("Device Protocol payload type is invalid")
	}
	return nil
}

func validateWorkOrder(order WorkOrder) error {
	invalid := func(message string) error { return &ProtocolError{Code: ProtocolInvalidSchema, Message: message} }
	if !validIdentifier(order.WorkOrderID) || !bounded(order.IdempotencyKey, 1, 256) || !validIdentifier(order.ProjectID) || !validIdentifier(order.OriginActorID) || !validIdentifier(order.WorkspaceGrantID) || order.Deadline.IsZero() || len(order.RequiredCapabilities) > 16 || len(order.RequestedArtifactClasses) > 3 {
		return invalid("work order payload is invalid")
	}
	if order.Task.Kind == "agent_turn" {
		if order.Task.ToolCall != nil || !bounded(order.Task.Instruction, 1, 65536) {
			return invalid("invalid agent task")
		}
	} else if order.Task.Kind == "workspace_tool" {
		if order.Task.Instruction != "" || order.Task.ToolCall == nil {
			return invalid("invalid workspace tool task")
		}
		if err := order.Task.ToolCall.Validate(); err != nil {
			return invalid(err.Error())
		}
		caps := order.Task.ToolCall.Capabilities()
		if len(caps) != len(order.RequiredCapabilities) {
			return invalid("workspace tool capability mismatch")
		}
		for _, cap := range caps {
			found := false
			for _, actual := range order.RequiredCapabilities {
				found = found || cap == actual
			}
			if !found {
				return invalid("workspace tool capability mismatch")
			}
		}
		if len(order.RequestedArtifactClasses) != 0 {
			return invalid("workspace tools return explicit results only")
		}
	} else {
		return invalid("unknown task kind")
	}
	seenCapabilities := map[Capability]struct{}{}
	for _, capability := range order.RequiredCapabilities {
		if !validCapability(capability) {
			return invalid("work order capability is invalid")
		}
		if _, exists := seenCapabilities[capability]; exists {
			return invalid("work order capabilities must be unique")
		}
		seenCapabilities[capability] = struct{}{}
	}
	seenClasses := map[ArtifactClass]struct{}{}
	for _, class := range order.RequestedArtifactClasses {
		if !validArtifactClass(class) {
			return invalid("work order artifact class is invalid")
		}
		if _, exists := seenClasses[class]; exists {
			return invalid("work order artifact classes must be unique")
		}
		seenClasses[class] = struct{}{}
	}
	return nil
}

func validateRuntimeFrame(frame RuntimeFrame) error {
	invalid := func(message string) error { return &ProtocolError{Code: ProtocolInvalidSchema, Message: message} }
	switch frame.Kind {
	case "tool_result":
		copy := frame
		copy.Kind, copy.CallID, copy.Content = "detached", "", ""
		copy.Reason = "workspace tool result"
		if !validIdentifier(frame.CallID) || len(frame.Content) > 196608 || !json.Valid([]byte(frame.Content)) {
			return invalid("invalid workspace tool result")
		}
		return validateRuntimeFrame(copy)
	case "text":
		if (frame.Channel != "output" && frame.Channel != "thinking") || !bounded(frame.Content, 0, 262144) || frame.Phase != "" || frame.CallID != "" || frame.ToolName != "" || frame.Summary != "" || frame.Category != "" || frame.Decision != "" || frame.InputTokens != nil || frame.OutputTokens != nil || frame.CostUSD != nil || frame.Reason != "" {
			return invalid("text runtime frame is invalid")
		}
	case "tool":
		if (frame.Phase != "started" && frame.Phase != "completed" && frame.Phase != "failed") || !validIdentifier(frame.CallID) || !bounded(frame.ToolName, 1, 128) || !bounded(frame.Summary, 0, 4096) || frame.Channel != "" || frame.Content != "" || frame.Category != "" || frame.Decision != "" || frame.InputTokens != nil || frame.OutputTokens != nil || frame.CostUSD != nil || frame.Reason != "" {
			return invalid("tool runtime frame is invalid")
		}
	case "permission":
		if (frame.Phase != "waiting" && frame.Phase != "resolved") || !validIdentifier(frame.CallID) || !oneOf(frame.Category, "workspace", "shell", "network", "mcp", "credential", "other") || (frame.Decision != "" && !oneOf(frame.Decision, "allow_once", "allow_session", "deny")) || frame.Channel != "" || frame.Content != "" || frame.ToolName != "" || frame.Summary != "" || frame.InputTokens != nil || frame.OutputTokens != nil || frame.CostUSD != nil || frame.Reason != "" {
			return invalid("permission runtime frame is invalid")
		}
	case "usage":
		if frame.InputTokens == nil || frame.OutputTokens == nil || *frame.InputTokens < 0 || *frame.OutputTokens < 0 || (frame.CostUSD != nil && *frame.CostUSD < 0) || frame.Channel != "" || frame.Content != "" || frame.Phase != "" || frame.CallID != "" || frame.ToolName != "" || frame.Summary != "" || frame.Category != "" || frame.Decision != "" || frame.Reason != "" {
			return invalid("usage runtime frame is invalid")
		}
	case "detached":
		if !bounded(frame.Reason, 1, 512) || frame.Channel != "" || frame.Content != "" || frame.Phase != "" || frame.CallID != "" || frame.ToolName != "" || frame.Summary != "" || frame.Category != "" || frame.Decision != "" || frame.InputTokens != nil || frame.OutputTokens != nil || frame.CostUSD != nil {
			return invalid("detached runtime frame is invalid")
		}
	default:
		return invalid("runtime frame kind is invalid")
	}
	return nil
}

func validateActiveWorkOrders(active []ActiveWorkOrder) error {
	for _, work := range active {
		if !validIdentifier(work.WorkOrderID) || !oneOf(work.State, "accepted", "running", "waiting_local_approval", "detached", "cancelling") || work.LastSequence < 0 || work.LastSequence > maxSafeInteger {
			return errors.New("invalid active work order")
		}
	}
	return nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func findProhibitedKey(value any) string {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			if key := findProhibitedKey(item); key != "" {
				return key
			}
		}
	case map[string]any:
		for key, item := range value {
			if _, prohibited := prohibitedRemoteKeys[strings.ToLower(key)]; prohibited {
				return key
			}
			if nested := findProhibitedKey(item); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func knownMessageType(kind MessageType) bool {
	if isBrowserMessage(kind) {
		return true
	}
	switch kind {
	case MessageDeviceWelcome, MessageWorkOrderOffer, MessageWorkOrderCancel, MessageDeviceHello, MessageDeviceHeartbeat, MessageWorkOrderAccepted, MessageWorkOrderRejected, MessageWorkOrderFrame, MessageWorkOrderTerminal, MessageArtifactManifest:
		return true
	default:
		return false
	}
}

func typeAllowed(kind MessageType, direction Direction) bool {
	if direction == CloudToDevice {
		return kind == MessageDeviceWelcome || kind == MessageWorkOrderOffer || kind == MessageWorkOrderCancel || kind == MessageBrowserRequest || kind == MessageBrowserCancel
	}
	if direction == DeviceToCloud {
		return kind == MessageDeviceHello || kind == MessageDeviceHeartbeat || kind == MessageWorkOrderAccepted || kind == MessageWorkOrderRejected || kind == MessageWorkOrderFrame || kind == MessageWorkOrderTerminal || kind == MessageArtifactManifest || kind == MessageBrowserState || kind == MessageBrowserResult
	}
	return false
}

func validIdentifier(value string) bool {
	return bounded(value, 3, 160) && identifierPattern.MatchString(value)
}
func bounded(value string, minimum, maximum int) bool {
	count := utf8.RuneCountInString(value)
	return count >= minimum && count <= maximum
}
func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func validCapability(value Capability) bool {
	return oneOf(string(value), string(CapabilityWorkspaceRead), string(CapabilityWorkspaceWrite), string(CapabilityProcessShell), string(CapabilityNetworkAccess), string(CapabilityArtifactPatch), string(CapabilityArtifactSummary), string(CapabilityArtifactSelected))
}
func validArtifactClass(value ArtifactClass) bool {
	return oneOf(string(value), string(ArtifactPatch), string(ArtifactSummary), string(ArtifactSelected))
}
func validRejectReason(value RejectReason) bool {
	return oneOf(string(value), string(RejectProtocolIncompatible), string(RejectInvalidPayload), string(RejectPayloadTooLarge), string(RejectDuplicateConflict), string(RejectWorkspaceGrantMissing), string(RejectWorkspaceGrantRevoked), string(RejectWorkspaceGrantExpired), string(RejectWorkspaceScopeMismatch), string(RejectCapabilityDenied), string(RejectPolicyDenied), string(RejectDeadlineExpired), string(RejectDeviceBusy), string(RejectSpoolFull), string(RejectLocalRuntimeUnavailable))
}
func validTerminalStatus(value TerminalStatus) bool {
	return oneOf(string(value), string(TerminalSucceeded), string(TerminalFailed), string(TerminalCancelled))
}
func validTerminalReason(value TerminalReason) bool {
	return oneOf(string(value), string(TerminalCancelledByCloud), string(TerminalCancelledByLocal), string(TerminalDeadlineExceeded), string(TerminalLocalRuntimeError), string(TerminalLocalRuntimeCrashed), string(TerminalLocalRuntimeProtocolError), string(TerminalWorkspaceGrantRevoked), string(TerminalConnectorShutdown), string(TerminalArtifactPolicyFailed), string(TerminalSpoolOverflow))
}
