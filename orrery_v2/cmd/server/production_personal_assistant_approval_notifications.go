package main

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/hil"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
)

type productionPersonalAssistantApprovalCandidate struct {
	DecisionID     string
	ApprovalTenant string
	Requester      hil.ActorRef
	Action         string
	ActionTypeID   string
	Purpose        string
	RiskLevel      hil.RiskLevel
	RouteMode      hil.DecisionMode
	Payload        map[string]any
	CreatedAt      time.Time
	ExpiresAt      time.Time
	WorkflowID     string
	TaskID         string
	HumanProfileID string
	RequesterName  string
}

type productionPersonalAssistantWeixinTarget struct {
	TenantID  string
	OwnerID   string
	AccountID string
}

type productionPersonalAssistantApprovalNotificationStore struct {
	store *db.Store
}

type productionPersonalAssistantApprovalNotificationSource interface {
	ListPending(context.Context, int, int) ([]productionPersonalAssistantApprovalCandidate, error)
	IsPending(context.Context, productionPersonalAssistantApprovalCandidate) (bool, error)
	WeixinTarget(context.Context, string, string) (productionPersonalAssistantWeixinTarget, bool, error)
}

type productionPersonalAssistantApprovalDeliveryLookup interface {
	HasIdempotencyKey(context.Context, string, string, string) (bool, error)
}

type productionPersonalAssistantApprovalPresentation struct {
	Title           string
	DecisionRequest string
	BusinessContext string
	ActionDetails   string
	ExpectedResult  string
	ImpactScope     string
	RiskSummary     string
	MaterialNotice  string
}

func productionPersonalAssistantApprovalNotificationContent(candidate productionPersonalAssistantApprovalCandidate, publicOrigin string) (string, string) {
	return productionPersonalAssistantApprovalNotificationContentWithReview(candidate, publicOrigin, nil)
}

