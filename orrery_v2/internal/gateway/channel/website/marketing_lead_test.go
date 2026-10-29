package website

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingMarketingLeadCapture struct {
	mu      sync.Mutex
	inputs  []MarketingLeadCaptureInput
	err     error
	receipt MarketingLeadReceipt
}

func (c *recordingMarketingLeadCapture) CaptureWebsiteLead(_ context.Context, input MarketingLeadCaptureInput) (MarketingLeadReceipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs = append(c.inputs, input)
	if c.err != nil {
		return MarketingLeadReceipt{}, c.err
	}
	return c.receipt, nil
}

func (c *recordingMarketingLeadCapture) snapshot() []MarketingLeadCaptureInput {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]MarketingLeadCaptureInput(nil), c.inputs...)
}

func TestWebsiteLeadSubmissionUsesFixedCaptureWithoutCallingAgent(t *testing.T) {
	gateway := &fakeGateway{}
	gapStore := &recordingWebsiteGapStore{}
	capture := &recordingMarketingLeadCapture{receipt: MarketingLeadReceipt{
		LeadID: "lead_123", Status: "new", Source: "minvia_website_advisor", CreatedAt: time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC),
	}}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}, MarketingLeads: capture, GapStore: gapStore})
	session := createSession(t, handler, "https://www.minvia.cn")
	answer := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"我想预约演示","language":"zh-CN"}`)
	if answer.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", answer.Code, answer.Body.String())
	}

	rec := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/leads", "", session.AccessToken, `{
		"submission_id":"lead-submission-1",
		"contact_name":" Qu Zheng ",
		"email":"user@example.com",
		"enquiry":"Private deployment consultation",
		"locale":"zh-CN",
		"source_page_path":"/zh-CN/orrery",
		"consent":true
	}`)

	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "lead_123") || !strings.Contains(rec.Body.String(), `"status":"new"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if gateway.inputCount() != 0 {
		t.Fatalf("agent gateway calls = %d", gateway.inputCount())
	}
	inputs := capture.snapshot()
	if len(inputs) != 1 || inputs[0].IdempotencyKey != "lead-submission-1" || inputs[0].ContactName != "Qu Zheng" || inputs[0].Source != "minvia_website_advisor" || inputs[0].ConsentVersion != "website-lead-v1" || inputs[0].ConsentAt.IsZero() {
		t.Fatalf("capture input = %+v", inputs)
	}
	events := gapStore.snapshot()
	if len(events) != 2 || events[1].Route != "marketing_lead" || !events[1].Takeover || events[1].MatchedIntent != "demo.zh-CN" {
		t.Fatalf("gap events = %+v", events)
	}
}

