package discussionsession

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/platform/idgen"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func (s *Service) now() time.Time {
	if s.clock != nil {
		return s.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) runtimeUnavailableError() error {
	if s != nil && s.runtimeSetupError != nil {
		return RuntimeUnavailableError(s.runtimeSetupError)
	}
	return ErrRuntimeUnavailable
}

func (s *Service) discussionAgentID() string {
	if s != nil && s.discussionAgent != nil {
		if provider, ok := s.discussionAgent.(DiscussionAgentIdentityProvider); ok {
			if id := strings.TrimSpace(provider.DiscussionAgentID()); id != "" {
				return id
			}
		}
	}
	return ""
}

func (s *Service) configuredRunAgentID() (string, error) {
	agentID := s.discussionAgentID()
	if agentID == "" {
		return "", fmt.Errorf("%w: run agent is not configured", ErrInvalidRequest)
	}
	return agentID, nil
}

func (s *Service) configuredRunAgentIDForConversation(ctx context.Context, conversation ConversationRecordView) (string, error) {
	if s != nil && s.discussionAgent != nil {
		if resolver, ok := s.discussionAgent.(DiscussionAgentRunIdentityResolver); ok {
			agentID, err := resolver.ResolveDiscussionRunAgentID(ctx, conversation)
			if err != nil {
				return "", err
			}
			if agentID = strings.TrimSpace(agentID); agentID != "" {
				return agentID, nil
			}
		}
	}
	return s.configuredRunAgentID()
}

func (s *Service) configuredRunAgentDisplayNameForConversation(ctx context.Context, conversation ConversationRecordView) (string, error) {
	if s != nil && s.discussionAgent != nil {
		if resolver, ok := s.discussionAgent.(DiscussionAgentRunDisplayNameResolver); ok {
			displayName, err := resolver.ResolveDiscussionRunAgentDisplayName(ctx, conversation)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(displayName), nil
		}
	}
	return "", nil
}

func (s *Service) discussionAgentBoundTools() []DiscussionAgentBoundTool {
	if s != nil && s.discussionAgent != nil {
		if provider, ok := s.discussionAgent.(DiscussionAgentBoundToolProvider); ok {
			return appendDiscussionAgentBoundTools(provider.DiscussionAgentBoundTools(), nil)
		}
	}
	return nil
}

func (s *Service) discussionAgentBoundToolsForAgent(ctx context.Context, agentID string) []DiscussionAgentBoundTool {
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.agents == nil || agentID == "" {
		return nil
	}
	profile, err := s.agents.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil || profile.ID.IsZero() {
		return nil
	}
	return discussionAgentBoundToolsFromToolRefs(profile.ToolRefs)
}

func (s *Service) discussionAgentBoundToolsForConversationAgent(ctx context.Context, conversationID string, agentID string) []DiscussionAgentBoundTool {
	tools := s.discussionAgentBoundToolsForAgent(ctx, agentID)
	if policy, err := s.conversationToolPolicy(ctx, conversationID); err == nil && !policy.MemberAdditionsAllowed {
		tools = withoutDiscussionMemberAddTool(tools)
	}
	return tools
}

func (s *Service) discussionAgentBoundSkills() []DiscussionAgentBoundSkill {
	if s != nil && s.discussionAgent != nil {
		if provider, ok := s.discussionAgent.(DiscussionAgentBoundSkillProvider); ok {
			return append([]DiscussionAgentBoundSkill(nil), provider.DiscussionAgentBoundSkills()...)
		}
	}
	return nil
}

