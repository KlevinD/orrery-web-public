package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	devicedirectory "orrery_v2/internal/device/directory"
)

const (
	personalAssistantRouteResolvedKey  = "personal_assistant_route_resolved"
	personalAssistantRouteTargetKey    = "personal_assistant_route_target"
	personalAssistantHostingModeKey    = "personal_assistant_hosting_mode"
	personalAssistantOwnedRuntimeKey   = "personal_assistant_owned_runtime_kind"
	personalAssistantFallbackKey       = "personal_assistant_fallback_to_managed"
	personalAssistantRouteManaged      = "managed"
	personalAssistantRouteDevice       = "device"
	personalAssistantLogicalAgentIDKey = "personal_assistant_agent_id"
	personalAssistantAssignedAgentKey  = "personal_assistant_assigned_agent_id"
	personalAssistantPersonaPrompt     = "You are the user's independently configured private personal assistant in Orrery. Act only for the current user and never exceed that user's current permissions."
)

type productionPersonalAssistantDirectory interface {
	GetPersonalAssistant(context.Context, devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantView, error)
}

type productionPersonalAssistantRunnerResolver interface {
	ResolvePersonalAssistantRunner(context.Context, string, string, string) (discussionsession.DiscussionAgentRunner, error)
}

type productionPersonalAssistantDiscussionAgentRunner struct {
	notificationMu      sync.RWMutex
	notificationContext productionPersonalAssistantNotificationContextProvider
	managed             discussionsession.DiscussionAgentRunner
	device              discussionsession.DiscussionAgentRunner
	directory           productionPersonalAssistantDirectory
	resolver            productionPersonalAssistantRunnerResolver
	active              sync.Map
}

type productionPersonalAssistantActiveRun struct {
	runner discussionsession.DiscussionAgentRunner
	target string
}

