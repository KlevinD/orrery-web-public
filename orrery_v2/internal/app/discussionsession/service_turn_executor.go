package discussionsession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type DiscussionTurnOrigin string

const (
	DiscussionTurnOriginMessage  DiscussionTurnOrigin = "message"
	DiscussionTurnOriginRetry    DiscussionTurnOrigin = "retry"
	DiscussionTurnOriginApproval DiscussionTurnOrigin = "approval"
)

// DiscussionTurnCommand is the single normalized input accepted by the turn
// executor. Delivery style changes event emission only; it does not select a
// different execution path.
type DiscussionTurnCommand struct {
	TurnID                 string
	ConversationID         string
	IdempotencyKey         string
	Origin                 DiscussionTurnOrigin
	Viewer                 ViewerRef
	SourceMessage          MessageRecord
	Current                ConversationRecordView
	Targets                discussionTurnTargetPlan
	NotificationBestEffort bool
	History                []MessageDTO
	OnEvent                func(StreamEventDTO) bool
	Approval               *discussionApprovalTurnContext
}

type discussionApprovalTurnContext struct {
	Event        EventRecord
	BlockedRun   RunRecord
	Metadata     map[string]string
	Continuation runtimecore.ApprovalContinuation
}

type AgentTurnExecutor struct {
	service *Service
}

func (s *Service) newDiscussionTurnCommand(current ConversationRecordView, source MessageRecord, viewer ViewerRef, origin DiscussionTurnOrigin, idempotencyKey string, onEvent func(StreamEventDTO) bool) DiscussionTurnCommand {
	mode := resolveDiscussionTurnTargets(current, source, s.discussionAgentID())
	history := promptHistoryWithoutMessage(current.Messages, source.MessageID)
	if origin == DiscussionTurnOriginRetry {
		history = retryPromptHistoryBeforeMessage(current.Messages, source.MessageID)
	}
	turnID := strings.TrimSpace(source.TurnID)
	if origin != DiscussionTurnOriginMessage || turnID == "" {
		turnID = stableDiscussionTurnID(origin, current.ConversationID, source.MessageID, idempotencyKey)
	}
	return DiscussionTurnCommand{
		TurnID:         turnID,
		ConversationID: current.ConversationID,
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		Origin:         origin,
		Viewer:         viewer,
		SourceMessage:  source,
		Current:        current,
		Targets:        mode,
		History:        history,
		OnEvent:        onEvent,
	}
}

