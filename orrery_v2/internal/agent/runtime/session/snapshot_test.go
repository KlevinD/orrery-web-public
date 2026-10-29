package session

import (
	"reflect"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestSnapshotIsDefensiveCopy(t *testing.T) {
	state := Running(2)
	state.Running.Subagents = []SubagentState{{Ref: "sub", Status: agentcore.SubagentRunning}}
	capabilities := agentcore.Capabilities{
		AcceptsProactiveInput: true,
		AvailableModels:       []agentcore.ModelInfo{{ID: "m", Name: "M", ReasoningEfforts: []string{"high"}}},
		AuthMethods:           []string{"oauth"},
	}
	pending := []agentcore.PendingPermissionView{{RequestID: "p", Questions: []agentcore.PermissionQuestion{{Question: "Continue?", Options: []agentcore.PermissionQuestionOption{{Label: "Yes"}}}}}}
	snapshot, err := NewSnapshot("session", state, 2, 7, capabilities, pending, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	state.Running.Subagents[0].Ref = "mutated"
	capabilities.AvailableModels[0].ReasoningEfforts[0] = "low"
	capabilities.AuthMethods[0] = "mutated"
	pending[0].Questions[0].Question = "mutated"
	pending[0].Questions[0].Options[0].Label = "mutated"
	if snapshot.State.Running.Subagents[0].Ref != "sub" || snapshot.Capabilities.AvailableModels[0].ReasoningEfforts[0] != "high" || snapshot.Capabilities.AuthMethods[0] != "oauth" || snapshot.Pending[0].Questions[0].Question != "Continue?" || snapshot.Pending[0].Questions[0].Options[0].Label != "Yes" {
		t.Fatalf("snapshot shares caller memory: %#v", snapshot)
	}

	clone := snapshot.Clone()
	clone.State.Running.Subagents[0].Ref = "clone-mutated"
	clone.Capabilities.AuthMethods[0] = "clone-mutated"
	clone.Pending[0].Questions[0].Question = "clone-mutated"
	clone.Pending[0].Questions[0].Options[0].Label = "clone-mutated"
	if snapshot.State.Running.Subagents[0].Ref != "sub" || snapshot.Capabilities.AuthMethods[0] != "oauth" || snapshot.Pending[0].Questions[0].Question != "Continue?" || snapshot.Pending[0].Questions[0].Options[0].Label != "Yes" {
		t.Fatal("snapshot clone shares source memory")
	}
}

func TestSnapshotRejectsInconsistentDerivedFields(t *testing.T) {
	snapshot, err := NewSnapshot("session", Idle(), 1, 1, agentcore.Capabilities{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.CanCancel = true
	if err := snapshot.Validate(); err == nil {
		t.Fatal("inconsistent snapshot accepted")
	}
}

func TestSnapshotContractHasNoSecretRawWireOrLocalPathFields(t *testing.T) {
	banned := []string{"credential", "secret", "raw", "argv", "environment", "localpath", "filepath", "cwd"}
	var inspect func(reflect.Type, map[reflect.Type]bool)
	inspect = func(value reflect.Type, visited map[reflect.Type]bool) {
		for value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct || visited[value] {
			return
		}
		visited[value] = true
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			name := strings.ToLower(field.Name)
			for _, token := range banned {
				if strings.Contains(name, token) {
					t.Fatalf("snapshot contract exposes banned field %s.%s", value, field.Name)
				}
			}
			inspect(field.Type, visited)
		}
	}
	inspect(reflect.TypeOf(Snapshot{}), map[reflect.Type]bool{})
}
