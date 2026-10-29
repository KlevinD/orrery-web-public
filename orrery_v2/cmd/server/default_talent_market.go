package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

const talentMarketImportExtensionKey = "talent_market_import"

type defaultTalentMarketSeed struct {
	id             agentcore.AgentProfileID
	name           string
	roleName       string
	category       string
	slug           string
	description    string
	responsibility string
	behavior       string
	prompt         string
}

func skipDefaultTalentMarketBootstrap() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ORRERY_SKIP_DEFAULT_TALENT_MARKET_BOOTSTRAP")), "true")
}

func ensureDefaultTalentMarketProfiles(ctx context.Context, profiles *agentcore.PlatformProfileRepository) error {
	if profiles == nil || skipDefaultTalentMarketBootstrap() {
		return nil
	}
	for _, seeded := range defaultTalentMarketProfiles() {
		existing, found, err := getAgentProfile(ctx, profiles, seeded.ID)
		if err != nil {
			return err
		}
		if !found {
			deleted, err := profiles.IsProfileDeleted(ctx, seeded.ID)
			if err != nil {
				return err
			}
			if deleted {
				continue
			}
		} else {
			seeded = mergeDefaultTalentMarketProfile(existing, seeded)
		}
		if errs := agentcore.ValidateAgentProfile(seeded); len(errs) > 0 {
			return operations.ErrInvalidRequest
		}
		if err := profiles.SaveProfile(ctx, seeded); err != nil {
			return err
		}
		if err := saveDefaultTalentMarketAvailability(ctx, profiles, seeded, !found); err != nil {
			return err
		}
	}
	return nil
}

func mergeDefaultTalentMarketProfile(existing, seeded agentcore.AgentProfile) agentcore.AgentProfile {
	merged := existing
	if merged.ID.IsZero() {
		merged.ID = seeded.ID
	}
	if strings.TrimSpace(merged.RealName) == "" || !containsChinese(merged.RealName) {
		merged.RealName = seeded.RealName
	}
	if strings.TrimSpace(merged.DisplayName) == "" {
		merged.DisplayName = seeded.DisplayName
	}
	if strings.TrimSpace(merged.RoleName) == "" {
		merged.RoleName = seeded.RoleName
	}
	if !merged.AgentKind.IsValid() {
		merged.AgentKind = seeded.AgentKind
	}
	if merged.Placement.Status == "" &&
		merged.OrgIdentity.OrganizationRef.ID == "" &&
		merged.OrgIdentity.DepartmentRef.ID == "" &&
		merged.OrgIdentity.PositionRef.ID == "" {
		merged.Placement = seeded.Placement
	}
	if strings.TrimSpace(merged.Persona.Backstory) == "" {
		merged.Persona.Backstory = seeded.Persona.Backstory
	}
	if strings.TrimSpace(merged.Persona.ResponsibilityBoundary) == "" || !containsChinese(merged.Persona.ResponsibilityBoundary) {
		merged.Persona.ResponsibilityBoundary = seeded.Persona.ResponsibilityBoundary
	}
	if strings.TrimSpace(merged.Persona.BehaviorBoundary) == "" {
		merged.Persona.BehaviorBoundary = seeded.Persona.BehaviorBoundary
	}
	merged.BaseAgent = mergeDefaultBaseAgentSpec(merged.BaseAgent, seeded.BaseAgent)
	if merged.PersonalMemoryProfileRef.IsZero() {
		merged.PersonalMemoryProfileRef = seeded.PersonalMemoryProfileRef
	}
	if merged.CollectiveMemoryScopeRefs == nil {
		merged.CollectiveMemoryScopeRefs = append([]agentcore.MemoryProfileRef(nil), seeded.CollectiveMemoryScopeRefs...)
	}
	if merged.SkillRefs == nil {
		merged.SkillRefs = []agentcore.SkillRef{}
	}
	if merged.ToolRefs == nil {
		merged.ToolRefs = []agentcore.ToolRef{}
	}
	if !merged.RuntimeSpec.HasKind() {
		merged.RuntimeSpec = seeded.RuntimeSpec
	}
	if len(merged.BaseAgent.Extensions) == 0 {
		merged.BaseAgent.Extensions = cloneTalentMarketExtensions(seeded.BaseAgent.Extensions)
	} else if _, ok := merged.BaseAgent.Extensions[talentMarketImportExtensionKey]; !ok {
		merged.BaseAgent.Extensions = cloneTalentMarketExtensions(merged.BaseAgent.Extensions)
		merged.BaseAgent.Extensions[talentMarketImportExtensionKey] = seeded.BaseAgent.Extensions[talentMarketImportExtensionKey]
	}
	return merged
}

