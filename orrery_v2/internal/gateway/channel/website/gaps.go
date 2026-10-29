package website

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"orrery_v2/internal/gateway/protocol"
)

const (
	websiteGapWindow            = 7 * 24 * time.Hour
	websitePublicGapThreshold   = 5
	websiteCustomerGapThreshold = 3
)

type GapOutcome string

const (
	GapOutcomeResolved           GapOutcome = "resolved"
	GapOutcomeUnresolved         GapOutcome = "unresolved"
	GapOutcomeNeedsClarification GapOutcome = "needs_clarification"
	GapOutcomeUnavailable        GapOutcome = "unavailable"
)

type GapFeedback string

const (
	GapFeedbackResolved   GapFeedback = "resolved"
	GapFeedbackUnresolved GapFeedback = "unresolved"
)

type WebsiteGapEventInput struct {
	ConversationID string
	Topic          string
	Tier           protocol.AdvisorTier
	Route          string
	MatchedIntent  string
	RetrievalScore float64
	SourceIDs      []string
	Outcome        GapOutcome
	Feedback       GapFeedback
	Takeover       bool
	OccurredAt     time.Time
}

type WebsiteGapEvent struct {
	EventID                 string               `json:"event_id"`
	TopicFingerprint        string               `json:"topic_fingerprint"`
	ConversationFingerprint string               `json:"conversation_fingerprint"`
	Tier                    protocol.AdvisorTier `json:"tier"`
	Route                   string               `json:"route"`
	MatchedIntent           string               `json:"matched_intent,omitempty"`
	RetrievalScore          float64              `json:"retrieval_score"`
	SourceIDs               []string             `json:"source_ids"`
	Outcome                 GapOutcome           `json:"outcome"`
	Feedback                GapFeedback          `json:"feedback,omitempty"`
	Takeover                bool                 `json:"takeover"`
	OccurredAt              time.Time            `json:"occurred_at"`
}

type WebsiteGapAggregate struct {
	GapID             string               `json:"gap_id"`
	TopicFingerprint  string               `json:"topic_fingerprint"`
	Tier              protocol.AdvisorTier `json:"tier"`
	ConversationCount int                  `json:"conversation_count"`
	Candidate         bool                 `json:"candidate"`
	FirstSeenAt       time.Time            `json:"first_seen_at"`
	LastSeenAt        time.Time            `json:"last_seen_at"`
}

func NewWebsiteGapEvent(input WebsiteGapEventInput) (WebsiteGapEvent, error) {
	topic := normalizeGapTopic(input.Topic)
	conversation := strings.TrimSpace(input.ConversationID)
	if topic == "" || conversation == "" {
		return WebsiteGapEvent{}, errors.New("website gap topic and conversation are required")
	}
	tier := input.Tier
	if tier != protocol.AdvisorTierCustomer {
		tier = protocol.AdvisorTierPublic
	}
	when := input.OccurredAt.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	topicFingerprint := hashWebsiteGapValue("topic", topic)
	conversationFingerprint := hashWebsiteGapValue("conversation", conversation)
	eventID := hashWebsiteGapValue("event", conversationFingerprint+"\x00"+topicFingerprint)
	return WebsiteGapEvent{
		EventID: eventID, TopicFingerprint: topicFingerprint, ConversationFingerprint: conversationFingerprint,
		Tier: tier, Route: cleanGapLabel(input.Route), MatchedIntent: cleanGapLabel(input.MatchedIntent),
		RetrievalScore: boundedGapScore(input.RetrievalScore), SourceIDs: sanitizeGapSources(input.SourceIDs, tier),
		Outcome: normalizeGapOutcome(input.Outcome), Feedback: normalizeGapFeedback(input.Feedback),
		Takeover: input.Takeover, OccurredAt: when,
	}, nil
}

func websiteGapThreshold(tier protocol.AdvisorTier) int {
	if tier == protocol.AdvisorTierCustomer {
		return websiteCustomerGapThreshold
	}
	return websitePublicGapThreshold
}