func productionPersonalAssistantApprovalNotificationContentWithReview(
	candidate productionPersonalAssistantApprovalCandidate,
	publicOrigin string,
	review *productionPersonalAssistantApprovalReview,
) (string, string) {
	presentation := productionPersonalAssistantApprovalPresentationFor(candidate)
	paragraphs := []string{"你的个人助理收到一项需要你处理的 Orrery 审批。"}
	if review != nil {
		paragraphs[0] = "我已经看过这项审批。它仍在等待你本人决定，所以需要通知你。"
		paragraphs = append(paragraphs, "为什么现在通知你："+review.NotificationReason)
	}
	paragraphs = append(paragraphs, "审批事项："+presentation.Title)
	seen := map[string]bool{strings.TrimSpace(presentation.Title): true}
	appendField := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		paragraphs = append(paragraphs, label+value)
	}
	if requester := strings.TrimSpace(candidate.RequesterName); requester != "" {
		paragraphs = append(paragraphs, "申请人："+requester)
	}
	appendField("业务背景：", presentation.BusinessContext)
	appendField("需要你决定：", presentation.DecisionRequest)
	appendField("具体操作：", presentation.ActionDetails)
	appendField("同意后结果：", presentation.ExpectedResult)
	appendField("影响范围：", presentation.ImpactScope)
	if presentation.MaterialNotice != "" {
		paragraphs = append(paragraphs, "材料提示："+presentation.MaterialNotice)
	}
	if presentation.RiskSummary != "" {
		paragraphs = append(paragraphs, "风险与应对："+presentation.RiskSummary)
	} else if riskLabel := productionPersonalAssistantApprovalRiskLabel(candidate.RiskLevel); riskLabel != "" {
		paragraphs = append(paragraphs, "风险等级："+riskLabel)
	}
	if !candidate.ExpiresAt.IsZero() {
		paragraphs = append(paragraphs, "截止时间："+candidate.ExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	if review != nil {
		paragraphs = append(paragraphs,
			"我的理解："+review.PlainLanguageSummary,
			"我的建议："+productionPersonalAssistantApprovalRecommendationText(review.Recommendation),
			"建议理由："+review.RecommendationReason,
		)
		if review.RiskNote != "" {
			paragraphs = append(paragraphs, "需要注意："+review.RiskNote)
		}
		if review.MissingInformation != "" {
			paragraphs = append(paragraphs, "还缺少的信息："+review.MissingInformation)
		}
	}
	paragraphs = append(paragraphs,
		"如果哪里看不明白，直接问我“这是什么”“同意后会发生什么”或“有什么风险”，我会结合上面的完整材料解释。",
		"决定后，请单独回复“同意”或“拒绝”。",
	)
	if approvalURL := productionPersonalAssistantApprovalURL(candidate, publicOrigin); approvalURL != "" {
		paragraphs = append(paragraphs, "需要查看详情时，可打开 Orrery 审批中心：\n"+approvalURL)
	}
	body := truncateProductionHILRunes(strings.Join(paragraphs, "\n\n"), gwoutbound.MaxDeliveryBodyRunes)
	return truncateProductionHILRunes("待审批："+presentation.Title, 1000), body
}

func productionPersonalAssistantApprovalRecommendationText(value string) string {
	switch value {
	case "approve":
		return "根据现有材料，建议同意。"
	case "reject":
		return "根据现有材料，建议拒绝。"
	case "ask_for_clarification":
		return "建议先拒绝，请申请人补充材料后重新提交。"
	default:
		return "请根据完整材料自行决定。"
	}
}

func productionPersonalAssistantApprovalPresentationFor(candidate productionPersonalAssistantApprovalCandidate) productionPersonalAssistantApprovalPresentation {
	if material, ok := productionHILBusinessApprovalMaterial(candidate.Payload); ok {
		title := productionPersonalAssistantApprovalDetailText(material.Title, 80)
		return productionPersonalAssistantApprovalPresentation{
			Title:           firstNonBlank(title, "业务事项审批"),
			DecisionRequest: productionPersonalAssistantApprovalDetailText(material.DecisionRequest, 320),
			BusinessContext: productionPersonalAssistantApprovalDetailText(material.BusinessContext, 320),
			ActionDetails:   productionPersonalAssistantApprovalDetailText(material.ProposedAction, 600),
			ExpectedResult:  productionPersonalAssistantApprovalDetailText(material.ExpectedResult, 320),
			ImpactScope:     productionPersonalAssistantApprovalDetailText(material.ImpactScope, 320),
			RiskSummary:     productionPersonalAssistantApprovalBusinessRisks(material.Risks),
		}
	}
	payload := productionPersonalAssistantApprovalDisplayPayload(candidate.Payload)

	presentation, _ := productionPersonalAssistantApprovalToolPresentation(
		productionPersonalAssistantApprovalPayloadString(payload, "approval_tool_name"),
	)
	if presentation.Title == "" {
		presentation, _ = productionPersonalAssistantApprovalActionPresentation(
			firstNonBlank(strings.TrimSpace(candidate.ActionTypeID), strings.TrimSpace(candidate.Action)),
		)
	}
	if value := productionPersonalAssistantApprovalFirstReadableText(payload, 80,
		"title", "payload_title", "approval_title", "todo_title", "scheduled_task_name"); value != "" {
		presentation.Title = value
	} else if presentation.Title == "" {
		value := productionPersonalAssistantApprovalReadableText(candidate.Purpose, 80)
		presentation.Title = value
	}
	if value := productionPersonalAssistantApprovalFirstDetailText(payload, 320,
		"business_context", "request_context", "final_goal", "root_goal", "task_goal", "goal", "business_direction", "scheduled_task_description", "description"); value != "" {
		presentation.BusinessContext = value
	}
	if value := productionPersonalAssistantApprovalFirstDetailText(payload, 320,
		"decision_request", "approval_summary", "approval_description", "question", "prompt"); value != "" {
		presentation.DecisionRequest = value
	}
	presentation.ActionDetails = productionPersonalAssistantApprovalActionDetails(payload)
	if value := productionPersonalAssistantApprovalFirstDetailText(payload, 320,
		"expected_result", "acceptance_criteria", "acceptance_checks"); value != "" {
		presentation.ExpectedResult = value
	}
	if value := productionPersonalAssistantApprovalFirstDetailText(payload, 240,
		"impact_summary", "impact_scope", "expected_result"); value != "" {
		presentation.ImpactScope = value
	}
	presentation.RiskSummary = productionPersonalAssistantApprovalRiskSummary(payload)
	if presentation.DecisionRequest == "" {
		if fields, ok := payload["fields"].(map[string]any); ok {
			for _, key := range []string{"decision_request", "approval_summary", "question", "prompt"} {
				value, _ := fields[key].(string)
				if value = productionPersonalAssistantApprovalDetailText(value, 320); value != "" {
					presentation.DecisionRequest = value
					break
				}
			}
		}
	}
	presentation.Title = firstNonBlank(presentation.Title, "需要处理的审批")
	presentation.DecisionRequest = firstNonBlank(presentation.DecisionRequest, "是否允许继续执行这项操作？")
	presentation.ImpactScope = firstNonBlank(presentation.ImpactScope, "批准后系统会继续执行；拒绝后该操作将保持暂停。")
	if presentation.ActionDetails == "" {
		presentation.MaterialNotice = "审批请求没有提供可核对的具体内容。建议先拒绝，并让申请人补充操作对象、范围和预期结果。"
	}
	return presentation
}

func productionPersonalAssistantApprovalDisplayPayload(payload map[string]any) map[string]any {
	merged := make(map[string]any, len(payload))
	for key, value := range payload {
		merged[key] = value
	}
	display, _ := payload[hil.ApprovalDisplayPayloadKey].(map[string]any)
	for key, value := range display {
		merged[key] = value
	}
	return merged
}

var (
	productionPersonalAssistantApprovalSecretPattern      = regexp.MustCompile(`(?i)\b(password|passwd|authorization|access[_-]?token|refresh[_-]?token|api[_-]?key|secret)\b[[:space:]]*[:=][[:space:]]*(bearer[[:space:]]+)?[^,;[:space:]]+`)
	productionPersonalAssistantApprovalCLISecretPattern   = regexp.MustCompile(`(?i)(--?[a-z0-9_-]*(password|passwd|authorization|token|secret|api[-_]?key)[a-z0-9_-]*)[=[:space:]]+[^,;[:space:]]+`)
	productionPersonalAssistantApprovalBearerPattern      = regexp.MustCompile(`(?i)\bbearer[[:space:]]+[^,;[:space:]]+`)
	productionPersonalAssistantApprovalInternalRefPattern = regexp.MustCompile(`(?i)\b(call|fc)_[a-z0-9_|:.-]+|\bsha256:[a-f0-9]{16,}|\bresponsibility-run-[a-z0-9_.:-]+`)
	productionPersonalAssistantApprovalURLPattern         = regexp.MustCompile(`https?://[^[:space:]]+`)
)

func productionPersonalAssistantApprovalBusinessRisks(risks []hil.BusinessApprovalRisk) string {
	parts := make([]string, 0, len(risks))
	for index, risk := range risks {
		description := productionPersonalAssistantApprovalDetailText(risk.Description, 180)
		mitigation := productionPersonalAssistantApprovalDetailText(risk.Mitigation, 180)
		if description == "" {
			continue
		}
		part := fmt.Sprintf("风险 %d：%s", index+1, description)
		if mitigation != "" {
			part += "；应对：" + mitigation
		}
		parts = append(parts, part)
	}
	return productionPersonalAssistantApprovalDetailText(strings.Join(parts, "；"), 600)
}

func productionPersonalAssistantApprovalActionDetails(payload map[string]any) string {
	parts := make([]string, 0, 6)
	seen := map[string]bool{}
	appendPart := func(prefix, value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		parts = append(parts, prefix+value)
	}
	appendPart("拟执行：", productionPersonalAssistantApprovalFirstDetailText(payload, 360, "proposed_action"))
	appendPart("方案摘要：", productionPersonalAssistantApprovalFirstDetailText(payload, 360, "summary", "plan_summary"))
	appendPart("内容摘要：", productionPersonalAssistantApprovalFirstDetailText(payload, 360, "content"))
	appendPart("命令：", productionPersonalAssistantApprovalFirstDetailText(payload, 300, "command"))
	appendPart("查询内容：", productionPersonalAssistantApprovalFirstDetailText(payload, 260, "query"))
	if target := productionPersonalAssistantApprovalSafeURL(productionPersonalAssistantApprovalPayloadString(payload, "url")); target != "" {
		appendPart("目标网站：", target)
	}
	if path := productionPersonalAssistantApprovalSafePath(productionPersonalAssistantApprovalPayloadString(payload, "path")); path != "" {
		appendPart("涉及文件：", path)
	}
	appendPart("修改摘要：", productionPersonalAssistantApprovalFirstDetailText(payload, 360, "payload_preview"))
	appendPart("执行步骤：", productionPersonalAssistantApprovalStepSummary(payload["steps"]))
	if len(parts) == 0 {
		if fields, ok := payload["fields"].(map[string]any); ok {
			appendPart("内容摘要：", productionPersonalAssistantApprovalFirstDetailText(fields, 360, "content", "summary", "description"))
		}
	}
	return productionPersonalAssistantApprovalDetailText(strings.Join(parts, "；"), 1200)
}

func productionPersonalAssistantApprovalStepSummary(raw any) string {
	steps := make([]any, 0)
	switch typed := raw.(type) {
	case []any:
		steps = append(steps, typed...)
	case []map[string]any:
		for _, step := range typed {
			steps = append(steps, step)
		}
	}
	parts := make([]string, 0, 4)
	for _, rawStep := range steps {
		step, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		title := productionPersonalAssistantApprovalFirstDetailText(step, 120, "title")
		goal := productionPersonalAssistantApprovalFirstDetailText(step, 180, "goal", "description")
		if title == "" && goal == "" {
			continue
		}
		value := firstNonBlank(title, goal)
		if title != "" && goal != "" && title != goal {
			value += "：" + goal
		}
		parts = append(parts, fmt.Sprintf("%d. %s", len(parts)+1, value))
		if len(parts) == 4 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	prefix := fmt.Sprintf("共 %d 个步骤，", len(steps))
	if len(steps) > len(parts) {
		return prefix + strings.Join(parts, "；") + "；其余步骤请在审批中心查看"
	}
	return prefix + strings.Join(parts, "；")
}

func productionPersonalAssistantApprovalRiskSummary(payload map[string]any) string {
	if value := productionPersonalAssistantApprovalPayloadValueText(payload["risk_summary"], 600); value != "" {
		return value
	}
	return productionPersonalAssistantApprovalPayloadValueText(payload["risks"], 600)
}

func productionPersonalAssistantApprovalFirstDetailText(payload map[string]any, maxRunes int, keys ...string) string {
	for _, key := range keys {
		if value := productionPersonalAssistantApprovalPayloadValueText(payload[key], maxRunes); value != "" {
			return value
		}
	}
	return ""
}

func productionPersonalAssistantApprovalPayloadValueText(raw any, maxRunes int) string {
	values := make([]string, 0)
	switch typed := raw.(type) {
	case string:
		values = append(values, typed)
	case []string:
		values = append(values, typed...)
	case []any:
		for _, value := range typed {
			if text, ok := value.(string); ok {
				values = append(values, text)
			}
		}
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if text := productionPersonalAssistantApprovalDetailText(value, maxRunes); text != "" {
			parts = append(parts, text)
		}
	}
	return productionPersonalAssistantApprovalDetailText(strings.Join(parts, "；"), maxRunes)
}

func productionPersonalAssistantApprovalDetailText(value string, maxRunes int) string {
	value = productionPersonalAssistantApprovalSecretPattern.ReplaceAllString(value, "$1=[已隐藏]")
	value = productionPersonalAssistantApprovalCLISecretPattern.ReplaceAllString(value, "$1=[已隐藏]")
	value = productionPersonalAssistantApprovalBearerPattern.ReplaceAllString(value, "Bearer [已隐藏]")
	value = productionPersonalAssistantApprovalInternalRefPattern.ReplaceAllString(value, "[内部引用已隐藏]")
	value = productionPersonalAssistantApprovalURLPattern.ReplaceAllStringFunc(value, productionPersonalAssistantApprovalSafeURL)
	value = strings.NewReplacer(
		"```", " ", "`", "", "**", "", "__", "", "\r", " ", "\n", "；", "\t", " ",
		"##", "", "#", "", "- ", "", "* ", "", "> ", "",
	).Replace(value)
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	value = strings.Trim(value, "；,，。 ")
	if value == "" || value == "[内部引用已隐藏]" || productionPersonalAssistantApprovalMachineIdentifier(value) {
		return ""
	}
	if maxRunes == 1 && len([]rune(value)) > 1 {
		return "…"
	}
	if maxRunes > 1 && len([]rune(value)) > maxRunes {
		runes := []rune(value)
		value = strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
	}
	return value
}

func productionPersonalAssistantApprovalSafeURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(strings.TrimRight(value, ".,，。；;")))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Hostname()
}

func productionPersonalAssistantApprovalSafePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	parts := make([]string, 0)
	for _, part := range strings.Split(value, "/") {
		part = strings.TrimSpace(part)
		if part != "" && part != "." && part != ".." {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	value = productionPersonalAssistantApprovalSecretPattern.ReplaceAllString(strings.Join(parts, "/"), "$1=[已隐藏]")
	return truncateProductionHILRunes(value, 180)
}

func productionPersonalAssistantApprovalRequesterName(ctx context.Context, candidate productionPersonalAssistantApprovalCandidate, profiles productionHILProfileDirectory) string {
	for _, key := range []string{"approval_requester_name", "requested_by_name", "requester_name"} {
		if value := productionPersonalAssistantApprovalPayloadString(candidate.Payload, key); value != "" {
			return value
		}
	}
	requesterID := strings.TrimSpace(candidate.Requester.ID)
	if requesterID != "" && profiles != nil {
		if profile, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(requesterID)); err == nil {
			if name := firstNonBlank(strings.TrimSpace(profile.DisplayName), strings.TrimSpace(profile.RealName), strings.TrimSpace(profile.RoleName)); name != "" {
				return name
			}
		}
	}
	switch candidate.Requester.Kind {
	case hil.ActorHuman:
		return "Orrery 用户"
	case hil.ActorAgent:
		return "Orrery 智能体"
	case hil.ActorService:
		return "Orrery 服务"
	default:
		return "Orrery"
	}
}

