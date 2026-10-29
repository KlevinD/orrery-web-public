package feishu

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeMeetingRemote struct {
	reservation     MeetingReservation
	reservationCode string
	active          ActiveMeeting
	activeFound     bool
	activeCode      string
	recordingURL    string
	recordingCode   string
	mediaURL        string
	mediaCode       string
	lastRequest     MeetingReservationRequest
}

func (r *fakeMeetingRemote) ApplyReservation(_ context.Context, request MeetingReservationRequest) (MeetingReservation, string, error) {
	r.lastRequest = request
	return r.reservation, r.reservationCode, nil
}

func (r *fakeMeetingRemote) GetActiveMeeting(context.Context, string) (ActiveMeeting, bool, string, error) {
	return r.active, r.activeFound, r.activeCode, nil
}

func (r *fakeMeetingRemote) GetRecordingURL(context.Context, string) (string, string, error) {
	return r.recordingURL, r.recordingCode, nil
}

func (r *fakeMeetingRemote) GetMinuteMediaURL(context.Context, string) (string, string, error) {
	return r.mediaURL, r.mediaCode, nil
}

func TestMeetingAPICreatesAutoRecordedMultipartyReservation(t *testing.T) {
	remote := &fakeMeetingRemote{reservation: MeetingReservation{ID: "reserve-1", MeetingNo: "123456789", MeetingURL: "https://vc.feishu.cn/j/123"}, reservationCode: "0"}
	api := newMeetingAPI(Options{AppID: "app-a", DomainName: "feishu"}, remote)
	expiresAt := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	reservation, err := api.CreateReservation(context.Background(), MeetingReservationRequest{
		AccountID: "feishu:app-a", OwnerExternalID: "ou_owner", Topic: "weekly", ExpiresAt: expiresAt, AutoRecord: true,
	})
	if err != nil {
		t.Fatalf("CreateReservation: %v", err)
	}
	if reservation.ID != "reserve-1" || reservation.MeetingNo != "123456789" || !remote.lastRequest.AutoRecord || remote.lastRequest.OwnerExternalID != "ou_owner" {
		t.Fatalf("reservation = %+v request = %+v", reservation, remote.lastRequest)
	}
	if _, err := api.CreateReservation(context.Background(), MeetingReservationRequest{
		AccountID: "feishu:app-a", OwnerExternalID: "ou_owner", Topic: "weekly", ExpiresAt: expiresAt,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-recorded reservation error = %v", err)
	}
	if _, err := api.CreateReservation(context.Background(), MeetingReservationRequest{
		AccountID: "feishu:other", OwnerExternalID: "ou_owner", Topic: "weekly", ExpiresAt: expiresAt, AutoRecord: true,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-account reservation error = %v", err)
	}
}

func TestMeetingAPIClassifiesProviderErrorsWithoutResponseMessage(t *testing.T) {
	api := newMeetingAPI(Options{AppID: "app-a", DomainName: "feishu"}, &fakeMeetingRemote{reservationCode: "121005"})
	_, err := api.CreateReservation(context.Background(), MeetingReservationRequest{
		AccountID: "feishu:app-a", OwnerExternalID: "owner", Topic: "weekly", ExpiresAt: time.Now().Add(time.Hour), AutoRecord: true,
	})
	var providerErr *MeetingAPIError
	if !errors.As(err, &providerErr) || providerErr.ProviderErrorCode() != "feishu_121005" {
		t.Fatalf("provider error = %#v", err)
	}
}

func TestExtractMinuteTokenAllowsOnlyConfiguredOfficialDomain(t *testing.T) {
	const token = "testminute00000000000000"
	tests := []struct {
		name       string
		rawURL     string
		domainName string
		wantToken  string
		wantError  bool
	}{
		{name: "feishu tenant domain", rawURL: "https://acme.feishu.cn/minutes/" + token, domainName: "feishu", wantToken: token},
		{name: "lark tenant domain", rawURL: "https://acme.larksuite.com/minutes/" + token + "?from=vc", domainName: "larksuite", wantToken: token},
		{name: "wrong environment", rawURL: "https://acme.larksuite.com/minutes/" + token, domainName: "feishu", wantError: true},
		{name: "suffix attack", rawURL: "https://acme.feishu.cn.attacker.example/minutes/" + token, domainName: "feishu", wantError: true},
		{name: "encoded separator", rawURL: "https://acme.feishu.cn/minutes/abc%2Fdef", domainName: "feishu", wantError: true},
		{name: "userinfo", rawURL: "https://user@acme.feishu.cn/minutes/" + token, domainName: "feishu", wantError: true},
		{name: "insecure", rawURL: "http://acme.feishu.cn/minutes/" + token, domainName: "feishu", wantError: true},
		{name: "wrong token length", rawURL: "https://acme.feishu.cn/minutes/short", domainName: "feishu", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token, err := ExtractMinuteToken(test.rawURL, test.domainName)
			if test.wantError && err == nil {
				t.Fatalf("URL %q was accepted with token %q", test.rawURL, token)
			}
			if !test.wantError && (err != nil || token != test.wantToken) {
				t.Fatalf("token = %q err=%v, want %q", token, err, test.wantToken)
			}
		})
	}
}

func TestMeetingAPIResolvesRecordingAndEphemeralMediaURL(t *testing.T) {
	const token = "testminute00000000000000"
	remote := &fakeMeetingRemote{
		recordingURL: "https://acme.feishu.cn/minutes/" + token,
		mediaURL:     "https://download.feishu.cn/recording/file.mp4?signature=temporary",
	}
	api := newMeetingAPI(Options{AppID: "app-a", DomainName: "feishu"}, remote)
	gotToken, err := api.ResolveRecordingMinuteToken(context.Background(), "feishu:app-a", "meeting-1")
	if err != nil || gotToken != token {
		t.Fatalf("ResolveRecordingMinuteToken = %q, %v", gotToken, err)
	}
	downloadURL, err := api.ResolveMediaDownloadURL(context.Background(), "feishu:app-a", token)
	if err != nil || downloadURL != remote.mediaURL {
		t.Fatalf("ResolveMediaDownloadURL = %q, %v", downloadURL, err)
	}
	remote.mediaURL = "http://127.0.0.1/private"
	if _, err := api.ResolveMediaDownloadURL(context.Background(), "feishu:app-a", token); err == nil {
		t.Fatal("insecure media URL was accepted")
	}
}
