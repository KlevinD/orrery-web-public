package discussionsession

import (
	"context"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

func runtimeProgressFromAgent(agent agentcore.Agent, runID agentcore.AgentRunID) (<-chan agentcore.RuntimeMessage, func()) {
	if agent == nil || agent.Base() == nil {
		return nil, func() {}
	}
	reader, ok := agent.Base().Runtime().(agentcore.SessionRuntimeRecordReader)
	if !ok {
		return nil, func() {}
	}
	return canonicalRuntimeProgress(reader, runID)
}

func runtimeProgressHistoryFromAgent(agent agentcore.Agent, runID agentcore.AgentRunID) []agentcore.RuntimeMessage {
	if agent == nil || agent.Base() == nil {
		return nil
	}
	reader, ok := agent.Base().Runtime().(agentcore.SessionRuntimeRecordReader)
	if !ok {
		return nil
	}
	return canonicalRuntimeProgressHistory(reader, runID)
}

func canonicalRuntimeProgress(reader agentcore.SessionRuntimeRecordReader, runID agentcore.AgentRunID) (<-chan agentcore.RuntimeMessage, func()) {
	events, unsubscribe := reader.SubscribeRuntimeEvents(runID)
	out := make(chan agentcore.RuntimeMessage, 256)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer close(out)
		var projection *runtimesession.ResponseProjection
		for {
			select {
			case <-ctx.Done():
				return
			case envelope, ok := <-events:
				if !ok {
					return
				}
				if projection == nil {
					request, _ := reader.RuntimeRequest(runID)
					projection = runtimesession.NewResponseProjection(request, runtimeKindFromRequest(request), time.Now().UTC())
				}
				if err := projection.Fold(envelope); err != nil {
					return
				}
				for _, message := range projection.LastFoldMessages() {
					select {
					case out <- message:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	var once sync.Once
	return out, func() {
		once.Do(func() {
			cancel()
			unsubscribe()
		})
	}
}

func canonicalRuntimeProgressHistory(reader agentcore.SessionRuntimeRecordReader, runID agentcore.AgentRunID) []agentcore.RuntimeMessage {
	request, _ := reader.RuntimeRequest(runID)
	projection := runtimesession.NewResponseProjection(request, runtimeKindFromRequest(request), time.Now().UTC())
	for _, envelope := range reader.RuntimeEvents(runID) {
		if envelope.Event.Kind == agentcore.EventLagged {
			continue
		}
		if err := projection.Fold(envelope); err != nil {
			return nil
		}
	}
	return projection.IncrementalMessages()
}

func runtimeKindFromRequest(request agentcore.RuntimeRequest) agentcore.RuntimeKind {
	if kind := agentcore.RuntimeKind(strings.TrimSpace(request.RuntimeOptions.ProviderSpecific["runtime_kind"])); kind.IsValid() {
		return kind
	}
	return agentcore.RuntimeKindManaged
}