func productionPersonalAssistantApprovalRiskLabel(level hil.RiskLevel) string {
	switch strings.ToLower(strings.TrimSpace(string(level))) {
	case "low":
		return "低"
	case "medium":
		return "中"
	case "high":
		return "高"
	case "critical":
		return "严重"
	case "unknown":
		return "待确认"
	default:
		return ""
	}
}

func productionPersonalAssistantApprovalToolPresentation(toolName string) (productionPersonalAssistantApprovalPresentation, bool) {
	var presentation productionPersonalAssistantApprovalPresentation
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "memory_diary_append":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "写入工作记忆", DecisionRequest: "是否允许保存本次任务的工作总结，供后续追踪和复盘？",
			ImpactScope: "会新增或更新该智能体的内部工作记录，不会直接对外发布。",
		}
	case "memory_diary_read":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "读取工作记忆", DecisionRequest: "是否允许读取此前保存的工作记录，以便继续当前任务？",
			ImpactScope: "只读取内部工作记录，不会修改内容或对外发送。",
		}
	case "memory_search":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "搜索工作记忆", DecisionRequest: "是否允许查找与当前任务有关的历史工作记录？",
			ImpactScope: "只查询内部工作记录，不会修改内容或对外发送。",
		}
	case "knowledge_search":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "搜索组织知识库", DecisionRequest: "是否允许查找完成当前任务所需的组织资料？",
			ImpactScope: "只读取当前账号有权访问的组织资料，不会修改原始内容。",
		}
	case "knowledge_add":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "写入组织知识库", DecisionRequest: "是否允许把这项内容写入组织知识库？",
			ImpactScope: "会新增组织知识内容，之后可能被其他有权限的成员和智能体检索。",
		}
	case "load_skill":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "加载任务能力", DecisionRequest: "是否允许加载完成当前任务所需的能力？",
			ImpactScope: "会为当前任务加载既有能力；不会因此直接对外发布内容。",
		}
	case "patch_apply":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "修改任务文件", DecisionRequest: "是否允许应用智能体为当前任务准备的文件修改？",
			ImpactScope: "会修改当前任务工作区中的文件；审批前不会执行。",
		}
	case "exec_command":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "执行系统命令", DecisionRequest: "是否允许执行当前任务所需的系统命令？",
			ImpactScope: "命令可能读取或修改运行环境；请在审批中心查看详情后决定。",
		}
	case "enterprise_graph_search_objects":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "查询组织知识图谱", DecisionRequest: "是否允许查找与当前任务有关的组织信息？",
			ImpactScope: "只查询当前账号有权访问的组织信息，不会修改知识图谱。",
		}
	case "enterprise_graph_build_context_bundle":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "整理组织知识上下文", DecisionRequest: "是否允许整理当前任务所需的组织信息？",
			ImpactScope: "只汇总当前账号有权访问的信息，不会修改原始资料。",
		}
	case "scheduled_task_list":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "查看定时任务", DecisionRequest: "是否允许查看当前账号的定时任务安排？",
			ImpactScope: "只读取任务安排，不会新增、修改或执行任务。",
		}
	case "webfetch", "web_fetch":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "访问网页", DecisionRequest: "是否允许访问完成当前任务所需的网页？",
			ImpactScope: "会向目标网站发起网络请求，不会自动发布内容。",
		}
	case "websearch", "web_search":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "搜索网页", DecisionRequest: "是否允许搜索完成当前任务所需的公开网页？",
			ImpactScope: "会向搜索服务发起网络请求，不会自动发布内容。",
		}
	case "marketing_wechat_draft_create", "mcp_marketing_marketing_wechat_draft_create":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "创建微信公众号草稿", DecisionRequest: "是否允许把已准备的内容保存到微信公众号草稿箱？",
			ImpactScope: "只会创建草稿，不会自动向公众号读者发布。",
		}
	case "email_send", "mcp_system_email_send":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "发送外部邮件", DecisionRequest: "是否允许向指定收件人发送这封邮件？",
			ImpactScope: "同意后邮件会发送到组织外部；发送后无法从收件人的邮箱撤回。",
		}
	case hil.BusinessApprovalToolName:
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "业务事项审批", DecisionRequest: "是否同意申请人提交的业务方案？",
			ImpactScope: "同意后将按申请方案继续执行；拒绝后方案保持暂停。",
		}
	default:
		return productionPersonalAssistantApprovalPresentation{}, false
	}
	return presentation, true
}

