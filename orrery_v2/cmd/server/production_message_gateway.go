package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/outputpolicy"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/operations"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	gwdefaults "orrery_v2/internal/gateway/channel/defaults"
	gwconfig "orrery_v2/internal/gateway/config"
	gwcore "orrery_v2/internal/gateway/core"
	gwinbound "orrery_v2/internal/gateway/inbound"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/logging"
)

var errGatewayRouteInvalid = errors.New("gateway identity route is invalid")

type productionGatewayDiscussionService interface {
	SendMessageStream(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SendMessageCommand) (<-chan discussionsession.StreamEventDTO, error)
}

type productionGatewayAgentDirectory interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type productionGatewayAuditRecorder interface {
	RecordAuditEvent(ctx context.Context, event operations.AuditEventDTO) error
}

type productionMessageGatewayRunner struct {
	discussions      productionGatewayDiscussionService
	agents           productionGatewayAgentDirectory
	accounts         productionGatewayAccountLookup
	scopes           productionGatewayPermissionScopeResolver
	configStore      *gwconfig.Store
	bindings         *gwbindings.Store
	audit            productionGatewayAuditRecorder
	approvalReplies  productionPersonalAssistantApprovalReplyHandler
	approvalContexts productionPersonalAssistantApprovalContextProvider
	audio            *productionGatewayAudioService
	routes           productionGatewayRouteOptions

	mu            sync.Mutex
	conversations map[string]string
}

func newProductionMessageGatewayRunner(discussions productionGatewayDiscussionService, cfg ...gwconfig.Config) *productionMessageGatewayRunner {
	var routes productionGatewayRouteOptions
	if len(cfg) > 0 {
		routes = productionGatewayRouteOptionsFromConfig(cfg[0])
	}
	return &productionMessageGatewayRunner{discussions: discussions, routes: routes, conversations: map[string]string{}}
}

func (r *productionMessageGatewayRunner) SetAgentDirectory(agents productionGatewayAgentDirectory) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents = agents
}

func (r *productionMessageGatewayRunner) SetConfigStore(store *gwconfig.Store) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configStore = store
}

func (r *productionMessageGatewayRunner) SetBindingStore(store *gwbindings.Store) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindings = store
}

func (r *productionMessageGatewayRunner) SetAuditRecorder(recorder productionGatewayAuditRecorder) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audit = recorder
}

func (r *productionMessageGatewayRunner) UpdateConfig(cfg gwconfig.Config) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = productionGatewayRouteOptionsFromConfig(cfg)
}

func (r *productionMessageGatewayRunner) RunTurn(ctx context.Context, input gwprotocol.SubmitTurnInput) (<-chan gwprotocol.Event, error) {
	if r == nil || r.discussions == nil {
		return nil, fmt.Errorf("discussion service is required")
	}
	out := make(chan gwprotocol.Event, 16)
	go r.run(ctx, input, out)
	return out, nil
}

