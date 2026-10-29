package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/accesscontrol"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/memorydistill"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/userauth"
	toolstd "orrery_v2/internal/resource/tool/std"
)

const (
	permissionMissionRead        = "mission.read"
	permissionMissionWrite       = "mission.write"
	permissionProjectRead        = "project.read"
	permissionProjectCreate      = "project.create"
	permissionProjectWrite       = "project.write"
	permissionProjectManage      = "project.manage"
	permissionDevelopmentRead    = "development.read"
	permissionDevelopmentMonitor = "development.monitor"
	permissionDevelopmentCreate  = "development.create"
	permissionDevelopmentWrite   = "development.write"
	permissionDevelopmentManage  = "development.manage"
	permissionDevelopmentExecute = "development.execute"
	permissionDevelopmentSubmit  = "development.submit"
	permissionDevelopmentApprove = "development.approve"
	permissionDevelopmentMerge   = "development.merge"
	permissionConnectionRead     = "connection.read"
	permissionConnectionUse      = "connection.use"
	permissionConnectionManage   = "connection.manage"
	permissionApprovalsRead      = "approvals.read"
	permissionApprovalsDecide    = "approvals.decide"
	permissionApprovalsManage    = "approvals.manage"
	permissionSettingsRead       = "settings.read"
	permissionSettingsManage     = "settings.manage"
	permissionGatewaySend        = "gateway.delivery.send"
	permissionDiscussionRead     = "discussion.read"
	permissionDiscussionWrite    = "discussion.write"
	permissionSchedulerRead      = "scheduler.read"
	permissionSchedulerWrite     = "scheduler.write"
	permissionSchedulerRun       = "scheduler.run"

	maxAuthorizationBodyPeekBytes = 1 << 20
)

type PermissionCapabilitiesDTO struct {
	Capabilities []PermissionCapabilityDTO `json:"capabilities"`
}

