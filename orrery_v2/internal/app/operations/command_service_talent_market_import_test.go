package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	skillres "orrery_v2/internal/resource/skill"
)

func TestCommandServicePreviewsTalentMarketImportNameConflict(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-existing": validMarketProfile("profile-existing", "测试专家"),
	}}
	service := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	preview, err := service.PreviewTalentMarketImport(ctx, viewer, operations.TalentMarketImportPreviewRequest{
		Candidates: []operations.TalentMarketImportCandidateDTO{{
			AgentProfileID: "profile-import",
			RealName:       "测试专家",
			DisplayName:    "测试专家",
			Persona: operations.WorkforcePersonaDTO{
				ResponsibilityBoundary: "负责测试策略设计并提交证据。",
			},
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("PreviewTalentMarketImport: %v", err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Status != "conflict" {
		t.Fatalf("preview items = %+v", preview.Items)
	}
	if len(preview.Conflicts) != 1 {
		t.Fatalf("preview conflicts = %+v", preview.Conflicts)
	}
	conflict := preview.Conflicts[0]
	if conflict.CandidateIndex != 0 ||
		conflict.ConflictType != "real_name" ||
		conflict.ExistingAgentProfileID != "profile-existing" ||
		conflict.ExistingPlacementStatus != "market" {
		t.Fatalf("conflict = %+v", conflict)
	}
}

func TestCommandServiceImportsTalentMarketProfileAndHirePreservesDetails(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	audit := &fakeOperationsAuditRecorder{}
	knowledge := &fakeKnowledgeLifecycle{}
	service := operations.NewCommandService(store)
	service.SetAuditRecorder(audit)
	service.SetKnowledgeLifecycle(knowledge)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage}, RequestID: "corr-import"}

	commit, err := service.CommitTalentMarketImport(ctx, viewer, operations.TalentMarketImportCommitRequest{
		Candidates: []operations.TalentMarketImportCandidateDTO{{
			AgentProfileID: "profile-researcher",
			RealName:       "研思",
			DisplayName:    "研思",
			RoleName:       "学术研究员",
			AgentKind:      "ai",
			Description:    "负责文献研究、论证和引用校验。",
			Category:       "academic",
			SourceSlug:     "academic-researcher",
			SourcePath:     "academic/academic-researcher.md",
			SourceHash:     "sha256:fixture",
			SourcePrompt:   "# Academic Researcher\n\n保持引用准确，并输出可审计证据。",
			Persona: operations.WorkforcePersonaDTO{
				Backstory:              "擅长把模糊问题拆成可验证研究问题。",
				ResponsibilityBoundary: "负责文献研究、论证和引用校验，并提交证据。",
				BehaviorBoundary:       "不得编造来源；不确定时标注假设。",
			},
			RuntimeKind:      "codex",
			RuntimeConfigRef: "runtime-codex",
			RuntimeConfig:    json.RawMessage(`{"model":"gpt-5-codex","sandbox":"workspace-write"}`),
			PersonalMemoryRef: operations.WorkforceMemoryProfileRefDTO{
				MemoryProfileID: "memory-researcher",
				MemoryKind:      "personal",
				AccessScope:     "agent",
			},
			CollectiveMemoryRefs: []operations.WorkforceMemoryProfileRefDTO{{
				MemoryProfileID: "memory-research",
				MemoryKind:      "organization",
				AccessScope:     "research",
			}},
			SkillRefs: []operations.WorkforceSkillRefDTO{{
				SkillID:           "skill.research",
				VersionConstraint: "^1.0.0",
				Alias:             "研究设计",
				Level:             "senior",
				EvidenceRefs:      []string{"evidence.citation-check"},
			}},
			ToolRefs: []operations.WorkforceToolRefDTO{{
				ToolID:            "tool.web-search",
				VersionConstraint: ">=1",
				Alias:             "Web Search",
				PermissionScope:   "web:read",
				BindingPolicy:     "approval_required",
			}},
			QAEnabled: true,
			Enabled:   true,
		}},
	})
	if err != nil {
		t.Fatalf("CommitTalentMarketImport: %v", err)
	}
	if len(commit.Imported) != 1 || commit.Imported[0].PlacementStatus != "market" {
		t.Fatalf("commit imported = %+v", commit.Imported)
	}
	if commit.Imported[0].KnowledgeBaseID == "" {
		t.Fatalf("imported member should expose personal knowledge base id: %+v", commit.Imported[0])
	}
	profile := store.profiles["profile-researcher"]
	if profile.PersonalKnowledgeBaseID == "" || profile.PersonalKnowledgeBaseID != commit.Imported[0].KnowledgeBaseID {
		t.Fatalf("personal knowledge base should be bound to profile and DTO: profile=%+v dto=%+v", profile, commit.Imported[0])
	}
	if len(knowledge.created) != 1 ||
		knowledge.created[0].cmd.OwnerType != "agent" ||
		knowledge.created[0].cmd.OwnerID != "profile-researcher" ||
		knowledge.created[0].cmd.Visibility != "private" ||
		knowledge.created[0].viewer.AgentIDs[0] != "profile-researcher" {
		t.Fatalf("personal knowledge create = %+v", knowledge.created)
	}
	if !profile.Placement.IsMarket(profile.OrgIdentity) || profile.OrgIdentity.OrganizationRef.ID != "" {
		t.Fatalf("imported profile should live only in market: placement=%+v org=%+v", profile.Placement, profile.OrgIdentity)
	}
	if profile.RoleName != "学术研究员" ||
		profile.Persona.BehaviorBoundary != "不得编造来源；不确定时标注假设。" ||
		profile.RuntimeSpec.Kind != agentcore.RuntimeKindCodex ||
		string(profile.RuntimeSpec.Config) != `{"model":"gpt-5-codex","sandbox":"workspace-write"}` ||
		len(profile.SkillRefs) != 2 ||
		!profile.QAEnabled {
		t.Fatalf("imported profile details were not preserved: %+v", profile)
	}
	assertConfiguredAgentToolAndDefaultReads(t, profile, agentcore.ToolRef{
		ToolID:            "tool.web-search",
		VersionConstraint: ">=1",
		Alias:             "Web Search",
		PermissionScope:   "web:read",
		BindingPolicy:     "approval_required",
	})
	if profile.PersonalMemoryProfileRef.OwnerAgentID != "profile-researcher" {
		t.Fatalf("personal memory owner should use profile id: %+v", profile.PersonalMemoryProfileRef)
	}
	if profile.BaseAgent.Extensions == nil || !strings.Contains(string(profile.BaseAgent.Extensions["talent_market_import"]), `"source_path":"academic/academic-researcher.md"`) {
		t.Fatalf("source metadata extension missing: %+v", profile.BaseAgent.Extensions)
	}

	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "research", OrganizationID: "orrery", Name: "Research"},
	}, []operations.PositionDTO{
		{PositionID: "researcher", DepartmentID: "research", Name: "Researcher"},
	})
	service.SetOrganizationCatalogStore(orgs)
	hired, err := service.HireTalentFromMarket(ctx, viewer, operations.HireTalentFromMarketCommand{
		AgentProfileID: "profile-researcher",
		OrganizationID: "orrery",
		DepartmentID:   "research",
		PositionID:     "researcher",
		RoleIDs:        []string{"researcher"},
	})
	if err != nil {
		t.Fatalf("HireTalentFromMarket: %v", err)
	}
	if hired.PlacementStatus != "internal" ||
		hired.Persona.Backstory != "擅长把模糊问题拆成可验证研究问题。" ||
		hired.Persona.BehaviorBoundary != "不得编造来源；不确定时标注假设。" ||
		hired.RuntimeKind != "codex" ||
		string(hired.RuntimeConfig) != `{"model":"gpt-5-codex","sandbox":"workspace-write"}` ||
		len(hired.SkillRefs) != 2 ||
		!hired.QAEnabled {
		t.Fatalf("hired member lost imported details: %+v", hired)
	}
	assertConfiguredWorkforceAgentToolAndDefaultReads(t, hired, operations.WorkforceToolRefDTO{
		ToolID:            "tool.web-search",
		VersionConstraint: ">=1",
		Alias:             "Web Search",
		PermissionScope:   "web:read",
		BindingPolicy:     "approval_required",
	})
}

