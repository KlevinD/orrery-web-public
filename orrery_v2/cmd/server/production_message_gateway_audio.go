package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	cloudaudio "orrery_v2/internal/agent/runtime/audio"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/speech"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

type productionAudioDecoder interface {
	Decode(context.Context, []byte) ([]byte, error)
}

type productionGatewayAudioService struct {
	transcriber cloudaudio.Transcriber
	decoder     productionAudioDecoder
}

func newProductionGatewayAudioService(profiles llmprofile.ProfileProvider, secrets llmprofile.SecretResolver) *productionGatewayAudioService {
	provider := newProductionSpeechProvider(profiles, secrets)
	if provider == nil {
		return nil
	}
	return &productionGatewayAudioService{transcriber: provider, decoder: cloudaudio.SILKDecoder{
		NodePath:   strings.TrimSpace(os.Getenv("ORRERY_AUDIO_SILK_NODE")),
		ScriptPath: strings.TrimSpace(os.Getenv("ORRERY_AUDIO_SILK_SCRIPT")),
	}}
}

func newProductionPersonalAssistantSpeech(conversations speech.Conversations, profiles llmprofile.ProfileProvider, secrets llmprofile.SecretResolver) *speech.Service {
	return speech.NewService(conversations, newProductionSpeechProvider(profiles, secrets))
}

func newProductionSpeechProvider(profiles llmprofile.ProfileProvider, secrets llmprofile.SecretResolver) cloudaudio.Provider {
	// Opt-in deployment settings, never inferred from a successful chat request.
	var provider cloudaudio.Provider
	switch os.Getenv("ORRERY_AUDIO_PROTOCOL") {
	case "local-sherpa":
		provider = cloudaudio.NewLocal(cloudaudio.LocalConfig{
			PythonPath: strings.TrimSpace(os.Getenv("ORRERY_AUDIO_LOCAL_PYTHON")),
			ASRDir:     strings.TrimSpace(os.Getenv("ORRERY_AUDIO_LOCAL_ASR_DIR")),
			TTSDir:     strings.TrimSpace(os.Getenv("ORRERY_AUDIO_LOCAL_TTS_DIR")),
			WorkDir:    strings.TrimSpace(os.Getenv("ORRERY_AUDIO_LOCAL_WORK_DIR")),
		})
	case "openclaw":
		provider = cloudaudio.NewOpenClaw(cloudaudio.OpenClawConfig{
			NodePath:    strings.TrimSpace(os.Getenv("ORRERY_AUDIO_OPENCLAW_NODE")),
			ScriptPath:  strings.TrimSpace(os.Getenv("ORRERY_AUDIO_OPENCLAW_SCRIPT")),
			PackagePath: strings.TrimSpace(os.Getenv("ORRERY_AUDIO_OPENCLAW_PACKAGE")),
			ConfigPath:  strings.TrimSpace(os.Getenv("ORRERY_AUDIO_OPENCLAW_CONFIG")),
		})
	case "openai-compatible":
		provider = cloudaudio.NewGateway(cloudaudio.Config{
			BaseURL:                strings.TrimSpace(os.Getenv("ORRERY_AUDIO_GATEWAY_BASE_URL")),
			TranscriptionProfileID: strings.TrimSpace(os.Getenv("ORRERY_AUDIO_TRANSCRIPTION_PROFILE_ID")),
			SpeechProfileID:        strings.TrimSpace(os.Getenv("ORRERY_AUDIO_SPEECH_PROFILE_ID")),
			Voices:                 strings.FieldsFunc(os.Getenv("ORRERY_AUDIO_VOICES"), func(r rune) bool { return r == ',' }),
			Profiles:               profiles, Secrets: secrets,
		})
	default:
		return nil
	}
	return provider
}

func gatewayVoiceInput(input gwprotocol.SubmitTurnInput) bool {
	if strings.EqualFold(strings.TrimSpace(input.InputModality), "voice") || strings.TrimSpace(input.TranscriptSource) != "" {
		return true
	}
	for _, attachment := range input.Attachments {
		if strings.EqualFold(strings.TrimSpace(attachment.Type), "audio") {
			return true
		}
	}
	return false
}

func (r *productionMessageGatewayRunner) prepareGatewayVoice(ctx context.Context, viewer discussionsession.ViewerRef, input gwprotocol.SubmitTurnInput, personalAssistant bool) (context.Context, gwprotocol.SubmitTurnInput) {
	if !gatewayVoiceInput(input) {
		return ctx, input
	}
	// Audio remains private until other conversation kinds have an explicit policy.
	if !personalAssistant {
		return gatewayVoiceFailureInput(ctx, input, cloudaudio.ErrNotConfigured)
	}
	prepared, err := r.audio.prepare(ctx, viewer, input)
	if err != nil {
		return gatewayVoiceFailureInput(ctx, input, err)
	}
	if _, decision := productionPersonalAssistantApprovalReplyDecision(prepared.Message); decision {
		prepared.Message = gatewayVoiceContent(prepared)
		return discussionsession.WithOperationalFailure(ctx, "A voice transcript contains an approval decision. No approval was executed. Speech recognition may be inaccurate. A separate text confirmation or action in the approval center is required."), withoutGatewayAudio(prepared)
	}
	prepared.Message = gatewayVoiceContent(prepared)
	return ctx, prepared
}

