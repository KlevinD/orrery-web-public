package website

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/gateway/protocol"
)

const (
	defaultIdleTTL            = 30 * time.Minute
	defaultMaxTTL             = 2 * time.Hour
	defaultMaxMessageRunes    = 2000
	defaultPerMinuteLimit     = 10
	defaultPerHourLimit       = 30
	maxRequestBodyBytes       = 16 << 10
	websiteAdvisorMaxAttempts = 2
)

type gateway interface {
	SubmitTurn(context.Context, protocol.SubmitTurnInput) <-chan protocol.Event
	ActiveTurnSnapshot(sessionKey string) protocol.ActiveTurnSnapshot
}

type Options struct {
	AllowedOrigins              []string
	PublicOnly                  bool
	BearerAuth                  WebsiteBearerAuthenticator
	IdentityAssertions          *WebsiteIdentityAssertionVerifier
	HumanVerificationAssertions *WebsiteHumanVerificationAssertionVerifier
	KnowledgeSearch             WebsiteKnowledgeSearch
	HistoryStore                WebsiteHistoryStore
	MarketingLeads              MarketingLeadCapture
	GapStore                    WebsiteKnowledgeGapStore
	DistillationStore           WebsiteDistillationStore
	FAQCatalog                  *WebsiteFAQCatalog
	OwnerHMACKey                []byte
	HistoryRetention            time.Duration
	Now                         func() time.Time
	Random                      io.Reader
	IdleTTL                     time.Duration
	MaxTTL                      time.Duration
	MaxMessageRunes             int
	PerMinuteLimit              int
	PerHourLimit                int
	LeadPerHourLimit            int
}

