package std

import (
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/resource/tool"
)

func TestToolIDsMatchDefinitions(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	ids := ToolIDs()
	if len(ids) != len(definitions) {
		t.Fatalf("ToolIDs length = %d, want %d", len(ids), len(definitions))
	}
	seen := make(map[agentcore.ToolID]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			t.Fatal("ToolIDs contains empty id")
		}
		if seen[id] {
			t.Fatalf("ToolIDs contains duplicate id %q", id)
		}
		seen[id] = true
	}
	for _, definition := range definitions {
		if !seen[definition.ID] {
			t.Fatalf("ToolIDs missing definition id %q", definition.ID)
		}
	}
}

func TestDefinitionsUseStandardDiscussionPermission(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	permissionByID := map[string]string{}
	for _, definition := range definitions {
		if len(definition.Permissions) != 1 {
			t.Fatalf("%s permissions length = %d, want 1", definition.ID, len(definition.Permissions))
		}
		permissionByID[string(definition.ID)] = definition.Permissions[0].Scope
	}
	for _, id := range []string{string(DiscussionMemberAdd), string(DiscussionAck)} {
		if permissionByID[id] != PermissionDiscussionWrite {
			t.Fatalf("%s permission = %q, want %q", id, permissionByID[id], PermissionDiscussionWrite)
		}
	}
	if permissionByID[string(DiscussionTalentList)] != PermissionDiscussionRead {
		t.Fatalf("%s permission = %q, want %q", DiscussionTalentList, permissionByID[string(DiscussionTalentList)], PermissionDiscussionRead)
	}
	for _, id := range []string{string(MissionPrepare), string(MissionCoordinatorPlanParallel), string(DiscussionClose)} {
		if permissionByID[id] != PermissionMissionWrite {
			t.Fatalf("%s permission = %q, want %q", id, permissionByID[id], PermissionMissionWrite)
		}
	}
}

func TestDefinitionsPublishCuratedMCPToolSurface(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	published := map[agentcore.ToolID]bool{}
	for _, id := range ToolIDs() {
		published[id] = true
	}
	for _, definition := range definitions {
		published[definition.ID] = true
	}

	for _, id := range []agentcore.ToolID{
		MissionContextList,
		MissionContextSelect,
		MissionBriefCreate,
		MissionTaskCreateRoot,
		MissionTaskDelegate,
	} {
		if published[id] {
			t.Fatalf("deprecated Mission tool %q should not be published", id)
		}
	}

	for _, id := range []agentcore.ToolID{
		MissionPrepare,
		MissionCoordinatorPlanParallel,
		DiscussionTalentList,
		DiscussionMemberAdd,
		DiscussionAck,
		DiscussionClose,
	} {
		if !published[id] {
			t.Fatalf("curated system tool %q should be published", id)
		}
	}
}

func TestSkillToolDefinitionsArePublishedThroughSystemMCPServer(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	byID := map[agentcore.ToolID]bool{}
	for _, definition := range definitions {
		byID[definition.ID] = true
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	if system.MCP == nil {
		t.Fatalf("system mcp definition missing MCP config")
	}
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}

	for _, id := range []agentcore.ToolID{SkillList, SkillLoad, SkillResourceList, SkillResourceLoad} {
		if !byID[id] {
			t.Fatalf("skill runtime tool %q should be registered as a system tool", id)
		}
		if !exposed[string(id)] {
			t.Fatalf("skill runtime tool %q should be exposed through mcp.system", id)
		}
	}
}

