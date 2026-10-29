package speech

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"orrery_v2/internal/agent/runtime/audio"
	"orrery_v2/internal/app/discussionsession"
)

type testConversations struct {
	view          discussionsession.ConversationDTO
	err           error
	downloadErr   error
	downloadCalls int
	viewer        discussionsession.ViewerRef
}

func (c *testConversations) GetConversation(_ context.Context, viewer discussionsession.ViewerRef, _ string) (discussionsession.ConversationDTO, error) {
	c.viewer = viewer
	return c.view, c.err
}

func (c *testConversations) DownloadAttachment(context.Context, discussionsession.ViewerRef, string, string) (discussionsession.AttachmentDownloadDTO, error) {
	c.downloadCalls++
	if c.downloadErr != nil {
		return discussionsession.AttachmentDownloadDTO{}, c.downloadErr
	}
	data := append([]byte("RIFF\x28\x00\x00\x00WAVEfmt "), make([]byte, 32)...)
	return discussionsession.AttachmentDownloadDTO{Reader: io.NopCloser(bytes.NewReader(data)), ContentType: "audio/wav", SizeBytes: int64(len(data))}, nil
}

type testProvider struct {
	calls         int
	speech        audio.SpeechRequest
	transcription audio.TranscriptionRequest
}

func (p *testProvider) Synthesize(_ context.Context, input audio.SpeechRequest) (audio.Speech, error) {
	p.calls++
	p.speech = input
	return audio.Speech{Data: []byte("ID3speech"), MIMEType: "audio/mpeg"}, nil
}

func (p *testProvider) Transcribe(_ context.Context, input audio.TranscriptionRequest) (audio.Transcript, error) {
	p.calls++
	p.transcription = input
	return audio.Transcript{Text: "approve", Source: "test"}, nil
}

func fixture() (*testConversations, *testProvider, discussionsession.ViewerRef) {
	return &testConversations{view: discussionsession.ConversationDTO{
		ConversationID: "c", TenantID: "tenant-a", CreatedBy: "human-a", ConversationKind: discussionsession.ConversationKindPersonalAssistant,
		Messages: []discussionsession.MessageDTO{{ConversationID: "c", MessageID: "m", Role: "assistant", Content: "public reply", Metadata: map[string]string{"reasoning": "private reasoning"}}},
	}}, &testProvider{}, discussionsession.ViewerRef{ViewerID: "human-a", OwnerID: "account-a", TenantID: "tenant-a", ViewerKind: discussionsession.ViewerKindHuman}
}

func TestSpeechOwnsIdentityAndUsesOnlyPersistedAssistantText(t *testing.T) {
	c, p, viewer := fixture()
	_, err := NewService(c, p).Synthesize(context.Background(), viewer, SynthesizeInput{ConversationID: "c", MessageID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if p.speech.Text != "public reply" || p.speech.Scope.OwnerID != "account-a" || p.speech.Scope.TenantID != "tenant-a" || p.calls != 1 {
		t.Fatalf("provider input=%+v calls=%d", p.speech, p.calls)
	}
}

func TestSpeechRejectsOtherUsersTenantsOrganizationsAndNonHumanCallers(t *testing.T) {
	for _, name := range []string{"owner", "tenant", "organization", "agent", "system", "access"} {
		t.Run(name, func(t *testing.T) {
			c, p, viewer := fixture()
			switch name {
			case "owner":
				c.view.CreatedBy = "human-b"
			case "tenant":
				c.view.TenantID = "tenant-b"
			case "organization":
				c.view.ConversationKind = "discussion"
			case "agent":
				viewer.ViewerKind = discussionsession.ViewerKindAgent
			case "system":
				viewer.ViewerKind = discussionsession.ViewerKindSystem
			case "access":
				c.err = discussionsession.ErrForbidden
			}
			s := NewService(c, p)
			_, err := s.Synthesize(context.Background(), viewer, SynthesizeInput{ConversationID: "c", MessageID: "m"})
			if !errors.Is(err, discussionsession.ErrForbidden) {
				t.Fatalf("synthesis err=%v", err)
			}
			_, err = s.Transcribe(context.Background(), viewer, TranscribeInput{ConversationID: "c", AttachmentID: "a"})
			if !errors.Is(err, discussionsession.ErrForbidden) || p.calls != 0 || c.downloadCalls != 0 {
				t.Fatalf("transcription err=%v calls=%d", err, p.calls)
			}
		})
	}
}

func TestSpeechRejectsToolAndUserMessages(t *testing.T) {
	for _, role := range []string{"tool", "user", "system"} {
		c, p, viewer := fixture()
		c.view.Messages[0].Role = role
		_, err := NewService(c, p).Synthesize(context.Background(), viewer, SynthesizeInput{ConversationID: "c", MessageID: "m"})
		if !errors.Is(err, discussionsession.ErrInvalidRequest) || p.calls != 0 {
			t.Fatalf("role=%s err=%v", role, err)
		}
	}
}

func TestSpeechTranscriptionKeepsVoiceProvenanceAndAttachmentAuthorization(t *testing.T) {
	c, p, viewer := fixture()
	s := NewService(c, p)
	result, err := s.Transcribe(context.Background(), viewer, TranscribeInput{ConversationID: "c", AttachmentID: "a"})
	if err != nil || result.InputModality != "voice" || !result.ApprovalConfirmationRequired {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if p.transcription.Scope.OwnerID != "account-a" || p.transcription.Scope.SourceID != "c:a" {
		t.Fatalf("scope=%+v", p.transcription.Scope)
	}
	c.downloadErr = discussionsession.ErrNotFound
	_, err = s.Transcribe(context.Background(), viewer, TranscribeInput{ConversationID: "c", AttachmentID: "other-conversation"})
	if !errors.Is(err, discussionsession.ErrNotFound) || p.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, p.calls)
	}
}

func TestSpeechDisabledBackendDoesNotImplySuccess(t *testing.T) {
	c, _, viewer := fixture()
	_, err := NewService(c, nil).Synthesize(context.Background(), viewer, SynthesizeInput{ConversationID: "c", MessageID: "m"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}