func productionPersonalAssistantApprovalActionPresentation(action string) (productionPersonalAssistantApprovalPresentation, bool) {
	var presentation productionPersonalAssistantApprovalPresentation
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "information.read":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "读取任务所需信息", DecisionRequest: "是否允许读取完成当前任务所需的信息？",
			ImpactScope: "只读取当前账号有权访问的信息，不会修改原始内容。",
		}
	case "system.file.change":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "修改任务文件", DecisionRequest: "是否允许应用智能体为当前任务准备的文件修改？",
			ImpactScope: "会修改当前任务工作区中的文件；审批前不会执行。",
		}
	case "system.command.execute":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "执行系统命令", DecisionRequest: "是否允许执行当前任务所需的系统命令？",
			ImpactScope: "命令可能读取或修改运行环境；请在审批中心查看详情后决定。",
		}
	case "network.external.access":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "访问外部网络", DecisionRequest: "是否允许访问完成当前任务所需的外部服务？",
			ImpactScope: "会向外部服务发起网络请求，不会自动发布内容。",
		}
	case "communication.wechat_draft.create":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "创建微信公众号草稿", DecisionRequest: "是否允许把已准备的内容保存到微信公众号草稿箱？",
			ImpactScope: "只会创建草稿，不会自动向公众号读者发布。",
		}
	case "tool.external.execute":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "调用外部工具", DecisionRequest: "是否允许使用外部工具继续当前任务？",
			ImpactScope: "外部工具可能读取或处理任务数据；请在审批中心查看详情后决定。",
		}
	case "tool.unclassified.execute":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "执行一项工具操作", DecisionRequest: "是否允许智能体继续执行这项操作？",
			ImpactScope: "该工具尚未完成分类，因此系统已暂停执行并请你确认。",
		}
	case "communication.email.send":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "发送外部邮件", DecisionRequest: "是否允许向指定收件人发送这封邮件？",
			ImpactScope: "同意后邮件会发送到组织外部；发送后无法从收件人的邮箱撤回。",
		}
	case "todo.execution_plan.approve":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "审批 Todo 执行方案", DecisionRequest: "是否同意系统按这份方案执行 Todo？",
			ImpactScope: "同意后系统会开始执行方案；拒绝后不会开始执行。",
		}
	case "responsibility.plan.execute":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "审批职责执行方案", DecisionRequest: "是否同意按这份计划推进职责工作？",
			ImpactScope: "同意后系统会创建并推进计划中的工作；拒绝后不会执行。",
		}
	case "business.approval.request":
		presentation = productionPersonalAssistantApprovalPresentation{
			Title: "业务事项审批", DecisionRequest: "是否同意申请人提交的业务方案？",
			ImpactScope: "同意后将按申请方案继续执行；拒绝后方案保持暂停。",
		}
	default:
		return productionPersonalAssistantApprovalPresentation{}, false
	}
	return presentation, true
}

