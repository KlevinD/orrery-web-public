package website

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const (
	websiteDistillationValidatorActor = "system:max-distillation-validator"
	websiteDistillationPublisherActor = "system:max-distillation-publisher"
	websiteDistillationPublishMinimum = 5
)

type WebsiteLowRiskPublisher struct {
	Store      WebsiteFAQGovernanceStore
	Candidates WebsiteDistillationStore
	Catalog    *WebsiteFAQCatalog
}

func (p WebsiteLowRiskPublisher) PublishEligibleProposal(ctx context.Context, proposal WebsiteKnowledgeProposal, now time.Time) (WebsiteKnowledgeProposal, error) {
	if p.Store == nil || p.Candidates == nil || p.Catalog == nil {
		return proposal, errors.New("website low-risk publisher is not configured")
	}
	if proposal.Status != ProposalStatusDraft || proposal.FAQRevision == nil || proposal.FAQRevision.RevisionHash != proposal.RevisionHash {
		return proposal, errors.New("website low-risk proposal revision is invalid")
	}
	if proposal.ProposalKind != ProposalKindFAQAlias && proposal.ProposalKind != ProposalKindFAQSuggestionOrder {
		return proposal, errors.New("website proposal requires human review")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	current, err := websiteFAQRevisionFromCatalog(p.Catalog, proposal.FAQRevision.LogicalIntentID)
	if err != nil {
		return proposal, err
	}
	cluster, err := p.evidenceCluster(ctx, proposal, current, now)
	if err != nil {
		return proposal, err
	}
	switch proposal.ProposalKind {
	case ProposalKindFAQAlias:
		if err := validateWebsiteAliasRevision(current, *proposal.FAQRevision, cluster, p.Catalog); err != nil {
			return proposal, err
		}
	case ProposalKindFAQSuggestionOrder:
		if err := validateWebsiteSuggestionRevision(current, *proposal.FAQRevision, cluster, p.Catalog); err != nil {
			return proposal, err
		}
	}
	if err := preflightWebsiteFAQRevision(p.Catalog, *proposal.FAQRevision); err != nil {
		return proposal, err
	}
	approved, err := p.Store.ApproveProposal(ctx, proposal.ProposalID, proposal.RevisionHash, websiteDistillationValidatorActor, now)
	if err != nil {
		return proposal, err
	}
	published, err := p.Store.PublishProposal(ctx, approved.ProposalID, approved.RevisionHash, websiteDistillationPublisherActor, now)
	if err != nil {
		return approved, err
	}
	if err := p.Catalog.Reload(ctx, p.Store); err != nil {
		return published, err
	}
	return published, nil
}

func (p WebsiteLowRiskPublisher) evidenceCluster(ctx context.Context, proposal WebsiteKnowledgeProposal, current WebsiteFAQRevision, now time.Time) (WebsiteDistillationCandidateCluster, error) {
	clusters, err := p.Candidates.ListDistillationCandidateClusters(ctx, websiteDistillationPublishMinimum, now)
	if err != nil {
		return WebsiteDistillationCandidateCluster{}, err
	}
	var cluster WebsiteDistillationCandidateCluster
	for _, candidate := range clusters {
		if candidate.ClusterID == proposal.GapID {
			cluster = candidate
			break
		}
	}
	if cluster.ClusterID == "" || cluster.ConversationCount < websiteDistillationPublishMinimum || cluster.UnresolvedCount != 0 || len(cluster.SourceIDs) == 0 {
		return WebsiteDistillationCandidateCluster{}, errors.New("website low-risk proposal lacks eligible evidence")
	}
	if evaluateWebsiteCommercialPolicy(cluster.RepresentativeQuestion, cluster.RepresentativeAnswer, cluster.Locale).Decision != CommercialPolicyAllow {
		return WebsiteDistillationCandidateCluster{}, errors.New("website low-risk proposal crosses the commercial policy")
	}
	approvedSources := map[string]struct{}{}
	for _, entry := range current.Entries {
		for _, source := range entry.Sources {
			approvedSources[source] = struct{}{}
		}
	}
	proposalSources := map[string]struct{}{}
	for _, source := range proposal.SourceRefs {
		if !strings.HasPrefix(source, "minviabrain://08-Wiki/Public/Orrery/") || strings.Contains(source, "..") || strings.Contains(source, "\\") {
			return WebsiteDistillationCandidateCluster{}, errors.New("website low-risk proposal uses a non-public source")
		}
		if _, ok := approvedSources[source]; !ok {
			return WebsiteDistillationCandidateCluster{}, errors.New("website low-risk proposal changes source authority")
		}
		proposalSources[source] = struct{}{}
	}
	for _, source := range cluster.SourceIDs {
		if _, ok := approvedSources[source]; !ok {
			return WebsiteDistillationCandidateCluster{}, errors.New("website candidate source does not support the target FAQ")
		}
		if _, ok := proposalSources[source]; !ok {
			return WebsiteDistillationCandidateCluster{}, errors.New("website proposal omits candidate evidence")
		}
	}
	return cluster, nil
}

func websiteFAQRevisionFromCatalog(catalog *WebsiteFAQCatalog, logicalID string) (WebsiteFAQRevision, error) {
	logicalID = strings.TrimSpace(logicalID)
	revision := WebsiteFAQRevision{LogicalIntentID: logicalID}
	for _, entry := range catalog.Entries() {
		if entry.LogicalIntentID != logicalID {
			continue
		}
		revision.SuggestionEligible = entry.SuggestionEligible
		revision.SuggestionRank = entry.SuggestionRank
		revision.Entries = append(revision.Entries, WebsiteFAQRevisionEntry{
			Locale: entry.Locale, CanonicalQuestion: entry.CanonicalQuestion,
			Exact: append([]string(nil), entry.Exact...), Patterns: append([]string(nil), entry.Patterns...),
			Answer: entry.Answer, Sources: append([]string(nil), entry.Sources...), Class: standardAnswerClass(entry.Class), Action: entry.Action,
		})
	}
	if len(revision.Entries) != 2 {
		return WebsiteFAQRevision{}, errors.New("website low-risk proposal must target an existing bilingual FAQ")
	}
	return revision, nil
}

func validateWebsiteAliasRevision(current, proposed WebsiteFAQRevision, cluster WebsiteDistillationCandidateCluster, catalog *WebsiteFAQCatalog) error {
	if current.LogicalIntentID != proposed.LogicalIntentID || current.SuggestionEligible != proposed.SuggestionEligible || current.SuggestionRank != proposed.SuggestionRank {
		return errors.New("website alias proposal changes non-alias fields")
	}
	currentByLocale := websiteFAQEntriesByLocale(current.Entries)
	proposedByLocale := websiteFAQEntriesByLocale(proposed.Entries)
	if len(currentByLocale) != 2 || len(proposedByLocale) != 2 {
		return errors.New("website alias proposal changes locale structure")
	}
	type addition struct {
		locale  string
		value   string
		pattern bool
	}
	var additions []addition
	for locale, before := range currentByLocale {
		after, ok := proposedByLocale[locale]
		if !ok || before.CanonicalQuestion != after.CanonicalQuestion || before.Answer != after.Answer || !reflect.DeepEqual(before.Sources, after.Sources) || before.Class != after.Class || before.Action != after.Action {
			return errors.New("website alias proposal changes factual FAQ content")
		}
		exactAdditions, exactRemoved := websiteStringSetDiff(before.Exact, after.Exact)
		patternAdditions, patternsRemoved := websiteStringSetDiff(before.Patterns, after.Patterns)
		if exactRemoved || patternsRemoved {
			return errors.New("website alias proposal removes an existing rule")
		}
		for _, value := range exactAdditions {
			additions = append(additions, addition{locale: locale, value: value})
		}
		for _, value := range patternAdditions {
			additions = append(additions, addition{locale: locale, value: value, pattern: true})
		}
	}
	if len(additions) != 1 || additions[0].locale != normalizeStandardAnswerLocale(cluster.Locale) {
		return errors.New("website alias proposal must add exactly one evidence-matched rule")
	}
	added := additions[0]
	normalizedQuestion := normalizeStandardAnswerText(cluster.RepresentativeQuestion)
	if added.pattern {
		if !safeWebsiteDistillationPattern(added.value) {
			return errors.New("website alias proposal pattern is not bounded")
		}
		matched, err := regexp.MatchString(added.value, normalizedQuestion)
		if err != nil || !matched || websitePatternConflictsWithCatalog(added.value, current.LogicalIntentID, catalog) {
			return errors.New("website alias proposal pattern does not match only the target intent")
		}
	} else if normalizeStandardAnswerText(added.value) != normalizedQuestion {
		return errors.New("website alias proposal does not match its evidence question")
	}
	if existing, ok := catalog.Match(cluster.RepresentativeQuestion, cluster.Locale); ok && logicalIntentID(existing.IntentID) != current.LogicalIntentID {
		return errors.New("website alias proposal collides with another intent")
	}
	return nil
}

func validateWebsiteSuggestionRevision(current, proposed WebsiteFAQRevision, cluster WebsiteDistillationCandidateCluster, catalog *WebsiteFAQCatalog) error {
	if current.LogicalIntentID != proposed.LogicalIntentID || !reflect.DeepEqual(current.Entries, proposed.Entries) {
		return errors.New("website suggestion proposal changes FAQ content")
	}
	if current.SuggestionEligible == proposed.SuggestionEligible && current.SuggestionRank == proposed.SuggestionRank {
		return errors.New("website suggestion proposal does not change ordering")
	}
	matched, ok := catalog.Match(cluster.RepresentativeQuestion, cluster.Locale)
	if !ok || logicalIntentID(matched.IntentID) != current.LogicalIntentID {
		return errors.New("website suggestion evidence does not target the proposed intent")
	}
	return nil
}

func websiteFAQEntriesByLocale(entries []WebsiteFAQRevisionEntry) map[string]WebsiteFAQRevisionEntry {
	result := make(map[string]WebsiteFAQRevisionEntry, len(entries))
	for _, entry := range entries {
		if _, exists := result[entry.Locale]; exists {
			return map[string]WebsiteFAQRevisionEntry{}
		}
		result[entry.Locale] = entry
	}
	return result
}

func websiteStringSetDiff(before, after []string) ([]string, bool) {
	beforeSet := map[string]struct{}{}
	afterSet := map[string]struct{}{}
	for _, value := range before {
		beforeSet[value] = struct{}{}
	}
	for _, value := range after {
		afterSet[value] = struct{}{}
	}
	for value := range beforeSet {
		if _, ok := afterSet[value]; !ok {
			return nil, true
		}
	}
	additions := []string{}
	for _, value := range after {
		if _, ok := beforeSet[value]; !ok {
			additions = append(additions, value)
		}
	}
	return additions, false
}

func safeWebsiteDistillationPattern(pattern string) bool {
	if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") {
		return false
	}
	for _, broad := range []string{".*", ".+", "(?", "\\p", "\\P", "{"} {
		if strings.Contains(pattern, broad) {
			return false
		}
	}
	_, err := regexp.Compile(pattern)
	return err == nil
}

func websitePatternConflictsWithCatalog(pattern, logicalID string, catalog *WebsiteFAQCatalog) bool {
	for _, entry := range catalog.Entries() {
		if entry.LogicalIntentID == logicalID {
			continue
		}
		for _, question := range append([]string{entry.CanonicalQuestion}, entry.Exact...) {
			if matched, _ := regexp.MatchString(pattern, normalizeStandardAnswerText(question)); matched {
				return true
			}
		}
	}
	return false
}

func preflightWebsiteFAQRevision(catalog *WebsiteFAQCatalog, revision WebsiteFAQRevision) error {
	answers, err := standardAnswersFromFAQRevision(revision)
	if err != nil {
		return err
	}
	catalog.mu.RLock()
	active := cloneStandardAnswers(catalog.active)
	catalog.mu.RUnlock()
	return validateStandardAnswerCatalog(replaceLogicalIntent(active, revision.LogicalIntentID, answers))
}