type SessionResponse struct {
	SessionID      string    `json:"session_id"`
	ConversationID string    `json:"conversation_id"`
	AccessToken    string    `json:"access_token"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type messageRequest struct {
	Message         string `json:"message"`
	PagePath        string `json:"page_path"`
	Language        string `json:"language"`
	ClientMessageID string `json:"client_message_id"`
}

type turnLedgerEntry struct {
	requestHash [sha256.Size]byte
	events      [][]byte
	done        chan struct{}
	completed   bool
}

type visitorSession struct {
	id                   string
	tokenHash            [sha256.Size]byte
	createdAt            time.Time
	lastUsedAt           time.Time
	active               bool
	minuteEntries        []time.Time
	hourEntries          []time.Time
	leadEntries          []time.Time
	turns                map[string]*turnLedgerEntry
	conversationID       string
	ownerHash            string
	advisorTier          protocol.AdvisorTier
	locale               string
	initialPagePath      string
	unresolvedCount      int
	standardAnswerBypass string
	humanVerifiedUntil   time.Time
	lastGap              *WebsiteGapEvent
}

type turnAcquireMode int

const (
	turnAcquireExecute turnAcquireMode = iota
	turnAcquireWait
	turnAcquireReplay
)

type Handler struct {
	gateway                     gateway
	publicOnly                  bool
	now                         func() time.Time
	random                      io.Reader
	idleTTL                     time.Duration
	maxTTL                      time.Duration
	maxMessageRunes             int
	perMinuteLimit              int
	perHourLimit                int
	bearerAuth                  WebsiteBearerAuthenticator
	identityAssertions          *WebsiteIdentityAssertionVerifier
	humanVerificationAssertions *WebsiteHumanVerificationAssertionVerifier
	knowledgeSearch             WebsiteKnowledgeSearch
	historyStore                WebsiteHistoryStore
	ownerHMACKey                []byte
	historyRetention            time.Duration
	marketingLeads              MarketingLeadCapture
	gapStore                    WebsiteKnowledgeGapStore
	distillationStore           WebsiteDistillationStore
	faqCatalog                  *WebsiteFAQCatalog
	leadPerHourLimit            int
	allowedOrigins              map[string]struct{}
	mu                          sync.Mutex
	sessions                    map[string]*visitorSession
}

func NewHandler(gateway gateway, opts Options) http.Handler {
	handler, err := NewHandlerChecked(gateway, opts)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "advisor_unavailable", "Advisor is temporarily unavailable.")
		})
	}
	return handler
}

func NewHandlerChecked(gateway gateway, opts Options) (*Handler, error) {
	if err := validateStandardAnswerCatalog(websiteStandardAnswers); err != nil {
		return nil, err
	}
	if opts.BearerAuth != nil && len(opts.OwnerHMACKey) < 32 {
		return nil, fmt.Errorf("website advisor owner HMAC key must contain at least 32 bytes")
	}
	if opts.BearerAuth != nil && opts.HistoryStore == nil {
		return nil, fmt.Errorf("website advisor history store is required with bearer authentication")
	}
	if opts.IdentityAssertions != nil && len(opts.OwnerHMACKey) < 32 {
		return nil, fmt.Errorf("website advisor owner HMAC key must contain at least 32 bytes")
	}
	if opts.IdentityAssertions != nil && opts.HistoryStore == nil {
		return nil, fmt.Errorf("website advisor history store is required with identity assertions")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	random := opts.Random
	if random == nil {
		random = rand.Reader
	}
	idleTTL := opts.IdleTTL
	if idleTTL <= 0 {
		idleTTL = defaultIdleTTL
	}
	maxTTL := opts.MaxTTL
	if maxTTL <= 0 {
		maxTTL = defaultMaxTTL
	}
	maxMessageRunes := opts.MaxMessageRunes
	if maxMessageRunes <= 0 {
		maxMessageRunes = defaultMaxMessageRunes
	}
	perMinuteLimit := opts.PerMinuteLimit
	if perMinuteLimit <= 0 {
		perMinuteLimit = defaultPerMinuteLimit
	}
	perHourLimit := opts.PerHourLimit
	if perHourLimit <= 0 {
		perHourLimit = defaultPerHourLimit
	}
	leadPerHourLimit := opts.LeadPerHourLimit
	if leadPerHourLimit <= 0 {
		leadPerHourLimit = defaultLeadPerHourLimit
	}
	historyRetention := opts.HistoryRetention
	if historyRetention <= 0 {
		historyRetention = defaultHistoryRetention
	}
	faqCatalog := opts.FAQCatalog
	if faqCatalog == nil {
		faqCatalog = NewWebsiteFAQCatalog(nil)
	}
	allowedOrigins := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, origin := range opts.AllowedOrigins {
		if normalized := normalizeOrigin(origin); normalized != "" {
			allowedOrigins[normalized] = struct{}{}
		}
	}
	return &Handler{
		gateway: gateway, publicOnly: opts.PublicOnly, now: now, random: random,
		idleTTL: idleTTL, maxTTL: maxTTL, maxMessageRunes: maxMessageRunes,
		perMinuteLimit: perMinuteLimit, perHourLimit: perHourLimit,
		bearerAuth: opts.BearerAuth, identityAssertions: opts.IdentityAssertions,
		humanVerificationAssertions: opts.HumanVerificationAssertions,
		knowledgeSearch:             opts.KnowledgeSearch, historyStore: opts.HistoryStore,
		ownerHMACKey: append([]byte(nil), opts.OwnerHMACKey...), historyRetention: historyRetention,
		marketingLeads: opts.MarketingLeads, gapStore: opts.GapStore, distillationStore: opts.DistillationStore,
		faqCatalog: faqCatalog, leadPerHourLimit: leadPerHourLimit,
		allowedOrigins: allowedOrigins, sessions: map[string]*visitorSession{},
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		writeError(w, http.StatusForbidden, "advisor_origin_forbidden", "Origin is not allowed.")
		return
	}
	h.applyCORS(w, r)
	if assertion := strings.TrimSpace(r.Header.Get("X-Website-Identity")); assertion != "" {
		if h.identityAssertions == nil {
			writeError(w, http.StatusServiceUnavailable, "advisor_auth_unavailable", "Website identity verification is unavailable.")
			return
		}
		identity, err := h.identityAssertions.Verify(assertion)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "advisor_identity_invalid", "Website identity is not authorized.")
			return
		}
		if h.publicOnly {
			identity = WebsiteIdentity{Tier: protocol.AdvisorTierPublic}
		}
		r = r.WithContext(context.WithValue(r.Context(), websiteIdentityContextKey{}, identity))
	}
	if r.Method == http.MethodOptions {
		if isAdvisorPath(r.URL.Path) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/advisor/sessions" {
		h.createSession(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/advisor/faq-suggestions" {
		w.Header().Set("Cache-Control", "public, max-age=60")
		writeJSON(w, http.StatusOK, map[string]any{
			"items":    h.faqCatalog.Suggestions(r.URL.Query().Get("language"), 6),
			"revision": h.faqCatalog.Revision(),
		})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/advisor/human-verification" {
		h.verifyHuman(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/advisor/account" {
		h.handleAccount(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/advisor/conversations" {
		h.handleConversationList(w, r)
		return
	}
	if conversationID, action, ok := conversationRoute(r.URL.Path); ok {
		switch {
		case r.Method == http.MethodGet && action == "":
			h.handleConversationGet(w, r, conversationID)
		case r.Method == http.MethodDelete && action == "":
			h.handleConversationDelete(w, r, conversationID)
		case r.Method == http.MethodPost && action == "resume":
			h.handleConversationResume(w, r, conversationID)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if r.Method == http.MethodPost {
		if sessionID, ok := messageSessionID(r.URL.Path); ok {
			h.sendMessage(w, r, sessionID)
			return
		}
		if sessionID, ok := adoptSessionID(r.URL.Path); ok {
			h.handleSessionAdopt(w, r, sessionID)
			return
		}
		if sessionID, ok := leadSessionID(r.URL.Path); ok {
			h.submitMarketingLead(w, r, sessionID)
			return
		}
		if sessionID, ok := feedbackSessionID(r.URL.Path); ok {
			h.submitFeedback(w, r, sessionID)
			return
		}
	}
	http.NotFound(w, r)
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "advisor_unavailable", "Advisor is temporarily unavailable.")
		return
	}
	ownerHash := ""
	tier := protocol.AdvisorTierPublic
	if identity, ok := websiteIdentityFromContext(r.Context()); ok {
		tier = identity.Tier
		if identity.Tier == protocol.AdvisorTierCustomer {
			ownerHash = websiteOwnerHash(h.ownerHMACKey, websiteAssertionIssuer, identity.Subject)
		}
	} else if !h.publicOnly && bearerToken(r.Header.Get("Authorization")) != "" {
		_, authenticatedOwner, status, code := h.authenticatedOwner(r)
		if status != 0 {
			h.writeHistoryError(w, status, code)
			return
		}
		ownerHash = authenticatedOwner
		tier = protocol.AdvisorTierCustomer
	}
	conversationID, err := h.randomValue("webconv_", 18)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "advisor_unavailable", "Advisor is temporarily unavailable.")
		return
	}
	h.createVisitorSessionForTier(w, ownerHash, conversationID, "", "", tier)
}

func (h *Handler) createVisitorSession(w http.ResponseWriter, ownerHash, conversationID, locale, pagePath string) {
	tier := protocol.AdvisorTierPublic
	if ownerHash != "" {
		tier = protocol.AdvisorTierCustomer
	}
	h.createVisitorSessionForTier(w, ownerHash, conversationID, locale, pagePath, tier)
}

func (h *Handler) createVisitorSessionForTier(w http.ResponseWriter, ownerHash, conversationID, locale, pagePath string, tier protocol.AdvisorTier) {
	sessionID, err := h.randomValue("adv_", 18)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "advisor_unavailable", "Advisor is temporarily unavailable.")
		return
	}
	token, err := h.randomValue("", 32)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "advisor_unavailable", "Advisor is temporarily unavailable.")
		return
	}
	now := h.now().UTC()
	session := &visitorSession{
		id: sessionID, tokenHash: sha256.Sum256([]byte(token)), createdAt: now, lastUsedAt: now,
		conversationID: conversationID, ownerHash: ownerHash, advisorTier: tier, locale: locale, initialPagePath: pagePath,
	}
	h.mu.Lock()
	h.sessions[sessionID] = session
	h.mu.Unlock()
	writeJSON(w, http.StatusCreated, SessionResponse{SessionID: sessionID, ConversationID: conversationID, AccessToken: token, ExpiresAt: now.Add(h.maxTTL)})
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request, sessionID string) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var request messageRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "advisor_invalid_request", "Invalid advisor request.")
		return
	}
	request.Message = strings.TrimSpace(request.Message)
	request.ClientMessageID = strings.TrimSpace(request.ClientMessageID)
	if request.Message == "" {
		writeError(w, http.StatusBadRequest, "advisor_message_required", "Message is required.")
		return
	}
	if !utf8.ValidString(request.Message) || utf8.RuneCountInString(request.Message) > h.maxMessageRunes {
		writeError(w, http.StatusBadRequest, "advisor_message_too_long", "Message is too long.")
		return
	}
	if !validClientMessageID(request.ClientMessageID) {
		writeError(w, http.StatusBadRequest, "advisor_client_message_id_required", "Client message ID is required.")
		return
	}
	token := advisorSessionToken(r)
	entry, mode, status, code := h.acquireTurn(sessionID, token, request.ClientMessageID, sha256.Sum256([]byte(request.Message)))
	if status != 0 {
		message := "Advisor session is not authorized."
		if code == "advisor_session_expired" {
			message = "Advisor session has expired."
		} else if code == "advisor_session_active" {
			message = "An advisor response is already in progress."
		} else if code == "advisor_rate_limited" {
			message = "Advisor message rate limit exceeded."
		} else if code == "advisor_idempotency_conflict" {
			message = "Client message ID was already used for different content."
		}
		writeError(w, status, code, message)
		return
	}
	if mode == turnAcquireWait {
		select {
		case <-entry.done:
		case <-r.Context().Done():
			return
		}
		h.writeRecordedEventStream(w, entry.events)
		return
	}
	if mode == turnAcquireReplay {
		h.writeRecordedEventStream(w, entry.events)
		return
	}
	var recordedEvents [][]byte
	defer func() {
		h.persistWebsiteTurn(context.WithoutCancel(r.Context()), sessionID, request, recordedEvents)
		h.completeTurn(sessionID, request.ClientMessageID, entry, recordedEvents)
	}()
	if policy := evaluateWebsiteCommercialPolicy(request.Message, "", request.Language); policy.Decision != CommercialPolicyAllow {
		tier, _ := h.sessionAdvisorIdentity(sessionID)
		h.resetUnresolved(sessionID)
		h.recordWebsiteGap(context.WithoutCancel(r.Context()), sessionID, request.Message, tier, "commercial_policy", string(policy.Decision), GapOutcomeResolved, 0, nil, false)
		recordedEvents = h.writePayloadStream(w, []any{
			map[string]any{
				"type": string(protocol.EventAssistantTextDelta), "text": policy.Text,
				"class": "lead_prompt", "sources": []string{}, "action": policy.Action,
			},
			completionPayload("commercial_policy", "resolved", false),
		})
		return
	}
	if answer, ok := h.faqCatalog.Match(request.Message, request.Language); ok && !h.consumeStandardAnswerBypass(sessionID, answer.IntentID) {
		tier, _ := h.sessionAdvisorIdentity(sessionID)
		if answer.Class == standardAnswerConversational {
			h.resetUnresolved(sessionID)
		}
		h.recordWebsiteGap(context.WithoutCancel(r.Context()), sessionID, request.Message, tier, "standard_answer", answer.IntentID, GapOutcomeResolved, 0, answer.Sources, false)
		recordedEvents = h.writeStandardAnswer(w, answer)
		return
	}

	tier, externalUserID := h.sessionAdvisorIdentity(sessionID)
	if h.knowledgeSearch != nil && (tier == protocol.AdvisorTierCustomer || !h.humanVerificationActive(sessionID)) {
		result, err := h.knowledgeSearch.Search(r.Context(), tier, request.Message)
		if err != nil {
			h.recordWebsiteGap(context.WithoutCancel(r.Context()), sessionID, request.Message, tier, "knowledge_search", "", GapOutcomeUnavailable, 0, nil, false)
			recordedEvents = h.writePayloadStream(w, []any{map[string]string{
				"type": "error", "code": "advisor_search_unavailable",
			}})
			return
		}
		if validWebsiteSearchAnswer(result, tier) {
			h.resetUnresolved(sessionID)
			h.recordWebsiteGap(context.WithoutCancel(r.Context()), sessionID, request.Message, tier, "knowledge_search", "", GapOutcomeResolved, result.Score, []string{result.SourceURI}, false)
			recordedEvents = h.writePayloadStream(w, []any{
				map[string]any{
					"type": string(protocol.EventAssistantTextDelta), "text": result.Answer,
					"class": "standard", "sources": []string{result.SourceURI}, "revision": result.Revision,
				},
				completionPayload("knowledge_search", "resolved", false),
			})
			return
		}
		if tier == protocol.AdvisorTierPublic {
			stage := h.advanceUnresolved(sessionID)
			if stage == 1 && !websiteQuestionHasAgentDetail(request.Message) {
				h.recordWebsiteGap(context.WithoutCancel(r.Context()), sessionID, request.Message, tier, "clarification", "", GapOutcomeNeedsClarification, result.Score, []string{result.SourceURI}, false)
				recordedEvents = h.writePayloadStream(w, []any{
					map[string]any{
						"type": string(protocol.EventAssistantTextDelta), "text": websiteClarificationText(request.Language),
						"class": "clarification", "sources": []string{},
					},
					completionPayload("clarification", "needs_clarification", false),
				})
				return
			}
			h.recordWebsiteGap(context.WithoutCancel(r.Context()), sessionID, request.Message, tier, "human_verification", "", GapOutcomeUnresolved, result.Score, []string{result.SourceURI}, false)
			recordedEvents = h.writePayloadStream(w, []any{
				map[string]any{
					"type": string(protocol.EventAssistantTextDelta), "text": websiteHumanVerificationText(request.Language),
					"class": "clarification", "sources": []string{}, "action": "human_verification",
				},
				completionPayload("human_verification", "unresolved", true),
			})
			return
		}
	}
	input := protocol.SubmitTurnInput{
		SessionKey: "website:" + sessionID,
		ChannelKey: protocol.ChannelWebsite,
		Message:    h.websiteAgentMessage(r.Context(), sessionID, request.Message),
		RunID:      websiteRunID(sessionID, request.ClientMessageID),
		Telemetry: map[string]string{
			"page_path": truncateRunes(strings.TrimSpace(request.PagePath), 512),
			"language":  truncateRunes(strings.TrimSpace(request.Language), 32),
		},
		Identity: &protocol.InboundIdentity{
			ExternalUserID: externalUserID,
			ChatKind:       "direct",
			DisplayName:    websiteIdentityDisplayName(tier),
			Verified:       true,
		},
		AdvisorTier: tier,
	}
	route := "public_agent"
	if tier == protocol.AdvisorTierCustomer {
		route = "customer_agent"
	}
	recordedEvents = h.writeEventStream(w, route, request.Message, request.Language, func() <-chan protocol.Event {
		return h.gateway.SubmitTurn(r.Context(), input)
	})
	actualRoute := routeFromRecordedEvents(recordedEvents, route)
	outcome := outcomeFromRecordedEvents(recordedEvents)
	sources := sourcesFromRecordedEvents(recordedEvents)
	h.recordWebsiteGap(context.WithoutCancel(r.Context()), sessionID, request.Message, tier, actualRoute, "", outcome, 0, sources, actualRoute == route)
	h.recordWebsiteDistillationCandidate(context.WithoutCancel(r.Context()), sessionID, request, recordedEvents, tier, actualRoute, outcome)
}

type humanVerificationRequest struct {
	SessionID string `json:"session_id"`
}

func (h *Handler) verifyHuman(w http.ResponseWriter, r *http.Request) {
	if h.humanVerificationAssertions == nil {
		writeError(w, http.StatusServiceUnavailable, "advisor_human_verification_unavailable", "Human verification is unavailable.")
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var request humanVerificationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !validWebsiteOpaqueClaim(strings.TrimSpace(request.SessionID)) {
		writeError(w, http.StatusBadRequest, "advisor_invalid_request", "Invalid human verification request.")
		return
	}
	request.SessionID = strings.TrimSpace(request.SessionID)
	if _, status, code := h.authorizeSession(request.SessionID, advisorSessionToken(r)); status != 0 {
		writeError(w, status, code, "Advisor session is not authorized.")
		return
	}
	verifiedSessionID, err := h.humanVerificationAssertions.Verify(r.Header.Get("X-Website-Human-Verification"))
	if err != nil || verifiedSessionID != request.SessionID {
		writeError(w, http.StatusUnauthorized, "advisor_human_verification_invalid", "Human verification is not authorized.")
		return
	}
	h.mu.Lock()
	if session := h.sessions[request.SessionID]; session != nil {
		session.humanVerifiedUntil = h.now().UTC().Add(websiteHumanVerificationTTL)
	}
	h.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) humanVerificationActive(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[sessionID]
	return session != nil && session.humanVerifiedUntil.After(h.now().UTC())
}

func (h *Handler) advanceUnresolved(sessionID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if session := h.sessions[sessionID]; session != nil {
		session.unresolvedCount++
		return session.unresolvedCount
	}
	return 1
}

func (h *Handler) resetUnresolved(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if session := h.sessions[sessionID]; session != nil {
		session.unresolvedCount = 0
	}
}

func (h *Handler) consumeStandardAnswerBypass(sessionID, intentID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[sessionID]
	if session == nil || session.standardAnswerBypass != intentID {
		return false
	}
	if session.advisorTier != protocol.AdvisorTierCustomer && !session.humanVerifiedUntil.After(h.now().UTC()) {
		return false
	}
	session.standardAnswerBypass = ""
	return true
}

func websiteClarificationText(locale string) string {
	if normalizeStandardAnswerLocale(locale) == "en-US" {
		return "Please add the product, systems involved, desired outcome, and any deployment constraints."
	}
	return "请补充具体产品、涉及的系统、希望达成的结果，以及部署或权限限制。"
}

func websiteQuestionHasAgentDetail(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if utf8.RuneCountInString(message) < 80 {
		return false
	}
	detailGroups := [][]string{
		{"orrery", "产品", "智能体", "agent"},
		{"erp", "crm", "飞书", "用友", "现有系统", "existing system"},
		{"希望", "目标", "流程", "推进", "达成", "goal", "workflow", "outcome"},
		{"本地部署", "私有化", "内网", "权限", "人工确认", "审计", "限制", "on-prem", "private deployment", "permission", "audit", "constraint"},
	}
	for _, group := range detailGroups {
		if !containsWebsitePolicyPhrase(message, group) {
			return false
		}
	}
	return true
}

func websiteHumanVerificationText(locale string) string {
	if normalizeStandardAnswerLocale(locale) == "en-US" {
		return "I still cannot resolve this from approved website knowledge. Complete human verification to continue with the Orrery service agent."
	}
	return "已审核的官网知识仍无法解决这个问题。请完成人机验证后，由 Orrery 客服智能体继续接待。"
}

func completionPayload(route, resolution string, humanVerificationRequired bool) map[string]any {
	return map[string]any{
		"type": string(protocol.EventTurnCompleted), "route": route, "resolution": resolution,
		"human_verification_required": humanVerificationRequired,
	}
}

func (h *Handler) sessionAdvisorIdentity(sessionID string) (protocol.AdvisorTier, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[sessionID]
	if session == nil {
		return protocol.AdvisorTierPublic, sessionID
	}
	tier := session.advisorTier
	if tier != protocol.AdvisorTierCustomer {
		tier = protocol.AdvisorTierPublic
	}
	externalUserID := sessionID
	if tier == protocol.AdvisorTierCustomer && session.ownerHash != "" {
		externalUserID = session.ownerHash
	}
	return tier, externalUserID
}

func websiteIdentityDisplayName(tier protocol.AdvisorTier) string {
	if tier == protocol.AdvisorTierCustomer {
		return "Website customer"
	}
	return "Website visitor"
}

type websiteIdentityContextKey struct{}

func websiteIdentityFromContext(ctx context.Context) (WebsiteIdentity, bool) {
	identity, ok := ctx.Value(websiteIdentityContextKey{}).(WebsiteIdentity)
	return identity, ok
}

func (h *Handler) writeStandardAnswer(w http.ResponseWriter, answer standardAnswer) [][]byte {
	sources := answer.Sources
	if sources == nil {
		sources = []string{}
	}
	payload := map[string]any{
		"type":    string(protocol.EventAssistantTextDelta),
		"text":    answer.Text,
		"class":   string(answer.Class),
		"sources": sources,
	}
	if answer.Action != "" {
		payload["action"] = answer.Action
	}
	return h.writePayloadStream(w, []any{
		payload,
		completionPayload("standard_answer", "resolved", false),
	})
}

func (h *Handler) acquireTurn(sessionID, token, clientMessageID string, requestHash [sha256.Size]byte) (*turnLedgerEntry, turnAcquireMode, int, string) {
	if strings.TrimSpace(token) == "" {
		return nil, turnAcquireExecute, http.StatusUnauthorized, "advisor_unauthorized"
	}
	now := h.now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[sessionID]
	if session == nil {
		return nil, turnAcquireExecute, http.StatusUnauthorized, "advisor_unauthorized"
	}
	providedHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(providedHash[:], session.tokenHash[:]) != 1 {
		return nil, turnAcquireExecute, http.StatusUnauthorized, "advisor_unauthorized"
	}
	if now.Sub(session.lastUsedAt) > h.idleTTL || now.Sub(session.createdAt) > h.maxTTL {
		delete(h.sessions, sessionID)
		return nil, turnAcquireExecute, http.StatusUnauthorized, "advisor_session_expired"
	}
	if session.turns == nil {
		session.turns = map[string]*turnLedgerEntry{}
	}
	if existing := session.turns[clientMessageID]; existing != nil {
		if subtle.ConstantTimeCompare(existing.requestHash[:], requestHash[:]) != 1 {
			return nil, turnAcquireExecute, http.StatusConflict, "advisor_idempotency_conflict"
		}
		session.lastUsedAt = now
		if existing.completed {
			return existing, turnAcquireReplay, 0, ""
		}
		return existing, turnAcquireWait, 0, ""
	}
	if session.active {
		return nil, turnAcquireExecute, http.StatusTooManyRequests, "advisor_session_active"
	}
	session.minuteEntries = timestampsSince(session.minuteEntries, now.Add(-time.Minute))
	session.hourEntries = timestampsSince(session.hourEntries, now.Add(-time.Hour))
	if len(session.minuteEntries) >= h.perMinuteLimit || len(session.hourEntries) >= h.perHourLimit {
		return nil, turnAcquireExecute, http.StatusTooManyRequests, "advisor_rate_limited"
	}
	session.minuteEntries = append(session.minuteEntries, now)
	session.hourEntries = append(session.hourEntries, now)
	session.lastUsedAt = now
	session.active = true
	entry := &turnLedgerEntry{requestHash: requestHash, done: make(chan struct{})}
	session.turns[clientMessageID] = entry
	return entry, turnAcquireExecute, 0, ""
}

func (h *Handler) authorizeSession(sessionID, token string) (*visitorSession, int, string) {
	if strings.TrimSpace(token) == "" {
		return nil, http.StatusUnauthorized, "advisor_unauthorized"
	}
	now := h.now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[sessionID]
	if session == nil {
		return nil, http.StatusUnauthorized, "advisor_unauthorized"
	}
	providedHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(providedHash[:], session.tokenHash[:]) != 1 {
		return nil, http.StatusUnauthorized, "advisor_unauthorized"
	}
	if now.Sub(session.lastUsedAt) > h.idleTTL || now.Sub(session.createdAt) > h.maxTTL {
		delete(h.sessions, sessionID)
		return nil, http.StatusUnauthorized, "advisor_session_expired"
	}
	session.lastUsedAt = now
	return session, 0, ""
}

func (h *Handler) completeTurn(sessionID, clientMessageID string, entry *turnLedgerEntry, events [][]byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if session := h.sessions[sessionID]; session != nil {
		current := session.turns[clientMessageID]
		if current == entry && !entry.completed {
			entry.events = cloneRecordedEvents(events)
			entry.completed = true
			close(entry.done)
		}
		session.active = false
		session.lastUsedAt = h.now().UTC()
	}
}

func (h *Handler) writeEventStream(w http.ResponseWriter, route, question, locale string, submit func() <-chan protocol.Event) [][]byte {
	prepareEventStream(w)
	flusher, _ := w.(http.Flusher)
	for attempt := 0; attempt < websiteAdvisorMaxAttempts; attempt++ {
		events := submit()
		terminal := false
		terminalError := false
		retry := false
		var answer strings.Builder
		var sources []string
		for event := range events {
			if terminal {
				continue
			}
			switch event.Type {
			case protocol.EventAssistantTextDelta:
				if event.Text == "" {
					continue
				}
				answer.WriteString(event.Text)
				sources = append(sources, event.Sources...)
			case protocol.EventTurnCompleted:
				terminal = true
			case protocol.EventError:
				terminal = true
				terminalError = true
				if attempt+1 < websiteAdvisorMaxAttempts && answer.Len() == 0 {
					retry = true
					continue
				}
			default:
				continue
			}
		}
		if retry {
			continue
		}
		if terminal {
			if answer.Len() == 0 {
				return [][]byte{writeSSE(w, flusher, safeUnavailableEvent())}
			}
			text := strings.TrimSpace(answer.String())
			policy := evaluateWebsiteCommercialPolicy(question, text, locale)
			payload := map[string]any{
				"type": string(protocol.EventAssistantTextDelta), "text": text,
				"sources": uniqueWebsitePublicSources(sources),
			}
			completionRoute := route
			if policy.Decision != CommercialPolicyAllow {
				payload["text"] = policy.Text
				payload["sources"] = []string{}
				payload["class"] = "lead_prompt"
				payload["action"] = policy.Action
				completionRoute = "commercial_policy"
			}
			if terminalError && policy.Decision == CommercialPolicyAllow {
				return [][]byte{
					writeSSE(w, flusher, payload),
					writeSSE(w, flusher, safeUnavailableEvent()),
				}
			}
			return [][]byte{
				writeSSE(w, flusher, payload),
				writeSSE(w, flusher, completionPayload(completionRoute, "responded", false)),
			}
		}
		if attempt+1 < websiteAdvisorMaxAttempts && answer.Len() == 0 {
			continue
		}
		return [][]byte{writeSSE(w, flusher, safeUnavailableEvent())}
	}
	return [][]byte{writeSSE(w, flusher, safeUnavailableEvent())}
}

func uniqueWebsitePublicSources(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, "minviabrain://08-Wiki/Public/Orrery/") || strings.Contains(value, "..") || strings.Contains(value, "\\") {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == 8 {
			break
		}
	}
	return result
}

func safeUnavailableEvent() map[string]string {
	return map[string]string{
		"type": "error", "code": "advisor_unavailable",
	}
}

func writeSSE(w io.Writer, flusher http.Flusher, payload any) []byte {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	writeRecordedSSE(w, flusher, raw)
	return raw
}

func writeRecordedSSE(w io.Writer, flusher http.Flusher, raw []byte) {
	if len(raw) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	if flusher != nil {
		flusher.Flush()
	}
}

func (h *Handler) writePayloadStream(w http.ResponseWriter, payloads []any) [][]byte {
	prepareEventStream(w)
	flusher, _ := w.(http.Flusher)
	recorded := make([][]byte, 0, len(payloads))
	for _, payload := range payloads {
		recorded = append(recorded, writeSSE(w, flusher, payload))
	}
	return recorded
}

func (h *Handler) writeRecordedEventStream(w http.ResponseWriter, events [][]byte) {
	prepareEventStream(w)
	flusher, _ := w.(http.Flusher)
	for _, event := range events {
		writeRecordedSSE(w, flusher, event)
	}
}

func prepareEventStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func cloneRecordedEvents(events [][]byte) [][]byte {
	cloned := make([][]byte, len(events))
	for index, event := range events {
		cloned[index] = append([]byte(nil), event...)
	}
	return cloned
}

func validClientMessageID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-", r) {
			continue
		}
		return false
	}
	return true
}

func websiteRunID(sessionID, clientMessageID string) string {
	digest := sha256.Sum256([]byte(sessionID + "\x00" + clientMessageID))
	return fmt.Sprintf("web_%x", digest[:12])
}

func (h *Handler) randomValue(prefix string, size int) (string, error) {
	raw := make([]byte, size)
	if _, err := io.ReadFull(h.random, raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (h *Handler) originAllowed(r *http.Request) bool {
	if h == nil || r == nil {
		return false
	}
	origin := normalizeOrigin(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	_, ok := h.allowedOrigins[origin]
	return ok
}

func (h *Handler) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := normalizeOrigin(r.Header.Get("Origin"))
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Headers", "authorization, content-type, x-advisor-session-token")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Vary", "Origin")
}

func normalizeOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}

func isAdvisorPath(path string) bool {
	if path == "/api/advisor/sessions" || path == "/api/advisor/account" || path == "/api/advisor/conversations" || path == "/api/advisor/human-verification" || path == "/api/advisor/faq-suggestions" {
		return true
	}
	if _, ok := messageSessionID(path); ok {
		return true
	}
	if _, ok := adoptSessionID(path); ok {
		return true
	}
	if _, ok := leadSessionID(path); ok {
		return true
	}
	_, _, ok := conversationRoute(path)
	return ok
}

func messageSessionID(path string) (string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "api" || parts[2] != "advisor" || parts[3] != "sessions" || parts[5] != "messages" || strings.TrimSpace(parts[4]) == "" {
		return "", false
	}
	return parts[4], true
}

func adoptSessionID(path string) (string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "api" || parts[2] != "advisor" || parts[3] != "sessions" || parts[5] != "adopt" || strings.TrimSpace(parts[4]) == "" {
		return "", false
	}
	return parts[4], true
}

func leadSessionID(path string) (string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "api" || parts[2] != "advisor" || parts[3] != "sessions" || parts[5] != "leads" || strings.TrimSpace(parts[4]) == "" {
		return "", false
	}
	return parts[4], true
}

func conversationRoute(path string) (string, string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) == 5 && parts[0] == "" && parts[1] == "api" && parts[2] == "advisor" && parts[3] == "conversations" && strings.TrimSpace(parts[4]) != "" {
		return parts[4], "", true
	}
	if len(parts) == 6 && parts[0] == "" && parts[1] == "api" && parts[2] == "advisor" && parts[3] == "conversations" && strings.TrimSpace(parts[4]) != "" && parts[5] == "resume" {
		return parts[4], "resume", true
	}
	return "", "", false
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func advisorSessionToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if token := strings.TrimSpace(r.Header.Get("X-Advisor-Session-Token")); token != "" {
		return token
	}
	return bearerToken(r.Header.Get("Authorization"))
}

func timestampsSince(values []time.Time, threshold time.Time) []time.Time {
	out := values[:0]
	for _, value := range values {
		if !value.Before(threshold) {
			out = append(out, value)
		}
	}
	return out
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
