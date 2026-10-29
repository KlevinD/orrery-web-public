package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	projectadmin "orrery_v2/internal/app/projectadmin"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/scmconnection"
	todoapp "orrery_v2/internal/app/todo"
	memretrieval "orrery_v2/internal/memory/retrieval"
	missioncore "orrery_v2/internal/mission/core"
	missiondiscussion "orrery_v2/internal/mission/discussion"
	missionevent "orrery_v2/internal/mission/event"
	missionprojection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
	httptransport "orrery_v2/internal/transport/http"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type productionAPI struct {
	agentCoreRecovery            agentCoreRecoveryStore
	missionQuery                 httptransport.MissionQueryService
	missionCommand               *missioncontrol.CommandService
	missionRepo                  productionMissionProjectionRepository
	discussions                  productionMissionDiscussionSessions
	missionMembers               productionMissionDiscussionMembers
	discussionRepository         authorizationBackfillDiscussionRepository
	missionLinkCleaner           productionMissionLinkCleaner
	missionResponsibilityCleaner productionMissionResponsibilityCleaner
	projectCommand               *projectadmin.Service
	projectRepository            *projectadmin.PlatformRepository
	developmentProjectRepository *developmentproject.PlatformRepository
	scmConnectionRepository      *scmconnection.PlatformRepository
	todoRepository               *todoapp.Repository
	authorizationService         *authorizationapp.Service
	authorizationRelations       *authorizationapp.RelationProjector
	authorizationShadow          *authorizationapp.LoggingShadowRecorder
	authorizationBackfill        authorizationBackfillOperations
	unitOfWork                   *uow.UnitOfWork
	taskRepo                     interface {
		GetTask(ctx context.Context, tx uow.Tx, taskID missiontask.TaskID) (missiontask.TaskRecord, error)
		DeleteMissionTasks(ctx context.Context, tx uow.Tx, missionID missioncore.MissionID) error
		DeleteTask(ctx context.Context, tx uow.Tx, taskID missiontask.TaskID) error
	}
	approvals        *missioncontrol.ApprovalService
	stream           *missioncontrol.StreamService
	eventReader      missionevent.Reader
	runtimeTasks     missioncontrol.RuntimeTaskProjectionReaderPort
	resourceQuery    *resourceadmin.QueryService
	resourceCommand  *resourceadmin.CommandService
	operations       *operations.Service
	humanProfiles    productionHumanProfileBootstrap
	mcpGateway       *mcptransport.Gateway
	ontologyProject  *ontology.MappingRuleProjectionRunner
	ontologyEvents   productionOntologyEventLog
	contextGraphCore *db.Store
	ontologySecurity *productionOntologySecurityState
}

type productionMissionLinkCleaner interface {
	DeleteMissionLinks(context.Context, string) error
}

type productionMissionResponsibilityCleaner interface {
	TerminateMissionAssignments(context.Context, string, string, string, string, time.Time) (int, error)
}

type productionMissionProjectionRepository interface {
	operations.MissionProjectionRepository
	Delete(ctx context.Context, missionID missioncore.MissionID) error
}

func loadProductionMissionProjection(ctx context.Context, repo productionMissionProjectionRepository, tenantID string, missionID missioncore.MissionID) (missionprojection.ReadModel, error) {
	tenantRepo, ok := repo.(interface {
		GetMissionControlForTenant(context.Context, string, missioncore.MissionID) (missionprojection.ReadModel, error)
	})
	if !ok {
		return missionprojection.ReadModel{}, missioncontrol.ErrServiceDependency
	}
	return tenantRepo.GetMissionControlForTenant(ctx, tenantID, missionID)
}

func saveProductionMissionProjection(ctx context.Context, repo productionMissionProjectionRepository, tenantID string, model missionprojection.ReadModel) error {
	tenantRepo, ok := repo.(interface {
		SaveForTenant(context.Context, string, missionprojection.ReadModel) error
	})
	if !ok {
		return missioncontrol.ErrServiceDependency
	}
	return tenantRepo.SaveForTenant(ctx, tenantID, model)
}

