package feishu

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkminutes "github.com/larksuite/oapi-sdk-go/v3/service/minutes/v1"
	larkvc "github.com/larksuite/oapi-sdk-go/v3/service/vc/v1"

	"orrery_v2/internal/gateway/channel"
)

var minuteTokenPattern = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)

type MeetingAPI struct {
	options Options
	remote  meetingRemote
}

type meetingRemote interface {
	ApplyReservation(context.Context, MeetingReservationRequest) (MeetingReservation, string, error)
	GetActiveMeeting(context.Context, string) (ActiveMeeting, bool, string, error)
	GetRecordingURL(context.Context, string) (string, string, error)
	GetMinuteMediaURL(context.Context, string) (string, string, error)
}

type larkMeetingRemote struct {
	client *lark.Client
}

type MeetingAPIError struct {
	Code      string
	Operation string
}

func (e *MeetingAPIError) Error() string {
	return fmt.Sprintf("feishu meeting API %s failed with code %s", e.Operation, e.Code)
}

func (e *MeetingAPIError) ProviderErrorCode() string {
	return "feishu_" + strings.TrimSpace(e.Code)
}

func NewMeetingAPI(options Options, logger channel.Logger) *MeetingAPI {
	domain := lark.FeishuBaseUrl
	if isLarkDomain(options.DomainName) {
		domain = lark.LarkBaseUrl
	}
	client := lark.NewClient(
		options.AppID,
		options.AppSecret,
		lark.WithOpenBaseUrl(domain),
		lark.WithLogger(feishuSDKLogger{logger: logger}),
		lark.WithHttpClient(newFeishuHTTPClient()),
	)
	return &MeetingAPI{options: options, remote: &larkMeetingRemote{client: client}}
}

func newMeetingAPI(options Options, remote meetingRemote) *MeetingAPI {
	return &MeetingAPI{options: options, remote: remote}
}

func (a *MeetingAPI) ResolveProvider(provider, accountID string) (MeetingProvider, bool) {
	if a == nil || a.remote == nil || !strings.EqualFold(strings.TrimSpace(provider), ProviderFeishu) ||
		strings.TrimSpace(accountID) != a.options.AccountID() {
		return nil, false
	}
	return a, true
}

func (a *MeetingAPI) CreateReservation(ctx context.Context, request MeetingReservationRequest) (MeetingReservation, error) {
	if err := a.validateAccount(request.AccountID); err != nil {
		return MeetingReservation{}, err
	}
	if strings.TrimSpace(request.OwnerExternalID) == "" || strings.TrimSpace(request.Topic) == "" || request.ExpiresAt.IsZero() || !request.AutoRecord {
		return MeetingReservation{}, ErrInvalidRequest
	}
	reservation, code, err := a.remote.ApplyReservation(ctx, request)
	if err != nil {
		return MeetingReservation{}, err
	}
	if code != "" && code != "0" {
		return MeetingReservation{}, &MeetingAPIError{Code: code, Operation: "apply_reservation"}
	}
	reservation.ID = strings.TrimSpace(reservation.ID)
	reservation.MeetingNo = strings.TrimSpace(reservation.MeetingNo)
	reservation.MeetingURL = strings.TrimSpace(reservation.MeetingURL)
	if reservation.ID == "" || reservation.MeetingURL == "" {
		return MeetingReservation{}, errors.New("feishu reservation response is incomplete")
	}
	return reservation, nil
}

func (a *MeetingAPI) LookupActiveMeeting(ctx context.Context, accountID, reservationID string) (ActiveMeeting, bool, error) {
	if err := a.validateAccount(accountID); err != nil {
		return ActiveMeeting{}, false, err
	}
	reservationID = strings.TrimSpace(reservationID)
	if reservationID == "" {
		return ActiveMeeting{}, false, ErrInvalidRequest
	}
	meeting, found, code, err := a.remote.GetActiveMeeting(ctx, reservationID)
	if err != nil {
		return ActiveMeeting{}, false, err
	}
	if code != "" && code != "0" {
		return ActiveMeeting{}, false, &MeetingAPIError{Code: code, Operation: "get_active_meeting"}
	}
	if !found || strings.TrimSpace(meeting.ID) == "" {
		return ActiveMeeting{}, false, nil
	}
	meeting.ID = strings.TrimSpace(meeting.ID)
	meeting.Topic = strings.TrimSpace(meeting.Topic)
	return meeting, true, nil
}

