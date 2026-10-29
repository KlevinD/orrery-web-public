package device

import (
	"encoding/json"
	"regexp"
	"time"

	devicebrowser "orrery_v2/internal/device/browser"
)

type BrowserRequestPayload struct {
	RequestID string         `json:"request_id"`
	Operation string         `json:"operation"`
	Arguments map[string]any `json:"arguments"`
	Deadline  time.Time      `json:"deadline"`
	TraceID   string         `json:"trace_id"`
}

type BrowserCancelPayload struct {
	RequestID string `json:"request_id"`
}
type BrowserStatePayload struct {
	Enabled   *bool `json:"enabled"`
	Connected *bool `json:"connected"`
}
type BrowserError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *BrowserError) Error() string { return e.Code + ": " + e.Message }

type BrowserResultPayload struct {
	RequestID string          `json:"request_id"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *BrowserError   `json:"error,omitempty"`
}

var browserErrorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)

func isBrowserMessage(kind MessageType) bool {
	return kind == MessageBrowserRequest || kind == MessageBrowserCancel || kind == MessageBrowserState || kind == MessageBrowserResult
}

func decodeBrowserPayload(kind MessageType, raw json.RawMessage) (any, error) {
	invalid := &ProtocolError{Code: ProtocolInvalidSchema, Message: "invalid fixed browser payload"}
	switch kind {
	case MessageBrowserRequest:
		var p BrowserRequestPayload
		if decodeStrict(raw, &p) != nil || !validIdentifier(p.RequestID) || !devicebrowser.ValidToken(p.TraceID) || validateRawBrowserDeadline(raw) != nil || devicebrowser.ValidateArguments(p.Operation, p.Arguments) != nil {
			return nil, invalid
		}
		return p, nil
	case MessageBrowserCancel:
		var p BrowserCancelPayload
		if decodeStrict(raw, &p) != nil || !validIdentifier(p.RequestID) {
			return nil, invalid
		}
		return p, nil
	case MessageBrowserState:
		var p BrowserStatePayload
		if decodeStrict(raw, &p) != nil || p.Enabled == nil || p.Connected == nil || (*p.Connected && !*p.Enabled) {
			return nil, invalid
		}
		return p, nil
	case MessageBrowserResult:
		var p BrowserResultPayload
		if decodeStrict(raw, &p) != nil || !validIdentifier(p.RequestID) || ((len(p.Result) == 0) == (p.Error == nil)) {
			return nil, invalid
		}
		if p.Error != nil && (!browserErrorCode.MatchString(p.Error.Code) || !bounded(p.Error.Message, 0, 512)) {
			return nil, invalid
		}
		if len(p.Result) > devicebrowser.MaxResultBytes {
			return nil, invalid
		}
		if len(p.Result) > 0 {
			var object map[string]any
			if json.Unmarshal(p.Result, &object) != nil || object == nil {
				return nil, invalid
			}
		}
		return p, nil
	}
	return nil, invalid
}

func validateRawBrowserDeadline(raw []byte) error {
	var p struct {
		Deadline string `json:"deadline"`
	}
	if json.Unmarshal(raw, &p) != nil || !timestampPattern.MatchString(p.Deadline) {
		return &ProtocolError{Code: ProtocolInvalidSchema}
	}
	return nil
}