type productionMissionDiscussionSessions interface {
	EnsureMissionConversation(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.EnsureMissionConversationCommand) (discussionsession.ConversationDTO, error)
	EnsureMissionAgentMember(ctx context.Context, request missioncontrol.CanonicalConversationMembershipRequest) error
	ReconcileMissionAgentMembers(ctx context.Context, request missioncontrol.CanonicalConversationMembershipSetRequest) error
	ReconcileTodoMissionAgentMembers(ctx context.Context, request missioncontrol.CanonicalConversationMembershipSetRequest) error
	GetConversation(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.ConversationDTO, error)
}

type productionPersonalAssistantOriginResolver interface {
	ResolvePersonalAssistantOrigin(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.PersonalAssistantOrigin, error)
}

type productionMissionDiscussionMembers interface {
	AddMember(ctx context.Context, groupID missiondiscussion.DiscussionGroupID, member missiondiscussion.DiscussionMember) error
	ListMembers(ctx context.Context, groupID missiondiscussion.DiscussionGroupID) ([]missiondiscussion.DiscussionMember, error)
}

type productionDiscussionExecutionDeleter struct {
	api productionAPI
}

type productionDiscussionMissionMessenger struct {
	api *productionAPI
}

type productionDiscussionAgentMentionResponder struct {
	profiles                *agentcore.PlatformProfileRepository
	memory                  memretrieval.Repository
	tools                   toolres.Registry
	skills                  skillres.Registry
	deps                    productionDiscussionAgentDependencies
	personalAssistantRunner discussionsession.DiscussionAgentRunner
	workspaceRootProvider   discussionsession.WorkspaceRootProvider
	clock                   interface{ Now() time.Time }
	idGenerator             interface{ NewIDContext() (string, error) }
	runtimeConfig           config.Config
	timeout                 time.Duration
}

func (d productionDiscussionExecutionDeleter) DeleteMission(ctx context.Context, viewer missioncontrol.ViewerRef, missionID string) error {
	return d.api.DeleteMission(ctx, viewer, missioncore.MissionID(strings.TrimSpace(missionID)))
}

func (d productionDiscussionExecutionDeleter) DeleteMissionTask(ctx context.Context, viewer missioncontrol.ViewerRef, missionID string, taskID string) error {
	return d.api.DeleteMissionTask(ctx, viewer, missioncore.MissionID(strings.TrimSpace(missionID)), missiontask.TaskID(strings.TrimSpace(taskID)))
}

func (m productionDiscussionMissionMessenger) PostDiscussionMessage(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, mission discussionsession.LinkedMissionDTO, content string, idempotencyKey string) error {
	if m.api == nil {
		return missioncontrol.ErrServiceDependency
	}
	view, err := m.api.GetMissionControlView(ctx, viewer, missioncore.MissionID(strings.TrimSpace(mission.MissionID)))
	if err != nil {
		return err
	}
	if strings.TrimSpace(view.Discussion.DiscussionGroupID) == "" {
		return missioncontrol.ErrProjectionNotFound
	}
	content = strings.TrimSpace(content)
	if err := m.syncMentionedSourceDiscussionAgents(ctx, viewer, conversationID, view, content); err != nil {
		return err
	}
	_, err = m.api.PostDiscussionMessage(ctx, viewer, missioncontrol.PostDiscussionMessageCommand{
		RequestID:         firstNonBlank(conversationID, idempotencyKey),
		IdempotencyKey:    firstNonBlank(idempotencyKey, conversationID),
		MissionID:         missioncore.MissionID(strings.TrimSpace(mission.MissionID)),
		DiscussionGroupID: missiondiscussion.DiscussionGroupID(view.Discussion.DiscussionGroupID),
		Content:           content,
		MessageKind:       "request",
		Visibility:        "mission",
		RequiredResponse:  true,
	})
	return err
}