func (r *productionMessageGatewayRunner) run(ctx context.Context, input gwprotocol.SubmitTurnInput, out chan<- gwprotocol.Event) {
	defer close(out)
	if gatewayGroupMessage(input) {
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "group_not_approved"}
		return
	}
	sessionKey := strings.TrimSpace(input.SessionKey)
	if sessionKey == "" {
		sessionKey = string(input.ChannelKey) + ":" + gatewayHash(input.Message)
	}
	if input.ChannelKey == gwprotocol.ChannelWeixin {
		identity := r.weixinIdentity(input)
		if decision, ok := productionPersonalAssistantApprovalReplyDecision(input.Message); ok && identity.PersonalAssistant && !gatewayVoiceInput(input) {
			route := productionGatewayRoute{
				ViewerID: identity.ViewerID, UserID: identity.UserID, TenantID: identity.TenantID,
				OwnerID: identity.OwnerID, PersonalAssistant: true,
			}
			if err := r.validateGatewayRouteTenant(ctx, route); err != nil {
				emitGatewayRouteUnavailable(out)
				return
			}
			facts := r.handlePersonalAssistantApprovalReply(ctx, input, identity, decision, out)
			if facts == "" {
				return
			}
			ctx = discussionsession.WithOperationalFailure(ctx, facts)
		}
		if arg, ok := weixinSwitchCommand(input.Message); ok && !identity.PersonalAssistant && !gatewayVoiceInput(input) {
			if err := r.validateGatewayRouteTenant(ctx, productionGatewayRoute{
				AgentID:  identity.AgentID,
				ViewerID: identity.ViewerID,
				UserID:   identity.UserID,
				TenantID: identity.TenantID,
			}); err != nil {
				if !errors.Is(err, errGatewayRouteInvalid) {
					emitGatewayRouteUnavailable(out)
					return
				}
				identity = productionGatewayWeixinIdentity{}
			}
			r.handleWeixinSwitchCommand(ctx, input, identity, arg, out)
			return
		}
	}
	if input.ChannelKey == gwprotocol.ChannelFeishu || input.ChannelKey == gwprotocol.ChannelDingTalk {
		if code, ok := gatewayBindingCommand(input.Message); ok && !gatewayVoiceInput(input) {
			r.handleGatewayBindingCommand(ctx, input, code, out)
			return
		}
	}
	route, routed, routeErr := r.resolveRoute(ctx, input)
	if routeErr != nil {
		emitGatewayRouteUnavailable(out)
		return
	}
	if routed && route.TenantID != "" {
		if err := r.validateGatewayRouteTenant(ctx, route); err != nil {
			if !errors.Is(err, errGatewayRouteInvalid) {
				emitGatewayRouteUnavailable(out)
				return
			}
			routed = false
		}
	}
	if input.ChannelKey == gwprotocol.ChannelWeixin && !routed {
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayPermissionDenied}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "unregistered"}
		return
	}
	if (input.ChannelKey == gwprotocol.ChannelFeishu || input.ChannelKey == gwprotocol.ChannelDingTalk) && !routed {
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayPermissionDenied}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "unregistered"}
		return
	}
	useNormalDirectChat := isBoundDirectGatewayChannel(input.ChannelKey) && strings.TrimSpace(route.AgentID) != ""
	usePersonalAssistant := input.ChannelKey == gwprotocol.ChannelWeixin && route.PersonalAssistant
	if usePersonalAssistant {
		useNormalDirectChat = false
	}
	cmd := discussionsession.SendMessageCommand{
		Content:        strings.TrimSpace(input.Message),
		IdempotencyKey: firstNonBlank(input.RunID, gatewayHash(sessionKey+":"+input.Message)),
		AttachmentIDs:  gatewayAttachmentIDs(input.Attachments),
	}
	if usePersonalAssistant {
		cmd.ConversationKind = discussionsession.ConversationKindPersonalAssistant
		cmd.ForceNewConversation = strings.EqualFold(strings.TrimSpace(input.Telemetry["weixin_new_session"]), "true")
	}
	if !useNormalDirectChat && !usePersonalAssistant {
		cmd.ConversationID = r.conversationID(sessionKey)
	}
	if route.AgentID != "" && !usePersonalAssistant {
		cmd.DirectTargetKind = "agent"
		cmd.DirectTargetID = route.AgentID
	}
	viewer, err := r.authorizedGatewayViewer(ctx, input, route)
	if err != nil {
		emitGatewayRouteUnavailable(out)
		return
	}
	ctx, input = r.prepareGatewayVoice(ctx, viewer, input, usePersonalAssistant)
	cmd.Content = strings.TrimSpace(input.Message)
	cmd, err = r.prepareGatewayAttachments(ctx, viewer, cmd, input.Attachments)
	if err != nil {
		emitGatewayRuntimeUnavailable(out, "")
		return
	}
	if usePersonalAssistant {
		ctx = r.withPersonalAssistantApprovalDialogue(ctx, productionPersonalAssistantApprovalContextInput{
			TenantID: route.TenantID, HumanProfileID: route.ViewerID,
			AccountID: strings.TrimSpace(input.Telemetry["weixin_account_id"]),
		})
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := r.discussions.SendMessageStream(runCtx, viewer, cmd)
	if err != nil {
		emitGatewayRuntimeUnavailable(out, "")
		return
	}
	if events == nil {
		emitGatewayRuntimeUnavailable(out, "")
		return
	}
	var sawTokenDelta bool
	imageReplies := map[string]bool{}
	var imageFailure bool
	var agentReplies []gwprotocol.Event
	var completionEvents []gwprotocol.Event
	var sourceConversationID, sourceMessageID string
	var terminal bool
	var websiteOutputRejected bool
	// External channels receive one readable final answer. The website keeps
	// streaming because its UI can render progress and tool state explicitly.
	progressIsInternal := input.ChannelKey != gwprotocol.ChannelWebsite
	var websiteRejectedRunID string
	var websiteApprovedSources []string
	var websiteTextFilter *websiteAdvisorTextFilter
	var websiteText strings.Builder
	if input.ChannelKey == gwprotocol.ChannelWebsite {
		websiteTextFilter = &websiteAdvisorTextFilter{}
	}
	for event := range events {
		switch event.Kind {
		case discussionsession.StreamEventSnapshot:
			if event.Conversation != nil && sourceMessageID == "" {
				sourceConversationID = event.Conversation.ConversationID
				for _, message := range event.Conversation.Messages {
					if message.Role == "user" && message.Content == cmd.Content {
						sourceMessageID = message.MessageID
					}
				}
			}
			if !useNormalDirectChat && !usePersonalAssistant && event.Conversation != nil && event.Conversation.ConversationID != "" {
				r.setConversationID(sessionKey, event.Conversation.ConversationID)
			}
		case discussionsession.StreamEventRunStarted:
			out <- gwprotocol.Event{Type: gwprotocol.EventTurnStarted, RunID: gatewayRunID(event)}
		case discussionsession.StreamEventTokenDelta:
			if input.ChannelKey != gwprotocol.ChannelWebsite {
				continue
			}
			if event.Delta != "" {
				sawTokenDelta = true
				if websiteTextFilter != nil {
					event.Delta = websiteTextFilter.Write(event.Delta)
					if !appendWebsiteAdvisorText(&websiteText, event.Delta) {
						emitGatewayRuntimeUnavailable(out, event.RunID)
						return
					}
					continue
				}
				if !progressIsInternal {
					out <- gwprotocol.Event{Type: gwprotocol.EventAssistantTextDelta, Text: event.Delta, RunID: event.RunID}
				}
			}
		case discussionsession.StreamEventToolCall:
			if event.ToolCall != nil {
				if input.ChannelKey == gwprotocol.ChannelWebsite && !websiteAdvisorToolAllowed(event.ToolCall.Name) {
					emitGatewayRuntimeUnavailable(out, event.RunID)
					return
				}
				out <- gwprotocol.Event{Type: gwprotocol.EventToolCallStarted, ToolCallID: event.ToolCall.ToolCallID, Name: event.ToolCall.Name, ArgsPreview: gatewayStringMapPreview(event.ToolCall.Arguments), RunID: event.RunID}
			}
		case discussionsession.StreamEventToolResult:
			if event.ToolCall != nil {
				if usePersonalAssistant && event.ToolCall.Name == "image_generate" && event.ToolCall.Result["status"] == "completed" {
					id := event.ToolCall.Result["attachment_id"]
					if id != "" && !imageReplies[id] {
						imageReplies[id] = true
						imageFailure = !r.emitGeneratedImage(runCtx, viewer, event.ToolCall.Result, out) || imageFailure
					}
				}
				if input.ChannelKey == gwprotocol.ChannelWebsite {
					if !websiteAdvisorToolAllowed(event.ToolCall.Name) {
						emitGatewayRuntimeUnavailable(out, event.RunID)
						return
					}
					if strings.EqualFold(event.ToolCall.Status, "failed") {
						continue
					}
					websiteApprovedSources = append(websiteApprovedSources, websiteAdvisorApprovedSources(event.ToolCall.Result)...)
				}
				out <- gwprotocol.Event{Type: gwprotocol.EventToolCallFinished, ToolCallID: event.ToolCall.ToolCallID, Name: event.ToolCall.Name, OK: !strings.EqualFold(event.ToolCall.Status, "failed"), ResultPreview: gatewayStringMapPreview(event.ToolCall.Result), RunID: event.RunID}
			}
		case discussionsession.StreamEventApprovalRequested:
			if event.Approval != nil {
				metadata := event.Approval.Metadata
				payload := map[string]string{
					"approval_id":     event.Approval.EventID,
					"conversation_id": event.Approval.ConversationID,
					"status":          event.Approval.Status,
				}
				if usePersonalAssistant {
					payload["personal_assistant_reply"] = "notification"
				}
				out <- gwprotocol.Event{
					Type:      gwprotocol.EventPermissionRequest,
					RunID:     gatewayRunID(event),
					RequestID: firstNonBlank(metadata["approval_request_id"], event.Approval.EventID),
					ToolName:  firstNonBlank(metadata["approval_tool_name"], metadata["approval_native_tool_name"]),
					Payload:   payload,
				}
			}
		case discussionsession.StreamEventMessageCompleted:
			if event.Message != nil && strings.EqualFold(event.Message.Role, "assistant") && (!sawTokenDelta || progressIsInternal) && strings.TrimSpace(event.Message.Content) != "" {
				text := event.Message.Content
				if websiteTextFilter != nil {
					text = websiteTextFilter.Write(text) + websiteTextFilter.Flush()
					if !appendWebsiteAdvisorText(&websiteText, text) {
						emitGatewayRuntimeUnavailable(out, event.RunID)
						return
					}
					continue
				}
				if text != "" {
					text = outputpolicy.ChatText(text)
					agentReplies = append(agentReplies, gwprotocol.Event{Type: gwprotocol.EventAssistantTextDelta, Text: text})
				}
			}
		case discussionsession.StreamEventRunCompleted:
			if usePersonalAssistant && event.Run != nil {
				for _, toolCall := range event.Run.ToolCalls {
					id := toolCall.Result["attachment_id"]
					if toolCall.Name == "image_generate" && toolCall.Result["status"] == "completed" && id != "" && !imageReplies[id] {
						imageReplies[id] = true
						imageFailure = !r.emitGeneratedImage(runCtx, viewer, toolCall.Result, out) || imageFailure
					}
				}
			}
			if websiteTextFilter != nil {
				if !appendWebsiteAdvisorText(&websiteText, websiteTextFilter.Flush()) {
					terminal = true
					websiteOutputRejected = true
					websiteRejectedRunID = gatewayRunID(event)
					continue
				}
				answer := websiteAdvisorAnswerWithApprovedSources(websiteText.String(), websiteApprovedSources)
				if !websiteAdvisorOutputAllowed(answer) {
					terminal = true
					websiteOutputRejected = true
					websiteRejectedRunID = gatewayRunID(event)
					continue
				}
				answer = websiteAdvisorVisibleAnswer(answer)
				if answer == "" {
					terminal = true
					websiteOutputRejected = true
					websiteRejectedRunID = gatewayRunID(event)
					continue
				}
				out <- gwprotocol.Event{
					Type: gwprotocol.EventAssistantTextDelta, Text: answer, RunID: gatewayRunID(event),
					Sources: append([]string(nil), websiteApprovedSources...),
				}
			}
			terminal = true
			finish := "completed"
			if event.Run != nil && event.Run.Status != "" {
				finish = event.Run.Status
			}
			completionEvents = append(completionEvents, gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: finish, RunID: gatewayRunID(event)})
		case discussionsession.StreamEventTurnCompleted:
			if !terminal {
				terminal = true
				completionEvents = append(completionEvents, gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "completed", RunID: gatewayRunID(event)})
			}
		case discussionsession.StreamEventError:
			terminal = true
			emitGatewayRuntimeError(out, event)
		}
	}
	if imageFailure {
		r.explainGatewayImageFailure(runCtx, viewer, sourceConversationID, sourceMessageID, cmd.IdempotencyKey, out)
		for _, completion := range completionEvents {
			out <- completion
		}
		return
	}
	for _, reply := range agentReplies {
		out <- reply
	}
	if websiteOutputRejected {
		emitGatewayRuntimeUnavailable(out, websiteRejectedRunID)
		return
	}
	if !terminal {
		emitGatewayRuntimeUnavailable(out, "")
	}
	for _, completion := range completionEvents {
		out <- completion
	}
}

