package website

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

func TestNewWebsiteGapEventStoresOnlySanitizedFingerprints(t *testing.T) {
	raw := "contact me at secret@example.com with token sk-private and tenant tenant-42"
	event, err := NewWebsiteGapEvent(WebsiteGapEventInput{
		ConversationID: "webconv-secret", Topic: raw, Tier: protocol.AdvisorTierCustomer,
		Route: "customer_agent", Outcome: GapOutcomeUnresolved,
		SourceIDs:  []string{"minviabrain://08-Wiki/Customer/Orrery/guide.md", "https://invalid.example/private"},
		OccurredAt: time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	rawJSON, _ := json.Marshal(event)
	serialized := string(rawJSON)
	for _, forbidden := range []string{raw, "secret@example.com", "sk-private", "tenant-42", "webconv-secret"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("sanitized event leaked %q: %s", forbidden, serialized)
		}
	}
	if len(event.TopicFingerprint) != 64 || len(event.ConversationFingerprint) != 64 {
		t.Fatalf("fingerprints = %q %q", event.TopicFingerprint, event.ConversationFingerprint)
	}
	if len(event.SourceIDs) != 1 || !strings.HasPrefix(event.SourceIDs[0], "minviabrain://08-Wiki/Customer/Orrery/") {
		t.Fatalf("source IDs = %+v", event.SourceIDs)
	}
}

func TestAutomaticRuleRevisionAllowsOnlyNonFactualBoundedChanges(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	valid, err := NewAutomaticRuleRevision(AutomaticRuleRevisionInput{
		Kind: RuleKindRetrievalWeight, Before: "1.0", After: "1.2", EvidenceWindow: "2026-07-20/2026-07-27",
		AuthoringJob: "job-1", MetricsSnapshot: `{"resolved":12}`, ApprovedAnswerRevision: "faq-v4", Now: now,
	})
	if err != nil || valid.RevisionHash == "" || valid.RollbackRevisionID == "" {
		t.Fatalf("revision=%+v err=%v", valid, err)
	}
	for _, input := range []AutomaticRuleRevisionInput{
		{Kind: RuleKindRetrievalWeight, Before: "1.0", After: "2.5", ApprovedAnswerRevision: "faq-v4", Now: now},
		{Kind: RuleKindAlias, Before: "", After: "new product promise", Now: now},
		{Kind: RuleKind("factual_answer"), Before: "old", After: "new", ApprovedAnswerRevision: "faq-v4", Now: now},
	} {
		if _, err := NewAutomaticRuleRevision(input); err == nil {
			t.Fatalf("expected invalid automatic rule: %+v", input)
		}
	}
}

func TestKnowledgeProposalApprovalIsBoundToExactRevisionHash(t *testing.T) {
	proposal, err := NewWebsiteKnowledgeProposal(WebsiteKnowledgeProposalInput{
		GapID: "gap-1", TargetKnowledgeKey: "08-Wiki/Public/Orrery/faq.md",
		Draft:      "Orrery supports an approved deployment mode.",
		SourceRefs: []string{"minviabrain://08-Wiki/Public/Orrery/overview.md"},
		Rationale:  "Repeated verified demand", CreatedBy: "website-knowledge-curator",
		Now: time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Status != ProposalStatusDraft || proposal.RevisionHash == "" {
		t.Fatalf("proposal = %+v", proposal)
	}
	if _, err := proposal.Approve("wrong-hash", "reviewer", proposal.CreatedAt.Add(time.Minute)); err == nil {
		t.Fatal("approval accepted a different revision hash")
	}
	approved, err := proposal.Approve(proposal.RevisionHash, "reviewer", proposal.CreatedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approved.Publish("wrong-hash", "publisher", proposal.CreatedAt.Add(2*time.Minute)); err == nil {
		t.Fatal("publication accepted a different revision hash")
	}
	published, err := approved.Publish(approved.RevisionHash, "publisher", proposal.CreatedAt.Add(2*time.Minute))
	if err != nil || published.Status != ProposalStatusPublished {
		t.Fatalf("published=%+v err=%v", published, err)
	}
}

func TestFAQProposalRequiresAValidBilingualRevision(t *testing.T) {
	input := WebsiteKnowledgeProposalInput{
		Kind:               ProposalKindFAQEntry,
		GapID:              "gap-faq-1",
		TargetKnowledgeKey: "08-Wiki/Public/Orrery/faq/orrery.md",
		Draft:              "Review the bilingual Orrery answer.",
		SourceRefs:         []string{overviewSource},
		Rationale:          "Repeated public consultation demand",
		CreatedBy:          "website-knowledge-curator",
		Now:                time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
		FAQRevision: &WebsiteFAQRevision{
			LogicalIntentID:    "orrery",
			SuggestionEligible: true,
			SuggestionRank:     10,
			Entries: []WebsiteFAQRevisionEntry{
				{Locale: "zh-CN", CanonicalQuestion: "Orrery 是什么", Exact: []string{"什么是 Orrery"}, Answer: "Orrery 是经过治理的智能体协作与执行平台。", Sources: []string{overviewSource}, Class: standardAnswerFactual},
				{Locale: "en-US", CanonicalQuestion: "what is orrery", Exact: []string{"tell me about orrery"}, Answer: "Orrery is a governed platform for agent collaboration and execution.", Sources: []string{overviewSource}, Class: standardAnswerFactual},
			},
		},
	}
	proposal, err := NewWebsiteKnowledgeProposal(input)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalKind != ProposalKindFAQEntry || proposal.FAQRevision == nil || proposal.FAQRevision.RevisionHash != proposal.RevisionHash {
		t.Fatalf("proposal = %+v", proposal)
	}

	invalid := input
	invalid.FAQRevision = &WebsiteFAQRevision{
		LogicalIntentID: "orrery",
		Entries: []WebsiteFAQRevisionEntry{
			{Locale: "zh-CN", CanonicalQuestion: "Orrery", Answer: "没有英文版本。", Sources: []string{overviewSource}, Class: standardAnswerFactual},
		},
	}
	if _, err := NewWebsiteKnowledgeProposal(invalid); err == nil {
		t.Fatal("single-locale FAQ proposal was accepted")
	}
}