func (m productionDiscussionMissionMessenger) syncMentionedSourceDiscussionAgents(ctx context.Context, viewer missioncontrol.ViewerRef, conversationID string, view missioncontrol.MissionControlViewDTO, content string) error {
	if m.api == nil || m.api.discussions == nil || m.api.missionMembers == nil {
		return nil
	}
	groupID := missiondiscussion.DiscussionGroupID(strings.TrimSpace(view.Discussion.DiscussionGroupID))
	if groupID == "" || strings.TrimSpace(conversationID) == "" {
		return nil
	}
	parsed := missiondiscussion.NewMentionParser().Parse(content)
	if len(parsed.Targets) == 0 {
		return nil
	}
	members, err := m.api.missionMembers.ListMembers(ctx, groupID)
	if err != nil {
		return err
	}
	source, err := m.api.discussions.GetConversation(ctx, discussionSessionViewer(viewer), conversationID)
	if err != nil {
		return err
	}
	sourceAgents := sourceDiscussionAgentMembers(source.Members)
	missionAgents := missionDiscussionAgentMembers(members)
	for _, target := range parsed.Targets {
		if target.Kind != missiondiscussion.MentionTargetAgent {
			continue
		}
		agentID := strings.TrimSpace(target.Ref)
		if agentID == "" || !sourceAgents[agentID] {
			continue
		}
		if !missionAgents[agentID] {
			if err := m.api.missionMembers.AddMember(ctx, groupID, missiondiscussion.DiscussionMember{
				AgentProfileID:  agentcore.AgentProfileID(agentID),
				AgentInstanceID: agentcore.AgentInstanceID(agentID),
				MemberKind:      missiondiscussion.DiscussionMemberAgent,
				MissionRole:     missionteam.MissionRoleObserver,
				VisibilityScope: missiondiscussion.VisibilityMission,
				CanPost:         true,
				CanMention:      true,
			}); err != nil {
				return err
			}
			missionAgents[agentID] = true
		}
		if m.api.authorizationRelations == nil {
			return missioncontrol.ErrServiceDependency
		}
		subject := authorizationapp.SubjectRef{Kind: "agent", ID: agentID}
		if err := m.api.authorizationRelations.WriteMissionParticipant(ctx, subject, view.Mission.MissionID, viewer.Tenant()); err != nil {
			return err
		}
		if err := m.api.authorizationRelations.WriteDiscussionMember(ctx, subject, string(groupID), viewer.Tenant()); err != nil {
			return err
		}
	}
	return nil
}

func sourceDiscussionAgentMembers(members []discussionsession.ConversationMemberDTO) map[string]bool {
	out := make(map[string]bool, len(members))
	for _, member := range members {
		if strings.TrimSpace(member.Kind) != string(discussionsession.ViewerKindAgent) {
			continue
		}
		if id := strings.TrimSpace(member.ID); id != "" {
			out[id] = true
		}
	}
	return out
}

func missionDiscussionAgentMembers(members []missiondiscussion.DiscussionMember) map[string]bool {
	out := make(map[string]bool, len(members)*2)
	for _, member := range members {
		if member.MemberKind != missiondiscussion.DiscussionMemberAgent || !member.Active() {
			continue
		}
		for _, id := range []string{member.AgentProfileID.String(), member.AgentInstanceID.String()} {
			if id = strings.TrimSpace(id); id != "" {
				out[id] = true
			}
		}
	}
	return out
}