func websiteAdvisorToolAllowed(name string) bool {
	name = strings.TrimSpace(name)
	if name == "list_skills" {
		return true
	}
	for _, downstream := range []string{"search_documents", "get_document", "get_document_structure", "get_page_content"} {
		if name == downstream {
			return true
		}
		for _, tool := range []struct {
			id     agentcore.ToolID
			prefix string
		}{
			{id: defaultOrreryPublicMCPServerToolID, prefix: "mcp_orrery_public_readonly_"},
			{id: defaultOrreryCustomerMCPServerToolID, prefix: "mcp_orrery_customer_readonly_"},
		} {
			if name == string(tool.id)+"."+downstream || name == tool.prefix+downstream {
				return true
			}
		}
	}
	return false
}

func gatewayGroupMessage(input gwprotocol.SubmitTurnInput) bool {
	if input.Identity != nil && strings.EqualFold(strings.TrimSpace(input.Identity.ChatKind), "group") {
		return true
	}
	return input.ChannelKey == gwprotocol.ChannelWeixin && strings.EqualFold(strings.TrimSpace(input.Telemetry["weixin_group"]), "true")
}

func (r *productionMessageGatewayRunner) validateGatewayRouteTenant(ctx context.Context, route productionGatewayRoute) error {
	tenantID := strings.TrimSpace(route.TenantID)
	if tenantID == "" {
		return fmt.Errorf("%w: gateway route tenant is required", errGatewayRouteInvalid)
	}
	r.mu.Lock()
	agents := r.agents
	r.mu.Unlock()
	if agents == nil {
		return fmt.Errorf("agent profile directory is not configured")
	}
	expectedProfiles := []struct {
		role string
		id   string
		kind agentcore.AgentKind
	}{
		{role: "viewer", id: route.ViewerID, kind: agentcore.AgentKindHuman},
	}
	if !route.PersonalAssistant {
		expectedProfiles = append(expectedProfiles, struct {
			role string
			id   string
			kind agentcore.AgentKind
		}{role: "agent", id: route.AgentID, kind: agentcore.AgentKindAI})
	}
	for _, expected := range expectedProfiles {
		profileID := strings.TrimSpace(expected.id)
		if profileID == "" {
			return fmt.Errorf("%w: gateway %s profile is required", errGatewayRouteInvalid, expected.role)
		}
		profile, err := agents.GetProfile(ctx, agentcore.AgentProfileID(profileID))
		if err != nil {
			if errors.Is(err, agentcore.ErrNotFound) {
				return fmt.Errorf("%w: %s profile was not found", errGatewayRouteInvalid, expected.role)
			}
			return fmt.Errorf("load %s profile: %w", expected.role, err)
		}
		if !profile.Enabled || profile.AgentKind != expected.kind {
			return fmt.Errorf("%w: %s profile is disabled or has the wrong kind", errGatewayRouteInvalid, expected.role)
		}
		if strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID)) != tenantID {
			return fmt.Errorf("%w: %s profile is outside tenant scope", errGatewayRouteInvalid, expected.role)
		}
	}
	return nil
}

