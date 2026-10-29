package website

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

const websiteDistillationRetention = 30 * 24 * time.Hour

type WebsiteDistillationCandidateInput struct {
	ConversationID string
	Question       string
	Answer         string
	Locale         string
	PagePath       string
	Tier           protocol.AdvisorTier
	SourceIDs      []string
	SourceRevision string
	ModelRevision  string
	PromptRevision string
	OccurredAt     time.Time
}

type WebsiteDistillationCandidate struct {
	CandidateID             string      `json:"candidate_id"`
	ClusterID               string      `json:"cluster_id"`
	TopicFingerprint        string      `json:"-"`
	ConversationFingerprint string      `json:"-"`
	Question                string      `json:"question"`
	Answer                  string      `json:"answer"`
	Locale                  string      `json:"locale"`
	PageCategory            string      `json:"page_category,omitempty"`
	SourceIDs               []string    `json:"source_ids"`
	SourceRevision          string      `json:"source_revision"`
	ModelRevision           string      `json:"model_revision"`
	PromptRevision          string      `json:"prompt_revision"`
	PolicyLabel             string      `json:"policy_label"`
	Feedback                GapFeedback `json:"feedback,omitempty"`
	OccurredAt              time.Time   `json:"occurred_at"`
}

type WebsiteDistillationCandidateCluster struct {
	ClusterID              string    `json:"cluster_id"`
	Locale                 string    `json:"locale"`
	RepresentativeQuestion string    `json:"representative_question"`
	RepresentativeAnswer   string    `json:"representative_answer"`
	SourceIDs              []string  `json:"source_ids"`
	SourceRevision         string    `json:"source_revision"`
	ConversationCount      int       `json:"conversation_count"`
	ResolvedCount          int       `json:"resolved_count"`
	UnresolvedCount        int       `json:"unresolved_count"`
	LastSeenAt             time.Time `json:"last_seen_at"`
}

type WebsiteDistillationStore interface {
	RecordDistillationCandidate(context.Context, WebsiteDistillationCandidate) error
	RecordDistillationFeedback(context.Context, string, string, GapFeedback) error
	ListDistillationCandidateClusters(context.Context, int, time.Time) ([]WebsiteDistillationCandidateCluster, error)
	DeleteExpiredDistillationCandidates(context.Context, time.Time) (int, error)
}

var (
	websiteEmailPattern  = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	websitePhonePattern  = regexp.MustCompile(`(?:\+?86[- ]?)?1[3-9][0-9]{9}`)
	websiteSecretPattern = regexp.MustCompile(`(?i)(?:sk-[a-z0-9_-]{8,}|bearer\s+[a-z0-9._~+/-]{8,}|(?:api[_ -]?key|access[_ -]?token|password|secret)\s*[:=]\s*[^\s]{6,})`)
	websiteIPv4Pattern   = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
)