func TestCommandServiceRegistersImportedAgentSkills(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	skillRegistry := skillres.NewMemoryRegistry()
	service := operations.NewCommandService(store)
	service.SetSkillRegistry(skillRegistry)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.CommitTalentMarketImport(ctx, viewer, operations.TalentMarketImportCommitRequest{
		Candidates: []operations.TalentMarketImportCandidateDTO{{
			AgentProfileID: "profile-social-ads",
			RealName:       "社交广告策略师",
			Description:    "制定社交广告投放策略、素材方向和效果复盘。",
			SourcePath:     "marketing/social-ads.md",
			SourcePrompt:   "# 社交广告策略师\n\n用中文输出策略建议。",
			Persona: operations.WorkforcePersonaDTO{
				ResponsibilityBoundary: "负责社交广告策略、素材方向和复盘建议。",
			},
			SkillRefs: []operations.WorkforceSkillRefDTO{{
				SkillID: "imported.social-ads",
				Alias:   "社交广告策略师",
				Level:   "reference",
			}},
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("CommitTalentMarketImport: %v", err)
	}

	definition, err := skillRegistry.Get(ctx, agentcore.SkillID("imported.social-ads"))
	if err != nil {
		t.Fatalf("imported skill was not registered: %v", err)
	}
	if definition.Name != "社交广告策略师" ||
		definition.Version != "1.0.0" ||
		!definition.Enabled ||
		len(definition.PromptAssets) != 1 ||
		!strings.Contains(definition.PromptAssets[0].Content, "用中文输出策略建议") {
		t.Fatalf("imported skill definition = %+v", definition)
	}
}

func TestCommandServiceRegistersPackagedSkillDefinitions(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	skillRegistry := skillres.NewMemoryRegistry()
	service := operations.NewCommandService(store)
	service.SetSkillRegistry(skillRegistry)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.CommitTalentMarketImport(ctx, viewer, operations.TalentMarketImportCommitRequest{
		Candidates: []operations.TalentMarketImportCandidateDTO{{
			AgentProfileID: "profile-social-ads",
			RealName:       "社交广告策略师",
			Persona: operations.WorkforcePersonaDTO{
				ResponsibilityBoundary: "负责社交广告策略、素材方向和复盘建议。",
			},
			SkillRefs: []operations.WorkforceSkillRefDTO{{
				SkillID: "skill.social-ads",
				Alias:   "社交广告策略",
				Level:   "senior",
			}},
			Enabled: true,
		}},
		Skills: []operations.TalentMarketSkillDefinitionDTO{{
			SkillID:     "skill.social-ads",
			Name:        "社交广告策略",
			Version:     "2.0.0",
			Description: "包内导出的社交广告策略技能。",
			PromptAssets: []skillres.PromptAsset{{
				Path:    "SKILL.md",
				Kind:    skillres.PromptAssetMarkdown,
				Content: "包内完整 skill 正文，而不是候选人 fallback prompt。",
			}},
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("CommitTalentMarketImport: %v", err)
	}

	definition, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.social-ads"))
	if err != nil {
		t.Fatalf("packaged skill was not registered: %v", err)
	}
	if definition.Version != "2.0.0" ||
		len(definition.PromptAssets) != 1 ||
		!strings.Contains(definition.PromptAssets[0].Content, "包内完整 skill 正文") {
		t.Fatalf("packaged skill definition = %+v", definition)
	}
}

func TestCommandServiceImportsPackagedSkillsIntoPackageStore(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	skillRegistry := skillres.NewMemoryRegistry()
	skillStore := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	service := operations.NewCommandService(store)
	service.SetSkillRegistry(skillRegistry)
	service.SetSkillPackageStore(skillStore)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.CommitTalentMarketImport(ctx, viewer, operations.TalentMarketImportCommitRequest{
		Candidates: []operations.TalentMarketImportCandidateDTO{{
			AgentProfileID: "profile-social-ads",
			RealName:       "社交广告策略师",
			Persona: operations.WorkforcePersonaDTO{
				ResponsibilityBoundary: "负责社交广告策略、素材方向和复盘建议。",
			},
			SkillRefs: []operations.WorkforceSkillRefDTO{{
				SkillID: "skill.social-ads.专项技能",
				Alias:   "社交广告专项技能",
				Level:   "senior",
			}},
			Enabled: true,
		}},
		Skills: []operations.TalentMarketSkillDefinitionDTO{{
			SkillID:     "skill.social-ads.专项技能",
			Name:        "社交广告专项技能",
			Version:     "2.0.0",
			Description: "包内导出的社交广告策略技能。",
			PromptAssets: []skillres.PromptAsset{{
				Path:    "SKILL.md",
				Kind:    skillres.PromptAssetMarkdown,
				Content: "# 社交广告专项技能\n\n包内完整 skill 正文，而不是候选人 fallback prompt。",
			}},
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("CommitTalentMarketImport: %v", err)
	}

	definition, err := skillRegistry.Get(ctx, agentcore.SkillID("skill.social-ads.专项技能"))
	if err != nil {
		t.Fatalf("packaged skill was not registered: %v", err)
	}
	if definition.ID != agentcore.SkillID("skill.social-ads.专项技能") {
		t.Fatalf("registered skill id = %q", definition.ID)
	}
	if _, err := skillStore.BuildFileTree(ctx, agentcore.SkillID("skill.social-ads.专项技能")); err != nil {
		t.Fatalf("skill package was not written: %v", err)
	}
	skillDir, err := skillStore.SkillDirectory(agentcore.SkillID("skill.social-ads.专项技能"))
	if err != nil {
		t.Fatalf("skill package dir: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read package SKILL.md: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "skill_id: skill.social-ads.专项技能") ||
		!strings.Contains(content, "包内完整 skill 正文") {
		t.Fatalf("package SKILL.md content = %s", content)
	}
}

func TestCommandServiceDoesNotBackfillImportedAgentSkillsFromExistingProfiles(t *testing.T) {
	ctx := context.Background()
	profile := validInternalProfile("profile-social-ads", "社交广告策略师")
	profile.RoleName = "制定社交广告投放策略、素材方向和效果复盘。"
	profile.Persona.Backstory = "# 社交广告策略师\n\n用中文输出策略建议。"
	profile.SkillRefs = []agentcore.SkillRef{{
		SkillID: "imported.social-ads",
		Alias:   "社交广告策略师",
		Level:   "reference",
	}}
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		profile.ID: profile,
	}}
	skillRegistry := skillres.NewMemoryRegistry()
	service := operations.NewCommandService(store)
	service.SetSkillRegistry(skillRegistry)

	if err := service.EnsureImportedAgentSkills(ctx); err == nil {
		t.Fatalf("EnsureImportedAgentSkills should reject legacy profile backfill")
	}
	if _, err := skillRegistry.Get(ctx, agentcore.SkillID("imported.social-ads")); !errors.Is(err, skillres.ErrNotFound) {
		t.Fatalf("legacy imported skill should not be backfilled, err=%v", err)
	}
}

func TestCommandServiceCommitsTalentMarketImportConflictResolutions(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-existing": validMarketProfile("profile-existing", "同名专家"),
	}}
	service := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	commit, err := service.CommitTalentMarketImport(ctx, viewer, operations.TalentMarketImportCommitRequest{
		Candidates: []operations.TalentMarketImportCandidateDTO{
			{
				AgentProfileID:     "profile-overwrite-request",
				RealName:           "同名专家",
				DisplayName:        "同名专家",
				RoleName:           "覆盖后的专家",
				Persona:            operations.WorkforcePersonaDTO{ResponsibilityBoundary: "负责覆盖后的专家工作并提交证据。"},
				Enabled:            true,
				ConflictResolution: "overwrite",
				RuntimeKind:        "codex",
			},
			{
				AgentProfileID:     "profile-renamed",
				RealName:           "同名专家",
				DisplayName:        "同名专家",
				RoleName:           "改名后的专家",
				Persona:            operations.WorkforcePersonaDTO{ResponsibilityBoundary: "负责改名后的专家工作并提交证据。"},
				Enabled:            true,
				ConflictResolution: "rename",
				ResolvedRealName:   "同名专家二号",
			},
			{
				AgentProfileID:     "profile-skipped",
				RealName:           "跳过专家",
				Persona:            operations.WorkforcePersonaDTO{ResponsibilityBoundary: "负责跳过导入测试。"},
				ConflictResolution: "skip",
			},
		},
	})
	if err != nil {
		t.Fatalf("CommitTalentMarketImport: %v", err)
	}
	if len(commit.Imported) != 2 || len(commit.Skipped) != 1 {
		t.Fatalf("commit response = %+v", commit)
	}
	if profile := store.profiles["profile-existing"]; profile.RoleName != "覆盖后的专家" || profile.RuntimeSpec.Kind != agentcore.RuntimeKindCodex {
		t.Fatalf("overwrite should update existing market profile: %+v", profile)
	}
	if profile := store.profiles["profile-renamed"]; profile.RealName != "同名专家二号" || profile.RoleName != "改名后的专家" {
		t.Fatalf("rename should create renamed profile: %+v", profile)
	}
	if _, ok := store.profiles["profile-skipped"]; ok {
		t.Fatalf("skip should not create profile-skipped")
	}
}

func TestCommandServiceExportsSelectedTalentMarketProfiles(t *testing.T) {
	ctx := context.Background()
	marketProfile := validMarketProfile("profile-market", "市场专家")
	marketProfile.RoleName = "市场分析师"
	marketProfile.Persona.BehaviorBoundary = "仅导出人才市场成员。"
	marketProfile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex, ConfigRef: "runtime-codex", Config: json.RawMessage(`{"model":"codex"}`)}
	marketProfile.SkillRefs = []agentcore.SkillRef{{SkillID: "skill.market", Alias: "市场分析"}}
	marketProfile.ToolRefs = []agentcore.ToolRef{
		{ToolID: "tool.sheet", Alias: "Sheet"},
		knowledgeSearchReadToolRef(),
	}
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-market":   marketProfile,
		"profile-internal": validInternalProfile("profile-internal", "内部专家"),
	}}
	skillRegistry := skillres.NewMemoryRegistry()
	if err := skillRegistry.Register(ctx, skillres.SkillDefinition{
		ID:          agentcore.SkillID("skill.market"),
		Name:        "市场分析",
		Version:     "1.0.0",
		Description: "市场分析方法和输出规范。",
		PromptAssets: []skillres.PromptAsset{{
			Path:    "SKILL.md",
			Kind:    skillres.PromptAssetMarkdown,
			Content: "市场分析 skill 完整提示词。",
		}},
		RequiredTools: []agentcore.ToolRef{{
			ToolID:            agentcore.ToolID("tool.sheet"),
			VersionConstraint: ">=1.0.0",
			Alias:             "Sheet",
		}},
		EvidenceRefs: []agentcore.EvidenceRef{{ID: "evidence.market"}},
		Enabled:      true,
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	service := operations.NewCommandService(store)
	service.SetSkillRegistry(skillRegistry)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceRead}}

	pkg, err := service.ExportTalentMarket(ctx, viewer, operations.TalentMarketExportRequest{AgentProfileIDs: []string{"profile-market"}})
	if err != nil {
		t.Fatalf("ExportTalentMarket: %v", err)
	}
	if pkg.Manifest.Format != "orrery.talent-market" || pkg.Manifest.Version != 1 || pkg.Manifest.Count != 1 {
		t.Fatalf("manifest = %+v", pkg.Manifest)
	}
	if len(pkg.Agents) != 1 {
		t.Fatalf("agents = %+v", pkg.Agents)
	}
	if len(pkg.Skills) != 1 ||
		pkg.Skills[0].SkillID != "skill.market" ||
		len(pkg.Skills[0].RequiredTools) != 1 ||
		pkg.Skills[0].RequiredTools[0].ToolID != "tool.sheet" ||
		len(pkg.Skills[0].EvidenceRefs) != 1 ||
		pkg.Skills[0].EvidenceRefs[0] != "evidence.market" ||
		!strings.Contains(pkg.Skills[0].PromptAssets[0].Content, "市场分析 skill 完整提示词") {
		t.Fatalf("exported skills = %+v", pkg.Skills)
	}
	agent := pkg.Agents[0]
	if agent.AgentProfileID != "profile-market" ||
		agent.RealName != "市场专家" ||
		agent.RoleName != "市场分析师" ||
		agent.Persona.BehaviorBoundary != "仅导出人才市场成员。" ||
		agent.RuntimeKind != "codex" ||
		string(agent.RuntimeConfig) != `{"model":"codex"}` ||
		len(agent.SkillRefs) != 1 {
		t.Fatalf("exported agent = %+v", agent)
	}
	assertConfiguredWorkforceToolAndKnowledgeRead(t, agent.ToolRefs, operations.WorkforceToolRefDTO{ToolID: "tool.sheet", Alias: "Sheet"})
}

func TestCommandServiceDeletesTalentMarketProfilePhysically(t *testing.T) {
	ctx := context.Background()
	marketProfile := validMarketProfile("profile-market", "市场专家")
	marketProfile.PersonalKnowledgeBaseID = "kb-personal-market"
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-market":   marketProfile,
		"profile-internal": validInternalProfile("profile-internal", "内部专家"),
	}}
	knowledge := &fakeKnowledgeLifecycle{}
	service := operations.NewCommandService(store)
	service.SetKnowledgeLifecycle(knowledge)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	if err := service.DeleteTalentMarketMember(ctx, viewer, operations.DeleteTalentMarketMemberCommand{
		AgentProfileID: "profile-market",
		Reason:         "cleanup imported agent",
	}); err != nil {
		t.Fatalf("DeleteTalentMarketMember: %v", err)
	}
	if _, ok := store.profiles["profile-market"]; ok {
		t.Fatalf("market profile should be physically deleted")
	}
	if !store.deleted["profile-market"] {
		t.Fatalf("market profile delete port was not called")
	}
	if len(knowledge.deleted) != 1 || knowledge.deleted[0].id != "kb-personal-market" || knowledge.deleted[0].viewer.AgentIDs[0] != "profile-market" {
		t.Fatalf("personal knowledge delete = %+v", knowledge.deleted)
	}

	err := service.DeleteTalentMarketMember(ctx, viewer, operations.DeleteTalentMarketMemberCommand{AgentProfileID: "profile-internal"})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("DeleteTalentMarketMember internal err = %v, want ErrInvalidRequest", err)
	}
	if _, ok := store.profiles["profile-internal"]; !ok {
		t.Fatalf("internal profile must not be deleted through talent market route")
	}
	if store.deleted["profile-internal"] {
		t.Fatalf("internal profile delete port should not be called")
	}
}

func TestCommandServiceBatchDeletesTalentMarketProfilesAndSkipsNonMarketEntries(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-market-a": validMarketProfile("profile-market-a", "市场专家 A"),
		"profile-market-b": validMarketProfile("profile-market-b", "市场专家 B"),
		"profile-internal": validInternalProfile("profile-internal", "内部专家"),
	}}
	service := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	result, err := service.DeleteTalentMarketMembers(ctx, viewer, operations.DeleteTalentMarketMembersCommand{
		AgentProfileIDs: []string{"profile-market-a", "profile-internal", "profile-missing", " ", "profile-market-b"},
		Reason:          "cleanup selected imports",
	})
	if err != nil {
		t.Fatalf("DeleteTalentMarketMembers: %v", err)
	}
	if result.DeletedCount != 2 || len(result.Deleted) != 2 || result.Deleted[0] != "profile-market-a" || result.Deleted[1] != "profile-market-b" {
		t.Fatalf("deleted result = %+v", result)
	}
	if len(result.Skipped) != 3 {
		t.Fatalf("skipped result = %+v", result.Skipped)
	}
	wantSkipped := map[string]string{
		"profile-internal": "not_market",
		"profile-missing":  "not_found",
		"":                 "invalid_id",
	}
	for _, skipped := range result.Skipped {
		if wantSkipped[skipped.AgentProfileID] != skipped.Reason {
			t.Fatalf("unexpected skipped entry = %+v, want map %+v", skipped, wantSkipped)
		}
	}
	if _, ok := store.profiles["profile-market-a"]; ok {
		t.Fatalf("profile-market-a should be physically deleted")
	}
	if _, ok := store.profiles["profile-market-b"]; ok {
		t.Fatalf("profile-market-b should be physically deleted")
	}
	if _, ok := store.profiles["profile-internal"]; !ok {
		t.Fatalf("internal profile must not be deleted through batch talent market route")
	}
	if store.deleted["profile-internal"] {
		t.Fatalf("internal profile delete port should not be called")
	}
}

