package toolbridge

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeToolNameUsesAliasAndNormalizes(t *testing.T) {
	name := RuntimeToolName(agentcore.RuntimeToolBinding{
		ToolID: "tool.mission:prepare",
		Alias:  "mission-prepare",
	})
	if name != "mission_prepare" {
		t.Fatalf("name = %q", name)
	}
}

func TestRuntimeToolNameMapsPersonalShellToRunTerminal(t *testing.T) {
	name := RuntimeToolName(agentcore.RuntimeToolBinding{ToolID: "personal_assistant_shell_exec"})
	if name != "run_terminal" {
		t.Fatalf("name = %q", name)
	}
}

func TestRuntimeToolNameNormalizesProviderUnsafeAlias(t *testing.T) {
	name := RuntimeToolName(agentcore.RuntimeToolBinding{
		ToolID: "enterprise_graph.build_context_bundle",
		Alias:  "Enterprise Graph / Context",
	})
	if name != "Enterprise_Graph_Context" {
		t.Fatalf("name = %q", name)
	}
}

func TestRuntimeToolNameFallsBackFromNonASCIICAlias(t *testing.T) {
	name := RuntimeToolName(agentcore.RuntimeToolBinding{
		ToolID: "enterprise_graph.search_objects",
		Alias:  "企业图谱",
	})
	if name != "enterprise_graph_search_objects" {
		t.Fatalf("name = %q", name)
	}
}

func TestRuntimeToolBindingForCallMatchesAliasOrID(t *testing.T) {
	binding, ok := RuntimeToolBindingForCall("mission_prepare", []agentcore.RuntimeToolBinding{{
		ToolID: "mission.prepare",
		Alias:  "mission-prepare",
	}})
	if !ok || binding.ToolID != "mission.prepare" {
		t.Fatalf("binding = %+v ok=%v", binding, ok)
	}
}

func TestRuntimeToolBindingForCallMatchesNormalizedSpacedAlias(t *testing.T) {
	binding, ok := RuntimeToolBindingForCall("Enterprise_Graph", []agentcore.RuntimeToolBinding{{
		ToolID: "enterprise_graph.build_context_bundle",
		Alias:  "Enterprise Graph",
	}})
	if !ok || binding.ToolID != "enterprise_graph.build_context_bundle" {
		t.Fatalf("binding = %+v ok=%v", binding, ok)
	}
}