type PermissionCapabilityDTO struct {
	CapabilityID string `json:"capability_id"`
	Domain       string `json:"domain"`
	Action       string `json:"action"`
	AccessMode   string `json:"access_mode"`
	Scope        string `json:"scope"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	LabelZh      string `json:"label_zh"`
	LabelEn      string `json:"label_en"`
	CategoryZh   string `json:"category_zh"`
	CategoryEn   string `json:"category_en"`
	Description  string `json:"description,omitempty"`
}

type PermissionCapabilityRegistry struct {
	items []PermissionCapabilityDTO
	seen  map[string]struct{}
}

func NewPermissionCapabilityRegistry() *PermissionCapabilityRegistry {
	return &PermissionCapabilityRegistry{seen: map[string]struct{}{}}
}

func (r *PermissionCapabilityRegistry) Register(method, path, domain, action, scope, description string) {
	if r == nil {
		return
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	domain = strings.TrimSpace(domain)
	action = strings.TrimSpace(action)
	scope = strings.TrimSpace(scope)
	key := method + " " + path + " " + scope + " " + domain + " " + action
	if _, ok := r.seen[key]; ok {
		panic("duplicate permission capability: " + key)
	}
	r.seen[key] = struct{}{}
	r.items = append(r.items, PermissionCapabilityDTO{
		CapabilityID: capabilityID(method, path, scope),
		Domain:       domain,
		Action:       action,
		AccessMode:   accessMode(method, action),
		Scope:        scope,
		Method:       method,
		Path:         path,
		LabelZh:      permissionLabelZh(domain, action, description),
		LabelEn:      firstNonEmpty(strings.TrimSpace(description), domain+" "+action),
		CategoryZh:   permissionCategoryZh(domain),
		CategoryEn:   permissionCategoryEn(domain),
		Description:  strings.TrimSpace(description),
	})
}

func (r *PermissionCapabilityRegistry) List() []PermissionCapabilityDTO {
	if r == nil {
		return nil
	}
	out := make([]PermissionCapabilityDTO, len(r.items))
	copy(out, r.items)
	return out
}

func defaultPermissionCapabilities() []PermissionCapabilityDTO {
	registry := NewPermissionCapabilityRegistry()
	registerMissionCapabilities(registry)
	registerProjectCapabilities(registry)
	registerDevelopmentProjectCapabilities(registry)
	registerSCMConnectionCapabilities(registry)
	registerOntologyReplayCapabilities(registry)
	registerApprovalCapabilities(registry)
	registerResourceCapabilities(registry)
	registerMemoryCapabilities(registry)
	registerOperationsCapabilities(registry)
	registerResponsibilityCapabilities(registry)
	registerAuthorizationCapabilities(registry)
	registerIdentityCapabilities(registry)
	registerSettingsCapabilities(registry)
	registerDiscussionCapabilities(registry)
	registerRealtimeCapabilities(registry)
	registerKnowledgeCapabilities(registry)
	registerWebsiteKnowledgeGovernanceCapabilities(registry)
	registerSchedulerCapabilities(registry)
	registerAgentTraceCapabilities(registry)
	registerTodoCapabilities(registry)
	return registry.List()
}

func registerAgentTraceCapabilities(registry *PermissionCapabilityRegistry) {
	for _, path := range []string{
		"/api/agent-runs/{run_id}/trace",
		"/api/agent-runs/{run_id}/trace/events",
		"/api/agent-runs/{run_id}/trace/manifest",
	} {
		registry.Register(http.MethodGet, path, "agent_trace", "read", operations.PermissionAgentTraceRead, "Read sanitized Agent runtime traces")
	}
	registry.Register(http.MethodPost, "/api/agent-runs/{run_id}/trace/legal-hold", "agent_trace", "manage", operations.PermissionAgentTraceManage, "Apply Agent trace legal hold")
	registry.Register(http.MethodDelete, "/api/agent-runs/{run_id}/trace/legal-hold", "agent_trace", "manage", operations.PermissionAgentTraceManage, "Release Agent trace legal hold")
}

func registerResponsibilityCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/responsibility-profiles/agents/{agent_profile_id}", "responsibility", "read", operations.PermissionOperationsRead, "Read People responsibility profile for an agent")
	registry.Register(http.MethodGet, "/api/responsibility-profiles/organizations/{organization_scope_id}", "responsibility", "read", operations.PermissionOperationsRead, "Read People responsibility profiles for an organization")
	registry.Register(http.MethodGet, "/api/responsibilities", "responsibility", "read", operations.PermissionOperationsRead, "Read responsibility inbox")
	registry.Register(http.MethodGet, "/api/responsibilities/{responsibility_id}", "responsibility", "read", operations.PermissionOperationsRead, "Read responsibility detail")
	registry.Register(http.MethodGet, "/api/responsibility-profiles/agents/{agent_profile_id}", "workforce", "read", operations.PermissionWorkforceRead, "Read workforce responsibility profile")
	registry.Register(http.MethodGet, "/api/responsibility-profiles/organizations/{organization_scope_id}", "organization", "read", operations.PermissionOrgRead, "Read organization responsibility profile")
	for _, action := range []string{"pause", "resume", "retry", "terminate", "reassign"} {
		registry.Register(http.MethodPost, "/api/responsibilities/{responsibility_id}/"+action, "responsibility", "manage", operations.PermissionOperationsManage, "Control responsibility")
	}
	registry.Register(http.MethodGet, "/api/responsibility-policies/{agent_id}", "responsibility", "read", operations.PermissionOperationsRead, "Read responsibility policy")
	registry.Register(http.MethodPut, "/api/responsibility-policies/{agent_id}", "responsibility", "manage", operations.PermissionOperationsManage, "Update responsibility policy")
	registry.Register(http.MethodPost, "/api/responsibility-blockers/{blocker_id}/resolve", "responsibility", "manage", operations.PermissionOperationsManage, "Resolve responsibility blocker")
	registry.Register(http.MethodPost, "/api/responsibility-signals", "responsibility", "manage", operations.PermissionOperationsManage, "Observe responsibility external signal")
}

func registerWebsiteKnowledgeGovernanceCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/website-knowledge/gaps", "knowledge", "read", "knowledge.website_gap.propose", "List sanitized website knowledge gaps")
	registry.Register(http.MethodGet, "/api/website-knowledge/faqs", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read the governed website FAQ catalog")
	registry.Register(http.MethodGet, "/api/website-knowledge/metrics", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read aggregated website FAQ metrics")
	registry.Register(http.MethodGet, "/api/website-knowledge/proposals", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List website knowledge proposals")
	registry.Register(http.MethodGet, "/api/website-knowledge/proposals/{proposal_id}", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read a website knowledge proposal")
	registry.Register(http.MethodGet, "/api/website-knowledge/proposals/{proposal_id}/receipts", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List website knowledge proposal receipts")
	registry.Register(http.MethodPost, "/api/website-knowledge/proposals", "knowledge", "create", "knowledge.website_gap.propose", "Create website knowledge proposals")
	registry.Register(http.MethodPost, "/api/website-knowledge/proposals/{proposal_id}/approve", "approval", "decide", permissionApprovalsDecide, "Approve an exact website knowledge revision")
	registry.Register(http.MethodPost, "/api/website-knowledge/proposals/{proposal_id}/publish", "knowledge", "publish", knowledgeadmin.PermissionKnowledgeWrite, "Publish an approved website knowledge revision")
	registry.Register(http.MethodPost, "/api/website-knowledge/rules", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeWrite, "Create a bounded website answer rule revision")
	registry.Register(http.MethodPost, "/api/website-knowledge/rules/{revision_id}/evaluate", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeWrite, "Evaluate and roll back a website answer rule revision")
}

func registerMissionCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/missions", "mission", "list", permissionMissionRead, "List missions")
	registry.Register(http.MethodPost, "/api/missions", "mission", "create", permissionMissionWrite, "Create missions")
	registry.Register(http.MethodDelete, "/api/missions/{mission_id}", "mission", "delete", permissionMissionWrite, "Delete missions")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/cancel", "mission", "write", permissionMissionWrite, "Cancel missions")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/control", "mission", "read", permissionMissionRead, "Read mission control view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/control", "mission", "monitor", permissionDevelopmentMonitor, "Monitor development Mission control view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/ops", "mission", "read", permissionMissionRead, "Read mission operations view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/ops", "mission", "monitor", permissionDevelopmentMonitor, "Monitor development Mission operations view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/team", "mission", "read", permissionMissionRead, "Read mission team view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/team", "mission", "monitor", permissionDevelopmentMonitor, "Monitor development Mission team view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/tasks", "mission", "read", permissionMissionRead, "Read mission task view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/tasks", "mission", "monitor", permissionDevelopmentMonitor, "Monitor development Mission task view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/progress", "mission", "read", permissionMissionRead, "Read mission progress view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/progress", "mission", "monitor", permissionDevelopmentMonitor, "Monitor development Mission progress view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/evidence", "mission", "read", permissionMissionRead, "Read mission evidence view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/evidence", "mission", "monitor", permissionDevelopmentMonitor, "Monitor development Mission evidence view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/qa", "mission", "read", permissionMissionRead, "Read mission QA view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/qa", "mission", "monitor", permissionDevelopmentMonitor, "Monitor development Mission QA view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/discussion", "discussion", "read", permissionDiscussionRead, "Read mission discussion view")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/stream", "mission", "read", permissionMissionRead, "Subscribe to mission stream")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/stream", "mission", "monitor", permissionDevelopmentMonitor, "Monitor development Mission stream")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/tasks", "mission", "write", permissionMissionWrite, "Create mission tasks")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/tasks/{task_id}/assign", "mission", "write", permissionMissionWrite, "Assign mission tasks")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/tasks/{task_id}/agent-runs", "mission", "write", permissionMissionWrite, "Start agent runtime runs")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/tasks/{task_id}/agent-runs", "agent", "run", operations.PermissionAgentRun, "Start agent runtime runs")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/agent-runs/{run_id}/cancel", "mission", "write", permissionMissionWrite, "Cancel agent runtime runs")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/agent-runs/{run_id}/retry", "mission", "write", permissionMissionWrite, "Retry agent runtime runs")
	registry.Register(http.MethodGet, "/api/missions/{mission_id}/agent-runs/{run_id}/recovery", "mission", "read", operations.PermissionOperationsRead, "Read AgentCore task recovery state")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/agent-runs/{run_id}/recovery/reviews", "mission", "write", operations.PermissionOperationsManage, "Record evidence for an uncertain AgentCore tool batch")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/tasks/{task_id}/submit-result", "mission", "write", permissionMissionWrite, "Submit task result")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/qa/{qa_id}/decision", "mission", "write", permissionMissionWrite, "Submit QA decision")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/discussion/messages", "discussion", "write", permissionDiscussionWrite, "Post discussion message")
	registry.Register(http.MethodPost, "/api/missions/{mission_id}/discussion/messages/{message_id}/reply", "discussion", "write", permissionDiscussionWrite, "Reply to discussion message")
	registry.Register(http.MethodGet, "/api/inbox", "mission", "read", permissionMissionRead, "List inbox items")
	registry.Register(http.MethodPost, "/api/inbox/{item_id}/read", "mission", "write", permissionMissionWrite, "Mark inbox item read")
	registry.Register(http.MethodPost, "/api/inbox/{item_id}/close", "mission", "write", permissionMissionWrite, "Close inbox item")
}

func registerProjectCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/projects", "project", "read", permissionProjectRead, "List projects")
	registry.Register(http.MethodPost, "/api/projects", "project", "create", permissionProjectCreate, "Create projects")
	registry.Register(http.MethodGet, "/api/projects/{project_id}", "project", "read", permissionProjectRead, "Get project")
	registry.Register(http.MethodPatch, "/api/projects/{project_id}", "project", "update", permissionProjectWrite, "Update projects")
	registry.Register(http.MethodPost, "/api/projects/{project_id}/archive", "project", "archive", permissionProjectManage, "Archive projects")
}

func registerDevelopmentProjectCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/development-projects", "development_project", "read", permissionDevelopmentRead, "List development projects")
	registry.Register(http.MethodPost, "/api/development-projects", "development_project", "create", permissionDevelopmentCreate, "Create development projects")
	registry.Register(http.MethodGet, "/api/development-projects/{development_project_id}", "development_project", "read", permissionDevelopmentRead, "Read development project")
	registry.Register(http.MethodPatch, "/api/development-projects/{development_project_id}", "development_project", "write", permissionDevelopmentWrite, "Update development project")
	registry.Register(http.MethodPost, "/api/development-projects/{development_project_id}/archive", "development_project", "manage", permissionDevelopmentManage, "Archive development project")
	registry.Register(http.MethodGet, "/api/development-projects/{development_project_id}/members", "development_project", "read", permissionDevelopmentRead, "List development project members")
	registry.Register(http.MethodPost, "/api/development-projects/{development_project_id}/members", "development_project", "manage", permissionDevelopmentManage, "Add development project member")
	registry.Register(http.MethodPatch, "/api/development-projects/{development_project_id}/members/{member_id}", "development_project", "manage", permissionDevelopmentManage, "Update development project member")
	registry.Register(http.MethodDelete, "/api/development-projects/{development_project_id}/members/{member_id}", "development_project", "manage", permissionDevelopmentManage, "Remove development project member")
	registry.Register(http.MethodGet, "/api/development-projects/{development_project_id}/repositories", "development_project", "read", permissionDevelopmentRead, "List development project repositories")
	registry.Register(http.MethodGet, "/api/development-projects/{development_project_id}/executions", "development_project", "read", permissionDevelopmentRead, "List development project execution summaries")
	registry.Register(http.MethodGet, "/api/development-projects/{development_project_id}/workspaces", "development_project", "read", permissionDevelopmentRead, "List managed development workspaces")
	registry.Register(http.MethodGet, "/api/development-projects/{development_project_id}/runs", "development_project", "read", permissionDevelopmentRead, "List development execution runs")
	registry.Register(http.MethodPost, "/api/development-projects/{development_project_id}/repositories", "development_project", "manage", permissionDevelopmentManage, "Bind development project repository")
	registry.Register(http.MethodPatch, "/api/development-projects/{development_project_id}/repositories/{binding_id}", "development_project", "manage", permissionDevelopmentManage, "Update development project repository")
	registry.Register(http.MethodDelete, "/api/development-projects/{development_project_id}/repositories/{binding_id}", "development_project", "manage", permissionDevelopmentManage, "Remove development project repository")
	registry.Register(http.MethodPost, "/api/development-projects/{development_project_id}/repositories/{binding_id}/verify", "development_project", "manage", permissionDevelopmentManage, "Verify development project repository")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/commits", "development_project", "read", permissionDevelopmentRead, "List Todo commit records")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/execution-runs/{run_id}/commits", "development_project", "execute", permissionDevelopmentExecute, "Create a controlled Todo commit")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/code-submissions", "development_project", "read", permissionDevelopmentRead, "List Todo code submissions")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/code-submissions", "development_project", "submit", permissionDevelopmentSubmit, "Create a Todo code submission")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/code-submissions/{submission_id}", "development_project", "read", permissionDevelopmentRead, "Read a Todo code submission")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/code-submissions/{submission_id}/push", "development_project", "submit", permissionDevelopmentSubmit, "Push a Todo code submission")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/code-submissions/{submission_id}/pull-request", "development_project", "submit", permissionDevelopmentSubmit, "Create or update a Todo pull request")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/code-submissions/{submission_id}/request-changes", "development_project", "approve", permissionDevelopmentApprove, "Request Todo submission changes")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/code-submissions/{submission_id}/merge-preview", "development_project", "merge", permissionDevelopmentMerge, "Preview a Todo submission merge")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/code-submissions/{submission_id}/merge-approval", "development_project", "merge", permissionDevelopmentMerge, "Approve a Todo submission merge")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/code-submissions/{submission_id}/merge", "development_project", "merge", permissionDevelopmentMerge, "Merge a Todo code submission")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/code-submissions/{submission_id}/close", "development_project", "submit", permissionDevelopmentSubmit, "Close a Todo code submission")
}

func registerSCMConnectionCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/scm-connections", "scm_connection", "read", permissionConnectionRead, "List SCM connections")
	registry.Register(http.MethodPost, "/api/scm-connections", "scm_connection", "manage", permissionConnectionManage, "Create SCM connection")
	registry.Register(http.MethodGet, "/api/scm-connections/{connection_id}", "scm_connection", "read", permissionConnectionRead, "Read SCM connection")
	registry.Register(http.MethodPatch, "/api/scm-connections/{connection_id}", "scm_connection", "manage", permissionConnectionManage, "Update SCM connection")
	registry.Register(http.MethodPost, "/api/scm-connections/{connection_id}/verify", "scm_connection", "manage", permissionConnectionManage, "Verify SCM connection")
	registry.Register(http.MethodPost, "/api/scm-connections/{connection_id}/rotate", "scm_connection", "manage", permissionConnectionManage, "Rotate SCM connection secret")
	registry.Register(http.MethodPost, "/api/scm-connections/{connection_id}/revoke", "scm_connection", "manage", permissionConnectionManage, "Revoke SCM connection")
	registry.Register(http.MethodGet, "/api/scm-connections/{connection_id}/repositories", "scm_connection", "use", permissionConnectionUse, "List repositories available through SCM connection")
}

func registerOntologyReplayCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/ontology/scopes", "ontology_scope", "read", operations.PermissionOperationsRead, "List ontology scopes")
	registry.Register(http.MethodGet, "/api/ontology/scopes/resolve", "ontology_scope", "resolve", operations.PermissionOperationsRead, "Resolve ontology scope")
	registry.Register(http.MethodGet, "/api/ontology/schema/summary", "ontology_schema", "read", operations.PermissionOperationsRead, "Read ontology schema summary")
	registry.Register(http.MethodGet, "/api/ontology/schema/definitions", "ontology_schema", "read", operations.PermissionOperationsRead, "List ontology schema definitions")
	registry.Register(http.MethodGet, "/api/ontology/schema/definitions/{kind}/{definition_id}", "ontology_schema", "read", operations.PermissionOperationsRead, "Read ontology schema definition")
	registry.Register(http.MethodGet, "/api/ontology/schema/definitions/{kind}/{definition_id}/references", "ontology_schema", "read", operations.PermissionOperationsRead, "List ontology schema references")
	registry.Register(http.MethodGet, "/api/ontology/schema/graph", "ontology_schema", "read", operations.PermissionOperationsRead, "Read bounded ontology schema graph")
	registry.Register(http.MethodPost, "/api/ontology/inference/evaluate", "ontology_reasoning", "manage", "ontology.manage", "Evaluate rules and document claims into governed candidates")
	registry.Register(http.MethodPost, "/api/ontology/inference/context", "ontology_context", "read", "ontology.read", "Read current supported claims and validate explicit dependencies without creating candidates")
	registry.Register(http.MethodPost, "/api/ontology/inference/review", "ontology_reasoning", "manage", "ontology.manage", "Review a current inference candidate")
	registry.Register(http.MethodPost, "/api/ontology/inference/plan", "ontology_reasoning", "manage", "ontology.manage", "Preview an approved inference relationship")
	registry.Register(http.MethodPost, "/api/ontology/inference/apply", "ontology_reasoning", "manage", "ontology.manage", "Apply a confirmed inference relationship plan")
	registry.Register(http.MethodGet, "/api/ontology/inference/schema-export", "ontology_reasoning", "read", "ontology.read", "Export scoped OWL and SHACL schema")
	registry.Register(http.MethodGet, "/api/ontology/context/company-overview", "ontology_context", "read", operations.PermissionOperationsRead, "Read company ontology context overview")
	registry.Register(http.MethodGet, "/api/ontology/context/overview", "ontology_context", "read", operations.PermissionOperationsRead, "Read scoped ontology context overview")
	registry.Register(http.MethodGet, "/api/ontology/context/summary", "ontology_context", "read", operations.PermissionOperationsRead, "Read ontology context summary")
	registry.Register(http.MethodGet, "/api/ontology/context/objects", "ontology_context", "read", operations.PermissionOperationsRead, "List ontology context objects")
	registry.Register(http.MethodGet, "/api/ontology/context/objects/{object_id}", "ontology_context", "read", operations.PermissionOperationsRead, "Read ontology context object")
	registry.Register(http.MethodGet, "/api/ontology/context/objects/{object_id}/facts", "ontology_context", "read", operations.PermissionOperationsRead, "List ontology context object facts")
	registry.Register(http.MethodGet, "/api/ontology/context/objects/{object_id}/relations", "ontology_context", "read", operations.PermissionOperationsRead, "List ontology context object relations")
	registry.Register(http.MethodGet, "/api/ontology/context/objects/{object_id}/evidence", "ontology_context", "read", operations.PermissionOperationsRead, "List ontology context object evidence")
	registry.Register(http.MethodGet, "/api/ontology/context/neighborhood", "ontology_context", "read", operations.PermissionOperationsRead, "Read bounded ontology context neighborhood")
	registry.Register(http.MethodGet, "/api/ontology/executions/tasks", "ontology_execution", "read", operations.PermissionOperationsRead, "List ontology agent tasks")
	registry.Register(http.MethodGet, "/api/ontology/executions/tasks/{task_id}", "ontology_execution", "read", operations.PermissionOperationsRead, "Read ontology agent task")
	registry.Register(http.MethodGet, "/api/ontology/executions/runs/{run_id}", "ontology_execution", "read", operations.PermissionOperationsRead, "Read ontology agent run")
	registry.Register(http.MethodGet, "/api/ontology/executions/runs/{run_id}/lineage", "ontology_execution", "read", operations.PermissionOperationsRead, "Read ontology agent run lineage")
	registry.Register(http.MethodPost, "/api/ontology/actions/{action_type_id}/invoke", "ontology_action", "invoke", operations.PermissionOperationsManage, "Invoke governed ontology action")
	registry.Register(http.MethodGet, "/api/ontology/reliability/incidents/{incident_id}", "ontology_reliability", "read", operations.PermissionOperationsRead, "Read reliability incident control state")
	registry.Register(http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/diagnoses", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Propose an evidence-backed diagnosis")
	registry.Register(http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/diagnoses/{diagnosis_id}/decision", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Confirm or reject a diagnosis")
	registry.Register(http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/diagnoses/{diagnosis_id}/rules", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Propose a repair rule from a confirmed diagnosis")
	registry.Register(http.MethodPost, "/api/ontology/reliability/rules/{rule_id}/activation-reviews", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Request governed repair rule activation review")
	registry.Register(http.MethodPost, "/api/ontology/reliability/rules/{rule_id}/activation-reviews/{review_id}/decision", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Resolve governed repair rule activation review")
	registry.Register(http.MethodPost, "/api/ontology/reliability/rules/{rule_id}/activation-reviews/{review_id}/activate", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Activate a reviewed repair rule")
	registry.Register(http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/rules/{rule_id}/authorizations", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Request governed repair authorization")
	registry.Register(http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/rules/{rule_id}/authorizations/{decision_id}/decision", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Resolve governed repair authorization")
	registry.Register(http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/rules/{rule_id}/authorizations/{decision_id}/invoke", "ontology_reliability", "manage", operations.PermissionOperationsManage, "Invoke an authorized repair")
	registry.Register(http.MethodGet, "/api/ontology/problems", "ontology_problem", "read", operations.PermissionOperationsRead, "List company problem cases")
	registry.Register(http.MethodGet, "/api/ontology/problems/{problem_id}", "ontology_problem", "read", operations.PermissionOperationsRead, "Read a company problem case")
	registry.Register(http.MethodPost, "/api/ontology/problems/{problem_id}/route", "ontology_problem", "manage", operations.PermissionOperationsManage, "Route a company problem case to an owner")
	registry.Register(http.MethodGet, "/api/ontology/lineage/summary", "ontology_lineage", "read", operations.PermissionOperationsRead, "Read ontology lineage quality summary")
	registry.Register(http.MethodGet, "/api/ontology/lineage/facts/{fact_id}/provenance", "ontology_lineage", "read", operations.PermissionOperationsRead, "Read ontology fact provenance")
	registry.Register(http.MethodGet, "/api/ontology/lineage/facts/{fact_id}/assertions", "ontology_lineage", "read", operations.PermissionOperationsRead, "List ontology fact assertions")
	registry.Register(http.MethodGet, "/api/ontology/lineage/facts/{fact_id}/evidence", "ontology_lineage", "read", operations.PermissionOperationsRead, "List ontology fact evidence")
	registry.Register(http.MethodGet, "/api/ontology/lineage/facts/{fact_id}/projection-trace", "ontology_lineage", "read", operations.PermissionOperationsRead, "List ontology fact projection trace")
	registry.Register(http.MethodGet, "/api/ontology/governance/summary", "ontology_governance", "read", operations.PermissionOperationsRead, "Read ontology governance summary")
	registry.Register(http.MethodGet, "/api/ontology/governance/proposals", "ontology_governance", "read", operations.PermissionOperationsRead, "List ontology governance proposals")
	registry.Register(http.MethodGet, "/api/ontology/governance/proposals/{proposal_id}", "ontology_governance", "read", operations.PermissionOperationsRead, "Read ontology governance proposal")
	registry.Register(http.MethodGet, "/api/ontology/governance/proposals/{proposal_id}/analysis", "ontology_governance", "read", operations.PermissionOperationsRead, "Read ontology governance proposal analysis")
	registry.Register(http.MethodGet, "/api/ontology/governance/proposals/{proposal_id}/receipts", "ontology_governance", "read", operations.PermissionOperationsRead, "List ontology governance proposal receipts")
	registry.Register(http.MethodGet, "/api/ontology/governance/proposals/{proposal_id}/audit-events", "ontology_governance", "read", operations.PermissionOperationsRead, "List ontology governance proposal audit events")
	registry.Register(http.MethodPost, "/api/ontology/governance/proposals/{proposal_id}/decisions", "ontology_governance", "manage", operations.PermissionOperationsManage, "Decide ontology governance proposal")
	registry.Register(http.MethodPost, "/api/ontology/governance/proposals/{proposal_id}/plans", "ontology_governance", "manage", operations.PermissionOperationsManage, "Create ontology governance promotion plan")
	registry.Register(http.MethodPost, "/api/ontology/governance/plans/{plan_id}/apply", "ontology_governance", "manage", operations.PermissionOperationsManage, "Apply ontology governance promotion plan")
	registry.Register(http.MethodPost, "/api/ontology/governance/receipts/{receipt_id}/projection-refresh-retries", "ontology_governance", "manage", operations.PermissionOperationsManage, "Retry an abandoned ontology mapping projection refresh")
	registry.Register(http.MethodGet, "/api/ontology/projections/summary", "ontology_projection", "read", operations.PermissionOperationsRead, "Read ontology projection summary")
	registry.Register(http.MethodGet, "/api/ontology/projections/audits", "ontology_projection", "read", operations.PermissionOperationsRead, "List ontology projection audits")
	registry.Register(http.MethodGet, "/api/ontology/projections/audits/{audit_id}", "ontology_projection", "read", operations.PermissionOperationsRead, "Read ontology projection audit")
	registry.Register(http.MethodGet, "/api/ontology/projections/audits/{audit_id}/replay-history", "ontology_projection", "read", operations.PermissionOperationsRead, "List ontology projection audit replay history")
	registry.Register(http.MethodPost, "/api/ontology/projections/audits/{audit_id}/replays", "ontology_projection", "replay", operations.PermissionOperationsManage, "Replay ontology projection audit")
	registry.Register(http.MethodPost, "/api/ontology/projections/audits/{audit_id}/acknowledgements", "ontology_projection", "acknowledge", operations.PermissionOperationsManage, "Acknowledge ontology projection audit")
}

func registerApprovalCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/approvals", "approval", "read", permissionApprovalsRead, "List approvals")
	registry.Register(http.MethodPost, "/api/approvals/{approval_id}/decision", "approval", "decide", permissionApprovalsDecide, "Decide approvals")
	registry.Register(http.MethodPost, "/api/hil/decisions", "hil", "decide", permissionApprovalsDecide, "Create and route HIL decisions")
	registry.Register(http.MethodGet, "/api/hil/decisions", "hil", "read", permissionApprovalsRead, "List HIL decisions")
	registry.Register(http.MethodGet, "/api/hil/decisions/{decision_id}", "hil", "read", permissionApprovalsRead, "Read HIL decisions")
	registry.Register(http.MethodPost, "/api/hil/decisions/{decision_id}/decision", "hil", "decide", permissionApprovalsDecide, "Resolve HIL decisions")
	registry.Register(http.MethodGet, "/api/hil/councils/{council_id}", "hil", "read", permissionApprovalsRead, "Read council sessions")
	registry.Register(http.MethodPost, "/api/hil/councils/{council_id}/contributions", "hil", "decide", permissionApprovalsDecide, "Submit council contributions")
	registry.Register(http.MethodPost, "/api/hil/councils/{council_id}/votes", "hil", "decide", permissionApprovalsDecide, "Cast council votes")
	registry.Register(http.MethodGet, "/api/hil/workflow-templates", "hil_workflow", "read", permissionApprovalsRead, "List approval workflow templates")
	registry.Register(http.MethodPost, "/api/hil/workflow-templates", "hil_workflow", "manage", permissionApprovalsManage, "Create approval workflow templates")
	registry.Register(http.MethodGet, "/api/hil/workflow-templates/{template_id}", "hil_workflow", "read", permissionApprovalsRead, "Read approval workflow templates")
	registry.Register(http.MethodPatch, "/api/hil/workflow-templates/{template_id}/draft", "hil_workflow", "manage", permissionApprovalsManage, "Update approval workflow template drafts")
	registry.Register(http.MethodPost, "/api/hil/workflow-templates/{template_id}/publish", "hil_workflow", "manage", permissionApprovalsManage, "Publish approval workflow templates")
	registry.Register(http.MethodPost, "/api/hil/workflow-templates/{template_id}/disable", "hil_workflow", "manage", permissionApprovalsManage, "Disable approval workflow templates")
	registry.Register(http.MethodPut, "/api/hil/workflow-template-bindings/{action_type_id}", "hil_workflow", "manage", permissionApprovalsManage, "Bind actions to approval workflow templates")
	registry.Register(http.MethodGet, "/api/hil/workflow-instances", "hil_workflow", "read", permissionApprovalsRead, "List visible approval workflow instances")
	registry.Register(http.MethodGet, "/api/hil/workflow-instances/{instance_id}", "hil_workflow", "read", permissionApprovalsRead, "Read approval workflow instances")
	registry.Register(http.MethodPost, "/api/hil/workflow-instances/{instance_id}/viewed", "hil_workflow", "read", permissionApprovalsRead, "Record first approval workflow material view")
	registry.Register(http.MethodPost, "/api/hil/workflow-instances/{instance_id}/resubmit", "hil_workflow", "decide", permissionApprovalsDecide, "Resubmit a revised approval workflow")
	registry.Register(http.MethodPost, "/api/hil/workflow-tasks/{task_id}/decision", "hil_workflow", "decide", permissionApprovalsDecide, "Decide assigned approval workflow tasks")
	registry.Register(http.MethodPost, "/api/hil/workflow-tasks/{task_id}/transfer", "hil_workflow", "decide", permissionApprovalsDecide, "Transfer assigned approval workflow tasks")
	registry.Register(http.MethodPost, "/api/hil/workflow-tasks/{task_id}/reassign", "hil_workflow", "manage", permissionApprovalsManage, "Administratively reassign approval workflow tasks")
}

func registerResourceCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/resources/tools", "resource", "read", resourceadmin.PermissionResourceRead, "List tools")
	registry.Register(http.MethodPost, "/api/resources/tools/call", "tool", "call", operations.PermissionToolCall, "Call tool manually")
	registry.Register(http.MethodPost, "/api/resources/tools", "resource", "manage", resourceadmin.PermissionResourceManage, "Register tool")
	registry.Register(http.MethodPatch, "/api/resources/tools/{tool_id}", "resource", "manage", resourceadmin.PermissionResourceManage, "Update tool")
	registry.Register(http.MethodDelete, "/api/resources/tools/{tool_id}", "resource", "manage", resourceadmin.PermissionResourceManage, "Delete tool")
	registry.Register(http.MethodPost, "/api/resources/tools/{tool_id}/disable", "resource", "manage", resourceadmin.PermissionResourceManage, "Disable tool")
	registry.Register(http.MethodGet, "/api/resources/skills", "resource", "read", resourceadmin.PermissionResourceRead, "List skills")
	registry.Register(http.MethodPost, "/api/resources/skills", "resource", "manage", resourceadmin.PermissionResourceManage, "Create skill")
	registry.Register(http.MethodPost, "/api/resources/skills/{skill_id}/versions", "resource", "manage", resourceadmin.PermissionResourceManage, "Register skill version candidate")
	registry.Register(http.MethodPost, "/api/resources/skills/{skill_id}/versions/{version}/promote", "resource", "manage", resourceadmin.PermissionResourceManage, "Promote skill version")
	registry.Register(http.MethodPost, "/api/resources/skills/{skill_id}/versions/{version}/rollback", "resource", "manage", resourceadmin.PermissionResourceManage, "Rollback skill version")
	registry.Register(http.MethodGet, "/api/resources/skills/{skill_id}", "resource", "read", resourceadmin.PermissionResourceRead, "Read skill detail")
	registry.Register(http.MethodDelete, "/api/resources/skills/{skill_id}", "resource", "manage", resourceadmin.PermissionResourceManage, "Delete skill")
	registry.Register(http.MethodPost, "/api/resources/skills/import", "resource", "manage", resourceadmin.PermissionResourceManage, "Import skill package")
	registry.Register(http.MethodPut, "/api/resources/skills/{skill_id}/skill-md", "resource", "manage", resourceadmin.PermissionResourceManage, "Update skill markdown")
	registry.Register(http.MethodPost, "/api/resources/skills/{skill_id}/folders", "resource", "manage", resourceadmin.PermissionResourceManage, "Create skill folder")
	registry.Register(http.MethodGet, "/api/resources/skills/{skill_id}/files", "resource", "read", resourceadmin.PermissionResourceRead, "Read skill file")
	registry.Register(http.MethodGet, "/api/resources/skills/{skill_id}/files/download", "resource", "read", resourceadmin.PermissionResourceRead, "Download skill file")
	registry.Register(http.MethodPost, "/api/resources/skills/{skill_id}/files", "resource", "manage", resourceadmin.PermissionResourceManage, "Upload skill file")
	registry.Register(http.MethodPut, "/api/resources/skills/{skill_id}/files", "resource", "manage", resourceadmin.PermissionResourceManage, "Update skill file")
	registry.Register(http.MethodDelete, "/api/resources/skills/{skill_id}/files", "resource", "manage", resourceadmin.PermissionResourceManage, "Delete skill file")
	registry.Register(http.MethodPost, "/api/resources/skills/{skill_id}/disable", "resource", "manage", resourceadmin.PermissionResourceManage, "Disable skill")
	registry.Register(http.MethodGet, "/api/resources/skills/{skill_id}/export", "resource", "read", resourceadmin.PermissionResourceRead, "Export skill package")
	registry.Register(http.MethodGet, "/api/resources/skill-bundles", "resource", "read", resourceadmin.PermissionResourceRead, "List skill bundles")
	registry.Register(http.MethodPost, "/api/resources/skill-bundles", "resource", "manage", resourceadmin.PermissionResourceManage, "Create skill bundle")
	registry.Register(http.MethodPost, "/api/resources/skill-bundles/{bundle_id}/skills", "resource", "manage", resourceadmin.PermissionResourceManage, "Add skills to bundle")
	registry.Register(http.MethodPost, "/api/resources/skill-bundles/{bundle_id}/skills/import", "resource", "manage", resourceadmin.PermissionResourceManage, "Import skill into bundle")
	registry.Register(http.MethodDelete, "/api/resources/skill-bundles/{bundle_id}/skills/{skill_id}", "resource", "manage", resourceadmin.PermissionResourceManage, "Remove skill from bundle")
	registry.Register(http.MethodDelete, "/api/resources/skill-bundles/{bundle_id}", "resource", "manage", resourceadmin.PermissionResourceManage, "Delete skill bundle")
	registry.Register(http.MethodGet, "/api/resources/mcp-servers", "resource", "read", resourceadmin.PermissionResourceRead, "List MCP servers")
	registry.Register(http.MethodGet, "/api/resources/mcp-servers/{tool_id}", "resource", "read", resourceadmin.PermissionResourceRead, "Read MCP server")
	registry.Register(http.MethodPost, "/api/resources/mcp-servers", "resource", "manage", resourceadmin.PermissionResourceManage, "Register MCP server")
	registry.Register(http.MethodPatch, "/api/resources/mcp-servers/{tool_id}", "resource", "manage", resourceadmin.PermissionResourceManage, "Update MCP server")
	registry.Register(http.MethodDelete, "/api/resources/mcp-servers/{tool_id}", "resource", "manage", resourceadmin.PermissionResourceManage, "Delete MCP server")
	registry.Register(http.MethodGet, "/api/resources/mcp-servers/discover", "resource", "read", resourceadmin.PermissionResourceRead, "Discover MCP servers")
	registry.Register(http.MethodPost, "/api/resources/mcp-servers/{tool_id}/sync", "resource", "manage", resourceadmin.PermissionResourceManage, "Sync MCP server")
	registry.Register(http.MethodGet, "/api/resources/mcp-servers/{tool_id}/health", "resource", "read", resourceadmin.PermissionResourceRead, "Read MCP server health")
	registry.Register(http.MethodPost, "/api/resources/mcp-servers/{tool_id}/disable", "resource", "manage", resourceadmin.PermissionResourceManage, "Disable MCP server")
	registry.Register(http.MethodPost, "/api/resources/bindings", "resource", "manage", resourceadmin.PermissionResourceManage, "Bind resource to agent")
	registry.Register(http.MethodPost, "/api/resources/bindings", "tool", "call", operations.PermissionToolCall, "Bind callable resource to agent")
	registry.Register(http.MethodDelete, "/api/resources/bindings", "resource", "manage", resourceadmin.PermissionResourceManage, "Unbind resource from agent")
	registry.Register(http.MethodGet, "/api/resources/memory-profiles", "resource", "read", resourceadmin.PermissionResourceRead, "List memory profiles")
	registry.Register(http.MethodGet, "/api/resources/memory-profiles/{memory_profile_id}", "resource", "read", resourceadmin.PermissionResourceRead, "Read memory profile")
	registry.Register(http.MethodPatch, "/api/resources/memory-profiles/{memory_profile_id}", "resource", "manage", resourceadmin.PermissionResourceManage, "Update memory profile settings")
	registry.Register(http.MethodGet, "/api/resources/memory-profiles/personal", "resource", "read", resourceadmin.PermissionResourceRead, "Read personal memory profile")
	registry.Register(http.MethodPost, "/api/resources/memory-profiles/digest", "resource", "read", resourceadmin.PermissionResourceRead, "Build memory digest")
}

func registerMemoryCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/memory/candidates", "memory", "read", memoryadmin.PermissionMemoryRead, "List memory candidates")
	registry.Register(http.MethodPost, "/api/memory/candidates", "memory", "write", memoryadmin.PermissionMemoryWrite, "Create memory candidate")
	registry.Register(http.MethodPost, "/api/memory/candidates/{candidate_id}/accept", "memory", "review", memoryadmin.PermissionMemoryReview, "Accept memory candidate")
	registry.Register(http.MethodPost, "/api/memory/candidates/{candidate_id}/accept", "memory", "world.manage", memoryadmin.PermissionMemoryWorldManage, "Accept organization world memory candidate")
	registry.Register(http.MethodPost, "/api/memory/candidates/{candidate_id}/reject", "memory", "review", memoryadmin.PermissionMemoryReview, "Reject memory candidate")
	registry.Register(http.MethodPost, "/api/memory/candidates/{candidate_id}/merge", "memory", "review", memoryadmin.PermissionMemoryReview, "Merge memory candidate")
	registry.Register(http.MethodPost, "/api/memory/distill/experience", "memory", "distill", memorydistill.PermissionMemoryDistill, "Distill experience memory")
	registry.Register(http.MethodPost, "/api/memory/distill/world", "memory", "distill", memorydistill.PermissionMemoryDistill, "Consolidate organization world memory")
	registry.Register(http.MethodGet, "/api/memory/records", "memory", "read", memoryadmin.PermissionMemoryRead, "List memory records")
	registry.Register(http.MethodPost, "/api/memory/records/touch", "memory", "read", memoryadmin.PermissionMemoryRead, "Mark memory records referenced")
	registry.Register(http.MethodPost, "/api/memory/records/sweep-retention", "memory", "review", memoryadmin.PermissionMemoryReview, "Sweep expired unused memory records")
	registry.Register(http.MethodPost, "/api/memory/records/sweep-retention", "memory", "world.manage", memoryadmin.PermissionMemoryWorldManage, "Sweep organization world memory records")
	registry.Register(http.MethodGet, "/api/memory/records/{memory_id}", "memory", "read", memoryadmin.PermissionMemoryRead, "Read memory record")
	registry.Register(http.MethodPost, "/api/memory/records/{memory_id}/archive", "memory", "review", memoryadmin.PermissionMemoryReview, "Archive memory record")
	registry.Register(http.MethodPost, "/api/memory/records/{memory_id}/archive", "memory", "world.manage", memoryadmin.PermissionMemoryWorldManage, "Archive organization world memory record")
	registry.Register(http.MethodPost, "/api/memory/records/{memory_id}/anonymize", "memory", "review", memoryadmin.PermissionMemoryReview, "Anonymize memory record")
	registry.Register(http.MethodPost, "/api/memory/records/{memory_id}/anonymize", "memory", "world.manage", memoryadmin.PermissionMemoryWorldManage, "Anonymize organization world memory record")
	registry.Register(http.MethodPost, "/api/memory/records/{memory_id}/delete", "memory", "review", memoryadmin.PermissionMemoryReview, "Delete memory record")
	registry.Register(http.MethodPost, "/api/memory/records/{memory_id}/delete", "memory", "world.manage", memoryadmin.PermissionMemoryWorldManage, "Delete organization world memory record")
}

func registerOperationsCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/workforce/members", "workforce", "read", operations.PermissionWorkforceRead, "List workforce members")
	registry.Register(http.MethodGet, "/api/talent-market/members", "workforce", "read", operations.PermissionWorkforceRead, "List talent market members")
	registry.Register(http.MethodPost, "/api/talent-market/import/preview", "workforce", "manage", operations.PermissionWorkforceManage, "Preview talent market import")
	registry.Register(http.MethodPost, "/api/talent-market/import/commit", "workforce", "manage", operations.PermissionWorkforceManage, "Commit talent market import")
	registry.Register(http.MethodPost, "/api/talent-market/export", "workforce", "read", operations.PermissionWorkforceRead, "Export selected talent market members")
	registry.Register(http.MethodDelete, "/api/talent-market/members/{agent_profile_id}", "workforce", "manage", operations.PermissionWorkforceManage, "Physically delete talent market member")
	registry.Register(http.MethodPost, "/api/talent-market/members/delete-batch", "workforce", "manage", operations.PermissionWorkforceManage, "Batch delete talent market members")
	registry.Register(http.MethodPost, "/api/workforce/members", "workforce", "manage", operations.PermissionWorkforceManage, "Create workforce member")
	registry.Register(http.MethodPut, "/api/workforce/members/{agent_profile_id}", "workforce", "manage", operations.PermissionWorkforceManage, "Upsert workforce member")
	registry.Register(http.MethodPatch, "/api/workforce/members/{agent_profile_id}", "workforce", "manage", operations.PermissionWorkforceManage, "Update workforce member")
	registry.Register(http.MethodDelete, "/api/workforce/members/{agent_profile_id}", "workforce", "manage", operations.PermissionWorkforceManage, "Release workforce member to talent market")
	registry.Register(http.MethodGet, "/api/workforce/members/{agent_profile_id}/channel-bindings", "workforce", "read", operations.PermissionWorkforceRead, "List Feishu account ownership for workforce member")
	registry.Register(http.MethodPut, "/api/workforce/members/{agent_profile_id}/channel-bindings/feishu/{account_id}", "workforce", "manage", operations.PermissionWorkforceManage, "Assign Feishu account to workforce member")
	registry.Register(http.MethodDelete, "/api/workforce/members/{agent_profile_id}/channel-bindings/feishu/{account_id}", "workforce", "manage", operations.PermissionWorkforceManage, "Remove Feishu account ownership")
	registry.Register(http.MethodGet, "/api/workforce/avatar-defaults", "workforce", "read", operations.PermissionWorkforceRead, "List built-in workforce avatars")
	registry.Register(http.MethodGet, "/api/workforce/avatar-defaults/{default_id}", "workforce", "read", operations.PermissionWorkforceRead, "Read built-in workforce avatar")
	registry.Register(http.MethodGet, "/api/workforce/members/{agent_profile_id}/avatar", "workforce", "read", operations.PermissionWorkforceRead, "Read workforce member avatar")
	registry.Register(http.MethodPut, "/api/workforce/members/{agent_profile_id}/avatar", "workforce", "manage", operations.PermissionWorkforceManage, "Upload workforce member avatar")
	registry.Register(http.MethodPut, "/api/workforce/members/{agent_profile_id}/avatar/default", "workforce", "manage", operations.PermissionWorkforceManage, "Select built-in workforce avatar")
	registry.Register(http.MethodDelete, "/api/workforce/members/{agent_profile_id}/avatar", "workforce", "manage", operations.PermissionWorkforceManage, "Reset workforce member avatar")
	registry.Register(http.MethodPost, "/api/workforce/members/{agent_profile_id}/release-to-market", "workforce", "manage", operations.PermissionWorkforceManage, "Release workforce member to talent market")
	registry.Register(http.MethodPost, "/api/talent-market/members/{agent_profile_id}/hire", "workforce", "manage", operations.PermissionWorkforceManage, "Hire talent market member into workforce")
	registry.Register(http.MethodPost, "/api/workforce/members/{agent_profile_id}/enable", "workforce", "manage", operations.PermissionWorkforceManage, "Enable workforce member")
	registry.Register(http.MethodPost, "/api/workforce/members/{agent_profile_id}/disable", "workforce", "manage", operations.PermissionWorkforceManage, "Disable workforce member")
	registry.Register(http.MethodGet, "/api/org/graph", "organization", "read", operations.PermissionOrgRead, "Read organization graph")
	registry.Register(http.MethodPost, "/api/org", "organization", "manage", operations.PermissionOrgManage, "Create organization")
	registry.Register(http.MethodDelete, "/api/org/{organization_id}", "organization", "manage", operations.PermissionOrgManage, "Delete organization")
	registry.Register(http.MethodPost, "/api/org/units", "organization", "manage", operations.PermissionOrgManage, "Create organization unit")
	registry.Register(http.MethodPost, "/api/org/positions", "organization", "manage", operations.PermissionOrgManage, "Create organization position")
	registry.Register(http.MethodDelete, "/api/org/positions/{position_id}", "organization", "manage", operations.PermissionOrgManage, "Delete organization position")
	registry.Register(http.MethodDelete, "/api/org/units/{department_id}", "organization", "manage", operations.PermissionOrgManage, "Delete organization unit")
	registry.Register(http.MethodPut, "/api/org/nodes/{organization_node_id}/owner", "organization", "manage", operations.PermissionOrgManage, "Update organization owner")
	registry.Register(http.MethodGet, "/api/org/roles", "roles", "read", operations.PermissionRolesRead, "List organization roles")
	registry.Register(http.MethodGet, "/api/org/role-assignments", "roles", "read", operations.PermissionRolesRead, "List organization role assignments")
	registry.Register(http.MethodPut, "/api/org/role-assignments", "roles", "manage", operations.PermissionRolesManage, "Update organization role assignments")
	registry.Register(http.MethodDelete, "/api/org/role-assignments/{assignment_id}", "roles", "manage", operations.PermissionRolesManage, "Delete organization role assignments")
	registry.Register(http.MethodGet, "/api/permissions/capabilities", "permissions", "read", operations.PermissionPermissionsRead, "List registered permission capabilities")
	registry.Register(http.MethodGet, "/api/permissions/matrix", "permissions", "read", operations.PermissionPermissionsRead, "Read permission matrix")
	registry.Register(http.MethodPost, "/api/permissions/decisions", "permissions", "read", operations.PermissionPermissionsRead, "Evaluate permission decision")
	registry.Register(http.MethodGet, "/api/permissions/policy", "permissions", "read", operations.PermissionPermissionsRead, "Read permission policy")
	registry.Register(http.MethodPut, "/api/permissions/policy", "permissions", "manage", operations.PermissionPermissionsManage, "Update permission policy")
	registry.Register(http.MethodGet, "/api/permissions/roles", "permissions", "read", operations.PermissionPermissionsRead, "List permission roles")
	registry.Register(http.MethodGet, "/api/permissions/packages", "permissions", "read", operations.PermissionPermissionsRead, "List permission packages")
	registry.Register(http.MethodGet, "/api/permissions/packages/{package_id}/capabilities", "permissions", "read", operations.PermissionPermissionsRead, "List permission package capabilities")
	registry.Register(http.MethodGet, "/api/permissions/effective-grants", "permissions", "read", operations.PermissionPermissionsRead, "List effective permission grants")
	registry.Register(http.MethodPut, "/api/permissions/policy/batch", "permissions", "manage", operations.PermissionPermissionsManage, "Apply permission policy changes")
	registry.Register(http.MethodGet, "/api/audit-log", "audit", "read", operations.PermissionAuditRead, "List audit log")
	registry.Register(http.MethodGet, "/api/audit/ip-locks", "audit", "read", operations.PermissionAuditRead, "List authentication IP locks")
	registry.Register(http.MethodPost, "/api/audit/ip-locks/unlock", "audit", "manage", operations.PermissionAuditManage, "Unlock authentication IP")
	registry.Register(http.MethodGet, "/api/evaluations", "operations", "read", operations.PermissionOperationsRead, "List evaluations")
	registry.Register(http.MethodGet, "/api/evaluations/stream", "operations", "read", operations.PermissionOperationsRead, "Stream evaluations")
}

func registerAuthorizationCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/authorization/relation-backfill/diagnostics", "authorization", "read", operations.PermissionPermissionsRead, "Read authorization relation backfill diagnostics")
	registry.Register(http.MethodGet, "/api/authorization/enforce-transition/diagnostics", "authorization", "read", operations.PermissionPermissionsRead, "Read authorization enforce transition diagnostics")
}

func registerIdentityCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/identity/subjects", "identity", "read", operations.PermissionPermissionsRead, "List external identity subjects")
	registry.Register(http.MethodPost, "/api/identity/subjects", "identity", "manage", operations.PermissionPermissionsManage, "Link external identity subject")
	registry.Register(http.MethodDelete, "/api/identity/subjects/{subject_id}", "identity", "manage", operations.PermissionPermissionsManage, "Disable external identity subject")
	registry.Register(http.MethodPost, "/api/identity/subjects/{subject_id}/enable", "identity", "manage", operations.PermissionPermissionsManage, "Enable external identity subject")
	registry.Register(http.MethodGet, "/api/oauth2/clients", "identity", "read", operations.PermissionPermissionsRead, "List OIDC clients")
	registry.Register(http.MethodPost, "/api/oauth2/clients", "identity", "manage", operations.PermissionPermissionsManage, "Upsert OIDC client")
}

func registerKnowledgeCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/knowledge-providers", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List knowledge providers")
	registry.Register(http.MethodPost, "/api/knowledge-providers", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Create knowledge provider")
	registry.Register(http.MethodGet, "/api/knowledge-providers/{provider_id}", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read knowledge provider")
	registry.Register(http.MethodPatch, "/api/knowledge-providers/{provider_id}", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Update knowledge provider")
	registry.Register(http.MethodPost, "/api/knowledge-providers/{provider_id}/test", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Test knowledge provider")
	registry.Register(http.MethodPost, "/api/knowledge-providers/{provider_id}/sync", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Sync knowledge provider")
	registry.Register(http.MethodGet, "/api/knowledge-bases", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List knowledge bases")
	registry.Register(http.MethodPost, "/api/knowledge-bases", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Create knowledge base")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read knowledge base")
	registry.Register(http.MethodPatch, "/api/knowledge-bases/{knowledge_base_id}", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Update knowledge base")
	registry.Register(http.MethodDelete, "/api/knowledge-bases/{knowledge_base_id}", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Delete knowledge base")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/bindings", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List knowledge bindings")
	registry.Register(http.MethodPost, "/api/knowledge-bases/{knowledge_base_id}/bindings", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Bind knowledge base")
	registry.Register(http.MethodDelete, "/api/knowledge-bases/{knowledge_base_id}/bindings/{binding_id}", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Unbind knowledge base")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List knowledge documents")
	registry.Register(http.MethodPost, "/api/knowledge-bases/{knowledge_base_id}/documents", "knowledge", "write", knowledgeadmin.PermissionKnowledgeWrite, "Create knowledge document metadata")
	registry.Register(http.MethodPost, "/api/knowledge-bases/{knowledge_base_id}/documents/sync", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Sync knowledge document catalog")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read knowledge document")
	registry.Register(http.MethodDelete, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Delete knowledge document")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/status", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Refresh knowledge document status")
	registry.Register(http.MethodPost, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/reprocess", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Reprocess knowledge document")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/result", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read knowledge document processing result")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/source", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read knowledge document source")
	registry.Register(http.MethodPut, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/source", "knowledge", "write", knowledgeadmin.PermissionKnowledgeWrite, "Update knowledge document source")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/revisions", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List knowledge document revisions")
	registry.Register(http.MethodPut, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/revisions/{revision}", "knowledge", "write", knowledgeadmin.PermissionKnowledgeWrite, "Publish knowledge document revision")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/ontology", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read knowledge ontology projection")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/relations", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List knowledge document relations")
	registry.Register(http.MethodPost, "/api/knowledge-bases/{knowledge_base_id}/reindex", "knowledge", "manage", knowledgeadmin.PermissionKnowledgeManage, "Reindex knowledge base")
	registry.Register(http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/jobs", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "List knowledge jobs")
	registry.Register(http.MethodGet, "/api/knowledge-jobs/{job_id}", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Read knowledge job")
	registry.Register(http.MethodPost, "/api/knowledge-bases/{knowledge_base_id}/search", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Search knowledge base")
	registry.Register(http.MethodPost, "/api/knowledge/search", "knowledge", "read", knowledgeadmin.PermissionKnowledgeRead, "Search visible knowledge bases")
}

func registerSettingsCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/settings", "settings", "read", permissionSettingsRead, "Read settings")
	registry.Register(http.MethodPut, "/api/settings", "settings", "manage", permissionSettingsManage, "Update settings")
	registry.Register(http.MethodGet, "/api/settings/runtime/discovery", "settings", "read", permissionSettingsRead, "Discover local runtime candidates")
	registry.Register(http.MethodPost, "/api/settings/knowledge/pageindex/test", "settings", "manage", permissionSettingsManage, "Test PageIndex knowledge connection")
	registry.Register(http.MethodGet, "/api/settings/image-generation", "settings", "read", permissionSettingsRead, "Read image generation settings")
	registry.Register(http.MethodPut, "/api/settings/image-generation", "settings", "manage", permissionSettingsManage, "Configure image generation gateway")
	registry.Register(http.MethodPost, "/api/settings/image-generation/test", "settings", "manage", permissionSettingsManage, "Check saved image gateway model catalog")
	registry.Register(http.MethodGet, "/api/settings/gateway", "settings", "read", permissionSettingsRead, "Read gateway settings")
	registry.Register(http.MethodPut, "/api/settings/gateway", "settings", "manage", permissionSettingsManage, "Update gateway settings")
	registry.Register(http.MethodPut, "/api/settings/gateway/raw", "settings", "manage", permissionSettingsManage, "Update raw gateway settings")
	registry.Register(http.MethodGet, "/api/settings/gateway/accounts", "settings", "read", permissionSettingsRead, "Read Gateway account connection status")
	registry.Register(http.MethodPost, "/api/settings/gateway/feishu/registrations", "settings", "manage", permissionSettingsManage, "Start Feishu application registration")
	registry.Register(http.MethodGet, "/api/settings/gateway/feishu/registrations/{registration_id}", "settings", "manage", permissionSettingsManage, "Read Feishu application registration")
	registry.Register(http.MethodDelete, "/api/settings/gateway/feishu/registrations/{registration_id}", "settings", "manage", permissionSettingsManage, "Cancel Feishu application registration")
	registry.Register(http.MethodPost, "/api/gateway/deliveries", "gateway", "send", permissionGatewaySend, "Send Gateway delivery")
	registry.Register(http.MethodGet, "/api/gateway/deliveries/{delivery_id}", "settings", "read", permissionSettingsRead, "Read Gateway delivery")
}

func registerDiscussionCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/discussions", "discussion", "read", permissionDiscussionRead, "List discussions")
	registry.Register(http.MethodPost, "/api/discussions/prewarm", "discussion", "write", permissionDiscussionWrite, "Prepare a new discussion runtime")
	registry.Register(http.MethodPost, "/api/discussions/messages", "discussion", "write", permissionDiscussionWrite, "Send discussion message")
	registry.Register(http.MethodPost, "/api/discussions/attachments", "discussion", "write", permissionDiscussionWrite, "Start discussion with attachment")
	registry.Register(http.MethodGet, "/api/discussions/{discussion_id}", "discussion", "read", permissionDiscussionRead, "Read discussion")
	registry.Register(http.MethodDelete, "/api/discussions/{discussion_id}", "discussion", "write", permissionDiscussionWrite, "Delete discussion")
	registry.Register(http.MethodPost, "/api/discussions/{discussion_id}/prewarm", "discussion", "write", permissionDiscussionWrite, "Prepare a discussion runtime")
	registry.Register(http.MethodGet, "/api/discussions/{discussion_id}/messages", "discussion", "read", permissionDiscussionRead, "List discussion messages")
	registry.Register(http.MethodPost, "/api/discussions/{discussion_id}/messages", "discussion", "write", permissionDiscussionWrite, "Send discussion message")
	registry.Register(http.MethodPost, "/api/discussions/{discussion_id}/messages/{message_id}/retry", "discussion", "write", permissionDiscussionWrite, "Retry discussion message")
	registry.Register(http.MethodGet, "/api/discussions/{discussion_id}/attachments", "discussion", "read", permissionDiscussionRead, "List discussion attachments")
	registry.Register(http.MethodPost, "/api/discussions/{discussion_id}/attachments", "discussion", "write", permissionDiscussionWrite, "Upload discussion attachment")
	registry.Register(http.MethodGet, "/api/discussions/{discussion_id}/attachments/{attachment_id}/download", "discussion", "read", permissionDiscussionRead, "Download discussion attachment")
	registry.Register(http.MethodGet, "/api/discussions/{discussion_id}/attachments/{attachment_id}/preview", "discussion", "read", permissionDiscussionRead, "Preview discussion HTML attachment")
	registry.Register(http.MethodPost, "/api/discussions/{discussion_id}/members", "discussion", "write", permissionDiscussionWrite, "Add discussion members")
	registry.Register(http.MethodPatch, "/api/discussions/{discussion_id}/workspace", "discussion", "write", permissionDiscussionWrite, "Set discussion workspace")
	registry.Register(http.MethodPatch, "/api/discussions/{discussion_id}/hil-mode", "discussion", "write", permissionDiscussionWrite, "Set discussion HIL mode")
	registry.Register(http.MethodPost, "/api/discussions/{discussion_id}/approvals/{approval_id}/decision", "approval", "decide", permissionApprovalsDecide, "Decide discussion runtime approvals")
	registry.Register(http.MethodPost, "/api/discussions/{discussion_id}/abort", "discussion", "write", permissionDiscussionWrite, "Abort active discussion processing")
	registry.Register(http.MethodPost, "/api/discussions/{discussion_id}/interjections", "discussion", "write", permissionDiscussionWrite, "Interject into active discussion processing")
	registry.Register(http.MethodGet, "/api/discussions/{discussion_id}/debug/latest", "discussion", "read", permissionDiscussionRead, "Read latest discussion run debug")
	registry.Register(http.MethodGet, "/api/discussions/{discussion_id}/runs/{run_id}/debug", "discussion", "read", permissionDiscussionRead, "Read discussion run debug")
}

func registerRealtimeCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/realtime/connect", "discussion", "read", permissionDiscussionRead, "Open realtime event connection")
}

func registerSchedulerCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/scheduled-runs", "scheduler", "read", permissionSchedulerRead, "List scheduled run inbox")
	registry.Register(http.MethodPatch, "/api/scheduled-runs/{scheduled_run_id}/read", "scheduler", "read", permissionSchedulerRead, "Update personal scheduled run read state")
	registry.Register(http.MethodGet, "/api/scheduled-tasks", "scheduler", "read", permissionSchedulerRead, "List scheduled tasks")
	registry.Register(http.MethodGet, "/api/scheduled-tasks/summary", "scheduler", "read", permissionSchedulerRead, "Read scheduled task statistics")
	registry.Register(http.MethodPost, "/api/scheduled-tasks", "scheduler", "write", permissionSchedulerWrite, "Create scheduled task")
	registry.Register(http.MethodGet, "/api/scheduled-tasks/{scheduled_task_id}", "scheduler", "read", permissionSchedulerRead, "Read scheduled task")
	registry.Register(http.MethodPatch, "/api/scheduled-tasks/{scheduled_task_id}", "scheduler", "write", permissionSchedulerWrite, "Update scheduled task")
	registry.Register(http.MethodPost, "/api/scheduled-tasks/{scheduled_task_id}/cancel", "scheduler", "write", permissionSchedulerWrite, "Cancel scheduled task")
	registry.Register(http.MethodPost, "/api/scheduled-tasks/{scheduled_task_id}/run-now", "scheduler", "run", permissionSchedulerRun, "Run scheduled task now")
	registry.Register(http.MethodGet, "/api/scheduled-tasks/{scheduled_task_id}/runs", "scheduler", "read", permissionSchedulerRead, "List scheduled task runs")
	registry.Register(http.MethodGet, "/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/responsibility-evaluations", "scheduler", "read", permissionSchedulerRead, "List scheduled run responsibility evaluations")
	registry.Register(http.MethodGet, "/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/artifacts/content", "scheduler", "read", permissionSchedulerRead, "Read scheduled run artifact")
}

func registerTodoCapabilities(registry *PermissionCapabilityRegistry) {
	registry.Register(http.MethodGet, "/api/work-hub/summary", "todo", "read", toolstd.PermissionTodoRead, "Read personal work hub summary")
	registry.Register(http.MethodGet, "/api/todos", "todo", "read", toolstd.PermissionTodoRead, "List todo items")
	registry.Register(http.MethodPost, "/api/todo-drafts/suggest", "todo", "write", toolstd.PermissionTodoWrite, "Suggest todo or idea title and tags")
	registry.Register(http.MethodPost, "/api/todos", "todo", "write", toolstd.PermissionTodoWrite, "Create todo item")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/attachments", "todo", "write", toolstd.PermissionTodoWrite, "Upload Todo feedback attachment")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/attachments/{attachment_id}", "todo", "read", toolstd.PermissionTodoRead, "Download Todo feedback attachment")
	registry.Register(http.MethodDelete, "/api/todos/{todo_id}/attachments/{attachment_id}", "todo", "write", toolstd.PermissionTodoWrite, "Remove unsubmitted Todo attachment")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}", "todo", "read", toolstd.PermissionTodoRead, "Read todo item")
	registry.Register(http.MethodPatch, "/api/todos/{todo_id}", "todo", "write", toolstd.PermissionTodoWrite, "Update todo item")
	registry.Register(http.MethodDelete, "/api/todos/{todo_id}", "todo", "write", toolstd.PermissionTodoWrite, "Delete todo item")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/comments", "todo", "read", toolstd.PermissionTodoRead, "List todo comments")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/comments", "todo", "write", toolstd.PermissionTodoWrite, "Create todo comment")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/missions", "todo", "read", toolstd.PermissionTodoRead, "List Todo execution attempts")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/progress", "todo", "read", toolstd.PermissionTodoRead, "Read Todo execution progress")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/discussion", "todo", "read", toolstd.PermissionTodoRead, "Read canonical Todo discussion")
	registry.Register(http.MethodGet, "/api/todos/{todo_id}/execution-plans", "todo", "read", toolstd.PermissionTodoRead, "List Todo execution plans")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/automation/check", "todo", "write", toolstd.PermissionTodoWrite, "Check Todo automation eligibility")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/automation/start", "todo", "write", toolstd.PermissionTodoWrite, "Start approved Todo automation")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/execution-attempts", "todo", "write", toolstd.PermissionTodoWrite, "Start approved Todo execution attempt")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/claim", "todo", "write", toolstd.PermissionTodoWrite, "Claim todo item")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/move", "todo", "write", toolstd.PermissionTodoWrite, "Move todo item")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/complete", "todo", "write", toolstd.PermissionTodoWrite, "Complete todo item")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/cancel", "todo", "write", toolstd.PermissionTodoWrite, "Cancel todo item")
	registry.Register(http.MethodPost, "/api/todos/{todo_id}/reopen", "todo", "write", toolstd.PermissionTodoWrite, "Reopen todo item")
	registry.Register(http.MethodGet, "/api/ideas", "idea", "read", toolstd.PermissionIdeaRead, "List idea records")
	registry.Register(http.MethodPost, "/api/ideas", "idea", "write", toolstd.PermissionIdeaWrite, "Create idea record")
	registry.Register(http.MethodGet, "/api/ideas/{idea_id}", "idea", "read", toolstd.PermissionIdeaRead, "Read idea record")
	registry.Register(http.MethodPatch, "/api/ideas/{idea_id}", "idea", "write", toolstd.PermissionIdeaWrite, "Update idea record")
	registry.Register(http.MethodDelete, "/api/ideas/{idea_id}", "idea", "write", toolstd.PermissionIdeaWrite, "Delete idea record")
	registry.Register(http.MethodGet, "/api/ideas/{idea_id}/comments", "idea", "read", toolstd.PermissionIdeaRead, "List idea comments")
	registry.Register(http.MethodPost, "/api/ideas/{idea_id}/comments", "idea", "write", toolstd.PermissionIdeaWrite, "Create idea comment")
	registry.Register(http.MethodPost, "/api/ideas/{idea_id}/archive", "idea", "write", toolstd.PermissionIdeaWrite, "Archive idea record")
	registry.Register(http.MethodPost, "/api/ideas/{idea_id}/restore", "idea", "write", toolstd.PermissionIdeaWrite, "Restore idea record")
	registry.Register(http.MethodPost, "/api/ideas/{idea_id}/transition", "idea", "write", toolstd.PermissionIdeaWrite, "Move idea through review lifecycle")
	registry.Register(http.MethodPost, "/api/ideas/{idea_id}/vote", "idea", "write", toolstd.PermissionIdeaWrite, "Toggle idea support vote")
	registry.Register(http.MethodPost, "/api/ideas/{idea_id}/convert-to-todo", "idea", "write", toolstd.PermissionIdeaWrite, "Convert idea to todo")
}

func capabilityID(method, path, scope string) string {
	value := strings.ToLower(method + "." + strings.Trim(path, "/") + "." + scope)
	replacer := strings.NewReplacer("/", ".", "{", "", "}", "", "_", ".", ":", ".", " ", ".")
	value = replacer.Replace(value)
	value = strings.ReplaceAll(value, "..", ".")
	return strings.Trim(value, ".")
}

func viewerHasScope(scopes []string, required, manage string) bool {
	required = strings.TrimSpace(required)
	manage = strings.TrimSpace(manage)
	if manage == required {
		manage = impliedManageScope(required)
	}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == required || scope == manage {
			return true
		}
	}
	return false
}

func impliedManageScope(required string) string {
	if strings.HasSuffix(required, ".read") {
		return strings.TrimSuffix(required, ".read") + ".manage"
	}
	return required
}

type authorizedRoutePermissionScopesContextKey struct{}

func (r *Router) authorizeRegisteredRoute(w http.ResponseWriter, req *http.Request) bool {
	capabilities := r.permissionsForRequest(req)
	if len(capabilities) == 0 {
		if unauthenticatedRouteAllowed(req) || authenticatedProfileRouteAllowed(req) || !isAPIRoute(req) {
			return true
		}
		status, body := MapError(operations.ErrPermissionDenied, correlationID(req))
		writeJSON(w, status, body)
		return false
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return false
	}
	for _, capability := range capabilities {
		allowed, authorizeErr := r.authorizePermissionCapability(req, viewer, capability)
		if authorizeErr != nil {
			status, body := MapError(authorizeErr, correlationID(req))
			writeJSON(w, status, body)
			return false
		}
		if allowed {
			ensureRequestPermissionScope(req, capability.Scope)
			return true
		}
	}
	if selfResponsibilityRouteAllowed(req, viewer) {
		return true
	}
	status, body := MapError(operations.ErrPermissionDenied, correlationID(req))
	writeJSON(w, status, body)
	return false
}

func (r *Router) authorizeAdditionalPermissionCapability(w http.ResponseWriter, req *http.Request, capability PermissionCapabilityDTO) bool {
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return false
	}
	allowed, err := r.authorizePermissionCapability(req, viewer, capability)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return false
	}
	if !allowed {
		status, body := MapError(operations.ErrPermissionDenied, correlationID(req))
		writeJSON(w, status, body)
		return false
	}
	ensureRequestPermissionScope(req, capability.Scope)
	return true
}

func (r *Router) authorizePermissionCapability(req *http.Request, viewer missioncontrol.ViewerRef, capability PermissionCapabilityDTO) (bool, error) {
	if strings.TrimSpace(capability.Scope) == "" {
		return false, nil
	}
	// Internal viewer authentication is already bound to loopback and the
	// deployment secret, so its declared coarse scopes are authoritative.
	if identity.TrustedInternalServiceFromContext(req.Context()) && viewerHasScope(viewer.PermissionScope, capability.Scope, capability.Scope) {
		return true, nil
	}
	if r.authorization != nil {
		objects := authorizationObjectsForCapability(req, capability)
		object := objects[0]
		action := authorizationActionForCapability(capability, object)
		principal := principalForRouteAuthorization(req, viewer)
		if workforceAvatarSelfServiceAllowed(req, capability, principal) {
			return true, nil
		}
		if routeObjectRequiresRoleAuthorization(object) && principal.ViewerKind != identity.ViewerKindSystem && !identity.TrustedInternalServiceFromContext(req.Context()) {
			if r.roleAuthorization == nil {
				return false, operations.ErrServiceDependency
			}
			return r.roleAuthorization.AuthorizeRolePermission(req.Context(), principal, capability.Scope, objects)
		}
		err := r.authorization.Can(req.Context(), principal, action, object)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, authorizationapp.ErrDenied) {
			return false, err
		}
		if r.roleAuthorization == nil {
			return false, nil
		}
		return r.roleAuthorization.AuthorizeRolePermission(req.Context(), principal, capability.Scope, objects)
	}
	if r.accessControl != nil {
		return r.accessControl.Authorize(req.Context(), accesscontrol.AuthorizeRequest{
			PrincipalKind: string(viewer.ViewerKind),
			PrincipalID:   viewer.ViewerID,
			CapabilityID:  capability.CapabilityID,
			Scope:         capability.Scope,
		})
	}
	return viewerHasScope(viewer.PermissionScope, capability.Scope, capability.Scope), nil
}

func workforceAvatarSelfServiceAllowed(req *http.Request, capability PermissionCapabilityDTO, principal identity.Principal) bool {
	if req == nil || req.URL == nil || !isWorkforceAvatarMutationCapability(capability) {
		return false
	}
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() || principal.ViewerKind != identity.ViewerKindUser || strings.TrimSpace(principal.AgentProfileID) == "" {
		return false
	}
	params, ok := pathTemplateValues(capability.Path, req.URL.Path)
	return ok && strings.TrimSpace(params["agent_profile_id"]) == principal.AgentProfileID
}

func selfResponsibilityRouteAllowed(req *http.Request, viewer missioncontrol.ViewerRef) bool {
	if req == nil || req.URL == nil || req.Method != http.MethodGet || viewer.ViewerKind != missioncontrol.ViewerKindAgent {
		return false
	}
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if len(parts) >= 2 && len(parts) <= 3 && parts[0] == "api" && parts[1] == "responsibilities" {
		return true
	}
	return len(parts) == 4 && parts[0] == "api" && parts[1] == "responsibility-profiles" && parts[2] == "agents" && strings.TrimSpace(parts[3]) == strings.TrimSpace(viewer.ViewerID)
}

func isWorkforceAvatarMutationCapability(capability PermissionCapabilityDTO) bool {
	if strings.TrimSpace(capability.Domain) != "workforce" || strings.TrimSpace(capability.Scope) != operations.PermissionWorkforceManage {
		return false
	}
	method := strings.ToUpper(strings.TrimSpace(capability.Method))
	switch strings.TrimSpace(capability.Path) {
	case "/api/workforce/members/{agent_profile_id}/avatar":
		return method == http.MethodPut || method == http.MethodDelete
	case "/api/workforce/members/{agent_profile_id}/avatar/default":
		return method == http.MethodPut
	default:
		return false
	}
}

func routeObjectRequiresRoleAuthorization(object identity.ObjectRef) bool {
	switch strings.ToLower(strings.TrimSpace(object.Type)) {
	case "platform", "organization_scope", "organization", "department", "position", "workforce", "role_assignment", "role":
		return true
	default:
		return false
	}
}

func authorizationObjectsForCapability(req *http.Request, capability PermissionCapabilityDTO) []identity.ObjectRef {
	if strings.TrimSpace(capability.Domain) == "roles" {
		if code := queryValue(req, "organization_code"); code != "" {
			return []identity.ObjectRef{{Type: "organization_scope", ID: code, TenantID: authorizationTenantID(req)}}
		}
		var payload struct {
			Assignments []struct {
				OrganizationCode string `json:"organization_code"`
			} `json:"assignments"`
		}
		if authorizationBodyJSON(req, &payload) && len(payload.Assignments) > 0 {
			objects := make([]identity.ObjectRef, 0, len(payload.Assignments))
			seen := map[string]struct{}{}
			for _, assignment := range payload.Assignments {
				code := strings.TrimSpace(assignment.OrganizationCode)
				if code == "" {
					return []identity.ObjectRef{{Type: "platform", ID: "global", TenantID: "global"}}
				}
				if _, ok := seen[code]; ok {
					continue
				}
				seen[code] = struct{}{}
				objects = append(objects, identity.ObjectRef{Type: "organization_scope", ID: code, TenantID: authorizationTenantID(req)})
			}
			if len(objects) > 0 {
				return objects
			}
		}
	}
	return []identity.ObjectRef{authorizationObjectForCapability(req, capability)}
}

func unauthenticatedRouteAllowed(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	path := strings.Trim(req.URL.Path, "/")
	parts := strings.Split(path, "/")
	return req.Method == http.MethodGet && len(parts) == 2 && parts[0] == "api" && parts[1] == "health"
}

func authenticatedProfileRouteAllowed(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	for _, route := range authenticatedProfileRoutes {
		if route.method == method && pathTemplateMatches(route.path, req.URL.Path) {
			return true
		}
	}
	return false
}

var authenticatedProfileRoutes = [...]struct {
	method string
	path   string
}{
	{method: http.MethodGet, path: "/api/notifications"},
	{method: http.MethodGet, path: "/api/notifications/summary"},
	{method: http.MethodGet, path: "/api/notifications/preferences"},
	{method: http.MethodGet, path: "/api/notifications/channels"},
	{method: http.MethodPut, path: "/api/notifications/preferences"},
	{method: http.MethodGet, path: "/api/notifications/sources"},
	{method: http.MethodPost, path: "/api/notifications/sources"},
	{method: http.MethodGet, path: "/api/notifications/members"},
	{method: http.MethodGet, path: "/api/notifications/sources/candidates"},
	{method: http.MethodGet, path: "/api/notifications/sources/{id}"},
	{method: http.MethodPut, path: "/api/notifications/sources/{id}"},
	{method: http.MethodGet, path: "/api/notifications/sources/{id}/preview"},
	{method: http.MethodGet, path: "/api/notifications/sources/{id}/events"},
	{method: http.MethodPut, path: "/api/notifications/sources/{id}/subscription"},
	{method: http.MethodGet, path: "/api/notifications/items/{id}"},
	{method: http.MethodPatch, path: "/api/notifications/items/{id}"},
	{method: http.MethodGet, path: "/api/notifications/items/{id}/artifacts/{artifact_id}"},
	{method: http.MethodGet, path: "/api/notifications/events/{id}/deliveries"},
	{method: http.MethodPost, path: "/api/diagnostics/client-errors"},
	{method: http.MethodGet, path: "/api/profile/personal-assistant"},
	{method: http.MethodPost, path: "/api/profile/personal-assistant"},
	{method: http.MethodPut, path: "/api/profile/personal-assistant"},
	{method: http.MethodGet, path: "/api/profile/personal-assistant/avatar"},
	{method: http.MethodPut, path: "/api/profile/personal-assistant/avatar"},
	{method: http.MethodDelete, path: "/api/profile/personal-assistant/avatar"},
	{method: http.MethodGet, path: "/api/profile/personal-assistant/notifications"},
	{method: http.MethodPut, path: "/api/profile/personal-assistant/notifications"},
	{method: http.MethodPost, path: "/api/profile/personal-assistant/audio/speech"},
	{method: http.MethodPost, path: "/api/profile/personal-assistant/audio/transcriptions"},
	{method: http.MethodGet, path: "/api/profile/weixin"},
	{method: http.MethodDelete, path: "/api/profile/weixin"},
	{method: http.MethodPut, path: "/api/profile/weixin/default-agent"},
	{method: http.MethodPost, path: "/api/profile/weixin/login/start"},
	{method: http.MethodGet, path: "/api/profile/weixin/login/{session_key}"},
	{method: http.MethodPost, path: "/api/profile/weixin/login/{session_key}/verify-code"},
	{method: http.MethodGet, path: "/api/profile/gateway-bindings"},
	{method: http.MethodPost, path: "/api/profile/gateway-bindings/{channel}/code"},
	{method: http.MethodDelete, path: "/api/profile/gateway-bindings/{binding_id}"},
	{method: http.MethodGet, path: "/api/profile/identity/feishu"},
	{method: http.MethodDelete, path: "/api/profile/identity/feishu"},
	{method: http.MethodGet, path: "/api/profile/identity/feishu/begin"},
	{method: http.MethodGet, path: "/api/profile/identity/feishu/callback"},
}

func isAPIRoute(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	path := strings.Trim(req.URL.Path, "/")
	return path == "api" || strings.HasPrefix(path, "api/")
}

func principalForRouteAuthorization(req *http.Request, viewer missioncontrol.ViewerRef) identity.Principal {
	if req != nil {
		if principal, ok := identity.PrincipalFromContext(req.Context()); ok {
			principal = identity.NormalizePrincipal(principal)
			if viewer.ViewerKind == missioncontrol.ViewerKindSystem &&
				strings.EqualFold(strings.TrimSpace(viewer.ViewerID), agentcore.SystemSuperadminPrincipalID) &&
				strings.EqualFold(strings.TrimSpace(principal.AgentProfileID), agentcore.SystemSuperadminPrincipalID) &&
				strings.EqualFold(strings.TrimSpace(principal.Username), userauth.SuperadminUsername) {
				principal.Subject = agentcore.SystemSuperadminPrincipalID
				principal.UserID = agentcore.SystemSuperadminPrincipalID
				principal.AgentProfileID = ""
				principal.ViewerKind = identity.ViewerKindSystem
			}
			return principal
		}
	}
	viewerKind := string(viewer.ViewerKind)
	if strings.TrimSpace(viewerKind) == "" {
		viewerKind = identity.ViewerKindUser
	}
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "transport",
		Subject:         viewer.ViewerID,
		UserID:          viewer.ViewerID,
		Username:        viewer.ViewerID,
		ViewerKind:      viewerKind,
		CoarseScopes:    append([]string(nil), viewer.PermissionScope...),
	})
}

func ensureRequestPermissionScope(req *http.Request, scope string) {
	if req == nil {
		return
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return
	}
	scopes := splitHeader(req.Header.Get("X-Permission-Scope"))
	headerHasScope := false
	for _, existing := range scopes {
		if existing == scope {
			headerHasScope = true
			break
		}
	}
	if !headerHasScope {
		scopes = append(scopes, scope)
		req.Header.Set("X-Permission-Scope", strings.Join(scopes, ","))
	}
	authorizedScopes := authorizedRoutePermissionScopes(req)
	for _, existing := range authorizedScopes {
		if strings.TrimSpace(existing) == scope {
			return
		}
	}
	authorizedScopes = append(authorizedScopes, scope)
	*req = *req.WithContext(context.WithValue(req.Context(), authorizedRoutePermissionScopesContextKey{}, authorizedScopes))
}

func authorizedRoutePermissionScopes(req *http.Request) []string {
	if req == nil {
		return nil
	}
	scopes, _ := req.Context().Value(authorizedRoutePermissionScopesContextKey{}).([]string)
	return append([]string(nil), scopes...)
}

func authorizationActionForCapability(capability PermissionCapabilityDTO, object identity.ObjectRef) string {
	scope := strings.TrimSpace(capability.Scope)
	domain := strings.TrimSpace(capability.Domain)
	action := strings.TrimSpace(capability.Action)
	switch object.Type {
	case "mission":
		if domain == "discussion" {
			switch action {
			case "read", "list", "write", "create", "update":
				// Mission routes can only identify the parent mission here. The
				// command service performs the object-level discussion.write check
				// against the canonical discussion before accepting a mutation.
				return "mission.view"
			case "delete", "manage":
				return "mission.manage"
			default:
				return scope
			}
		}
		if domain != "mission" {
			return scope
		}
		switch action {
		case "read", "list":
			return "mission.view"
		case "monitor":
			return "mission.monitor"
		case "write", "create":
			return "mission.edit"
		case "delete", "manage":
			return "mission.manage"
		default:
			return scope
		}
	case "project":
		if domain != "project" {
			return scope
		}
		switch action {
		case "read", "list":
			return "project.view"
		case "create", "update":
			return "project.edit"
		case "archive", "manage":
			return "project.manage"
		default:
			return scope
		}
	case "development_project":
		if domain != "development_project" {
			return scope
		}
		switch action {
		case "read", "list":
			return "development_project.view"
		case "create":
			return "development_project.create"
		case "write", "update":
			return "development_project.edit"
		case "manage", "delete", "archive":
			return "development_project.manage"
		default:
			return scope
		}
	case "scm_connection":
		if domain != "scm_connection" {
			return scope
		}
		switch action {
		case "read", "list":
			return "scm_connection.view"
		case "use":
			return "scm_connection.use"
		case "create", "write", "update", "manage", "delete":
			return "scm_connection.manage"
		default:
			return scope
		}
	case "resource":
		switch domain {
		case "resource":
			switch action {
			case "read":
				return "resource.view"
			case "manage", "create", "update", "delete":
				return "resource.manage"
			default:
				return scope
			}
		case "tool":
			if action == "call" {
				return "resource.use"
			}
			return scope
		default:
			return scope
		}
	case "discussion":
		if domain != "discussion" {
			return scope
		}
		switch action {
		case "read", "list":
			return "discussion.read"
		case "write", "create", "update":
			return "discussion.write"
		case "delete", "manage":
			return "discussion.manage"
		default:
			return scope
		}
	case "organization":
		if action == "read" || action == "list" {
			return "organization.read"
		}
		return "organization.manage"
	case "department":
		if action == "read" || action == "list" {
			return "department.read"
		}
		return "department.manage"
	default:
		return scope
	}
}

func unfilteredOrganizationGraphUsesPlatformScope(req *http.Request, capability PermissionCapabilityDTO) bool {
	if req == nil || req.URL == nil ||
		!strings.EqualFold(strings.TrimSpace(capability.Method), http.MethodGet) ||
		strings.TrimSpace(capability.Path) != "/api/org/graph" {
		return false
	}
	organizationID := strings.TrimSpace(queryValue(req, "organization_id"))
	return organizationID == ""
}

func authorizationObjectForCapability(req *http.Request, capability PermissionCapabilityDTO) identity.ObjectRef {
	tenantID := authorizationTenantID(req)
	if req == nil || req.URL == nil {
		return identity.ObjectRef{
			Type:     "platform",
			ID:       "global",
			TenantID: "global",
		}
	}
	params, _ := pathTemplateValues(capability.Path, req.URL.Path)
	switch strings.TrimSpace(capability.Domain) {
	case "mission":
		missionID := firstNonEmpty(params["mission_id"], queryValue(req, "mission_id"))
		if missionID == "" {
			missionID = authorizationBodyValue(req, "mission_id")
		}
		if missionID != "" {
			return identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}
		}
	case "project":
		projectID := firstNonEmpty(params["project_id"], queryValue(req, "project_id"), projectIDFromTenant(queryValue(req, "tenant_id")))
		if projectID == "" {
			projectID = authorizationBodyValue(req, "project_id")
		}
		if projectID != "" {
			return identity.ObjectRef{Type: "project", ID: projectID, TenantID: tenantID}
		}
	case "development_project":
		projectID := firstNonEmpty(params["development_project_id"], queryValue(req, "development_project_id"))
		if projectID == "" {
			projectID = authorizationBodyValue(req, "development_project_id")
		}
		if projectID != "" {
			return identity.ObjectRef{Type: "development_project", ID: projectID, TenantID: tenantID}
		}
		if organizationID := firstNonEmpty(queryValue(req, "organization_id"), authorizationBodyValue(req, "organization_id")); organizationID != "" {
			return identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: tenantID}
		}
	case "scm_connection":
		connectionID := firstNonEmpty(params["connection_id"], queryValue(req, "connection_id"))
		if connectionID == "" {
			connectionID = authorizationBodyValue(req, "connection_id")
		}
		if connectionID != "" {
			return identity.ObjectRef{Type: "scm_connection", ID: connectionID, TenantID: tenantID}
		}
		if organizationID := firstNonEmpty(queryValue(req, "organization_id"), authorizationBodyValue(req, "organization_id")); organizationID != "" {
			return identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: tenantID}
		}
	case "resource", "tool":
		if resourceID := resourceObjectID(req, params); resourceID != "" {
			return identity.ObjectRef{Type: "resource", ID: resourceID, TenantID: tenantID}
		}
	case "discussion":
		discussionID := firstNonEmpty(params["discussion_id"], queryValue(req, "discussion_id"))
		if discussionID == "" {
			discussionID = authorizationBodyValue(req, "discussion_id")
		}
		if discussionID != "" {
			return identity.ObjectRef{Type: "discussion", ID: discussionID, TenantID: tenantID}
		}
		if missionID := firstNonEmpty(params["mission_id"], queryValue(req, "mission_id"), authorizationBodyValue(req, "mission_id")); missionID != "" {
			return identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}
		}
	case "organization":
		if unfilteredOrganizationGraphUsesPlatformScope(req, capability) {
			return identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"}
		}
		if departmentID := firstNonEmpty(params["department_id"], queryValue(req, "department_id")); departmentID != "" {
			return identity.ObjectRef{Type: "department", ID: departmentID, TenantID: tenantID}
		}
		if positionID := firstNonEmpty(params["position_id"], queryValue(req, "position_id")); positionID != "" {
			return identity.ObjectRef{Type: "position", ID: positionID, TenantID: tenantID}
		}
		if departmentID := authorizationBodyValue(req, "department_id"); departmentID != "" {
			return identity.ObjectRef{Type: "department", ID: departmentID, TenantID: tenantID}
		}
		if organizationID := firstNonEmpty(params["organization_id"], queryValue(req, "organization_id"), tenantID); organizationID != "" {
			return identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: organizationID}
		}
		if organizationID := authorizationBodyValue(req, "organization_id"); organizationID != "" {
			return identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: organizationID}
		}
	case "workforce":
		if departmentID := firstNonEmpty(queryValue(req, "department_id"), authorizationBodyValue(req, "department_id")); departmentID != "" {
			return identity.ObjectRef{Type: "department", ID: departmentID, TenantID: tenantID}
		}
		if organizationID := firstNonEmpty(queryValue(req, "organization_id"), authorizationBodyValue(req, "organization_id")); organizationID != "" {
			return identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: firstNonEmpty(tenantID, organizationID)}
		}
		if profileID := params["agent_profile_id"]; profileID != "" {
			return identity.ObjectRef{Type: "workforce", ID: profileID, TenantID: tenantID}
		}
	case "gateway":
		if profileID := authorizationBodyValue(req, "human_profile_id"); profileID != "" {
			return identity.ObjectRef{Type: "workforce", ID: profileID, TenantID: tenantID}
		}
	case "roles":
		if assignmentID := params["assignment_id"]; assignmentID != "" {
			return identity.ObjectRef{Type: "role_assignment", ID: assignmentID, TenantID: tenantID}
		}
	}
	return identity.ObjectRef{
		Type:     "platform",
		ID:       "global",
		TenantID: "global",
	}
}

func authorizationTenantID(req *http.Request) string {
	if req == nil {
		return "global"
	}
	return strings.TrimSpace(firstNonEmpty(req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant"), queryValue(req, "tenant_id"), "global"))
}

func resourceObjectID(req *http.Request, params map[string]string) string {
	resourceID := firstNonEmpty(
		params["tool_id"],
		params["skill_id"],
		params["resource_id"],
		params["memory_profile_id"],
		queryValue(req, "resource_id"),
		queryValue(req, "tool_id"),
		queryValue(req, "skill_id"),
		queryValue(req, "memory_profile_id"),
	)
	if resourceID != "" {
		return resourceID
	}
	return authorizationBodyValue(req, "resource_id", "tool_id", "skill_id", "memory_profile_id")
}

func queryValue(req *http.Request, key string) string {
	if req == nil || req.URL == nil {
		return ""
	}
	return strings.TrimSpace(req.URL.Query().Get(key))
}

func projectIDFromTenant(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if !strings.HasPrefix(tenantID, "project:") {
		return ""
	}
	return strings.TrimPrefix(tenantID, "project:")
}

func authorizationBodyValue(req *http.Request, keys ...string) string {
	if req == nil || req.Body == nil || req.ContentLength <= 0 || req.ContentLength > maxAuthorizationBodyPeekBytes {
		return ""
	}
	body, err := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) == 0 {
		return ""
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return ""
	}
	for _, key := range keys {
		if value := stringFromJSONValue(payload[key]); value != "" {
			return value
		}
	}
	return ""
}

func authorizationBodyJSON(req *http.Request, target any) bool {
	if req == nil || req.Body == nil || target == nil || req.ContentLength <= 0 || req.ContentLength > maxAuthorizationBodyPeekBytes {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxAuthorizationBodyPeekBytes+1))
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) == 0 || len(body) > maxAuthorizationBodyPeekBytes {
		return false
	}
	return json.Unmarshal(body, target) == nil
}

func stringFromJSONValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func (r *Router) permissionForRequest(req *http.Request) (PermissionCapabilityDTO, bool) {
	capabilities := r.permissionsForRequest(req)
	if len(capabilities) == 0 {
		return PermissionCapabilityDTO{}, false
	}
	return capabilities[0], true
}

func (r *Router) permissionsForRequest(req *http.Request) []PermissionCapabilityDTO {
	if r == nil || req == nil {
		return nil
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	exactMatches := make([]PermissionCapabilityDTO, 0, 2)
	matches := make([]PermissionCapabilityDTO, 0, 2)
	for _, capability := range r.permissions {
		if capability.Method != method {
			continue
		}
		if exactPathMatches(capability.Path, req.URL.Path) {
			exactMatches = append(exactMatches, capability)
			continue
		}
		if pathTemplateMatches(capability.Path, req.URL.Path) {
			matches = append(matches, capability)
		}
	}
	if len(exactMatches) > 0 {
		return exactMatches
	}
	return matches
}

func exactPathMatches(template, path string) bool {
	return strings.Trim(template, "/") == strings.Trim(path, "/")
}

func pathTemplateMatches(template, path string) bool {
	_, ok := pathTemplateValues(template, path)
	return ok
}

func pathTemplateValues(template, path string) (map[string]string, bool) {
	templateParts := splitPathSegments(template)
	pathParts := splitPathSegments(path)
	if len(templateParts) != len(pathParts) {
		return nil, false
	}
	values := make(map[string]string)
	for i := range templateParts {
		if strings.HasPrefix(templateParts[i], "{") && strings.HasSuffix(templateParts[i], "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(templateParts[i], "{"), "}")
			if name != "" {
				values[name] = pathParts[i]
			}
			continue
		}
		if templateParts[i] != pathParts[i] {
			return nil, false
		}
	}
	return values, true
}

func splitPathSegments(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func accessMode(method, action string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	action = strings.ToLower(strings.TrimSpace(action))
	if method == http.MethodGet && action != "manage" && action != "write" && action != "create" && action != "delete" && action != "update" {
		return "read"
	}
	return "write"
}

func permissionCategoryZh(domain string) string {
	switch strings.TrimSpace(domain) {
	case "mission":
		return "任务"
	case "project":
		return "项目"
	case "approval":
		return "审批"
	case "resource":
		return "资源"
	case "tool":
		return "工具"
	case "memory":
		return "记忆"
	case "operations":
		return "运营"
	case "workforce":
		return "人才"
	case "organization":
		return "组织"
	case "roles":
		return "角色"
	case "permissions":
		return "权限"
	case "audit":
		return "审计"
	case "authorization":
		return "授权"
	case "identity":
		return "身份"
	case "knowledge":
		return "知识"
	case "agent":
		return "Agent"
	case "ontology_scope":
		return "本体范围"
	case "ontology_schema":
		return "本体模式"
	case "ontology_context":
		return "本体知识成果"
	case "ontology_lineage":
		return "本体血缘"
	case "ontology_governance":
		return "本体治理"
	case "ontology_projection":
		return "本体投影"
	case "settings":
		return "设置"
	case "discussion":
		return "聊天"
	case "scheduler":
		return "自动化"
	case "todo":
		return "待办"
	case "idea":
		return "想法"
	default:
		return domain
	}
}

func permissionCategoryEn(domain string) string {
	switch strings.TrimSpace(domain) {
	case "mission":
		return "Mission"
	case "project":
		return "Project"
	case "approval":
		return "Approval"
	case "resource":
		return "Resource"
	case "tool":
		return "Tool"
	case "memory":
		return "Memory"
	case "operations":
		return "Operations"
	case "workforce":
		return "Workforce"
	case "organization":
		return "Organization"
	case "roles":
		return "Roles"
	case "permissions":
		return "Permissions"
	case "audit":
		return "Audit"
	case "authorization":
		return "Authorization"
	case "identity":
		return "Identity"
	case "knowledge":
		return "Knowledge"
	case "agent":
		return "Agent"
	case "ontology_scope":
		return "Ontology scope"
	case "ontology_schema":
		return "Ontology schema"
	case "ontology_context":
		return "Ontology context"
	case "ontology_lineage":
		return "Ontology lineage"
	case "ontology_governance":
		return "Ontology governance"
	case "ontology_projection":
		return "Ontology projection"
	case "settings":
		return "Settings"
	case "discussion":
		return "Chat"
	case "scheduler":
		return "Scheduler"
	case "todo":
		return "Todo"
	case "idea":
		return "Ideas"
	default:
		return domain
	}
}

func permissionLabelZh(domain, action, description string) string {
	if label := permissionDescriptionLabelZh(description); label != "" {
		return label
	}
	category := permissionCategoryZh(domain)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "read", "list":
		return category + "查看"
	case "decide":
		return category + "处理"
	case "write", "manage", "create", "update", "delete":
		return category + "编辑"
	default:
		if strings.TrimSpace(description) != "" {
			return category + " " + strings.TrimSpace(action)
		}
		return category + "权限"
	}
}

func permissionDescriptionLabelZh(description string) string {
	switch strings.ToLower(strings.TrimSpace(description)) {
	case "list approvals":
		return "审批列表"
	case "decide approvals":
		return "审批处理"
	case "decide discussion runtime approvals":
		return "聊天审批处理"
	case "list todo items":
		return "待办列表"
	case "suggest todo or idea title and tags":
		return "建议待办/想法标题和标签"
	case "create todo item":
		return "新建待办"
	case "read todo item":
		return "查看待办"
	case "update todo item":
		return "编辑待办"
	case "delete todo item":
		return "删除待办"
	case "list todo comments":
		return "查看待办评论"
	case "create todo comment":
		return "新建待办评论"
	case "claim todo item":
		return "认领待办"
	case "move todo item":
		return "移动待办"
	case "complete todo item":
		return "完成待办"
	case "cancel todo item":
		return "取消待办"
	case "reopen todo item":
		return "重新打开待办"
	case "list idea records":
		return "想法列表"
	case "create idea record":
		return "新建想法"
	case "read idea record":
		return "查看想法"
	case "update idea record":
		return "编辑想法"
	case "delete idea record":
		return "删除想法"
	case "list idea comments":
		return "查看想法评论"
	case "create idea comment":
		return "新建想法评论"
	case "archive idea record":
		return "归档想法"
	case "restore idea record":
		return "恢复想法"
	case "convert idea to todo":
		return "转为待办"
	default:
		return ""
	}
}

func permissionCapabilitiesForAccess(capabilities []PermissionCapabilityDTO) []accesscontrol.CapabilityDTO {
	out := make([]accesscontrol.CapabilityDTO, 0, len(capabilities))
	for _, capability := range capabilities {
		out = append(out, accesscontrol.CapabilityDTO{
			CapabilityID: capability.CapabilityID,
			Domain:       capability.Domain,
			Action:       capability.Action,
			AccessMode:   capability.AccessMode,
			Scope:        capability.Scope,
			Method:       capability.Method,
			Path:         capability.Path,
			LabelZh:      capability.LabelZh,
			LabelEn:      capability.LabelEn,
			CategoryZh:   capability.CategoryZh,
			CategoryEn:   capability.CategoryEn,
			Description:  capability.Description,
		})
	}
	return out
}
