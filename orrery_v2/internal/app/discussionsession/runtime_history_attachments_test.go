package discussionsession

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestFreshDiscussionSessionRestoresLatestImageWithinConversation(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "image.png")
	data := discussionTestPNG(t)
	if err := os.WriteFile(imagePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	request := DiscussionAgentRequest{
		ConversationID: "conv", MessageID: "current", Content: "Store the information from before.",
		Workspace: DiscussionWorkspaceContext{AttachmentsDir: root},
		History:   []MessageDTO{{ConversationID: "conv", MessageID: "image", Attachments: []AttachmentDTO{{AttachmentID: "attachment", ConversationID: "conv", MessageID: "image", FileName: "image.png", ContentType: "image/png", StoragePath: imagePath, SizeBytes: int64(len(data))}}}},
	}
	blocks, err := discussionRuntimeInputMediaForSession(request, discussionExternalCLISession{})
	if err != nil || len(blocks) != 2 || blocks[1].Kind != agentcore.ContentBlockImage {
		t.Fatalf("historical image was not restored: blocks=%d err=%v", len(blocks), err)
	}
	prompt := discussionExternalCLIPrompt(request, discussionExternalCLISession{})
	if !strings.Contains(prompt, "attachment") || !strings.Contains(prompt, "image.png") {
		t.Fatal("image-only history was discarded")
	}
	blocks, err = discussionRuntimeInputMediaForSession(request, discussionExternalCLISession{SessionID: "existing"})
	if err != nil || len(blocks) != 0 {
		t.Fatal("resumed turn duplicated native image history")
	}
	request.History[0].Attachments[0].ConversationID = "other-conversation"
	blocks, err = discussionRuntimeInputMediaForSession(request, discussionExternalCLISession{})
	if err != nil || len(blocks) != 0 {
		t.Fatal("restored another conversation's image")
	}
}

func TestExpiredHistoricalImageDoesNotBreakTextChat(t *testing.T) {
	root := t.TempDir()
	request := DiscussionAgentRequest{ConversationID: "conv", MessageID: "current", Workspace: DiscussionWorkspaceContext{AttachmentsDir: root}, History: []MessageDTO{{MessageID: "old", Attachments: []AttachmentDTO{{ConversationID: "conv", MessageID: "old", ContentType: "image/png", StoragePath: filepath.Join(root, "missing.png")}}}}}
	blocks, err := discussionRuntimeInputMediaForSession(request, discussionExternalCLISession{})
	if err != nil || len(blocks) != 1 || blocks[0].Kind != agentcore.ContentBlockText || !strings.Contains(blocks[0].Text, "could not be reloaded") {
		t.Fatal("expired historical image blocked chat or was silently fabricated")
	}
}
