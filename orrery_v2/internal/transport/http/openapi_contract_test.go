package httptransport

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsStage10Routes(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	routes := []string{
		"/api/health",
		"/api/missions/{mission_id}/control",
		"/api/missions/{mission_id}/ops",
		"/api/missions/{mission_id}/team",
		"/api/missions/{mission_id}/tasks",
		"/api/missions/{mission_id}/progress",
		"/api/missions/{mission_id}/evidence",
		"/api/missions/{mission_id}/qa",
		"/api/missions/{mission_id}/discussion",
		"/api/missions",
		"/api/missions/{mission_id}/tasks/{task_id}/submit-result",
		"/api/missions/{mission_id}/tasks/{task_id}/assign",
		"/api/missions/{mission_id}/tasks/{task_id}/agent-runs",
		"/api/missions/{mission_id}/agent-runs/{run_id}/cancel",
		"/api/missions/{mission_id}/agent-runs/{run_id}/retry",
		"/api/missions/{mission_id}/tasks",
		"/api/missions/{mission_id}/qa/{qa_id}/decision",
		"/api/missions/{mission_id}/discussion/messages",
		"/api/missions/{mission_id}/discussion/messages/{message_id}/reply",
		"/api/inbox",
		"/api/inbox/{item_id}/read",
		"/api/inbox/{item_id}/close",
		"/api/approvals",
		"/api/approvals/{approval_id}/decision",
		"/api/hil/decisions",
		"/api/hil/decisions/{decision_id}",
		"/api/hil/decisions/{decision_id}/decision",
		"/api/hil/workflow-templates",
		"/api/hil/workflow-templates/{template_id}",
		"/api/hil/workflow-templates/{template_id}/draft",
		"/api/hil/workflow-templates/{template_id}/publish",
		"/api/hil/workflow-templates/{template_id}/disable",
		"/api/hil/workflow-template-bindings/{action_type_id}",
		"/api/hil/workflow-instances",
		"/api/hil/workflow-instances/{instance_id}",
		"/api/hil/workflow-instances/{instance_id}/viewed",
		"/api/hil/workflow-instances/{instance_id}/resubmit",
		"/api/hil/workflow-tasks/{task_id}/decision",
		"/api/hil/workflow-tasks/{task_id}/transfer",
		"/api/hil/workflow-tasks/{task_id}/reassign",
		"/api/resources/tools",
		"/api/resources/tools/call",
		"/api/resources/tools/{tool_id}",
		"/api/resources/tools/{tool_id}/disable",
		"/api/resources/skills",
		"/api/resources/skills/{skill_id}",
		"/api/resources/skills/{skill_id}/versions",
		"/api/resources/skills/{skill_id}/versions/{version}/promote",
		"/api/resources/skills/{skill_id}/versions/{version}/rollback",
		"/api/resources/skills/{skill_id}/disable",
		"/api/resources/skills/{skill_id}/export",
		"/api/resources/skills/import",
		"/api/resources/skill-bundles",
		"/api/resources/skill-bundles/{bundle_id}",
		"/api/resources/skill-bundles/{bundle_id}/skills",
		"/api/resources/skill-bundles/{bundle_id}/skills/import",
		"/api/resources/skill-bundles/{bundle_id}/skills/{skill_id}",
		"/api/resources/mcp-servers",
		"/api/resources/mcp-servers/{tool_id}",
		"/api/resources/mcp-servers/discover",
		"/api/resources/mcp-servers/{tool_id}/sync",
		"/api/resources/mcp-servers/{tool_id}/health",
		"/api/resources/mcp-servers/{tool_id}/disable",
		"/api/resources/bindings",
		"/api/resources/memory-profiles",
		"/api/resources/memory-profiles/{memory_profile_id}",
		"/api/resources/memory-profiles/personal",
		"/api/resources/memory-profiles/digest",
		"/api/workforce/members",
		"/api/talent-market/members",
		"/api/workforce/members/{agent_profile_id}",
		"/api/workforce/members/{agent_profile_id}/channel-bindings",
		"/api/workforce/members/{agent_profile_id}/channel-bindings/feishu/{account_id}",
		"/api/workforce/avatar-defaults",
		"/api/workforce/avatar-defaults/{default_id}",
		"/api/workforce/members/{agent_profile_id}/avatar",
		"/api/workforce/members/{agent_profile_id}/avatar/default",
		"/api/workforce/members/{agent_profile_id}/release-to-market",
		"/api/talent-market/members/{agent_profile_id}",
		"/api/talent-market/members/delete-batch",
		"/api/talent-market/members/{agent_profile_id}/hire",
		"/api/workforce/members/{agent_profile_id}/enable",
		"/api/workforce/members/{agent_profile_id}/disable",
		"/api/org/graph",
		"/api/org",
		"/api/org/{organization_id}",
		"/api/org/units",
		"/api/org/positions",
		"/api/org/positions/{position_id}",
		"/api/org/units/{department_id}",
		"/api/authorization/relation-backfill/diagnostics",
		"/api/authorization/enforce-transition/diagnostics",
		"/api/auth/session",
		"/api/oauth2/login",
		"/api/oauth2/consent",
		"/api/oauth2/consent/pending",
		"/api/oauth2/consent/accept",
		"/api/oauth2/consent/reject",
		"/api/oauth2/clients",
		"/api/permissions/capabilities",
		"/api/permissions/matrix",
		"/api/permissions/policy",
		"/api/audit-log",
		"/api/evaluations",
		"/api/evaluations/stream",
		"/api/settings",
		"/api/settings/runtime/discovery",
		"/api/settings/knowledge/pageindex/test",
		"/api/missions/{mission_id}/stream",
		"/api/discussions",
		"/api/discussions/messages",
		"/api/discussions/{discussion_id}",
		"/api/discussions/{discussion_id}/messages",
		"/api/discussions/{discussion_id}/abort",
		"/api/discussions/{discussion_id}/interjections",
		"/api/discussions/{discussion_id}/workspace",
		"/api/discussions/{discussion_id}/approvals/{approval_id}/decision",
		"/api/discussions/{discussion_id}/debug/latest",
		"/api/discussions/{discussion_id}/runs/{run_id}/debug",
		"/api/scheduled-tasks",
		"/api/scheduled-tasks/{scheduled_task_id}",
		"/api/scheduled-tasks/{scheduled_task_id}/cancel",
		"/api/scheduled-tasks/{scheduled_task_id}/run-now",
		"/api/scheduled-tasks/{scheduled_task_id}/runs",
		"/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/responsibility-evaluations",
		"/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/artifacts/content",
		"/api/scheduled-runs",
		"/api/scheduled-runs/{scheduled_run_id}/read",
	}
	for _, route := range routes {
		if !strings.Contains(doc, route+":") {
			t.Fatalf("OpenAPI contract missing route %s", route)
		}
	}
}

