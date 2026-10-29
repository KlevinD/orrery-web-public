package codex

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *NativeSession) applyModelDiscovery(raw json.RawMessage) {
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	items := nativeObjectList(result, "data", "models")
	models := make([]agentcore.ModelInfo, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		id := nativeString(item, "id")
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		name := nativeString(item, "displayName", "name")
		if name == "" {
			name = id
		}
		efforts := make([]string, 0)
		if values, ok := item["supportedReasoningEfforts"].([]any); ok {
			for _, value := range values {
				switch typed := value.(type) {
				case string:
					if strings.TrimSpace(typed) != "" {
						efforts = append(efforts, strings.TrimSpace(typed))
					}
				case map[string]any:
					if effort := nativeString(typed, "reasoningEffort"); effort != "" {
						efforts = append(efforts, effort)
					}
				}
			}
		}
		models = append(models, agentcore.ModelInfo{ID: id, Name: name, Description: nativeString(item, "description"), ReasoningEfforts: nativeUniqueStrings(efforts)})
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	efforts := make([]string, 0)
	for _, model := range models {
		efforts = append(efforts, model.ReasoningEfforts...)
	}
	efforts = nativeUniqueStrings(efforts)
	s.mu.Lock()
	s.capabilities.AvailableModels = models
	s.capabilities.ConfigOptions = nil
	if len(efforts) != 0 {
		option := agentcore.ConfigOptionInfo{ID: "effort", Name: "Reasoning effort", Values: efforts}
		requested := strings.TrimSpace(s.config.ConfigOptions["effort"])
		if requested == "" {
			requested = strings.TrimSpace(s.config.ConfigOptions["reasoning_effort"])
		}
		if nativeContains(efforts, requested) {
			option.Current = requested
		}
		s.capabilities.ConfigOptions = []agentcore.ConfigOptionInfo{option}
	}
	s.mu.Unlock()
}

func (s *NativeSession) applyModeDiscovery(raw json.RawMessage) {
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	items := nativeObjectList(result, "data", "permissions")
	modes := make([]agentcore.ModeInfo, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		wireID := nativeString(item, "id")
		if wireID == "" {
			continue
		}
		id := nativeModeCatalogValue(wireID)
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		name, description := nativeModeDisplay(wireID)
		if value := nativeString(item, "name", "displayName"); value != "" {
			name = value
		}
		if value := nativeString(item, "description"); value != "" {
			description = value
		}
		if name == "" {
			name = id
		}
		modes = append(modes, agentcore.ModeInfo{ID: id, Name: name, Description: description})
	}
	sort.SliceStable(modes, func(i, j int) bool { return modes[i].ID < modes[j].ID })
	s.mu.Lock()
	s.capabilities.AvailableModes = modes
	s.mu.Unlock()
}

func (s *NativeSession) emitCatalog() {
	caps := s.Capabilities()
	event := agentcore.SessionEvent{Kind: agentcore.EventCatalogUpdated, CatalogUpdated: &agentcore.CatalogUpdatedEvent{
		Models: caps.AvailableModels, Modes: caps.AvailableModes, ConfigOptions: caps.ConfigOptions, SlashCommands: caps.SlashCommands,
	}}
	s.emit(s.currentGeneration(), event)
}

func (s *NativeSession) reconcileRequestedSettings(ctx context.Context) {
	caps := s.Capabilities()
	if model := strings.TrimSpace(s.requestedModel); model != "" {
		if nativeHasModel(caps.AvailableModels, model) {
			_, _ = s.dispatchSettingContext(ctx, "model", model)
		} else {
			s.mu.Lock()
			s.capabilities.CurrentModel = ""
			s.mu.Unlock()
			s.emitNotice("stored Codex model is not available and was not applied")
		}
	}
	if mode := strings.TrimSpace(s.requestedMode); mode != "" {
		catalogValue := nativeModeCatalogValue(mode)
		if nativeHasMode(caps.AvailableModes, catalogValue) {
			_, _ = s.dispatchSettingContext(ctx, "mode", nativeModeProfileID(mode))
		} else {
			s.mu.Lock()
			s.capabilities.CurrentMode = ""
			s.mu.Unlock()
			s.emitNotice("stored Codex permission profile is not available and was not applied")
		}
	}
}

func (s *NativeSession) applyConfigChanged(params map[string]any) {
	settings := nativeMap(params, "threadSettings", "thread_settings")
	model := nativeString(settings, "model")
	mode := ""
	if profile := nativeMap(settings, "activePermissionProfile"); profile != nil {
		mode = nativeModeCatalogValue(nativeString(profile, "id"))
	}
	options := make(map[string]string)
	if effort := nativeString(settings, "effort", "reasoningEffort"); effort != "" {
		options["effort"] = effort
	}
	s.mu.Lock()
	if model != "" {
		s.capabilities.CurrentModel = model
	}
	if mode != "" {
		s.capabilities.CurrentMode = mode
	}
	if effort := options["effort"]; effort != "" {
		for i := range s.capabilities.ConfigOptions {
			if s.capabilities.ConfigOptions[i].ID == "effort" && nativeContains(s.capabilities.ConfigOptions[i].Values, effort) {
				s.capabilities.ConfigOptions[i].Current = effort
			}
		}
	}
	s.mu.Unlock()
	if model == "" && mode == "" && len(options) == 0 {
		return
	}
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: &agentcore.ConfigChangedEvent{Model: model, Mode: mode, Options: options}})
}

