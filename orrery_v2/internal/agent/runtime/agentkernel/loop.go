package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"orrery_v2/internal/agent/llm"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

func (r *Runner) loop(ctx context.Context, messages []*llm.Message, turn uint64, emit kernelcompat.Emit) (agentcore.TurnResultEvent, []*llm.Message, error) {
	maxCalls := r.modelCallLimit()
	maxTools := r.config.MaxToolCalls
	if r.TurnTimeout() > 0 {
		maxTools = r.config.LongTaskMaxToolCalls
	}
	seen := map[string]bool{}
	validationRepairs := 0
	validationStopReviewed := false
	// Honor the deployed checkpoint format as well as the per-turn marker.
	if r.checkpoint != nil && r.checkpoint.BlockedResultReviewRunID == r.request.RunID {
		r.blockedTaskReviewed = true
	}
	for _, message := range validationTurnMessages(messages) {
		if message.Role == llm.User && message.Content == toolValidationStopReview {
			validationStopReviewed = true
		}
	}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			seen[call.ID] = true
		}
	}
	for r.modelCalls < maxCalls {
		if err := ctx.Err(); err != nil {
			return agentcore.TurnResultEvent{}, nil, err
		}
		var err error
		messages, err = r.compactContext(ctx, messages, turn, maxCalls, emit)
		if err != nil {
			return agentcore.TurnResultEvent{}, nil, err
		}
		r.modelCalls++
		if err := r.saveCheckpoint(ctx, CheckpointReady, messages); err != nil {
			return agentcore.TurnResultEvent{}, nil, err
		}
		message, err := r.modelStep(ctx, messages, fmt.Sprintf("kernel-%d-%d", turn, r.modelCalls), emit)
		if errors.Is(err, profilemodel.ErrContextWindowExceeded) && r.config.AutoCompact && r.windowRecoveries == 0 {
			r.windowRecoveries++
			if err := r.saveCheckpoint(ctx, CheckpointReady, messages); err != nil {
				return agentcore.TurnResultEvent{}, nil, err
			}
			messages, err = r.compactContextForced(ctx, messages, turn, maxCalls, emit, true)
			if err != nil {
				return agentcore.TurnResultEvent{}, nil, err
			}
			continue
		}
		if err != nil {
			return agentcore.TurnResultEvent{}, nil, err
		}
		if message.ResponseMeta == nil {
			return agentcore.TurnResultEvent{}, nil, ErrProtocol
		}
		finish := message.ResponseMeta.FinishReason
		r.observeContext(messages, message)
		messages = append(messages, message)
		if _, err := r.cloneContext(messages); err != nil {
			return agentcore.TurnResultEvent{}, nil, err
		}
		if len(message.ToolCalls) == 0 {
			stop := agentcore.StopReasonEndTurn
			if finish == "content_filter" {
				stop = agentcore.StopReasonRefused
			} else if finish != "stop" {
				return agentcore.TurnResultEvent{}, nil, kernelcompat.NewExecutionError("finish_reason_invalid", ErrProtocol)
			}
			if strings.TrimSpace(message.Content) == "" && stop != agentcore.StopReasonRefused {
				return agentcore.TurnResultEvent{}, nil, kernelcompat.NewExecutionError("empty_model_response", ErrProtocol)
			}
			if stop != agentcore.StopReasonRefused && !validationStopReviewed && hasUncorrectedToolValidation(messages) && r.modelCalls < maxCalls {
				validationStopReviewed = true
				messages = append(messages, llm.UserMessage(toolValidationStopReview))
				if err := r.saveCheckpoint(ctx, CheckpointReady, messages); err != nil {
					return agentcore.TurnResultEvent{}, nil, err
				}
				continue
			}
			if stop != agentcore.StopReasonRefused && (r.request.TaskID != "" || hasFormalResultContract(r.request)) && !r.blockedTaskReviewed && r.modelCalls < maxCalls && r.toolCalls < maxTools && proposedBlockedTaskResult(message.Content) {
				r.blockedTaskReviewed = true
				if r.checkpoint != nil {
					r.checkpoint.BlockedResultReviewRunID = r.request.RunID
				}
				prompt := blockedTaskStopReview
				if r.request.TaskID == "" {
					prompt = blockedResultStopReview
				}
				messages = append(messages, llm.UserMessage(prompt))
				// Persist the consumed review before another model call or
				// compaction. Recovery must not replenish this allowance.
				if err := r.saveCheckpoint(ctx, CheckpointReady, messages); err != nil {
					return agentcore.TurnResultEvent{}, nil, err
				}
				continue
			}
			if err := r.saveCheckpoint(ctx, CheckpointCompleted, messages); err != nil {
				return agentcore.TurnResultEvent{}, nil, err
			}
			return agentcore.TurnResultEvent{ResultText: message.Content, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: stop}}}, messages, nil
		}
		if finish != "tool_calls" {
			return agentcore.TurnResultEvent{}, nil, kernelcompat.NewExecutionError("finish_reason_invalid", ErrProtocol)
		}
		// Do not execute a batch that cannot fit the remaining execution budget.
		if r.modelCalls >= maxCalls || len(message.ToolCalls) > maxTools-r.toolCalls {
			return agentcore.TurnResultEvent{}, nil, ErrBudget
		}
		calls, err := r.prepareCalls(message.ToolCalls, seen)
		if err != nil {
			var invalid *toolValidationFailure
			if errors.As(err, &invalid) && validationRepairs < 3 {
				validationRepairs++
				messages, err = r.rejectInvalidToolBatch(ctx, messages, message.ToolCalls, seen, invalid, emit)
				if err != nil {
					return agentcore.TurnResultEvent{}, nil, err
				}
				continue
			}
			return agentcore.TurnResultEvent{}, nil, err
		}
		if r.batchRequiresApproval(calls) && r.canPersistApprovalWait() {
			if err := r.beginApprovalWait(ctx, messages, calls); err != nil {
				return agentcore.TurnResultEvent{}, nil, err
			}
			calls, err = r.resolveApprovalWait(ctx, messages, calls, emit)
			if err != nil {
				return agentcore.TurnResultEvent{}, nil, err
			}
		} else {
			if err := r.saveCheckpoint(ctx, CheckpointToolsPending, messages); err != nil {
				return agentcore.TurnResultEvent{}, nil, err
			}
		}
		messages, err = r.executeToolBatch(ctx, calls, messages, seen, emit)
		if err != nil {
			return agentcore.TurnResultEvent{}, nil, err
		}
		if err := r.saveCheckpoint(ctx, CheckpointReady, messages); err != nil {
			return agentcore.TurnResultEvent{}, nil, err
		}
	}
	return agentcore.TurnResultEvent{}, nil, ErrBudget
}

