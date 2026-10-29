package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

func TestMigrateCanonicalAgentKnowledgeNamesRenamesOnlyLegacyOwnedBases(t *testing.T) {
	at := time.Date(2026, 8, 27, 13, 0, 0, 0, time.UTC)
	profiles := canonicalKnowledgeProfileDirectoryFake{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"A000001": {ID: "A000001", DisplayName: "贴饼", PersonalKnowledgeBaseID: "kb-1"},
		"A000016": {ID: "A000016", DisplayName: "曲奇", PersonalKnowledgeBaseID: "kb-2"},
		"A000017": {ID: "A000017", DisplayName: "钢镚", PersonalKnowledgeBaseID: "kb-custom"},
	}}
	knowledge := &canonicalKnowledgeStoreFake{bases: map[knowledgecore.KnowledgeBaseID]knowledgecore.KnowledgeBase{
		"kb-1":      {ID: "kb-1", Name: "钢镚 个人知识库", OwnerType: knowledgecore.OwnerAgent, OwnerID: "A000001"},
		"kb-2":      {ID: "kb-2", Name: "A000016 default knowledge", OwnerType: knowledgecore.OwnerAgent, OwnerID: "A000016"},
		"kb-custom": {ID: "kb-custom", Name: "客户成功手册", OwnerType: knowledgecore.OwnerAgent, OwnerID: "A000017"},
	}}

	report, err := migrateCanonicalAgentKnowledgeNames(context.Background(), profiles, knowledge, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	if report.Renamed != 2 {
		t.Fatalf("report = %+v", report)
	}
	if got := knowledge.bases["kb-1"].Name; got != "贴饼 个人知识库" {
		t.Fatalf("A000001 knowledge name = %q", got)
	}
	if got := knowledge.bases["kb-2"].Name; got != "曲奇 个人知识库" {
		t.Fatalf("A000016 knowledge name = %q", got)
	}
	if got := knowledge.bases["kb-custom"].Name; got != "客户成功手册" {
		t.Fatalf("custom knowledge name = %q", got)
	}
}

type canonicalKnowledgeProfileDirectoryFake struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func (f canonicalKnowledgeProfileDirectoryFake) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	page := agentcore.AgentDirectoryPage{Total: len(f.profiles)}
	for id := range f.profiles {
		page.Entries = append(page.Entries, agentcore.AgentDirectoryEntry{AgentProfileID: id})
	}
	return page, nil
}

func (f canonicalKnowledgeProfileDirectoryFake) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return f.profiles[id], nil
}

type canonicalKnowledgeStoreFake struct {
	bases map[knowledgecore.KnowledgeBaseID]knowledgecore.KnowledgeBase
}

func (f *canonicalKnowledgeStoreFake) GetKnowledgeBase(_ context.Context, id knowledgecore.KnowledgeBaseID) (knowledgecore.KnowledgeBase, error) {
	base, ok := f.bases[id]
	if !ok {
		return knowledgecore.KnowledgeBase{}, knowledgecore.ErrNotFound
	}
	return base, nil
}

func (f *canonicalKnowledgeStoreFake) SaveKnowledgeBase(_ context.Context, base knowledgecore.KnowledgeBase) error {
	f.bases[base.ID] = base
	return nil
}