func normalizeGapTopic(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	space := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			out.WriteRune(r)
			space = false
		case out.Len() > 0 && !space:
			out.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(out.String())
}

func hashWebsiteGapValue(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + value))
	return hex.EncodeToString(sum[:])
}

func cleanGapLabel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func boundedGapScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func normalizeGapOutcome(value GapOutcome) GapOutcome {
	switch value {
	case GapOutcomeResolved, GapOutcomeNeedsClarification, GapOutcomeUnavailable:
		return value
	default:
		return GapOutcomeUnresolved
	}
}

func normalizeGapFeedback(value GapFeedback) GapFeedback {
	if value == GapFeedbackResolved || value == GapFeedbackUnresolved {
		return value
	}
	return ""
}

func sanitizeGapSources(values []string, tier protocol.AdvisorTier) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, source := range values {
		source = strings.TrimSpace(source)
		allowed := strings.HasPrefix(source, "minviabrain://08-Wiki/Public/Orrery/")
		if tier == protocol.AdvisorTierCustomer {
			allowed = allowed || strings.HasPrefix(source, "minviabrain://08-Wiki/Customer/Orrery/")
		}
		if !allowed || strings.Contains(source, "..") || strings.Contains(source, "\\") {
			continue
		}
		if _, exists := seen[source]; exists {
			continue
		}
		seen[source] = struct{}{}
		out = append(out, source)
		if len(out) == 8 {
			break
		}
	}
	return out
}

type RuleKind string

const (
	RuleKindAlias           RuleKind = "alias"
	RuleKindMatchRule       RuleKind = "match_rule"
	RuleKindPopularityOrder RuleKind = "popularity_order"
	RuleKindRetrievalWeight RuleKind = "retrieval_weight"
	RuleKindAnswerDemotion  RuleKind = "answer_demotion"
)

type AutomaticRuleRevisionInput struct {
	Kind                   RuleKind
	Before                 string
	After                  string
	EvidenceWindow         string
	AuthoringJob           string
	MetricsSnapshot        string
	ApprovedAnswerRevision string
	PreviousRevisionID     string
	Now                    time.Time
}

type AutomaticRuleRevision struct {
	RevisionID             string    `json:"revision_id"`
	RevisionHash           string    `json:"revision_hash"`
	Kind                   RuleKind  `json:"kind"`
	Before                 string    `json:"before"`
	After                  string    `json:"after"`
	EvidenceWindow         string    `json:"evidence_window"`
	AuthoringJob           string    `json:"authoring_job"`
	MetricsSnapshot        string    `json:"metrics_snapshot"`
	ApprovedAnswerRevision string    `json:"approved_answer_revision"`
	ActivatedAt            time.Time `json:"activated_at"`
	RollbackRevisionID     string    `json:"rollback_revision_id"`
	RolledBackAt           time.Time `json:"rolled_back_at,omitempty"`
	RollbackReason         string    `json:"rollback_reason,omitempty"`
}

func NewAutomaticRuleRevision(input AutomaticRuleRevisionInput) (AutomaticRuleRevision, error) {
	if !automaticRuleKindAllowed(input.Kind) || strings.TrimSpace(input.After) == "" || strings.TrimSpace(input.ApprovedAnswerRevision) == "" {
		return AutomaticRuleRevision{}, errors.New("automatic website rule change is not allowed")
	}
	if input.Kind == RuleKindRetrievalWeight {
		weight, err := strconv.ParseFloat(strings.TrimSpace(input.After), 64)
		if err != nil || weight < 0.5 || weight > 1.5 {
			return AutomaticRuleRevision{}, errors.New("website retrieval weight is outside the safe range")
		}
	}
	when := input.Now.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	canonical, _ := json.Marshal([]string{string(input.Kind), input.Before, input.After, input.EvidenceWindow, input.AuthoringJob, input.MetricsSnapshot, input.ApprovedAnswerRevision, input.PreviousRevisionID})
	revisionHash := hashWebsiteGapValue("rule-revision", string(canonical))
	rollbackRevisionID := strings.TrimSpace(input.PreviousRevisionID)
	if rollbackRevisionID == "" {
		rollbackRevisionID = "baseline_" + hashWebsiteGapValue("rule-baseline", string(input.Kind)+"\x00"+input.Before)[:24]
	}
	return AutomaticRuleRevision{
		RevisionID: "wkr_" + revisionHash[:24], RevisionHash: revisionHash, Kind: input.Kind,
		Before: input.Before, After: input.After, EvidenceWindow: input.EvidenceWindow,
		AuthoringJob: input.AuthoringJob, MetricsSnapshot: input.MetricsSnapshot,
		ApprovedAnswerRevision: input.ApprovedAnswerRevision, ActivatedAt: when,
		RollbackRevisionID: rollbackRevisionID,
	}, nil
}

