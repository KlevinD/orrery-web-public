package main

import (
	agentidentity "orrery_v2/internal/agent/identity"
	"strings"
)

func defaultAgentChineseName(slug, fallback string) string {
	slug = agentidentity.LegacyProfileID(slug)
	names := map[string]string{
		"adr-writer":                   "陆文衡",
		"brainstormer":                 "林启思",
		"bug-fixer":                    "沈修远",
		"code-explainer":               "顾明理",
		"code-reviewer":                "秦阅川",
		"commit-message":               "周简言",
		"coordinator-agent":            "程统筹",
		"email-reply":                  "许回澜",
		"frontend-builder":             "苏砚青",
		"frontend-designer":            "叶观澜",
		"html-slides":                  "唐映川",
		"jd-writer":                    "何招明",
		"mission-task-breakdown-agent": "邵分工",
		"okr-drafter":                  "孟定衡",
		"one-pager":                    "白一页",
		"lead-agent":                   "顾明澈",
		"prd-critic":                   "杜审言",
		"prd-drafter":                  "温产品",
		"qa-check-agent":               "严质检",
		"qa-plan-agent":                "钟验策",
		"rca-writer":                   "罗溯因",
		"release-notes":                "宋发布",
		"subagent-worker":              "赵执行",
		"summarizer":                   "简知微",
		"translator-zh-en":             "许译桥",
		"tutor":                        "陈导师",
		"user-story-writer":            "方故事",
		"ux-copywriter":                "柳文案",
		"webapp-tester":                "韩测云",
		"writing-critic":               "柏审文",
	}
	if name := names[strings.TrimSpace(slug)]; name != "" {
		return name
	}
	if containsChinese(fallback) {
		return strings.TrimSpace(fallback)
	}
	return "智能体" + strings.TrimSpace(slug)
}

func defaultAgentChineseResponsibility(slug, roleName string) string {
	slug = agentidentity.LegacyProfileID(slug)
	responsibilities := map[string]string{
		"adr-writer":                   "负责沉淀架构决策记录，说明背景、取舍、风险和后续影响。",
		"brainstormer":                 "负责发散和收敛方案，帮助团队形成可执行的创意方向。",
		"bug-fixer":                    "负责定位缺陷、修复问题，并提交可复验的修复证据。",
		"code-explainer":               "负责解释代码结构、行为路径和关键设计边界。",
		"code-reviewer":                "负责审查代码风险、回归点、安全性和测试缺口。",
		"commit-message":               "负责整理清晰、可审计的提交说明和变更摘要。",
		"coordinator-agent":            "负责并行拆解任务、协调工位、跟踪交付状态和审计记录。",
		"email-reply":                  "负责起草礼貌、准确、符合上下文的邮件回复。",
		"frontend-builder":             "负责实现前端界面、交互状态、可访问性和验证证据。",
		"frontend-designer":            "负责设计界面体验、视觉层级、状态规范和交互方案。",
		"html-slides":                  "负责制作结构清晰、可展示的 HTML 幻灯片。",
		"jd-writer":                    "负责撰写岗位说明、能力要求和招聘评价要点。",
		"mission-task-breakdown-agent": "负责把 Mission 目标拆解成清晰任务、依赖和验收标准。",
		"okr-drafter":                  "负责起草目标、关键结果和可衡量推进指标。",
		"one-pager":                    "负责产出一页式方案，凝练目标、价值、范围和风险。",
		"lead-agent":                   "负责澄清需求、生成 Mission Brief、编排团队和推进审计化协作。",
		"prd-critic":                   "负责审查产品需求的完整性、一致性、风险和验收条件。",
		"prd-drafter":                  "负责起草产品需求文档、用户场景和验收标准。",
		"qa-check-agent":               "负责执行质量检查，核对交付物、证据和验收标准。",
		"qa-plan-agent":                "负责制定质量验证计划、证据要求和评审路径。",
		"rca-writer":                   "负责撰写根因分析，梳理事实、原因、影响和改进动作。",
		"release-notes":                "负责整理发布说明、用户影响、修复内容和注意事项。",
		"subagent-worker":              "负责执行被分配的具体任务，并提交结果、证据和简要说明。",
		"summarizer":                   "负责压缩长内容，保留结论、上下文和可执行要点。",
		"translator-zh-en":             "负责中英文互译，保持语义、语气和专业表达一致。",
		"tutor":                        "负责教学讲解、分步引导和理解检查。",
		"user-story-writer":            "负责撰写用户故事、场景、收益和验收条件。",
		"ux-copywriter":                "负责优化界面文案、提示语、空状态和错误反馈。",
		"webapp-tester":                "负责验证 Web 应用行为、界面状态、错误路径和证据输出。",
		"writing-critic":               "负责审阅文稿结构、表达质量、逻辑缺口和改进建议。",
	}
	if responsibility := responsibilities[strings.TrimSpace(slug)]; responsibility != "" {
		return responsibility
	}
	if containsChinese(roleName) {
		return strings.TrimSpace(roleName)
	}
	return "负责" + strings.TrimSpace(roleName) + "，并按 BaseAgent 审计要求提交过程、结果和证据。"
}

func defaultAgentChineseBackstory(name, roleName string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "该 Agent"
	}
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		return name + "是 Orrery 系统托管的协作 Agent。"
	}
	return name + "是 Orrery 系统托管的" + roleName + "。"
}

func containsChinese(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}
