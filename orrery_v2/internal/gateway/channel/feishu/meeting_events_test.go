package feishu

import (
	"context"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"

	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	gwcore "orrery_v2/internal/gateway/core"
)

type fakeMeetingAutomation struct {
	events []MeetingEvent
}

func (a *fakeMeetingAutomation) HandleProviderEvent(_ context.Context, event MeetingEvent) (MeetingSession, bool, error) {
	a.events = append(a.events, event)
	return MeetingSession{MeetingID: event.MeetingID}, true, nil
}

func TestMeetingEventDispatcherNormalizesRecordingReadyWithoutPersistingURL(t *testing.T) {
	const token = "testminute00000000000000"
	eventDispatcher := dispatcher.NewEventDispatcher("", "")
	var got InboundEvent
	registerMeetingEventHandlers(eventDispatcher, Options{AppID: "app-a", DomainName: "feishu"}, func(_ context.Context, event InboundEvent) error {
		got = event
		return nil
	})
	payload := []byte(`{
		"schema":"2.0",
		"header":{"event_id":"evt-ready","event_type":"vc.meeting.recording_ready_v1","create_time":"1785056400000","app_id":"app-a","tenant_key":"tenant-key"},
		"event":{"meeting":{"id":"meeting-1","meeting_no":"123456789","topic":"weekly"},"url":"https://acme.feishu.cn/minutes/` + token + `","duration":"3600000"}
	}`)
	if _, err := eventDispatcher.Do(context.Background(), payload); err != nil {
		t.Fatalf("dispatch recording_ready: %v", err)
	}
	if got.Kind != InboundEventMeeting || got.EventID != "evt-ready" || got.Meeting == nil {
		t.Fatalf("inbound event = %+v", got)
	}
	if got.Meeting.MinuteToken != token || got.Meeting.MeetingID != "meeting-1" || got.Meeting.MeetingNo != "123456789" || got.Meeting.RecordingDuration != time.Hour {
		t.Fatalf("provider event = %+v", got.Meeting)
	}
}

func TestFeishuAdapterDurablyDeduplicatesMeetingEvents(t *testing.T) {
	runner := &fakeRunner{}
	client := &fakeClient{}
	meetings := &fakeMeetingAutomation{}
	adapter := NewAdapterWithMeetingAutomation(
		gwconfig.FeishuAdapterConfig{Enabled: true, AppID: "app", DefaultSessionLabel: "general"},
		client,
		meetings,
	)
	handle, err := adapter.Start(context.Background(), channel.StartDeps{
		Gateway: gwcore.NewGateway(runner), InboundEvents: newFeishuInboundStore(t),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = handle.Stop(ctx, "test cleanup")
	})
	feishuHandle := handle.(*Handle)
	event := InboundEvent{
		Kind: InboundEventMeeting, EventID: "evt-started", AccountID: "feishu:app", TenantID: "tenant-key",
		Meeting: &MeetingEvent{
			Provider: ProviderFeishu, AccountID: "feishu:app", ProviderTenantKey: "tenant-key",
			EventID: "evt-started", Type: ProviderEventStarted, MeetingID: "meeting-1",
		},
	}
	if err := feishuHandle.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if err := feishuHandle.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent duplicate: %v", err)
	}
	if len(meetings.events) != 1 || len(runner.inputs) != 0 || len(client.replies) != 0 {
		t.Fatalf("meeting events=%+v gateway inputs=%+v replies=%+v", meetings.events, runner.inputs, client.replies)
	}
}