func (r productionDiscussionAgentMentionResponder) RespondToDiscussionMention(ctx context.Context, request discussionsession.DiscussionAgentMentionRequest) (discussionsession.DiscussionAgentResponse, error) {
	if request.PersonalAssistantHost {
		runner, mentionRequest, err := r.personalAssistantMentionRunner(request)
		if err != nil {
			return discussionsession.DiscussionAgentResponse{}, err
		}
		responseCtx, cancel := r.responseContext(ctx)
		defer cancel()
		return runner.RunDiscussion(responseCtx, mentionRequest)
	}
	runner, mentionRequest, err := r.mentionRunner(ctx, request)
	if err != nil {
		return discussionsession.DiscussionAgentResponse{}, err
	}
	mentionRequest, cleanup, err := prepareWebsiteAdvisorNativeWorkspace(ctx, mentionRequest)
	if err != nil {
		return discussionsession.DiscussionAgentResponse{}, err
	}
	defer cleanup()
	responseCtx, cancel := r.responseContext(ctx)
	defer cancel()
	return runner.RunDiscussion(responseCtx, mentionRequest)
}

func (r productionDiscussionAgentMentionResponder) RespondToDiscussionMentionStream(ctx context.Context, request discussionsession.DiscussionAgentMentionRequest) (<-chan discussionsession.DiscussionAgentStreamEvent, error) {
	if request.PersonalAssistantHost {
		runner, mentionRequest, err := r.personalAssistantMentionRunner(request)
		if err != nil {
			return nil, err
		}
		streamer, ok := runner.(discussionsession.DiscussionAgentStreamRunner)
		if !ok {
			return nil, discussionsession.ErrRuntimeUnavailable
		}
		responseCtx, cancel := r.responseContext(ctx)
		events, err := streamer.RunDiscussionStream(responseCtx, mentionRequest)
		if err != nil {
			cancel()
			return nil, err
		}
		return cancelDiscussionMentionStream(events, cancel), nil
	}
	runner, mentionRequest, err := r.mentionRunner(ctx, request)
	if err != nil {
		return nil, err
	}
	mentionRequest, cleanup, err := prepareWebsiteAdvisorNativeWorkspace(ctx, mentionRequest)
	if err != nil {
		return nil, err
	}
	responseCtx, cancel := r.responseContext(ctx)
	events, err := runner.RunDiscussionStream(responseCtx, mentionRequest)
	if err != nil {
		cancel()
		cleanup()
		return nil, err
	}
	return cancelDiscussionMentionStream(events, func() { cancel(); cleanup() }), nil
}

func (r productionDiscussionAgentMentionResponder) SteerDiscussionRun(ctx context.Context, run discussionsession.RunRecord, message string) error {
	agentID := strings.TrimSpace(run.AgentID)
	message = strings.TrimSpace(message)
	if agentID == "" || strings.TrimSpace(run.RunID) == "" || message == "" {
		return discussionsession.ErrInvalidRequest
	}
	if r.profiles == nil || r.deps.RuntimeRegistry == nil {
		return discussionsession.ErrRuntimeUnavailable
	}
	profile, err := r.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil {
		return err
	}
	spec := configuredConversationAgentRuntimeOptions(r.runtimeConfig, agentID).RuntimeSpec
	if !spec.HasKind() {
		spec = profile.RuntimeSpec
	}
	if !spec.HasKind() && systemManagedDiscussionProfile(profile) {
		spec = defaultAgentRuntimeSpec(r.runtimeConfig, agentID)
	}
	if !spec.Kind.IsValid() {
		return discussionsession.ErrRuntimeUnavailable
	}
	runtime, err := r.deps.RuntimeRegistry.ResolveRuntime(ctx, spec)
	if err != nil {
		return err
	}
	return runtime.SteerSession(ctx, agentcore.AgentRunID(run.RunID), message)
}

