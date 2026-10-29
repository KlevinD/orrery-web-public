// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Reference: aionui-session/backend/types.rs@v0.1.52.

type RuntimeCommandKind string

const (
	RuntimeCommandSend             RuntimeCommandKind = "send"
	RuntimeCommandCancel           RuntimeCommandKind = "cancel"
	RuntimeCommandSteer            RuntimeCommandKind = "steer"
	RuntimeCommandAnswerPermission RuntimeCommandKind = "answer_permission"
	RuntimeCommandAnswerAuth       RuntimeCommandKind = "answer_auth"
	RuntimeCommandAcknowledge      RuntimeCommandKind = "acknowledge"
	RuntimeCommandSetMode          RuntimeCommandKind = "set_mode"
	RuntimeCommandSetModel         RuntimeCommandKind = "set_model"
	RuntimeCommandSetConfigOption  RuntimeCommandKind = "set_config_option"
	RuntimeCommandRewind           RuntimeCommandKind = "rewind"
	RuntimeCommandListCheckpoints  RuntimeCommandKind = "list_checkpoints"
	RuntimeCommandQuerySessionInfo RuntimeCommandKind = "query_session_info"
)

type RuntimeCommand struct {
	CommandID        string                   `json:"command_id"`
	Kind             RuntimeCommandKind       `json:"kind"`
	Send             *SendCommand             `json:"send,omitempty"`
	Cancel           *CancelCommand           `json:"cancel,omitempty"`
	Steer            *SteerCommand            `json:"steer,omitempty"`
	AnswerPermission *AnswerPermissionCommand `json:"answer_permission,omitempty"`
	AnswerAuth       *AnswerAuthCommand       `json:"answer_auth,omitempty"`
	Acknowledge      *AcknowledgeCommand      `json:"acknowledge,omitempty"`
	SetMode          *SetModeCommand          `json:"set_mode,omitempty"`
	SetModel         *SetModelCommand         `json:"set_model,omitempty"`
	SetConfigOption  *SetConfigOptionCommand  `json:"set_config_option,omitempty"`
	Rewind           *RewindCommand           `json:"rewind,omitempty"`
	ListCheckpoints  *ListCheckpointsCommand  `json:"list_checkpoints,omitempty"`
	QuerySessionInfo *QuerySessionInfoCommand `json:"query_session_info,omitempty"`
}

type ContentBlockKind string

const (
	ContentBlockText         ContentBlockKind = "text"
	ContentBlockImage        ContentBlockKind = "image"
	ContentBlockAudio        ContentBlockKind = "audio"
	ContentBlockResourceLink ContentBlockKind = "resource_link"
	ContentBlockAtMention    ContentBlockKind = "at_mention"
)

type ContentBlock struct {
	Kind      ContentBlockKind `json:"kind"`
	Text      string           `json:"text,omitempty"`
	Data      []byte           `json:"data,omitempty"`
	MediaType string           `json:"media_type,omitempty"`
	URI       string           `json:"uri,omitempty"`
	UserID    string           `json:"user_id,omitempty"`
}

type CommandMetadata struct {
	ClientMsgID string `json:"client_msg_id,omitempty"`
}

type SendCommand struct {
	Content  []ContentBlock  `json:"content"`
	Metadata CommandMetadata `json:"metadata"`
}

type CancelTargetKind string

const (
	CancelTargetTool    CancelTargetKind = "tool"
	CancelTargetTurn    CancelTargetKind = "turn"
	CancelTargetSession CancelTargetKind = "session"
)

type CancelCommand struct {
	Target CancelTargetKind `json:"target"`
	ToolID string           `json:"tool_id,omitempty"`
	Reason string           `json:"reason,omitempty"`
}

type SteerCommand struct {
	Content []ContentBlock `json:"content"`
}

type PermissionDecision string

const (
	PermissionApproved    PermissionDecision = "approved"
	PermissionDenied      PermissionDecision = "denied"
	PermissionAllowAlways PermissionDecision = "allow_always"
)

type QuestionAnswer struct {
	Question string   `json:"question"`
	Labels   []string `json:"labels"`
}

type AnswerPermissionCommand struct {
	RequestID string             `json:"request_id"`
	Decision  PermissionDecision `json:"decision"`
	Selected  string             `json:"selected,omitempty"`
	Answers   []QuestionAnswer   `json:"answers,omitempty"`
}

type AnswerAuthCommand struct {
	RequestID   string          `json:"request_id"`
	MethodID    string          `json:"method_id"`
	Credentials json.RawMessage `json:"-"`
}

