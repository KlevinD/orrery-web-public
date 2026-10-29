package claude

import (
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *NativeSession) dispatchSend(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if err := s.drainPendingControls(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	if uint32(len(s.active)) >= s.capabilities.PendingLimit {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeQueueFull, "claude proactive turn queue is full", true)
	}
	generation := s.turnGeneration + 1
	s.turnGeneration = generation
	s.active[generation] = true
	s.terminated[generation] = false
	s.generationQueue = append(s.generationQueue, generation)
	s.resultQueue = append(s.resultQueue, generation)
	if s.turnOpen == 0 {
		s.turnOpen = generation
	}
	clientID := strings.TrimSpace(command.Send.Metadata.ClientMsgID)
	if clientID != "" {
		s.clientGeneration[clientID] = generation
	}
	s.mu.Unlock()

	if err := s.writeFrame(claudePromptFrame(command.Send.Content, clientID)); err != nil {
		s.mu.Lock()
		delete(s.active, generation)
		s.terminated[generation] = true
		delete(s.clientGeneration, clientID)
		for index, candidate := range s.generationQueue {
			if candidate == generation {
				s.generationQueue = append(s.generationQueue[:index], s.generationQueue[index+1:]...)
				break
			}
		}
		for index, candidate := range s.resultQueue {
			if candidate == generation {
				s.resultQueue = append(s.resultQueue[:index], s.resultQueue[index+1:]...)
				break
			}
		}
		if s.turnOpen == generation {
			s.turnOpen = 0
		}
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, err
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchCancel(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if command.Cancel.Target == agentcore.CancelTargetTool {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "cancel_tool", false)
	}
	s.mu.Lock()
	active := len(s.active) != 0
	generation := s.turnGeneration
	s.mu.Unlock()
	if !active {
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
	}
	id := s.nextControlID("ctl")
	if err := s.writeFrame(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "interrupt"}}); err != nil {
		if s.dead.Load() {
			return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
		}
		return agentcore.CommandReceipt{}, err
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchAnswerPermission(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	requestID := command.AnswerPermission.RequestID
	s.mu.Lock()
	pending, ok := s.pendingPerms[requestID]
	if !ok {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnknownPermissionRequest, "claude permission request is not pending", false)
	}
	response, err := claudePermissionResponse(pending, command.AnswerPermission)
	if err != nil {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, err
	}
	delete(s.pendingPerms, requestID)
	if err := s.writeFrame(response); err != nil {
		s.pendingPerms[requestID] = pending
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, err
	}
	generation := s.turnGeneration
	s.mu.Unlock()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool}})
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func claudePermissionResponse(pending claudePendingPermission, command *agentcore.AnswerPermissionCommand) (map[string]any, error) {
	allow := command.Decision == agentcore.PermissionApproved || command.Decision == agentcore.PermissionAllowAlways
	result := map[string]any{"behavior": "deny", "message": "User rejected the request.", "toolUseID": pending.toolUseID}
	if allow {
		updated := cloneClaudeObject(pending.input)
		if updated == nil {
			updated = make(map[string]any)
		}
		if pending.toolName == "AskUserQuestion" {
			answers, err := claudeQuestionAnswers(pending, command)
			if err != nil {
				return nil, err
			}
			updated["answers"] = answers
		}
		result = map[string]any{"behavior": "allow", "updatedInput": updated, "toolUseID": pending.toolUseID}
	}
	return map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": pending.requestID, "response": result}}, nil
}

func claudeQuestionAnswers(pending claudePendingPermission, command *agentcore.AnswerPermissionCommand) (map[string]any, error) {
	known := make(map[string]agentcore.PermissionQuestion, len(pending.questions))
	for _, question := range pending.questions {
		known[question.Question] = question
	}
	answers := make(map[string]any)
	for _, answer := range command.Answers {
		question, ok := known[answer.Question]
		if !ok {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude answer references an unknown question", false)
		}
		if _, duplicate := answers[answer.Question]; duplicate {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude question was answered more than once", false)
		}
		allowed := make(map[string]struct{}, len(question.Options))
		for _, option := range question.Options {
			allowed[option.Label] = struct{}{}
		}
		for _, label := range answer.Labels {
			if _, ok := allowed[label]; !ok {
				return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude answer references an unknown option", false)
			}
		}
		if len(answer.Labels) == 0 {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude question requires at least one answer", false)
		}
		if !question.MultiSelect && len(answer.Labels) != 1 {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude single-select question requires one answer", false)
		}
		if len(answer.Labels) == 1 {
			answers[answer.Question] = answer.Labels[0]
		} else {
			answers[answer.Question] = append([]string(nil), answer.Labels...)
		}
	}
	if len(command.Answers) != 0 && len(answers) != len(pending.questions) {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude answer must cover every question", false)
	}
	if len(answers) == 0 && len(pending.questions) != 0 {
		if len(pending.questions) != 1 {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude multi-question request requires an answer for every question", false)
		}
		question := pending.questions[0]
		label := strings.TrimSpace(command.Selected)
		if label == "" && len(question.Options) != 0 {
			label = question.Options[0].Label
		}
		valid := false
		for _, option := range question.Options {
			if option.Label == label {
				valid = true
				break
			}
		}
		if !valid {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude selected answer is not an advertised option", false)
		}
		answers[question.Question] = label
	}
	return answers, nil
}

