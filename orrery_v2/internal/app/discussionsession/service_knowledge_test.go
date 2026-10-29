package discussionsession

import "context"

type capturingDiscussionAgentKnowledgeProvider struct {
	called  bool
	request DiscussionAgentRequest
	context DiscussionAgentKnowledgeContext
	err     error
}

func (p *capturingDiscussionAgentKnowledgeProvider) DiscussionAgentKnowledgeContext(_ context.Context, request DiscussionAgentRequest) (DiscussionAgentKnowledgeContext, error) {
	p.called = true
	p.request = request
	return p.context, p.err
}
