package todo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

const todoExecutionPlanVersion = 3

type todoExecutionPlanProposal struct {
	Version              int                        `json:"version"`
	Summary              string                     `json:"summary"`
	Steps                []TodoExecutionPlanStep    `json:"steps"`
	RiskSummary          []string                   `json:"risk_summary"`
	LegacyRisks          []string                   `json:"risks"`
	AcceptanceChecks     []string                   `json:"acceptance_checks"`
	RequiredApprovals    []string                   `json:"required_approvals"`
	RepositoryBindingIDs []string                   `json:"repository_binding_ids"`
	AllowedFileScopes    []string                   `json:"allowed_file_scopes"`
	PlannedCommands      []string                   `json:"planned_commands"`
	NetworkPolicy        TodoExecutionNetworkPolicy `json:"network_policy"`
	RequiredSecretRefs   []string                   `json:"required_secret_refs"`
}

func (s *Service) resolveTodoDevelopmentPlanContext(ctx context.Context, item TodoItem) (TodoDevelopmentExecutionPlanContext, error) {
	if item.WorkKind != TodoWorkKindDevelopment {
		return TodoDevelopmentExecutionPlanContext{AllowedNetworkMode: "none"}, nil
	}
	if strings.TrimSpace(item.DevelopmentProjectID) == "" {
		return TodoDevelopmentExecutionPlanContext{}, fmt.Errorf("%w: development Todo requires a development project before planning", ErrInvalidRequest)
	}
	if s == nil || s.developmentExecution == nil {
		return TodoDevelopmentExecutionPlanContext{}, ErrServiceDependency
	}
	planContext, err := s.developmentExecution.ResolveTodoExecutionPlanContext(ctx, TodoDevelopmentExecutionPlanContextRequest{
		TenantID: item.TenantID, DevelopmentProjectID: item.DevelopmentProjectID,
	})
	if err != nil {
		return TodoDevelopmentExecutionPlanContext{}, err
	}
	if strings.TrimSpace(planContext.DevelopmentProjectID) != strings.TrimSpace(item.DevelopmentProjectID) ||
		!strings.HasPrefix(strings.TrimSpace(planContext.ResourceSnapshotDigest), "sha256:") || len(planContext.Repositories) == 0 {
		return TodoDevelopmentExecutionPlanContext{}, fmt.Errorf("%w: development execution plan context is incomplete", ErrServiceDependency)
	}
	return planContext, nil
}

