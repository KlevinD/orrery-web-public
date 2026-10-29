package core

import "strings"

// RuntimeCurrentInputGoalVariable is a host-provided presentation hint for a
// synthetic discussion goal. It never replaces the audited task contract.
const RuntimeCurrentInputGoalVariable = "current_input_goal"

type runtimePromptGoal struct {
	Label   string
	Content string
}

// Only equal, complete goal values share a line. All scope labels stay visible;
// similar goals, nested goals and repeated text in history are not deduplicated.
func writeRuntimePromptGoal(b *strings.Builder, label string, goals []runtimePromptGoal) {
	for i, goal := range goals {
		if goal.Label != label || goal.Content == "" {
			continue
		}
		labels := []string{label}
		for j := i + 1; j < len(goals); j++ {
			if goals[j].Content == goal.Content {
				labels = append(labels, goals[j].Label)
				goals[j].Content = ""
			}
		}
		writeNamedPromptLine(b, strings.Join(labels, " / "), goal.Content)
		return
	}
}

func compactCurrentInputGoal(req AgentPromptMessagesRequest, blocks []RuntimePromptBlock, segments promptInputSegments, variables map[string]string) {
	goal := strings.TrimSpace(req.RuntimeVariables[RuntimeCurrentInputGoalVariable])
	current := promptMessagesVariableValue(segments.Current)
	if goal == "" || current == "" || !strings.Contains(current, goal) ||
		variables[PromptBlockCurrentUserInput] != current {
		return
	}
	if _, explicit := runtimePromptVariable(req, PromptBlockRuntimeContext); explicit {
		return
	}
	// Editable or standalone context blocks may omit the current input. Only
	// compact the built-in combined template, with its full input still present.
	combined := false
	for _, block := range blocks {
		if block.BlockID == PromptBlockRuntimeInputTemplate && block.Enabled && !block.VariableOnly &&
			block.Role == RuntimePromptRoleUser && block.Content == defaultRuntimeInputTemplatePrompt() &&
			strings.Contains(resolvePromptVariables(block.Content, variables), current) {
			combined = true
		}
	}
	if !combined || variables[PromptBlockRuntimeContext] != buildRuntimeContextPrompt(req) {
		return
	}
	// Mutate a rendering copy; other goals, constraints and source records stay
	// intact. The current-input section in this same message owns the full text.
	for _, value := range []*string{&req.ContextPack.MissionFinalGoal, &req.ContextPack.WorkerPersonalGoal, &req.Task.Goal, &req.Task.WorkerPersonalGoal} {
		if strings.TrimSpace(*value) == goal {
			*value = ""
		}
	}
	context := buildRuntimeContextPrompt(req)
	if context == "## 当前任务上下文" {
		context = ""
	}
	variables[PromptBlockRuntimeContext] = context
}

// The built-in combined message already contains the complete discussion goal.
// Keep every task scope label while referring to that single authoritative copy.
// Custom templates and explicit/structured contexts retain their original text.
func compactDiscussionGoal(req AgentPromptMessagesRequest, blocks []RuntimePromptBlock, variables map[string]string) {
	goal := strings.TrimSpace(req.ContextPack.DiscussionGroupGoal)
	discussion := "## 讨论组目标\n" + goal
	if goal == "" || variables[PromptBlockDiscussionGoal] != discussion || len(req.RuntimeContextParts) > 0 {
		return
	}
	if _, explicit := runtimePromptVariable(req, PromptBlockRuntimeContext); explicit {
		return
	}
	combined := false
	for _, block := range blocks {
		if block.BlockID == PromptBlockRuntimeInputTemplate && block.Enabled && !block.VariableOnly &&
			block.Role == RuntimePromptRoleUser && block.Content == defaultRuntimeInputTemplatePrompt() &&
			strings.Contains(resolvePromptVariables(block.Content, variables), discussion) {
			combined = true
		}
	}
	if !combined || variables[PromptBlockRuntimeContext] != buildRuntimeContextPrompt(req) {
		return
	}
	for _, value := range []*string{&req.ContextPack.MissionFinalGoal, &req.ContextPack.WorkerPersonalGoal, &req.Task.Goal, &req.Task.WorkerPersonalGoal} {
		if strings.TrimSpace(*value) == goal {
			*value = "见“讨论组目标”"
		}
	}
	variables[PromptBlockRuntimeContext] = buildRuntimeContextPrompt(req)
}
