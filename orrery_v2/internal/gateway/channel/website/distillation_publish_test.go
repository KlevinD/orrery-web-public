package website

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

func TestWebsiteLowRiskPublisherPublishesEvidenceBackedAlias(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	catalog := NewWebsiteFAQCatalog(nil)
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	question := "Orrery 能作为智能体控制台吗"
	clusterID := recordPublisherCandidates(t, ctx, repository, question, 5, false, now)
	revision := catalogRevisionForTest(t, catalog, "orrery")
	for index := range revision.Entries {
		if revision.Entries[index].Locale == "zh-CN" {
			revision.Entries[index].Exact = append(revision.Entries[index].Exact, question)
		}
	}
	proposal := createPublisherProposal(t, ctx, repository, ProposalKindFAQAlias, clusterID, revision, []string{overviewSource}, now)

	published, err := (WebsiteLowRiskPublisher{Store: repository, Candidates: repository, Catalog: catalog}).PublishEligibleProposal(ctx, proposal, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != ProposalStatusPublished || published.ApprovedBy != websiteDistillationValidatorActor || published.PublishedBy != websiteDistillationPublisherActor {
		t.Fatalf("published=%+v", published)
	}
	answer, ok := catalog.Match(question, "zh-CN")
	if !ok || logicalIntentID(answer.IntentID) != "orrery" {
		t.Fatalf("published alias did not match: answer=%+v ok=%v", answer, ok)
	}
}

func TestWebsiteLowRiskPublisherRejectsUnsafeAliasChanges(t *testing.T) {
	tests := []struct {
		name       string
		question   string
		count      int
		unresolved bool
		sources    []string
		mutate     func(*WebsiteFAQRevision)
	}{
		{
			name: "answer changed", question: "Orrery 能否治理跨系统智能体", count: 5, sources: []string{overviewSource},
			mutate: func(revision *WebsiteFAQRevision) {
				revision.Entries[0].Answer = "MAX 生成的未审核事实。"
				revision.Entries[0].Exact = append(revision.Entries[0].Exact, "Orrery 能否治理跨系统智能体")
			},
		},
		{
			name: "insufficient evidence", question: "Orrery 是否支持任务治理", count: 4, sources: []string{overviewSource},
			mutate: func(revision *WebsiteFAQRevision) {
				revision.Entries[0].Exact = append(revision.Entries[0].Exact, "Orrery 是否支持任务治理")
			},
		},
		{
			name: "unresolved feedback", question: "Orrery 怎样协调数字员工", count: 5, unresolved: true, sources: []string{overviewSource},
			mutate: func(revision *WebsiteFAQRevision) {
				revision.Entries[0].Exact = append(revision.Entries[0].Exact, "Orrery 怎样协调数字员工")
			},
		},
		{
			name: "commercially sensitive question", question: "Orrery 最低价格是多少", count: 5, sources: []string{overviewSource},
			mutate: func(revision *WebsiteFAQRevision) {
				revision.Entries[0].Exact = append(revision.Entries[0].Exact, "Orrery 最低价格是多少")
			},
		},
		{
			name: "non public proposal source", question: "Orrery 如何编排数字员工", count: 5,
			sources: []string{"minviabrain://08-Wiki/Customer/Orrery/private.md"},
			mutate: func(revision *WebsiteFAQRevision) {
				revision.Entries[0].Exact = append(revision.Entries[0].Exact, "Orrery 如何编排数字员工")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := openWebsiteGapTestStore(t, ctx)
			repository := NewSQLWebsiteGapStore(store)
			catalog := NewWebsiteFAQCatalog(nil)
			now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
			clusterID := recordPublisherCandidates(t, ctx, repository, test.question, test.count, test.unresolved, now)
			revision := catalogRevisionForTest(t, catalog, "orrery")
			test.mutate(&revision)
			proposal := createPublisherProposal(t, ctx, repository, ProposalKindFAQAlias, clusterID, revision, test.sources, now)
			if _, err := (WebsiteLowRiskPublisher{Store: repository, Candidates: repository, Catalog: catalog}).PublishEligibleProposal(ctx, proposal, now.Add(time.Minute)); err == nil {
				t.Fatal("unsafe proposal was published")
			}
			detail, err := repository.GetWebsiteKnowledgeProposal(ctx, proposal.ProposalID)
			if err != nil || detail.Status != ProposalStatusDraft {
				t.Fatalf("proposal status=%+v err=%v", detail, err)
			}
		})
	}
}

func TestWebsiteLowRiskPublisherPublishesOnlySuggestionOrderingFields(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	catalog := NewWebsiteFAQCatalog(nil)
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	clusterID := recordPublisherCandidates(t, ctx, repository, "Orrery 是什么", 5, false, now)
	revision := catalogRevisionForTest(t, catalog, "orrery")
	revision.SuggestionRank = 2
	proposal := createPublisherProposal(t, ctx, repository, ProposalKindFAQSuggestionOrder, clusterID, revision, []string{overviewSource}, now)

	published, err := (WebsiteLowRiskPublisher{Store: repository, Candidates: repository, Catalog: catalog}).PublishEligibleProposal(ctx, proposal, now.Add(time.Minute))
	if err != nil || published.Status != ProposalStatusPublished {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	items := catalog.Suggestions("zh-CN", 1)
	if len(items) != 1 || items[0].ID != "orrery" {
		t.Fatalf("suggestions=%+v", items)
	}
}

func recordPublisherCandidates(t *testing.T, ctx context.Context, store *SQLWebsiteGapStore, question string, count int, unresolved bool, now time.Time) string {
	t.Helper()
	clusterID := ""
	for index := 0; index < count; index++ {
		candidate, err := NewWebsiteDistillationCandidate(WebsiteDistillationCandidateInput{
			ConversationID: "publisher-conversation-" + string(rune('a'+index)), Question: question,
			Answer: "Orrery 是企业级 AI Agent Mission Control。", Locale: "zh-CN", Tier: protocol.AdvisorTierPublic,
			SourceIDs: []string{overviewSource}, OccurredAt: now.Add(time.Duration(index) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		clusterID = candidate.ClusterID
		if err := store.RecordDistillationCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		if unresolved && index == 0 {
			if err := store.RecordDistillationFeedback(ctx, candidate.TopicFingerprint, candidate.ConversationFingerprint, GapFeedbackUnresolved); err != nil {
				t.Fatal(err)
			}
		}
	}
	return clusterID
}

func catalogRevisionForTest(t *testing.T, catalog *WebsiteFAQCatalog, logicalID string) WebsiteFAQRevision {
	t.Helper()
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
		t.Fatalf("catalog entries for %s = %+v", logicalID, revision.Entries)
	}
	return revision
}

func createPublisherProposal(t *testing.T, ctx context.Context, store *SQLWebsiteGapStore, kind WebsiteKnowledgeProposalKind, clusterID string, revision WebsiteFAQRevision, sources []string, now time.Time) WebsiteKnowledgeProposal {
	t.Helper()
	proposal, err := store.CreateProposal(ctx, WebsiteKnowledgeProposalInput{
		Kind: kind, GapID: clusterID, TargetKnowledgeKey: "08-Wiki/Public/Orrery/faq/orrery.md",
		Draft: "Bounded MAX maintenance proposal", SourceRefs: sources, Rationale: "Five source-backed conversations",
		CreatedBy: "website-knowledge-curator", FAQRevision: &revision, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}