func TestOpenAPIContractTypesMissionControlReadViews(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse openapi: %v", err)
	}
	paths := parsed["paths"].(map[string]any)
	wantRefs := map[string]string{
		"/api/missions/{mission_id}/control":    "#/components/schemas/MissionControlViewDTO",
		"/api/missions/{mission_id}/team":       "#/components/schemas/TeamDTO",
		"/api/missions/{mission_id}/tasks":      "#/components/schemas/TaskBoardDTO",
		"/api/missions/{mission_id}/progress":   "#/components/schemas/ProgressGraphDTO",
		"/api/missions/{mission_id}/evidence":   "#/components/schemas/EvidenceDTO",
		"/api/missions/{mission_id}/qa":         "#/components/schemas/QADTO",
		"/api/missions/{mission_id}/discussion": "#/components/schemas/MissionControlDiscussionDTO",
	}
	for path, want := range wantRefs {
		pathItem := paths[path].(map[string]any)
		operation := pathItem["get"].(map[string]any)
		responses := operation["responses"].(map[string]any)
		response := responses["200"].(map[string]any)
		content := response["content"].(map[string]any)
		media := content["application/json"].(map[string]any)
		schema := media["schema"].(map[string]any)
		if got, _ := schema["$ref"].(string); got != want {
			t.Fatalf("OpenAPI %s response schema = %q, want %q", path, got, want)
		}
	}
	schemas := parsed["components"].(map[string]any)["schemas"].(map[string]any)
	discussion := schemas["MissionControlDiscussionDTO"].(map[string]any)
	properties := discussion["properties"].(map[string]any)
	if restricted, ok := properties["restricted"].(map[string]any); !ok || restricted["type"] != "boolean" {
		t.Fatalf("MissionControlDiscussionDTO must expose the restricted state: %+v", properties["restricted"])
	}
	control := schemas["MissionControlViewDTO"].(map[string]any)
	controlProperties := control["properties"].(map[string]any)
	controlDiscussion := controlProperties["discussion"].(map[string]any)
	if got, _ := controlDiscussion["$ref"].(string); got != "#/components/schemas/MissionControlDiscussionDTO" {
		t.Fatalf("MissionControlViewDTO discussion schema = %q", got)
	}
}

func TestOpenAPIContractDocumentsDevelopmentProjectAndSCMResources(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse openapi: %v", err)
	}
	paths := parsed["paths"].(map[string]any)
	for _, path := range []string{
		"/api/development-projects",
		"/api/development-projects/{development_project_id}",
		"/api/development-projects/{development_project_id}/archive",
		"/api/development-projects/{development_project_id}/members",
		"/api/development-projects/{development_project_id}/members/{member_id}",
		"/api/development-projects/{development_project_id}/repositories",
		"/api/development-projects/{development_project_id}/repositories/{binding_id}",
		"/api/development-projects/{development_project_id}/repositories/{binding_id}/verify",
		"/api/scm-connections",
		"/api/scm-connections/{connection_id}",
		"/api/scm-connections/{connection_id}/verify",
		"/api/scm-connections/{connection_id}/rotate",
		"/api/scm-connections/{connection_id}/revoke",
		"/api/scm-connections/{connection_id}/repositories",
	} {
		if _, ok := paths[path].(map[string]any); !ok {
			t.Fatalf("OpenAPI missing development resource route %s", path)
		}
	}
	components := parsed["components"].(map[string]any)["schemas"].(map[string]any)
	connectionProperties := components["SCMConnectionDTO"].(map[string]any)["properties"].(map[string]any)
	if _, exposed := connectionProperties["secret_ref"]; exposed {
		t.Fatal("SCMConnectionDTO must not expose secret_ref")
	}
	createProperties := components["CreateSCMConnectionRequest"].(map[string]any)["properties"].(map[string]any)
	if _, accepted := createProperties["secret_ref"]; !accepted {
		t.Fatal("CreateSCMConnectionRequest must accept a secret reference")
	}
	projectProperties := components["DevelopmentProjectDTO"].(map[string]any)["properties"].(map[string]any)
	if _, ok := projectProperties["capabilities"]; !ok {
		t.Fatal("DevelopmentProjectDTO must expose server-computed capabilities")
	}
}