func saveDefaultTalentMarketAvailability(ctx context.Context, profiles *agentcore.PlatformProfileRepository, profile agentcore.AgentProfile, newlySeeded bool) error {
	if !profile.Enabled {
		return nil
	}
	availability, err := profiles.GetAvailability(ctx, profile.ID)
	if err != nil {
		return err
	}
	if !newlySeeded && availability.Status != agentcore.AvailabilityUnknown {
		return nil
	}
	return profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: agentcore.AgentInstanceID(profile.ID.String()),
		Status:          agentcore.AvailabilityAvailable,
		Capacity:        1,
		Source:          "talent-market-bootstrap",
		UpdatedAt:       time.Now().UTC(),
	})
}

func defaultTalentMarketProfiles() []agentcore.AgentProfile {
	seeds := []defaultTalentMarketSeed{
		{
			id:             "talent-market-backoffice-finance-planning",
			name:           "林衡远",
			roleName:       "财务规划与分析顾问",
			category:       "backoffice-finance",
			slug:           "finance-planning",
			description:    "负责预算、预测、经营分析和财务决策材料。",
			responsibility: "负责预算编制、滚动预测、差异分析、经营指标和财务建议；不执行付款、转账或资金划拨。",
			behavior:       "区分账面事实、管理假设和预测结果；所有关键数字注明期间、口径和来源。",
			prompt:         "你是林衡远，Orrery 开源示例人才市场中的财务规划与分析顾问。你负责预算、预测、差异分析和经营财务材料。必须说明数据口径、期间、来源和不确定性；可以提出建议，但不能执行付款、转账、资金划拨或替负责人批准预算。",
		},
		{
			id:             "talent-market-backoffice-finance-control",
			name:           "苏谨言",
			roleName:       "财务控制与会计顾问",
			category:       "backoffice-finance",
			slug:           "finance-control",
			description:    "负责会计流程、对账、费用审核和内部财务控制。",
			responsibility: "负责凭证检查、账户对账、费用规则、内控缺口和审计材料整理；不篡改账簿或批准付款。",
			behavior:       "保留凭证和差异证据；异常先挂起并升级，不用猜测补齐财务记录。",
			prompt:         "你是苏谨言，Orrery 开源示例人才市场中的财务控制与会计顾问。你负责对账、费用审核、凭证检查和内控证据整理。所有异常必须保留证据并升级；不得修改账簿、伪造凭证、批准付款或代替持牌/授权财务人员作最终决定。",
		},
		{
			id:             "talent-market-backoffice-legal-contract",
			name:           "顾谨川",
			roleName:       "法务合同与合规顾问",
			category:       "backoffice-legal",
			slug:           "legal-contract",
			description:    "负责合同条款、义务、合规风险和审查意见整理。",
			responsibility: "负责合同条款审阅、义务提取、风险分类、合规清单和人工复核材料；不提供未经复核的最终法律意见。",
			behavior:       "引用合同原文和适用政策；区分事实、风险提示和法律判断；高风险事项进入人工法务复核。",
			prompt:         "你是顾谨川，Orrery 开源示例人才市场中的法务合同与合规顾问。你负责条款审阅、义务提取和合规风险整理。必须引用依据，明确事实与风险提示的边界；不得冒充律师给出最终法律意见、签署合同或执行外部法律操作，高风险结论必须交由人工法务复核。",
		},
		{
			id:             "talent-market-backoffice-hr-operations",
			name:           "叶知行",
			roleName:       "人力资源与组织运营顾问",
			category:       "backoffice-hr",
			slug:           "hr-operations",
			description:    "负责岗位、组织流程、入职离职和人员运营材料。",
			responsibility: "负责岗位说明、组织流程、入转调离材料和人力运营分析；不擅自作出录用、解雇、晋升、薪酬或劳动争议决定。",
			behavior:       "最小化处理个人信息；敏感结论只给授权人员；涉及个人权益的决定必须进入人工审批。",
			prompt:         "你是叶知行，Orrery 开源示例人才市场中的人力资源与组织运营顾问。你负责岗位、组织流程和人力运营材料。必须遵守最小权限和隐私边界；不得擅自录用、解雇、晋升、调整薪酬或处理劳动争议，涉及个人权益的事项必须交由授权人员审批。",
		},
		{
			id:             "talent-market-backoffice-procurement-operations",
			name:           "周简宁",
			roleName:       "采购与行政运营顾问",
			category:       "backoffice-procurement",
			slug:           "procurement-operations",
			description:    "负责供应商比较、采购流程、行政资源和合同前置材料。",
			responsibility: "负责供应商信息整理、比价、采购需求、行政流程和审批材料；不下单、付款、签约或绕过采购审批。",
			behavior:       "记录供应商来源、比较口径和利益冲突；高金额或高风险采购必须进入授权审批。",
			prompt:         "你是周简宁，Orrery 开源示例人才市场中的采购与行政运营顾问。你负责供应商比较、采购需求和行政流程材料。必须记录来源、比较口径和利益冲突；不得下单、付款、签约或绕过采购审批，重大采购必须交由授权人员决定。",
		},
	}
	profiles := make([]agentcore.AgentProfile, 0, len(seeds))
	for _, seed := range seeds {
		profiles = append(profiles, defaultTalentMarketProfile(seed))
	}
	return profiles
}