func todoPlanningPrompt(item TodoItem, planContext TodoDevelopmentExecutionPlanContext) string {
	contextBlock := "本 Todo 不涉及代码仓库；计划中的仓库、文件、命令、网络和 Secret 范围必须为空。"
	if item.WorkKind == TodoWorkKindDevelopment {
		type promptRepository struct {
			BindingID          string   `json:"binding_id"`
			DisplayName        string   `json:"display_name"`
			Role               string   `json:"role"`
			AccessMode         string   `json:"access_mode"`
			DefaultBranch      string   `json:"default_branch"`
			SourceSubdirectory string   `json:"source_subdirectory,omitempty"`
			BaseCommit         string   `json:"base_commit"`
			SuggestedCommands  []string `json:"suggested_commands"`
		}
		promptRepositories := make([]promptRepository, 0, len(planContext.Repositories))
		for _, repository := range planContext.Repositories {
			commands := append([]string(nil), repository.BuildCommands...)
			commands = append(commands, repository.TestCommands...)
			promptRepositories = append(promptRepositories, promptRepository{
				BindingID: repository.BindingID, DisplayName: repository.DisplayName, Role: repository.Role,
				AccessMode: repository.AccessMode, DefaultBranch: repository.DefaultBranch,
				SourceSubdirectory: repository.SourceSubdirectory, BaseCommit: repository.BaseCommit,
				SuggestedCommands: normalizeTags(commands),
			})
		}
		payload, _ := json.Marshal(struct {
			DevelopmentProjectID    string             `json:"development_project_id"`
			Repositories            []promptRepository `json:"repositories"`
			AllowedFileScopes       []string           `json:"allowed_file_scopes"`
			AllowedNetworkMode      string             `json:"allowed_network_mode"`
			AllowedNetworkHosts     []string           `json:"allowed_network_hosts"`
			AllowedSecretRefDigests []string           `json:"allowed_secret_ref_digests"`
		}{
			DevelopmentProjectID: item.DevelopmentProjectID, Repositories: promptRepositories,
			AllowedFileScopes:       append([]string(nil), planContext.AllowedFileScopes...),
			AllowedNetworkMode:      planContext.AllowedNetworkMode,
			AllowedNetworkHosts:     append([]string(nil), planContext.AllowedNetworkHosts...),
			AllowedSecretRefDigests: append([]string(nil), planContext.AllowedSecretRefDigests...),
		})
		contextBlock = "以下是服务端已验证的本次计划边界。只选择实际需要的范围，不得扩张；Secret 只能引用不可逆摘要标识，不能输出真实引用或值：\n" + string(payload)
	}

	return strings.TrimSpace(fmt.Sprintf(`你们正在 Todo 自动评估群聊中。先讨论，不要执行任何会改变外部状态、文件、Mission 或 Todo 的操作。

请判断这个 Todo 现在是否具备推进条件，并由多位 Agent 交叉检查方案、依赖、风险、验收标准和所需权限。

Todo: %s
标题: %s
状态: %s
优先级: %s
描述: %s
验收标准: %s
阻塞原因: %s

%s

如果当前不可推进，最终结论必须以 TODO_AUTOMATION_NOT_READY 开头，并明确缺失条件。
如果可以推进，最终结论必须以 TODO_AUTOMATION_PROPOSAL 开头，随后提供一个 json 代码块。JSON 必须严格符合：
{"version":3,"summary":"方案摘要","repository_binding_ids":["实际需要的 binding_id"],"allowed_file_scopes":["实际需要的相对路径"],"planned_commands":["确切命令"],"network_policy":{"mode":"none","allowed_hosts":[]},"required_secret_refs":[],"risk_summary":["风险"],"acceptance_checks":["整体可核验条件"],"steps":[{"step_id":"step-1","title":"步骤标题","description":"工作说明","assignee_agent_id":"Agent Profile ID","acceptance_criteria":["可核验条件"],"evidence_requirements":[{"evidence_requirement_id":"evidence-step-1-1","acceptance_criterion":"与本步骤某条 acceptance_criteria 完全一致","kind":"artifact|tool_log|test_log|external_url","required":true}],"depends_on":[]}],"required_approvals":["审批要求"]}
steps 应列出明确步骤；如果当前只能确认整体目标，可以返回空数组，系统会创建一个“完成 Todo：<title>”根任务供协调 Agent 后续拆分。非空时 step_id 必须唯一，depends_on 只能引用同一方案中的 step_id。每条验收条件至少绑定一项必需证据；外部副作用必须使用真实 tool_log 或 external_url，不能用文字总结代替。开发计划必须选择一个可写 primary；综合计划的执行边界字段必须为空。审批前不得开始执行。`,
		item.TodoID, item.Title, item.Status, item.Priority, firstNonEmptyText(item.Description, "未提供"),
		firstNonEmptyText(item.AcceptanceCriteria, "未提供"), firstNonEmptyText(item.BlockedReason, "无"), contextBlock))
}