type AcknowledgeCommand struct {
	NodeID string `json:"node_id"`
}

type SetModeCommand struct {
	Mode string `json:"mode"`
}

type SetModelCommand struct {
	Model string `json:"model"`
}

type SetConfigOptionCommand struct {
	OptionID string `json:"option_id"`
	Value    string `json:"value"`
}

type RewindCommand struct {
	NumTurns uint32 `json:"num_turns"`
}

type ListCheckpointsCommand struct{}

type SessionInfoKind string

const (
	SessionInfoContextUsage SessionInfoKind = "context_usage"
	SessionInfoSessionCost  SessionInfoKind = "session_cost"
)

type QuerySessionInfoCommand struct {
	Kind SessionInfoKind `json:"kind"`
}

type CommandAdmission string

const (
	AdmissionStarted CommandAdmission = "started"
	AdmissionQueued  CommandAdmission = "queued"
	AdmissionNoTurn  CommandAdmission = "no_turn"
)

type CommandReceipt struct {
	Accepted       bool             `json:"accepted"`
	Admission      CommandAdmission `json:"admission"`
	TurnGeneration uint64           `json:"turn_generation"`
}

func (c RuntimeCommand) Validate() error {
	if strings.TrimSpace(c.CommandID) == "" {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "command_id is required", false)
	}
	if !knownRuntimeCommandKind(c.Kind) {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidCommand, string(c.Kind), false)
	}
	if c.payloadCount() != 1 || !c.payloadMatchesKind() {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "command must contain exactly its matching payload", false)
	}
	switch c.Kind {
	case RuntimeCommandSend:
		return validateContent(c.Send.Content)
	case RuntimeCommandCancel:
		switch c.Cancel.Target {
		case CancelTargetTool:
			if strings.TrimSpace(c.Cancel.ToolID) == "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "tool cancel requires tool_id", false)
			}
		case CancelTargetTurn, CancelTargetSession:
			if strings.TrimSpace(c.Cancel.ToolID) != "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "non-tool cancel cannot carry tool_id", false)
			}
		default:
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid cancel target", false)
		}
	case RuntimeCommandSteer:
		return validateContent(c.Steer.Content)
	case RuntimeCommandAnswerPermission:
		if strings.TrimSpace(c.AnswerPermission.RequestID) == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "permission request_id is required", false)
		}
		switch c.AnswerPermission.Decision {
		case PermissionApproved, PermissionDenied, PermissionAllowAlways:
		default:
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid permission decision", false)
		}
		for _, answer := range c.AnswerPermission.Answers {
			if strings.TrimSpace(answer.Question) == "" || len(answer.Labels) == 0 {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "question answers require question and labels", false)
			}
			for _, label := range answer.Labels {
				if strings.TrimSpace(label) == "" {
					return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "question answer labels cannot be empty", false)
				}
			}
		}
	case RuntimeCommandAnswerAuth:
		if strings.TrimSpace(c.AnswerAuth.RequestID) == "" || strings.TrimSpace(c.AnswerAuth.MethodID) == "" || len(c.AnswerAuth.Credentials) == 0 || !json.Valid(c.AnswerAuth.Credentials) {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "auth response requires request_id, method_id, and valid credentials", false)
		}
		if len(c.AnswerAuth.Credentials) > 64*1024 {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "auth credentials exceed delivery limit", false)
		}
		var credentials map[string]any
		if err := json.Unmarshal(c.AnswerAuth.Credentials, &credentials); err != nil || len(credentials) == 0 {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "auth credentials must be a non-empty JSON object", false)
		}
	case RuntimeCommandAcknowledge:
		if strings.TrimSpace(c.Acknowledge.NodeID) == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "node_id is required", false)
		}
	case RuntimeCommandSetMode:
		if strings.TrimSpace(c.SetMode.Mode) == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "mode is required", false)
		}
	case RuntimeCommandSetModel:
		if strings.TrimSpace(c.SetModel.Model) == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "model is required", false)
		}
	case RuntimeCommandSetConfigOption:
		if strings.TrimSpace(c.SetConfigOption.OptionID) == "" || strings.TrimSpace(c.SetConfigOption.Value) == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "config option id and value are required", false)
		}
	case RuntimeCommandRewind:
		if c.Rewind.NumTurns == 0 {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "rewind num_turns must be positive", false)
		}
	case RuntimeCommandQuerySessionInfo:
		if c.QuerySessionInfo.Kind != SessionInfoContextUsage && c.QuerySessionInfo.Kind != SessionInfoSessionCost {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid session info kind", false)
		}
	}
	return nil
}

