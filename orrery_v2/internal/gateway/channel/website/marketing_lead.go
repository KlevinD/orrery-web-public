package website

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultLeadPerHourLimit = 5
	websiteLeadSource       = "minvia_website_advisor"
	websiteConsentVersion   = "website-lead-v1"
)

var websiteLeadEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

type MarketingLeadCaptureInput struct {
	IdempotencyKey       string
	ContactName          string
	Company              string
	Role                 string
	Email                string
	Phone                string
	Interest             string
	DeploymentPreference string
	Enquiry              string
	Locale               string
	SourcePagePath       string
	ConversationID       string
	UserSubjectRef       string
	Source               string
	ConsentVersion       string
	ConsentAt            time.Time
}

type MarketingLeadReceipt struct {
	LeadID    string    `json:"lead_id"`
	Status    string    `json:"status"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

type MarketingLeadCapture interface {
	CaptureWebsiteLead(context.Context, MarketingLeadCaptureInput) (MarketingLeadReceipt, error)
}

type websiteLeadRequest struct {
	SubmissionID         string `json:"submission_id"`
	ContactName          string `json:"contact_name"`
	Company              string `json:"company"`
	Role                 string `json:"role"`
	Email                string `json:"email"`
	Phone                string `json:"phone"`
	Interest             string `json:"interest"`
	DeploymentPreference string `json:"deployment_preference"`
	Enquiry              string `json:"enquiry"`
	Locale               string `json:"locale"`
	SourcePagePath       string `json:"source_page_path"`
	Consent              bool   `json:"consent"`
}

func (h *Handler) submitMarketingLead(w http.ResponseWriter, r *http.Request, sessionID string) {
	if h.marketingLeads == nil {
		writeLeadUnavailable(w)
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request websiteLeadRequest
	if err := decoder.Decode(&request); err != nil {
		writeLeadInvalid(w)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeLeadInvalid(w)
		return
	}
	request.trim()
	if request.Email != "" && !websiteLeadEmailPattern.MatchString(request.Email) {
		writeLeadEmailInvalid(w)
		return
	}
	if !request.valid() {
		writeLeadInvalid(w)
		return
	}

	conversationID, ownerHash, status, code := h.acquireLeadSubmission(sessionID, advisorSessionToken(r))
	if status != 0 {
		message := "Advisor session is not authorized."
		if code == "advisor_session_expired" {
			message = "Advisor session has expired."
		} else if code == "advisor_lead_rate_limited" {
			message = "Lead submission rate limit exceeded."
		}
		writeError(w, status, code, message)
		return
	}

	receipt, err := h.marketingLeads.CaptureWebsiteLead(r.Context(), MarketingLeadCaptureInput{
		IdempotencyKey: request.SubmissionID, ContactName: request.ContactName,
		Company: request.Company, Role: request.Role, Email: request.Email, Phone: request.Phone,
		Interest: request.Interest, DeploymentPreference: request.DeploymentPreference,
		Enquiry: request.Enquiry, Locale: request.Locale, SourcePagePath: request.SourcePagePath,
		ConversationID: conversationID, UserSubjectRef: ownerHash, Source: websiteLeadSource,
		ConsentVersion: websiteConsentVersion, ConsentAt: h.now().UTC(),
	})
	if err != nil {
		writeLeadUnavailable(w)
		return
	}
	h.recordWebsiteLeadTransfer(context.WithoutCancel(r.Context()), sessionID, request.Enquiry)
	writeJSON(w, http.StatusCreated, receipt)
}

func (r *websiteLeadRequest) trim() {
	r.SubmissionID = strings.TrimSpace(r.SubmissionID)
	r.ContactName = strings.TrimSpace(r.ContactName)
	r.Company = strings.TrimSpace(r.Company)
	r.Role = strings.TrimSpace(r.Role)
	r.Email = strings.TrimSpace(r.Email)
	r.Phone = strings.TrimSpace(r.Phone)
	r.Interest = strings.TrimSpace(r.Interest)
	r.DeploymentPreference = strings.TrimSpace(r.DeploymentPreference)
	r.Enquiry = strings.TrimSpace(r.Enquiry)
	r.Locale = strings.TrimSpace(r.Locale)
	r.SourcePagePath = strings.TrimSpace(r.SourcePagePath)
}

func (r websiteLeadRequest) valid() bool {
	if !r.Consent || !validClientMessageID(r.SubmissionID) || (r.Email == "" && r.Phone == "") || r.Enquiry == "" {
		return false
	}
	if r.Email != "" && !websiteLeadEmailPattern.MatchString(r.Email) {
		return false
	}
	for value, limit := range map[string]int{
		r.ContactName: 160, r.Company: 240, r.Role: 160, r.Email: 320, r.Phone: 64,
		r.Interest: 240, r.DeploymentPreference: 80, r.Enquiry: 4000, r.Locale: 32, r.SourcePagePath: 512,
	} {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
			return false
		}
	}
	return true
}

func (h *Handler) acquireLeadSubmission(sessionID, token string) (string, string, int, string) {
	if strings.TrimSpace(token) == "" {
		return "", "", http.StatusUnauthorized, "advisor_unauthorized"
	}
	now := h.now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[sessionID]
	if session == nil {
		return "", "", http.StatusUnauthorized, "advisor_unauthorized"
	}
	providedHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(providedHash[:], session.tokenHash[:]) != 1 {
		return "", "", http.StatusUnauthorized, "advisor_unauthorized"
	}
	if now.Sub(session.lastUsedAt) > h.idleTTL || now.Sub(session.createdAt) > h.maxTTL {
		delete(h.sessions, sessionID)
		return "", "", http.StatusUnauthorized, "advisor_session_expired"
	}
	session.leadEntries = timestampsSince(session.leadEntries, now.Add(-time.Hour))
	if len(session.leadEntries) >= h.leadPerHourLimit {
		return "", "", http.StatusTooManyRequests, "advisor_lead_rate_limited"
	}
	session.leadEntries = append(session.leadEntries, now)
	session.lastUsedAt = now
	return session.conversationID, session.ownerHash, 0, ""
}

func writeLeadInvalid(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "advisor_lead_invalid", "Lead submission is invalid.")
}

func writeLeadEmailInvalid(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "advisor_lead_email_invalid", "Email address is invalid.")
}

func writeLeadUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error": map[string]any{
			"code": "advisor_lead_unavailable", "message": "Lead submission is temporarily unavailable.", "retryable": true,
		},
	})
}