func automaticRuleKindAllowed(kind RuleKind) bool {
	switch kind {
	case RuleKindAlias, RuleKindMatchRule, RuleKindPopularityOrder, RuleKindRetrievalWeight, RuleKindAnswerDemotion:
		return true
	default:
		return false
	}
}

type ProposalStatus string

const (
	ProposalStatusDraft     ProposalStatus = "draft"
	ProposalStatusApproved  ProposalStatus = "approved"
	ProposalStatusPublished ProposalStatus = "published"
)

type WebsiteKnowledgeProposalKind string

const (
	ProposalKindKnowledgeDocument  WebsiteKnowledgeProposalKind = "knowledge_document"
	ProposalKindFAQEntry           WebsiteKnowledgeProposalKind = "faq_entry"
	ProposalKindFAQAlias           WebsiteKnowledgeProposalKind = "faq_alias"
	ProposalKindFAQSuggestionOrder WebsiteKnowledgeProposalKind = "faq_suggestion_order"
	ProposalKindFAQDemotion        WebsiteKnowledgeProposalKind = "faq_demotion"
)

type WebsiteKnowledgeReceiptKind string

const (
	WebsiteKnowledgeReceiptApproval    WebsiteKnowledgeReceiptKind = "approval"
	WebsiteKnowledgeReceiptPublication WebsiteKnowledgeReceiptKind = "publication"
)

type WebsiteKnowledgeReceipt struct {
	ReceiptID    string                      `json:"receipt_id"`
	Kind         WebsiteKnowledgeReceiptKind `json:"kind"`
	ProposalID   string                      `json:"proposal_id"`
	RevisionHash string                      `json:"revision_hash"`
	ActorID      string                      `json:"actor_id"`
	CreatedAt    time.Time                   `json:"created_at"`
}

type WebsiteFAQMetric struct {
	IntentID          string               `json:"intent_id"`
	Tier              protocol.AdvisorTier `json:"tier"`
	Route             string               `json:"route"`
	Outcome           GapOutcome           `json:"outcome"`
	ConversationCount int                  `json:"conversation_count"`
	TakeoverCount     int                  `json:"takeover_count"`
	SourceIDs         []string             `json:"source_ids"`
	LastSeenAt        time.Time            `json:"last_seen_at"`
}

type WebsiteKnowledgeProposalInput struct {
	Kind               WebsiteKnowledgeProposalKind
	GapID              string
	TargetKnowledgeKey string
	Draft              string
	SourceRefs         []string
	Rationale          string
	CreatedBy          string
	FAQRevision        *WebsiteFAQRevision
	Now                time.Time
}

type WebsiteKnowledgeProposal struct {
	ProposalID         string                       `json:"proposal_id"`
	RevisionHash       string                       `json:"revision_hash"`
	ProposalKind       WebsiteKnowledgeProposalKind `json:"proposal_kind"`
	GapID              string                       `json:"gap_id"`
	TargetKnowledgeKey string                       `json:"target_knowledge_key"`
	Draft              string                       `json:"draft"`
	SourceRefs         []string                     `json:"source_refs"`
	Rationale          string                       `json:"rationale"`
	FAQRevision        *WebsiteFAQRevision          `json:"faq_revision,omitempty"`
	Status             ProposalStatus               `json:"status"`
	CreatedBy          string                       `json:"created_by"`
	CreatedAt          time.Time                    `json:"created_at"`
	ApprovedBy         string                       `json:"approved_by,omitempty"`
	ApprovedAt         time.Time                    `json:"approved_at,omitempty"`
	PublishedBy        string                       `json:"published_by,omitempty"`
	PublishedAt        time.Time                    `json:"published_at,omitempty"`
}