func (c RuntimeCommand) Fingerprint() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	copy := c
	copy.CommandID = ""
	if copy.AnswerAuth != nil {
		copy.AnswerAuth = &AnswerAuthCommand{RequestID: copy.AnswerAuth.RequestID, MethodID: copy.AnswerAuth.MethodID}
	}
	data, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("marshal command fingerprint: %w", err)
	}
	if c.AnswerAuth != nil {
		credentialDigest := sha256.Sum256(c.AnswerAuth.Credentials)
		data = append(data, credentialDigest[:]...)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (r CommandReceipt) Validate() error {
	if !r.Accepted {
		if r.Admission != "" || r.TurnGeneration != 0 {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "rejected receipt cannot carry admission or generation", false)
		}
		return nil
	}
	switch r.Admission {
	case AdmissionStarted:
		if r.TurnGeneration == 0 {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "started admission requires a generation", false)
		}
	case AdmissionQueued:
		// A conversation-owned Held message has no backend generation yet. Once
		// the backend accepts it, the returned receipt carries its generation.
	case AdmissionNoTurn:
	default:
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid command admission", false)
	}
	return nil
}

func (c RuntimeCommand) payloadCount() int {
	count := 0
	for _, present := range []bool{c.Send != nil, c.Cancel != nil, c.Steer != nil, c.AnswerPermission != nil, c.AnswerAuth != nil, c.Acknowledge != nil, c.SetMode != nil, c.SetModel != nil, c.SetConfigOption != nil, c.Rewind != nil, c.ListCheckpoints != nil, c.QuerySessionInfo != nil} {
		if present {
			count++
		}
	}
	return count
}

func (c RuntimeCommand) payloadMatchesKind() bool {
	switch c.Kind {
	case RuntimeCommandSend:
		return c.Send != nil
	case RuntimeCommandCancel:
		return c.Cancel != nil
	case RuntimeCommandSteer:
		return c.Steer != nil
	case RuntimeCommandAnswerPermission:
		return c.AnswerPermission != nil
	case RuntimeCommandAnswerAuth:
		return c.AnswerAuth != nil
	case RuntimeCommandAcknowledge:
		return c.Acknowledge != nil
	case RuntimeCommandSetMode:
		return c.SetMode != nil
	case RuntimeCommandSetModel:
		return c.SetModel != nil
	case RuntimeCommandSetConfigOption:
		return c.SetConfigOption != nil
	case RuntimeCommandRewind:
		return c.Rewind != nil
	case RuntimeCommandListCheckpoints:
		return c.ListCheckpoints != nil
	case RuntimeCommandQuerySessionInfo:
		return c.QuerySessionInfo != nil
	default:
		return false
	}
}

func knownRuntimeCommandKind(kind RuntimeCommandKind) bool {
	switch kind {
	case RuntimeCommandSend, RuntimeCommandCancel, RuntimeCommandSteer, RuntimeCommandAnswerPermission, RuntimeCommandAnswerAuth, RuntimeCommandAcknowledge, RuntimeCommandSetMode, RuntimeCommandSetModel, RuntimeCommandSetConfigOption, RuntimeCommandRewind, RuntimeCommandListCheckpoints, RuntimeCommandQuerySessionInfo:
		return true
	default:
		return false
	}
}

func validateContent(content []ContentBlock) error {
	if len(content) == 0 {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "content is required", false)
	}
	for _, block := range content {
		switch block.Kind {
		case ContentBlockText:
			if block.Text == "" || len(block.Data) != 0 || block.MediaType != "" || block.URI != "" || block.UserID != "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid text block", false)
			}
		case ContentBlockImage, ContentBlockAudio:
			if len(block.Data) == 0 || strings.TrimSpace(block.MediaType) == "" || block.Text != "" || block.URI != "" || block.UserID != "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid binary content block", false)
			}
		case ContentBlockResourceLink:
			if strings.TrimSpace(block.URI) == "" || block.Text != "" || len(block.Data) != 0 || block.UserID != "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid resource link block", false)
			}
		case ContentBlockAtMention:
			if strings.TrimSpace(block.UserID) == "" || block.Text != "" || len(block.Data) != 0 || block.MediaType != "" || block.URI != "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid mention block", false)
			}
		default:
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "unknown content block kind", false)
		}
	}
	return nil
}
