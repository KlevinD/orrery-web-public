// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package core

import "strings"

// Reference: aionui-session/capability.rs@v0.1.52.

type CapabilityTier string

const (
	CapabilityParsed CapabilityTier = "parsed"
	CapabilityHook   CapabilityTier = "hook"
	CapabilityOpaque CapabilityTier = "opaque"
)

type PromptAcceptedSource string

const (
	PromptAcceptedNone        PromptAcceptedSource = "none"
	PromptAcceptedNative      PromptAcceptedSource = "native"
	PromptAcceptedSynthesized PromptAcceptedSource = "synthesized"
)

type CommandSet struct {
	Send             bool `json:"send"`
	CancelTurn       bool `json:"cancel_turn"`
	CancelSession    bool `json:"cancel_session"`
	CancelTool       bool `json:"cancel_tool"`
	Steer            bool `json:"steer"`
	AnswerPermission bool `json:"answer_permission"`
	AnswerAuth       bool `json:"answer_auth"`
	Acknowledge      bool `json:"acknowledge"`
	SetMode          bool `json:"set_mode"`
	SetModel         bool `json:"set_model"`
	SetConfigOption  bool `json:"set_config_option"`
	Rewind           bool `json:"rewind"`
	ListCheckpoints  bool `json:"list_checkpoints"`
	QuerySessionInfo bool `json:"query_session_info"`
}

type BlockSet struct {
	Text      bool `json:"text"`
	Image     bool `json:"image"`
	Audio     bool `json:"audio"`
	Resource  bool `json:"resource"`
	AtMention bool `json:"at_mention"`
}

func (s BlockSet) Allows(kind ContentBlockKind) bool {
	switch kind {
	case ContentBlockText:
		return s.Text
	case ContentBlockImage:
		return s.Image
	case ContentBlockAudio:
		return s.Audio
	case ContentBlockResourceLink:
		return s.Resource
	case ContentBlockAtMention:
		return s.AtMention
	default:
		return false
	}
}

type SignalSet struct {
	Heartbeat      bool `json:"heartbeat"`
	ToolLifecycle  bool `json:"tool_lifecycle"`
	TerminalResult bool `json:"terminal_result"`
	Usage          bool `json:"usage"`
	Plan           bool `json:"plan"`
	Subagent       bool `json:"subagent"`
}

type ModelInfo struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}

type ModeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ConfigOptionInfo struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Values  []string `json:"values"`
	Current string   `json:"current,omitempty"`
}

type SlashCommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type TransportEvidence struct {
	Provider string `json:"provider"`
	Protocol string `json:"protocol"`
	Version  string `json:"version,omitempty"`
}

type Capabilities struct {
	Tier                  CapabilityTier       `json:"tier"`
	SupportedCommands     CommandSet           `json:"supported_commands"`
	PromptBlocks          BlockSet             `json:"prompt_blocks"`
	PromptAccepted        PromptAcceptedSource `json:"prompt_accepted"`
	Signals               SignalSet            `json:"signals"`
	AvailableModels       []ModelInfo          `json:"available_models,omitempty"`
	AvailableModes        []ModeInfo           `json:"available_modes,omitempty"`
	ConfigOptions         []ConfigOptionInfo   `json:"config_options,omitempty"`
	CurrentModel          string               `json:"current_model,omitempty"`
	CurrentMode           string               `json:"current_mode,omitempty"`
	AuthMethods           []string             `json:"auth_methods,omitempty"`
	SlashCommands         []SlashCommandInfo   `json:"slash_commands,omitempty"`
	AcceptsProactiveInput bool                 `json:"accepts_proactive_input"`
	SupportsQueue         bool                 `json:"supports_queue"`
	SupportsMultiTurn     bool                 `json:"supports_multi_turn"`
	SupportsResume        bool                 `json:"supports_resume"`
	SupportsIdleSuspend   bool                 `json:"supports_idle_suspend"`
	PendingLimit          uint32               `json:"pending_limit,omitempty"`
	PayloadLimitBytes     uint64               `json:"payload_limit_bytes,omitempty"`
	Transport             TransportEvidence    `json:"transport"`
}

func (c Capabilities) Supports(command RuntimeCommand) bool {
	if command.Validate() != nil || c.Validate() != nil {
		return false
	}
	switch command.Kind {
	case RuntimeCommandSend:
		if !c.SupportedCommands.Send {
			return false
		}
		for _, block := range command.Send.Content {
			if !c.PromptBlocks.Allows(block.Kind) {
				return false
			}
		}
		return true
	case RuntimeCommandCancel:
		switch command.Cancel.Target {
		case CancelTargetTool:
			return c.SupportedCommands.CancelTool
		case CancelTargetTurn:
			return c.SupportedCommands.CancelTurn
		case CancelTargetSession:
			return c.SupportedCommands.CancelSession
		}
	case RuntimeCommandSteer:
		return c.SupportedCommands.Steer
	case RuntimeCommandAnswerPermission:
		return c.SupportedCommands.AnswerPermission
	case RuntimeCommandAnswerAuth:
		return c.SupportedCommands.AnswerAuth
	case RuntimeCommandAcknowledge:
		return c.SupportedCommands.Acknowledge
	case RuntimeCommandSetMode:
		return c.SupportedCommands.SetMode
	case RuntimeCommandSetModel:
		return c.SupportedCommands.SetModel
	case RuntimeCommandSetConfigOption:
		return c.SupportedCommands.SetConfigOption
	case RuntimeCommandRewind:
		return c.SupportedCommands.Rewind
	case RuntimeCommandListCheckpoints:
		return c.SupportedCommands.ListCheckpoints
	case RuntimeCommandQuerySessionInfo:
		return c.SupportedCommands.QuerySessionInfo
	}
	return false
}