type productionPersonalAssistantDeviceConfig struct {
	DeviceID             string   `json:"device_id"`
	WorkspaceGrantID     string   `json:"workspace_grant_id"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
}

func newProductionPersonalAssistantDiscussionAgentRunner(
	managed discussionsession.DiscussionAgentRunner,
	device discussionsession.DiscussionAgentRunner,
	directory productionPersonalAssistantDirectory,
	resolvers ...productionPersonalAssistantRunnerResolver,
) *productionPersonalAssistantDiscussionAgentRunner {
	var resolver productionPersonalAssistantRunnerResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return &productionPersonalAssistantDiscussionAgentRunner{managed: managed, device: device, directory: directory, resolver: resolver}
}

func (r *productionPersonalAssistantDiscussionAgentRunner) RouteDiscussionAgentRequest(ctx context.Context, request *discussionsession.DiscussionAgentRequest) error {
	if request == nil || !discussionsession.IsPersonalAssistantRuntimeRequest(request) {
		return nil
	}
	if r == nil || r.managed == nil || r.device == nil || r.directory == nil {
		return discussionsession.NewRuntimeSetupError("personal_assistant", "personal assistant runtime routing is unavailable")
	}
	if request.ProviderSpecific[personalAssistantRouteResolvedKey] == "true" {
		r.attachNotificationContext(ctx, request)
		request.ProviderSpecific = clonePersonalAssistantMetadata(request.ProviderSpecific)
		request.ProviderSpecific[agentcore.DedicatedLogicalMemoryCaptureProviderKey] = "true"
		applyProductionPersonalAssistantApprovalDialogue(ctx, request)
		return nil
	}

	ownerID := firstNonBlank(request.PersonalAssistantOriginOwnerID, request.ConversationOwnerID, request.Viewer.OwnerID, request.Viewer.ViewerID)
	principalID := firstNonBlank(request.PersonalAssistantOriginSubjectID, request.Viewer.AgentProfileID, request.Viewer.ViewerID, ownerID)
	tenantID := firstNonBlank(request.ConversationTenantID, request.Viewer.TenantID)
	if ownerID == "" {
		return discussionsession.NewRuntimeSetupError("personal_assistant", "personal assistant owner is unavailable")
	}
	request.ExecutionPrincipal = agentcore.ExecutionPrincipal{
		Kind: agentcore.ExecutionPrincipalUser, ID: principalID, TenantID: tenantID, Source: "system_assistant",
		PermissionScope: append([]string(nil), request.Viewer.PermissionScope...),
	}
	view, err := r.directory.GetPersonalAssistant(ctx, devicedirectory.OwnerScope{TenantID: tenantID, OwnerID: ownerID})
	if err != nil {
		return discussionsession.NewRuntimeSetupError("personal_assistant", fmt.Sprintf("load hosting configuration: %v", err))
	}
	if !view.AssistantEnabled {
		return discussionsession.NewRuntimeSetupError("personal_assistant", "personal assistant is disabled")
	}
	request.ProviderSpecific = clonePersonalAssistantMetadata(request.ProviderSpecific)
	request.ProviderSpecific[personalAssistantRouteResolvedKey] = "true"
	request.ProviderSpecific[agentcore.DedicatedLogicalMemoryCaptureProviderKey] = "true"
	request.LogicalAgentID = discussionsession.PersonalAssistantAgentID(tenantID, ownerID)
	if request.LogicalAgentID == "" {
		return discussionsession.NewRuntimeSetupError("personal_assistant", "personal assistant logical identity is unavailable")
	}
	request.ProviderSpecific[personalAssistantLogicalAgentIDKey] = request.LogicalAgentID
	profileID := strings.TrimSpace(view.AgentProfileID)
	if profileID == "" || profileID != request.LogicalAgentID {
		return discussionsession.NewRuntimeSetupError("personal_assistant", "personal assistant AgentProfile identity is unavailable")
	}
	request.AgentID = profileID
	request.ProviderSpecific[personalAssistantAssignedAgentKey] = profileID
	request.PersonaPrompt = firstNonBlank(view.SystemPrompt, personalAssistantPersonaPrompt)
	if request.ConversationKind == discussionsession.ConversationKindPersonalAssistant {
		request.ConversationTenantID, request.ConversationOwnerID = tenantID, ownerID
	}
	if request.Viewer.TenantID == "" {
		request.Viewer.TenantID = tenantID
	}
	r.attachNotificationContext(ctx, request)
	applyProductionPersonalAssistantApprovalDialogue(ctx, request)

	if view.HostingMode == devicedirectory.HostingModeManaged || strings.TrimSpace(view.HostingMode) == "" {
		setManagedPersonalAssistantRoute(request)
		return r.attachSelectedRunnerBindings(ctx, request)
	}
	if view.HostingMode != devicedirectory.HostingModeOwnedAgent {
		return discussionsession.NewRuntimeSetupError("personal_assistant", "personal assistant hosting mode is invalid")
	}

	ownedAgent, found := selectedPersonalAssistantOwnedAgent(view)
	if !found {
		return unavailablePersonalAssistantRuntime("local_runtime_missing")
	}
	if ownedAgent.Status != devicedirectory.AgentStatusOnline {
		return unavailablePersonalAssistantRuntime("local_runtime_" + firstNonBlank(ownedAgent.Status, "offline"))
	}
	grant, found := selectedPersonalAssistantWorkspaceGrant(ownedAgent, view.WorkspaceGrantID)
	if !found || !grant.Available {
		return unavailablePersonalAssistantRuntime("workspace_grant_unavailable")
	}
	config, err := json.Marshal(productionPersonalAssistantDeviceConfig{
		DeviceID:             ownedAgent.DeviceID,
		WorkspaceGrantID:     grant.GrantID,
		RequiredCapabilities: append([]string(nil), grant.Capabilities...),
	})
	if err != nil {
		return discussionsession.NewRuntimeSetupError("device", "personal assistant device binding is invalid")
	}
	request.RuntimeProviderConfig = config
	request.ProviderSpecific[personalAssistantRouteTargetKey] = personalAssistantRouteDevice
	request.ProviderSpecific[personalAssistantHostingModeKey] = devicedirectory.HostingModeOwnedAgent
	request.ProviderSpecific[personalAssistantOwnedRuntimeKey] = strings.TrimSpace(ownedAgent.RuntimeKind)
	request.ProviderSpecific[personalAssistantFallbackKey] = "false"
	request.ProviderSpecific["runtime_kind"] = personalAssistantRouteDevice
	return r.attachSelectedRunnerBindings(ctx, request)
}

func (r *productionPersonalAssistantDiscussionAgentRunner) setNotificationContext(provider productionPersonalAssistantNotificationContextProvider) {
	r.notificationMu.Lock()
	defer r.notificationMu.Unlock()
	r.notificationContext = provider
}

func (r *productionPersonalAssistantDiscussionAgentRunner) attachNotificationContext(ctx context.Context, request *discussionsession.DiscussionAgentRequest) {
	r.notificationMu.RLock()
	provider := r.notificationContext
	r.notificationMu.RUnlock()
	if provider == nil || request.ConversationKind != discussionsession.ConversationKindPersonalAssistant {
		return
	}
	identity, _ := json.Marshal([]any{request.RunID, request.MessageID, request.ConversationID,
		request.ConversationKind, request.ConversationTenantID, request.ConversationOwnerID,
		request.AgentID, request.LogicalAgentID, request.Viewer, request.ExecutionPrincipal})
	scope := fmt.Sprintf("%x", sha256.Sum256(identity))
	if request.HasPreparedNotificationContext(scope) {
		return
	}
	noticeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	material, err := provider.NotificationContext(noticeCtx, *request)
	if err != nil {
		material = "通知状态暂时无法读取；不要猜测提醒内容或声称已经批准、拒绝、送达。"
	}
	request.SetPreparedNotificationContext(scope, material)
}

func (r *productionPersonalAssistantDiscussionAgentRunner) RunDiscussion(ctx context.Context, request discussionsession.DiscussionAgentRequest) (discussionsession.DiscussionAgentResponse, error) {
	if err := r.ensurePersonalAssistantRoute(ctx, &request); err != nil {
		return discussionsession.DiscussionAgentResponse{}, err
	}
	request.ConsumePreparedNotificationContext()
	runner, target, err := r.runnerForRequest(ctx, request)
	if err != nil {
		return discussionsession.DiscussionAgentResponse{}, err
	}
	if runner == nil {
		return discussionsession.DiscussionAgentResponse{}, discussionsession.ErrRuntimeUnavailable
	}
	if request.RunID != "" {
		r.active.Store(request.RunID, productionPersonalAssistantActiveRun{runner: runner, target: target})
		defer r.active.Delete(request.RunID)
	}
	response, err := runner.RunDiscussion(ctx, request)
	return response, err
}

func (r *productionPersonalAssistantDiscussionAgentRunner) RunDiscussionStream(ctx context.Context, request discussionsession.DiscussionAgentRequest) (<-chan discussionsession.DiscussionAgentStreamEvent, error) {
	if err := r.ensurePersonalAssistantRoute(ctx, &request); err != nil {
		return nil, err
	}
	request.ConsumePreparedNotificationContext()
	runner, target, err := r.runnerForRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	streamer, ok := runner.(discussionsession.DiscussionAgentStreamRunner)
	if !ok {
		return nil, discussionsession.ErrRuntimeUnavailable
	}
	events, err := streamer.RunDiscussionStream(ctx, request)
	if err != nil {
		return nil, err
	}
	if request.RunID != "" {
		r.active.Store(request.RunID, productionPersonalAssistantActiveRun{runner: runner, target: target})
	}
	out := make(chan discussionsession.DiscussionAgentStreamEvent, 16)
	go r.forwardPersonalAssistantStream(ctx, out, events, request, target)
	return out, nil
}

func (r *productionPersonalAssistantDiscussionAgentRunner) forwardPersonalAssistantStream(
	ctx context.Context,
	out chan<- discussionsession.DiscussionAgentStreamEvent,
	events <-chan discussionsession.DiscussionAgentStreamEvent,
	request discussionsession.DiscussionAgentRequest,
	target string,
) {
	defer close(out)
	if request.RunID != "" {
		defer r.active.Delete(request.RunID)
	}
	for event := range events {
		if !r.sendPersonalAssistantStreamEvent(ctx, out, event) {
			return
		}
	}
}

func (r *productionPersonalAssistantDiscussionAgentRunner) sendPersonalAssistantStreamEvent(ctx context.Context, out chan<- discussionsession.DiscussionAgentStreamEvent, event discussionsession.DiscussionAgentStreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func (r *productionPersonalAssistantDiscussionAgentRunner) PrewarmDiscussion(ctx context.Context, request discussionsession.DiscussionAgentRequest) error {
	if err := r.ensurePersonalAssistantRoute(ctx, &request); err != nil {
		return err
	}
	request.ConsumePreparedNotificationContext()
	runner, target, err := r.runnerForRequest(ctx, request)
	if err != nil {
		return err
	}
	if target == personalAssistantRouteDevice {
		return nil
	}
	prewarmer, ok := runner.(discussionsession.DiscussionAgentPrewarmer)
	if !ok {
		return nil
	}
	return prewarmer.PrewarmDiscussion(ctx, request)
}

func (r *productionPersonalAssistantDiscussionAgentRunner) SteerDiscussionRun(ctx context.Context, run discussionsession.RunRecord, message string) error {
	runner := r.managed
	if active, ok := r.active.Load(run.RunID); ok {
		state, _ := active.(productionPersonalAssistantActiveRun)
		runner = state.runner
	} else if strings.EqualFold(strings.TrimSpace(run.RuntimeKind), personalAssistantRouteDevice) {
		runner = r.device
	}
	steerer, ok := runner.(discussionsession.DiscussionAgentRunSteerer)
	if !ok {
		return discussionsession.ErrRuntimeUnavailable
	}
	return steerer.SteerDiscussionRun(ctx, run, message)
}

func (r *productionPersonalAssistantDiscussionAgentRunner) PromptDebug(request discussionsession.DiscussionAgentRequest) *discussionsession.DiscussionAgentPromptDebug {
	runner, _, _ := r.runnerForRequest(context.Background(), request)
	debugger, ok := runner.(discussionsession.DiscussionAgentPromptDebugger)
	if !ok {
		return nil
	}
	return debugger.PromptDebug(request)
}

func (r *productionPersonalAssistantDiscussionAgentRunner) DiscussionAgentID() string {
	provider, ok := r.managed.(discussionsession.DiscussionAgentIdentityProvider)
	if !ok {
		return ""
	}
	return provider.DiscussionAgentID()
}

func (r *productionPersonalAssistantDiscussionAgentRunner) DiscussionAgentBoundTools() []discussionsession.DiscussionAgentBoundTool {
	provider, ok := r.managed.(discussionsession.DiscussionAgentBoundToolProvider)
	if !ok {
		return nil
	}
	return provider.DiscussionAgentBoundTools()
}

func (r *productionPersonalAssistantDiscussionAgentRunner) DiscussionAgentBoundSkills() []discussionsession.DiscussionAgentBoundSkill {
	provider, ok := r.managed.(discussionsession.DiscussionAgentBoundSkillProvider)
	if !ok {
		return nil
	}
	return provider.DiscussionAgentBoundSkills()
}

func (r *productionPersonalAssistantDiscussionAgentRunner) ResolveDiscussionRunAgentID(ctx context.Context, conversation discussionsession.ConversationRecordView) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(conversation.ConversationKind), discussionsession.ConversationKindPersonalAssistant) {
		return r.DiscussionAgentID(), nil
	}
	ownerID := firstNonBlank(conversation.OwnerID, conversation.CreatedBy)
	view, err := r.directory.GetPersonalAssistant(ctx, devicedirectory.OwnerScope{TenantID: conversation.TenantID, OwnerID: ownerID})
	if err != nil {
		return "", discussionsession.NewRuntimeSetupError("personal_assistant", fmt.Sprintf("load hosting configuration: %v", err))
	}
	return firstNonBlank(view.AgentProfileID, r.DiscussionAgentID()), nil
}

func (r *productionPersonalAssistantDiscussionAgentRunner) ResolveDiscussionRunAgentDisplayName(ctx context.Context, conversation discussionsession.ConversationRecordView) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(conversation.ConversationKind), discussionsession.ConversationKindPersonalAssistant) && (strings.TrimSpace(conversation.PersonalAssistantOriginOwnerID) == "" || strings.TrimSpace(conversation.PersonalAssistantOriginSubjectID) == "") {
		return "", nil
	}
	ownerID := firstNonBlank(conversation.OwnerID, conversation.PersonalAssistantOriginOwnerID, conversation.CreatedBy)
	view, err := r.directory.GetPersonalAssistant(ctx, devicedirectory.OwnerScope{TenantID: conversation.TenantID, OwnerID: ownerID})
	if err != nil {
		return "", discussionsession.NewRuntimeSetupError("personal_assistant", fmt.Sprintf("load personal assistant profile: %v", err))
	}
	return firstNonBlank(view.DisplayName, defaultPersonalAssistantDisplayName), nil
}

func (r *productionPersonalAssistantDiscussionAgentRunner) DeleteDiscussionRuntimeRecord(ctx context.Context, runID string) error {
	var cleanup []error
	for _, runner := range []discussionsession.DiscussionAgentRunner{r.managed, r.device} {
		if cleaner, ok := runner.(discussionsession.DiscussionRuntimeRecordCleaner); ok {
			cleanup = append(cleanup, cleaner.DeleteDiscussionRuntimeRecord(ctx, runID))
		}
	}
	if cleaner, ok := r.resolver.(discussionsession.DiscussionRuntimeRecordCleaner); ok {
		cleanup = append(cleanup, cleaner.DeleteDiscussionRuntimeRecord(ctx, runID))
	}
	return errors.Join(cleanup...)
}

func (r *productionPersonalAssistantDiscussionAgentRunner) ReleaseRuntimeWorkspace(ctx context.Context, workspaceRoot string) error {
	var cleanup []error
	for _, runner := range []discussionsession.DiscussionAgentRunner{r.managed, r.device} {
		if releaser, ok := runner.(discussionsession.DiscussionRuntimeWorkspaceReleaser); ok {
			cleanup = append(cleanup, releaser.ReleaseRuntimeWorkspace(ctx, workspaceRoot))
		}
	}
	if releaser, ok := r.resolver.(discussionsession.DiscussionRuntimeWorkspaceReleaser); ok {
		cleanup = append(cleanup, releaser.ReleaseRuntimeWorkspace(ctx, workspaceRoot))
	}
	return errors.Join(cleanup...)
}

func (r *productionPersonalAssistantDiscussionAgentRunner) ensurePersonalAssistantRoute(ctx context.Context, request *discussionsession.DiscussionAgentRequest) error {
	if request == nil || !discussionsession.IsPersonalAssistantRuntimeRequest(request) {
		return nil
	}
	return r.RouteDiscussionAgentRequest(ctx, request)
}

func (r *productionPersonalAssistantDiscussionAgentRunner) runnerForRequest(ctx context.Context, request discussionsession.DiscussionAgentRequest) (discussionsession.DiscussionAgentRunner, string, error) {
	target := personalAssistantRouteManaged
	if discussionsession.IsPersonalAssistantRuntimeRequest(&request) &&
		request.ProviderSpecific[personalAssistantRouteResolvedKey] == "true" &&
		request.ProviderSpecific[personalAssistantRouteTargetKey] == personalAssistantRouteDevice {
		target = personalAssistantRouteDevice
	}
	assignedAgentID := strings.TrimSpace(request.ProviderSpecific[personalAssistantAssignedAgentKey])
	if assignedAgentID != "" {
		if r.resolver == nil {
			return nil, target, discussionsession.NewRuntimeSetupError("personal_assistant", "personal assistant AgentProfile runtime resolver is unavailable")
		}
		tenantID := firstNonBlank(request.ConversationTenantID, request.Viewer.TenantID)
		runner, err := r.resolver.ResolvePersonalAssistantRunner(ctx, tenantID, assignedAgentID, target)
		if err != nil {
			return nil, target, discussionsession.NewRuntimeSetupError("personal_assistant", fmt.Sprintf("resolve personal assistant AgentProfile %s: %v", assignedAgentID, err))
		}
		return runner, target, nil
	}
	if target == personalAssistantRouteDevice {
		return r.device, target, nil
	}
	return r.managed, target, nil
}

func (r *productionPersonalAssistantDiscussionAgentRunner) attachSelectedRunnerBindings(ctx context.Context, request *discussionsession.DiscussionAgentRequest) error {
	runner, _, err := r.runnerForRequest(ctx, *request)
	if err != nil {
		return err
	}
	if provider, ok := runner.(discussionsession.DiscussionAgentBoundToolProvider); ok {
		request.BoundTools = provider.DiscussionAgentBoundTools()
	}
	if provider, ok := runner.(discussionsession.DiscussionAgentBoundSkillProvider); ok {
		request.BoundSkills = provider.DiscussionAgentBoundSkills()
	}
	return nil
}

func selectedPersonalAssistantOwnedAgent(view devicedirectory.PersonalAssistantView) (devicedirectory.OwnedAgentView, bool) {
	selectedID := strings.TrimSpace(view.OwnedAgentID)
	for _, ownedAgent := range view.OwnedAgents {
		if strings.TrimSpace(ownedAgent.OwnedAgentID) == selectedID {
			return ownedAgent, true
		}
	}
	return devicedirectory.OwnedAgentView{}, false
}

func selectedPersonalAssistantWorkspaceGrant(agent devicedirectory.OwnedAgentView, grantID string) (devicedirectory.WorkspaceGrantDescriptor, bool) {
	grantID = strings.TrimSpace(grantID)
	for _, grant := range agent.WorkspaceGrants {
		if strings.TrimSpace(grant.GrantID) == grantID {
			return grant, true
		}
	}
	return devicedirectory.WorkspaceGrantDescriptor{}, false
}

func unavailablePersonalAssistantRuntime(reason string) error {
	return discussionsession.NewRuntimeSetupError("device", "personal assistant owned agent is unavailable: "+strings.TrimSpace(reason))
}

func setManagedPersonalAssistantRoute(request *discussionsession.DiscussionAgentRequest) {
	request.RuntimeProviderConfig = nil
	request.ProviderSpecific[personalAssistantRouteTargetKey] = personalAssistantRouteManaged
	request.ProviderSpecific[personalAssistantHostingModeKey] = devicedirectory.HostingModeManaged
	request.ProviderSpecific[personalAssistantFallbackKey] = "false"
	delete(request.ProviderSpecific, personalAssistantOwnedRuntimeKey)
	delete(request.ProviderSpecific, "runtime_kind")
}

func clonePersonalAssistantMetadata(source map[string]string) map[string]string {
	out := make(map[string]string, len(source)+6)
	for key, value := range source {
		out[key] = value
	}
	return out
}

func applyProductionPersonalAssistantApprovalDialogue(ctx context.Context, request *discussionsession.DiscussionAgentRequest) {
	if request == nil {
		return
	}
	prompt := productionPersonalAssistantApprovalDialogueFromContext(ctx)
	if prompt == "" {
		return
	}
	request.PersonalAssistantApprovalContext = prompt
	if !strings.Contains(request.PersonaPrompt, prompt) {
		request.PersonaPrompt = strings.TrimSpace(request.PersonaPrompt) + "\n\n" + prompt
	}
	if request.ProviderSpecific == nil {
		request.ProviderSpecific = map[string]string{}
	}
	request.ProviderSpecific[personalAssistantApprovalDialogueKey] = "true"
	if !isProductionPersonalAssistantApprovalInquiry(firstNonBlank(request.TriggerMessage.Content, request.Content)) {
		return
	}
	request.ForceNewSession = true
	request.ProviderSpecific[discussionsession.PersonalAssistantReadOnlyReviewProviderKey] = "true"
}

func isProductionPersonalAssistantApprovalInquiry(content string) bool {
	content = strings.ToLower(strings.TrimSpace(content))
	if content == "" {
		return false
	}
	switch strings.Trim(content, " \t\r\n?!？。！") {
	case "审批", "待审批", "approval", "pending approvals":
		return true
	}
	for _, marker := range []string{
		"解释审批", "查看审批", "看看审批", "审批状态", "要我同意", "要我拒绝", "要我决定", "需要我决定",
		"同意后", "拒绝后", "有什么风险", "有啥风险",
		"这是什么", "这是啥", "申请什么", "什么内容", "什么意思", "怎么回事",
		"看不懂", "没看懂", "听不懂", "会发生什么", "有什么影响", "有啥影响",
		"explain the approval", "explain this approval", "pending approval", "approval status", "what happens if", "should i approve", "should i reject",
	} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

var _ discussionsession.DiscussionAgentRunner = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionAgentStreamRunner = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionAgentPrewarmer = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionAgentRequestRouter = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionAgentRunSteerer = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionAgentPromptDebugger = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionAgentIdentityProvider = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionAgentRunIdentityResolver = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionAgentRunDisplayNameResolver = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionRuntimeRecordCleaner = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
var _ discussionsession.DiscussionRuntimeWorkspaceReleaser = (*productionPersonalAssistantDiscussionAgentRunner)(nil)