func todoExecutionPlanFromProposal(
	proposal string,
	item TodoItem,
	state TodoAutomationState,
	proposalRevision int,
	planContext TodoDevelopmentExecutionPlanContext,
	defaultAgents []string,
) (TodoExecutionPlan, error) {
	var draft todoExecutionPlanProposal
	parsed := false
	for _, candidate := range jsonObjectCandidates(proposal) {
		var current todoExecutionPlanProposal
		if err := json.Unmarshal([]byte(candidate), &current); err == nil && current.Version != 0 {
			draft = current
			parsed = true
			break
		}
	}
	if !parsed {
		return TodoExecutionPlan{}, fmt.Errorf("%w: automation proposal did not contain a structured execution plan", ErrInvalidRequest)
	}
	if draft.Version != todoExecutionPlanVersion {
		return TodoExecutionPlan{}, fmt.Errorf("%w: automation proposal execution plan version must be %d", ErrInvalidRequest, todoExecutionPlanVersion)
	}
	if proposalRevision <= 0 {
		return TodoExecutionPlan{}, fmt.Errorf("%w: proposal revision is required", ErrInvalidRequest)
	}
	if len(draft.Steps) == 0 {
		draft.Steps = []TodoExecutionPlanStep{rootTodoExecutionPlanStep(item, defaultAgents)}
	}

	plan := TodoExecutionPlan{
		TenantID: item.TenantID, Version: todoExecutionPlanVersion, PlanID: state.ProposalID,
		TodoID: item.TodoID, TodoVersion: item.Version, ProposalRevision: proposalRevision,
		ConversationID: state.ConversationID, DevelopmentProjectID: item.DevelopmentProjectID,
		Summary: strings.TrimSpace(draft.Summary), Steps: append([]TodoExecutionPlanStep(nil), draft.Steps...),
		RiskSummary:       normalizeTags(append(append([]string(nil), draft.RiskSummary...), draft.LegacyRisks...)),
		RequiredApprovals: normalizeTags(draft.RequiredApprovals), Status: "proposed",
		CreatedAt: state.CreatedAt.UTC(), UpdatedAt: state.UpdatedAt.UTC(),
		RepositoryScopes: []TodoExecutionRepositoryScope{}, BaseCommits: []TodoExecutionBaseCommit{},
		AllowedFileScopes: []string{}, PlannedCommands: []string{}, RequiredSecretRefs: []string{},
		AcceptanceChecks: []string{}, NetworkPolicy: TodoExecutionNetworkPolicy{Mode: "none", AllowedHosts: []string{}},
	}
	if plan.PlanID == "" || plan.TodoID == "" || plan.TodoVersion <= 0 || plan.ConversationID == "" || plan.Summary == "" || plan.CreatedAt.IsZero() || plan.UpdatedAt.IsZero() {
		return TodoExecutionPlan{}, fmt.Errorf("%w: execution plan identity and summary are required", ErrInvalidRequest)
	}
	if err := normalizeTodoExecutionPlanSteps(&plan, item, defaultAgents); err != nil {
		return TodoExecutionPlan{}, err
	}
	plan.AcceptanceChecks = normalizeTags(draft.AcceptanceChecks)
	if len(plan.AcceptanceChecks) == 0 {
		for _, step := range plan.Steps {
			plan.AcceptanceChecks = append(plan.AcceptanceChecks, step.AcceptanceCriteria...)
		}
		plan.AcceptanceChecks = normalizeTags(plan.AcceptanceChecks)
	}
	if acceptance := strings.TrimSpace(item.AcceptanceCriteria); acceptance != "" {
		plan.AcceptanceChecks = normalizeTags(append(plan.AcceptanceChecks, acceptance))
	}
	if len(plan.AcceptanceChecks) == 0 {
		return TodoExecutionPlan{}, fmt.Errorf("%w: execution plan acceptance checks are required", ErrInvalidRequest)
	}

	if item.WorkKind == TodoWorkKindDevelopment {
		if err := bindDevelopmentExecutionPlan(&plan, draft, planContext); err != nil {
			return TodoExecutionPlan{}, err
		}
		plan.RequiredApprovals = normalizeTags(append(plan.RequiredApprovals, "Development project approver confirmation"))
	} else if len(draft.RepositoryBindingIDs) > 0 || len(draft.AllowedFileScopes) > 0 || len(draft.PlannedCommands) > 0 ||
		len(draft.RequiredSecretRefs) > 0 || strings.TrimSpace(draft.NetworkPolicy.Mode) != "" && !strings.EqualFold(strings.TrimSpace(draft.NetworkPolicy.Mode), "none") || len(draft.NetworkPolicy.AllowedHosts) > 0 {
		return TodoExecutionPlan{}, fmt.Errorf("%w: general Todo execution plan cannot request development resources", ErrInvalidRequest)
	}
	plan.RequiredApprovals = normalizeTags(append(plan.RequiredApprovals, "Todo execution plan approval"))
	return plan, nil
}

