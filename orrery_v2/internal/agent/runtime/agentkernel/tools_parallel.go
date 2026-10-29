package agentkernel

import (
	"context"
	"orrery_v2/internal/agent/llm"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
)

func (r *Runner) parallelTool(call preparedCall) bool {
	if r.config.MaxParallelTools < 2 || r.requiresToolApproval(call) {
		return false
	}
	for _, id := range r.config.ParallelToolIDs {
		if id == string(call.tool.binding.ToolID) {
			return true
		}
	}
	return false
}

// Only contiguous host-approved read calls overlap. An unknown or stateful
// tool is an ordering barrier, regardless of its name or model-supplied input.
func (r *Runner) executeToolBatch(ctx context.Context, calls []preparedCall, messages []*llm.Message, seen map[string]bool, emit kernelcompat.Emit) ([]*llm.Message, error) {
	for offset := 0; offset < len(calls); {
		end := offset + 1
		if r.parallelTool(calls[offset]) {
			for end < len(calls) && end-offset < r.config.MaxParallelTools && r.parallelTool(calls[end]) {
				end++
			}
		}
		var results []*llm.Message
		if end-offset > 1 {
			var err error
			results, err = r.executeParallelTools(ctx, calls[offset:end], emit)
			if err != nil {
				return nil, err
			}
		} else {
			result, err := r.executeTool(ctx, calls[offset], emit)
			if err != nil {
				return nil, err
			}
			results = []*llm.Message{result}
		}
		// Results enter the next model request in the original call order,
		// independently of completion/event order. No partial batch is saved.
		for index, result := range results {
			seen[calls[offset+index].call.ID] = true
			r.toolCalls++
			messages = append(messages, result)
			if _, err := r.cloneContext(messages); err != nil {
				return nil, err
			}
		}
		offset = end
	}
	return messages, nil
}

func (r *Runner) executeParallelTools(ctx context.Context, calls []preparedCall, emit kernelcompat.Emit) ([]*llm.Message, error) {
	journal, ok := r.config.ToolJournal.(BatchToolJournal)
	if !ok {
		return nil, ErrConfiguration
	}
	admission, stop := context.WithTimeout(ctx, r.config.ToolTimeout)
	intents := make([]ToolExecution, len(calls))
	for index, call := range calls {
		if err := r.authorizeTool(admission, call); err != nil {
			stop()
			return nil, err
		}
		intent, err := r.toolExecution(call)
		if err != nil {
			stop()
			return nil, err
		}
		intents[index] = intent
	}
	claimCtx, cancelClaim := context.WithTimeout(admission, r.config.JournalTimeout)
	claims, err := journal.BeginBatch(claimCtx, intents)
	cancelClaim()
	stop()
	if err != nil {
		return nil, err
	}
	if len(claims) != len(intents) {
		return nil, ErrJournal
	}
	for index, claim := range claims {
		if !sameExecution(claim, intents[index]) || claim.State != ToolExecutionStarted || claim.ClaimID == "" {
			return nil, ErrJournal
		}
	}
	group, cancelGroup := context.WithCancel(ctx)
	defer cancelGroup()
	results := make([]*llm.Message, len(calls))
	var workers sync.WaitGroup
	var failed sync.Once
	var firstErr error
	var events sync.Mutex
	serializedEmit := func(event agentcore.SessionEvent) error {
		events.Lock()
		defer events.Unlock()
		return emit(event)
	}
	for index, call := range calls {
		workers.Add(1)
		go func(index int, call preparedCall) {
			defer func() {
				if recover() != nil {
					failed.Do(func() { firstErr = errCallbackPanic; cancelGroup() })
				}
				workers.Done()
			}()
			result, err := r.executeClaimedTool(group, call, serializedEmit, &claims[index])
			if err != nil {
				failed.Do(func() { firstErr = err; cancelGroup() })
				return
			}
			results[index] = result
		}(index, call)
	}
	workers.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return results, firstErr
}
