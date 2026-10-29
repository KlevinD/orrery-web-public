package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	agentcore "orrery_v2/internal/agent/core"
)

type permissionSelection struct {
	Decision agentcore.PermissionDecision `json:"decision"`
	Selected string                       `json:"selected,omitempty"`
	Answers  []agentcore.QuestionAnswer   `json:"answers,omitempty"`
}

// permissionAnswer only accepts an explicit, advertised decision. Missing data
// must never reach the provider's legacy default-selection behavior.
func permissionAnswer(permission agentcore.PermissionEvent, data json.RawMessage, alwaysAllow bool) (*agentcore.AnswerPermissionCommand, error) {
	if permission.Kind != agentcore.PermissionKindTool {
		return nil, errors.New("authentication requires a separate credential flow")
	}
	answer := &agentcore.AnswerPermissionCommand{RequestID: permission.RequestID}
	var optionID string
	if err := json.Unmarshal(data, &optionID); err == nil {
		if permission.Context != nil && len(permission.Context.Options) > 0 {
			for _, option := range permission.Context.Options {
				if option.OptionID == optionID {
					answer.Decision = optionDecision(option.Kind)
					answer.Selected = option.OptionID
					break
				}
			}
		} else if len(permission.Questions) == 0 {
			answer.Decision = agentcore.PermissionDecision(optionID)
		}
	} else {
		var selection permissionSelection
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&selection); err != nil {
			return nil, errors.New("permission selection is invalid")
		}
		answer.Decision, answer.Selected, answer.Answers = selection.Decision, selection.Selected, selection.Answers
		if permission.Context != nil && len(permission.Context.Options) > 0 && answer.Decision != agentcore.PermissionDenied {
			return nil, errors.New("an advertised provider option is required")
		}
	}
	switch answer.Decision {
	case agentcore.PermissionDenied:
		// Denial takes precedence over a stale always_allow checkbox.
		answer.Selected, answer.Answers = "", nil
		return answer, nil
	case agentcore.PermissionApproved, agentcore.PermissionAllowAlways:
	default:
		return nil, errors.New("an explicit permission decision is required")
	}
	if len(permission.Questions) > 0 {
		if alwaysAllow || answer.Decision == agentcore.PermissionAllowAlways {
			return nil, errors.New("question answers cannot grant persistent permission")
		}
		if err := validateQuestionAnswers(permission.Questions, answer); err != nil {
			return nil, err
		}
	} else if len(answer.Answers) > 0 || (answer.Selected != "" && (permission.Context == nil || len(permission.Context.Options) == 0)) {
		return nil, errors.New("unexpected question answer")
	}
	if alwaysAllow && answer.Decision == agentcore.PermissionApproved {
		if permission.Context != nil && len(permission.Context.Options) > 0 {
			return nil, errors.New("persistent permission requires the provider's allow_always option")
		}
		answer.Decision = agentcore.PermissionAllowAlways
	}
	return answer, nil
}

func optionDecision(kind string) agentcore.PermissionDecision {
	switch kind {
	case "allow_once":
		return agentcore.PermissionApproved
	case "allow_always":
		return agentcore.PermissionAllowAlways
	case "reject_once", "reject_always":
		return agentcore.PermissionDenied
	default:
		return ""
	}
}