func emitGatewayRouteUnavailable(out chan<- gwprotocol.Event) {
	out <- gwprotocol.Event{
		Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayRouteUnavailable,
		Message: "Gateway identity resolution is temporarily unavailable.", Recoverable: true,
	}
}

func (r *productionMessageGatewayRunner) conversationID(sessionKey string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conversations[sessionKey]
}

func (r *productionMessageGatewayRunner) setConversationID(sessionKey string, conversationID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conversations == nil {
		r.conversations = map[string]string{}
	}
	r.conversations[sessionKey] = conversationID
}

func startProductionMessageGatewayChannels(ctx context.Context, rt *platform.Runtime, discussions *discussionsession.Service, profiles productionGatewayAgentDirectory, stores ...*gwconfig.Store) func() {
	manager := startProductionMessageGatewayManager(ctx, rt, discussions, profiles, stores...)
	return func() {
		manager.Stop(context.Background())
	}
}

func startProductionMessageGatewayManager(ctx context.Context, rt *platform.Runtime, discussions *discussionsession.Service, profiles productionGatewayAgentDirectory, stores ...*gwconfig.Store) *productionMessageGatewayManager {
	manager := newProductionMessageGatewayManager(rt, discussions, profiles, stores...)
	manager.Start(ctx)
	return manager
}

