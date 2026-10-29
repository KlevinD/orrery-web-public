package email

import (
	"context"
	"testing"

	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	gwcore "orrery_v2/internal/gateway/core"
	"orrery_v2/internal/gateway/protocol"
)

type fakeRunner struct {
	inputs []protocol.SubmitTurnInput
}

func (f *fakeRunner) RunTurn(ctx context.Context, input protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	f.inputs = append(f.inputs, input)
	ch := make(chan protocol.Event, 2)
	go func() {
		defer close(ch)
		ch <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "email reply"}
		ch <- protocol.Event{Type: protocol.EventTurnCompleted, FinishReason: "completed"}
	}()
	return ch, nil
}

type fakeClient struct {
	replies []OutboundEmail
}

func (f *fakeClient) SendReply(ctx context.Context, message OutboundEmail) error {
	f.replies = append(f.replies, message)
	return nil
}

func TestOptionsFromConfigMapsEmailFields(t *testing.T) {
	opts := OptionsFromConfig(gwconfig.PlatformAdapterConfig{APIKey: "smtp-user", Token: "pw", Extra: map[string]any{
		"from":     "bot@example.test",
		"imapHost": "imap.example.test",
		"smtpHost": "smtp.example.test",
		"imapPort": 993,
		"smtpPort": 465,
	}})

	if opts.Address != "bot@example.test" || opts.Username != "smtp-user" || opts.Password != "pw" || opts.IMAPHost != "imap.example.test" || opts.SMTPHost != "smtp.example.test" || opts.IMAPPort != 993 || opts.SMTPPort != 465 || opts.TLSMode != TLSModeImplicit {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestBuildSubmitInputMapsThreadAndAnswer(t *testing.T) {
	input := BuildSubmitInput(InboundEmail{MessageID: "m1", ThreadID: "t1", From: "user@example.test", Subject: "Hello", Body: "hi"})
	answer := BuildSubmitInput(InboundEmail{MessageID: "m2", ThreadID: "t1", From: "user@example.test", Body: "/answer req-1 yes"})

	if input.ChannelKey != protocol.ChannelEmail || input.SessionKey != "email:t1" || input.Message != "Subject: Hello\n\nhi" {
		t.Fatalf("input = %+v", input)
	}
	if answer.Telemetry["elicitation_request_id"] != "req-1" || answer.Message != "yes" {
		t.Fatalf("answer = %+v", answer)
	}
}

func TestHandleInboundDedupesAndSendsReply(t *testing.T) {
	runner := &fakeRunner{}
	client := &fakeClient{}
	adapter := NewAdapter(gwconfig.PlatformAdapterConfig{}, client)
	handle, err := adapter.Start(context.Background(), channel.StartDeps{Gateway: gwcore.NewGateway(runner)})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	emailHandle := handle.(*Handle)
	msg := InboundEmail{MessageID: "m1", ThreadID: "t1", From: "user@example.test", Subject: "Hello", Body: "hi"}

	if err := emailHandle.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("HandleInbound failed: %v", err)
	}
	if err := emailHandle.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("duplicate HandleInbound failed: %v", err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0].ChannelKey != protocol.ChannelEmail {
		t.Fatalf("inputs = %+v", runner.inputs)
	}
	if len(client.replies) != 1 || client.replies[0].To != "user@example.test" || client.replies[0].InReplyTo != "m1" || client.replies[0].Body != "email reply" {
		t.Fatalf("replies = %+v", client.replies)
	}
}
