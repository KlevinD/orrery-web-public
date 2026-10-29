package discussionsession

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

func (s *Service) postDiscussionMentionMessage(ctx context.Context, agentID string, conversationID string, content string, missions []LinkedMissionDTO, completedAt time.Time) error {
	if s == nil || s.missionMessenger == nil || !hasDiscussionMentionLink(content) {
		return nil
	}
	mission := firstLinkedMission(missions)
	if strings.TrimSpace(mission.MissionID) == "" {
		return nil
	}
	keyParts := []string{"discussion-mention", strings.TrimSpace(conversationID), strings.TrimSpace(agentID), strings.TrimSpace(mission.MissionID)}
	if !completedAt.IsZero() {
		keyParts = append(keyParts, completedAt.UTC().Format(time.RFC3339Nano))
	}
	return s.missionMessenger.PostDiscussionMessage(ctx, discussionAgentViewer(agentID), conversationID, mission, strings.TrimSpace(content), strings.Join(nonEmptyStrings(keyParts), "|"))
}

func (s *Service) respondToDiscussionAgentMentions(ctx context.Context, conversationID string, sourceAgentID string, linkedMissions []LinkedMissionDTO, after time.Time) (time.Time, error) {
	return s.respondToDiscussionAgentMentionsWithOptions(ctx, conversationID, sourceAgentID, linkedMissions, after, discussionMentionDispatchOptions{})
}

func (s *Service) respondToDiscussionAgentMentionsWithOptions(ctx context.Context, conversationID string, sourceAgentID string, linkedMissions []LinkedMissionDTO, after time.Time, opts discussionMentionDispatchOptions) (time.Time, error) {
	messages, err := s.respondToDiscussionAgentMentionsWithMessages(ctx, conversationID, sourceAgentID, linkedMissions, after, opts)
	if err != nil {
		return after, err
	}
	for _, message := range messages {
		if message.CreatedAt.After(after) {
			after = message.CreatedAt
		}
	}
	return after, nil
}

func (s *Service) respondToDiscussionAgentMentionsWithMessages(ctx context.Context, conversationID string, sourceAgentID string, linkedMissions []LinkedMissionDTO, after time.Time, opts discussionMentionDispatchOptions, onMessage ...func(MessageDTO) bool) ([]MessageDTO, error) {
	if s == nil || s.repo == nil || s.mentionResponder == nil {
		return nil, nil
	}
	sourceAgentID = strings.TrimSpace(sourceAgentID)
	if sourceAgentID == "" {
		return nil, nil
	}
	messages, err := s.repo.listMessages(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return nil, err
	}
	source, ok := latestAgentMessageAt(messages, sourceAgentID, after)
	if !ok {
		return nil, nil
	}
	return s.respondToDiscussionMentionsFromSource(ctx, conversationID, source, linkedMissions, after, opts, onMessage...)
}

func (s *Service) respondToDiscussionMentionsFromMessage(ctx context.Context, conversationID string, source MessageRecord, linkedMissions []LinkedMissionDTO, after time.Time, opts discussionMentionDispatchOptions, onMessage ...func(MessageDTO) bool) ([]MessageDTO, error) {
	if s == nil || s.repo == nil || s.mentionResponder == nil {
		return nil, nil
	}
	if strings.TrimSpace(source.MessageID) == "" || strings.TrimSpace(source.ConversationID) == "" {
		return nil, nil
	}
	return s.respondToDiscussionMentionsFromSource(ctx, conversationID, source, linkedMissions, after, opts, onMessage...)
}