func productionPersonalAssistantApprovalFirstReadableText(payload map[string]any, maxRunes int, keys ...string) string {
	for _, key := range keys {
		if value := productionPersonalAssistantApprovalReadableText(productionPersonalAssistantApprovalPayloadString(payload, key), maxRunes); value != "" {
			return value
		}
	}
	return ""
}

func productionPersonalAssistantApprovalReadableText(value string, maxRunes int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	normalized := strings.Join(strings.Fields(trimmed), " ")
	if maxRunes > 0 && len([]rune(normalized)) > maxRunes {
		return ""
	}
	lower := strings.ToLower(normalized)
	switch lower {
	case "low", "medium", "high", "critical", "unknown", "pending", "approved", "rejected", "true", "false", "runtime.approval":
		return ""
	}
	for _, marker := range []string{
		"runtime approval", "human review required", "hil decision", "approval decision",
		"call_", "|fc_", "sha256:", "responsibility-run-", "approval_request_id",
		"approval_run_id", "approval_conversation_id", "material_hash", "```", "`", "**", "http://", "https://",
	} {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	if strings.HasPrefix(normalized, "#") || strings.HasPrefix(normalized, "- ") || productionPersonalAssistantApprovalMachineIdentifier(normalized) {
		return ""
	}
	return normalized
}

func productionPersonalAssistantApprovalMachineIdentifier(value string) bool {
	if value == "" {
		return false
	}
	hasSeparator := false
	hasLetter := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z':
			hasLetter = true
		case character >= '0' && character <= '9':
		case character == '_', character == '-', character == '.', character == ':', character == '/', character == '|':
			hasSeparator = true
		default:
			return false
		}
	}
	return hasLetter && hasSeparator
}

func productionPersonalAssistantApprovalPayloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func productionPersonalAssistantApprovalURL(candidate productionPersonalAssistantApprovalCandidate, publicOrigin string) string {
	origin := strings.TrimRight(strings.TrimSpace(publicOrigin), "/")
	if origin == "" {
		return ""
	}
	mode := string(candidate.RouteMode)
	if mode != string(hil.ModeHIL) && mode != string(hil.ModeApproval) {
		mode = string(hil.ModeApproval)
	}
	approvalURL := origin + "/approvals?mode=" + url.QueryEscape(mode)
	if candidate.WorkflowID != "" {
		approvalURL += "&workflow_id=" + url.QueryEscape(candidate.WorkflowID)
	}
	return approvalURL
}
