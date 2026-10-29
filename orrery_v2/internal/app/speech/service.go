// Package speech owns Orrery's private conversation speech use cases.
package speech

import (
	"context"
	"errors"
	"io"
	"strings"

	"orrery_v2/internal/agent/runtime/audio"
	"orrery_v2/internal/app/discussionsession"
)

type Conversations interface {
	GetConversation(context.Context, discussionsession.ViewerRef, string) (discussionsession.ConversationDTO, error)
	DownloadAttachment(context.Context, discussionsession.ViewerRef, string, string) (discussionsession.AttachmentDownloadDTO, error)
}

type Service struct {
	conversations Conversations
	provider      audio.Provider
}

var (
	ErrNotConfigured = errors.New("speech capability is not configured")
	ErrInvalidInput  = errors.New("speech input cannot be processed")
	ErrUnavailable   = errors.New("speech backend is unavailable")
)

type Audio struct {
	Data     []byte
	MIMEType string
}

func mapBackendError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, audio.ErrNotConfigured):
		return ErrNotConfigured
	case errors.Is(err, audio.ErrInvalidAudio), errors.Is(err, audio.ErrEmptyTranscript):
		return ErrInvalidInput
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return ErrUnavailable
	}
}

func NewService(conversations Conversations, provider audio.Provider) *Service {
	return &Service{conversations: conversations, provider: provider}
}

type SynthesizeInput struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Voice          string `json:"voice,omitempty"`
}

type TranscribeInput struct {
	ConversationID string `json:"conversation_id"`
	AttachmentID   string `json:"attachment_id"`
	Language       string `json:"language,omitempty"`
}

type Transcript struct {
	Text                         string `json:"text"`
	InputModality                string `json:"input_modality"`
	ApprovalConfirmationRequired bool   `json:"approval_confirmation_required"`
}

func (s *Service) Synthesize(ctx context.Context, viewer discussionsession.ViewerRef, input SynthesizeInput) (Audio, error) {
	if strings.TrimSpace(input.MessageID) == "" {
		return Audio{}, discussionsession.ErrInvalidRequest
	}
	view, err := s.privateConversation(ctx, viewer, input.ConversationID)
	if err != nil {
		return Audio{}, err
	}
	for _, message := range view.Messages {
		if message.MessageID != input.MessageID {
			continue
		}
		if message.ConversationID != view.ConversationID || message.Role != "assistant" || strings.TrimSpace(message.Content) == "" {
			return Audio{}, discussionsession.ErrInvalidRequest
		}
		if s.provider == nil {
			return Audio{}, ErrNotConfigured
		}
		result, err := s.provider.Synthesize(ctx, audio.SpeechRequest{
			Scope: requestScope(viewer, view.ConversationID+":"+message.MessageID),
			Text:  message.Content, Voice: input.Voice, Format: "mp3",
		})
		if err != nil {
			return Audio{}, mapBackendError(err)
		}
		return Audio{Data: result.Data, MIMEType: result.MIMEType}, nil
	}
	return Audio{}, discussionsession.ErrNotFound
}

func (s *Service) Transcribe(ctx context.Context, viewer discussionsession.ViewerRef, input TranscribeInput) (Transcript, error) {
	if strings.TrimSpace(input.AttachmentID) == "" {
		return Transcript{}, discussionsession.ErrInvalidRequest
	}
	view, err := s.privateConversation(ctx, viewer, input.ConversationID)
	if err != nil {
		return Transcript{}, err
	}
	if s.provider == nil {
		return Transcript{}, ErrNotConfigured
	}
	attachment, err := s.conversations.DownloadAttachment(ctx, viewer, view.ConversationID, input.AttachmentID)
	if err != nil {
		return Transcript{}, err
	}
	if attachment.Reader == nil {
		return Transcript{}, discussionsession.ErrServiceDependency
	}
	defer attachment.Reader.Close()
	if attachment.SizeBytes > audio.MaxAudioBytes {
		return Transcript{}, ErrInvalidInput
	}
	data, err := io.ReadAll(io.LimitReader(attachment.Reader, audio.MaxAudioBytes+1))
	if err != nil {
		return Transcript{}, discussionsession.ErrServiceDependency
	}
	if _, err := audio.ValidateAudio(data, attachment.ContentType); err != nil {
		return Transcript{}, mapBackendError(err)
	}
	result, err := s.provider.Transcribe(ctx, audio.TranscriptionRequest{
		Scope: requestScope(viewer, view.ConversationID+":"+input.AttachmentID),
		Data:  data, MIMEType: attachment.ContentType, Language: input.Language,
	})
	if err != nil {
		return Transcript{}, mapBackendError(err)
	}
	return Transcript{Text: result.Text, InputModality: "voice", ApprovalConfirmationRequired: true}, nil
}

func (s *Service) privateConversation(ctx context.Context, viewer discussionsession.ViewerRef, id string) (discussionsession.ConversationDTO, error) {
	empty := discussionsession.ConversationDTO{}
	if viewer.ViewerID == "" || viewer.TenantID == "" || strings.TrimSpace(id) == "" {
		return empty, discussionsession.ErrInvalidRequest
	}
	if viewer.ViewerKind != discussionsession.ViewerKindUser && viewer.ViewerKind != discussionsession.ViewerKindHuman {
		return empty, discussionsession.ErrForbidden
	}
	if s == nil || s.conversations == nil {
		return empty, discussionsession.ErrServiceDependency
	}
	view, err := s.conversations.GetConversation(ctx, viewer, id)
	if err != nil {
		return empty, err
	}
	if view.TenantID != viewer.TenantID || view.ConversationKind != discussionsession.ConversationKindPersonalAssistant {
		return empty, discussionsession.ErrForbidden
	}
	identities := append([]string{viewer.ViewerID, viewer.OwnerID}, viewer.IdentityIDs...)
	for _, identity := range identities {
		if identity != "" && identity == view.CreatedBy {
			return view, nil
		}
	}
	return empty, discussionsession.ErrForbidden
}

func requestScope(viewer discussionsession.ViewerRef, source string) audio.Scope {
	owner := viewer.OwnerID
	if owner == "" {
		owner = viewer.ViewerID
	}
	return audio.Scope{TenantID: viewer.TenantID, OwnerID: owner, SourceID: source}
}