func cleanupProductionMessageGateway(cancel context.CancelFunc, manager *productionMessageGatewayManager, cleanup func()) {
	cancel()
	manager.Stop(context.Background())
	cleanup()
}

type productionMessageGatewayManager struct {
	rt           *platform.Runtime
	discussions  *discussionsession.Service
	profiles     productionGatewayAgentDirectory
	registry     *channel.Registry
	runner       *productionMessageGatewayRunner
	gateway      *gwcore.Gateway
	configStore  *gwconfig.Store
	bindingStore *gwbindings.Store
	inboundStore *gwinbound.Store
	logger       productionGatewayChannelLogger

	reloadMu   sync.Mutex
	mu         sync.Mutex
	serviceCtx context.Context
	cancel     context.CancelFunc
	config     gwconfig.Config
	started    []channel.StartedAdapter
	lastErrors map[string]string
	stopped    bool
}

func newProductionMessageGatewayManager(rt *platform.Runtime, discussions *discussionsession.Service, profiles productionGatewayAgentDirectory, stores ...*gwconfig.Store) *productionMessageGatewayManager {
	if rt == nil || discussions == nil {
		return nil
	}
	store := productionGatewayConfigStore(rt)
	if len(stores) > 0 && stores[0] != nil {
		store = stores[0]
	}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.audio = newProductionGatewayAudioService(productionSettingsStore{router: rt.DBRouter, config: rt.Config}, platformSecretResolver{provider: rt.Secrets})
	runner.SetAgentDirectory(profiles)
	runner.SetConfigStore(store)
	var bindingStore *gwbindings.Store
	var inboundStore *gwinbound.Store
	if rt.Databases != nil {
		if core, err := rt.Databases.Store("core"); err == nil && core != nil {
			bindingStore = gwbindings.NewStore(core)
			inboundStore = gwinbound.NewStore(core)
			runner.SetBindingStore(bindingStore)
		}
	}
	return &productionMessageGatewayManager{
		rt:           rt,
		discussions:  discussions,
		profiles:     profiles,
		registry:     gwdefaults.NewRegistry(),
		runner:       runner,
		gateway:      gwcore.NewGateway(runner),
		configStore:  store,
		bindingStore: bindingStore,
		inboundStore: inboundStore,
		logger:       productionGatewayChannelLogger{logger: rt.Logger},
		lastErrors:   map[string]string{},
	}
}

