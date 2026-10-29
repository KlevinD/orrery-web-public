// context-goal-replay renders saved discussion goals without a model or tools.
// Build it once before the change and once after; compare only fixtures whose
// old rendering occurs exactly once in a saved input message.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type fixture struct {
	Name     string                         `json:"name"`
	Pack     agentcore.ContextPack          `json:"pack"`
	TaskID   string                         `json:"task_id"`
	Messages []agentcore.RuntimeChatMessage `json:"messages"`
}

type rendering struct {
	Name             string `json:"name"`
	Context          string `json:"context"`
	ContextBytes     int    `json:"context_bytes"`
	EstimatedTokens  int    `json:"estimated_tokens"`
	InputBytes       int    `json:"input_bytes"`
	ExactOccurrences int    `json:"exact_occurrences"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: context-goal-replay FIXTURES.private.json")
		os.Exit(2)
	}
	file, err := os.Open(os.Args[1])
	check(err)
	defer file.Close()
	var fixtures []fixture
	check(json.NewDecoder(file).Decode(&fixtures))
	results := make([]rendering, 0, len(fixtures))
	for _, item := range fixtures {
		// DiscussionAgentRunner assigns the audit goal to both task goals and
		// ContextPack.WorkerPersonalGoal. The baseline match must independently
		// confirm this reconstruction before a fixture can be counted.
		goal := strings.TrimSpace(item.Pack.WorkerPersonalGoal)
		messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{
			ContextPack: item.Pack,
			Task:        agentcore.TaskInput{ID: item.TaskID, Goal: goal, WorkerPersonalGoal: goal},
			Blocks:      []agentcore.RuntimePromptBlock{{BlockID: agentcore.PromptBlockRuntimeContext, Enabled: true}},
		})
		if len(messages) != 1 {
			panic("unexpected context message count")
		}
		context := messages[0].Content
		result := rendering{Name: item.Name, Context: context, ContextBytes: len(context), EstimatedTokens: agentcore.EstimateRuntimeTextTokens(context)}
		for _, message := range item.Messages {
			result.InputBytes += len(message.Content)
			result.ExactOccurrences += strings.Count(message.Content, context)
		}
		results = append(results, result)
	}
	check(json.NewEncoder(os.Stdout).Encode(results))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
