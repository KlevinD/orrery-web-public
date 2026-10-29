package website

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

func TestSQLWebsiteDistillationStoreAggregatesIndependentConversations(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	var first WebsiteDistillationCandidate
	for index := 0; index < 5; index++ {
		candidate, err := NewWebsiteDistillationCandidate(WebsiteDistillationCandidateInput{
			ConversationID: "conversation-" + string(rune('a'+index)), Question: "Orrery 如何管理智能体权限？",
			Answer: "Orrery 通过显式权限和审批边界治理智能体。", Locale: "zh-CN", Tier: protocol.AdvisorTierPublic,
			SourceIDs: []string{overviewSource}, SourceRevision: "public-rev-1", ModelRevision: "minimax-m3",
			PromptRevision: "max-public-v1", OccurredAt: now.Add(time.Duration(index) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			first = candidate
		}
		if err := repository.RecordDistillationCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		if err := repository.RecordDistillationCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.RecordDistillationFeedback(ctx, first.TopicFingerprint, first.ConversationFingerprint, GapFeedbackResolved); err != nil {
		t.Fatal(err)
	}

	clusters, err := repository.ListDistillationCandidateClusters(ctx, 5, now.Add(time.Hour))
	if err != nil || len(clusters) != 1 {
		t.Fatalf("clusters=%+v err=%v", clusters, err)
	}
	cluster := clusters[0]
	if cluster.ConversationCount != 5 || cluster.ResolvedCount != 1 || cluster.UnresolvedCount != 0 || cluster.ClusterID != first.ClusterID {
		t.Fatalf("cluster = %+v", cluster)
	}
}

func TestSQLWebsiteDistillationStoreExpiresOldCandidates(t *testing.T) {
	ctx := context.Background()
	store := openWebsiteGapTestStore(t, ctx)
	repository := NewSQLWebsiteGapStore(store)
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	for _, occurred := range []time.Time{now.Add(-31 * 24 * time.Hour), now.Add(-time.Hour)} {
		candidate, err := NewWebsiteDistillationCandidate(WebsiteDistillationCandidateInput{
			ConversationID: occurred.String(), Question: "Orrery 如何部署？", Answer: "Orrery 支持多种部署方式。",
			Locale: "zh-CN", Tier: protocol.AdvisorTierPublic, SourceIDs: []string{overviewSource}, OccurredAt: occurred,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.RecordDistillationCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := repository.DeleteExpiredDistillationCandidates(ctx, now.Add(-30*24*time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	clusters, err := repository.ListDistillationCandidateClusters(ctx, 1, now)
	if err != nil || len(clusters) != 1 || clusters[0].ConversationCount != 1 {
		t.Fatalf("clusters=%+v err=%v", clusters, err)
	}
}