func TestKnowledgeSearchToolIsPublishedThroughSystemMCPServer(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	byID := map[agentcore.ToolID]bool{}
	enabledByID := map[agentcore.ToolID]bool{}
	permissionByID := map[agentcore.ToolID]string{}
	for _, definition := range definitions {
		byID[definition.ID] = true
		enabledByID[definition.ID] = definition.Enabled
		if len(definition.Permissions) > 0 {
			permissionByID[definition.ID] = definition.Permissions[0].Scope
		}
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}
	if !byID[KnowledgeSearch] {
		t.Fatalf("knowledge search should be registered as a system tool")
	}
	if !enabledByID[KnowledgeSearch] {
		t.Fatalf("knowledge search should be enabled")
	}
	if !exposed[string(KnowledgeSearch)] {
		t.Fatalf("knowledge search should be exposed through mcp.system")
	}
	if permissionByID[KnowledgeSearch] != PermissionKnowledgeRead {
		t.Fatalf("knowledge search permission = %q, want %q", permissionByID[KnowledgeSearch], PermissionKnowledgeRead)
	}
	if len(definitionByID(definitions)[KnowledgeSearch].InputSchema) == 0 {
		t.Fatalf("knowledge search input schema should be published")
	}
	properties, ok := definitionByID(definitions)[KnowledgeSearch].InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("knowledge search properties = %+v", definitionByID(definitions)[KnowledgeSearch].InputSchema)
	}
	mode, ok := properties["retrieval_mode"].(map[string]any)
	if !ok {
		t.Fatalf("retrieval mode schema = %+v", properties["retrieval_mode"])
	}
	values, ok := mode["enum"].([]any)
	if !ok || len(values) != 3 || values[0] != "auto" || values[1] != "fast" || values[2] != "agentic" {
		t.Fatalf("retrieval mode enum = %+v", mode["enum"])
	}
	outputProperties, ok := definitionByID(definitions)[KnowledgeSearch].OutputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("knowledge search output schema = %+v", definitionByID(definitions)[KnowledgeSearch].OutputSchema)
	}
	retrievals, ok := outputProperties["retrievals"].(map[string]any)
	if !ok || retrievals["type"] != "array" {
		t.Fatalf("knowledge search retrieval output schema = %+v", outputProperties["retrievals"])
	}
	receipt, ok := retrievals["items"].(map[string]any)
	if !ok {
		t.Fatalf("knowledge search retrieval receipt schema = %+v", retrievals["items"])
	}
	receiptProperties, ok := receipt["properties"].(map[string]any)
	if !ok || receiptProperties["requested_mode"] == nil || receiptProperties["actual_mode"] == nil || receiptProperties["fallback_reason"] == nil {
		t.Fatalf("knowledge search retrieval receipt properties = %+v", receiptProperties)
	}
}