func stableDiscussionTurnID(origin DiscussionTurnOrigin, conversationID string, messageID string, idempotencyKey string) string {
	identity := strings.Join([]string{
		strings.TrimSpace(string(origin)),
		strings.TrimSpace(conversationID),
		strings.TrimSpace(messageID),
		strings.TrimSpace(idempotencyKey),
	}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	return "turn-" + hex.EncodeToString(digest[:16])
}

func (s *Service) newApprovalDiscussionTurnCommand(view ConversationRecordView, source MessageRecord, viewer ViewerRef, event EventRecord, blockedRun RunRecord, metadata map[string]string, continuation runtimecore.ApprovalContinuation, idempotencyKey string) DiscussionTurnCommand {
	targets := resolveRuntimeApprovalTurnTargets(view, blockedRun)
	return DiscussionTurnCommand{
		TurnID:         stableDiscussionTurnID(DiscussionTurnOriginApproval, view.ConversationID, event.EventID, idempotencyKey),
		ConversationID: view.ConversationID,
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
		Origin:         DiscussionTurnOriginApproval,
		Viewer:         viewer,
		SourceMessage:  source,
		Current:        view,
		Targets:        targets,
		History:        promptHistoryWithoutMessage(view.Messages, ""),
		Approval: &discussionApprovalTurnContext{
			Event:        event,
			BlockedRun:   blockedRun,
			Metadata:     cloneStringMap(metadata),
			Continuation: continuation,
		},
	}
}

func (s *Service) executeDiscussionTurn(ctx context.Context, command DiscussionTurnCommand) (ConversationDTO, error) {
	ctx = s.withRunLifecycleOwner(ctx, command.ConversationID)
	executor := AgentTurnExecutor{service: s}
	return executor.Execute(context.WithoutCancel(ctx), command)
}

func (s *Service) executeDiscussionTurnStream(ctx context.Context, commandRecord CommandRecord, source MessageRecord, turn DiscussionTurnCommand, initial ConversationRecordView, release func()) <-chan StreamEventDTO {
	out := make(chan StreamEventDTO, 64)
	executionEvents := make(chan StreamEventDTO, 64)
	ready := make(chan struct{})
	var readyOnce sync.Once
	markReady := func() { readyOnce.Do(func() { close(ready) }) }
	send := func(event StreamEventDTO) bool {
		if s.streams != nil {
			s.streams.publish(turn.ConversationID, event)
		}
		if event.Kind == StreamEventRunStarted || event.Kind == StreamEventTurnCompleted || event.Kind == StreamEventTurnFailed || event.Kind == StreamEventError {
			markReady()
		}
		executionEvents <- event
		return true
	}
	turn.OnEvent = send
	go func() {
		defer close(executionEvents)
		defer markReady()
		if release != nil {
			defer release()
		}
		snapshot := mapConversationDTO(initial)
		_ = send(StreamEventDTO{
			Kind:           StreamEventSnapshot,
			EventID:        "discussion-turn-initial-snapshot-" + turn.TurnID,
			TurnID:         turn.TurnID,
			ConversationID: turn.ConversationID,
			Revision:       snapshot.Revision,
			Conversation:   &snapshot,
		})
		dto, executionErr := s.executeDiscussionTurn(context.WithoutCancel(ctx), turn)
		if executionErr != nil {
			_ = s.failDiscussionCommand(context.WithoutCancel(ctx), commandRecord, executionErr)
			failureEvent := streamErrorEvent(executionErr)
			failureEvent.EventID = "discussion-turn-error-" + turn.TurnID
			failureEvent.TurnID = turn.TurnID
			failureEvent.ConversationID = turn.ConversationID
			failureEvent.Revision = dto.Revision
			_ = send(failureEvent)
			return
		}
		if strings.EqualFold(strings.TrimSpace(dto.Status), "failed") {
			failure := "discussion turn failed"
			if persisted, err := s.repo.GetTurn(context.WithoutCancel(ctx), turn.TurnID); err == nil && strings.TrimSpace(persisted.ErrorMessage) != "" {
				failure = persisted.ErrorMessage
			}
			failureErr := errors.New(failure)
			_ = s.failDiscussionCommand(context.WithoutCancel(ctx), commandRecord, failureErr)
			failureEvent := streamErrorEvent(failureErr)
			failureEvent.EventID = "discussion-turn-terminal-error-" + turn.TurnID
			failureEvent.TurnID = turn.TurnID
			failureEvent.ConversationID = turn.ConversationID
			failureEvent.Revision = dto.Revision
			_ = send(failureEvent)
			return
		}
		if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), commandRecord, turn.ConversationID, source.MessageID, turn.TurnID); err != nil {
			_ = send(StreamEventDTO{
				Kind:           StreamEventError,
				EventID:        "discussion-command-completion-error-" + turn.TurnID,
				TurnID:         turn.TurnID,
				ConversationID: turn.ConversationID,
				Revision:       dto.Revision,
				Error:          err.Error(),
			})
		}
	}()
	go func() {
		detached := false
		requestDone := ctx.Done()
		defer func() {
			if !detached {
				close(out)
			}
		}()
		for {
			select {
			case event, ok := <-executionEvents:
				if !ok {
					return
				}
				if detached {
					continue
				}
				select {
				case out <- event:
				case <-requestDone:
					close(out)
					detached = true
					requestDone = nil
				}
			case <-requestDone:
				close(out)
				detached = true
				requestDone = nil
			}
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
	}
	return out
}