func TestOpenAPIContractDocumentsScheduledTasksAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"/api/scheduled-tasks:",
		"operationId: listScheduledTasks",
		"/api/scheduled-tasks/summary:",
		"operationId: getScheduledTaskStats",
		"ScheduledTaskStatsDTO:",
		"operationId: createScheduledTask",
		"/api/scheduled-tasks/{scheduled_task_id}:",
		"operationId: getScheduledTask",
		"operationId: updateScheduledTask",
		"/api/scheduled-tasks/{scheduled_task_id}/cancel:",
		"operationId: cancelScheduledTask",
		"/api/scheduled-tasks/{scheduled_task_id}/run-now:",
		"operationId: runScheduledTaskNow",
		"/api/scheduled-tasks/{scheduled_task_id}/runs:",
		"operationId: listScheduledTaskRuns",
		"/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/responsibility-evaluations:",
		"operationId: listScheduledRunResponsibilityEvaluations",
		"/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/artifacts/content:",
		"operationId: readScheduledRunArtifact",
		"/api/scheduled-runs:",
		"operationId: listScheduledRunInbox",
		"/api/scheduled-runs/{scheduled_run_id}/read:",
		"operationId: setScheduledRunReadState",
		"ScheduledTaskDTO:",
		"ScheduledTaskPageDTO:",
		"ScheduledTaskSummaryDTO:",
		"executor_agent_id:",
		"ScheduledRunDTO:",
		"ScheduledRunPageDTO:",
		"ScheduledRunInboxItemDTO:",
		"ScheduledRunInboxPageDTO:",
		"ScheduledRunReadStateDTO:",
		"SetScheduledRunReadStateRequest:",
		"ResponsibilityEvaluationItemDTO:",
		"ResponsibilityEvaluationItemPageDTO:",
		"CreateScheduledTaskRequest:",
		"UpdateScheduledTaskRequest:",
		"ScheduledTaskScheduleDTO:",
		"ScheduledTaskTargetDTO:",
		"RunScheduledTaskNowRequest:",
		"discussion_workflow",
		"agent_task",
		"skip_if_running",
		"allow_parallel",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing scheduled task token %s", token)
		}
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse openapi yaml: %v", err)
	}
	components := parsed["components"].(map[string]any)["schemas"].(map[string]any)
	paths := parsed["paths"].(map[string]any)
	inboxPath := paths["/api/scheduled-runs"].(map[string]any)
	inboxGet := inboxPath["get"].(map[string]any)
	inboxResponses := inboxGet["responses"].(map[string]any)
	inboxOK := inboxResponses["200"].(map[string]any)
	inboxContent := inboxOK["content"].(map[string]any)
	inboxJSON := inboxContent["application/json"].(map[string]any)
	inboxSchema := inboxJSON["schema"].(map[string]any)
	if got := inboxSchema["$ref"]; got != "#/components/schemas/ScheduledRunInboxPageDTO" {
		t.Fatalf("scheduled run inbox response schema = %v", got)
	}
	for _, name := range []string{"ScheduledRunInboxItemDTO", "ScheduledRunInboxPageDTO", "ScheduledRunReadStateDTO", "SetScheduledRunReadStateRequest"} {
		if components[name] == nil {
			t.Fatalf("OpenAPI missing scheduled run inbox schema %s", name)
		}
	}
	evaluationPath := paths["/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/responsibility-evaluations"].(map[string]any)
	evaluationGet := evaluationPath["get"].(map[string]any)
	evaluationResponses := evaluationGet["responses"].(map[string]any)
	evaluationOK := evaluationResponses["200"].(map[string]any)
	evaluationContent := evaluationOK["content"].(map[string]any)
	evaluationJSON := evaluationContent["application/json"].(map[string]any)
	evaluationSchema := evaluationJSON["schema"].(map[string]any)
	if got := evaluationSchema["$ref"]; got != "#/components/schemas/ResponsibilityEvaluationItemPageDTO" {
		t.Fatalf("responsibility evaluation item page response schema = %v", got)
	}
	for _, name := range []string{"ResponsibilityEvaluationItemDTO", "ResponsibilityEvaluationItemPageDTO"} {
		if components[name] == nil {
			t.Fatalf("OpenAPI missing scheduled responsibility evaluation schema %s", name)
		}
	}
	target := components["ScheduledTaskTargetDTO"].(map[string]any)
	targetProps := target["properties"].(map[string]any)
	for _, removed := range []string{"mission_id", "task_id", "runtime_kind"} {
		if _, ok := targetProps[removed]; ok {
			t.Fatalf("ScheduledTaskTargetDTO must not expose %s", removed)
		}
	}
	mode := targetProps["mode"].(map[string]any)
	if !yamlEnumContains(mode, "tool_call") {
		t.Fatalf("ScheduledTaskTargetDTO mode must include tool_call: %+v", mode)
	}
	if _, ok := targetProps["approval_assignee_id"]; !ok {
		t.Fatal("ScheduledTaskTargetDTO must expose approval_assignee_id")
	}
	run := components["ScheduledRunDTO"].(map[string]any)
	runProps := run["properties"].(map[string]any)
	for _, required := range []string{"mission_id", "task_id", "responsibility_id", "delivery_status"} {
		if _, ok := runProps[required]; !ok {
			t.Fatalf("ScheduledRunDTO must expose %s", required)
		}
	}
	for _, removed := range []string{"runtime_kind"} {
		if _, ok := runProps[removed]; ok {
			t.Fatalf("ScheduledRunDTO must not expose %s", removed)
		}
	}
	runTargetMode := runProps["target_mode"].(map[string]any)
	if !yamlEnumContains(runTargetMode, "tool_call") {
		t.Fatalf("ScheduledRunDTO target_mode must include tool_call: %+v", runTargetMode)
	}
}

func TestOpenAPIContractDocumentsResponsibilityOperations(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse openapi yaml: %v", err)
	}
	paths := parsed["paths"].(map[string]any)
	for path, method := range map[string]string{
		"/api/responsibilities":                               "get",
		"/api/responsibilities/{responsibility_id}":           "get",
		"/api/responsibilities/{responsibility_id}/pause":     "post",
		"/api/responsibilities/{responsibility_id}/resume":    "post",
		"/api/responsibilities/{responsibility_id}/retry":     "post",
		"/api/responsibilities/{responsibility_id}/terminate": "post",
		"/api/responsibilities/{responsibility_id}/reassign":  "post",
		"/api/responsibility-policies/{agent_id}":             "put",
		"/api/responsibility-blockers/{blocker_id}/resolve":   "post",
		"/api/responsibility-signals":                         "post",
	} {
		item, ok := paths[path].(map[string]any)
		if !ok || item[method] == nil {
			t.Fatalf("OpenAPI missing %s %s", strings.ToUpper(method), path)
		}
	}
	schemas := parsed["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{
		"ResponsibilityInboxPageDTO", "ResponsibilityInboxItemDTO", "ResponsibilityDetailDTO",
		"ResponsibilityPolicyDTO", "ResponsibilityPolicyUpdateRequest", "ResponsibilityBlockerDTO",
		"ResponsibilitySupervisionAlertDTO", "ResponsibilityAttemptDTO", "ResponsibilityActivationDTO", "ResponsibilityAuditDTO",
	} {
		if schemas[name] == nil {
			t.Fatalf("OpenAPI missing responsibility schema %s", name)
		}
	}
	taskProps := schemas["TaskCardDTO"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"source_kind", "source_id", "source_step_id", "execution_permission_scopes"} {
		if taskProps[field] == nil {
			t.Fatalf("TaskCardDTO must expose %s", field)
		}
	}
	itemProps := schemas["ResponsibilityInboxItemDTO"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"source_kind", "source_id", "source_step_id", "open_alerts"} {
		if itemProps[field] == nil {
			t.Fatalf("ResponsibilityInboxItemDTO must expose %s", field)
		}
	}
}

