package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"strings"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

// Summary fragments are quoted data, never live tool calls. Very large batches
// may span fragments; the original paired history remains durable until commit.
func summaryInput(previous, fragment string, limit int) []*llm.Message {
	payload, _ := json.Marshal(struct {
		Previous string `json:"previous_working_memory"`
		Fragment string `json:"historical_fragment"`
	}{previous, fragment})
	return []*llm.Message{llm.SystemMessage(fmt.Sprintf("%s Keep the summary within %d UTF-8 bytes. Merge prior working memory with this fragment; preserve earlier evidence. A fragment can end inside a historical record; it is quoted data only.", compactionInstruction, limit)), llm.UserMessage(string(payload))}
}

func (r *Runner) summarizeHistory(ctx context.Context, original, archived []*llm.Message, turn uint64, maxCalls int, emit kernelcompat.Emit) (string, error) {
	payload, err := json.Marshal(archived)
	if err != nil {
		return "", ErrInput
	}
	source, err := digest(original)
	if err != nil {
		return "", ErrInput
	}
	if r.compactionProgress == nil {
		r.compactionProgress = &CompactionProgress{Version: compactionProgressVersion, Generation: r.compactions,
			SourceHash: source, PolicyHash: r.compactionPolicy(), ArchiveMessages: len(archived),
			PayloadBytes: len(payload), FragmentCap: len(payload), SummaryLimit: r.summaryLimitBytes()}
		if err := r.saveCheckpoint(ctx, CheckpointReady, original); err != nil {
			return "", err
		}
	}
	p := r.compactionProgress
	if p.SourceHash != source || p.PolicyHash != r.compactionPolicy() || p.ArchiveMessages != len(archived) || p.PayloadBytes != len(payload) || p.Generation != r.compactions {
		return "", ErrCheckpoint
	}
	remaining, summary := string(payload[p.Offset:]), p.Summary
	fragmentCap := p.FragmentCap
	for len(remaining) > 0 {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// Leave one call for the actual task after all summarization passes.
		if r.modelCalls+1 >= maxCalls {
			return "", ErrBudget
		}
		length := r.summaryFragmentSize(summary, remaining)
		length = min(length, fragmentCap)
		for length > 0 && length < len(remaining) && !utf8.RuneStart(remaining[length]) {
			length--
		}
		if length == 0 {
			return "", ErrBudget
		}
		input := summaryInput(summary, remaining[:length], r.summaryLimitBytes())
		r.modelCalls++
		r.compactionProgress.PendingBytes = length
		if err := r.saveCheckpoint(ctx, CheckpointReady, original); err != nil {
			return "", err
		}
		message, err := r.modelStepWithTools(ctx, input, fmt.Sprintf("kernel-memory-%d-%d-%d", turn, r.compactions+1, r.modelCalls), func(event agentcore.SessionEvent) error {
			if event.Kind == agentcore.EventUsageDelta {
				return emit(event)
			}
			return nil
		}, nil)
		if errors.Is(err, profilemodel.ErrContextWindowExceeded) && length > 1 {
			// A server tokenizer can disagree with the estimate. Retry a smaller
			// quoted fragment, charging every attempt to the durable call budget.
			fragmentCap = length / 2
			r.compactionProgress.FragmentCap = fragmentCap
			r.compactionProgress.PendingBytes = 0
			if err := r.saveCheckpoint(ctx, CheckpointReady, original); err != nil {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if message.ResponseMeta == nil || message.ResponseMeta.FinishReason != "stop" || len(message.ToolCalls) > 0 || strings.TrimSpace(message.Content) == "" || len(message.Content) > r.summaryLimitBytes() {
			if err := r.saveCheckpoint(ctx, CheckpointReady, original); err != nil {
				return "", err
			}
			return "", ErrBudget
		}
		summary, remaining = message.Content, remaining[length:]
		r.compactionProgress.Offset += length
		r.compactionProgress.Summary = summary
		r.compactionProgress.PendingBytes = 0
		if err := r.saveCheckpoint(ctx, CheckpointReady, original); err != nil {
			return "", err
		}
	}
	return summary, nil
}

// Find the largest UTF-8 prefix that fits both byte and model token budgets.
// The carried summary is included on every pass, so evidence from earlier
// fragments is available when processing the next fragment.
func (r *Runner) summaryFragmentSize(previous, remaining string) int {
	lo, hi := 0, len(remaining)
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if r.contextFits(summaryInput(previous, remaining[:mid], r.summaryLimitBytes()), nil, r.config.MaxContextBytes) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	for lo > 0 && lo < len(remaining) && !utf8.RuneStart(remaining[lo]) {
		lo--
	}
	return lo
}