func (s *Service) prepareDiscussionAgentRequest(ctx context.Context, request *DiscussionAgentRequest) (err error) {
	if request == nil {
		return nil
	}
	finishStage := s.startDiscussionStage(ctx, request.ConversationID, "", request.RunID, "context_prepare")
	defer func() { finishStage(err) }()
	boundTools := appendDiscussionAgentBoundTools(s.discussionAgentBoundTools(), nil)
	policy, err := s.conversationToolPolicy(ctx, request.ConversationID)
	if err != nil {
		return err
	} else if !policy.MemberAdditionsAllowed {
		boundTools = withoutDiscussionMemberAddTool(boundTools)
	}
	request.BoundTools = boundTools
	request.BoundToolsPolicy = true
	request.BoundSkills = firstDiscussionAgentBoundSkills(request.BoundSkills, s.discussionAgentBoundSkills())
	if router, ok := s.discussionAgent.(DiscussionAgentRequestRouter); ok {
		if err := router.RouteDiscussionAgentRequest(ctx, request); err != nil {
			return err
		}
	}
	if !policy.MemberAdditionsAllowed {
		request.BoundTools = withoutDiscussionMemberAddTool(request.BoundTools)
	}
	return s.attachDiscussionWorkspace(ctx, request)
}

func (s *Service) prepareDiscussionAgentRequestOrFailRun(ctx context.Context, conversationID string, run RunRecord, request *DiscussionAgentRequest) error {
	if err := s.prepareDiscussionAgentRequest(ctx, request); err != nil {
		if saveErr := s.persistFailedRun(ctx, conversationID, run, err); saveErr != nil {
			return saveErr
		}
		return err
	}
	return nil
}

func boundToolsForResponse(request DiscussionAgentRequest, response DiscussionAgentResponse) []DiscussionAgentBoundTool {
	return appendDiscussionAgentBoundTools(request.BoundTools, nil)
}

func discussionAgentBoundToolsFromToolRefs(refs []agentcore.ToolRef) []DiscussionAgentBoundTool {
	out := make([]DiscussionAgentBoundTool, 0, len(refs))
	for _, ref := range refs {
		toolID := strings.TrimSpace(string(ref.ToolID))
		if toolID == "" {
			continue
		}
		out = append(out, DiscussionAgentBoundTool{
			ToolID: toolID,
			Alias:  strings.TrimSpace(ref.Alias),
		})
	}
	return appendDiscussionAgentBoundTools(out, nil)
}

type discussionToolPolicy struct {
	MemberAdditionsAllowed bool
}

func (s *Service) conversationToolPolicy(ctx context.Context, conversationID string) (discussionToolPolicy, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(conversationID) == "" {
		return discussionToolPolicy{MemberAdditionsAllowed: true}, nil
	}
	view, err := s.repo.GetConversationRecord(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return discussionToolPolicy{}, err
	}
	return discussionToolPolicy{MemberAdditionsAllowed: conversationMemberAdditionsAllowed(view)}, nil
}