func (e AgentTurnExecutor) validate(command DiscussionTurnCommand) error {
	if e.service == nil || e.service.repo == nil {
		return ErrServiceDependency
	}
	if strings.TrimSpace(command.TurnID) == "" || strings.TrimSpace(command.ConversationID) == "" || strings.TrimSpace(command.SourceMessage.MessageID) == "" {
		return ErrInvalidRequest
	}
	if command.Targets.Mode == discussionTurnTargetDirectAgent && len(command.Targets.Members) == 0 {
		return ErrInvalidRequest
	}
	if command.Targets.requiresMentionResponder() && e.service.mentionResponder == nil {
		return ErrRuntimeUnavailable
	}
	if !command.Targets.requiresDiscussionAgent() {
		return nil
	}
	if e.service.discussionAgent == nil {
		return e.service.runtimeUnavailableError()
	}
	if command.OnEvent != nil {
		if _, ok := e.service.discussionAgent.(DiscussionAgentStreamRunner); !ok {
			return e.service.runtimeUnavailableError()
		}
	}
	_, err := e.service.configuredRunAgentID()
	return err
}

func (e AgentTurnExecutor) start(ctx context.Context, command *DiscussionTurnCommand) error {
	startedAt := firstTimeAfter(e.service.now(), command.SourceMessage.CreatedAt)
	turn := TurnRecord{
		TurnID:         command.TurnID,
		ConversationID: command.ConversationID,
		RootMessageID:  command.SourceMessage.MessageID,
		IdempotencyKey: command.IdempotencyKey,
		Mode:           string(command.Targets.Mode),
		Status:         "running",
		StartedAt:      startedAt,
		CreatedAt:      startedAt,
		UpdatedAt:      startedAt,
	}
	messages := []MessageRecord(nil)
	if command.Origin == DiscussionTurnOriginMessage && strings.TrimSpace(command.SourceMessage.TurnID) == "" {
		command.SourceMessage.TurnID = command.TurnID
		messages = append(messages, command.SourceMessage)
	}
	var committed ConversationRecord
	for attempt := 0; attempt < 3; attempt++ {
		current, err := e.service.repo.GetConversationRecord(ctx, command.ConversationID)
		if err != nil {
			return err
		}
		current.Status = "running"
		current.UpdatedAt = firstTimeAfter(startedAt, current.CreatedAt)
		committed, err = e.service.repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
			Conversation:        current,
			ExpectedRevision:    current.Revision,
			Turn:                &turn,
			Messages:            messages,
			SupersedeActiveTurn: command.Origin == DiscussionTurnOriginRetry,
		})
		if err == nil {
			break
		}
		if !errors.Is(err, ErrRevisionConflict) || attempt == 2 {
			return err
		}
	}
	command.Current.ConversationRecord = committed
	if err := e.service.appendRealtimeTurnStarted(ctx, turn); err != nil {
		if !command.NotificationBestEffort {
			return err
		}
		if e.service.logger != nil {
			e.service.logger.Warn(ctx, "durable discussion turn realtime notification failed", "conversation_id", command.ConversationID, "turn_id", command.TurnID, "error", err)
		}
	}
	e.emit(*command, StreamEventDTO{
		Kind:           StreamEventTurnStarted,
		EventID:        "discussion-turn-started-" + command.TurnID,
		TurnID:         command.TurnID,
		ConversationID: command.ConversationID,
	})
	return nil
}

func (e AgentTurnExecutor) executeResolvedTargets(ctx context.Context, command DiscussionTurnCommand) error {
	switch command.Targets.Mode {
	case discussionTurnTargetGroup:
		return e.executeGroupTarget(ctx, command)
	case discussionTurnTargetDirectAgent:
		return e.executeDirectTarget(ctx, command)
	case discussionTurnTargetDirectPassive:
		return nil
	case discussionTurnTargetMention:
		return e.executeMentionTargets(ctx, command)
	case discussionTurnTargetApprovalContinuation:
		return e.executeApprovalContinuation(ctx, command)
	default:
		return ErrInvalidRequest
	}
}

