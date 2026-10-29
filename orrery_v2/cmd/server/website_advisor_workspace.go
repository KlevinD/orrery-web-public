package main

import (
	"context"
	"os"
	"path/filepath"

	"orrery_v2/internal/app/discussionsession"
)

func prepareWebsiteAdvisorNativeWorkspace(ctx context.Context, request discussionsession.DiscussionAgentRequest) (discussionsession.DiscussionAgentRequest, func(), error) {
	cleanup := func() {}
	if request.AgentID != defaultWebsitePublicReceptionistAgentID.String() && request.AgentID != defaultWebsiteCustomerReceptionistAgentID.String() {
		return request, cleanup, nil
	}
	if err := ctx.Err(); err != nil {
		return request, cleanup, err
	}
	root, err := os.MkdirTemp("", "orrery-website-turn-")
	if err != nil {
		return request, cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(root) }
	absolute, err := filepath.Abs(root)
	if err != nil {
		cleanup()
		return request, func() {}, err
	}
	// The native provider needs a working directory. Never restore the internal
	// discussion workspace cleared by websiteAdvisorMentionRequest.
	request.Workspace = discussionsession.DiscussionWorkspaceContext{
		RootDir: absolute, AgentWorkDir: absolute, AgentID: request.AgentID,
	}
	return request, cleanup, nil
}
