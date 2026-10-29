package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	devicedirectory "orrery_v2/internal/device/directory"
)

func TestProductionPersonalAssistantRunnerRoutesOwnedAgentThroughDeviceGrant(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent", response: discussionsession.DiscussionAgentResponse{AssistantMessage: "managed", RuntimeKind: "pi"}}
	device := &recordingPersonalAssistantRunner{id: "lead-agent", response: discussionsession.DiscussionAgentResponse{AssistantMessage: "local", RuntimeKind: "device"}}
	directory := &recordingPersonalAssistantDirectory{view: ownedPersonalAssistantView(devicedirectory.AgentStatusOnline, true)}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory, &recordingPersonalAssistantRunnerResolver{runner: device})

	response, err := runner.RunDiscussion(context.Background(), personalAssistantRuntimeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.AssistantMessage != "local" || device.calls != 1 || managed.calls != 0 {
		t.Fatalf("response=%+v managed_calls=%d device_calls=%d", response, managed.calls, device.calls)
	}
	if directory.scope.TenantID != "tenant-a" || directory.scope.OwnerID != "user-1" {
		t.Fatalf("directory scope = %+v", directory.scope)
	}
	var config productionPersonalAssistantDeviceConfig
	if err := json.Unmarshal(device.request.RuntimeProviderConfig, &config); err != nil {
		t.Fatal(err)
	}
	if config.DeviceID != "device-a" || config.WorkspaceGrantID != "grant-a" || len(config.RequiredCapabilities) != 2 {
		t.Fatalf("device config = %+v", config)
	}
	if device.request.ProviderSpecific["runtime_kind"] != "device" || device.request.ProviderSpecific[personalAssistantOwnedRuntimeKey] != "hermes" {
		t.Fatalf("provider metadata = %+v", device.request.ProviderSpecific)
	}
	if device.request.ExecutionPrincipal.Kind != agentcore.ExecutionPrincipalUser || device.request.ExecutionPrincipal.ID != "user-1" || device.request.ExecutionPrincipal.Source != "system_assistant" {
		t.Fatalf("execution principal = %+v", device.request.ExecutionPrincipal)
	}
	if len(device.request.ExecutionPrincipal.PermissionScope) != 2 || device.request.ExecutionPrincipal.PermissionScope[0] != "mission.read" {
		t.Fatalf("execution permission scope = %+v", device.request.ExecutionPrincipal.PermissionScope)
	}
}

func TestProductionPersonalAssistantRunnerUsesUserIdentityOnManagedRuntime(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent", response: discussionsession.DiscussionAgentResponse{AssistantMessage: "managed", RuntimeKind: "pi"}}
	device := &recordingPersonalAssistantRunner{id: "lead-agent"}
	directory := &recordingPersonalAssistantDirectory{view: devicedirectory.PersonalAssistantView{
		AssistantEnabled: true, HostingMode: devicedirectory.HostingModeManaged,
	}}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory, &recordingPersonalAssistantRunnerResolver{runner: managed})

	response, err := runner.RunDiscussion(context.Background(), personalAssistantRuntimeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.AssistantMessage != "managed" || managed.calls != 1 || device.calls != 0 {
		t.Fatalf("response=%+v managed_calls=%d device_calls=%d", response, managed.calls, device.calls)
	}
	principal := managed.request.ExecutionPrincipal
	if principal.Kind != agentcore.ExecutionPrincipalUser || principal.ID != "user-1" || principal.TenantID != "tenant-a" || principal.Source != "system_assistant" {
		t.Fatalf("execution principal = %+v", principal)
	}
	if managed.request.ProviderSpecific[personalAssistantFallbackKey] != "false" {
		t.Fatalf("provider metadata = %+v", managed.request.ProviderSpecific)
	}
	if managed.request.ProviderSpecific[agentcore.DedicatedLogicalMemoryCaptureProviderKey] != "true" {
		t.Fatalf("dedicated memory capture metadata = %+v", managed.request.ProviderSpecific)
	}
	if managed.request.PersonaPrompt != personalAssistantPersonaPrompt {
		t.Fatalf("personal assistant persona = %q", managed.request.PersonaPrompt)
	}
}

