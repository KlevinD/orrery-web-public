package feishu

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkvc "github.com/larksuite/oapi-sdk-go/v3/service/vc/v1"
)

func registerMeetingEventHandlers(
	eventDispatcher *dispatcher.EventDispatcher,
	options Options,
	inbound func(context.Context, InboundEvent) error,
) {
	eventDispatcher.OnP2MeetingStartedV1(func(ctx context.Context, event *larkvc.P2MeetingStartedV1) error {
		if event == nil || event.Event == nil {
			return errors.New("feishu meeting started event is empty")
		}
		return enqueueMeetingEvent(ctx, options, inbound, event.EventV2Base, event.Event.Meeting, MeetingEvent{
			Type:       ProviderEventStarted,
			OccurredAt: meetingEventTime(event.Event.Meeting, ProviderEventStarted),
		})
	})
	eventDispatcher.OnP2MeetingEndedV1(func(ctx context.Context, event *larkvc.P2MeetingEndedV1) error {
		if event == nil || event.Event == nil {
			return errors.New("feishu meeting ended event is empty")
		}
		return enqueueMeetingEvent(ctx, options, inbound, event.EventV2Base, event.Event.Meeting, MeetingEvent{
			Type:       ProviderEventEnded,
			OccurredAt: meetingEventTime(event.Event.Meeting, ProviderEventEnded),
		})
	})
	eventDispatcher.OnP2MeetingRecordingReadyV1(func(ctx context.Context, event *larkvc.P2MeetingRecordingReadyV1) error {
		if event == nil || event.Event == nil {
			return errors.New("feishu recording ready event is empty")
		}
		minuteToken, err := ExtractMinuteToken(stringValue(event.Event.Url), options.DomainName)
		if err != nil {
			return err
		}
		duration, err := millisecondsDuration(stringValue(event.Event.Duration))
		if err != nil {
			return err
		}
		return enqueueMeetingEvent(ctx, options, inbound, event.EventV2Base, event.Event.Meeting, MeetingEvent{
			Type: ProviderEventRecordingReady, MinuteToken: minuteToken,
			RecordingDuration: duration,
		})
	})
}

func enqueueMeetingEvent(
	ctx context.Context,
	options Options,
	inbound func(context.Context, InboundEvent) error,
	base *larkevent.EventV2Base,
	meeting *larkvc.MeetingEventMeeting,
	event MeetingEvent,
) error {
	if inbound == nil || base == nil || base.Header == nil || meeting == nil {
		return errors.New("feishu meeting event is incomplete")
	}
	header := base.Header
	if strings.TrimSpace(header.EventID) == "" || strings.TrimSpace(header.TenantKey) == "" || strings.TrimSpace(header.AppID) != strings.TrimSpace(options.AppID) {
		return errors.New("feishu meeting event identity is invalid")
	}
	meetingID := stringValue(meeting.Id)
	if meetingID == "" {
		return errors.New("feishu meeting event has no meeting id")
	}
	event.Provider = ProviderFeishu
	event.AccountID = options.AccountID()
	event.ProviderTenantKey = strings.TrimSpace(header.TenantKey)
	event.EventID = strings.TrimSpace(header.EventID)
	event.MeetingID = meetingID
	event.MeetingNo = stringValue(meeting.MeetingNo)
	event.Topic = stringValue(meeting.Topic)
	if event.OccurredAt.IsZero() {
		event.OccurredAt = millisecondsTime(header.CreateTime)
	}
	return inbound(ctx, InboundEvent{
		Kind: InboundEventMeeting, EventID: event.EventID, AccountID: event.AccountID,
		TenantID: event.ProviderTenantKey, Meeting: &event,
	})
}

func meetingEventTime(meeting *larkvc.MeetingEventMeeting, eventType MeetingEventType) time.Time {
	if meeting == nil {
		return time.Time{}
	}
	if eventType == ProviderEventEnded {
		return unixSeconds(stringValue(meeting.EndTime))
	}
	return unixSeconds(stringValue(meeting.StartTime))
}

func millisecondsTime(value string) time.Time {
	milliseconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || milliseconds <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(milliseconds).UTC()
}

func millisecondsDuration(value string) (time.Duration, error) {
	milliseconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || milliseconds < 0 {
		return 0, errors.New("feishu recording duration is invalid")
	}
	if milliseconds > int64((24*time.Hour)/time.Millisecond) {
		return 0, errors.New("feishu recording duration exceeds the supported limit")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func validateMeetingInboundIdentity(event InboundEvent) error {
	if event.Kind != InboundEventMeeting {
		return nil
	}
	if event.Meeting == nil || event.Meeting.Provider != ProviderFeishu ||
		strings.TrimSpace(event.Meeting.EventID) != strings.TrimSpace(event.EventID) ||
		strings.TrimSpace(event.Meeting.AccountID) != strings.TrimSpace(event.AccountID) ||
		strings.TrimSpace(event.Meeting.ProviderTenantKey) != strings.TrimSpace(event.TenantID) {
		return errors.New("feishu meeting event identity mismatch")
	}
	return nil
}