func NewWebsiteKnowledgeProposal(input WebsiteKnowledgeProposalInput) (WebsiteKnowledgeProposal, error) {
	target := strings.TrimSpace(input.TargetKnowledgeKey)
	draft := strings.TrimSpace(input.Draft)
	creator := strings.TrimSpace(input.CreatedBy)
	sources := sanitizeGapSources(input.SourceRefs, protocol.AdvisorTierCustomer)
	kind := normalizeWebsiteKnowledgeProposalKind(input.Kind)
	if kind == "" || strings.TrimSpace(input.GapID) == "" || draft == "" || creator == "" || len(sources) == 0 || !validWebsiteKnowledgeKey(target) {
		return WebsiteKnowledgeProposal{}, errors.New("website knowledge proposal is invalid")
	}
	var faqRevision *WebsiteFAQRevision
	if kind != ProposalKindKnowledgeDocument {
		normalized, err := normalizeWebsiteFAQProposalRevision(input.FAQRevision)
		if err != nil {
			return WebsiteKnowledgeProposal{}, err
		}
		faqRevision = &normalized
	} else if input.FAQRevision != nil {
		return WebsiteKnowledgeProposal{}, errors.New("website knowledge document proposal cannot contain an FAQ revision")
	}
	when := input.Now.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	canonical, _ := json.Marshal([]any{kind, input.GapID, target, draft, sources, strings.TrimSpace(input.Rationale), faqRevision})
	revisionHash := hashWebsiteGapValue("proposal-revision", string(canonical))
	if faqRevision != nil {
		faqRevision.RevisionHash = revisionHash
	}
	return WebsiteKnowledgeProposal{
		ProposalID: "wkp_" + revisionHash[:24], RevisionHash: revisionHash, GapID: strings.TrimSpace(input.GapID),
		ProposalKind: kind, TargetKnowledgeKey: target, Draft: draft, SourceRefs: sources, Rationale: strings.TrimSpace(input.Rationale), FAQRevision: faqRevision,
		Status: ProposalStatusDraft, CreatedBy: creator, CreatedAt: when,
	}, nil
}

func normalizeWebsiteKnowledgeProposalKind(kind WebsiteKnowledgeProposalKind) WebsiteKnowledgeProposalKind {
	if kind == "" {
		return ProposalKindKnowledgeDocument
	}
	switch kind {
	case ProposalKindKnowledgeDocument, ProposalKindFAQEntry, ProposalKindFAQAlias, ProposalKindFAQSuggestionOrder, ProposalKindFAQDemotion:
		return kind
	default:
		return ""
	}
}

func normalizeWebsiteFAQProposalRevision(input *WebsiteFAQRevision) (WebsiteFAQRevision, error) {
	if input == nil || input.SuggestionRank < 0 || input.SuggestionRank > 1000 {
		return WebsiteFAQRevision{}, errors.New("website FAQ proposal revision is invalid")
	}
	value := *input
	value.RevisionHash = "pending-review"
	value.Entries = append([]WebsiteFAQRevisionEntry(nil), input.Entries...)
	for index := range value.Entries {
		entry := &value.Entries[index]
		entry.Exact = append([]string(nil), entry.Exact...)
		entry.Patterns = append([]string(nil), entry.Patterns...)
		entry.Sources = append([]string(nil), entry.Sources...)
		for _, source := range entry.Sources {
			if !strings.HasPrefix(source, "minviabrain://08-Wiki/Public/Orrery/") || strings.Contains(source, "..") || strings.Contains(source, "\\") {
				return WebsiteFAQRevision{}, errors.New("website FAQ proposal uses a non-public source")
			}
		}
	}
	if _, err := standardAnswersFromFAQRevision(value); err != nil {
		return WebsiteFAQRevision{}, err
	}
	value.RevisionHash = ""
	return value, nil
}