func (e AgentTurnExecutor) executeGroupTarget(ctx context.Context, command DiscussionTurnCommand) error {
	finishLoad := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, "", "run_context_load")
	current, err := e.service.repo.GetConversation(ctx, command.ConversationID)
	finishLoad(err)
	if err != nil {
		return err
	}
	agentID, err := e.service.configuredRunAgentIDForConversation(ctx, current)
	if err != nil {
		return err
	}
	finishCreate := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, "", "run_create")
	run, err := e.service.createDiscussionTurnRun(ctx, command.TurnID, string(command.Targets.Mode), command.ConversationID, agentID, command.SourceMessage, command.SourceMessage.CreatedAt)
	finishCreate(err)
	if err != nil {
		return err
	}
	e.emitRunStarted(command, run)
	request := discussionAgentRequestFromConversation(current, run, command.Viewer, command.SourceMessage, command.History)
	request = applyOperationalFailure(ctx, request)
	request.ForceNewSession = command.Origin == DiscussionTurnOriginRetry
	if err := e.service.prepareDiscussionAgentRuntimeRequestOrFailRun(ctx, command.ConversationID, run, &request); err != nil {
		return err
	}
	if err := e.service.capturePromptDebug(ctx, command.ConversationID, &run, request); err != nil {
		return err
	}
	e.service.logDiscussionRunStart(ctx, run, request)
	runCtx, cancel := context.WithCancel(ctx)
	e.service.registerActiveRun(run.RunID, cancel)
	finishRuntime := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, run.RunID, "runtime_execute")
	response, runErr := e.runGroupRuntime(runCtx, command, &run, request)
	finishRuntime(runErr)
	e.service.unregisterActiveRun(run.RunID)
	cancel()
	if e.service.isRunInvalidated(run.RunID) {
		return newSupersededDiscussionRunError(runErr)
	}
	if runErr != nil {
		e.service.logDiscussionRunFailure(ctx, run, runErr)
		e.service.applyRuntimeFailureResponse(&run, response)
		finalizeCtx := context.WithoutCancel(ctx)
		if e.service.isRunCurrent(finalizeCtx, run) {
			partialMessage, saveErr := e.service.persistFailedRunWithResponse(finalizeCtx, command.ConversationID, run, response, runErr)
			if saveErr != nil {
				return saveErr
			}
			if partialMessage != nil {
				e.emit(command, StreamEventDTO{Kind: StreamEventMessageCompleted, Message: partialMessage, RunID: run.RunID})
			}
			if failed, getErr := e.service.repo.GetRun(finalizeCtx, run.RunID); getErr == nil {
				e.emitRunCompleted(command, failed)
			}
		}
		return runErr
	}
	if !e.service.isRunCurrent(ctx, run) {
		return ErrNotFound
	}
	e.service.logDiscussionRunResponse(ctx, run, response)
	message, completedRun, err := e.service.completeRunLocked(ctx, command.ConversationID, run, response, request)
	if err != nil {
		return err
	}
	if message != nil {
		e.emit(command, StreamEventDTO{Kind: StreamEventMessageCompleted, Message: message, RunID: run.RunID})
	}
	if completedRun != nil {
		e.emit(command, StreamEventDTO{Kind: StreamEventRunCompleted, Run: completedRun, RunID: run.RunID})
	}
	if completedRun != nil && strings.EqualFold(strings.TrimSpace(completedRun.Status), "blocked") {
		return nil
	}
	return e.executeGroupPostPolicy(ctx, command, run, response)
}

func (e AgentTurnExecutor) runGroupRuntime(ctx context.Context, command DiscussionTurnCommand, run *RunRecord, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	if command.OnEvent == nil {
		return e.service.discussionAgent.RunDiscussion(ctx, request)
	}
	finishFirstText := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, run.RunID, "runtime_first_text")
	firstTextSeen := false
	streamer := e.service.discussionAgent.(DiscussionAgentStreamRunner)
	events, err := streamer.RunDiscussionStream(ctx, request)
	if err != nil {
		return DiscussionAgentResponse{}, err
	}
	return e.service.drainDiscussionMentionStream(ctx, run, events, func(event StreamEventDTO) bool {
		if !firstTextSeen && event.Kind == StreamEventTokenDelta && event.Delta != "" {
			firstTextSeen = true
			finishFirstText(nil)
		}
		e.decorateEvent(command, &event)
		return command.OnEvent(event)
	})
}