func withoutDiscussionMemberAddTool(tools []DiscussionAgentBoundTool) []DiscussionAgentBoundTool {
	if len(tools) == 0 {
		return tools
	}
	out := make([]DiscussionAgentBoundTool, 0, len(tools))
	for _, tool := range tools {
		if normalizeToolName(tool.ToolID) == string(toolstd.DiscussionMemberAdd) || normalizeToolName(tool.Alias) == string(toolstd.DiscussionMemberAdd) {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func validateBoundToolCalls(calls []ToolCallDTO, boundTools []DiscussionAgentBoundTool) error {
	if len(calls) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, tool := range boundTools {
		id := normalizeToolName(tool.ToolID)
		if id != "" {
			allowed[id] = true
		}
		alias := normalizeToolName(tool.Alias)
		if alias != "" {
			allowed[alias] = true
		}
	}
	allowMissionPreparationEquivalentTools(allowed)
	for _, call := range calls {
		name := normalizeToolName(call.Name)
		if name == "" || !isSideEffectDiscussionTool(name) || allowed[name] {
			continue
		}
		return newOrchestrationIncompleteError("unbound tool call " + name + " is not allowed for this Agent run; bind the tool through the personnel directory profile before it can mutate discussion or Mission state")
	}
	return nil
}

func allowMissionPreparationEquivalentTools(allowed map[string]bool) {
	if allowed == nil {
		return
	}
	if allowed[string(toolstd.MissionPrepare)] {
		allowed[string(toolstd.MissionBriefCreate)] = true
		allowed[string(toolstd.MissionTaskCreateRoot)] = true
		return
	}
	if allowed[string(toolstd.MissionBriefCreate)] && allowed[string(toolstd.MissionTaskCreateRoot)] {
		allowed[string(toolstd.MissionPrepare)] = true
	}
}

func isSideEffectDiscussionTool(name string) bool {
	switch normalizeToolName(name) {
	case string(toolstd.MissionPrepare),
		string(toolstd.MissionBriefCreate),
		string(toolstd.MissionTaskCreateRoot),
		string(toolstd.MissionTaskDelegate),
		string(toolstd.MissionCoordinatorPlanParallel),
		string(toolstd.DiscussionClose),
		string(toolstd.DiscussionTalentList),
		string(toolstd.DiscussionMemberAdd),
		string(toolstd.DiscussionAck):
		return true
	default:
		return false
	}
}

func (s *Service) logDiscussionRunStart(ctx context.Context, run RunRecord, request DiscussionAgentRequest) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Debug(ctx, "discussion runtime run starting",
		"conversation_id", run.ConversationID,
		"run_id", run.RunID,
		"agent_id", run.AgentID,
		"message_id", request.MessageID,
		"content_length", len(strings.TrimSpace(request.Content)),
		"history_count", len(request.History),
		"linked_mission_count", len(request.LinkedMissions),
	)
}

func (s *Service) logDiscussionRunFailure(ctx context.Context, run RunRecord, err error) {
	if s == nil || s.logger == nil || err == nil {
		return
	}
	s.logger.Warn(ctx, "discussion runtime run failed",
		"conversation_id", run.ConversationID,
		"run_id", run.RunID,
		"agent_id", run.AgentID,
		"error", err.Error(),
	)
}

func (s *Service) logDiscussionRunResponse(ctx context.Context, run RunRecord, response DiscussionAgentResponse) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Debug(ctx, "discussion runtime run completed",
		"conversation_id", run.ConversationID,
		"run_id", run.RunID,
		"agent_id", run.AgentID,
		"runtime_kind", response.RuntimeKind,
		"status", response.Status,
		"assistant_message_length", len(strings.TrimSpace(response.AssistantMessage)),
		"tool_call_count", len(response.ToolCalls),
		"linked_mission_count", len(response.LinkedMissions),
		"duration_ms", response.DurationMs,
		"usage", response.Usage,
	)
}

func applyResponseTelemetry(run *RunRecord, response DiscussionAgentResponse) {
	if run == nil {
		return
	}
	run.Usage = mergeModelUsageMaps(run.Usage, response.Usage)
	if response.DurationMs > 0 {
		run.DurationMs += response.DurationMs
		return
	}
	if run.DurationMs == 0 {
		run.DurationMs = durationMsBetween(run.StartedAt, run.CompletedAt)
	}
}

func (s *Service) newID() (string, error) {
	if s.generator == nil {
		return idgen.NewRandom("discussion").NewIDContext()
	}
	return s.generator.NewIDContext()
}

func missionViewer(viewer ViewerRef) missioncontrol.ViewerRef {
	kind := missioncontrol.ViewerKindUser
	switch viewer.ViewerKind {
	case ViewerKindAgent:
		kind = missioncontrol.ViewerKindAgent
	case ViewerKindHuman:
		kind = missioncontrol.ViewerKindHuman
	case ViewerKindSystem:
		kind = missioncontrol.ViewerKindSystem
	}
	viewerID := strings.TrimSpace(viewer.ViewerID)
	authorizationSubjectID := ""
	if (kind == missioncontrol.ViewerKindUser || kind == missioncontrol.ViewerKindHuman) && strings.TrimSpace(viewer.OwnerID) != "" {
		viewerID = strings.TrimSpace(viewer.OwnerID)
		authorizationSubjectID = firstNonBlank(viewer.AgentProfileID, viewer.ViewerID)
	}
	return missioncontrol.ViewerRef{
		ViewerID: viewerID, ViewerKind: kind, TenantID: firstNonBlank(viewer.TenantID, "global"),
		AuthorizationSubjectID: authorizationSubjectID,
		PermissionScope:        append([]string(nil), viewer.PermissionScope...),
	}
}

func discussionMissionToolViewer(run RunRecord, request DiscussionAgentRequest, view ConversationRecordView) missioncontrol.ViewerRef {
	if !isPersonalAssistantRuntimeRun(run) {
		return discussionAgentViewer(run.AgentID, request.Viewer.TenantID)
	}
	principal := request.Viewer
	principal.ViewerKind = ViewerKindUser
	principal.ViewerID = firstNonBlank(view.PersonalAssistantOriginSubjectID, request.PersonalAssistantOriginSubjectID, principal.AgentProfileID, principal.ViewerID)
	principal.AgentProfileID = principal.ViewerID
	principal.OwnerID = firstNonBlank(view.PersonalAssistantOriginOwnerID, request.PersonalAssistantOriginOwnerID, principal.OwnerID, principal.ViewerID)
	return missionViewer(principal)
}

func discussionAgentViewer(agentID string, tenantIDs ...string) missioncontrol.ViewerRef {
	agentID = strings.TrimSpace(agentID)
	tenantID := "global"
	if len(tenantIDs) > 0 {
		tenantID = firstNonBlank(tenantIDs[0], tenantID)
	}
	return missioncontrol.ViewerRef{
		ViewerID:        agentID,
		ViewerKind:      missioncontrol.ViewerKindAgent,
		TenantID:        tenantID,
		PermissionScope: []string{"mission.read", "mission.write", "discussion.read", "discussion.write"},
	}
}

func toolCallResults(calls []ToolCallDTO) []ToolCallResult {
	out := make([]ToolCallResult, 0, len(calls))
	for _, call := range calls {
		arguments := cloneStringMap(call.Arguments)
		if toolCallID := strings.TrimSpace(call.ToolCallID); toolCallID != "" {
			if arguments == nil {
				arguments = map[string]string{}
			}
			arguments["tool_call_id"] = toolCallID
		}
		out = append(out, ToolCallResult{
			Name:      call.Name,
			Arguments: arguments,
			Result:    cloneStringMap(call.Result),
		})
	}
	return out
}

type initialHumanAgent struct {
	AgentID     string
	DisplayName string
}

func discussionReplyWithMentionFallback(content string, viewer ViewerRef) string {
	content = strings.TrimSpace(content)
	if content == "" || strings.TrimSpace(viewer.ViewerID) == "" {
		return content
	}
	if hasDiscussionMentionLink(content) || hasPlainDiscussionMention(content) {
		return content
	}
	name := strings.TrimSpace(viewer.DisplayName)
	if name == "" {
		name = strings.TrimSpace(viewer.ViewerID)
	}
	if name == "" {
		return content
	}
	return "[@" + name + "](mention://user/" + viewer.ViewerID + ") " + content
}

func runHumanViewer(messages []MessageRecord, run RunRecord) ViewerRef {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != "user" || strings.TrimSpace(message.AuthorID) == "" {
			continue
		}
		if !run.CreatedAt.IsZero() && message.CreatedAt.After(run.CreatedAt) {
			continue
		}
		return ViewerRef{ViewerID: message.AuthorID, ViewerKind: ViewerKindUser, DisplayName: message.AuthorName}
	}
	for _, message := range messages {
		if message.Role != "user" || strings.TrimSpace(message.AuthorID) == "" {
			continue
		}
		return ViewerRef{ViewerID: message.AuthorID, ViewerKind: ViewerKindUser, DisplayName: message.AuthorName}
	}
	return ViewerRef{}
}

