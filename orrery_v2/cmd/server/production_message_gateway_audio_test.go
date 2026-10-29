package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	cloudaudio "orrery_v2/internal/agent/runtime/audio"
	"orrery_v2/internal/app/discussionsession"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

type recordingGatewayTranscriber struct {
	calls  int
	input  cloudaudio.TranscriptionRequest
	text   string
	source string
	err    error
}

func TestProductionSpeechProviderSelectsExplicitLocalBackendWithoutChatProfiles(t *testing.T) {
	t.Setenv("ORRERY_AUDIO_PROTOCOL", "local-sherpa")
	if _, ok := newProductionSpeechProvider(nil, nil).(*cloudaudio.Local); !ok {
		t.Fatal("explicit offline speech backend was not selected")
	}
}

func (f *recordingGatewayTranscriber) Transcribe(_ context.Context, input cloudaudio.TranscriptionRequest) (cloudaudio.Transcript, error) {
	f.calls++
	f.input = input
	source := f.source
	if source == "" {
		source = "model_gateway"
	}
	return cloudaudio.Transcript{Text: f.text, Source: source}, f.err
}

func voiceTestInput() gwprotocol.SubmitTurnInput {
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin, InputModality: "voice",
		RunID: "voice-event-1", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	input.Telemetry["weixin_message_id"] = "message-voice-1"
	return input
}

func voiceTestAttachment() gwprotocol.Attachment {
	data := append([]byte("RIFF\x28\x00\x00\x00WAVEfmt "), make([]byte, 32)...)
	return gwprotocol.Attachment{Type: "audio", MIMEType: "audio/wav", Bytes: int64(len(data)),
		Content: base64.StdEncoding.EncodeToString(data), Metadata: map[string]any{"content_encoding": "base64"}}
}

func TestProductionGatewayVoiceApprovalRequiresTypedConfirmation(t *testing.T) {
	for _, source := range []string{"platform", "model_gateway", "openclaw"} {
		for _, decision := range []string{"同意", "拒绝！"} {
			t.Run(source+decision, func(t *testing.T) {
				discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{
					{Kind: discussionsession.StreamEventMessageCompleted, Message: &discussionsession.MessageDTO{Role: "assistant", Content: "请另行用文字确认，这次还没有执行审批。"}},
					{Kind: discussionsession.StreamEventRunCompleted},
				}}
				handler := &recordingPersonalAssistantApprovalReplyHandler{}
				runner := personalAssistantApprovalReplyTestRunner(t, discussions, handler)
				transcriber := &recordingGatewayTranscriber{text: decision, source: source}
				runner.audio = &productionGatewayAudioService{transcriber: transcriber}
				input := voiceTestInput()
				if source == "platform" {
					input.Message = decision
					input.TranscriptSource = source
				} else {
					input.Attachments = []gwprotocol.Attachment{voiceTestAttachment()}
				}
				events, err := runner.RunTurn(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				got := collectGatewayEvents(events)
				if len(got) != 2 || got[0].Text != "请另行用文字确认，这次还没有执行审批。" || !strings.Contains(discussions.operationalFailureFacts, "No approval was executed") {
					t.Fatalf("events=%+v", got)
				}
				if handler.input.Decision != "" || len(discussions.cmds) != 1 {
					t.Fatal("voice executed an approval or bypassed Agent explanation")
				}
				if source != "platform" && (transcriber.input.Scope.TenantID != "tenant-a" || transcriber.input.Scope.OwnerID != "human-alice") {
					t.Fatalf("audio scope=%+v", transcriber.input.Scope)
				}
				if source == "platform" && transcriber.calls != 0 {
					t.Fatal("platform text was retranscribed")
				}
			})
		}
	}
}

func TestProductionGatewayVoicePlatformTranscriptKeepsContextWithoutConfiguredModel(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, &recordingPersonalAssistantApprovalReplyHandler{})
	input := voiceTestInput()
	input.Message, input.TranscriptSource = "同意后会发生什么", "platform"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	collectGatewayEvents(events)
	if len(discussions.cmds) != 1 || discussions.cmd.Content != "[平台语音转写]\n同意后会发生什么" || discussions.cmd.ConversationKind != discussionsession.ConversationKindPersonalAssistant {
		t.Fatalf("cmd=%+v", discussions.cmd)
	}
}

func TestProductionGatewayVoiceNeverTranscribesAnUnboundSender(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, &recordingPersonalAssistantApprovalReplyHandler{})
	transcriber := &recordingGatewayTranscriber{text: "private"}
	runner.audio = &productionGatewayAudioService{transcriber: transcriber}
	input := voiceTestInput()
	input.Attachments = []gwprotocol.Attachment{voiceTestAttachment()}
	input.Telemetry["weixin_sender_id"] = "someone-else"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	collectGatewayEvents(events)
	if transcriber.calls != 0 || len(discussions.cmds) != 0 {
		t.Fatal("unbound sender reached cloud audio or conversation")
	}
}