func rootTodoExecutionPlanStep(item TodoItem, defaultAgents []string) TodoExecutionPlanStep {
	title := "完成 Todo：" + strings.TrimSpace(item.Title)
	criterion := strings.TrimSpace(item.AcceptanceCriteria)
	if criterion == "" {
		criterion = title + "，且结果已经过可核验证据确认"
	}
	agentID := ""
	if agents := normalizeTags(defaultAgents); len(agents) > 0 {
		agentID = agents[0]
	}
	return TodoExecutionPlanStep{
		StepID: "root", Title: title, Description: "完成该 Todo，并在 Mission 内按需拆分后续工作。", AssigneeAgentID: agentID,
		AcceptanceCriteria: []string{criterion}, EvidenceRequirements: []TodoExecutionEvidenceRequirement{{
			EvidenceRequirementID: "evidence-root-1", AcceptanceCriterion: criterion, Kind: "artifact", Required: true,
		}},
	}
}

func normalizeTodoExecutionPlanSteps(plan *TodoExecutionPlan, item TodoItem, defaultAgents []string) error {
	defaultAgent := ""
	allowedAgents := make(map[string]string, len(defaultAgents))
	for _, agentID := range normalizeTags(defaultAgents) {
		allowedAgents[strings.ToLower(agentID)] = agentID
		if defaultAgent == "" {
			defaultAgent = agentID
		}
	}
	stepIDs := map[string]bool{}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		step.StepID = strings.TrimSpace(step.StepID)
		step.Title = strings.TrimSpace(step.Title)
		step.Description = strings.TrimSpace(step.Description)
		step.AssigneeAgentID = firstNonEmptyText(step.AssigneeAgentID, defaultAgent)
		canonicalAgentID, allowed := allowedAgents[strings.ToLower(strings.TrimSpace(step.AssigneeAgentID))]
		if !allowed {
			return fmt.Errorf("%w: execution plan assignee %q is not in the allowed Agent set", ErrInvalidRequest, step.AssigneeAgentID)
		}
		step.AssigneeAgentID = canonicalAgentID
		step.AcceptanceCriteria = normalizeTags(step.AcceptanceCriteria)
		step.DependsOn = normalizeTags(step.DependsOn)
		if step.StepID == "" || step.Title == "" || stepIDs[step.StepID] {
			return fmt.Errorf("%w: execution plan step ids and titles must be unique and non-empty", ErrInvalidRequest)
		}
		if len(step.AcceptanceCriteria) == 0 {
			criterion := strings.TrimSpace(item.AcceptanceCriteria)
			if criterion == "" {
				return fmt.Errorf("%w: every execution plan step requires acceptance criteria", ErrInvalidRequest)
			}
			step.AcceptanceCriteria = []string{criterion}
		}
		stepIDs[step.StepID] = true
	}
	for _, step := range plan.Steps {
		for _, dependency := range step.DependsOn {
			if dependency == step.StepID || !stepIDs[dependency] {
				return fmt.Errorf("%w: execution plan contains an invalid dependency", ErrInvalidRequest)
			}
		}
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	dependencies := make(map[string][]string, len(plan.Steps))
	for _, step := range plan.Steps {
		dependencies[step.StepID] = step.DependsOn
	}
	var visit func(string) bool
	visit = func(stepID string) bool {
		if visiting[stepID] {
			return false
		}
		if visited[stepID] {
			return true
		}
		visiting[stepID] = true
		for _, dependency := range dependencies[stepID] {
			if !visit(dependency) {
				return false
			}
		}
		visiting[stepID] = false
		visited[stepID] = true
		return true
	}
	for stepID := range dependencies {
		if !visit(stepID) {
			return fmt.Errorf("%w: execution plan dependencies must form a directed acyclic graph", ErrInvalidRequest)
		}
	}
	if err := normalizeTodoExecutionEvidenceRequirements(plan); err != nil {
		return err
	}
	return nil
}

