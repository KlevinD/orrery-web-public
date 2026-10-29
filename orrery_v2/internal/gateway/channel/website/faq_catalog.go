package website

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const baselineWebsiteFAQRevision = "baseline-70"

type WebsiteFAQSuggestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

type WebsiteFAQCatalogEntry struct {
	LogicalIntentID    string   `json:"logical_intent_id"`
	IntentID           string   `json:"intent_id"`
	Locale             string   `json:"locale"`
	CanonicalQuestion  string   `json:"canonical_question"`
	Exact              []string `json:"exact"`
	Patterns           []string `json:"patterns"`
	Answer             string   `json:"answer"`
	Sources            []string `json:"sources"`
	Class              string   `json:"class"`
	Action             string   `json:"action,omitempty"`
	SuggestionEligible bool     `json:"suggestion_eligible"`
	SuggestionRank     int      `json:"suggestion_rank,omitempty"`
	Revision           string   `json:"revision"`
}

type WebsiteFAQRevisionEntry struct {
	Locale            string              `json:"locale"`
	CanonicalQuestion string              `json:"canonical_question"`
	Exact             []string            `json:"exact"`
	Patterns          []string            `json:"patterns"`
	Answer            string              `json:"answer"`
	Sources           []string            `json:"sources"`
	Class             standardAnswerClass `json:"class"`
	Action            string              `json:"action,omitempty"`
}

type WebsiteFAQRevision struct {
	RevisionHash       string                    `json:"revision_hash"`
	LogicalIntentID    string                    `json:"logical_intent_id"`
	Entries            []WebsiteFAQRevisionEntry `json:"entries"`
	SuggestionEligible bool                      `json:"suggestion_eligible"`
	SuggestionRank     int                       `json:"suggestion_rank"`
}

type WebsiteFAQRevisionStore interface {
	ListPublishedFAQRevisions(context.Context) ([]WebsiteFAQRevision, error)
}

type websiteFAQSuggestionSetting struct {
	Eligible bool
	Rank     int
}

type WebsiteFAQCatalog struct {
	mu          sync.RWMutex
	baseline    []standardAnswer
	active      []standardAnswer
	suggestions map[string]websiteFAQSuggestionSetting
	revision    string
}

func NewWebsiteFAQCatalog(_ WebsiteFAQRevisionStore) *WebsiteFAQCatalog {
	baseline := cloneStandardAnswers(websiteStandardAnswers)
	return &WebsiteFAQCatalog{
		baseline: baseline,
		active:   cloneStandardAnswers(baseline),
		suggestions: map[string]websiteFAQSuggestionSetting{
			"orrery":                {Eligible: true, Rank: 10},
			"suitable-scenarios":    {Eligible: true, Rank: 20},
			"integration":           {Eligible: true, Rank: 30},
			"deployment":            {Eligible: true, Rank: 40},
			"permissions-approvals": {Eligible: true, Rank: 50},
			"demo":                  {Eligible: true, Rank: 60},
		},
		revision: baselineWebsiteFAQRevision,
	}
}

func (c *WebsiteFAQCatalog) Reload(ctx context.Context, store WebsiteFAQRevisionStore) error {
	if c == nil || store == nil {
		return fmt.Errorf("website FAQ revision store is not configured")
	}
	revisions, err := store.ListPublishedFAQRevisions(ctx)
	if err != nil {
		return err
	}
	active := cloneStandardAnswers(c.baseline)
	suggestions := baselineWebsiteFAQSuggestionSettings()
	hashes := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		entries, err := standardAnswersFromFAQRevision(revision)
		if err != nil {
			return err
		}
		active = replaceLogicalIntent(active, revision.LogicalIntentID, entries)
		if revision.SuggestionEligible {
			rank := revision.SuggestionRank
			if rank < 1 {
				rank = 100
			}
			suggestions[revision.LogicalIntentID] = websiteFAQSuggestionSetting{Eligible: true, Rank: rank}
		} else {
			delete(suggestions, revision.LogicalIntentID)
		}
		hashes = append(hashes, revision.RevisionHash)
	}
	if err := validateStandardAnswerCatalog(active); err != nil {
		return err
	}
	revisionID := baselineWebsiteFAQRevision
	if len(hashes) == 1 {
		revisionID = hashes[0]
	} else if len(hashes) > 1 {
		sort.Strings(hashes)
		digest := sha256.Sum256([]byte(strings.Join(hashes, "\x00")))
		revisionID = fmt.Sprintf("faq-%x", digest[:12])
	}
	c.mu.Lock()
	c.active = active
	c.suggestions = suggestions
	c.revision = revisionID
	c.mu.Unlock()
	return nil
}

