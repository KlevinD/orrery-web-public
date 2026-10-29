package codex

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

//go:embed codex_init_prompt.md
var nativeInitPrompt string

type nativeSlashRoute struct {
	method string
	params map[string]any
	logout bool
}

func (s *NativeSession) dispatchSend(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	s.mu.Lock()
	if s.turnInFlight {
		generation := s.turnGeneration
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, fmt.Sprintf("codex turn %d is already active", generation), false)
	}
	threadID := s.threadID
	s.mu.Unlock()
	if threadID == "" {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeHandshakeTimeout, "codex thread is not bound", true)
	}

	route := nativeRouteSlash(command.Send.Content, threadID)
	if route.logout {
		generation := s.currentGeneration()
		if _, err := s.callRPCWithPromptAcceptance(s.ctx, route.method, nil, command.Send.Metadata.ClientMsgID, generation); err != nil {
			return agentcore.CommandReceipt{}, err
		}
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeWarning, Message: "Logged out of Codex. New turns require authentication."}})
		return s.noTurnReceipt(), nil
	}

	s.mu.Lock()
	generation := s.turnGeneration + 1
	s.turnGeneration = generation
	s.fileChangePaths = nil
	s.turnInFlight = true
	s.activeTurnID = ""
	s.terminated[generation] = false
	delete(s.idlePending, generation)
	s.mu.Unlock()

	method := "turn/start"
	params := map[string]any{"threadId": threadID, "input": nativeInput(command.Send.Content)}
	if route.method != "" {
		method, params = route.method, route.params
	}
	result, err := s.callRPCWithPromptAcceptance(s.ctx, method, params, command.Send.Metadata.ClientMsgID, generation)
	if err != nil {
		s.mu.Lock()
		if s.turnGeneration == generation {
			s.turnInFlight = false
			s.activeTurnID = ""
			s.terminated[generation] = true
		}
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, err
	}
	if turnID := extractNativeTurnID(result); turnID != "" {
		s.bindTurn(turnID, generation)
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchCancel(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if command.Cancel.Target == agentcore.CancelTargetTool {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "cancel_tool", false)
	}
	threadID, turnID, generation, active := s.activeTurn()
	if !active {
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
	}
	if turnID == "" {
		deadline := time.NewTimer(time.Second)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()
		for turnID == "" && active {
			select {
			case <-deadline.C:
				return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "codex active turn id is not available", true)
			case <-ticker.C:
				threadID, turnID, generation, active = s.activeTurn()
			}
		}
		if !active {
			return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
		}
	}
	if _, err := s.callRPC(s.ctx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID}); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchSteer(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	threadID, turnID, generation, active := s.activeTurn()
	if !active || turnID == "" {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "codex has no steerable active turn", false)
	}
	_, err := s.callRPC(s.ctx, "turn/steer", map[string]any{
		"threadId": threadID, "expectedTurnId": turnID, "input": nativeInput(command.Steer.Content),
	})
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchAnswerPermission(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	requestID := command.AnswerPermission.RequestID
	s.mu.Lock()
	pending, ok := s.pendingPermissions[requestID]
	s.mu.Unlock()
	if !ok {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnknownPermissionRequest, "codex permission request is not pending", false)
	}
	approved := command.AnswerPermission.Decision != agentcore.PermissionDenied
	var result any
	if pending.questions {
		answers := make(map[string]any, len(command.AnswerPermission.Answers))
		provided := command.AnswerPermission.Answers
		if !approved {
			provided = nil
		}
		for _, answer := range provided {
			id := pending.answerIDs[answer.Question]
			if id == "" {
				return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "codex answer references an unknown question", false)
			}
			answers[id] = map[string]any{"answers": append([]string(nil), answer.Labels...)}
		}
		result = map[string]any{"answers": answers}
	} else if pending.eliciting {
		result = map[string]any{"action": nativeConditional(approved, "accept", "decline"), "content": nativeElicitationContent(command.AnswerPermission)}
	} else {
		decision := "decline"
		switch command.AnswerPermission.Decision {
		case agentcore.PermissionApproved:
			decision = "accept"
		case agentcore.PermissionAllowAlways:
			decision = "acceptForSession"
		}
		result = map[string]any{"decision": decision}
	}
	if err := s.writeReverseResult(pending.wireID, result); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	_, stillPending := s.pendingPermissions[requestID]
	if stillPending {
		delete(s.pendingPermissions, requestID)
		s.resolvedRequests[string(pending.wireID)] = agentcore.PermissionKindTool
	}
	s.mu.Unlock()
	if stillPending {
		s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: requestID, Kind: agentcore.PermissionKindTool}})
	}
	return s.noTurnReceipt(), nil
}