func TestOpenAPIContractDocumentsPermissionPolicyManagement(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"/api/permissions/capabilities:",
		"operationId: listPermissionCapabilities",
		"/api/permissions/policy:",
		"operationId: getPermissionPolicy",
		"operationId: updatePermissionPolicy",
		"/api/permissions/decisions:",
		"operationId: decidePermission",
		"PermissionDecisionRequestDTO:",
		"PermissionDecisionDTO:",
		"PermissionDecisionSourceDTO:",
		"operationId: getAuthorizationRelationBackfillDiagnostics",
		"operationId: getAuthorizationEnforceTransitionDiagnostics",
		"AuthorizationRelationBackfillDiagnosticsDTO:",
		"AuthorizationRelationBackfillDiffDTO:",
		"AuthorizationEnforceTransitionDiagnosticsDTO:",
		"AuthorizationShadowDiagnosticsDTO:",
		"AuthorizationShadowCounterDTO:",
		"AuthorizationRelationTupleDTO:",
		"ApprovalRequirementDTO:",
		"PermissionCapabilityDTO:",
		"PermissionPolicyViewDTO:",
		"PermissionPolicyDTO:",
		"PermissionRoleDTO:",
		"PermissionRoleAssignmentDTO:",
		"PermissionGrantDTO:",
		"PermissionEffectiveGrantDTO:",
		"label_zh:",
		"label_en:",
		"access_mode:",
		"capability_id:",
		"enforcement_mode:",
		"policy_mutable:",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing permission policy token %s", token)
		}
	}
}

func TestOpenAPIContractDocumentsCurrentAuthSession(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	for _, token := range []string{
		"/api/auth/session:",
		"operationId: getCurrentAuthSession",
		"AuthSessionDTO:",
		"AuthSessionUserDTO:",
	} {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing auth session token %s", token)
		}
	}
}

func TestOpenAPIContractDocumentsOAuth2ConsentAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"/api/oauth2/login:",
		"operationId: acceptOAuth2Login",
		"/api/oauth2/consent:",
		"operationId: acceptOAuth2Consent",
		"/api/oauth2/consent/pending:",
		"operationId: getOAuth2ConsentPending",
		"/api/oauth2/consent/accept:",
		"operationId: acceptOAuth2ExplicitConsent",
		"/api/oauth2/consent/reject:",
		"operationId: rejectOAuth2ExplicitConsent",
		"/api/oauth2/clients:",
		"operationId: listOIDCClients",
		"operationId: saveOIDCClient",
		"OAuth2ConsentPreviewDTO:",
		"OAuth2ConsentDecisionRequestDTO:",
		"OAuth2RedirectDTO:",
		"OIDCClientDTO:",
		"redirect_uris:",
		"grant_types:",
		"token_endpoint_auth_method:",
		"client_secret:",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing OAuth2 token %s", token)
		}
	}
}

func TestOpenAPIContractDocumentsToolLifecycleCommands(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	toolsPath := schemaBlock(doc, "  /api/resources/tools:", "  /api/resources/skills:")
	for _, required := range []string{
		"operationId: registerTool",
		"operationId: callTool",
		"ToolCallRequest",
		"ToolCallResultDTO",
		"ToolDefinitionRequest",
		"post:",
	} {
		if !strings.Contains(toolsPath, required) {
			t.Fatalf("OpenAPI tool collection contract missing %s", required)
		}
	}
	for _, required := range []string{
		"operationId: listTools",
		"/api/resources/tools/{tool_id}:",
		"operationId: updateTool",
		"operationId: deleteTool",
		"/api/resources/tools/{tool_id}/disable:",
		"operationId: disableTool",
		"/api/resources/mcp-servers/{tool_id}/disable:",
		"operationId: disableMCPServer",
		"operationId: deleteMCPServer",
		"operationId: deleteSkill",
		"input_schema:",
		"output_schema:",
	} {
		if !strings.Contains(doc, required) {
			t.Fatalf("OpenAPI contract missing system-tool/MCP boundary token %s", required)
		}
	}
}

func TestOpenAPIContractDocumentsSkillLifecycleCommands(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	skillsPath := schemaBlock(doc, "  /api/resources/skills:", "  /api/resources/skills/{skill_id}:")
	for _, required := range []string{
		"operationId: listSkills",
		"ResourceAdminViewDTO",
	} {
		if !strings.Contains(skillsPath, required) {
			t.Fatalf("OpenAPI skill collection contract missing %s", required)
		}
	}
	for _, required := range []string{
		"operationId: getSkillDetail",
		"operationId: updateSkillMarkdown",
		"operationId: getSkillFile",
		"operationId: uploadSkillFile",
		"operationId: updateSkillFile",
		"operationId: downloadSkillFile",
		"operationId: deleteSkill",
		"operationId: disableSkill",
		"operationId: importSkillPackage",
		"operationId: exportSkillPackage",
		"operationId: registerSkillVersion",
		"operationId: promoteSkillVersion",
		"operationId: rollbackSkillVersion",
		"SkillVersionCandidateRequest",
		"PromoteSkillVersionRequest",
		"RollbackSkillVersionRequest",
		"SkillVersionDTO",
		"SaveSkillMarkdownRequest",
		"SkillDetailDTO",
		"SkillFileDTO",
	} {
		if !strings.Contains(doc, required) {
			t.Fatalf("OpenAPI contract missing skill lifecycle token %s", required)
		}
	}
}