func (c *WebsiteFAQCatalog) Match(message, locale string) (standardAnswer, bool) {
	if c == nil {
		return standardAnswer{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return matchStandardAnswerCatalog(c.active, message, locale)
}

func (c *WebsiteFAQCatalog) Suggestions(locale string, limit int) []WebsiteFAQSuggestion {
	if c == nil || limit < 1 {
		return []WebsiteFAQSuggestion{}
	}
	if limit > 6 {
		limit = 6
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	type rankedID struct {
		id   string
		rank int
	}
	ranked := make([]rankedID, 0, len(c.suggestions))
	for id, setting := range c.suggestions {
		if setting.Eligible {
			ranked = append(ranked, rankedID{id: id, rank: setting.Rank})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].rank != ranked[j].rank {
			return ranked[i].rank < ranked[j].rank
		}
		return ranked[i].id < ranked[j].id
	})
	preferred := normalizeStandardAnswerLocale(locale)
	items := make([]WebsiteFAQSuggestion, 0, limit)
	for _, candidate := range ranked {
		for _, answer := range c.active {
			if logicalIntentID(answer.IntentID) != candidate.id || answer.Locale != preferred || len(answer.Exact) == 0 {
				continue
			}
			items = append(items, WebsiteFAQSuggestion{ID: candidate.id, Question: answer.Exact[0]})
			break
		}
		if len(items) == limit {
			break
		}
	}
	return items
}

func (c *WebsiteFAQCatalog) Revision() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.revision
}

func (c *WebsiteFAQCatalog) Entries() []WebsiteFAQCatalogEntry {
	if c == nil {
		return []WebsiteFAQCatalogEntry{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	values := make([]WebsiteFAQCatalogEntry, 0, len(c.active))
	for _, answer := range c.active {
		logicalID := logicalIntentID(answer.IntentID)
		setting := c.suggestions[logicalID]
		canonical := ""
		if len(answer.Exact) > 0 {
			canonical = answer.Exact[0]
		}
		values = append(values, WebsiteFAQCatalogEntry{
			LogicalIntentID: logicalID, IntentID: answer.IntentID, Locale: answer.Locale,
			CanonicalQuestion: canonical, Exact: append([]string(nil), answer.Exact...), Patterns: append([]string(nil), answer.Patterns...),
			Answer: answer.Text, Sources: append([]string(nil), answer.Sources...), Class: string(answer.Class), Action: answer.Action,
			SuggestionEligible: setting.Eligible, SuggestionRank: setting.Rank, Revision: c.revision,
		})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].LogicalIntentID != values[j].LogicalIntentID {
			return values[i].LogicalIntentID < values[j].LogicalIntentID
		}
		return values[i].Locale > values[j].Locale
	})
	return values
}

func standardAnswersFromFAQRevision(revision WebsiteFAQRevision) ([]standardAnswer, error) {
	logicalID := strings.TrimSpace(revision.LogicalIntentID)
	if logicalID == "" || strings.TrimSpace(revision.RevisionHash) == "" || len(revision.Entries) != 2 {
		return nil, fmt.Errorf("website FAQ revision is invalid")
	}
	answers := make([]standardAnswer, 0, len(revision.Entries))
	seenLocales := map[string]bool{}
	for _, entry := range revision.Entries {
		if seenLocales[entry.Locale] || (entry.Locale != "zh-CN" && entry.Locale != "en-US") {
			return nil, fmt.Errorf("website FAQ revision locales are invalid")
		}
		seenLocales[entry.Locale] = true
		exact := uniqueFAQPhrases(append([]string{entry.CanonicalQuestion}, entry.Exact...))
		answers = append(answers, standardAnswer{
			IntentID: logicalID + "." + entry.Locale,
			Locale:   entry.Locale,
			Exact:    exact,
			Patterns: append([]string(nil), entry.Patterns...),
			Text:     strings.TrimSpace(entry.Answer),
			Sources:  append([]string(nil), entry.Sources...),
			Class:    entry.Class,
			Action:   strings.TrimSpace(entry.Action),
		})
	}
	if !seenLocales["zh-CN"] || !seenLocales["en-US"] {
		return nil, fmt.Errorf("website FAQ revision must contain both locales")
	}
	if err := validateStandardAnswerCatalog(answers); err != nil {
		return nil, err
	}
	return answers, nil
}

func uniqueFAQPhrases(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		normalized := normalizeStandardAnswerText(value)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, value)
	}
	return out
}

func replaceLogicalIntent(catalog []standardAnswer, logicalID string, replacement []standardAnswer) []standardAnswer {
	out := make([]standardAnswer, 0, len(catalog)-2+len(replacement))
	for _, answer := range catalog {
		if logicalIntentID(answer.IntentID) != logicalID {
			out = append(out, answer)
		}
	}
	return append(out, replacement...)
}

func logicalIntentID(intentID string) string {
	if separator := strings.LastIndex(intentID, "."); separator > 0 {
		return intentID[:separator]
	}
	return strings.TrimSpace(intentID)
}

func cloneStandardAnswers(values []standardAnswer) []standardAnswer {
	out := make([]standardAnswer, len(values))
	for index, value := range values {
		out[index] = value
		out[index].Exact = append([]string(nil), value.Exact...)
		out[index].Patterns = append([]string(nil), value.Patterns...)
		out[index].Sources = append([]string(nil), value.Sources...)
	}
	return out
}

func baselineWebsiteFAQSuggestionSettings() map[string]websiteFAQSuggestionSetting {
	return map[string]websiteFAQSuggestionSetting{
		"orrery":                {Eligible: true, Rank: 10},
		"suitable-scenarios":    {Eligible: true, Rank: 20},
		"integration":           {Eligible: true, Rank: 30},
		"deployment":            {Eligible: true, Rank: 40},
		"permissions-approvals": {Eligible: true, Rank: 50},
		"demo":                  {Eligible: true, Rank: 60},
	}
}