func NewWebsiteDistillationCandidate(input WebsiteDistillationCandidateInput) (WebsiteDistillationCandidate, error) {
	if input.Tier != protocol.AdvisorTierPublic {
		return WebsiteDistillationCandidate{}, errors.New("website distillation accepts public consultations only")
	}
	conversation := strings.TrimSpace(input.ConversationID)
	question := strings.TrimSpace(input.Question)
	answer := strings.TrimSpace(input.Answer)
	if conversation == "" || question == "" || answer == "" {
		return WebsiteDistillationCandidate{}, errors.New("website distillation candidate content is required")
	}
	if websiteUnsafeDistillationText(question) || websiteUnsafeDistillationText(answer) {
		return WebsiteDistillationCandidate{}, errors.New("website distillation candidate contains unsafe content")
	}
	question = redactWebsiteContacts(question)
	answer = redactWebsiteContacts(answer)
	question = truncateRunes(question, defaultMaxMessageRunes)
	answer = truncateRunes(answer, 8192)
	sources := uniqueWebsitePublicSources(input.SourceIDs)
	if len(sources) == 0 {
		return WebsiteDistillationCandidate{}, errors.New("website distillation candidate requires approved public sources")
	}
	when := input.OccurredAt.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	locale := normalizeStandardAnswerLocale(input.Locale)
	topicFingerprint := hashWebsiteGapValue("topic", normalizeGapTopic(input.Question))
	conversationFingerprint := hashWebsiteGapValue("conversation", conversation)
	clusterHash := hashWebsiteGapValue("distillation-cluster", locale+"\x00"+normalizeGapTopic(question))
	clusterID := "wdc_" + clusterHash[:24]
	candidateHash := hashWebsiteGapValue("distillation-candidate", clusterID+"\x00"+conversationFingerprint)
	return WebsiteDistillationCandidate{
		CandidateID: "wds_" + candidateHash[:24], ClusterID: clusterID,
		TopicFingerprint: topicFingerprint, ConversationFingerprint: conversationFingerprint,
		Question: question, Answer: answer, Locale: locale, PageCategory: websitePageCategory(input.PagePath),
		SourceIDs: sources, SourceRevision: boundedDistillationLabel(input.SourceRevision, "runtime-source-gate"),
		ModelRevision:  boundedDistillationLabel(input.ModelRevision, "managed-runtime"),
		PromptRevision: boundedDistillationLabel(input.PromptRevision, "max-public"),
		PolicyLabel:    string(CommercialPolicyAllow), OccurredAt: when,
	}, nil
}

func redactWebsiteContacts(value string) string {
	value = websiteEmailPattern.ReplaceAllString(value, "[redacted]")
	return websitePhonePattern.ReplaceAllString(value, "[redacted]")
}

func websiteUnsafeDistillationText(value string) bool {
	if websiteSecretPattern.MatchString(value) {
		return true
	}
	for _, raw := range websiteIPv4Pattern.FindAllString(value, -1) {
		ip := net.ParseIP(raw)
		if ip == nil {
			continue
		}
		parts := strings.Split(raw, ".")
		if ip.IsPrivate() || ip.IsLoopback() || strings.HasPrefix(raw, "169.254.") ||
			(len(parts) == 4 && parts[0] == "100" && parseIPv4Octet(parts[1]) >= 64 && parseIPv4Octet(parts[1]) <= 127) {
			return true
		}
	}
	return false
}

func parseIPv4Octet(value string) int {
	result := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return -1
		}
		result = result*10 + int(r-'0')
	}
	return result
}

func websitePageCategory(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !strings.HasPrefix(parsed.Path, "/") {
		return ""
	}
	return truncateRunes(parsed.Path, 256)
}

func boundedDistillationLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return truncateRunes(value, 128)
}

func (h *Handler) recordWebsiteDistillationCandidate(ctx context.Context, sessionID string, request messageRequest, events [][]byte, tier protocol.AdvisorTier, route string, outcome GapOutcome) {
	if h == nil || h.distillationStore == nil || tier != protocol.AdvisorTierPublic || route != "public_agent" || outcome != GapOutcomeResolved {
		return
	}
	h.mu.Lock()
	session := h.sessions[sessionID]
	conversationID := ""
	if session != nil {
		conversationID = session.conversationID
	}
	h.mu.Unlock()
	candidate, err := NewWebsiteDistillationCandidate(WebsiteDistillationCandidateInput{
		ConversationID: conversationID,
		Question:       request.Message,
		Answer:         assistantTextFromRecordedEvents(events),
		Locale:         request.Language,
		PagePath:       request.PagePath,
		Tier:           tier,
		SourceIDs:      sourcesFromRecordedEvents(events),
		SourceRevision: "runtime-source-gate",
		ModelRevision:  "managed-runtime",
		PromptRevision: "max-public-v1",
		OccurredAt:     h.now().UTC(),
	})
	if err != nil {
		return
	}
	_ = h.distillationStore.RecordDistillationCandidate(ctx, candidate)
}
