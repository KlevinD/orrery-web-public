package main

import (
	"context"
	"orrery_v2/internal/platform/filestore"
	"strings"
)

type productionDiscussionArtifactCleaner struct {
	store filestore.Store
}

func (c productionDiscussionArtifactCleaner) DeleteConversationArtifacts(ctx context.Context, conversationID string) error {
	if c.store == nil || strings.TrimSpace(conversationID) == "" {
		return nil
	}
	return c.store.DeletePrefix(ctx, discussionConversationArtifactPrefix(conversationID))
}

func (c productionDiscussionArtifactCleaner) DeleteRunArtifacts(ctx context.Context, conversationID string, runID string) error {
	if c.store == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(runID) == "" {
		return nil
	}
	if err := c.store.DeletePrefix(ctx, discussionConversationArtifactPrefix(conversationID)+"runs/"+artifactKeySegment(runID)+"/"); err != nil {
		return err
	}
	return c.store.DeletePrefix(ctx, "agent-runs/"+artifactKeySegment(runID)+"/")
}

func discussionConversationArtifactPrefix(conversationID string) string {
	return "discussion-conversations/" + artifactKeySegment(conversationID) + "/"
}

func artifactKeySegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "_", "\\", "_").Replace(value)
	if value == "" || value == "." || value == ".." {
		return "unknown"
	}
	return value
}