func toolCallResultsWithInitialHumanAgent(calls []ToolCallDTO, humanAgentID string, displayName string) []ToolCallResult {
	results := toolCallResults(calls)
	humanAgentID = strings.TrimSpace(humanAgentID)
	displayName = strings.TrimSpace(displayName)
	if humanAgentID == "" {
		return results
	}
	for i := range results {
		name := normalizeToolName(results[i].Name)
		if name != string(toolstd.MissionBriefCreate) && name != string(toolstd.MissionPrepare) {
			continue
		}
		if results[i].Arguments == nil {
			results[i].Arguments = map[string]string{}
		}
		if strings.TrimSpace(results[i].Arguments["initial_human_agent_id"]) == "" {
			results[i].Arguments["initial_human_agent_id"] = humanAgentID
		}
		if displayName != "" && strings.TrimSpace(results[i].Arguments["initial_human_display_name"]) == "" {
			results[i].Arguments["initial_human_display_name"] = displayName
		}
	}
	return results
}

func toolCallsWithAppliedResults(calls []ToolCallDTO, results []ToolCallResult) []ToolCallDTO {
	out := append([]ToolCallDTO(nil), calls...)
	for i := range out {
		if i >= len(results) || len(results[i].Result) == 0 {
			continue
		}
		out[i].Result = cloneStringMap(out[i].Result)
		if out[i].Result == nil {
			out[i].Result = map[string]string{}
		}
		for key, value := range results[i].Result {
			if strings.TrimSpace(value) != "" {
				out[i].Result[key] = value
			}
		}
	}
	return out
}