func (p WebsiteKnowledgeProposal) Approve(expectedHash, reviewer string, when time.Time) (WebsiteKnowledgeProposal, error) {
	if p.Status != ProposalStatusDraft || strings.TrimSpace(expectedHash) != p.RevisionHash || strings.TrimSpace(reviewer) == "" {
		return p, errors.New("website knowledge proposal revision cannot be approved")
	}
	p.Status, p.ApprovedBy, p.ApprovedAt = ProposalStatusApproved, strings.TrimSpace(reviewer), when.UTC()
	return p, nil
}

func (p WebsiteKnowledgeProposal) Publish(expectedHash, publisher string, when time.Time) (WebsiteKnowledgeProposal, error) {
	if p.Status != ProposalStatusApproved || strings.TrimSpace(expectedHash) != p.RevisionHash || strings.TrimSpace(publisher) == "" {
		return p, errors.New("website knowledge proposal revision cannot be published")
	}
	p.Status, p.PublishedBy, p.PublishedAt = ProposalStatusPublished, strings.TrimSpace(publisher), when.UTC()
	return p, nil
}

func validWebsiteKnowledgeKey(value string) bool {
	if strings.Contains(value, "..") || strings.Contains(value, "\\") {
		return false
	}
	return strings.HasPrefix(value, "08-Wiki/Public/Orrery/") || strings.HasPrefix(value, "08-Wiki/Customer/Orrery/")
}

func gapID(topic string, tier protocol.AdvisorTier) string {
	return fmt.Sprintf("wkg_%s", hashWebsiteGapValue("aggregate", string(tier)+"\x00"+topic)[:24])
}

func (h *Handler) recordWebsiteGap(ctx context.Context, sessionID, topic string, tier protocol.AdvisorTier, route, matchedIntent string, outcome GapOutcome, score float64, sources []string, takeover bool) {
	if h == nil || h.gapStore == nil {
		return
	}
	h.mu.Lock()
	session := h.sessions[sessionID]
	conversationID := ""
	if session != nil {
		conversationID = session.conversationID
	}
	h.mu.Unlock()
	event, err := NewWebsiteGapEvent(WebsiteGapEventInput{
		ConversationID: conversationID, Topic: topic, Tier: tier, Route: route, RetrievalScore: score,
		MatchedIntent: matchedIntent, SourceIDs: sources, Outcome: outcome, Takeover: takeover, OccurredAt: h.now().UTC(),
	})
	if err != nil {
		return
	}
	h.mu.Lock()
	if current := h.sessions[sessionID]; current != nil {
		copyEvent := event
		current.lastGap = &copyEvent
	}
	h.mu.Unlock()
	_, _ = h.gapStore.RecordGap(ctx, event)
}

func (h *Handler) recordWebsiteLeadTransfer(ctx context.Context, sessionID, fallbackTopic string) {
	if h == nil || h.gapStore == nil {
		return
	}
	h.mu.Lock()
	session := h.sessions[sessionID]
	if session == nil || session.lastGap == nil {
		tier := protocol.AdvisorTierPublic
		if session != nil {
			tier = session.advisorTier
		}
		h.mu.Unlock()
		h.recordWebsiteGap(ctx, sessionID, fallbackTopic, tier, "marketing_lead", "", GapOutcomeResolved, 0, nil, true)
		return
	}
	event := *session.lastGap
	event.Route = "marketing_lead"
	event.Outcome = GapOutcomeResolved
	event.Takeover = true
	event.OccurredAt = h.now().UTC()
	copyEvent := event
	session.lastGap = &copyEvent
	h.mu.Unlock()
	_, _ = h.gapStore.RecordGap(ctx, event)
}

type websiteFeedbackRequest struct {
	Outcome GapFeedback `json:"outcome"`
}

