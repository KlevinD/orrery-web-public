package agentkernel

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func kernelImage(t *testing.T) agentcore.ContentBlock {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return agentcore.ContentBlock{Kind: agentcore.ContentBlockImage, MediaType: "image/png", Data: buffer.Bytes()}
}

func TestLoopCarriesAuthorizedImageAndAttachmentTextToModel(t *testing.T) {
	img := kernelImage(t)
	m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		last := messages[len(messages)-1]
		found := false
		for _, part := range last.UserInputMultiContent {
			if part.Image != nil && part.Image.Base64Data != nil {
				decoded, err := base64.StdEncoding.DecodeString(*part.Image.Base64Data)
				if err != nil || !bytes.Equal(decoded, img.Data) {
					t.Fatal("image data changed")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("image missing from actual model call")
		}
		var text strings.Builder
		text.WriteString(last.Content)
		for _, part := range last.UserInputMultiContent {
			text.WriteString(part.Text)
		}
		if !strings.Contains(text.String(), "verified attachment PF-9R2K") {
			t.Fatal("attachment extraction missing")
		}
		return llm.NewMessageStream([]*llm.Message{reply("done", "stop")}), nil
	}}
	r, send := openRunner(t, m, func(_ *Config, req *agentcore.RuntimeRequest) {
		req.InputMedia = []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "\nverified attachment PF-9R2K\n"}, img}
	})
	send.Content = append(send.Content, r.request.InputMedia...)
	if _, err := r.Run(context.Background(), send, discard); err != nil {
		t.Fatal(err)
	}
}

func TestLoopRejectsImageSubstitutionBeforeModelCall(t *testing.T) {
	img := kernelImage(t)
	m := &modelFunc{}
	r, send := openRunner(t, m, func(_ *Config, req *agentcore.RuntimeRequest) { req.InputMedia = []agentcore.ContentBlock{img} })
	img.Data = append([]byte(nil), img.Data...)
	img.Data[len(img.Data)-1] ^= 1
	send.Content = append(send.Content, img)
	if _, err := r.Run(context.Background(), send, discard); !errors.Is(err, ErrInput) || m.calls.Load() != 0 {
		t.Fatalf("substitution accepted: %v", err)
	}
}

func TestImageContextHasSeparateTransportBudget(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 1<<20))
	m := llm.UserMessage("bounded text")
	m.UserInputMultiContent = []llm.MessageInputPart{{Type: llm.ChatMessagePartTypeImageURL, Image: &llm.MessageInputImage{MessagePartCommon: llm.MessagePartCommon{Base64Data: &encoded, MIMEType: "image/png"}}}}
	if _, err := cloneBounded([]*llm.Message{m}, 512<<10); err != nil {
		t.Fatalf("base64 charged as text: %v", err)
	}
	m.Content = strings.Repeat("x", 600<<10)
	if _, err := cloneBounded([]*llm.Message{m}, 512<<10); err == nil {
		t.Fatal("text context cap bypassed")
	}
}

func TestAttachmentWithTrailingHostPolicyRunsAndResumesWithCheckpoint(t *testing.T) {
	img := kernelImage(t)
	m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		var user *llm.Message
		policy := false
		for _, message := range messages {
			if message.Role == llm.User {
				user = message
			}
			if message.Role == llm.System && message.Content == "host policy" {
				policy = true
				if len(message.UserInputMultiContent) != 0 {
					t.Fatal("attachment became policy")
				}
			}
		}
		if !policy || user == nil || len(user.UserInputMultiContent) == 0 {
			t.Fatal("policy or attachment lost")
		}
		return llm.NewMessageStream([]*llm.Message{reply("done", "stop")}), nil
	}}
	r, send := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		req.InputMessages = agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{
			InputMessages: req.InputMessages,
			Blocks: []agentcore.RuntimePromptBlock{
				{BlockID: agentcore.PromptBlockCurrentUserInput, Role: "user", Enabled: true},
				{BlockID: "host_policy", Role: "system", Content: "host policy", Enabled: true},
			},
		})
		req.InputMedia = []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "\nfile contents\n"}, img}
	})
	send.Content = append(send.Content, r.request.InputMedia...)
	if _, err := r.Run(context.Background(), send, discard); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumeTestRunner(t, r, r.request, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resumed.Run(context.Background(), send, discard); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointMediaBudgetKeepsTextAndImageLimits(t *testing.T) {
	c := testCheckpoint(t)
	c.Revision = 1
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 2<<20))
	c.Messages[0].UserInputMultiContent = []llm.MessageInputPart{{Type: llm.ChatMessagePartTypeImageURL, Image: &llm.MessageInputImage{MessagePartCommon: llm.MessagePartCommon{Base64Data: &encoded, MIMEType: "image/png"}}}}
	if _, err := checkpointRecord(c); err != nil {
		t.Fatal(err)
	}
	c.Messages[0].Content = strings.Repeat("x", 2<<20)
	if _, err := checkpointRecord(c); err == nil {
		t.Fatal("text checkpoint limit bypassed")
	}
	c.Messages[0].Content = "bounded"
	encoded = base64.StdEncoding.EncodeToString(make([]byte, maxKernelMediaBytes+4))
	if _, err := checkpointRecord(c); err == nil {
		t.Fatal("media checkpoint limit bypassed")
	}
}