func normalizeTodoExecutionEvidenceRequirements(plan *TodoExecutionPlan) error {
	if plan == nil {
		return fmt.Errorf("%w: execution plan is required", ErrInvalidRequest)
	}
	seenIDs := map[string]bool{}
	for stepIndex := range plan.Steps {
		step := &plan.Steps[stepIndex]
		criteria := make(map[string]string, len(step.AcceptanceCriteria))
		for _, criterion := range step.AcceptanceCriteria {
			criteria[strings.ToLower(strings.Join(strings.Fields(criterion), " "))] = criterion
		}
		covered := map[string]bool{}
		for evidenceIndex := range step.EvidenceRequirements {
			requirement := &step.EvidenceRequirements[evidenceIndex]
			requirement.EvidenceRequirementID = strings.TrimSpace(requirement.EvidenceRequirementID)
			requirement.AcceptanceCriterion = strings.Join(strings.Fields(requirement.AcceptanceCriterion), " ")
			requirement.Kind = strings.ToLower(strings.TrimSpace(requirement.Kind))
			criterionKey := strings.ToLower(requirement.AcceptanceCriterion)
			canonicalCriterion, criterionExists := criteria[criterionKey]
			if requirement.EvidenceRequirementID == "" || seenIDs[requirement.EvidenceRequirementID] {
				return fmt.Errorf("%w: execution plan evidence requirement ids must be unique and non-empty", ErrInvalidRequest)
			}
			if !criterionExists {
				return fmt.Errorf("%w: evidence requirement %s must reference an exact step acceptance criterion", ErrInvalidRequest, requirement.EvidenceRequirementID)
			}
			switch requirement.Kind {
			case "artifact", "tool_log", "test_log", "external_url":
			default:
				return fmt.Errorf("%w: evidence requirement %s has unsupported kind %q", ErrInvalidRequest, requirement.EvidenceRequirementID, requirement.Kind)
			}
			if !requirement.Required {
				return fmt.Errorf("%w: every execution plan evidence requirement must be required", ErrInvalidRequest)
			}
			requirement.AcceptanceCriterion = canonicalCriterion
			seenIDs[requirement.EvidenceRequirementID] = true
			covered[criterionKey] = true
		}
		for criterionKey := range criteria {
			if !covered[criterionKey] {
				return fmt.Errorf("%w: every execution plan acceptance criterion requires explicit evidence", ErrInvalidRequest)
			}
		}
	}
	return nil
}

