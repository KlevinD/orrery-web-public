package main

import toolstd "orrery_v2/internal/resource/tool/std"

// This host-owned list admits only established, independent read operations.
// MCP annotations and model-controlled names cannot authorize parallel writes.
func productionAgentCoreParallelToolIDs() []string {
	return []string{
		string(toolstd.KnowledgeSearch), string(toolstd.MemorySearch), string(toolstd.MemoryDiaryRead),
		string(toolstd.SessionSearch), string(toolstd.SessionRead), string(toolstd.ScheduledTaskList),
		string(toolstd.WebSearch), string(toolstd.WebFetch),
		string(toolstd.EnterpriseGraphBuildContextBundle), string(toolstd.EnterpriseGraphSearchObjects),
	}
}