func TestOpenAPIContractDocumentsSkillBundleManagement(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	for _, required := range []string{
		"/api/resources/skill-bundles:",
		"operationId: listSkillBundles",
		"operationId: createSkillBundle",
		"/api/resources/skill-bundles/{bundle_id}:",
		"operationId: deleteSkillBundle",
		"delete_skills",
		"/api/resources/skill-bundles/{bundle_id}/skills:",
		"operationId: addSkillsToBundle",
		"/api/resources/skill-bundles/{bundle_id}/skills/import:",
		"operationId: importSkillIntoBundle",
		"/api/resources/skill-bundles/{bundle_id}/skills/{skill_id}:",
		"operationId: removeSkillFromBundle",
		"SkillBundleListDTO:",
		"SkillBundleDTO:",
		"CreateSkillBundleRequest:",
		"AddSkillsToBundleRequest:",
	} {
		if !strings.Contains(doc, required) {
			t.Fatalf("OpenAPI contract missing skill bundle token %s", required)
		}
	}
}

func TestOpenAPIContractDocumentsResourceMCPDiscovery(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"/api/resources/mcp-servers/discover:",
		"operationId: discoverMCPServers",
		"DiscoverMCPResponseDTO:",
		"DiscoveredMCPServerDTO:",
		"discovered_id:",
		"package_refs:",
		"env_secret_refs:",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing MCP discovery token %s", token)
		}
	}
}

func TestOpenAPIContractDocumentsDiscussionAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"operationId: listDiscussions",
		"operationId: sendDiscussionMessage",
		"operationId: startDiscussionAttachmentDraft",
		"operationId: getDiscussion",
		"operationId: sendDiscussionMessageToExisting",
		"operationId: getLatestDiscussionDebug",
		"operationId: getDiscussionRunDebug",
		"DiscussionDTO:",
		"DiscussionPageDTO:",
		"DiscussionMessageRequest:",
		"DiscussionAttachmentDTO:",
		"DiscussionAttachmentDraftDTO:",
		"DiscussionRunDTO:",
		"DiscussionRunDebugDTO:",
		"DiscussionRunDebugSelectedDTO:",
		"DiscussionToolCallDTO:",
		"DiscussionLinkedMissionDTO:",
		"DiscussionStreamEventDTO:",
		"discussion send stream",
		"discussion_id:",
		"turn_id:",
		"revision:",
		"workspace_root_dir:",
		"linked_missions:",
		"runtime_kind:",
		"thinking_summary:",
		"outbound_prompt:",
		"runtime_response:",
		"tool_calls:",
		"text/event-stream:",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing discussion token %s", token)
		}
	}
	for _, forbidden := range []string{
		"/api/pm/conversations",
		"/api/discussions/{discussion_id}/stream",
		"operationId: streamDiscussion",
		"PMConversation",
	} {
		if strings.Contains(doc, forbidden) {
			t.Fatalf("OpenAPI contract must not expose legacy discussion token %s", forbidden)
		}
	}
	discussionSchemas := doc
	if start := strings.Index(doc, "    DiscussionPageDTO:"); start >= 0 {
		discussionSchemas = doc[start:]
		if end := strings.Index(discussionSchemas, "\n    GatewaySettingsDTO:"); end >= 0 {
			discussionSchemas = discussionSchemas[:end]
		}
	}
	if strings.Contains(discussionSchemas, "conversation_id:") {
		t.Fatalf("OpenAPI discussion contract must use discussion_id consistently")
	}
	debugSchema := doc
	if start := strings.Index(doc, "    DiscussionRunDebugDTO:"); start >= 0 {
		debugSchema = doc[start:]
		if end := strings.Index(debugSchema[len("    DiscussionRunDebugDTO:"):], "\n    DiscussionEventDTO:"); end >= 0 {
			debugSchema = debugSchema[:len("    DiscussionRunDebugDTO:")+end]
		}
	}
	for _, forbidden := range []string{"system_prompt:", "runtime_prompt:", "tool_contract:"} {
		if strings.Contains(debugSchema, forbidden) {
			t.Fatalf("DiscussionRunDebugDTO must not expose legacy token %s", forbidden)
		}
	}
}

func TestOpenAPIContractDocumentsKnowledgeAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"/api/knowledge-bases:",
		"operationId: listKnowledgeBases",
		"operationId: createKnowledgeBase",
		"/api/knowledge-bases/{knowledge_base_id}:",
		"operationId: getKnowledgeBase",
		"operationId: updateKnowledgeBase",
		"operationId: deleteKnowledgeBase",
		"/api/knowledge-bases/{knowledge_base_id}/bindings:",
		"operationId: listKnowledgeBindings",
		"operationId: bindKnowledgeBase",
		"/api/knowledge-bases/{knowledge_base_id}/bindings/{binding_id}:",
		"operationId: unbindKnowledgeBase",
		"/api/knowledge-bases/{knowledge_base_id}/documents:",
		"operationId: listKnowledgeDocuments",
		"operationId: createKnowledgeDocument",
		"/api/knowledge-bases/{knowledge_base_id}/documents/sync:",
		"operationId: syncKnowledgeDocuments",
		"/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}:",
		"operationId: getKnowledgeDocument",
		"operationId: deleteKnowledgeDocument",
		"/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/status:",
		"operationId: refreshKnowledgeDocumentStatus",
		"/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/reprocess:",
		"operationId: reprocessKnowledgeDocument",
		"/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/result:",
		"operationId: getKnowledgeDocumentProcessingResult",
		"/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/relations:",
		"operationId: listKnowledgeDocumentRelations",
		"/api/knowledge-bases/{knowledge_base_id}/jobs:",
		"operationId: listKnowledgeJobs",
		"/api/knowledge-jobs/{job_id}:",
		"operationId: getKnowledgeJob",
		"/api/knowledge-bases/{knowledge_base_id}/search:",
		"operationId: searchKnowledgeBase",
		"/api/knowledge/search:",
		"operationId: searchKnowledge",
		"/api/knowledge-bases/{knowledge_base_id}/reindex:",
		"operationId: reindexKnowledgeBase",
		"KnowledgeBaseDTO:",
		"KnowledgeBasePageDTO:",
		"KnowledgeBindingDTO:",
		"KnowledgeBindingPageDTO:",
		"KnowledgeDocumentDTO:",
		"KnowledgeDocumentPageDTO:",
		"CatalogReconcileResultDTO:",
		"KnowledgeDocumentRelationPageDTO:",
		"KnowledgeDocumentRelationDTO:",
		"KnowledgeDocumentProcessingResultDTO:",
		"DocumentProcessingJobDTO:",
		"KnowledgeJobDTO:",
		"KnowledgeJobPageDTO:",
		"KnowledgeSearchPageDTO:",
		"KnowledgeSearchResultDTO:",
		"CreateKnowledgeBaseCommand:",
		"UpdateKnowledgeBaseCommand:",
		"BindKnowledgeBaseCommand:",
		"CreateKnowledgeDocumentCommand:",
		"SearchKnowledgeCommand:",
		"knowledge_base_ids:",
		"owner_filter:",
		"OwnerFilter:",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing knowledge token %s", token)
		}
	}
	if strings.Contains(doc, "vector_store_path:") {
		t.Fatalf("OpenAPI contract must not expose internal LanceDB vector_store_path")
	}
}