// Called only after gateway binding, tenant and viewer authorization have succeeded.
func (s *productionGatewayAudioService) prepare(ctx context.Context, viewer discussionsession.ViewerRef, input gwprotocol.SubmitTurnInput) (gwprotocol.SubmitTurnInput, error) {
	input.InputModality = "voice"
	input.Attachments = append([]gwprotocol.Attachment(nil), input.Attachments...)
	count := 0
	for _, attachment := range input.Attachments {
		if strings.EqualFold(attachment.Type, "audio") {
			count++
		}
	}
	if count > 1 {
		return input, cloudaudio.ErrInvalidAudio
	}
	for index, attachment := range input.Attachments {
		if !strings.EqualFold(attachment.Type, "audio") {
			continue
		}
		if s == nil || s.transcriber == nil {
			return input, cloudaudio.ErrNotConfigured
		}
		encoding, _ := attachment.Metadata["content_encoding"].(string)
		if encoding != "base64" || len(attachment.Content) > base64.StdEncoding.EncodedLen(cloudaudio.MaxAudioBytes) {
			return input, cloudaudio.ErrInvalidAudio
		}
		data, err := base64.StdEncoding.Strict().DecodeString(attachment.Content)
		if err != nil || len(data) == 0 || len(data) > cloudaudio.MaxAudioBytes || attachment.Bytes > 0 && attachment.Bytes != int64(len(data)) {
			return input, cloudaudio.ErrInvalidAudio
		}
		mediaType := attachment.MIMEType
		if cloudaudio.IsSILK(data) {
			if s.decoder == nil {
				return input, cloudaudio.ErrNotConfigured
			}
			data, err = s.decoder.Decode(ctx, data)
			if err != nil {
				return input, err
			}
			mediaType = "audio/wav"
		}
		extension, err := cloudaudio.ValidateAudio(data, mediaType)
		if err != nil {
			return input, err
		}
		transcript, err := s.transcriber.Transcribe(ctx, cloudaudio.TranscriptionRequest{
			Scope: cloudaudio.Scope{TenantID: viewer.TenantID, OwnerID: viewer.ViewerID, SourceID: input.RunID},
			Data:  data, MIMEType: mediaType,
		})
		if err != nil {
			return input, err
		}
		if strings.TrimSpace(transcript.Text) == "" {
			return input, cloudaudio.ErrEmptyTranscript
		}
		if strings.TrimSpace(input.Message) == "" {
			input.Message = transcript.Text
		} else {
			input.Message += "\n" + transcript.Text
		}
		input.TranscriptSource = transcript.Source
		if input.TranscriptSource != "model_gateway" && input.TranscriptSource != "openclaw" {
			input.TranscriptSource = "speech_service"
		}
		input.Attachments[index] = gwprotocol.Attachment{
			Type: "audio", Name: "voice" + extension, MIMEType: mediaType, Bytes: int64(len(data)),
			Content: base64.StdEncoding.EncodeToString(data), Metadata: map[string]any{"content_encoding": "base64"},
		}
	}
	if strings.TrimSpace(input.Message) == "" {
		return input, cloudaudio.ErrEmptyTranscript
	}
	if len(input.Message) > 64<<10 {
		return input, cloudaudio.ErrInvalidAudio
	}
	return input, nil
}

func gatewayVoiceFailureInput(ctx context.Context, input gwprotocol.SubmitTurnInput, err error) (context.Context, gwprotocol.SubmitTurnInput) {
	facts := "Voice transcription failed. The content of the recording is unavailable; no approval or requested action was executed."
	switch {
	case errors.Is(err, cloudaudio.ErrNotConfigured):
		facts += " Speech recognition is not configured for this conversation."
	case errors.Is(err, cloudaudio.ErrInvalidAudio):
		facts += " The audio format or data could not be accepted."
	case errors.Is(err, cloudaudio.ErrEmptyTranscript):
		facts += " No intelligible transcript was returned."
	}
	if strings.TrimSpace(input.Message) == "" {
		input.Message = "[语音消息]"
	}
	return discussionsession.WithOperationalFailure(ctx, facts), withoutGatewayAudio(input)
}

func withoutGatewayAudio(input gwprotocol.SubmitTurnInput) gwprotocol.SubmitTurnInput {
	attachments := make([]gwprotocol.Attachment, 0, len(input.Attachments))
	for _, attachment := range input.Attachments {
		if !strings.EqualFold(attachment.Type, "audio") {
			attachments = append(attachments, attachment)
		}
	}
	input.Attachments = attachments
	return input
}

func gatewayVoiceContent(input gwprotocol.SubmitTurnInput) string {
	source := "语音转写"
	if input.TranscriptSource == "platform" {
		source = "平台语音转写"
	}
	if input.TranscriptSource == "model_gateway" || input.TranscriptSource == "openclaw" || input.TranscriptSource == "speech_service" {
		source = "语音转写（云端识别）"
	}
	return fmt.Sprintf("[%s]\n%s", source, strings.TrimSpace(input.Message))
}