func (e AgentTurnExecutor) executeDirectTarget(ctx context.Context, command DiscussionTurnCommand) error {
	current, err := e.service.repo.GetConversation(ctx, command.ConversationID)
	if err != nil {
		return err
	}
	if len(command.Targets.Members) == 0 {
		return ErrInvalidRequest
	}
	target := command.Targets.Members[0]
	opts, runCtx, cancel := e.targetDispatchOptions(ctx, command)
	defer cancel()
	_, err = e.service.respondToDirectConversationTarget(runCtx, current, command.SourceMessage, target, command.SourceMessage.CreatedAt, opts, func(message MessageDTO) bool {
		return e.emit(command, StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message, RunID: message.RunID})
	})
	return err
}

func (e AgentTurnExecutor) executeMentionTargets(ctx context.Context, command DiscussionTurnCommand) error {
	current, err := e.service.repo.GetConversation(ctx, command.ConversationID)
	if err != nil {
		return err
	}
	opts, runCtx, cancel := e.targetDispatchOptions(ctx, command)
	defer cancel()
	_, err = e.service.respondToDiscussionMentionsFromMessage(runCtx, command.ConversationID, command.SourceMessage, mapMissionLinks(current.LinkedMissions), command.SourceMessage.CreatedAt, opts, func(message MessageDTO) bool {
		return e.emit(command, StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message, RunID: message.RunID})
	})
	return err
}

