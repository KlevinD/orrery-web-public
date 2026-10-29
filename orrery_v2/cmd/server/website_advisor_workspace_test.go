package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"orrery_v2/internal/app/discussionsession"
)

func TestWebsiteAdvisorNativeWorkspaceIsPrivatePerTurnAndCleanedUp(t *testing.T) {
	internal := t.TempDir()
	for _, profile := range defaultWebsiteAdvisorProfiles() {
		request := websiteAdvisorMentionRequest(profile, discussionsession.DiscussionAgentRequest{
			AgentID:     profile.ID.String(),
			Workspace:   discussionsession.DiscussionWorkspaceContext{RootDir: internal, AgentWorkDir: internal},
			History:     []discussionsession.MessageDTO{{Content: "internal history"}},
			Attachments: []discussionsession.AttachmentDTO{{FileName: "internal.txt"}},
		})
		first, cleanupFirst, err := prepareWebsiteAdvisorNativeWorkspace(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanupFirst)
		second, cleanupSecond, err := prepareWebsiteAdvisorNativeWorkspace(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanupSecond)
		root := first.Workspace.RootDir
		if !filepath.IsAbs(root) || first.Workspace.AgentWorkDir != root || root == internal || root == second.Workspace.RootDir {
			t.Fatalf("website native workspace is not isolated per turn: first=%+v second=%+v", first.Workspace, second.Workspace)
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 || len(first.History) != 0 || len(first.Attachments) != 0 || !first.ForceNewSession {
			t.Fatalf("website turn inherited runtime context: request=%+v entries=%v err=%v", first, entries, err)
		}
		cleanupFirst()
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("website workspace remains after cleanup: %v", err)
		}
	}
}

func TestWebsiteAdvisorNativeWorkspaceRejectsCanceledTurnAndPreservesOtherAgents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, cleanup, err := prepareWebsiteAdvisorNativeWorkspace(ctx, discussionsession.DiscussionAgentRequest{AgentID: defaultWebsitePublicReceptionistAgentID.String()})
	cleanup()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled website turn error = %v", err)
	}
	request := discussionsession.DiscussionAgentRequest{AgentID: "internal-agent", Workspace: discussionsession.DiscussionWorkspaceContext{RootDir: t.TempDir()}}
	got, cleanup, err := prepareWebsiteAdvisorNativeWorkspace(context.Background(), request)
	defer cleanup()
	if err != nil || !reflect.DeepEqual(got, request) {
		t.Fatalf("non-website request changed: %+v, %v", got, err)
	}
}