func bindDevelopmentExecutionPlan(plan *TodoExecutionPlan, draft todoExecutionPlanProposal, planContext TodoDevelopmentExecutionPlanContext) error {
	if strings.TrimSpace(planContext.ResourceSnapshotDigest) == "" || strings.TrimSpace(planContext.DevelopmentProjectID) != plan.DevelopmentProjectID {
		return fmt.Errorf("%w: development execution resource snapshot is missing", ErrInvalidRequest)
	}
	plan.ResourceSnapshotDigest = strings.TrimSpace(planContext.ResourceSnapshotDigest)
	repositories := make(map[string]TodoDevelopmentPlanRepository, len(planContext.Repositories))
	defaultBindingIDs := make([]string, 0, 1)
	for _, repository := range planContext.Repositories {
		bindingID := strings.TrimSpace(repository.BindingID)
		if bindingID == "" || strings.TrimSpace(repository.BaseCommit) == "" {
			return fmt.Errorf("%w: development repository snapshot is incomplete", ErrServiceDependency)
		}
		repositories[bindingID] = repository
		if repository.Role == "primary" && repository.AccessMode == "read_write" {
			defaultBindingIDs = append(defaultBindingIDs, bindingID)
		}
	}
	selectedBindingIDs := normalizeTags(draft.RepositoryBindingIDs)
	if len(selectedBindingIDs) == 0 {
		selectedBindingIDs = defaultBindingIDs
	}
	if len(defaultBindingIDs) != 1 {
		return fmt.Errorf("%w: development plan requires exactly one verified writable primary repository", ErrInvalidRequest)
	}
	selectedPrimary := false
	configuredCommands := map[string]bool{}
	for _, bindingID := range selectedBindingIDs {
		repository, ok := repositories[bindingID]
		if !ok {
			return fmt.Errorf("%w: execution plan selected an unavailable repository binding", ErrInvalidRequest)
		}
		if repository.Role == "primary" && repository.AccessMode == "read_write" {
			selectedPrimary = true
		}
		plan.RepositoryScopes = append(plan.RepositoryScopes, TodoExecutionRepositoryScope{
			BindingID: bindingID, DisplayName: strings.TrimSpace(repository.DisplayName), Role: repository.Role,
			AccessMode: repository.AccessMode, DefaultBranch: repository.DefaultBranch,
			SourceSubdirectory: repository.SourceSubdirectory,
		})
		plan.BaseCommits = append(plan.BaseCommits, TodoExecutionBaseCommit{BindingID: bindingID, CommitSHA: strings.TrimSpace(repository.BaseCommit)})
		for _, command := range append(append([]string(nil), repository.BuildCommands...), repository.TestCommands...) {
			if command = strings.TrimSpace(command); command != "" {
				configuredCommands[strings.ToLower(command)] = true
			}
		}
	}
	if !selectedPrimary {
		return fmt.Errorf("%w: execution plan must include the writable primary repository", ErrInvalidRequest)
	}

	plan.AllowedFileScopes = normalizePlanFileScopes(draft.AllowedFileScopes)
	if len(plan.AllowedFileScopes) == 0 {
		plan.AllowedFileScopes = defaultPlanFileScopes(plan.RepositoryScopes, planContext.AllowedFileScopes)
	}
	if len(plan.AllowedFileScopes) == 0 {
		return fmt.Errorf("%w: development plan requires an explicit allowed file scope", ErrInvalidRequest)
	}
	for _, scope := range plan.AllowedFileScopes {
		if !fileScopeAllowed(scope, planContext.AllowedFileScopes) {
			return fmt.Errorf("%w: execution plan file scope %q exceeds project policy", ErrInvalidRequest, scope)
		}
	}

	plan.PlannedCommands = normalizeTags(draft.PlannedCommands)
	if len(plan.PlannedCommands) == 0 {
		for _, repository := range planContext.Repositories {
			if !containsStringFold(selectedBindingIDs, repository.BindingID) {
				continue
			}
			plan.PlannedCommands = append(plan.PlannedCommands, repository.BuildCommands...)
			plan.PlannedCommands = append(plan.PlannedCommands, repository.TestCommands...)
		}
		plan.PlannedCommands = normalizeTags(plan.PlannedCommands)
	}
	if len(plan.PlannedCommands) == 0 {
		return fmt.Errorf("%w: development plan requires at least one exact validation command", ErrInvalidRequest)
	}
	for _, command := range plan.PlannedCommands {
		if !executionCommandAllowed(command, planContext.AllowedCommandPrefixes, configuredCommands) {
			return fmt.Errorf("%w: planned command %q exceeds project policy", ErrInvalidRequest, command)
		}
	}

	networkPolicy, err := normalizeRequestedNetworkPolicy(draft.NetworkPolicy, planContext)
	if err != nil {
		return err
	}
	plan.NetworkPolicy = networkPolicy
	plan.RequiredSecretRefs = normalizeTags(draft.RequiredSecretRefs)
	for _, reference := range plan.RequiredSecretRefs {
		if !strings.HasPrefix(reference, "sha256:") || !containsStringFold(planContext.AllowedSecretRefDigests, reference) {
			return fmt.Errorf("%w: requested Secret reference is outside the project allowlist", ErrInvalidRequest)
		}
	}
	return nil
}