func (e AgentTurnExecutor) executeApprovalContinuation(ctx context.Context, command DiscussionTurnCommand) error {
	if command.Approval == nil || len(command.Targets.Members) == 0 {
		return ErrInvalidRequest
	}
	approval := command.Approval
	target := command.Targets.Members[0]
	now := firstTimeAfter(e.service.now(), approval.BlockedRun.CompletedAt)
	runID, err := e.service.newID()
	if err != nil {
		return err
	}
	run := RunRecord{
		RunID:            runID,
		ConversationID:   command.ConversationID,
		TurnID:           command.TurnID,
		MissionID:        approval.BlockedRun.MissionID,
		MissionTaskID:    approval.BlockedRun.MissionTaskID,
		Mode:             string(discussionTurnTargetApprovalContinuation),
		AgentID:          approval.BlockedRun.AgentID,
		LogicalAgentID:   "",
		AgentName:        target.DisplayName,
		Status:           "running",
		RuntimeKind:      approval.BlockedRun.RuntimeKind,
		SessionID:        approval.BlockedRun.SessionID,
		ProviderMetadata: cloneStringMap(approval.BlockedRun.ProviderMetadata),
		CreatedAt:        now,
		StartedAt:        now,
	}
	run, err = e.service.saveDiscussionTargetRun(ctx, run)
	if err != nil {
		return err
	}
	e.emitRunStarted(command, run)

	decisionEvent := decidedRuntimeApprovalEvent(approval.Event, approval.Metadata, approval.Continuation, "approved", command.Viewer, run.RunID, now)
	if err := e.service.repo.SaveEvent(ctx, decisionEvent); err != nil {
		return err
	}
	prompt := runtimecore.ApprovalContinuationPrompt(approval.Metadata, approval.Continuation)
	source := command.SourceMessage
	request := discussionTargetRequestFromRun(discussionApprovalContinuationMode, command.Current, source, target, run)
	request.OutputLanguage = normalizeDiscussionOutputLanguage(command.Viewer.Language)
	request.Content = prompt
	request.SourceMessage = triggerMessageDTOFromRecord(source)
	request.BoundTools = e.service.discussionAgentBoundToolsForConversationAgent(ctx, command.ConversationID, target.MemberID)
	request.BoundToolsPolicy = true
	request.ProviderSpecific = runtimeApprovalContinuationProviderSpecific(approval.Metadata, approval.Continuation)
	request.ResumeSessionID = firstNonBlank(approval.Metadata[runtimecore.ApprovalMetadataSessionID], approval.BlockedRun.SessionID)
	if err := e.service.attachDiscussionMentionWorkspace(ctx, &request); err != nil {
		return err
	}
	response, runErr := e.service.respondToDiscussionMention(ctx, &run, request, command.OnEvent)
	completedAt := firstTimeAfter(e.service.now(), run.StartedAt)
	if runErr != nil {
		_, saveErr := e.service.failDiscussionMentionRun(context.WithoutCancel(ctx), command.ConversationID, run, source, target, response, runErr, completedAt, command.OnEvent)
		return saveErr
	}
	linkedMissions := mergeLinkedMissions(response.LinkedMissions)
	if !response.ToolLoopCompleted {
		linkedMissions, err = e.service.applyBoundDiscussionMentionToolCalls(ctx, command.ConversationID, target.MemberID, source, target, response.ToolCalls, request.BoundTools, completedAt, request)
		if err != nil {
			_, saveErr := e.service.failDiscussionMentionRun(context.WithoutCancel(ctx), command.ConversationID, run, source, target, response, err, completedAt, command.OnEvent)
			return saveErr
		}
	}
	var stop bool
	response, linkedMissions, stop, err = e.service.continueDiscussionMentionTools(ctx, command.ConversationID, &run, request, source, target, response, linkedMissions, completedAt, command.OnEvent, func(RunRecord) {})
	if err != nil {
		return err
	}
	if stop || run.Status == "failed" {
		return nil
	}
	if err := e.service.saveRunMissionLinks(ctx, command.ConversationID, linkedMissions, completedAt); err != nil {
		return err
	}
	run.Status = firstNonBlank(response.Status, "completed")
	applyResponseRunProgress(&run, response)
	run.RuntimeKind = response.RuntimeKind
	run.SessionID = response.SessionID
	run.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
	run.CompletedAt = completedAt
	applyResponseTelemetry(&run, response)
	if err := e.service.saveRunAndRuntimeApprovalEvent(ctx, command.ConversationID, run, response, completedAt); err != nil {
		return err
	}
	if err := e.service.appendRealtimeToolEvents(ctx, command.ConversationID, run.RunID, run.ToolCalls); err != nil {
		return err
	}
	if reply := strings.TrimSpace(response.AssistantMessage); reply != "" {
		messageID, err := e.service.newID()
		if err != nil {
			return err
		}
		replyViewer := ViewerRef{ViewerID: source.AuthorID, ViewerKind: ViewerKindUser, DisplayName: source.AuthorName}
		authorID := strings.TrimSpace(target.MemberID)
		authorName := strings.TrimSpace(target.DisplayName)
		if request.PersonalAssistantHost {
			authorID, authorName, err = requiredConversationRunMessageAuthor(command.Current, run)
			if err != nil {
				return err
			}
			reply = conversationAssistantReplyContent(command.Current, run, reply)
		}
		record := correlateMessageToRun(assistantMessageRecordWithDisplayTimeline(messageID, command.ConversationID, authorID, authorName, reply, response, firstTimeAfter(completedAt, source.CreatedAt), replyViewer), run)
		message, err := e.service.persistAssistantMessageWithWorkspaceFiles(ctx, record, request.Workspace)
		if err != nil {
			return err
		}
		e.emit(command, StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message, RunID: run.RunID})
	}
	if err := e.service.appendRealtimeRunCompleted(ctx, run); err != nil {
		return err
	}
	e.emitRunCompleted(command, run)
	return nil
}

func (e AgentTurnExecutor) targetDispatchOptions(ctx context.Context, command DiscussionTurnCommand) (discussionMentionDispatchOptions, context.Context, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(ctx)
	opts := discussionMentionDispatchOptions{
		TurnID:          command.TurnID,
		Mode:            string(command.Targets.Mode),
		ActiveRunCancel: cancel,
		ForceNewSession: command.Origin == DiscussionTurnOriginRetry,
		OutputLanguage:  normalizeDiscussionOutputLanguage(command.Viewer.Language),
	}
	if command.OnEvent != nil {
		opts.OnEvent = func(event StreamEventDTO) bool {
			e.decorateEvent(command, &event)
			return command.OnEvent(event)
		}
	}
	return opts, runCtx, cancel
}