func TestProductionGatewayVoiceFailureIsExplainedByAgent(t *testing.T) {
	for _, missingMedia := range []bool{true, false} {
		discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{
			{Kind: discussionsession.StreamEventMessageCompleted, Message: &discussionsession.MessageDTO{Role: "assistant", Content: "我没有取得语音内容，可以改发文字。"}},
			{Kind: discussionsession.StreamEventRunCompleted},
		}}
		runner := personalAssistantApprovalReplyTestRunner(t, discussions, &recordingPersonalAssistantApprovalReplyHandler{})
		input := voiceTestInput()
		if !missingMedia {
			input.Attachments = []gwprotocol.Attachment{voiceTestAttachment()}
		}
		events, err := runner.RunTurn(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		got := collectGatewayEvents(events)
		if len(got) != 2 || got[0].Text != "我没有取得语音内容，可以改发文字。" || len(discussions.cmds) != 1 || discussions.operationalFailureFacts == "" || len(discussions.cmd.AttachmentIDs) != 0 {
			t.Fatalf("events=%+v", got)
		}
	}
}

type recordingSILKDecoder struct{ calls int }

func (d *recordingSILKDecoder) Decode(context.Context, []byte) ([]byte, error) {
	d.calls++
	return base64.StdEncoding.DecodeString(voiceTestAttachment().Content)
}

func TestProductionGatewayVoiceConvertsBeforeCloudAndPersistsPrivateAttachment(t *testing.T) {
	decoder := &recordingSILKDecoder{}
	transcriber := &recordingGatewayTranscriber{text: "解释一下这项审批"}
	audio := &productionGatewayAudioService{transcriber: transcriber, decoder: decoder}
	input := voiceTestInput()
	input.Attachments = []gwprotocol.Attachment{{Type: "audio", MIMEType: "audio/silk", Content: base64.StdEncoding.EncodeToString([]byte("\x02#!SILK_V3 input")), Metadata: map[string]any{"content_encoding": "base64"}}}
	viewer := discussionsession.ViewerRef{TenantID: "tenant-a", ViewerID: "human-alice"}
	prepared, err := audio.prepare(context.Background(), viewer, input)
	if err != nil {
		t.Fatal(err)
	}
	if decoder.calls != 1 || transcriber.calls != 1 || transcriber.input.MIMEType != "audio/wav" || prepared.TranscriptSource != "model_gateway" || prepared.Attachments[0].Name != "voice.wav" {
		t.Fatalf("prepared=%+v", prepared)
	}
	service := &fakeProductionGatewayAttachmentService{}
	runner := newProductionMessageGatewayRunner(service)
	_, err = runner.prepareGatewayAttachments(context.Background(), viewer, discussionsession.SendMessageCommand{
		ConversationKind: discussionsession.ConversationKindPersonalAssistant, IdempotencyKey: input.RunID,
	}, prepared.Attachments)
	if err != nil {
		t.Fatal(err)
	}
	if service.draftCommand.ConversationKind != discussionsession.ConversationKindPersonalAssistant || service.draftCommand.ContentType != "audio/wav" {
		t.Fatalf("draft=%+v", service.draftCommand)
	}
	if input.Attachments[0].MIMEType != "audio/silk" {
		t.Fatal("prepare mutated the queued input")
	}
}

func TestProductionGatewayAudioDisabledUnlessProtocolExplicit(t *testing.T) {
	t.Setenv("ORRERY_AUDIO_PROTOCOL", "")
	if newProductionGatewayAudioService(nil, nil) != nil {
		t.Fatal("audio unexpectedly enabled")
	}
	t.Setenv("ORRERY_AUDIO_PROTOCOL", "openai-compatible")
	service := newProductionGatewayAudioService(nil, nil)
	_, err := service.prepare(context.Background(), discussionsession.ViewerRef{TenantID: "tenant-a", ViewerID: "human-a"}, gwprotocol.SubmitTurnInput{RunID: "run-a", Attachments: []gwprotocol.Attachment{voiceTestAttachment()}})
	if !errors.Is(err, cloudaudio.ErrNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}

func TestProductionSpeechProviderSelectsOpenClawWithoutChatProfiles(t *testing.T) {
	t.Setenv("ORRERY_AUDIO_PROTOCOL", "openclaw")
	provider := newProductionSpeechProvider(nil, nil)
	if _, ok := provider.(*cloudaudio.OpenClaw); !ok {
		t.Fatalf("provider=%T", provider)
	}
	if newProductionGatewayAudioService(nil, nil) == nil {
		t.Fatal("gateway did not use native speech provider")
	}
	t.Setenv("ORRERY_AUDIO_PROTOCOL", "unknown")
	if newProductionSpeechProvider(nil, nil) != nil {
		t.Fatal("unknown provider silently fell back")
	}
}