func taskIDsCreatedByRuns(runs []RunRecord, cutoff time.Time) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, run := range runs {
		if run.CreatedAt.Before(cutoff) {
			continue
		}
		for _, call := range run.ToolCalls {
			if !isTaskCreateTool(call.Name) && !isCoordinatorPlanTool(call.Name) {
				continue
			}
			missionID := strings.TrimSpace(firstNonBlank(call.Result["mission_id"], call.Arguments["mission_id"], call.Arguments["mission"]))
			for _, taskID := range createdTaskIDsFromToolCall(call) {
				if missionID == "" || taskID == "" {
					continue
				}
				key := missionID + "|" + taskID
				if seen[key] {
					continue
				}
				seen[key] = true
				out[missionID] = append(out[missionID], taskID)
			}
		}
	}
	return out
}

func createdTaskIDsFromToolCall(call ToolCallDTO) []string {
	var out []string
	for key, value := range call.Result {
		normalized := strings.TrimSpace(key)
		if normalized == "task_id" || strings.HasPrefix(normalized, "task_id_") {
			if taskID := strings.TrimSpace(value); taskID != "" {
				out = append(out, taskID)
			}
		}
	}
	return out
}

func firstHumanAgent(messages []MessageRecord) initialHumanAgent {
	for _, message := range messages {
		authorID := strings.TrimSpace(message.AuthorID)
		if message.Role == "user" && authorID != "" && !strings.EqualFold(authorID, agentcore.SystemSuperadminPrincipalID) {
			return initialHumanAgent{
				AgentID:     authorID,
				DisplayName: strings.TrimSpace(message.AuthorName),
			}
		}
	}
	return initialHumanAgent{}
}

func isUnreadCompletedConversation(record ConversationRecord) bool {
	return isCompletedConversationStatus(record.Status) && (record.ViewedAt.IsZero() || record.ViewedAt.Before(record.UpdatedAt))
}

func normalizeListPage(query ListConversationsQuery) (int, int) {
	page := query.Page
	if page <= 0 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func normalizeMessagePage(query ListMessagesQuery) (int, int) {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func isCompletedConversationStatus(status string) bool {
	return strings.TrimSpace(strings.ToLower(status)) == "completed"
}