func (c Capabilities) Validate() error {
	switch c.Tier {
	case "", CapabilityParsed, CapabilityHook, CapabilityOpaque:
	default:
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "unknown capability tier", false)
	}
	switch c.PromptAccepted {
	case "", PromptAcceptedNone, PromptAcceptedNative, PromptAcceptedSynthesized:
	default:
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "unknown prompt accepted source", false)
	}
	if (strings.TrimSpace(c.Transport.Provider) == "") != (strings.TrimSpace(c.Transport.Protocol) == "") {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "transport provider and protocol must be supplied together", false)
	}
	if err := validateModelCatalog(c.AvailableModels); err != nil {
		return err
	}
	if err := validateModeCatalog(c.AvailableModes); err != nil {
		return err
	}
	if c.CurrentModel != "" && len(c.AvailableModels) != 0 && !containsModel(c.AvailableModels, c.CurrentModel) {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "current model is not advertised", false)
	}
	if c.CurrentMode != "" && len(c.AvailableModes) != 0 && !containsMode(c.AvailableModes, c.CurrentMode) {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "current mode is not advertised", false)
	}
	seenOptions := make(map[string]struct{}, len(c.ConfigOptions))
	for _, option := range c.ConfigOptions {
		id := strings.TrimSpace(option.ID)
		if id == "" || strings.TrimSpace(option.Name) == "" || len(option.Values) == 0 {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "config options require id, name, and values", false)
		}
		if _, exists := seenOptions[id]; exists {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "duplicate config option id", false)
		}
		seenOptions[id] = struct{}{}
		seenValues := make(map[string]struct{}, len(option.Values))
		for _, value := range option.Values {
			value = strings.TrimSpace(value)
			if value == "" {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "config option values cannot be empty", false)
			}
			if _, exists := seenValues[value]; exists {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "duplicate config option value", false)
			}
			seenValues[value] = struct{}{}
		}
		if option.Current != "" {
			if _, exists := seenValues[option.Current]; !exists {
				return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "current config option value is not advertised", false)
			}
		}
	}
	seenSlash := make(map[string]struct{}, len(c.SlashCommands))
	for _, command := range c.SlashCommands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "slash command name is required", false)
		}
		if _, exists := seenSlash[name]; exists {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "duplicate slash command", false)
		}
		seenSlash[name] = struct{}{}
	}
	seenAuth := make(map[string]struct{}, len(c.AuthMethods))
	for _, method := range c.AuthMethods {
		method = strings.TrimSpace(method)
		if method == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "auth method cannot be empty", false)
		}
		if _, exists := seenAuth[method]; exists {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "duplicate auth method", false)
		}
		seenAuth[method] = struct{}{}
	}
	return nil
}

func containsModel(models []ModelInfo, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func containsMode(modes []ModeInfo, id string) bool {
	for _, mode := range modes {
		if mode.ID == id {
			return true
		}
	}
	return false
}

func validateModelCatalog(models []ModelInfo) error {
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" || strings.TrimSpace(model.Name) == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "models require id and name", false)
		}
		if _, exists := seen[id]; exists {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "duplicate model id", false)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateModeCatalog(modes []ModeInfo) error {
	seen := make(map[string]struct{}, len(modes))
	for _, mode := range modes {
		id := strings.TrimSpace(mode.ID)
		if id == "" || strings.TrimSpace(mode.Name) == "" {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "modes require id and name", false)
		}
		if _, exists := seen[id]; exists {
			return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "duplicate mode id", false)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (c Capabilities) Clone() Capabilities {
	out := c
	out.AvailableModels = append([]ModelInfo(nil), c.AvailableModels...)
	for i := range out.AvailableModels {
		out.AvailableModels[i].ReasoningEfforts = append([]string(nil), c.AvailableModels[i].ReasoningEfforts...)
	}
	out.AvailableModes = append([]ModeInfo(nil), c.AvailableModes...)
	out.ConfigOptions = append([]ConfigOptionInfo(nil), c.ConfigOptions...)
	for i := range out.ConfigOptions {
		out.ConfigOptions[i].Values = append([]string(nil), c.ConfigOptions[i].Values...)
	}
	out.AuthMethods = append([]string(nil), c.AuthMethods...)
	out.SlashCommands = append([]SlashCommandInfo(nil), c.SlashCommands...)
	return out
}
