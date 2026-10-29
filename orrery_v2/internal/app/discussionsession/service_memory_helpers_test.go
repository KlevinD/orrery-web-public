package discussionsession

import "context"

type capturingDiscussionAgentMemoryProvider struct {
	called     bool
	agentID    string
	query      string
	context    DiscussionAgentMemoryContext
	err        error
	exchanges  []PersonalAssistantMemoryExchange
	captureErr error
}

func (p *capturingDiscussionAgentMemoryProvider) DiscussionAgentMemoryContext(_ context.Context, agentID string) (DiscussionAgentMemoryContext, error) {
	p.called = true
	p.agentID = agentID
	return p.context, p.err
}

func (p *capturingDiscussionAgentMemoryProvider) DiscussionAgentMemoryContextForQuery(_ context.Context, agentID, query string) (DiscussionAgentMemoryContext, error) {
	p.called = true
	p.agentID = agentID
	p.query = query
	return p.context, p.err
}

func (p *capturingDiscussionAgentMemoryProvider) CapturePersonalAssistantMemoryExchange(_ context.Context, exchange PersonalAssistantMemoryExchange) error {
	p.exchanges = append(p.exchanges, exchange)
	return p.captureErr
}