func (r *Runner) modelStep(ctx context.Context, messages []*llm.Message, itemID string, emit kernelcompat.Emit) (*llm.Message, error) {
	return r.modelStepWithTools(ctx, messages, itemID, emit, r.infos)
}

func (r *Runner) modelStepWithTools(ctx context.Context, messages []*llm.Message, itemID string, emit kernelcompat.Emit, tools []*llm.Tool) (*llm.Message, error) {
	input, err := cloneBounded(messages, r.config.MaxContextBytes)
	if err != nil {
		return nil, err
	}
	if !r.contextFits(input, tools, r.config.MaxContextBytes) {
		return nil, profilemodel.ErrContextWindowExceeded
	}
	modelCtx, cancel := context.WithTimeout(ctx, r.config.ModelTimeout)
	defer cancel()
	stream, err := invokeCallback(modelCtx, r.callbacks, func() (*llm.MessageStream, error) {
		r.observeDependency(modelCtx, "model_dispatch", input, tools, nil, "")
		return r.model.Stream(modelCtx, input, llm.WithTools(tools))
	}, func(stream *llm.MessageStream) {
		if stream != nil {
			stream.Close()
		}
	})
	if err != nil {
		if modelCtx.Err() != nil {
			return nil, modelCtx.Err()
		}
		if errors.Is(err, ErrCapacity) {
			return nil, ErrCapacity
		}
		if errors.Is(err, profilemodel.ErrContextWindowExceeded) {
			return nil, profilemodel.ErrContextWindowExceeded
		}
		return nil, ErrModel
	}
	if stream == nil {
		return nil, ErrProtocol
	}
	var closeOnce sync.Once
	closeStream := func() { closeOnce.Do(stream.Close) }
	defer closeStream()
	stop := context.AfterFunc(modelCtx, closeStream)
	defer stop()
	var chunks []*llm.Message
	bytes := 0
	for {
		if err := modelCtx.Err(); err != nil {
			return nil, err
		}
		chunk, err := invokeCallback(modelCtx, r.callbacks, stream.Recv, nil)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if modelCtx.Err() != nil {
				return nil, modelCtx.Err()
			}
			if errors.Is(err, ErrCapacity) {
				return nil, ErrCapacity
			}
			// A gateway may reject the input after opening its SSE response.
			// Only an empty stream is safe for automatic context recovery.
			if len(chunks) == 0 && errors.Is(err, profilemodel.ErrContextWindowExceeded) {
				return nil, profilemodel.ErrContextWindowExceeded
			}
			return nil, ErrModel
		}
		if chunk == nil || (chunk.Role != "" && chunk.Role != llm.Assistant) || len(chunk.MultiContent) != 0 || len(chunk.AssistantGenMultiContent) != 0 || len(chunk.UserInputMultiContent) != 0 {
			return nil, ErrProtocol
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil && !validModelUsage(chunk.ResponseMeta.Usage) {
			return nil, ErrProtocol
		}
		raw, err := json.Marshal(chunk)
		if err != nil {
			return nil, ErrProtocol
		}
		bytes += len(raw)
		if bytes > r.config.MaxResponseBytes || len(chunks) >= 4096 {
			return nil, ErrBudget
		}
		var snapshot llm.Message
		if json.Unmarshal(raw, &snapshot) != nil {
			return nil, ErrProtocol
		}
		chunks = append(chunks, &snapshot)
		if chunk.Content != "" {
			if err := emit(agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: itemID, Text: chunk.Content}}); err != nil {
				return nil, err
			}
		}
	}
	if err := modelCtx.Err(); err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, ErrProtocol
	}
	message, err := llm.MergeChunks(chunks)
	if err != nil {
		return nil, ErrProtocol
	}
	message.Role = llm.Assistant
	if message.ResponseMeta != nil && message.ResponseMeta.Usage != nil {
		usage := message.ResponseMeta.Usage
		if !validModelUsage(usage) {
			return nil, ErrProtocol
		}
		total := max(uint64(usage.TotalTokens), uint64(usage.PromptTokens)+uint64(usage.CompletionTokens))
		r.usage.InputTokens += uint64(usage.PromptTokens)
		r.usage.OutputTokens += uint64(usage.CompletionTokens)
		r.usage.CachedInputTokens += uint64(usage.PromptTokenDetails.CachedTokens)
		r.usage.ReasoningTokens += uint64(usage.CompletionTokensDetails.ReasoningTokens)
		delta := &agentcore.UsageDeltaEvent{InputTokens: uint64(usage.PromptTokens), OutputTokens: uint64(usage.CompletionTokens), TotalTokens: total}
		cachedKnown, reasoningKnown := profilemodel.ModelUsageDetailsKnown(message)
		if cachedKnown {
			cached := uint64(usage.PromptTokenDetails.CachedTokens)
			delta.CachedInputTokens = &cached
		}
		if reasoningKnown {
			reasoning := uint64(usage.CompletionTokensDetails.ReasoningTokens)
			delta.ReasoningTokens = &reasoning
		}
		if err := emit(agentcore.SessionEvent{Kind: agentcore.EventUsageDelta, UsageDelta: delta}); err != nil {
			return nil, err
		}
	}
	return message, nil
}
