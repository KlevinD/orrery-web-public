package feishu

import larkregistration "github.com/larksuite/oapi-sdk-go/v3/scene/registration"

const (
	RegistrationMeetingReserveScope        = "vc:reserve"
	RegistrationMeetingReserveReadScope    = "vc:reserve:readonly"
	RegistrationMeetingReadScope           = "vc:meeting:readonly"
	RegistrationMeetingRecordingReadScope  = "vc:record:readonly"
	RegistrationMinuteMediaExportScope     = "minutes:minutes.media:export"
	RegistrationMeetingStartedEvent        = "vc.meeting.meeting_started_v1"
	RegistrationMeetingEndedEvent          = "vc.meeting.meeting_ended_v1"
	RegistrationMeetingRecordingReadyEvent = "vc.meeting.recording_ready_v1"
)

// RegistrationOptionsWithMeetingAutomation is intentionally separate from
// the default Gateway registration so ordinary chat bots do not receive VC or
// recording export permissions.
func RegistrationOptionsWithMeetingAutomation() *larkregistration.Options {
	options := RegistrationOptions()
	options.Addons.Scopes.Tenant = append(options.Addons.Scopes.Tenant,
		RegistrationMeetingReserveScope,
		RegistrationMeetingReserveReadScope,
		RegistrationMeetingReadScope,
		RegistrationMeetingRecordingReadScope,
		RegistrationMinuteMediaExportScope,
	)
	options.Addons.Events.Items.Tenant = append(options.Addons.Events.Items.Tenant,
		RegistrationMeetingStartedEvent,
		RegistrationMeetingEndedEvent,
		RegistrationMeetingRecordingReadyEvent,
	)
	return options
}