func (a *MeetingAPI) ResolveRecordingMinuteToken(ctx context.Context, accountID, meetingID string) (string, error) {
	if err := a.validateAccount(accountID); err != nil {
		return "", err
	}
	meetingID = strings.TrimSpace(meetingID)
	if meetingID == "" {
		return "", ErrInvalidRequest
	}
	recordingURL, code, err := a.remote.GetRecordingURL(ctx, meetingID)
	if err != nil {
		return "", err
	}
	if code != "" && code != "0" {
		return "", &MeetingAPIError{Code: code, Operation: "get_recording"}
	}
	return ExtractMinuteToken(recordingURL, a.options.DomainName)
}

// ResolveMediaDownloadURL returns an ephemeral provider URL. Callers must not
// persist or log it, and must use a bounded downloader with SSRF protection.
func (a *MeetingAPI) ResolveMediaDownloadURL(ctx context.Context, accountID, minuteToken string) (string, error) {
	if err := a.validateAccount(accountID); err != nil {
		return "", err
	}
	minuteToken = strings.TrimSpace(minuteToken)
	if !minuteTokenPattern.MatchString(minuteToken) {
		return "", ErrInvalidRequest
	}
	downloadURL, code, err := a.remote.GetMinuteMediaURL(ctx, minuteToken)
	if err != nil {
		return "", err
	}
	if code != "" && code != "0" {
		return "", &MeetingAPIError{Code: code, Operation: "get_minute_media"}
	}
	parsed, err := url.Parse(strings.TrimSpace(downloadURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("feishu minute media response contains an invalid download URL")
	}
	return parsed.String(), nil
}

func (a *MeetingAPI) validateAccount(accountID string) error {
	if a == nil || a.remote == nil || a.options.AccountID() == "" {
		return ErrDependency
	}
	if strings.TrimSpace(accountID) != a.options.AccountID() {
		return ErrForbidden
	}
	return nil
}

func ExtractMinuteToken(rawURL, domainName string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("invalid Feishu recording URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.HasSuffix(host, ".") || (parsed.Port() != "" && parsed.Port() != "443") || !officialMeetingHost(host, domainName) {
		return "", errors.New("recording URL is outside the configured Feishu domain")
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	for index := 0; index+1 < len(segments); index++ {
		if segments[index] != "minutes" {
			continue
		}
		rawToken := segments[index+1]
		token, err := url.PathUnescape(rawToken)
		if err != nil || rawToken != token || !minuteTokenPattern.MatchString(token) {
			return "", errors.New("recording URL contains an invalid minute token")
		}
		return token, nil
	}
	return "", errors.New("recording URL does not contain a minute token")
}

func officialMeetingHost(host, domainName string) bool {
	suffix := ".feishu.cn"
	root := "feishu.cn"
	if isLarkDomain(domainName) {
		suffix = ".larksuite.com"
		root = "larksuite.com"
	}
	return host == root || strings.HasSuffix(host, suffix)
}

func isLarkDomain(domainName string) bool {
	return strings.EqualFold(strings.TrimSpace(domainName), "lark") || strings.EqualFold(strings.TrimSpace(domainName), "larksuite")
}

func (r *larkMeetingRemote) ApplyReservation(ctx context.Context, request MeetingReservationRequest) (MeetingReservation, string, error) {
	if r == nil || r.client == nil {
		return MeetingReservation{}, "", ErrDependency
	}
	settings := larkvc.NewReserveMeetingSettingBuilder().
		Topic(strings.TrimSpace(request.Topic)).
		MeetingInitialType(1).
		AutoRecord(true).
		Build()
	body := larkvc.NewApplyReserveReqBodyBuilder().
		EndTime(strconv.FormatInt(request.ExpiresAt.UTC().Unix(), 10)).
		OwnerId(strings.TrimSpace(request.OwnerExternalID)).
		MeetingSettings(settings).
		Build()
	req := larkvc.NewApplyReserveReqBuilder().UserIdType("open_id").Body(body).Build()
	resp, err := r.client.Vc.V1.Reserve.Apply(ctx, req)
	if err != nil {
		return MeetingReservation{}, "", err
	}
	if resp == nil {
		return MeetingReservation{}, "", errors.New("feishu reservation response is empty")
	}
	code := strconv.Itoa(resp.Code)
	if !resp.Success() || resp.Data == nil || resp.Data.Reserve == nil {
		return MeetingReservation{}, code, nil
	}
	return MeetingReservation{
		ID: stringValue(resp.Data.Reserve.Id), MeetingNo: stringValue(resp.Data.Reserve.MeetingNo), MeetingURL: stringValue(resp.Data.Reserve.Url),
	}, code, nil
}

func (r *larkMeetingRemote) GetActiveMeeting(ctx context.Context, reservationID string) (ActiveMeeting, bool, string, error) {
	if r == nil || r.client == nil {
		return ActiveMeeting{}, false, "", ErrDependency
	}
	req := larkvc.NewGetActiveMeetingReserveReqBuilder().
		ReserveId(strings.TrimSpace(reservationID)).
		WithParticipants(false).
		UserIdType("open_id").
		Build()
	resp, err := r.client.Vc.V1.Reserve.GetActiveMeeting(ctx, req)
	if err != nil {
		return ActiveMeeting{}, false, "", err
	}
	if resp == nil {
		return ActiveMeeting{}, false, "", errors.New("feishu active meeting response is empty")
	}
	code := strconv.Itoa(resp.Code)
	if !resp.Success() || resp.Data == nil || resp.Data.Meeting == nil {
		return ActiveMeeting{}, false, code, nil
	}
	meeting := resp.Data.Meeting
	return ActiveMeeting{
		ID: stringValue(meeting.Id), Topic: stringValue(meeting.Topic), StartedAt: unixSeconds(stringValue(meeting.StartTime)),
	}, true, code, nil
}

func (r *larkMeetingRemote) GetRecordingURL(ctx context.Context, meetingID string) (string, string, error) {
	if r == nil || r.client == nil {
		return "", "", ErrDependency
	}
	req := larkvc.NewGetMeetingRecordingReqBuilder().MeetingId(strings.TrimSpace(meetingID)).Build()
	resp, err := r.client.Vc.V1.MeetingRecording.Get(ctx, req)
	if err != nil {
		return "", "", err
	}
	if resp == nil {
		return "", "", errors.New("feishu meeting recording response is empty")
	}
	code := strconv.Itoa(resp.Code)
	if !resp.Success() || resp.Data == nil || resp.Data.Recording == nil {
		return "", code, nil
	}
	return stringValue(resp.Data.Recording.Url), code, nil
}

func (r *larkMeetingRemote) GetMinuteMediaURL(ctx context.Context, minuteToken string) (string, string, error) {
	if r == nil || r.client == nil {
		return "", "", ErrDependency
	}
	req := larkminutes.NewGetMinuteMediaReqBuilder().MinuteToken(strings.TrimSpace(minuteToken)).Build()
	resp, err := r.client.Minutes.V1.MinuteMedia.Get(ctx, req)
	if err != nil {
		return "", "", err
	}
	if resp == nil {
		return "", "", errors.New("feishu minute media response is empty")
	}
	code := strconv.Itoa(resp.Code)
	if !resp.Success() || resp.Data == nil {
		return "", code, nil
	}
	return stringValue(resp.Data.DownloadUrl), code, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func unixSeconds(value string) time.Time {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
