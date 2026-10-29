package feishu

import (
	"reflect"
	"testing"
)

func TestMeetingRegistrationOptionsAddOnlyManagedMeetingCapabilities(t *testing.T) {
	options := RegistrationOptionsWithMeetingAutomation()
	wantScopes := []string{
		RegistrationMessageSendScope,
		RegistrationMeetingReserveScope,
		RegistrationMeetingReserveReadScope,
		RegistrationMeetingReadScope,
		RegistrationMeetingRecordingReadScope,
		RegistrationMinuteMediaExportScope,
	}
	if got := options.Addons.Scopes.Tenant; !reflect.DeepEqual(got, wantScopes) {
		t.Fatalf("tenant scopes = %v, want %v", got, wantScopes)
	}
	wantEvents := []string{
		RegistrationMessageEvent,
		RegistrationMeetingStartedEvent,
		RegistrationMeetingEndedEvent,
		RegistrationMeetingRecordingReadyEvent,
	}
	if got := options.Addons.Events.Items.Tenant; !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("tenant events = %v, want %v", got, wantEvents)
	}
	for _, forbidden := range []string{"vc:record", "vc:meeting.all_meeting:readonly", "minutes:minute:download"} {
		for _, scope := range options.Addons.Scopes.Tenant {
			if scope == forbidden {
				t.Fatalf("meeting registration requested forbidden scope %q", scope)
			}
		}
	}
}