func TestKnowledgeAddToolIsPublishedThroughSystemMCPServer(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definition := definitionByID(definitions)[KnowledgeAdd]
	if definition.ID == "" {
		t.Fatalf("knowledge add should be registered as a system tool")
	}
	if !definition.Enabled {
		t.Fatalf("knowledge add should be enabled")
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionKnowledgeWrite {
		t.Fatalf("knowledge add permissions = %+v, want %q", definition.Permissions, PermissionKnowledgeWrite)
	}
	if len(definition.InputSchema) == 0 || len(definition.OutputSchema) == 0 {
		t.Fatalf("knowledge add schemas should be published: input=%+v output=%+v", definition.InputSchema, definition.OutputSchema)
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}
	if !exposed[string(KnowledgeAdd)] {
		t.Fatalf("knowledge add should be exposed through mcp.system: %+v", system.MCP.ExposedTools)
	}
}

func TestEmailSendToolIsPublishedAsHighRiskSystemTool(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definition := definitionByID(definitions)[EmailSend]
	if definition.ID == "" || !definition.Enabled {
		t.Fatal("email send should be an enabled system tool")
	}
	if definition.RiskLevel != tool.RiskHigh || len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionEmailSend {
		t.Fatalf("email definition = %+v", definition)
	}
	for _, field := range []string{"to", "subject"} {
		if !schemaRequires(definition.InputSchema, field) {
			t.Fatalf("email input schema should require %s: %+v", field, definition.InputSchema)
		}
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := false
	for _, name := range system.MCP.ExposedTools {
		exposed = exposed || name == string(EmailSend)
	}
	if !exposed {
		t.Fatalf("email send should be exposed through mcp.system: %+v", system.MCP.ExposedTools)
	}
}

func TestRequestBusinessApprovalToolRequiresHumanReadableMaterial(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definition := definitionByID(definitions)[RequestBusinessApproval]
	if definition.ID == "" || !definition.Enabled {
		t.Fatal("request_business_approval should be an enabled system tool")
	}
	if definition.RiskLevel != tool.RiskHigh || len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionApprovalRequest {
		t.Fatalf("business approval definition = %+v", definition)
	}
	for _, field := range []string{
		"title", "decision_request", "business_context", "proposed_action",
		"expected_result", "impact_scope", "risks", "resource_kind", "resource_id", "idempotency_key",
	} {
		if !schemaRequires(definition.InputSchema, field) {
			t.Fatalf("business approval input should require %s: %+v", field, definition.InputSchema)
		}
	}
	properties, _ := definition.InputSchema["properties"].(map[string]any)
	for _, forbidden := range []string{"approval_mode", "approval_assigned_to", "template_id"} {
		if _, exists := properties[forbidden]; exists {
			t.Fatalf("Agent must not control %s: %+v", forbidden, definition.InputSchema)
		}
	}
	if !strings.Contains(definition.Description, "必须") || !strings.Contains(definition.Description, "中文") {
		t.Fatalf("description should tell Agents the material is mandatory Chinese: %q", definition.Description)
	}
	if strings.Contains(definition.Description, "长任务执行前必须") || !strings.Contains(definition.Description, "已有授权") || !strings.Contains(definition.Description, "受保护动作") {
		t.Fatalf("approval tool must distinguish existing authorization from protected actions: %q", definition.Description)
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := false
	for _, name := range system.MCP.ExposedTools {
		exposed = exposed || name == string(RequestBusinessApproval)
	}
	if !exposed {
		t.Fatalf("request_business_approval should be exposed through mcp.system")
	}
}

func TestPersonalAssistantShellExecToolIsPublishedAsTemporarySystemTool(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definition := definitionByID(definitions)[PersonalAssistantShellExec]
	if definition.ID == "" {
		t.Fatalf("personal assistant shell exec should be registered as a system tool")
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}
	if !exposed[string(PersonalAssistantShellExec)] {
		t.Fatalf("personal assistant shell exec should be exposed through mcp.system")
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionPersonalAssistantShell {
		t.Fatalf("permission = %+v, want %q", definition.Permissions, PermissionPersonalAssistantShell)
	}
	if definition.RiskLevel != tool.RiskHigh {
		t.Fatalf("risk = %q, want high", definition.RiskLevel)
	}
	if !schemaRequires(definition.InputSchema, "command") {
		t.Fatalf("input schema should require command: %+v", definition.InputSchema)
	}
	notes := strings.ToLower(definition.RuntimeRequirements.Notes)
	if !strings.Contains(notes, "temporary") || !strings.Contains(notes, "dind") {
		t.Fatalf("temporary DinD design warning missing from runtime notes: %q", definition.RuntimeRequirements.Notes)
	}
}

func TestEnterpriseGraphBuildContextBundleToolIsPublishedThroughSystemMCPServer(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definition := definitionByID(definitions)[EnterpriseGraphBuildContextBundle]
	if definition.ID == "" {
		t.Fatalf("enterprise graph build context bundle should be registered as a system tool")
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}
	if !exposed[string(EnterpriseGraphBuildContextBundle)] {
		t.Fatalf("enterprise graph build context bundle should be exposed through mcp.system")
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionEnterpriseGraphRead {
		t.Fatalf("permission = %+v, want %q", definition.Permissions, PermissionEnterpriseGraphRead)
	}
	if definition.RiskLevel != tool.RiskLow {
		t.Fatalf("risk = %q, want low", definition.RiskLevel)
	}
	for _, field := range []string{"tenant_id", "consumer", "purpose", "actor_ref", "subject_refs"} {
		if !schemaRequires(definition.InputSchema, field) {
			t.Fatalf("input schema should require %s: %+v", field, definition.InputSchema)
		}
	}
	assertEnterpriseGraphIncludeKindsBounded(t, definition.InputSchema)
	if !schemaHasProperty(definition.OutputSchema, "permission_constraints") {
		t.Fatalf("output schema should include permission_constraints: %+v", definition.OutputSchema)
	}
	properties, ok := definition.OutputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("output schema should define properties: %+v", definition.OutputSchema)
	}
	buildTrace, ok := properties["build_trace"].(map[string]any)
	if !ok {
		t.Fatalf("output schema should include build_trace: %+v", definition.OutputSchema)
	}
	if schemaHasProperty(buildTrace, "hidden_object_ids") {
		t.Fatalf("output schema must not expose concealed object ids: %+v", buildTrace)
	}
}

func TestEnterpriseGraphSearchObjectsToolIsPublishedThroughSystemMCPServer(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definition := definitionByID(definitions)[EnterpriseGraphSearchObjects]
	if definition.ID == "" {
		t.Fatal("enterprise graph search objects should be registered")
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionEnterpriseGraphRead || definition.RiskLevel != tool.RiskLow {
		t.Fatalf("definition = %+v", definition)
	}
	for _, field := range []string{"tenant_id", "actor_ref", "query"} {
		if !schemaRequires(definition.InputSchema, field) {
			t.Fatalf("input schema missing %s: %+v", field, definition.InputSchema)
		}
	}
	for _, field := range []string{"include_kinds", "limit", "cursor"} {
		if !schemaHasProperty(definition.InputSchema, field) {
			t.Fatalf("input schema missing optional %s: %+v", field, definition.InputSchema)
		}
	}
	assertEnterpriseGraphIncludeKindsBounded(t, definition.InputSchema)
	exposed := map[string]bool{}
	for _, id := range SystemMCPServerDefinition(time.Unix(0, 0).UTC()).MCP.ExposedTools {
		exposed[id] = true
	}
	if !exposed[string(EnterpriseGraphSearchObjects)] {
		t.Fatal("enterprise graph search objects should be exposed through mcp.system")
	}
}

func assertEnterpriseGraphIncludeKindsBounded(t *testing.T, schema map[string]any) {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %+v", schema)
	}
	kinds, ok := properties["include_kinds"].(map[string]any)
	if !ok || kinds["maxItems"] != 8 {
		t.Fatalf("include_kinds schema = %+v", kinds)
	}
	items, ok := kinds["items"].(map[string]any)
	if !ok || items["maxLength"] != 256 {
		t.Fatalf("include_kinds items schema = %+v", items)
	}
}

func TestMemorySearchToolIsPublishedAsLowRiskReadOnlyCapability(t *testing.T) {
	definition := definitionByID(Definitions(time.Unix(0, 0).UTC()))[MemorySearch]
	if definition.ID == "" {
		t.Fatal("memory search should be registered")
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionMemoryRead {
		t.Fatalf("permissions = %+v, want %q", definition.Permissions, PermissionMemoryRead)
	}
	if definition.RiskLevel != tool.RiskLow {
		t.Fatalf("risk = %q, want low", definition.RiskLevel)
	}
	if !schemaRequires(definition.InputSchema, "query") || !schemaHasProperty(definition.OutputSchema, "personal_digest") {
		t.Fatalf("memory search schemas = input:%+v output:%+v", definition.InputSchema, definition.OutputSchema)
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	for _, exposed := range system.MCP.ExposedTools {
		if exposed == string(MemorySearch) {
			return
		}
	}
	t.Fatal("memory search should be exposed through mcp.system")
}

func TestEnsureEnterpriseGraphReadToolRefAddsSearchAndContextTools(t *testing.T) {
	refs := EnsureEnterpriseGraphReadToolRef([]agentcore.ToolRef{{
		ToolID: EnterpriseGraphBuildContextBundle, PermissionScope: "stale.permission",
	}})
	counts := map[agentcore.ToolID]int{}
	for _, ref := range refs {
		if ref.ToolID == EnterpriseGraphBuildContextBundle || ref.ToolID == EnterpriseGraphSearchObjects {
			if ref.PermissionScope != PermissionEnterpriseGraphRead {
				t.Fatalf("tool %s permission = %q", ref.ToolID, ref.PermissionScope)
			}
			counts[ref.ToolID]++
		}
	}
	if counts[EnterpriseGraphBuildContextBundle] != 1 || counts[EnterpriseGraphSearchObjects] != 1 {
		t.Fatalf("enterprise graph tool counts = %#v", counts)
	}
}

func TestWebToolsArePublishedAsLowRiskReadOnlyCapabilities(t *testing.T) {
	definitions := definitionByID(Definitions(time.Unix(0, 0).UTC()))
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}
	for _, id := range []agentcore.ToolID{WebSearch, WebFetch} {
		definition := definitions[id]
		if definition.ID == "" || definition.RiskLevel != tool.RiskLow {
			t.Fatalf("web tool %s definition = %+v", id, definition)
		}
		if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionWebRead {
			t.Fatalf("web tool %s permissions = %+v", id, definition.Permissions)
		}
		if len(definition.InputSchema) == 0 || len(definition.OutputSchema) == 0 || !exposed[string(id)] {
			t.Fatalf("web tool %s is not fully published", id)
		}
	}
	if !schemaRequires(definitions[WebSearch].InputSchema, "query") || !schemaRequires(definitions[WebFetch].InputSchema, "url") {
		t.Fatalf("web tool input schemas are incomplete")
	}
}

func TestSchedulerToolDefinitionsExposeSchemasAndPermissions(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definitionByID := map[agentcore.ToolID]struct {
		inputSchema  map[string]any
		outputSchema map[string]any
		permission   string
	}{}
	for _, definition := range definitions {
		if len(definition.Permissions) != 1 {
			t.Fatalf("%s permissions length = %d, want 1", definition.ID, len(definition.Permissions))
		}
		definitionByID[definition.ID] = struct {
			inputSchema  map[string]any
			outputSchema map[string]any
			permission   string
		}{
			inputSchema:  definition.InputSchema,
			outputSchema: definition.OutputSchema,
			permission:   definition.Permissions[0].Scope,
		}
	}

	cases := []struct {
		id         agentcore.ToolID
		permission string
	}{
		{ScheduledTaskCreate, PermissionSchedulerWrite},
		{ScheduledTaskUpdate, PermissionSchedulerWrite},
		{ScheduledTaskCancel, PermissionSchedulerWrite},
		{ScheduledTaskList, PermissionSchedulerRead},
		{ScheduledTaskRunNow, PermissionSchedulerRun},
	}
	for _, tc := range cases {
		definition, ok := definitionByID[tc.id]
		if !ok {
			t.Fatalf("scheduler tool %q was not published", tc.id)
		}
		if len(definition.inputSchema) == 0 {
			t.Fatalf("%s input schema is empty", tc.id)
		}
		if len(definition.outputSchema) == 0 {
			t.Fatalf("%s output schema is empty", tc.id)
		}
		if definition.permission != tc.permission {
			t.Fatalf("%s permission = %q, want %q", tc.id, definition.permission, tc.permission)
		}
	}
}

func TestSchedulerCreateSchemaExposesMemoryDistillationTargets(t *testing.T) {
	definition := definitionByID(Definitions(time.Unix(0, 0).UTC()))[ScheduledTaskCreate]
	target, ok := definition.InputSchema["properties"].(map[string]any)["target"].(map[string]any)
	if !ok {
		t.Fatalf("target schema missing: %+v", definition.InputSchema)
	}
	properties, ok := target["properties"].(map[string]any)
	if !ok {
		t.Fatalf("target properties missing: %+v", target)
	}
	mode, ok := properties["mode"].(map[string]any)
	if !ok || !enumContains(mode, "memory_experience_distillation") || !enumContains(mode, "memory_world_consolidation") || !enumContains(mode, "memory_lifecycle_sweep") {
		t.Fatalf("target mode schema = %+v, want memory distillation modes", mode)
	}
	for _, field := range []string{"memory_topic", "source_scope_kind", "source_scope_id", "target_scope_kind", "target_scope_id", "memory_record_limit"} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("target schema missing %s: %+v", field, properties)
		}
	}
}

func TestEvaluationReviewToolDefinitionIsPublishedThroughSystemMCPServer(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definition := definitionByID(definitions)[EvaluationReview]
	if definition.ID == "" {
		t.Fatalf("evaluation review tool should be registered")
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionEvaluationReview {
		t.Fatalf("permission = %+v, want %q", definition.Permissions, PermissionEvaluationReview)
	}
	if !schemaRequires(definition.InputSchema, "mode") || !schemaRequires(definition.InputSchema, "summary") {
		t.Fatalf("input schema required fields = %+v", definition.InputSchema["required"])
	}
	properties, ok := definition.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input schema properties missing: %+v", definition.InputSchema)
	}
	mode, ok := properties["mode"].(map[string]any)
	if !ok || !enumContains(mode, "all_changed") || !enumContains(mode, "agent") {
		t.Fatalf("mode schema = %+v, want all_changed and agent", mode)
	}

	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}
	if !exposed[string(EvaluationReview)] {
		t.Fatalf("evaluation review tool should be exposed through mcp.system")
	}
}

func TestMemoryDiaryWriteToolDefinitionRemainsRegisteredForAdministrativeBackfill(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	definition := definitionByID(definitions)[MemoryDiaryWrite]
	if definition.ID == "" {
		t.Fatalf("memory diary write tool should be registered")
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionMemoryDiaryWrite {
		t.Fatalf("permission = %+v, want %q", definition.Permissions, PermissionMemoryDiaryWrite)
	}
	if !schemaRequires(definition.InputSchema, "mode") {
		t.Fatalf("input schema required fields = %+v", definition.InputSchema["required"])
	}
	properties, ok := definition.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input schema properties missing: %+v", definition.InputSchema)
	}
	mode, ok := properties["mode"].(map[string]any)
	if !ok || !enumContains(mode, "all_agents") || !enumContains(mode, "agent") {
		t.Fatalf("mode schema = %+v, want all_agents and agent", mode)
	}

	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}
	if exposed[string(MemoryDiaryWrite)] {
		t.Fatalf("legacy memory diary rollup must not be exposed through ordinary mcp.system bindings")
	}
}

func TestMemoryDiaryMaintenanceToolsAreRegisteredButNotOrdinarilyExposed(t *testing.T) {
	definitions := definitionByID(Definitions(time.Unix(0, 0).UTC()))
	read := definitions[MemoryDiaryRead]
	appendTool := definitions[MemoryDiaryAppend]
	if read.ID == "" || appendTool.ID == "" {
		t.Fatalf("diary maintenance tools missing: read=%+v append=%+v", read, appendTool)
	}
	if len(read.Permissions) != 1 || read.Permissions[0].Scope != PermissionMemoryDiaryRead {
		t.Fatalf("read permissions = %+v", read.Permissions)
	}
	if len(appendTool.Permissions) != 1 || appendTool.Permissions[0].Scope != PermissionMemoryDiaryWrite {
		t.Fatalf("append permissions = %+v", appendTool.Permissions)
	}
	if !schemaRequires(read.InputSchema, "path") || !schemaRequires(appendTool.InputSchema, "path") || !schemaRequires(appendTool.InputSchema, "content") {
		t.Fatalf("maintenance schemas = read:%+v append:%+v", read.InputSchema, appendTool.InputSchema)
	}
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	for _, exposed := range system.MCP.ExposedTools {
		if exposed == string(MemoryDiaryRead) || exposed == string(MemoryDiaryAppend) {
			t.Fatalf("maintenance tool %q leaked into ordinary mcp.system exposure", exposed)
		}
	}
}

func TestSchedulerCreateSchemaDoesNotExposeEvaluationReviewTarget(t *testing.T) {
	definition := definitionByID(Definitions(time.Unix(0, 0).UTC()))[ScheduledTaskCreate]
	target, ok := definition.InputSchema["properties"].(map[string]any)["target"].(map[string]any)
	if !ok {
		t.Fatalf("target schema missing: %+v", definition.InputSchema)
	}
	properties, ok := target["properties"].(map[string]any)
	if !ok {
		t.Fatalf("target properties missing: %+v", target)
	}
	mode, ok := properties["mode"].(map[string]any)
	if !ok {
		t.Fatalf("target mode schema missing: %+v", properties)
	}
	if enumContains(mode, "evaluation_review") {
		t.Fatalf("target mode schema = %+v, evaluation_review must be a tool call, not a scheduled target", mode)
	}
	if enumContains(mode, "memory_diary_write") {
		t.Fatalf("target mode schema = %+v, memory_diary_write must be a tool call, not a scheduled target", mode)
	}
	for _, field := range []string{"evaluation_scope_kind", "evaluation_scope_id", "evaluator_agent_id", "evaluation_window_days"} {
		if _, ok := properties[field]; ok {
			t.Fatalf("target schema should not contain %s: %+v", field, properties)
		}
	}
}

func definitionByID(definitions []tool.ToolDefinition) map[agentcore.ToolID]tool.ToolDefinition {
	out := map[agentcore.ToolID]tool.ToolDefinition{}
	for _, definition := range definitions {
		out[definition.ID] = definition
	}
	return out
}

func enumContains(schema map[string]any, want string) bool {
	values, ok := schema["enum"].([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestDiscussionAndMissionToolDefinitionsExposeInputSchemas(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	byID := map[agentcore.ToolID]map[string]any{}
	for _, definition := range definitions {
		byID[definition.ID] = definition.InputSchema
	}

	memberAdd := byID[DiscussionMemberAdd]
	if len(memberAdd) == 0 || !schemaRequires(memberAdd, "agent_ids") || !schemaHasProperty(memberAdd, "agent_ids") {
		t.Fatalf("discussion_member_add schema = %+v", memberAdd)
	}
	talentList := byID[DiscussionTalentList]
	if len(talentList) == 0 || !schemaHasProperty(talentList, "placement") || !schemaHasProperty(talentList, "limit") {
		t.Fatalf("discussion_talent_list schema = %+v", talentList)
	}
	prepare := byID[MissionPrepare]
	if len(prepare) == 0 ||
		!schemaRequires(prepare, "title") ||
		!schemaRequires(prepare, "final_goal") ||
		!schemaRequires(prepare, "acceptance_criteria") ||
		!schemaRequires(prepare, "required_evidence") ||
		!schemaRequires(prepare, "idempotency_key") {
		t.Fatalf("mission_prepare schema = %+v", prepare)
	}
	parallel := byID[MissionCoordinatorPlanParallel]
	if len(parallel) == 0 ||
		!schemaRequires(parallel, "idempotency_key") ||
		!schemaHasProperty(parallel, "worker_lanes") ||
		!schemaHasProperty(parallel, "lanes_json") ||
		!schemaHasProperty(parallel, "worker_lanes_json") {
		t.Fatalf("mission_coordinator_plan_parallel schema = %+v", parallel)
	}
	if _, ok := parallel["anyOf"].([]any); !ok {
		t.Fatalf("mission_coordinator_plan_parallel schema should accept structured and legacy lane inputs: %+v", parallel)
	}
}

func schemaHasProperty(schema map[string]any, name string) bool {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = properties[name]
	return ok
}

func schemaRequires(schema map[string]any, name string) bool {
	for _, value := range schema["required"].([]any) {
		if value == name {
			return true
		}
	}
	return false
}