func nativeCheckpointEvent(raw json.RawMessage) agentcore.SessionEvent {
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	items := nativeObjectList(result, "data", "turns")
	entries := make([]agentcore.CheckpointEntry, 0, len(items))
	for _, item := range items {
		if id := nativeString(item, "id"); id != "" {
			entries = append(entries, agentcore.CheckpointEntry{ID: id, Label: nativeString(item, "status")})
		}
	}
	return agentcore.SessionEvent{Kind: agentcore.EventCheckpointList, CheckpointList: &agentcore.CheckpointListEvent{Entries: entries}}
}

func nativeModeProfileID(mode string) string {
	mode = strings.TrimSpace(mode)
	if strings.HasPrefix(mode, ":") && len(mode) > 1 {
		return mode
	}
	switch mode {
	case "read-only":
		return ":read-only"
	case "agent-full-access", "full-access", "yolo", "yoloNoSandbox":
		return ":danger-full-access"
	default:
		return ":workspace"
	}
}

func nativeModeCatalogValue(mode string) string {
	switch nativeModeProfileID(mode) {
	case ":read-only":
		return "read-only"
	case ":workspace":
		return "auto"
	case ":danger-full-access":
		return "full-access"
	default:
		return strings.TrimSpace(mode)
	}
}

func nativeModeDisplay(wireID string) (string, string) {
	switch wireID {
	case ":read-only":
		return "Read Only", "Read workspace files; approval is required for edits and network access."
	case ":workspace":
		return "Default", "Read and edit workspace files; approval is required outside the workspace and for network access."
	case ":danger-full-access":
		return "Full Access", "Edit outside the workspace and access the network without approval."
	default:
		return wireID, ""
	}
}

func nativeObjectList(result map[string]any, keys ...string) []map[string]any {
	for _, key := range keys {
		values, ok := result[key].([]any)
		if !ok {
			continue
		}
		out := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if object, ok := value.(map[string]any); ok {
				out = append(out, object)
			}
		}
		return out
	}
	return nil
}

func nativeMap(object map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := object[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func nativeString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nativeUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func nativeContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func nativeHasModel(models []agentcore.ModelInfo, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func nativeHasMode(modes []agentcore.ModeInfo, id string) bool {
	for _, mode := range modes {
		if mode.ID == id {
			return true
		}
	}
	return false
}