func (m *productionMessageGatewayManager) BindingStore() *gwbindings.Store {
	if m == nil {
		return nil
	}
	return m.bindingStore
}

func (m *productionMessageGatewayManager) Gateway() *gwcore.Gateway {
	if m == nil {
		return nil
	}
	return m.gateway
}

func (m *productionMessageGatewayManager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	if m.cancel == nil {
		m.serviceCtx, m.cancel = context.WithCancel(ctx)
	}
	m.mu.Unlock()
	record, err := m.configStore.LoadOrDefault()
	if err != nil {
		if m.rt.Logger != nil {
			m.rt.Logger.Warn(ctx, "gateway channel config unavailable", logging.Any("error", err))
		}
		return
	}
	if err := m.ReloadGatewaySettings(ctx, record.Config); err != nil {
		m.mu.Lock()
		if len(m.started) == 0 {
			m.config = record.Config
		}
		if len(m.lastErrors) == 0 {
			m.lastErrors = enabledGatewayChannelErrors(record.Config, "connection_failed")
		}
		m.mu.Unlock()
		if m.rt.Logger != nil {
			m.rt.Logger.Warn(ctx, "gateway channels failed to start", logging.Any("error", err))
		}
	}
}

func (m *productionMessageGatewayManager) ReloadGatewaySettings(ctx context.Context, cfg gwconfig.Config) error {
	if m == nil {
		return fmt.Errorf("gateway manager is not configured")
	}
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("gateway manager is stopped")
	}
	m.mu.Unlock()
	if err := m.registry.ValidateEnabled(cfg); err != nil {
		return err
	}

	m.mu.Lock()
	oldConfig := m.config
	oldStarted := append([]channel.StartedAdapter(nil), m.started...)
	runtimeCtx := m.runtimeContextLocked()
	m.mu.Unlock()

	changed := changedGatewayRuntimeChannels(oldConfig, cfg)
	if err := markMissingGatewayRuntimeChannels(cfg, oldStarted, changed); err != nil {
		return err
	}
	// Weixin begins polling inside Start, so queued messages must see the new routes first.
	routesStaged := changed["weixin"] && cfg.Adapters.Weixin.Enabled
	routesCommitted := false
	if routesStaged {
		m.runner.UpdateConfig(cfg)
		defer func() {
			if !routesCommitted {
				m.runner.UpdateConfig(oldConfig)
			}
		}()
	}
	deps := channel.StartDeps{
		Gateway:       m.gateway,
		Config:        cfg,
		Logger:        m.logger,
		StateDir:      productionGatewayStateDir(m.rt),
		InboundEvents: m.inboundStore,
	}
	replacements, err := m.startGatewayReplacements(runtimeCtx, cfg, deps, changed)
	if err != nil {
		cleanupErr := stopStartedGatewayAdapters(context.Background(), replacements, "gateway settings reload failed")
		m.mu.Lock()
		m.lastErrors = enabledGatewayChannelErrors(cfg, "connection_failed")
		m.mu.Unlock()
		return errors.Join(err, cleanupErr)
	}
	if err := m.syncWeixinCommonBindings(ctx, cfg); err != nil {
		cleanupErr := stopStartedGatewayAdapters(context.Background(), replacements, "gateway binding sync failed")
		return errors.Join(err, cleanupErr)
	}
	nextStarted, retired := replaceStartedGatewayAdapters(oldStarted, replacements, changed)
	if err := stopStartedGatewayAdapters(context.Background(), retired, "gateway settings reload"); err != nil {
		cleanupErr := stopStartedGatewayAdapters(context.Background(), replacements, "gateway settings reload retirement failed")
		rollbackErr := m.rollbackWeixinCommonBindings(oldConfig)
		m.mu.Lock()
		if m.lastErrors == nil {
			m.lastErrors = map[string]string{}
		}
		for _, adapter := range retired {
			m.lastErrors[adapter.Key] = "retirement_failed"
		}
		m.mu.Unlock()
		return errors.Join(fmt.Errorf("retire replaced gateway channels: %w", err), cleanupErr, rollbackErr)
	}
	m.runner.UpdateConfig(cfg)
	routesCommitted = true
	m.mu.Lock()
	m.config = cfg
	m.started = nextStarted
	m.lastErrors = map[string]string{}
	m.mu.Unlock()
	if m.rt != nil && m.rt.Logger != nil {
		m.rt.Logger.Info(ctx, "gateway channels reloaded", logging.Any("channels", startedGatewayKeys(nextStarted)))
	}
	return nil
}