func (r productionDiscussionAgentMentionResponder) personalAssistantMentionRunner(request discussionsession.DiscussionAgentMentionRequest) (discussionsession.DiscussionAgentRunner, discussionsession.DiscussionAgentRequest, error) {
	if !request.PersonalAssistantHost {
		return nil, discussionsession.DiscussionAgentRequest{}, discussionsession.ErrInvalidRequest
	}
	if r.personalAssistantRunner == nil {
		return nil, discussionsession.DiscussionAgentRequest{}, fmt.Errorf("%w: personal assistant mention runtime is unavailable", discussionsession.ErrRuntimeUnavailable)
	}
	if strings.TrimSpace(request.TargetAgent.ID) == "" || strings.TrimSpace(request.PersonalAssistantOriginOwnerID) == "" || strings.TrimSpace(request.PersonalAssistantOriginSubjectID) == "" {
		return nil, discussionsession.DiscussionAgentRequest{}, fmt.Errorf("%w: personal assistant host request is incomplete", discussionsession.ErrInvalidRequest)
	}
	content := firstNonBlank(request.Content, request.SourceMessage.Content)
	requestViewer := discussionsession.ViewerRef{
		ViewerID:       request.PersonalAssistantOriginSubjectID,
		OwnerID:        firstNonBlank(request.PersonalAssistantOriginOwnerID, request.ConversationOwnerID),
		AgentProfileID: request.PersonalAssistantOriginSubjectID,
		ViewerKind:     discussionsession.ViewerKindUser,
		DisplayName:    request.SourceMessage.AuthorName,
		TenantID:       request.ConversationTenantID,
	}
	mentionRequest := discussionsession.DiscussionAgentRequest{
		ConversationID:                   request.ConversationID,
		ConversationKind:                 request.ConversationKind,
		ConversationOwnerID:              request.ConversationOwnerID,
		ConversationTenantID:             request.ConversationTenantID,
		HILMode:                          request.HILMode,
		PersonalAssistantOriginOwnerID:   request.PersonalAssistantOriginOwnerID,
		PersonalAssistantOriginSubjectID: request.PersonalAssistantOriginSubjectID,
		MessageID:                        firstNonBlank(request.SourceMessage.MessageID, request.RunID),
		MissionID:                        request.SourceMessage.MissionID,
		MissionTaskID:                    request.SourceMessage.MissionTaskID,
		RunID:                            request.RunID,
		AgentID:                          request.TargetAgent.ID,
		LogicalAgentID:                   request.LogicalAgentID,
		Viewer:                           requestViewer,
		Content:                          content,
		TriggerMessage:                   request.SourceMessage,
		History:                          append([]discussionsession.MessageDTO(nil), request.History...),
		Runs:                             append([]discussionsession.RunRecord(nil), request.Runs...),
		Members:                          append([]discussionsession.ConversationMemberDTO(nil), request.Members...),
		LinkedMissions:                   append([]discussionsession.LinkedMissionDTO(nil), request.LinkedMissions...),
		Attachments:                      append([]discussionsession.AttachmentDTO(nil), request.Attachments...),
		Workspace:                        request.Workspace,
		BoundTools:                       append([]discussionsession.DiscussionAgentBoundTool(nil), request.BoundTools...),
		BoundToolsPolicy:                 request.BoundToolsPolicy,
		ProviderSpecific:                 cloneStringMap(request.ProviderSpecific),
		ResumeSessionID:                  request.ResumeSessionID,
		ForceNewSession:                  request.ForceNewSession,
		Mode:                             firstNonBlank(request.Mode, "mention"),
	}
	return r.personalAssistantRunner, mentionRequest, nil
}