func TestOpenAPIContractDocumentsSettingsPreferenceAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"/api/settings:",
		"SettingsDTO:",
		"operationId: discoverRuntimeCandidates",
		"operationId: testPageIndexConnection",
		"RuntimeDiscoveryResponseDTO:",
		"RuntimeDiscoveryCandidateDTO:",
		"SettingsPageIndexConnectionTestRequestDTO:",
		"SettingsPageIndexConnectionTestResultDTO:",
		"platform_name:",
		"organization_name:",
		"auto_create_agent_profile:",
		"auto_link_evidence:",
		"hil_default_enabled:",
		"discussion_agent_pair_loop_limit:",
		"runtime_profiles:",
		"SettingsRuntimeProfileDTO:",
		"llm_profiles:",
		"SettingsLLMProfileDTO:",
		"SettingsRuntimeApprovalPolicyDTO:",
		"RuntimeApprovalPolicy:",
		"RuntimeApprovalsReviewer:",
		"default_approval_policy:",
		"default_approvals_reviewer:",
		"approval_policy:",
		"approvals_reviewer:",
		"profile_id:",
		"display_name:",
		"provider:",
		"base_url:",
		"api_key_ref:",
		"temperature:",
		"context_length:",
		"config_ref:",
		"source:",
		"notes:",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing settings token %s", token)
		}
	}
	for _, forbidden := range []string{
		"/api/settings/integrations/{integration_id}/test:",
		"SettingsIntegrationDTO:",
		"SettingsIntegrationTestResultDTO:",
		"operationId: testSettingsIntegration",
		"integration_id:",
		"connection_state:",
	} {
		if strings.Contains(doc, forbidden) {
			t.Fatalf("OpenAPI contract must not expose settings integration placeholder %s", forbidden)
		}
	}
	settingsSchema := doc[strings.Index(doc, "    SettingsDTO:"):strings.Index(doc, "    SettingsRuntimeProfileDTO:")]
	if !strings.Contains(settingsSchema, "runtime_profiles:") {
		t.Fatalf("OpenAPI SettingsDTO must expose runtime_profiles")
	}
	for _, forbidden := range []string{
		"runtime_kind:",
		"runtime_config_ref:",
		"runtime_config:",
		"pm_conversation_agent_pair_loop_limit:",
		"pm_conversation_mention_max_responses:",
	} {
		if strings.Contains(settingsSchema, forbidden) {
			t.Fatalf("OpenAPI SettingsDTO must not expose runtime field %s", forbidden)
		}
	}
}

func TestOpenAPIContractDocumentsResourceRelationshipDetails(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"ResourceAdminViewDTO:",
		"ResourceUsageDTO:",
		"ResourceBindingDTO:",
		"usage:",
		"bindings:",
		"used_by_agent_ids:",
		"required_by_skill_ids:",
		"binding_type:",
		"bound_to_type:",
		"bound_to_id:",
		"name: resource_id",
		"name: bound_agent_id",
		"name: required_tool_id",
		"BindResourceRequest:",
		"UnbindResourceRequest:",
		"enum: [tool, mcp_server, skill]",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing resource relationship token %s", token)
		}
	}
}

func TestOpenAPIContractDocumentsBackendGeneratedMCPResourceCreateIDs(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"operationId: registerMCPServer",
		"$ref: \"#/components/schemas/CreateMCPServerRequest\"",
		"description: Path update id; ignored on create because backend generates opaque ids.",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing resource create token %s", token)
		}
	}
	mcpCreateSchema := schemaBlock(doc, "CreateMCPServerRequest:", "RegisterMCPServerRequest:")
	if strings.Contains(mcpCreateSchema, "\n        tool_id:") {
		t.Fatalf("CreateMCPServerRequest must not accept frontend-generated tool_id")
	}
	for _, token := range []string{
		"/api/resources/mcp-servers/discovered:",
		"/api/resources/mcp-servers/import:",
		"operationId: registerDiscoveredMCPServer",
		"operationId: importMCPConfig",
		"RegisterDiscoveredMCPRequest:",
		"ImportMCPConfigRequest:",
		"ImportMCPConfigResponseDTO:",
	} {
		if strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract must not expose bypass MCP registration token %s", token)
		}
	}
}

func TestOpenAPIContractDocumentsMemoryProfileResources(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"/api/resources/memory-profiles:",
		"/api/resources/memory-profiles/{memory_profile_id}:",
		"/api/resources/memory-profiles/personal:",
		"/api/resources/memory-profiles/digest:",
		"operationId: listMemoryProfiles",
		"operationId: getMemoryProfile",
		"operationId: getPersonalMemoryProfileByAgent",
		"operationId: buildMemoryDigest",
		"memory_profiles:",
		"MemoryProfileDTO:",
		"MemoryProfileRefRequestDTO:",
		"BuildMemoryDigestRequest:",
		"MemoryDigestDTO:",
		"MemoryScopeDTO:",
		"MemoryRetrievalPolicyDTO:",
		"MemoryCompressionPolicyDTO:",
		"MemoryVisibilityPolicyDTO:",
		"memory_profile_id:",
		"owner_agent_id:",
		"source_memory_refs:",
		"source_evidence_refs:",
		"memory_kind:",
		"default_scope:",
		"record_count:",
		"candidate_count:",
		"enum: [tool, mcp_server, skill, memory_profile]",
		"enum: [agent_tool_ref, agent_skill_ref, skill_required_tool, agent_memory_profile_ref]",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing memory profile resource token %s", token)
		}
	}
}