func todoExecutionPlanBaseCommit(plan TodoExecutionPlan, bindingID string) string {
	for _, base := range plan.BaseCommits {
		if strings.TrimSpace(base.BindingID) == strings.TrimSpace(bindingID) {
			return strings.TrimSpace(base.CommitSHA)
		}
	}
	return ""
}

func todoExecutionPlanPrimaryBindingID(plan TodoExecutionPlan) string {
	for _, repository := range plan.RepositoryScopes {
		if repository.Role == "primary" && repository.AccessMode == "read_write" {
			return strings.TrimSpace(repository.BindingID)
		}
	}
	return ""
}

func normalizePlanFileScopes(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = path.Clean(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
		if value != "" && value != "." && value != ".." && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "../") && !strings.Contains(value, ":") {
			out = append(out, value)
		}
	}
	return normalizeTags(out)
}

func defaultPlanFileScopes(repositories []TodoExecutionRepositoryScope, allowed []string) []string {
	for _, repository := range repositories {
		if repository.Role != "primary" || repository.AccessMode != "read_write" {
			continue
		}
		source := path.Clean(strings.ReplaceAll(strings.TrimSpace(repository.SourceSubdirectory), "\\", "/"))
		if source != "" && source != "." && fileScopeAllowed(source, allowed) {
			return []string{source}
		}
	}
	allowed = normalizePlanFileScopes(allowed)
	if len(allowed) == 1 {
		return allowed
	}
	return nil
}

func fileScopeAllowed(requested string, allowed []string) bool {
	requested = path.Clean(strings.ReplaceAll(strings.TrimSpace(requested), "\\", "/"))
	for _, candidate := range normalizePlanFileScopes(allowed) {
		if requested == candidate || strings.HasPrefix(requested, candidate+"/") {
			return true
		}
	}
	return false
}

func executionCommandAllowed(command string, prefixes []string, configured map[string]bool) bool {
	command = strings.TrimSpace(command)
	if command == "" || unsafeExecutionCommand(command) {
		return false
	}
	if configured[strings.ToLower(command)] {
		return true
	}
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if strings.EqualFold(command, prefix) || len(command) > len(prefix) && strings.EqualFold(command[:len(prefix)], prefix) && command[len(prefix)] == ' ' {
			return true
		}
	}
	return false
}

func unsafeExecutionCommand(command string) bool {
	if strings.ContainsAny(command, "\x00\r\n;&|<>`$%^!") {
		return true
	}
	for _, token := range strings.Fields(strings.ToLower(command)) {
		flag := token
		if index := strings.IndexByte(flag, '='); index >= 0 {
			flag = flag[:index]
		}
		switch flag {
		case "-c", "/c", "-command", "-encodedcommand", "-exec", "--exec", "--eval", "--script-shell", "--require", "--import", "--loader":
			return true
		}
	}
	return false
}