func defaultTalentMarketProfile(seed defaultTalentMarketSeed) agentcore.AgentProfile {
	id := seed.id.String()
	base := defaultBaseAgentSpec(seed.name, seed.roleName)
	base.SystemPrompt = strings.TrimSpace(seed.prompt)
	metadata, _ := json.Marshal(map[string]string{
		"description":   seed.description,
		"category":      seed.category,
		"source_slug":   seed.slug,
		"source_path":   "builtin/talent-market/" + seed.slug + ".json",
		"source_hash":   "builtin:talent-market-seed-v3-backoffice",
		"source_prompt": seed.prompt,
	})
	base.Extensions = map[string]json.RawMessage{
		talentMarketImportExtensionKey: metadata,
	}
	return agentcore.AgentProfile{
		ID:          seed.id,
		RealName:    seed.name,
		DisplayName: seed.name,
		RoleName:    seed.roleName,
		AgentKind:   agentcore.AgentKindAI,
		Placement:   agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket, ReleasedReason: "open-source example talent market seed"},
		Persona: agentcore.AgentPersona{
			Backstory:              seed.name + "是 Orrery 开源版本提供的合成后台职能岗位示例，不代表真实个人。",
			ResponsibilityBoundary: seed.responsibility,
			BehaviorBoundary:       seed.behavior,
		},
		BaseAgent: base,
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-" + id),
			OwnerAgentID:    agentcore.AgentID(id),
			MemoryKind:      agentcore.MemoryKindPersonal,
			AccessScope:     "agent",
		},
		CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{},
		SkillRefs:                 []agentcore.SkillRef{},
		ToolRefs:                  []agentcore.ToolRef{},
		RuntimeSpec:               agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged},
		QAEnabled:                 true,
		Enabled:                   true,
	}
}

func cloneTalentMarketExtensions(source map[string]json.RawMessage) map[string]json.RawMessage {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		cloned[key] = append(json.RawMessage(nil), value...)
	}
	return cloned
}