func TestOpenAPIContractDocumentsMemoryAdminAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"/api/memory/candidates:",
		"operationId: listMemoryCandidates",
		"operationId: createMemoryCandidate",
		"/api/memory/candidates/{candidate_id}/accept:",
		"operationId: acceptMemoryCandidate",
		"/api/memory/candidates/{candidate_id}/reject:",
		"operationId: rejectMemoryCandidate",
		"/api/memory/candidates/{candidate_id}/merge:",
		"operationId: mergeMemoryCandidate",
		"/api/memory/distill/experience:",
		"operationId: distillExperienceMemory",
		"/api/memory/distill/world:",
		"operationId: distillWorldMemory",
		"/api/memory/records:",
		"operationId: listMemoryRecords",
		"/api/memory/records/touch:",
		"operationId: touchMemoryRecords",
		"/api/memory/records/sweep-retention:",
		"operationId: sweepMemoryRetention",
		"/api/memory/records/{memory_id}:",
		"operationId: getMemoryRecord",
		"/api/memory/records/{memory_id}/archive:",
		"operationId: archiveMemoryRecord",
		"/api/memory/records/{memory_id}/anonymize:",
		"operationId: anonymizeMemoryRecord",
		"/api/memory/records/{memory_id}/delete:",
		"operationId: deleteMemoryRecord",
		"MemoryCandidateDTO:",
		"MemoryCandidatePageDTO:",
		"MemoryRecordDTO:",
		"MemoryRecordPageDTO:",
		"CreateMemoryCandidateRequest:",
		"AcceptMemoryCandidateRequest:",
		"RejectMemoryCandidateRequest:",
		"MergeMemoryCandidateRequest:",
		"MemoryLifecycleRequest:",
		"TouchMemoryRecordsRequest:",
		"MemoryRetentionSweepRequest:",
		"MemoryRetentionSweepResultDTO:",
		"reference_count:",
		"last_referenced_at:",
		"DistillExperienceRequest:",
		"DistillExperienceResponse:",
		"ExperienceCandidateDTO:",
		"DistillWorldRequest:",
		"DistillWorldResponse:",
		"WorldCandidateDTO:",
		"memory.read",
		"memory.write",
		"memory.review",
		"memory.world.manage",
		"memory.distill",
		"memory.audit.read",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing memory admin token %s", token)
		}
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse openapi yaml: %v", err)
	}
	paths := parsed["paths"].(map[string]any)
	worldPost := paths["/api/memory/distill/world"].(map[string]any)["post"].(map[string]any)
	worldResponses := worldPost["responses"].(map[string]any)
	worldAccepted := worldResponses["202"].(map[string]any)
	worldSchema := worldAccepted["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if worldSchema["$ref"] != "#/components/schemas/DistillWorldResponse" {
		t.Fatalf("world distill 202 schema = %+v", worldSchema)
	}
	components := parsed["components"].(map[string]any)["schemas"].(map[string]any)
	targetMode := components["ScheduledTaskTargetDTO"].(map[string]any)["properties"].(map[string]any)["mode"].(map[string]any)
	if !yamlEnumContains(targetMode, "memory_experience_distillation") || !yamlEnumContains(targetMode, "memory_world_consolidation") || !yamlEnumContains(targetMode, "memory_lifecycle_sweep") || !yamlEnumContains(targetMode, "todo_automation_scan") {
		t.Fatalf("scheduled target mode schema = %+v", targetMode)
	}
	sweepPost := paths["/api/memory/records/sweep-retention"].(map[string]any)["post"].(map[string]any)
	sweepResponses := sweepPost["responses"].(map[string]any)
	sweepAccepted := sweepResponses["202"].(map[string]any)
	sweepSchema := sweepAccepted["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if sweepSchema["$ref"] != "#/components/schemas/MemoryRetentionSweepResultDTO" {
		t.Fatalf("memory retention sweep 202 schema = %+v", sweepSchema)
	}
}

func TestOpenAPIContractDocumentsOntologyGovernanceAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	for _, legacy := range []string{
		"/api/ontology/registry:", "/api/ontology/context-graph:", "/api/ontology/reasoning/", "/api/ontology/actions/types:", "/api/ontology/actions/runs:",
		"/api/ontology/metadata-proposals", "/api/ontology/model-runs", "/api/ontology/model-run-", "/api/projection-audits:", "/api/ontology-events:",
	} {
		if strings.Contains(doc, legacy) {
			t.Fatalf("OpenAPI still exposes retired ontology route %s", legacy)
		}
	}
	for _, required := range []string{
		"/api/ontology/scopes:", "/api/ontology/schema/definitions:", "/api/ontology/context/objects:", "/api/ontology/lineage/summary:",
		"/api/ontology/governance/proposals:", "/api/ontology/projections/audits:", "/api/ontology/projections/audits/{audit_id}/replays:",
		"operationId: listOntologyScopes", "operationId: listOntologySchemaDefinitions", "operationId: listOntologyContextObjects",
		"operationId: getOntologyLineageSummary", "operationId: listOntologyGovernanceProposals", "operationId: listOntologyProjectionAudits",
		"operationId: replayOntologyProjectionAudit", "operationId: acknowledgeOntologyProjectionAudit",
	} {
		if !strings.Contains(doc, required) {
			t.Fatalf("OpenAPI missing ontology workbench contract %s", required)
		}
	}
}