func (r productionDiscussionAgentMentionResponder) mentionRunner(ctx context.Context, request discussionsession.DiscussionAgentMentionRequest) (*discussionsession.BaseAgentDiscussionAgentRunner, discussionsession.DiscussionAgentRequest, error) {
	agentID := strings.TrimSpace(request.TargetAgent.ID)
	if agentID == "" {
		return nil, discussionsession.DiscussionAgentRequest{}, fmt.Errorf("%w: mentioned agent id is required", discussionsession.ErrInvalidRequest)
	}
	if r.profiles == nil {
		return nil, discussionsession.DiscussionAgentRequest{}, fmt.Errorf("%w: agent profile directory unavailable", discussionsession.ErrRuntimeUnavailable)
	}
	profile, err := r.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil {
		return nil, discussionsession.DiscussionAgentRequest{}, err
	}
	if profile.ID.IsZero() || !profile.Enabled {
		return nil, discussionsession.DiscussionAgentRequest{}, fmt.Errorf("%w: mentioned agent %s is not available", discussionsession.ErrRuntimeUnavailable, agentID)
	}
	instanceID, err := discussionAgentInstanceID(ctx, r.profiles, profile.ID)
	if err != nil {
		return nil, discussionsession.DiscussionAgentRequest{}, err
	}
	spec := configuredConversationAgentRuntimeOptions(r.runtimeConfig, agentID).RuntimeSpec
	if !spec.HasKind() {
		spec = profile.RuntimeSpec
	}
	if !spec.HasKind() {
		if systemManagedDiscussionProfile(profile) {
			spec = defaultAgentRuntimeSpec(r.runtimeConfig, agentID)
		} else {
			return nil, discussionsession.DiscussionAgentRequest{}, fmt.Errorf("%w: mentioned agent %s has no runtime spec", discussionsession.ErrRuntimeUnavailable, agentID)
		}
	}
	if !spec.Kind.IsValid() {
		return nil, discussionsession.DiscussionAgentRequest{}, fmt.Errorf("%w: mentioned agent %s runtime kind %q is invalid", discussionsession.ErrRuntimeUnavailable, agentID, spec.Kind)
	}
	spec = automaticRepairRuntimeSpec(ctx, configString(r.runtimeConfig, "maintenance.repair_runtime_config_ref"), request, spec)
	profile.RuntimeSpec = spec
	deps := withProductionDiscussionAgentDefaults(r.deps)
	assembler, err := newProductionDiscussionAgentAssembler(r.memory, r.tools, r.skills, deps)
	if err != nil {
		return nil, discussionsession.DiscussionAgentRequest{}, err
	}
	workspaceRootProvider := r.workspaceRootProvider
	if websiteAdvisorSearchTools(profile) != "" {
		workspaceRootProvider = nil
	}
	runner, err := discussionsession.NewAssembledDiscussionAgentRunner(ctx, discussionsession.AssembledDiscussionAgentRunnerOptions{
		Assembler:             assembler,
		Profile:               profile,
		AgentID:               agentID,
		InstanceID:            instanceID,
		SystemPrompt:          discussionMentionFallbackPrompt(),
		PromptBlockProvider:   deps.PromptBlocks,
		ActionInterpreter:     r.deps.ActionSemantics,
		AttachmentProcessor:   discussionsession.NewRuntimeAttachmentProcessor(newProductionSpeechProvider(deps.LLMProfiles, deps.Secrets)),
		WorkspaceRootProvider: workspaceRootProvider,
		Timeout:               automaticRepairRuntimeTimeout(ctx, r.runtimeConfig, request, r.runtimeTimeout(agentID)),
	})
	if err != nil {
		return nil, discussionsession.DiscussionAgentRequest{}, err
	}
	runID := strings.TrimSpace(request.RunID)
	if runID == "" && r.idGenerator != nil {
		runID, _ = r.idGenerator.NewIDContext()
	}
	if runID == "" {
		runID = "discussion-mention-" + agentID
	}
	replyTo := strings.TrimSpace(request.SourceMessage.AuthorName)
	if replyTo == "" {
		replyTo = strings.TrimSpace(request.SourceMessage.AuthorID)
	}
	content := strings.TrimSpace(request.SourceMessage.Content)
	if strings.TrimSpace(request.Content) != "" {
		content = strings.TrimSpace(request.Content)
	}
	mentionRequest := discussionsession.DiscussionAgentRequest{
		ConversationID:       request.ConversationID,
		ConversationKind:     request.ConversationKind,
		ConversationOwnerID:  request.ConversationOwnerID,
		ConversationTenantID: request.ConversationTenantID,
		HILMode:              request.HILMode,
		MessageID:            request.SourceMessage.MessageID,
		RunID:                runID,
		AgentID:              agentID,
		PersonaPrompt:        discussionMentionFallbackPrompt(),
		Viewer:               discussionsession.ViewerRef{ViewerID: request.SourceMessage.AuthorID, ViewerKind: discussionsession.ViewerKindAgent, DisplayName: replyTo},
		Content:              content,
		History:              request.History,
		TriggerMessage:       request.SourceMessage,
		OutputLanguage:       request.OutputLanguage,
		Runs:                 append([]discussionsession.RunRecord(nil), request.Runs...),
		Members:              request.Members,
		LinkedMissions:       request.LinkedMissions,
		Attachments:          request.Attachments,
		Workspace:            request.Workspace,
		ProviderSpecific:     websiteAdvisorProviderSpecific(profile, request.ProviderSpecific),
		ResumeSessionID:      request.ResumeSessionID,
		ForceNewSession:      request.ForceNewSession,
		Mode:                 firstNonBlank(request.Mode, "mention"),
	}
	mentionRequest.TriggerMessage.Content = content
	return runner, websiteAdvisorMentionRequest(profile, mentionRequest), nil
}