func (s *Service) respondToDiscussionMentionsFromSource(ctx context.Context, conversationID string, source MessageRecord, linkedMissions []LinkedMissionDTO, after time.Time, opts discussionMentionDispatchOptions, onMessage ...func(MessageDTO) bool) ([]MessageDTO, error) {
	if s == nil || s.repo == nil || s.mentionResponder == nil {
		return nil, nil
	}
	if len(discussionAgentMentionCandidateIDs(source)) == 0 {
		return nil, nil
	}
	var out []MessageDTO
	queue := []discussionMentionQueueItem{{Message: source, IsRoot: true}}
	processed := map[string]bool{}
	latest := firstTimeAfter(firstTime(opts.InitialLatestAt, after, s.now()), source.CreatedAt)
	var emit func(MessageDTO) bool
	if len(onMessage) > 0 {
		emit = onMessage[0]
	}
	emitEvent := opts.OnEvent
	registerMentionRun := func(run RunRecord) {
		if opts.ActiveRunCancel != nil {
			s.registerActiveRun(run.RunID, opts.ActiveRunCancel)
		}
	}
	unregisterMentionRun := func(run RunRecord) {
		if opts.ActiveRunCancel != nil {
			s.unregisterActiveRun(run.RunID)
		}
	}
	pairLoopLimit, err := s.discussionAgentPairLoopLimit(ctx)
	if err != nil {
		return nil, err
	}
	for len(queue) > 0 {
		if ctx.Err() != nil {
			return out, nil
		}
		item := queue[0]
		queue = queue[1:]
		source = item.Message
		if len(discussionAgentMentionCandidateIDs(source)) == 0 {
			continue
		}
		view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
		if err != nil {
			return nil, err
		}
		targets := discussionAgentMentionTargets(source, view.Members)
		prepared := make([]discussionMentionPreparedRun, 0, len(targets))
		for _, target := range targets {
			if ctx.Err() != nil {
				return out, nil
			}
			if item.IsRoot && opts.SkipInitialTargetIDs[strings.TrimSpace(target.MemberID)] {
				continue
			}
			key := strings.TrimSpace(source.MessageID) + "|" + strings.TrimSpace(target.MemberID)
			if processed[key] {
				continue
			}
			processed[key] = true
			nextPairLoop := nextDiscussionAgentPairLoopState(item.PairLoop, source.AuthorID, target.MemberID)
			if nextPairLoop.Count > pairLoopLimit {
				continue
			}
			mode := firstNonBlank(opts.Mode, "mention")
			run, err := s.createDiscussionTargetRunForTurn(ctx, opts.TurnID, mode, conversationID, target.MemberID, source, latest, time.Time{})
			if err != nil {
				return nil, err
			}
			registerMentionRun(run)
			if emitEvent != nil {
				runDTO := mapRuns([]RunRecord{run})[0]
				if !emitEvent(StreamEventDTO{Kind: StreamEventRunStarted, Run: &runDTO}) {
					unregisterMentionRun(run)
					return out, nil
				}
			}
			request := discussionTargetRequestFromRun("mention", view, source, target, run)
			request.ForceNewSession = opts.ForceNewSession
			request.OutputLanguage = normalizeDiscussionOutputLanguage(opts.OutputLanguage)
			request.BoundTools = s.discussionAgentBoundToolsForConversationAgent(ctx, conversationID, target.MemberID)
			request.BoundToolsPolicy = true
			if err := s.attachDiscussionMentionWorkspace(ctx, &request); err != nil {
				unregisterMentionRun(run)
				return nil, err
			}
			prepared = append(prepared, discussionMentionPreparedRun{Target: target, Run: run, Request: request, NextPairLoop: nextPairLoop})
		}
		if len(prepared) == 0 {
			continue
		}

		results := make([]discussionMentionRunResult, len(prepared))
		var wg sync.WaitGroup
		wg.Add(len(prepared))
		for i := range prepared {
			i := i
			go func() {
				defer wg.Done()
				response, err := s.respondToDiscussionMention(ctx, &prepared[i].Run, prepared[i].Request, emitEvent)
				results[i] = discussionMentionRunResult{Response: response, Err: err}
			}()
		}
		wg.Wait()

		for i, preparedRun := range prepared {
			run := preparedRun.Run
			target := preparedRun.Target
			response := results[i].Response
			if !s.isRunCurrent(ctx, run) {
				unregisterMentionRun(run)
				return out, nil
			}
			completedAt := firstTimeAfter(firstTimeAfter(s.now(), run.StartedAt), latest)
			if results[i].Err != nil {
				stop, saveErr := s.failDiscussionMentionRun(ctx, conversationID, run, source, target, response, results[i].Err, completedAt, emitEvent)
				unregisterMentionRun(run)
				if saveErr != nil {
					return nil, saveErr
				}
				if stop {
					return out, nil
				}
				latest = completedAt
				continue
			}
			run.CompletedAt = completedAt
			applyResponseTelemetry(&run, response)
			linkedMissions := mergeLinkedMissions(response.LinkedMissions)
			if !response.ToolLoopCompleted {
				linkedMissions, err = s.applyBoundDiscussionMentionToolCalls(ctx, conversationID, target.MemberID, source, target, response.ToolCalls, preparedRun.Request.BoundTools, completedAt, preparedRun.Request)
				if err != nil {
					stop, saveErr := s.failDiscussionMentionRun(ctx, conversationID, run, source, target, response, err, completedAt, emitEvent)
					unregisterMentionRun(run)
					if saveErr != nil {
						return nil, saveErr
					}
					if stop {
						return out, nil
					}
					latest = completedAt
					continue
				}
			}
			var stop bool
			response, linkedMissions, stop, err = s.continueDiscussionMentionTools(ctx, conversationID, &run, preparedRun.Request, source, target, response, linkedMissions, completedAt, emitEvent, unregisterMentionRun)
			if err != nil {
				return nil, err
			}
			if stop {
				return out, nil
			}
			if run.Status == "failed" {
				latest = completedAt
				continue
			}
			for _, mission := range linkedMissions {
				if strings.TrimSpace(mission.MissionID) == "" {
					continue
				}
				if saveErr := s.repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: conversationID, MissionID: mission.MissionID, Title: mission.Title, Status: mission.Status, CreatedAt: firstTime(mission.CreatedAt, completedAt)}); saveErr != nil {
					return nil, saveErr
				}
			}
			run.Status = firstNonBlank(response.Status, "completed")
			applyResponseRunProgress(&run, response)
			run.RuntimeKind = response.RuntimeKind
			run.SessionID = response.SessionID
			run.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
			run.CompletedAt = completedAt
			if err := s.saveRunAndRuntimeApprovalEvent(ctx, conversationID, run, response, completedAt); err != nil {
				return nil, err
			}
			if err := s.appendRealtimeToolEvents(ctx, conversationID, run.RunID, run.ToolCalls); err != nil {
				return nil, err
			}
			unregisterMentionRun(run)
			reply := strings.TrimSpace(response.AssistantMessage)
			if reply != "" {
				messageID, err := s.newID()
				if err != nil {
					return nil, err
				}
				replyViewer := ViewerRef{ViewerID: source.AuthorID, ViewerKind: ViewerKindUser, DisplayName: source.AuthorName}
				record := correlateMessageToRun(assistantMessageRecordWithDisplayTimeline(messageID, conversationID, target.MemberID, target.DisplayName, reply, response, firstTimeAfter(completedAt, source.CreatedAt), replyViewer), run)
				dto, err := s.persistAssistantMessageWithWorkspaceFiles(ctx, record, preparedRun.Request.Workspace)
				if err != nil {
					return nil, err
				}
				out = append(out, dto)
				if emit != nil && !emit(dto) {
					return out, nil
				}
				queue = append(queue, discussionMentionQueueItem{Message: record, PairLoop: preparedRun.NextPairLoop})
				latest = record.CreatedAt
			} else {
				latest = completedAt
			}
			if emitEvent != nil {
				runDTO := mapRuns([]RunRecord{run})[0]
				if !emitEvent(StreamEventDTO{Kind: StreamEventRunCompleted, Run: &runDTO}) {
					return out, nil
				}
			}
			if err := s.appendRealtimeRunCompleted(ctx, run); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func (s *Service) discussionAgentPairLoopLimit(ctx context.Context) (int, error) {
	if s == nil || s.mentionConfig == nil {
		return defaultDiscussionAgentPairLoopLimit, nil
	}
	value, err := s.mentionConfig.DiscussionAgentPairLoopLimit(ctx)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("%w: discussion agent pair loop limit must be positive", ErrInvalidRequest)
	}
	return value, nil
}

func nextDiscussionAgentPairLoopState(previous discussionAgentPairLoopState, sourceAgentID string, targetAgentID string) discussionAgentPairLoopState {
	sourceAgentID = strings.TrimSpace(sourceAgentID)
	targetAgentID = strings.TrimSpace(targetAgentID)
	if sourceAgentID == "" || targetAgentID == "" {
		return discussionAgentPairLoopState{}
	}
	agentA, agentB := orderedAgentPair(sourceAgentID, targetAgentID)
	count := 1
	if previous.AgentA == agentA && previous.AgentB == agentB {
		count = previous.Count + 1
	}
	return discussionAgentPairLoopState{AgentA: agentA, AgentB: agentB, Count: count}
}

func orderedAgentPair(agentA string, agentB string) (string, string) {
	agentA = strings.TrimSpace(agentA)
	agentB = strings.TrimSpace(agentB)
	if agentB < agentA {
		return agentB, agentA
	}
	return agentA, agentB
}