func TestProductionPersonalAssistantRunnerAddsPrivateApprovalDialogueToConfiguredPersona(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent", response: discussionsession.DiscussionAgentResponse{AssistantMessage: "解释审批", RuntimeKind: "pi"}}
	device := &recordingPersonalAssistantRunner{id: "lead-agent"}
	directory := &recordingPersonalAssistantDirectory{view: devicedirectory.PersonalAssistantView{
		AssistantEnabled: true, HostingMode: devicedirectory.HostingModeManaged,
		SystemPrompt: "只服务当前用户。",
	}}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory, &recordingPersonalAssistantRunnerResolver{runner: managed})
	ctx := withProductionPersonalAssistantApprovalContext(context.Background(), productionPersonalAssistantApprovalContext{
		Title: "客户邮件审批", Material: "同意后会向客户发送邮件。",
	})
	request := personalAssistantRuntimeRequest()
	request.Content = "我咋听不懂这在申请什么内容"

	if _, err := runner.RunDiscussion(ctx, request); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"只服务当前用户。", personalAssistantApprovalDialogueMarker, "客户邮件审批", "不得因此调用工具"} {
		if !strings.Contains(managed.request.PersonaPrompt, expected) {
			t.Fatalf("personal assistant approval persona missing %q: %s", expected, managed.request.PersonaPrompt)
		}
	}
	if managed.request.ProviderSpecific[personalAssistantApprovalDialogueKey] != "true" {
		t.Fatalf("approval dialogue metadata = %+v", managed.request.ProviderSpecific)
	}
	if !strings.Contains(managed.request.PersonalAssistantApprovalContext, "客户邮件审批") {
		t.Fatalf("server-owned approval context = %q", managed.request.PersonalAssistantApprovalContext)
	}
	if !managed.request.ForceNewSession {
		t.Fatalf("approval inquiry reused a prior runtime session: %+v", managed.request)
	}
	if managed.request.ProviderSpecific[discussionsession.PersonalAssistantReadOnlyReviewProviderKey] != "true" {
		t.Fatalf("approval inquiry was not read-only: %+v", managed.request.ProviderSpecific)
	}
}

func TestProductionPersonalAssistantRunnerKeepsUnrelatedTurnOnExistingSessionWhileApprovalIsPending(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent", response: discussionsession.DiscussionAgentResponse{AssistantMessage: "整理会议", RuntimeKind: "pi"}}
	device := &recordingPersonalAssistantRunner{id: "lead-agent"}
	directory := &recordingPersonalAssistantDirectory{view: devicedirectory.PersonalAssistantView{
		AssistantEnabled: true, HostingMode: devicedirectory.HostingModeManaged,
		SystemPrompt: "只服务当前用户。",
	}}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory, &recordingPersonalAssistantRunnerResolver{runner: managed})
	ctx := withProductionPersonalAssistantApprovalContext(context.Background(), productionPersonalAssistantApprovalContext{
		Title: "客户邮件审批", Material: "同意后会向客户发送邮件。",
	})
	request := personalAssistantRuntimeRequest()
	request.Content = "帮我决定明天会议做什么"

	if _, err := runner.RunDiscussion(ctx, request); err != nil {
		t.Fatal(err)
	}
	if managed.request.ForceNewSession {
		t.Fatalf("unrelated personal-assistant turn unexpectedly forced a new session: %+v", managed.request)
	}
	if managed.request.ProviderSpecific[discussionsession.PersonalAssistantReadOnlyReviewProviderKey] != "" {
		t.Fatalf("unrelated personal-assistant turn unexpectedly lost tools: %+v", managed.request.ProviderSpecific)
	}
	if !strings.Contains(managed.request.PersonalAssistantApprovalContext, "客户邮件审批") {
		t.Fatalf("pending approval context was not retained: %q", managed.request.PersonalAssistantApprovalContext)
	}
}

