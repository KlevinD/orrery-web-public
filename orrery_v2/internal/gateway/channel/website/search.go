package website

import (
	"context"
	"strings"

	"orrery_v2/internal/gateway/protocol"
)

type WebsiteSearchResolution string

const (
	WebsiteSearchDirectAnswer       WebsiteSearchResolution = "direct_answer"
	WebsiteSearchNeedsClarification WebsiteSearchResolution = "needs_clarification"
)

type WebsiteKnowledgeSearchResult struct {
	Resolution WebsiteSearchResolution
	Answer     string
	SourceURI  string
	Revision   string
	Score      float64
}

type WebsiteKnowledgeSearch interface {
	Search(context.Context, protocol.AdvisorTier, string) (WebsiteKnowledgeSearchResult, error)
}

func validWebsiteSearchAnswer(result WebsiteKnowledgeSearchResult, tier protocol.AdvisorTier) bool {
	if result.Resolution != WebsiteSearchDirectAnswer || strings.TrimSpace(result.Answer) == "" || strings.TrimSpace(result.Revision) == "" {
		return false
	}
	source := strings.TrimSpace(result.SourceURI)
	if strings.Contains(source, "\\") || strings.Contains(source, "..") {
		return false
	}
	if strings.HasPrefix(source, "minviabrain://08-Wiki/Public/Orrery/") {
		return true
	}
	return tier == protocol.AdvisorTierCustomer && strings.HasPrefix(source, "minviabrain://08-Wiki/Customer/Orrery/")
}