func validateQuestionAnswers(questions []agentcore.PermissionQuestion, answer *agentcore.AnswerPermissionCommand) error {
	if answer.Selected != "" {
		if len(questions) != 1 || len(answer.Answers) != 0 {
			return errors.New("each question requires its own explicit answer")
		}
		answer.Answers = []agentcore.QuestionAnswer{{Question: questions[0].Question, Labels: []string{answer.Selected}}}
		answer.Selected = ""
	}
	if len(answer.Answers) != len(questions) {
		return errors.New("all questions require explicit answers")
	}
	seen := make(map[string]bool, len(questions))
	for _, response := range answer.Answers {
		var question *agentcore.PermissionQuestion
		for i := range questions {
			if questions[i].Question == response.Question {
				question = &questions[i]
				break
			}
		}
		if question == nil || seen[response.Question] || len(response.Labels) == 0 || (!question.MultiSelect && len(response.Labels) != 1) {
			return errors.New("question answer is incomplete or ambiguous")
		}
		seen[response.Question] = true
		labels := make(map[string]bool, len(response.Labels))
		for _, label := range response.Labels {
			known := false
			for _, option := range question.Options {
				if label == option.Label {
					known = true
					break
				}
			}
			if !known || labels[label] {
				return errors.New("question answer must use distinct advertised labels")
			}
			labels[label] = true
		}
	}
	return nil
}

func permissionWire(conversationID, id string, permission agentcore.PermissionEvent) map[string]any {
	options := make([]map[string]any, 0)
	if permission.Kind == agentcore.PermissionKindTool {
		if len(permission.Questions) == 1 && !permission.Questions[0].MultiSelect {
			for _, option := range permission.Questions[0].Options {
				options = append(options, map[string]any{"label": option.Label, "value": permissionSelection{
					Decision: agentcore.PermissionApproved, Selected: option.Label,
				}})
			}
		} else if len(permission.Questions) == 0 {
			if permission.Context != nil && len(permission.Context.Options) > 0 {
				for _, option := range permission.Context.Options {
					if optionDecision(option.Kind) != "" {
						options = append(options, map[string]any{"label": option.Kind, "value": option.OptionID})
					}
				}
			} else {
				options = append(options, map[string]any{"label": "Allow", "value": "approved"})
			}
		}
		options = append(options, map[string]any{"label": "Deny", "value": permissionSelection{Decision: agentcore.PermissionDenied}})
	}
	return map[string]any{
		"conversation_id": conversationID, "id": id, "msg_id": id, "call_id": id,
		"action": permission.ToolName, "command_type": string(permission.Kind),
		"description": "Local permission requested", "options": options, "questions": permission.Questions,
	}
}

func (c *localCore) confirmPermission(w http.ResponseWriter, r *http.Request, conv *conversation, requestID string) {
	var request struct {
		MessageID   string          `json:"msg_id"`
		Data        json.RawMessage `json:"data"`
		AlwaysAllow bool            `json:"always_allow"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.MessageID != "" && request.MessageID != requestID {
		writeError(w, http.StatusBadRequest, errors.New("permission message does not match the request"))
		return
	}
	conv.operationMu.Lock()
	defer conv.operationMu.Unlock()
	c.mu.RLock()
	permission, exists := conv.pending[requestID]
	backend := conv.backend
	c.mu.RUnlock()
	if !exists || backend == nil {
		writeError(w, http.StatusNotFound, errors.New("pending permission not found"))
		return
	}
	answer, err := permissionAnswer(permission, request.Data, request.AlwaysAllow)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	receipt, err := backend.Dispatch(r.Context(), agentcore.RuntimeCommand{
		CommandID: "permission-" + uuid.NewString(), Kind: agentcore.RuntimeCommandAnswerPermission,
		AnswerPermission: answer,
	})
	if err != nil || !receipt.Accepted {
		writeError(w, http.StatusBadGateway, errors.New("local provider rejected the permission answer"))
		return
	}
	c.mu.Lock()
	_, stillPending := conv.pending[requestID]
	delete(conv.pending, requestID)
	turnID := conv.turnID
	c.mu.Unlock()
	if stillPending {
		c.broadcast(confirmationRemoved(conv.id, turnID, requestID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func confirmationRemoved(conversationID, turnID, requestID string) map[string]any {
	return map[string]any{"name": "confirmation.remove", "data": map[string]any{
		"conversation_id": conversationID, "turn_id": turnID,
		"id": strings.TrimSpace(requestID), "call_id": requestID,
	}}
}