func validMarketProfile(id agentcore.AgentProfileID, realName string) agentcore.AgentProfile {
	profile := agentcore.AgentProfile{
		ID:          id,
		RealName:    realName,
		DisplayName: realName,
		AgentKind:   agentcore.AgentKindAI,
		Placement:   agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket},
		OrgIdentity: agentcore.AgentOrgIdentity{
			EmploymentType:    agentcore.EmploymentTypeAIAgent,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		Persona: agentcore.AgentPersona{
			ResponsibilityBoundary: "负责人才市场测试任务并提交证据。",
		},
		BaseAgent: agentcore.BaseAgentSpec{
			InheritsBaseAgent: true,
			AuditRequired:     true,
			SystemPrompt:      "你是人才市场候选 Agent。",
			LLMAPIKeyRef:      "secret://system/llm-api-key",
			SkillScope:        "profile_bound",
			ToolScope:         "profile_bound",
			MCPScope:          "profile_bound",
		},
		SkillRefs: []agentcore.SkillRef{},
		ToolRefs:  []agentcore.ToolRef{},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-" + id.String()),
			OwnerAgentID:    agentcore.AgentID(id),
			MemoryKind:      agentcore.MemoryKindPersonal,
		},
		Enabled: true,
	}
	return profile
}

func validInternalProfile(id agentcore.AgentProfileID, realName string) agentcore.AgentProfile {
	profile := validMarketProfile(id, realName)
	profile.Placement = agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal}
	profile.OrgIdentity.OrganizationRef = agentcore.OrganizationRef{ID: "orrery"}
	profile.OrgIdentity.DepartmentRef = agentcore.DepartmentRef{ID: "ops"}
	profile.OrgIdentity.PositionRef = agentcore.PositionRef{ID: "operator"}
	return profile
}