func (e AgentTurnExecutor) executeGroupPostPolicy(ctx context.Context, command DiscussionTurnCommand, run RunRecord, response DiscussionAgentResponse) (err error) {
	finishStage := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, run.RunID, "post_policy")
	defer func() { finishStage(err) }()
	if facts, _ := ctx.Value(operationalFailureKey{}).(string); facts != "" {
		return nil
	}
	currentLinks, err := e.service.repo.listMissionLinks(ctx, command.ConversationID)
	if err != nil {
		return err
	}
	linkedMissions := mergeLinkedMissions(mapMissionLinks(currentLinks), response.LinkedMissions)
	opts, runCtx, cancel := e.targetDispatchOptions(ctx, command)
	defer cancel()
	opts.SkipInitialTargetIDs = map[string]bool{strings.TrimSpace(run.AgentID): true}
	opts.InitialLatestAt = run.CompletedAt
	userMentionMessages, err := e.service.respondToDiscussionMentionsFromMessage(runCtx, command.ConversationID, command.SourceMessage, linkedMissions, run.CompletedAt, opts, func(message MessageDTO) bool {
		return e.emit(command, StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message, RunID: message.RunID})
	})
	if err != nil {
		return err
	}
	latest := run.CompletedAt
	for _, message := range userMentionMessages {
		if message.CreatedAt.After(latest) {
			latest = message.CreatedAt
		}
	}
	opts.SkipInitialTargetIDs = messageAuthorIDs(userMentionMessages)
	opts.InitialLatestAt = latest
	_, err = e.service.respondToDiscussionAgentMentionsWithMessages(runCtx, command.ConversationID, discussionRunMessageAuthorID(run), linkedMissions, run.CompletedAt, opts, func(message MessageDTO) bool {
		return e.emit(command, StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message, RunID: message.RunID})
	})
	return err
}

// discussionRunMessageAuthorID returns the identity persisted on the host
// message. PA-origin Mission hosts execute as the physical Lead but publish
// their reply under the user's logical personal-assistant identity.
func discussionRunMessageAuthorID(run RunRecord) string {
	if isPersonalAssistantRuntimeRun(run) {
		return strings.TrimSpace(run.LogicalAgentID)
	}
	return strings.TrimSpace(run.AgentID)
}

func (e AgentTurnExecutor) emitRunStarted(command DiscussionTurnCommand, run RunRecord) {
	runDTO := mapRuns([]RunRecord{run})[0]
	e.emit(command, StreamEventDTO{Kind: StreamEventRunStarted, Run: &runDTO, RunID: run.RunID})
}

func (e AgentTurnExecutor) emitRunCompleted(command DiscussionTurnCommand, run RunRecord) {
	runDTO := mapRuns([]RunRecord{run})[0]
	kind := StreamEventRunCompleted
	status := strings.ToLower(strings.TrimSpace(run.Status))
	if status == "failed" || status == "cancelled" || status == "canceled" {
		kind = StreamEventRunFailed
	}
	e.emit(command, StreamEventDTO{Kind: kind, Run: &runDTO, RunID: run.RunID})
}

func (e AgentTurnExecutor) emit(command DiscussionTurnCommand, event StreamEventDTO) bool {
	if command.OnEvent == nil {
		return true
	}
	e.decorateEvent(command, &event)
	return command.OnEvent(event)
}

func (e AgentTurnExecutor) decorateEvent(command DiscussionTurnCommand, event *StreamEventDTO) {
	if event == nil {
		return
	}
	if event.TurnID == "" {
		event.TurnID = command.TurnID
	}
	if event.ConversationID == "" {
		event.ConversationID = command.ConversationID
	}
	if event.Revision <= 0 {
		event.Revision = command.Current.Revision
	}
	if event.RunID == "" && event.Run != nil {
		event.RunID = event.Run.RunID
	}
	if event.EventID == "" {
		event.EventID = discussionStreamEventID(command.TurnID, *event)
	}
}