func (m *productionMessageGatewayManager) Stop(ctx context.Context) {
	if m == nil {
		return
	}
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	m.mu.Lock()
	m.stopped = true
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
		m.serviceCtx = nil
	}
	started := append([]channel.StartedAdapter(nil), m.started...)
	m.started = nil
	m.mu.Unlock()
	if err := stopStartedGatewayAdapters(ctx, started, "server shutdown"); err != nil && m.rt != nil && m.rt.Logger != nil {
		m.rt.Logger.Warn(ctx, "gateway channels did not stop cleanly", logging.Any("error", err))
	}
}

func (m *productionMessageGatewayManager) Send(ctx context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	if m == nil {
		return channel.DeliveryResult{}, fmt.Errorf("gateway manager is not configured")
	}
	m.mu.Lock()
	var sender channel.OutboundSender
	for _, adapter := range m.started {
		if adapter.Key != strings.TrimSpace(delivery.ChannelKey) {
			continue
		}
		if accountID, adapterAccountID := strings.TrimSpace(delivery.AccountID), strings.TrimSpace(adapter.AccountID); accountID != "" && adapterAccountID != "" && adapterAccountID != accountID {
			continue
		}
		if provider, ok := adapter.Handle.(channel.AccountConnectionHealthProvider); ok && strings.TrimSpace(delivery.AccountID) != "" {
			if !provider.GatewayAccountConnectionHealthy(delivery.AccountID) {
				break
			}
		} else if provider, ok := adapter.Handle.(channel.ConnectionHealthProvider); ok && !provider.GatewayConnectionHealthy() {
			break
		}
		if candidate, ok := adapter.Handle.(channel.OutboundSender); ok {
			sender = candidate
		}
		break
	}
	m.mu.Unlock()
	if sender == nil {
		return channel.DeliveryResult{}, fmt.Errorf("gateway channel %s is not connected for outbound delivery", delivery.ChannelKey)
	}
	return sender.Send(ctx, delivery)
}