func TestWebsiteLeadSubmissionValidatesConsentContactAndClosedFields(t *testing.T) {
	capture := &recordingMarketingLeadCapture{}
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}, MarketingLeads: capture})
	session := createSession(t, handler, "https://www.minvia.cn")
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "consent", body: `{"submission_id":"lead-invalid-1","email":"user@example.com","enquiry":"Demo","consent":false}`},
		{name: "contact", body: `{"submission_id":"lead-invalid-2","enquiry":"Demo","consent":true}`},
		{name: "unknown", body: `{"submission_id":"lead-invalid-3","email":"user@example.com","enquiry":"Demo","consent":true,"tenant_id":"tenant-secret"}`},
		{name: "email", body: `{"submission_id":"lead-invalid-4","email":"not-email","enquiry":"Demo","consent":true}`, wantCode: "advisor_lead_email_invalid"},
		{name: "bounded", body: `{"submission_id":"lead-invalid-5","email":"user@example.com","enquiry":"` + strings.Repeat("x", 4001) + `","consent":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/leads", "", session.AccessToken, test.body)
			wantCode := test.wantCode
			if wantCode == "" {
				wantCode = "advisor_lead_invalid"
			}
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), wantCode) {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
	if len(capture.snapshot()) != 0 {
		t.Fatalf("invalid requests reached Marketing: %+v", capture.snapshot())
	}
}

func TestWebsiteLeadSubmissionRejectsEmailWithoutDomainSuffixBeforeMarketing(t *testing.T) {
	capture := &recordingMarketingLeadCapture{}
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}, MarketingLeads: capture})
	session := createSession(t, handler, "https://www.minvia.cn")
	rec := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/leads", "", session.AccessToken, `{
		"submission_id":"lead-invalid-email-domain",
		"email":"123123@13",
		"phone":"111",
		"enquiry":"我想预约演示",
		"consent":true
	}`)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "advisor_lead_email_invalid") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if len(capture.snapshot()) != 0 {
		t.Fatalf("invalid email reached Marketing: %+v", capture.snapshot())
	}
}

func TestWebsiteLeadSubmissionKeepsIdempotencyAndSupportsAuthenticatedContact(t *testing.T) {
	capture := &recordingMarketingLeadCapture{receipt: MarketingLeadReceipt{LeadID: "lead_same", Status: "new", Source: "minvia_website_advisor"}}
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, MarketingLeads: capture,
		BearerAuth: historyTestAuthenticator(), HistoryStore: NewMemoryWebsiteHistoryStore(),
		OwnerHMACKey: []byte("history-test-owner-key-at-least-32-bytes"),
	})
	session := createOwnedSession(t, handler, "owner-a-token")
	body := `{"submission_id":"lead-repeat-1","contact_name":"Owner A","email":"owner-a@example.com","enquiry":"Demo","consent":true}`
	first := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/leads", "owner-a-token", session.AccessToken, body)
	second := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/leads", "owner-a-token", session.AccessToken, body)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("responses = %d %s / %d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	inputs := capture.snapshot()
	if len(inputs) != 2 || inputs[0].IdempotencyKey != inputs[1].IdempotencyKey || inputs[0].UserSubjectRef == "" || inputs[0].UserSubjectRef != inputs[1].UserSubjectRef {
		t.Fatalf("capture inputs = %+v", inputs)
	}
}

func TestWebsiteLeadSubmissionReturnsRetryableMarketingOutage(t *testing.T) {
	capture := &recordingMarketingLeadCapture{err: errors.New("private downstream details")}
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}, MarketingLeads: capture})
	session := createSession(t, handler, "https://www.minvia.cn")
	rec := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/leads", "", session.AccessToken, `{"submission_id":"lead-outage-1","phone":"13800000000","enquiry":"Demo","consent":true}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"retryable":true`) || strings.Contains(rec.Body.String(), "private downstream") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestWebsiteLeadSubmissionIsRateLimitedPerSession(t *testing.T) {
	capture := &recordingMarketingLeadCapture{receipt: MarketingLeadReceipt{LeadID: "lead_rate", Status: "new", Source: "minvia_website_advisor"}}
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}, MarketingLeads: capture, LeadPerHourLimit: 2})
	session := createSession(t, handler, "https://www.minvia.cn")
	for index := 0; index < 2; index++ {
		body := `{"submission_id":"lead-rate-` + string(rune('a'+index)) + `","phone":"13800000000","enquiry":"Demo","consent":true}`
		if rec := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/leads", "", session.AccessToken, body); rec.Code != http.StatusCreated {
			t.Fatalf("submission %d = %d %s", index, rec.Code, rec.Body.String())
		}
	}
	limited := advisorRequest(handler, http.MethodPost, "/api/advisor/sessions/"+session.SessionID+"/leads", "", session.AccessToken, `{"submission_id":"lead-rate-c","phone":"13800000000","enquiry":"Demo","consent":true}`)
	if limited.Code != http.StatusTooManyRequests || !strings.Contains(limited.Body.String(), "advisor_lead_rate_limited") {
		t.Fatalf("limited = %d %s", limited.Code, limited.Body.String())
	}
}