func TestProductionPersonalAssistantRunnerUsesIndependentAgentProfile(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent"}
	device := &recordingPersonalAssistantRunner{id: "lead-agent"}
	assigned := &recordingPersonalAssistantRunner{id: "organization-agent", response: discussionsession.DiscussionAgentResponse{AssistantMessage: "assigned", RuntimeKind: "pi"}}
	directory := &recordingPersonalAssistantDirectory{view: devicedirectory.PersonalAssistantView{
		AssistantEnabled: true, AssignedAgentID: "organization-agent", HostingMode: devicedirectory.HostingModeManaged,
		DisplayName: "我的助理", SystemPrompt: "只处理我明确授权的任务。",
	}}
	resolver := &recordingPersonalAssistantRunnerResolver{runner: assigned}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory, resolver)

	response, err := runner.RunDiscussion(context.Background(), personalAssistantRuntimeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.AssistantMessage != "assigned" || assigned.calls != 1 || managed.calls != 0 {
		t.Fatalf("response=%+v assigned_calls=%d managed_calls=%d", response, assigned.calls, managed.calls)
	}
	personalAssistantID := discussionsession.PersonalAssistantAgentID("tenant-a", "user-1")
	if resolver.tenantID != "tenant-a" || resolver.agentID != personalAssistantID || resolver.target != personalAssistantRouteManaged {
		t.Fatalf("resolver selection = tenant %q agent %q target %q", resolver.tenantID, resolver.agentID, resolver.target)
	}
	if assigned.request.AgentID != personalAssistantID || assigned.request.LogicalAgentID != personalAssistantID {
		t.Fatalf("assigned request identity = %+v", assigned.request)
	}
	if assigned.request.PersonaPrompt != "只处理我明确授权的任务。" {
		t.Fatalf("personal assistant prompt = %q", assigned.request.PersonaPrompt)
	}
	resolvedID, err := runner.ResolveDiscussionRunAgentID(context.Background(), discussionsession.ConversationRecordView{ConversationRecord: discussionsession.ConversationRecord{
		TenantID: "tenant-a", OwnerID: "user-1", CreatedBy: "human-1", ConversationKind: discussionsession.ConversationKindPersonalAssistant,
	}})
	if err != nil || resolvedID != personalAssistantID {
		t.Fatalf("run agent identity = %q, err=%v", resolvedID, err)
	}
	resolvedName, err := runner.ResolveDiscussionRunAgentDisplayName(context.Background(), discussionsession.ConversationRecordView{ConversationRecord: discussionsession.ConversationRecord{
		TenantID: "tenant-a", OwnerID: "user-1", CreatedBy: "human-1", ConversationKind: discussionsession.ConversationKindPersonalAssistant,
	}})
	if err != nil || resolvedName != "我的助理" {
		t.Fatalf("run agent display name = %q, err=%v", resolvedName, err)
	}
}

func TestProductionPersonalAssistantRunnerRejectsOfflineOwnedAgent(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent"}
	device := &recordingPersonalAssistantRunner{id: "lead-agent"}
	directory := &recordingPersonalAssistantDirectory{view: ownedPersonalAssistantView(devicedirectory.AgentStatusOffline, true)}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory)

	_, err := runner.RunDiscussion(context.Background(), personalAssistantRuntimeRequest())
	if !errors.Is(err, discussionsession.ErrRuntimeUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if managed.calls != 0 || device.calls != 0 {
		t.Fatalf("unavailable route executed managed=%d device=%d", managed.calls, device.calls)
	}
}

func TestProductionPersonalAssistantRunnerRejectsOfflineOwnedAgentWithoutFallback(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent"}
	device := &recordingPersonalAssistantRunner{id: "lead-agent"}
	directory := &recordingPersonalAssistantDirectory{view: ownedPersonalAssistantView(devicedirectory.AgentStatusOffline, false)}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory)

	_, err := runner.RunDiscussion(context.Background(), personalAssistantRuntimeRequest())
	if !errors.Is(err, discussionsession.ErrRuntimeUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if managed.calls != 0 || device.calls != 0 {
		t.Fatalf("unavailable route executed managed=%d device=%d", managed.calls, device.calls)
	}
}