func (s *NativeSession) dispatchSetting(kind, value string) (agentcore.CommandReceipt, error) {
	kind = strings.TrimSpace(kind)
	value = strings.TrimSpace(value)
	s.mu.Lock()
	caps := s.capabilities.Clone()
	s.mu.Unlock()

	subtype := ""
	request := map[string]any{}
	switch kind {
	case "mode":
		value = claudeNormalizedMode(value)
		if !validClaudeMode(value) || !claudeHasMode(caps.AvailableModes, value) {
			s.emitNotice("Claude permission mode is not advertised")
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "claude permission mode is not advertised", false)
		}
		subtype = "set_permission_mode"
		request = map[string]any{"subtype": subtype, "mode": value}
	case "model":
		if !claudeHasModel(caps.AvailableModels, value) {
			s.emitNotice("Claude model is not advertised")
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "claude model is not advertised", false)
		}
		subtype = "set_model"
		request = map[string]any{"subtype": subtype, "model": value}
	case "effort", "reasoning_effort", "thought_level":
		if !claudeHasEffort(caps, value) {
			s.emitNotice("Claude effort level is not advertised for the current model")
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "claude effort level is not advertised", false)
		}
		kind = "effort"
		subtype = "apply_flag_settings"
		settings := map[string]any{"effortLevel": value}
		if value == "ultracode" {
			settings = map[string]any{"ultracode": true}
		}
		request = map[string]any{"subtype": subtype, "settings": settings}
	default:
		s.emitNotice("Claude configuration option is unsupported")
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "set_config_option:"+kind, false)
	}

	id := s.nextControlID("ctl")
	frame := map[string]any{"type": "control_request", "request_id": id, "request": request}
	s.mu.Lock()
	s.pendingSettings[id] = claudePendingSetting{kind: kind, value: value}
	if len(s.active) != 0 {
		for index := range s.pendingControls {
			if s.pendingControls[index].subtype == subtype {
				delete(s.pendingSettings, s.pendingControls[index].id)
				s.pendingControls[index] = claudeQueuedControl{id: id, subtype: subtype, frame: frame}
				generation := s.turnGeneration
				s.mu.Unlock()
				return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
			}
		}
		s.pendingControls = append(s.pendingControls, claudeQueuedControl{id: id, subtype: subtype, frame: frame})
		generation := s.turnGeneration
		s.mu.Unlock()
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
	}
	generation := s.turnGeneration
	s.mu.Unlock()
	if err := s.writeFrame(frame); err != nil {
		s.mu.Lock()
		delete(s.pendingSettings, id)
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, err
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *NativeSession) drainPendingControls() error {
	s.mu.Lock()
	controls := append([]claudeQueuedControl(nil), s.pendingControls...)
	s.pendingControls = nil
	s.mu.Unlock()
	for index, control := range controls {
		if err := s.writeFrame(control.frame); err != nil {
			s.mu.Lock()
			s.pendingControls = append(append([]claudeQueuedControl(nil), controls[index:]...), s.pendingControls...)
			s.mu.Unlock()
			return err
		}
	}
	return nil
}

func (s *NativeSession) dispatchSessionInfo(kind agentcore.SessionInfoKind) (agentcore.CommandReceipt, error) {
	subtype := ""
	prefix := ""
	switch kind {
	case agentcore.SessionInfoContextUsage:
		subtype, prefix = "get_context_usage", "ctl-qsi-usage"
	case agentcore.SessionInfoSessionCost:
		subtype, prefix = "get_session_cost", "ctl-qsi-cost"
	default:
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "claude session info kind is unsupported", false)
	}
	id := s.nextControlID(prefix)
	if err := s.writeFrame(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": subtype}}); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	return s.noTurnReceipt(), nil
}

func claudeHasMode(modes []agentcore.ModeInfo, value string) bool {
	for _, mode := range modes {
		if mode.ID == value {
			return true
		}
	}
	return false
}

func claudeHasModel(models []agentcore.ModelInfo, value string) bool {
	if len(models) == 0 {
		return false
	}
	for _, model := range models {
		if model.ID == value {
			return true
		}
	}
	return false
}

func claudeHasEffort(caps agentcore.Capabilities, value string) bool {
	if caps.CurrentModel != "" {
		for _, model := range caps.AvailableModels {
			if model.ID == caps.CurrentModel && len(model.ReasoningEfforts) != 0 {
				return claudeContains(model.ReasoningEfforts, value)
			}
		}
	}
	for _, option := range caps.ConfigOptions {
		if option.ID != "effort" {
			continue
		}
		for _, candidate := range option.Values {
			if candidate == value {
				return true
			}
		}
	}
	return false
}

func cloneClaudeObject(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(encoded, &out) != nil {
		return nil
	}
	return out
}

func cloneClaudeQuestions(source []agentcore.PermissionQuestion) []agentcore.PermissionQuestion {
	out := make([]agentcore.PermissionQuestion, len(source))
	for index := range source {
		out[index] = source[index]
		out[index].Options = append([]agentcore.PermissionQuestionOption(nil), source[index].Options...)
	}
	return out
}