func (h *Handler) submitFeedback(w http.ResponseWriter, r *http.Request, sessionID string) {
	if h.gapStore == nil {
		writeError(w, http.StatusServiceUnavailable, "advisor_feedback_unavailable", "Advisor feedback is temporarily unavailable.")
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var request websiteFeedbackRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || normalizeGapFeedback(request.Outcome) == "" {
		writeError(w, http.StatusBadRequest, "advisor_feedback_invalid", "Feedback outcome must be resolved or unresolved.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "advisor_feedback_invalid", "Feedback outcome must be resolved or unresolved.")
		return
	}
	if _, status, code := h.authorizeSession(sessionID, advisorSessionToken(r)); status != 0 {
		writeError(w, status, code, "Advisor session is not authorized.")
		return
	}
	h.mu.Lock()
	session := h.sessions[sessionID]
	if session == nil || session.lastGap == nil {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, "advisor_feedback_target_missing", "There is no advisor answer to rate.")
		return
	}
	event := *session.lastGap
	event.Feedback = request.Outcome
	humanVerificationRequired := false
	takeoverRequired := false
	if request.Outcome == GapFeedbackUnresolved {
		event.Outcome = GapOutcomeUnresolved
		if event.Route == "standard_answer" && event.MatchedIntent != "" {
			session.standardAnswerBypass = event.MatchedIntent
			takeoverRequired = true
		}
		if session.advisorTier != protocol.AdvisorTierCustomer && !session.humanVerifiedUntil.After(h.now().UTC()) {
			humanVerificationRequired = true
			if session.unresolvedCount < 2 {
				session.unresolvedCount = 2
			}
		}
	} else if session.standardAnswerBypass == event.MatchedIntent {
		session.standardAnswerBypass = ""
	}
	copyEvent := event
	session.lastGap = &copyEvent
	h.mu.Unlock()
	if _, err := h.gapStore.RecordGap(r.Context(), event); err != nil {
		writeError(w, http.StatusServiceUnavailable, "advisor_feedback_unavailable", "Advisor feedback is temporarily unavailable.")
		return
	}
	if h.distillationStore != nil {
		_ = h.distillationStore.RecordDistillationFeedback(r.Context(), event.TopicFingerprint, event.ConversationFingerprint, request.Outcome)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"outcome": request.Outcome, "human_verification_required": humanVerificationRequired, "takeover_required": takeoverRequired,
	})
}

func feedbackSessionID(path string) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "advisor" || parts[2] != "sessions" || parts[4] != "feedback" || !validWebsiteOpaqueClaim(parts[3]) {
		return "", false
	}
	return parts[3], true
}

func outcomeFromRecordedEvents(events [][]byte) GapOutcome {
	outcome := GapOutcomeUnavailable
	for _, raw := range events {
		var payload struct {
			Type       string `json:"type"`
			Resolution string `json:"resolution"`
		}
		if json.Unmarshal(raw, &payload) != nil {
			continue
		}
		if payload.Type == string(protocol.EventTurnCompleted) {
			if payload.Resolution == "responded" || payload.Resolution == "resolved" {
				outcome = GapOutcomeResolved
			} else {
				outcome = normalizeGapOutcome(GapOutcome(payload.Resolution))
			}
		}
	}
	return outcome
}

func sourcesFromRecordedEvents(events [][]byte) []string {
	var sources []string
	for _, raw := range events {
		var payload struct {
			Sources []string `json:"sources"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			sources = append(sources, payload.Sources...)
		}
	}
	return sources
}

func routeFromRecordedEvents(events [][]byte, fallback string) string {
	route := fallback
	for _, raw := range events {
		var payload struct {
			Type  string `json:"type"`
			Route string `json:"route"`
		}
		if json.Unmarshal(raw, &payload) == nil && payload.Type == string(protocol.EventTurnCompleted) && strings.TrimSpace(payload.Route) != "" {
			route = strings.TrimSpace(payload.Route)
		}
	}
	return route
}
