package claude

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *NativeSession) applyClaudeDiscovery(response map[string]any) {
	modelsRaw, hasModels := response["models"].([]any)
	commandsRaw, hasCommands := response["commands"].([]any)
	if !hasModels && !hasCommands {
		return
	}

	models := make([]agentcore.ModelInfo, 0, len(modelsRaw))
	seenModels := make(map[string]struct{}, len(modelsRaw))
	for _, raw := range modelsRaw {
		model, _ := raw.(map[string]any)
		id := claudeString(model, "value", "id")
		if id == "" {
			continue
		}
		if _, exists := seenModels[id]; exists {
			continue
		}
		seenModels[id] = struct{}{}
		efforts := claudeStringArray(model["supportedEffortLevels"])
		if claudeContains(efforts, "xhigh") && !claudeContains(efforts, "ultracode") {
			efforts = append(efforts, "ultracode")
		}
		models = append(models, agentcore.ModelInfo{
			ID: id, Name: firstClaudeString(claudeString(model, "displayName", "name"), id),
			Description: claudeStringRaw(model, "description"), ReasoningEfforts: sortedClaudeStrings(efforts),
		})
	}

	commands := make([]agentcore.SlashCommandInfo, 0, len(commandsRaw))
	seenCommands := make(map[string]struct{}, len(commandsRaw))
	for _, raw := range commandsRaw {
		command, _ := raw.(map[string]any)
		name := claudeString(command, "name")
		if name == "" {
			continue
		}
		if _, exists := seenCommands[name]; exists {
			continue
		}
		seenCommands[name] = struct{}{}
		commands = append(commands, agentcore.SlashCommandInfo{Name: name, Description: claudeStringRaw(command, "description")})
	}

	s.mu.Lock()
	if hasModels {
		s.capabilities.AvailableModels = models
		efforts := make([]string, 0)
		for _, model := range models {
			efforts = append(efforts, model.ReasoningEfforts...)
		}
		efforts = sortedClaudeStrings(efforts)
		s.capabilities.ConfigOptions = nil
		if len(efforts) != 0 {
			option := agentcore.ConfigOptionInfo{ID: "effort", Name: "Reasoning effort", Values: efforts}
			if claudeContains(efforts, s.currentEffort) {
				option.Current = s.currentEffort
			}
			s.capabilities.ConfigOptions = []agentcore.ConfigOptionInfo{option}
		}
		if s.capabilities.CurrentModel != "" && !claudeHasModel(models, s.capabilities.CurrentModel) {
			s.capabilities.CurrentModel = ""
		}
	}
	if hasCommands {
		s.capabilities.SlashCommands = commands
	}
	caps := s.capabilities.Clone()
	generation := s.claudeEventGenerationLocked()
	s.mu.Unlock()

	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventCatalogUpdated, CatalogUpdated: &agentcore.CatalogUpdatedEvent{
		Models: caps.AvailableModels, Modes: caps.AvailableModes, ConfigOptions: caps.ConfigOptions, SlashCommands: caps.SlashCommands,
	}})
}

func (s *NativeSession) reconcileClaudeRequestedSettings() {
	caps := s.Capabilities()
	if model := strings.TrimSpace(s.config.Model); model != "" && len(caps.AvailableModels) != 0 && !claudeHasModel(caps.AvailableModels, model) {
		s.emitNotice("Stored Claude model is not available and was not applied")
	}
	if mode := claudeNormalizedMode(s.config.Mode); mode != "" && !claudeHasMode(caps.AvailableModes, mode) {
		s.mu.Lock()
		s.capabilities.CurrentMode = ""
		s.mu.Unlock()
		s.emitNotice("Stored Claude permission mode is not available and was not applied")
	}
	effort := strings.TrimSpace(s.config.ConfigOptions["effort"])
	if effort == "" {
		effort = strings.TrimSpace(s.config.ConfigOptions["reasoning_effort"])
	}
	if effort == "" {
		return
	}
	if !claudeHasEffort(caps, effort) {
		s.emitNotice("Stored Claude effort level is not available and was not applied")
		return
	}
	_, _ = s.dispatchSetting("effort", effort)
}

func (s *NativeSession) applyClaudeObservedConfig(generation uint64, model, mode string) {
	mode = claudeNormalizedMode(mode)
	model = strings.TrimSpace(model)
	s.mu.Lock()
	changedModel := model != "" && model != s.capabilities.CurrentModel
	changedMode := mode != "" && mode != s.capabilities.CurrentMode && validClaudeMode(mode) && claudeHasMode(s.capabilities.AvailableModes, mode)
	if changedModel {
		if len(s.capabilities.AvailableModels) == 0 || claudeHasModel(s.capabilities.AvailableModels, model) {
			s.capabilities.CurrentModel = model
		} else {
			changedModel = false
		}
	}
	if changedMode {
		s.capabilities.CurrentMode = mode
	}
	s.mu.Unlock()
	if !changedModel && !changedMode {
		return
	}
	event := &agentcore.ConfigChangedEvent{}
	if changedModel {
		event.Model = model
	}
	if changedMode {
		event.Mode = mode
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: event})
}

func (s *NativeSession) applyClaudeSettingAck(setting claudePendingSetting) {
	generation := s.currentGeneration()
	event := &agentcore.ConfigChangedEvent{}
	s.mu.Lock()
	switch setting.kind {
	case "mode":
		// system/status is the authoritative permission-mode signal.
		s.mu.Unlock()
		return
	case "model":
		s.capabilities.CurrentModel = setting.value
		event.Model = setting.value
	case "effort":
		s.currentEffort = setting.value
		for index := range s.capabilities.ConfigOptions {
			if s.capabilities.ConfigOptions[index].ID == "effort" {
				s.capabilities.ConfigOptions[index].Current = setting.value
			}
		}
		event.Options = map[string]string{"effort": setting.value}
	default:
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: event})
}

func claudeStringArray(value any) []string {
	values, _ := value.([]any)
	out := make([]string, 0, len(values))
	for _, raw := range values {
		if value, ok := raw.(string); ok {
			out = append(out, value)
		}
	}
	return out
}

func claudeContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func firstClaudeString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