func normalizeRequestedNetworkPolicy(requested TodoExecutionNetworkPolicy, planContext TodoDevelopmentExecutionPlanContext) (TodoExecutionNetworkPolicy, error) {
	requested.Mode = strings.ToLower(strings.TrimSpace(requested.Mode))
	if requested.Mode == "" {
		requested.Mode = "none"
	}
	requested.AllowedHosts = normalizeTags(requested.AllowedHosts)
	allowedMode := strings.ToLower(strings.TrimSpace(planContext.AllowedNetworkMode))
	if allowedMode == "" {
		allowedMode = "none"
	}
	valid := requested.Mode == "none" || requested.Mode == "restricted" && (allowedMode == "restricted" || allowedMode == "allowed") || requested.Mode == "allowed" && allowedMode == "allowed"
	if !valid {
		return TodoExecutionNetworkPolicy{}, fmt.Errorf("%w: requested network mode exceeds project policy", ErrInvalidRequest)
	}
	if requested.Mode == "none" {
		requested.AllowedHosts = []string{}
		return requested, nil
	}
	if requested.Mode == "restricted" && len(requested.AllowedHosts) == 0 {
		return TodoExecutionNetworkPolicy{}, fmt.Errorf("%w: restricted network plan requires exact hosts", ErrInvalidRequest)
	}
	if allowedMode != "allowed" || requested.Mode == "restricted" {
		for _, host := range requested.AllowedHosts {
			if !containsStringFold(planContext.AllowedNetworkHosts, host) {
				return TodoExecutionNetworkPolicy{}, fmt.Errorf("%w: requested network host exceeds project policy", ErrInvalidRequest)
			}
		}
	}
	return requested, nil
}

func (s *Service) todoExecutionPlanIsCurrent(ctx context.Context, item TodoItem, state TodoAutomationState) (bool, error) {
	plan := state.Plan
	if plan.TenantID == "" {
		plan.TenantID = state.TenantID
	}
	if plan.Version != todoExecutionPlanVersion || plan.PlanID != state.ProposalID || plan.TodoID != item.TodoID ||
		plan.TodoVersion != item.Version || plan.ProposalRevision <= 0 || plan.ConversationID != state.ConversationID ||
		plan.Status != "proposed" || plan.CreatedAt.IsZero() || plan.UpdatedAt.IsZero() {
		return false, nil
	}
	digest, err := todoExecutionPlanDigest(plan)
	if err != nil {
		return false, err
	}
	if digest != state.PlanDigest {
		return false, nil
	}
	if item.WorkKind != TodoWorkKindDevelopment {
		return plan.DevelopmentProjectID == "" && plan.ResourceSnapshotDigest == "" && len(plan.RepositoryScopes) == 0 &&
			len(plan.BaseCommits) == 0 && len(plan.AllowedFileScopes) == 0 && len(plan.PlannedCommands) == 0 &&
			len(plan.RequiredSecretRefs) == 0 && plan.NetworkPolicy.Mode == "none" && len(plan.NetworkPolicy.AllowedHosts) == 0, nil
	}
	planContext, err := s.resolveTodoDevelopmentPlanContext(ctx, item)
	if err != nil {
		return false, err
	}
	return plan.DevelopmentProjectID == item.DevelopmentProjectID && plan.ResourceSnapshotDigest == planContext.ResourceSnapshotDigest, nil
}

func todoDevelopmentAuthorization(item TodoItem, viewer ViewerRef, agentIDs []string) TodoDevelopmentExecutionAuthorization {
	return TodoDevelopmentExecutionAuthorization{
		TenantID: item.TenantID, DevelopmentProjectID: item.DevelopmentProjectID,
		RequesterID: viewer.ViewerID, RequesterKind: viewer.ViewerKind, RequesterSubjectID: viewer.AuthorizationSubjectID,
		RequesterScopes: append([]string(nil), viewer.PermissionScope...), AssignedAgentProfileID: append([]string(nil), agentIDs...),
	}
}

func todoExecutionPlanDigest(plan TodoExecutionPlan) (string, error) {
	payload, err := json.Marshal(struct {
		TenantID string            `json:"tenant_id"`
		Plan     TodoExecutionPlan `json:"plan"`
	}{TenantID: normalizedTenantID(plan.TenantID), Plan: plan})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func todoExecutionPlanAgentIDs(plan TodoExecutionPlan) []string {
	values := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		values = append(values, step.AssigneeAgentID)
	}
	return normalizeTags(values)
}

func jsonObjectCandidates(value string) []string {
	parts := strings.Split(value, "```")
	out := make([]string, 0)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "json")
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			out = append(out, part)
		}
	}
	return out
}

func containsStringFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