func TestProductionPersonalAssistantRunnerDoesNotUseLegacyCloudFallback(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent"}
	device := &recordingPersonalAssistantRunner{id: "lead-agent"}
	view := ownedPersonalAssistantView(devicedirectory.AgentStatusOffline, false)
	view.FallbackToManaged = true
	directory := &recordingPersonalAssistantDirectory{view: view}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory)

	_, err := runner.RunDiscussion(context.Background(), personalAssistantRuntimeRequest())
	if !errors.Is(err, discussionsession.ErrRuntimeUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if managed.calls != 0 || device.calls != 0 {
		t.Fatalf("legacy fallback executed managed=%d device=%d", managed.calls, device.calls)
	}
}

func TestProductionPersonalAssistantRunnerDoesNotFallbackAfterDeviceSetupFailure(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent"}
	device := &recordingPersonalAssistantRunner{id: "lead-agent", err: discussionsession.NewRuntimeSetupError("device", "work order was rejected")}
	directory := &recordingPersonalAssistantDirectory{view: ownedPersonalAssistantView(devicedirectory.AgentStatusOnline, true)}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory, &recordingPersonalAssistantRunnerResolver{runner: device})

	_, err := runner.RunDiscussion(context.Background(), personalAssistantRuntimeRequest())
	if !errors.Is(err, discussionsession.ErrRuntimeUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if device.calls != 1 || managed.calls != 0 {
		t.Fatalf("managed_calls=%d device_calls=%d", managed.calls, device.calls)
	}
}

func TestProductionPersonalAssistantRunnerDoesNotFallbackBeforeStreamingDeviceOutput(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent"}
	device := &recordingPersonalAssistantRunner{id: "lead-agent", stream: []discussionsession.DiscussionAgentStreamEvent{{
		Kind:  discussionsession.DiscussionAgentStreamRuntimeError,
		Error: discussionsession.NewRuntimeSetupError("device", "device admission closed"),
	}}}
	directory := &recordingPersonalAssistantDirectory{view: ownedPersonalAssistantView(devicedirectory.AgentStatusOnline, true)}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory, &recordingPersonalAssistantRunnerResolver{runner: device})

	events, err := runner.RunDiscussionStream(context.Background(), personalAssistantRuntimeRequest())
	if err != nil {
		t.Fatal(err)
	}
	var received []discussionsession.DiscussionAgentStreamEvent
	for event := range events {
		received = append(received, event)
	}
	if device.streamCalls != 1 || managed.streamCalls != 0 || len(received) != 1 {
		t.Fatalf("events=%+v managed_calls=%d device_calls=%d", received, managed.streamCalls, device.streamCalls)
	}
	if received[0].Kind != discussionsession.DiscussionAgentStreamRuntimeError || !errors.Is(received[0].Error, discussionsession.ErrRuntimeUnavailable) {
		t.Fatalf("stream event = %+v", received[0])
	}
}