func (s *NativeSession) dispatchAnswerAuth(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	requestID := command.AnswerAuth.RequestID
	s.mu.Lock()
	pending, ok := s.pendingAuth[requestID]
	s.mu.Unlock()
	if !ok {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnknownAuthRequest, "codex auth request is not pending", false)
	}
	if command.AnswerAuth.MethodID != "refresh" && command.AnswerAuth.MethodID != "chatgptAuthTokens" {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "codex auth method is not supported", false)
	}
	var credentials map[string]any
	if err := json.Unmarshal(command.AnswerAuth.Credentials, &credentials); err != nil {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "codex auth credentials are invalid", false)
	}
	access := firstNativeCredential(credentials, "accessToken", "access_token")
	account := firstNativeCredential(credentials, "chatgptAccountId", "chatgpt_account_id")
	if access == nil || account == nil {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "codex auth requires access token and account id", false)
	}
	result := map[string]any{"accessToken": access, "chatgptAccountId": account}
	if plan := firstNativeCredential(credentials, "chatgptPlanType", "chatgpt_plan_type"); plan != nil {
		result["chatgptPlanType"] = plan
	}
	if err := s.writeReverseResult(pending.wireID, result); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	_, stillPending := s.pendingAuth[requestID]
	if stillPending {
		delete(s.pendingAuth, requestID)
		s.resolvedRequests[string(pending.wireID)] = agentcore.PermissionKindAuth
	}
	s.mu.Unlock()
	if stillPending {
		s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: requestID, Kind: agentcore.PermissionKindAuth}})
	}
	return s.noTurnReceipt(), nil
}

func (s *NativeSession) dispatchSetting(field, value string) (agentcore.CommandReceipt, error) {
	return s.dispatchSettingContext(s.ctx, field, value)
}

func (s *NativeSession) dispatchSettingContext(ctx context.Context, field, value string) (agentcore.CommandReceipt, error) {
	s.mu.Lock()
	if s.turnInFlight {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "codex settings cannot change during a turn", false)
	}
	threadID := s.threadID
	generation := s.turnGeneration
	caps := s.capabilities.Clone()
	s.mu.Unlock()
	if field == "model" && len(caps.AvailableModels) != 0 && !nativeHasModel(caps.AvailableModels, value) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "codex model is not advertised", false)
	}
	if field == "mode" && len(caps.AvailableModes) != 0 && !nativeHasMode(caps.AvailableModes, nativeModeCatalogValue(value)) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "codex permission profile is not advertised", false)
	}
	wireField := field
	if field == "mode" {
		wireField = "permissions"
	}
	if _, err := s.callRPC(ctx, "thread/settings/update", map[string]any{"threadId": threadID, wireField: value}); err != nil {
		s.emitNotice("codex rejected a settings update")
		return agentcore.CommandReceipt{}, err
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchRewind(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	_, _, generation, active := s.activeTurn()
	if active {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "codex cannot rewind during a turn", false)
	}
	threadID := s.boundThread()
	if _, err := s.callRPC(s.ctx, "thread/rollback", map[string]any{"threadId": threadID, "numTurns": command.Rewind.NumTurns}); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	toTurn := uint64(0)
	if generation > uint64(command.Rewind.NumTurns) {
		toTurn = generation - uint64(command.Rewind.NumTurns)
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventRewound, Rewound: &agentcore.RewoundEvent{ToTurn: toTurn}})
	return s.noTurnReceipt(), nil
}

