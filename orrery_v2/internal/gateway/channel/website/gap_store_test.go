package website

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform/db"
)

func TestSQLWebsiteGapStoreDeduplicatesConversationsAndAppliesTierThresholds(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)

	for index := 0; index < websitePublicGapThreshold; index++ {
		event, err := NewWebsiteGapEvent(WebsiteGapEventInput{
			ConversationID: "public-conversation-" + string(rune('a'+index)), Topic: "How does Orrery deployment work?",
			Tier: protocol.AdvisorTierPublic, Route: "clarification", Outcome: GapOutcomeUnresolved, OccurredAt: now.Add(time.Duration(index) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.RecordGap(ctx, event); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.RecordGap(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	public, err := repository.ListKnowledgeGaps(ctx, 1, now.Add(time.Hour))
	if err != nil || len(public) != 1 || public[0].ConversationCount != websitePublicGapThreshold || !public[0].Candidate {
		t.Fatalf("public=%+v err=%v", public, err)
	}

	for index := 0; index < websiteCustomerGapThreshold; index++ {
		event, _ := NewWebsiteGapEvent(WebsiteGapEventInput{
			ConversationID: "customer-conversation-" + string(rune('a'+index)), Topic: "Which private deployment controls are supported?",
			Tier: protocol.AdvisorTierCustomer, Route: "customer_agent", Outcome: GapOutcomeUnresolved, Takeover: true, OccurredAt: now,
		})
		if _, err := repository.RecordGap(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	all, err := repository.ListKnowledgeGaps(ctx, 1, now.Add(time.Hour))
	if err != nil || len(all) != 2 || !all[0].Candidate || !all[1].Candidate {
		t.Fatalf("all=%+v err=%v", all, err)
	}
}

func TestWebsiteGapMigrationHasNoRawConversationContactOrTenantColumns(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	rows, err := store.Query(ctx, `PRAGMA table_info(website_knowledge_gap_events)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, fieldType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &fieldType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"raw", "message", "conversation_id", "contact", "credential", "token", "tenant"} {
			if strings.Contains(strings.ToLower(name), forbidden) {
				t.Fatalf("unsafe gap event column %q", name)
			}
		}
	}
}

func TestSQLWebsiteGapStoreRollsBackRegressedRuleAndGovernsProposalReceipts(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	revision, _ := NewAutomaticRuleRevision(AutomaticRuleRevisionInput{
		Kind: RuleKindAlias, After: "private deployment", ApprovedAnswerRevision: "faq-v4",
		EvidenceWindow: "7d", AuthoringJob: "curation-1", MetricsSnapshot: `{"resolved":10}`, Now: now,
	})
	if err := repository.SaveRuleRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := repository.EvaluateRuleRevision(ctx, revision.RevisionID, false, 0.8, 0.9, now.Add(time.Hour))
	if err != nil || rolledBack.RolledBackAt.IsZero() || rolledBack.RollbackReason != "source_validity_regression" {
		t.Fatalf("rolledBack=%+v err=%v", rolledBack, err)
	}

	proposal, err := repository.CreateProposal(ctx, WebsiteKnowledgeProposalInput{
		GapID: "gap-1", TargetKnowledgeKey: "08-Wiki/Customer/Orrery/deployment.md", Draft: "Reviewed deployment statement.",
		SourceRefs: []string{"minviabrain://08-Wiki/Customer/Orrery/README.md"}, Rationale: "Three customer conversations",
		CreatedBy: "website-knowledge-curator", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApproveProposal(ctx, proposal.ProposalID, "wrong", "reviewer", now.Add(time.Minute)); err == nil {
		t.Fatal("wrong proposal hash approved")
	}
	approved, err := repository.ApproveProposal(ctx, proposal.ProposalID, proposal.RevisionHash, "reviewer", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	published, err := repository.PublishProposal(ctx, proposal.ProposalID, approved.RevisionHash, "publisher", now.Add(2*time.Minute))
	if err != nil || published.Status != ProposalStatusPublished {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	for table, want := range map[string]int{"website_knowledge_approval_receipts": 1, "website_knowledge_publication_receipts": 1} {
		var count int
		if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

func TestWebsiteFAQCatalogMigrationIsRegisteredSeparately(t *testing.T) {
	migration := websiteFAQCatalogMigration()
	if migration.Version != "0190_website_faq_catalog" || len(migration.Statements) < 3 || len(migration.StatementsByDialect[db.DialectPostgres]) < 3 {
		t.Fatalf("migration = %+v", migration)
	}
}

func TestSQLWebsiteGapStorePersistsAndListsGovernedFAQRevisions(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)

	first := createFAQProposal(t, ctx, repository, now, "Initial reviewed answer.", 20)
	if _, err := repository.ApproveProposal(ctx, first.ProposalID, "wrong", "reviewer", now.Add(time.Minute)); err == nil {
		t.Fatal("wrong FAQ revision hash approved")
	}
	approved, err := repository.ApproveProposal(ctx, first.ProposalID, first.RevisionHash, "reviewer", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PublishProposal(ctx, first.ProposalID, approved.RevisionHash, "publisher", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	second := createFAQProposal(t, ctx, repository, now.Add(3*time.Minute), "Latest reviewed answer.", 5)
	approved, err = repository.ApproveProposal(ctx, second.ProposalID, second.RevisionHash, "reviewer", now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PublishProposal(ctx, second.ProposalID, approved.RevisionHash, "publisher", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	revisions, err := repository.ListPublishedFAQRevisions(ctx)
	if err != nil || len(revisions) != 1 || revisions[0].RevisionHash != second.RevisionHash || revisions[0].Entries[0].Answer != "Latest reviewed answer." {
		t.Fatalf("revisions=%+v err=%v", revisions, err)
	}
	proposals, err := repository.ListWebsiteKnowledgeProposals(ctx, ProposalStatusPublished, 1)
	if err != nil || len(proposals) != 1 || proposals[0].ProposalID != second.ProposalID || proposals[0].FAQRevision == nil {
		t.Fatalf("proposals=%+v err=%v", proposals, err)
	}
	detail, err := repository.GetWebsiteKnowledgeProposal(ctx, first.ProposalID)
	if err != nil || detail.ProposalKind != ProposalKindFAQEntry {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	receipts, err := repository.ListWebsiteKnowledgeReceipts(ctx, second.ProposalID)
	if err != nil || len(receipts) != 2 || receipts[0].Kind != WebsiteKnowledgeReceiptApproval || receipts[1].Kind != WebsiteKnowledgeReceiptPublication {
		t.Fatalf("receipts=%+v err=%v", receipts, err)
	}
}

func TestSQLWebsiteGapStoreListsSevenDayFAQMetricsWithoutRawMessages(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	for index, event := range []WebsiteGapEventInput{
		{ConversationID: "conversation-a", Topic: "raw question alpha", Tier: protocol.AdvisorTierPublic, Route: "standard_answer", MatchedIntent: "orrery.zh-CN", Outcome: GapOutcomeResolved, SourceIDs: []string{overviewSource}, OccurredAt: now.Add(-time.Hour)},
		{ConversationID: "conversation-b", Topic: "raw question beta", Tier: protocol.AdvisorTierPublic, Route: "clarification", MatchedIntent: "orrery.zh-CN", Outcome: GapOutcomeUnresolved, Takeover: true, SourceIDs: []string{overviewSource}, OccurredAt: now.Add(-2 * time.Hour)},
		{ConversationID: "conversation-old", Topic: "raw old question", Tier: protocol.AdvisorTierPublic, Route: "standard_answer", MatchedIntent: "orrery.zh-CN", Outcome: GapOutcomeResolved, OccurredAt: now.Add(-8 * 24 * time.Hour)},
	} {
		created, err := NewWebsiteGapEvent(event)
		if err != nil {
			t.Fatalf("event %d: %v", index, err)
		}
		if _, err := repository.RecordGap(ctx, created); err != nil {
			t.Fatal(err)
		}
	}
	metrics, err := repository.ListWebsiteFAQMetrics(ctx, now.Add(-7*24*time.Hour))
	if err != nil || len(metrics) != 2 {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
	encoded := fmt.Sprintf("%+v", metrics)
	for _, forbidden := range []string{"raw question", "conversation-a", "conversation-b"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("metrics leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSQLWebsiteGapStoreRollsBackFAQRevisionWhenReceiptFails(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	proposal := createFAQProposal(t, ctx, repository, now, "Answer that must roll back.", 10)
	approved, err := repository.ApproveProposal(ctx, proposal.ProposalID, proposal.RevisionHash, "reviewer", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `CREATE TRIGGER reject_faq_publication_receipt BEFORE INSERT ON website_knowledge_publication_receipts
		WHEN NEW.proposal_id = '`+approved.ProposalID+`' BEGIN SELECT RAISE(ABORT, 'forced receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PublishProposal(ctx, approved.ProposalID, approved.RevisionHash, "publisher", now.Add(2*time.Minute)); err == nil {
		t.Fatal("publication succeeded despite receipt failure")
	}
	var revisions, receipts int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM website_faq_published_revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM website_knowledge_publication_receipts WHERE proposal_id = ?`, approved.ProposalID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	detail, err := repository.GetWebsiteKnowledgeProposal(ctx, approved.ProposalID)
	if err != nil || revisions != 0 || receipts != 0 || detail.Status != ProposalStatusApproved {
		t.Fatalf("revisions=%d receipts=%d detail=%+v err=%v", revisions, receipts, detail, err)
	}
}

func createFAQProposal(t *testing.T, ctx context.Context, repository *SQLWebsiteGapStore, now time.Time, answer string, rank int) WebsiteKnowledgeProposal {
	t.Helper()
	proposal, err := repository.CreateProposal(ctx, WebsiteKnowledgeProposalInput{
		Kind: ProposalKindFAQEntry, GapID: "gap-orrery", TargetKnowledgeKey: "08-Wiki/Public/Orrery/faq/orrery.md",
		Draft: answer, SourceRefs: []string{overviewSource}, Rationale: "Reviewed FAQ demand", CreatedBy: "website-knowledge-curator", Now: now,
		FAQRevision: &WebsiteFAQRevision{
			LogicalIntentID: "orrery", SuggestionEligible: true, SuggestionRank: rank,
			Entries: []WebsiteFAQRevisionEntry{
				{Locale: "zh-CN", CanonicalQuestion: "Orrery 是什么", Answer: answer, Sources: []string{overviewSource}, Class: standardAnswerFactual},
				{Locale: "en-US", CanonicalQuestion: "what is orrery", Answer: "Reviewed English answer.", Sources: []string{overviewSource}, Class: standardAnswerFactual},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func openWebsiteGapTestStore(t *testing.T, ctx context.Context) *db.Store {
	t.Helper()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, statement := range websiteGapMigrationStatements("INTEGER") {
		if _, err := store.Exec(ctx, statement); err != nil {
			t.Fatalf("migration: %v", err)
		}
	}
	for _, statement := range websiteFAQCatalogMigrationStatements("INTEGER") {
		if _, err := store.Exec(ctx, statement); err != nil {
			t.Fatalf("FAQ migration: %v", err)
		}
	}
	for _, statement := range websiteDistillationMigrationStatements("INTEGER") {
		if _, err := store.Exec(ctx, statement); err != nil {
			t.Fatalf("distillation migration: %v", err)
		}
	}
	return store
}