func TestProductionPersonalAssistantRunnerLeavesRegularDiscussionOnManagedRuntime(t *testing.T) {
	managed := &recordingPersonalAssistantRunner{id: "lead-agent", response: discussionsession.DiscussionAgentResponse{AssistantMessage: "group", RuntimeKind: "pi"}}
	device := &recordingPersonalAssistantRunner{id: "lead-agent"}
	directory := &recordingPersonalAssistantDirectory{err: errors.New("must not be called")}
	runner := newProductionPersonalAssistantDiscussionAgentRunner(managed, device, directory)
	request := personalAssistantRuntimeRequest()
	request.ConversationKind = discussionsession.ConversationKindGroup

	response, err := runner.RunDiscussion(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.AssistantMessage != "group" || directory.calls != 0 || managed.calls != 1 || device.calls != 0 {
		t.Fatalf("response=%+v directory=%d managed=%d device=%d", response, directory.calls, managed.calls, device.calls)
	}
}

type recordingPersonalAssistantDirectory struct {
	view  devicedirectory.PersonalAssistantView
	err   error
	calls int
	scope devicedirectory.OwnerScope
}

func (d *recordingPersonalAssistantDirectory) GetPersonalAssistant(_ context.Context, scope devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantView, error) {
	d.calls++
	d.scope = scope
	view := d.view
	if view.AgentProfileID == "" {
		view.AgentProfileID = discussionsession.PersonalAssistantAgentID(scope.TenantID, scope.OwnerID)
	}
	if view.DisplayName == "" {
		view.DisplayName = defaultPersonalAssistantDisplayName
	}
	return view, d.err
}

type recordingPersonalAssistantRunner struct {
	id          string
	response    discussionsession.DiscussionAgentResponse
	err         error
	stream      []discussionsession.DiscussionAgentStreamEvent
	streamErr   error
	calls       int
	streamCalls int
	request     discussionsession.DiscussionAgentRequest
}

type recordingPersonalAssistantRunnerResolver struct {
	runner   discussionsession.DiscussionAgentRunner
	err      error
	tenantID string
	agentID  string
	target   string
}

func (r *recordingPersonalAssistantRunnerResolver) ResolvePersonalAssistantRunner(_ context.Context, tenantID string, agentID string, target string) (discussionsession.DiscussionAgentRunner, error) {
	r.tenantID = tenantID
	r.agentID = agentID
	r.target = target
	return r.runner, r.err
}

func (r *recordingPersonalAssistantRunner) RunDiscussion(_ context.Context, request discussionsession.DiscussionAgentRequest) (discussionsession.DiscussionAgentResponse, error) {
	r.calls++
	r.request = request
	return r.response, r.err
}

func (r *recordingPersonalAssistantRunner) RunDiscussionStream(_ context.Context, request discussionsession.DiscussionAgentRequest) (<-chan discussionsession.DiscussionAgentStreamEvent, error) {
	r.streamCalls++
	r.request = request
	if r.streamErr != nil {
		return nil, r.streamErr
	}
	out := make(chan discussionsession.DiscussionAgentStreamEvent, len(r.stream))
	for _, event := range r.stream {
		out <- event
	}
	close(out)
	return out, nil
}

func (r *recordingPersonalAssistantRunner) DiscussionAgentID() string {
	return r.id
}

func personalAssistantRuntimeRequest() discussionsession.DiscussionAgentRequest {
	return discussionsession.DiscussionAgentRequest{
		ConversationID:       "conv-assistant",
		ConversationKind:     discussionsession.ConversationKindPersonalAssistant,
		ConversationOwnerID:  "user-1",
		ConversationTenantID: "tenant-a",
		RunID:                "run-1",
		Viewer: discussionsession.ViewerRef{
			ViewerID: "user-1", ViewerKind: discussionsession.ViewerKindUser, TenantID: "tenant-a",
			PermissionScope: []string{"mission.read", "personal_assistant.shell"},
		},
		Content: "Handle this",
	}
}

func ownedPersonalAssistantView(status string, fallback bool) devicedirectory.PersonalAssistantView {
	return devicedirectory.PersonalAssistantView{
		AssistantEnabled:  true,
		HostingMode:       devicedirectory.HostingModeOwnedAgent,
		OwnedAgentID:      "owned-a",
		WorkspaceGrantID:  "grant-a",
		FallbackToManaged: fallback,
		CloudFallback:     fallback,
		OwnedAgents: []devicedirectory.OwnedAgentView{{
			OwnedAgentID: "owned-a",
			DeviceID:     "device-a",
			DisplayName:  "Local Hermes",
			RuntimeKind:  devicedirectory.RuntimeKindHermes,
			Status:       status,
			WorkspaceGrants: []devicedirectory.WorkspaceGrantDescriptor{{
				GrantID:      "grant-a",
				DisplayName:  "Main workspace",
				Capabilities: []string{"workspace.read", "workspace.write"},
				Available:    true,
			}},
		}},
	}
}