func (s *NativeSession) dispatchCheckpoints() (agentcore.CommandReceipt, error) {
	_, _, generation, active := s.activeTurn()
	if active {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "codex cannot list checkpoints during a turn", false)
	}
	result, err := s.callRPC(s.ctx, "thread/turns/list", map[string]any{"threadId": s.boundThread()})
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.emit(generation, nativeCheckpointEvent(result))
	return s.noTurnReceipt(), nil
}

func (s *NativeSession) activeTurn() (threadID, turnID string, generation uint64, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID, s.activeTurnID, s.turnGeneration, s.turnInFlight
}

func nativeInput(content []agentcore.ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(content))
	for _, block := range content {
		switch block.Kind {
		case agentcore.ContentBlockText:
			out = append(out, map[string]any{"type": "text", "text": block.Text})
		case agentcore.ContentBlockImage:
			out = append(out, map[string]any{"type": "image", "url": "data:" + block.MediaType + ";base64," + base64.StdEncoding.EncodeToString(block.Data)})
		case agentcore.ContentBlockResourceLink:
			path := strings.TrimPrefix(block.URI, "file://")
			out = append(out, map[string]any{"type": "text", "text": "[Attached file: " + path + "]"})
		}
	}
	return out
}

func nativeRouteSlash(content []agentcore.ContentBlock, threadID string) nativeSlashRoute {
	if len(content) == 0 || content[0].Kind != agentcore.ContentBlockText || !strings.HasPrefix(content[0].Text, "/") {
		return nativeSlashRoute{}
	}
	command, rest, _ := strings.Cut(strings.TrimPrefix(content[0].Text, "/"), " ")
	rest = strings.TrimSpace(rest)
	switch command {
	case "init":
		return nativeSlashRoute{method: "turn/start", params: map[string]any{"threadId": threadID, "input": []map[string]any{{"type": "text", "text": nativeInitPrompt}}}}
	case "compact":
		return nativeSlashRoute{method: "thread/compact/start", params: map[string]any{"threadId": threadID}}
	case "logout":
		return nativeSlashRoute{method: "account/logout", logout: true}
	case "review":
		target := map[string]any{"type": "uncommittedChanges"}
		if rest != "" {
			target = map[string]any{"type": "custom", "instructions": rest}
		}
		return nativeSlashRoute{method: "review/start", params: map[string]any{"threadId": threadID, "target": target}}
	case "review-branch":
		if rest != "" {
			return nativeSlashRoute{method: "review/start", params: map[string]any{"threadId": threadID, "target": map[string]any{"type": "baseBranch", "branch": rest}}}
		}
	case "review-commit":
		if rest != "" {
			return nativeSlashRoute{method: "review/start", params: map[string]any{"threadId": threadID, "target": map[string]any{"type": "commit", "sha": rest}}}
		}
	}
	return nativeSlashRoute{}
}

func extractNativeTurnID(raw json.RawMessage) string {
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	if id, _ := result["turnId"].(string); strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	if turn, _ := result["turn"].(map[string]any); turn != nil {
		if id, _ := turn["id"].(string); strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func nativeElicitationContent(command *agentcore.AnswerPermissionCommand) map[string]any {
	content := make(map[string]any)
	if command.Selected != "" {
		content["selected"] = command.Selected
	}
	for _, answer := range command.Answers {
		content[answer.Question] = append([]string(nil), answer.Labels...)
	}
	return content
}

func firstNativeCredential(credentials map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := credentials[key]; ok && value != nil {
			if text, stringValue := value.(string); !stringValue || strings.TrimSpace(text) != "" {
				return value
			}
		}
	}
	return nil
}

func nativeConditional[T any](condition bool, yes, no T) T {
	if condition {
		return yes
	}
	return no
}