func TestOpenAPIContractDocumentsWorkforceCommandsAndEvaluationProjection(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"UpsertWorkforceMemberCommand:",
		"CreateWorkforceMemberCommand:",
		"SetWorkforceMemberEnabledCommand:",
		"ReleaseWorkforceMemberCommand:",
		"HireTalentFromMarketCommand:",
		"WorkforceBaseAgentDTO:",
		"agent_profile_id:",
		"placement_status:",
		"released_from_organization_id:",
		"released_from_department_id:",
		"released_from_position_id:",
		"released_at:",
		"released_reason:",
		"base_agent:",
		"inherits_base_agent:",
		"audit_required:",
		"system_prompt:",
		"skill_scope:",
		"tool_scope:",
		"mcp_scope:",
		"runtime_kind:",
		"personal_memory_id:",
		"persona:",
		"responsibility_boundary:",
		"EvaluationItemDTO:",
		"EvaluationPageDTO:",
		"operationId: streamEvaluations",
		"text/event-stream:",
		"ProgressGraphDTO:",
		"ProgressNodeDTO:",
		"details:",
		"quality_score:",
		"collaboration_score:",
		"operationId: listTalentMarketMembers",
		"operationId: releaseWorkforceMemberToMarket",
		"operationId: deleteTalentMarketMember",
		"operationId: deleteTalentMarketMembers",
		"operationId: hireTalentFromMarket",
		"release-to-market",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing operations command/evaluation token %s", token)
		}
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse openapi yaml: %v", err)
	}
	paths, ok := parsed["paths"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI contract missing paths map")
	}
	evaluationPath, ok := paths["/api/evaluations"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI contract missing parsed /api/evaluations path")
	}
	evaluationGet, ok := evaluationPath["get"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI contract missing parsed /api/evaluations get operation")
	}
	if _, ok := evaluationGet["responses"].(map[string]any); !ok {
		t.Fatalf("OpenAPI contract /api/evaluations get responses must be an operation-level map")
	}
	streamPath, ok := paths["/api/evaluations/stream"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI contract missing parsed /api/evaluations/stream path")
	}
	streamGet, ok := streamPath["get"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI contract missing parsed /api/evaluations/stream get operation")
	}
	streamResponses, ok := streamGet["responses"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI contract /api/evaluations/stream get responses must be an operation-level map")
	}
	okResponse, ok := streamResponses["200"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI contract /api/evaluations/stream missing 200 response")
	}
	content, ok := okResponse["content"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI contract /api/evaluations/stream missing response content map")
	}
	if _, ok := content["text/event-stream"].(map[string]any); !ok {
		t.Fatalf("OpenAPI contract /api/evaluations/stream must document text/event-stream content")
	}
}

func TestOpenAPIContractDocumentsBackendGeneratedCreateIDs(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"operationId: createWorkforceMember",
		"CreateWorkforceMemberCommand:",
		"operationId: createOrganization",
		"CreateOrganizationCommand:",
		"operationId: deleteOrganization",
		"default_department_name:",
		"default_position_name:",
		"operationId: createOrganizationUnit",
		"CreateOrganizationUnitCommand:",
		"operationId: createOrganizationPosition",
		"CreateOrganizationPositionCommand:",
		"operationId: deleteOrganizationPosition",
		"operationId: deleteOrganizationUnit",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing backend-generated create token %s", token)
		}
	}
	createMemberSchema := schemaBlock(doc, "CreateWorkforceMemberCommand:", "SetWorkforceMemberEnabledCommand:")
	for _, forbidden := range []string{"\n        agent_profile_id:", "\n        agent_id:", "\n        agent_instance_id:"} {
		if strings.Contains(createMemberSchema, forbidden) {
			t.Fatalf("CreateWorkforceMemberCommand must not accept frontend-generated %s", forbidden)
		}
	}
	createOrgSchema := schemaBlock(doc, "CreateOrganizationCommand:", "CreateOrganizationUnitCommand:")
	for _, forbidden := range []string{"\n        default_department_id:", "\n        default_position_id:"} {
		if strings.Contains(createOrgSchema, forbidden) {
			t.Fatalf("CreateOrganizationCommand must not accept frontend-generated %s", forbidden)
		}
	}
	createTaskSchema := schemaBlock(doc, "CreateTaskRequest:", "TaskBoardDTO:")
	if strings.Contains(createTaskSchema, "\n        task_id:") {
		t.Fatalf("CreateTaskRequest must not accept frontend-generated task_id")
	}
}

func schemaBlock(doc, start, end string) string {
	startIndex := strings.Index(doc, start)
	if startIndex < 0 {
		return ""
	}
	rest := doc[startIndex:]
	endIndex := strings.Index(rest, end)
	if endIndex < 0 {
		return rest
	}
	return rest[:endIndex]
}

func yamlEnumContains(schema map[string]any, value string) bool {
	values, ok := schema["enum"].([]any)
	if !ok {
		return false
	}
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func enumContains(values []any, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func parameterNames(parameters []any) map[string]bool {
	names := map[string]bool{}
	for _, item := range parameters {
		parameter, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := parameter["name"].(string)
		if name != "" {
			names[name] = true
		}
	}
	return names
}

func TestOpenAPIContractDocumentsBusinessApprovalTypes(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	doc := string(raw)
	required := []string{
		"ApprovalPageDTO:",
		"ApprovalItemDTO:",
		"ApprovalActionDTO:",
		"permission_request",
		"tool_access",
		"evidence_review",
		"task_completion",
		"evidence_refs:",
		"organization_code:",
		"organization_name:",
		"requested_by_actor:",
		"triggered_by:",
		"triggered_by_actor:",
		"decided_by:",
		"decided_by_actor:",
		"target_actor:",
		"policy_id:",
		"risk_level:",
		"decision_effect:",
		"expires_at:",
		"metadata:",
	}
	for _, token := range required {
		if !strings.Contains(doc, token) {
			t.Fatalf("OpenAPI contract missing approval token %s", token)
		}
	}
	approvalSchema := schemaBlock(doc, "ApprovalItemDTO:", "ApprovalPayloadDTO:")
	if strings.Contains(approvalSchema, "inbox_action") {
		t.Fatalf("ApprovalItemDTO must not document inbox notifications as approval center items")
	}
}
