package discussionsession

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/audio"
)

// Explicit Linux acceptance uses the same controlled uploaded files as the live
// dialogue, with the production PDF/ffmpeg decoders rather than shell doubles.
func TestRuntimeAttachmentNativeFixtureAcceptance(t *testing.T) {
	fixtures := os.Getenv("ORRERY_MULTIMODAL_FIXTURES")
	if fixtures == "" {
		t.Skip("controlled media fixtures are not configured")
	}
	for _, tc := range []struct {
		name, mime, reference string
		images                int
	}{{"b1.txt", "text/plain", "TX-7M4Q", 0}, {"b2.pdf", "application/pdf", "PF-9R2K", 0}, {"b3.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "DX-5P8N", 0}, {"c1.mp4", "video/mp4", "Video duration", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			req := attachmentRequest(t, tc.mime)
			data, err := os.ReadFile(filepath.Join(fixtures, tc.name))
			if err != nil {
				t.Fatal(err)
			}
			req.Attachments[0].FileName = tc.name
			if err = os.WriteFile(req.Attachments[0].StoragePath, data, 0600); err != nil {
				t.Fatal(err)
			}
			blocks, err := NewRuntimeAttachmentProcessor(nil).Prepare(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			images := 0
			for _, b := range blocks {
				if b.Kind == agentcore.ContentBlockImage {
					images++
				} else {
					text.WriteString(b.Text)
				}
			}
			if images != tc.images || !strings.Contains(text.String(), tc.reference) || strings.Contains(text.String(), "processing failed") {
				t.Fatalf("native extraction failed: images=%d text=%q", images, text.String())
			}
		})
	}
}

type attachmentTranscriber struct {
	called int
	input  audio.TranscriptionRequest
}

func (p *attachmentTranscriber) Transcribe(_ context.Context, input audio.TranscriptionRequest) (audio.Transcript, error) {
	p.called++
	p.input = input
	return audio.Transcript{Text: "blue kite 40"}, nil
}

func attachmentRequest(t *testing.T, kind string) DiscussionAgentRequest {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "input")
	if err := os.WriteFile(path, []byte("Reference TX-7M4Q 170"), 0600); err != nil {
		t.Fatal(err)
	}
	return DiscussionAgentRequest{ConversationID: "own-conversation", MessageID: "message", ConversationKind: ConversationKindPersonalAssistant, ConversationOwnerID: "owner", ConversationTenantID: "tenant", Workspace: DiscussionWorkspaceContext{AttachmentsDir: root, AgentWorkDir: root}, Attachments: []AttachmentDTO{{ConversationID: "own-conversation", MessageID: "message", AttachmentID: "attachment", ContentType: kind, FileName: "input", StoragePath: path}}}
}

func TestAttachmentPreparationUsesAuthorizedContentsAndPrivateVoiceScope(t *testing.T) {
	req := attachmentRequest(t, "text/plain")
	p := NewRuntimeAttachmentProcessor(nil)
	blocks, err := p.Prepare(context.Background(), req)
	if err != nil || len(blocks) != 1 || !strings.Contains(blocks[0].Text, "TX-7M4Q 170") {
		t.Fatalf("blocks=%v err=%v", blocks, err)
	}
	voice := &attachmentTranscriber{}
	p.Transcriber = voice
	req.Attachments[0].ContentType = "audio/wav"
	blocks, err = p.Prepare(context.Background(), req)
	if err != nil || voice.called != 1 || !strings.Contains(blocks[0].Text, "blue kite 40") || voice.input.Scope.OwnerID != "owner" || voice.input.Scope.TenantID != "tenant" {
		t.Fatalf("voice scope lost: %+v %v", voice, err)
	}
	req.ConversationKind = "organization"
	_, err = p.Prepare(context.Background(), req)
	if err != nil || voice.called != 1 {
		t.Fatal("nonprivate conversation invoked speech provider")
	}
}

func TestManagedVoiceInputAlwaysRendersHostConfirmationPolicy(t *testing.T) {
	req := attachmentRequest(t, "audio/wav")
	req.Content = "Please read the voice attachment"
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged}})
	runner.attachmentProcessor = NewRuntimeAttachmentProcessor(&attachmentTranscriber{})
	input, err := runner.agentInput(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{Blocks: input.RuntimeOptions.PromptBlocks, InputMessages: input.InputMessages, RuntimeVariables: input.RuntimeVariables})
	policy := agentcore.RuntimePromptMessagesSystemPrompt(messages)
	if !strings.Contains(policy, "A spoken approval") || !strings.Contains(policy, "separate explicit text confirmation") {
		t.Fatal("voice confirmation policy did not reach system prompt")
	}
	if !strings.Contains(policy, "Use this server-generated transcript to answer questions") || !strings.Contains(policy, "If transcription actually failed") {
		t.Fatal("voice input policy must allow content interpretation without pretending direct playback")
	}
	if len(input.InputMedia) == 0 || !strings.Contains(input.InputMedia[0].Text, "original attachment bytes") || !strings.Contains(input.InputMedia[0].Text, "blue kite 40") {
		t.Fatal("voice evidence must retain its server provenance and recognized content")
	}
}

func TestAttachmentPreparationRejectsCrossConversationAndEscapedPaths(t *testing.T) {
	for _, mode := range []string{"conversation", "path"} {
		t.Run(mode, func(t *testing.T) {
			req := attachmentRequest(t, "text/plain")
			if mode == "conversation" {
				req.Attachments[0].ConversationID = "other"
			} else {
				req.Attachments[0].StoragePath = filepath.Join(t.TempDir(), "outside")
			}
			if _, err := NewRuntimeAttachmentProcessor(nil).Prepare(context.Background(), req); !errors.Is(err, ErrForbidden) {
				t.Fatalf("boundary bypass: %v", err)
			}
		})
	}
}