func websiteAdvisorProviderSpecific(_ agentcore.AgentProfile, existing map[string]string) map[string]string {
	return cloneStringMap(existing)
}

func websiteAdvisorSearchTools(profile agentcore.AgentProfile) string {
	switch strings.TrimSpace(profile.OrgIdentity.AuthorityScope) {
	case "public_website_receptionist":
		return "mcp_orrery_public_readonly_search_documents"
	case "customer_website_receptionist":
		return "mcp_orrery_public_readonly_search_documents,mcp_orrery_customer_readonly_search_documents"
	}
	return ""
}

func websiteAdvisorMentionRequest(profile agentcore.AgentProfile, request discussionsession.DiscussionAgentRequest) discussionsession.DiscussionAgentRequest {
	if websiteAdvisorSearchTools(profile) == "" {
		return request
	}
	request.DiscussionTitle = ""
	request.DiscussionGoal = ""
	request.PersonaPrompt = strings.TrimSpace(profile.BaseAgent.SystemPrompt)
	request.History = nil
	request.Runs = nil
	request.Members = nil
	request.LinkedMissions = nil
	request.Workspace = discussionsession.DiscussionWorkspaceContext{}
	request.Attachments = nil
	request.Memory = discussionsession.DiscussionAgentMemoryContext{}
	request.Knowledge = discussionsession.DiscussionAgentKnowledgeContext{}
	request.BoundSkills = nil
	request.ResumeSessionID = ""
	request.ForceNewSession = true
	return request
}

func (r productionDiscussionAgentMentionResponder) responseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}

func (r productionDiscussionAgentMentionResponder) runtimeTimeout(agentID string) time.Duration {
	if timeout := configuredConversationAgentRuntimeOptions(r.runtimeConfig, agentID).Timeout; timeout > 0 {
		return timeout
	}
	return r.timeout
}

func cancelDiscussionMentionStream(events <-chan discussionsession.DiscussionAgentStreamEvent, cancel context.CancelFunc) <-chan discussionsession.DiscussionAgentStreamEvent {
	out := make(chan discussionsession.DiscussionAgentStreamEvent, 16)
	go func() {
		defer cancel()
		defer close(out)
		for event := range events {
			out <- event
		}
	}()
	return out
}

func discussionMentionFallbackPrompt() string {
	return strings.Join([]string{
		"你在当前讨论组中被其他成员 @ 提及。请只输出要发送回这个讨论组的正文。",
		"只有需要通知、移交、要求处理或要求回复时，才原样使用当前讨论组成员信息中的完整 Markdown 通知链接（例如 [@显示名](mention://agent/profile-id)）；不要只输出裸 mention://agent/... URI。普通提及成员时只写 @显示名；不要编造成员、名字、工具或已经完成的 staffing 动作。",
		"如果当前信息不足，明确向话题发起人或相关负责人询问缺失信息。",
	}, "\n")
}
