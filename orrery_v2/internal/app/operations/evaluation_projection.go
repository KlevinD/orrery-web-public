package operations

import (
	"math"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/projection"
)

func aggregateEvaluationItems(items []EvaluationItemDTO) EvaluationPageDTO {
	byEntity := map[string]*EvaluationItemDTO{}
	for _, item := range items {
		key := item.EntityType + "\x00" + item.EntityID
		aggregate := byEntity[key]
		if aggregate == nil {
			copy := EvaluationItemDTO{EntityType: item.EntityType, EntityID: item.EntityID, DisplayName: item.DisplayName, RoleName: item.RoleName, Department: item.Department, Position: item.Position, RuntimeKind: item.RuntimeKind}
			aggregate = &copy
			byEntity[key] = aggregate
		}
		mergeEvaluationItem(aggregate, item)
	}
	out := make([]EvaluationItemDTO, 0, len(byEntity))
	for _, item := range byEntity {
		finalizeEvaluationScores(item)
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivityAt.After(out[j].LastActivityAt) })
	return EvaluationPageDTO{Items: out, Page: PageDTO{Total: len(out), Returned: len(out)}}
}

func mergeEvaluationItem(dst *EvaluationItemDTO, src EvaluationItemDTO) {
	dst.AssignedTasks += src.AssignedTasks
	dst.CompletedTasks += src.CompletedTasks
	dst.ReworkCount += src.ReworkCount
	dst.BlockerCount += src.BlockerCount
	dst.ResourceUsageCount += src.ResourceUsageCount
	dst.DiscussionReplyCount += src.DiscussionReplyCount
	dst.TokenConsumed += src.TokenConsumed
	dst.TaskHistory = append(dst.TaskHistory, src.TaskHistory...)
	dst.RepresentativeCases = append(dst.RepresentativeCases, src.RepresentativeCases...)
	if src.AverageDurationSeconds > 0 {
		if dst.AverageDurationSeconds == 0 {
			dst.AverageDurationSeconds = src.AverageDurationSeconds
		} else {
			dst.AverageDurationSeconds = (dst.AverageDurationSeconds + src.AverageDurationSeconds) / 2
		}
	}
	if src.LastActivityAt.After(dst.LastActivityAt) {
		dst.LastActivityAt = src.LastActivityAt
	}
	if dst.DisplayName == "" {
		dst.DisplayName = src.DisplayName
	}
	if dst.RoleName == "" {
		dst.RoleName = src.RoleName
	}
	if dst.Department == "" {
		dst.Department = src.Department
	}
	if dst.Position == "" {
		dst.Position = src.Position
	}
	if dst.RuntimeKind == "" {
		dst.RuntimeKind = src.RuntimeKind
	}
}

func finalizeEvaluationScores(item *EvaluationItemDTO) {
	item.QualityScore = qualityScore(*item)
	if item.AssignedTasks > 0 {
		item.OnTimeRate = float64(item.CompletedTasks) / float64(item.AssignedTasks)
	}
	item.CollaborationScore = math.Min(1, float64(item.DiscussionReplyCount+item.ResourceUsageCount+1)/float64(item.AssignedTasks+1))
	item.EfficiencyScore = efficiencyScore(*item)
	item.StabilityScore = stabilityScore(*item)
	for index := range item.TaskHistory {
		if item.TaskHistory[index].DurationSeconds == 0 {
			item.TaskHistory[index].DurationSeconds = item.AverageDurationSeconds
		}
	}
	item.Capabilities = capabilitiesForEvaluation(*item)
	item.Investment = investmentForEvaluation(*item)
	item.Trend = trendForEvaluation(*item)
	item.Summary = summaryForEvaluation(*item)
	item.Suggestions = suggestionsForEvaluation(*item)
	item.Advantages = advantagesForEvaluation(*item)
	item.Risks = risksForEvaluation(*item)
	item.Scenarios = scenariosForEvaluation(*item)
	if len(item.RepresentativeCases) == 0 {
		item.RepresentativeCases = casesForEvaluation(*item)
	}
}

func evaluationsFromMission(model projection.ReadModel, query EvaluationQuery) []EvaluationItemDTO {
	members := map[string]projection.TeamMemberView{}
	for _, member := range model.Team.Members {
		addEvaluationMemberLookup(members, member)
	}
	stats := map[string]*EvaluationItemDTO{}
	blockerRefsByAgent := map[string]map[string]struct{}{}
	for _, task := range model.TaskBoard.Tasks {
		agentID, ok := canonicalEvaluationAgentID(task.Assignee.String(), members)
		if !ok {
			continue
		}
		if query.EntityID != "" && query.EntityID != agentID {
			continue
		}
		item := stats[agentID]
		if item == nil {
			member := members[agentID]
			item = &EvaluationItemDTO{EntityType: "agent", EntityID: agentID, DisplayName: member.DisplayName, MissionID: model.Mission.MissionID.String(), RoleName: member.RoleName, Department: member.Department, Position: member.Position, OnTimeRate: 1, CollaborationScore: 1}
			stats[agentID] = item
		}
		item.AssignedTasks++
		if task.Status == projection.TaskStatusApproved {
			item.CompletedTasks++
		}
		if len(task.BlockerRefs) > 0 || task.Status == projection.TaskStatusBlocked {
			if blockerRefsByAgent[agentID] == nil {
				blockerRefsByAgent[agentID] = map[string]struct{}{}
			}
			if len(task.BlockerRefs) == 0 {
				blockerRefsByAgent[agentID]["task:"+task.TaskID.String()] = struct{}{}
			}
			for _, ref := range task.BlockerRefs {
				blockerRefsByAgent[agentID][ref] = struct{}{}
			}
		}
		if task.UpdatedAt.After(item.LastActivityAt) {
			item.LastActivityAt = task.UpdatedAt
		}
		taskSessions := runtimeSessionsForTask(model.RuntimeSessions, agentID, task.TaskID.String(), members)
		item.TaskHistory = append(item.TaskHistory, taskHistoryFromProjection(model, task, averageRuntimeDurationSeconds(taskSessions), totalRuntimeUsageTokens(taskSessions)))
	}
	for _, rework := range model.QA.ReworkRequests {
		for _, task := range model.TaskBoard.Tasks {
			if task.TaskID == rework.TaskID {
				if agentID, ok := canonicalEvaluationAgentID(task.Assignee.String(), members); ok {
					if item := stats[agentID]; item != nil {
						item.ReworkCount++
					}
				}
			}
		}
	}
	for _, blocker := range model.Blockers {
		agentID, ok := canonicalEvaluationAgentID(blocker.RaisedBy.String(), members)
		if !ok {
			continue
		}
		if stats[agentID] != nil {
			if blockerRefsByAgent[agentID] == nil {
				blockerRefsByAgent[agentID] = map[string]struct{}{}
			}
			blockerRefsByAgent[agentID][blocker.BlockerID] = struct{}{}
		}
	}
	for _, message := range model.Discussion.Messages {
		agentID, ok := canonicalEvaluationAgentID(message.AuthorID.String(), members)
		if !ok {
			continue
		}
		if query.EntityID != "" && query.EntityID != agentID {
			continue
		}
		if item := stats[agentID]; item != nil {
			item.DiscussionReplyCount++
			if message.PostedAt.After(item.LastActivityAt) {
				item.LastActivityAt = message.PostedAt
			}
		}
	}
	for _, evidence := range model.Evidence.EvidenceItems {
		agentID, ok := canonicalEvaluationAgentID(evidence.AgentID.String(), members)
		if !ok {
			continue
		}
		if query.EntityID != "" && query.EntityID != agentID {
			continue
		}
		if item := stats[agentID]; item != nil {
			if evidence.Kind == agentcore.EvidenceKindToolLog || evidence.ToolLogRef.ID != "" {
				item.ResourceUsageCount++
			}
			if evidence.SubmittedAt.After(item.LastActivityAt) {
				item.LastActivityAt = evidence.SubmittedAt
			}
		}
	}
	for _, session := range model.RuntimeSessions {
		agentID, ok := canonicalEvaluationAgentID(session.AgentID.String(), members)
		if !ok {
			continue
		}
		if query.EntityID != "" && query.EntityID != agentID {
			continue
		}
		if item := stats[agentID]; item != nil {
			if item.RuntimeKind == "" {
				item.RuntimeKind = session.RuntimeKind
			}
			duration := runtimeDurationSeconds(session)
			if duration > 0 {
				if item.AverageDurationSeconds == 0 {
					item.AverageDurationSeconds = duration
				} else {
					item.AverageDurationSeconds = (item.AverageDurationSeconds + duration) / 2
				}
			}
			if session.UpdatedAt.After(item.LastActivityAt) {
				item.LastActivityAt = session.UpdatedAt
			}
			item.TokenConsumed += totalRuntimeUsageTokens([]projection.RuntimeSessionView{session})
		}
	}
	out := make([]EvaluationItemDTO, 0, len(stats))
	for _, item := range stats {
		item.BlockerCount = len(blockerRefsByAgent[item.EntityID])
		finalizeEvaluationScores(item)
		if query.EntityType != "" && query.EntityType != item.EntityType {
			continue
		}
		out = append(out, *item)
	}
	return out
}

func taskHistoryFromProjection(model projection.ReadModel, task projection.TaskCardView, durationSeconds int, tokenConsumed int64) EvaluationTaskHistoryDTO {
	responsibility := strings.TrimSpace(task.WorkerPersonalGoalSummary)
	if responsibility == "" {
		responsibility = firstNonEmptyDeliverable(task.ExpectedDeliverables)
	}
	if responsibility == "" {
		responsibility = "按任务目标完成执行并提交结果"
	}
	return EvaluationTaskHistoryDTO{
		Goal:            firstNonEmptyText(task.Goal, model.Mission.FinalGoal, model.Mission.Title, task.TaskID.String()),
		Responsibility:  responsibility,
		Type:            taskTypeLabel(task),
		Result:          taskResultLabel(task),
		Quality:         taskQuality(task),
		TokenConsumed:   tokenConsumed,
		DurationSeconds: durationSeconds,
		Risk:            taskRiskLabel(task),
		Time:            task.UpdatedAt,
	}
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptyDeliverable(deliverables []projection.ExpectedDeliverableView) string {
	for _, deliverable := range deliverables {
		if strings.TrimSpace(deliverable.Title) != "" {
			return strings.TrimSpace(deliverable.Title)
		}
	}
	return ""
}

func taskTypeLabel(task projection.TaskCardView) string {
	if len(task.RequiredEvidence) > 0 {
		return "证据核查"
	}
	if len(task.ExpectedDeliverables) > 0 {
		return "交付审核"
	}
	if task.QAStatus != projection.QAStatusNone {
		return "质量复核"
	}
	return "任务执行"
}

func taskResultLabel(task projection.TaskCardView) string {
	switch task.Status {
	case projection.TaskStatusApproved:
		return "通过"
	case projection.TaskStatusReworkRequested, projection.TaskStatusRejected:
		return "需返工"
	case projection.TaskStatusBlocked:
		return "阻塞"
	case projection.TaskStatusSubmitted:
		return "待复核"
	case projection.TaskStatusCancelled:
		return "已取消"
	default:
		return "进行中"
	}
}

func taskQuality(task projection.TaskCardView) float64 {
	switch task.QAStatus {
	case projection.QAStatusApproved:
		return 5
	case projection.QAStatusRejected:
		return 2
	case projection.QAStatusRequested:
		return 3
	default:
		if task.Status == projection.TaskStatusApproved {
			return 4
		}
		return 3
	}
}

func taskRiskLabel(task projection.TaskCardView) string {
	if len(task.BlockerRefs) > 0 || task.Status == projection.TaskStatusBlocked {
		return "存在阻塞"
	}
	if task.QAStatus == projection.QAStatusRejected || task.Status == projection.TaskStatusReworkRequested {
		return "返工风险"
	}
	if len(task.RequiredEvidence) > 0 && task.LatestResultRef == "" && task.Status != projection.TaskStatusApproved {
		return "证据待补"
	}
	return ""
}

func runtimeDurationSeconds(session projection.RuntimeSessionView) int {
	if !session.CompletedAt.IsZero() && !session.StartedAt.IsZero() && session.CompletedAt.After(session.StartedAt) {
		return int(session.CompletedAt.Sub(session.StartedAt).Seconds())
	}
	if !session.UpdatedAt.IsZero() && !session.StartedAt.IsZero() && session.UpdatedAt.After(session.StartedAt) {
		return int(session.UpdatedAt.Sub(session.StartedAt).Seconds())
	}
	return 0
}

func runtimeSessionsForTask(sessions []projection.RuntimeSessionView, agentID string, taskID string, members map[string]projection.TeamMemberView) []projection.RuntimeSessionView {
	matched := make([]projection.RuntimeSessionView, 0)
	for _, session := range sessions {
		if session.TaskID.String() != taskID {
			continue
		}
		sessionAgentID, ok := canonicalEvaluationAgentID(session.AgentID.String(), members)
		if !ok || sessionAgentID != agentID {
			continue
		}
		matched = append(matched, session)
	}
	return matched
}

func averageRuntimeDurationSeconds(sessions []projection.RuntimeSessionView) int {
	total := 0
	count := 0
	for _, session := range sessions {
		duration := runtimeDurationSeconds(session)
		if duration <= 0 {
			continue
		}
		total += duration
		count++
	}
	if count == 0 {
		return 0
	}
	return total / count
}

func totalRuntimeUsageTokens(sessions []projection.RuntimeSessionView) int64 {
	total := int64(0)
	for _, session := range sessions {
		for _, usage := range session.Usage {
			total += usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheWriteTokens
		}
	}
	return total
}

func efficiencyScore(item EvaluationItemDTO) float64 {
	delivery := 0.0
	if item.AssignedTasks > 0 {
		delivery = float64(item.CompletedTasks) / float64(item.AssignedTasks)
	}
	durationPenalty := 0.0
	if item.AverageDurationSeconds > 0 {
		durationPenalty = math.Min(0.18, float64(item.AverageDurationSeconds)/3600)
	}
	return clamp01(delivery + 0.18 - durationPenalty)
}

func stabilityScore(item EvaluationItemDTO) float64 {
	if item.AssignedTasks == 0 {
		return 0
	}
	penalty := float64(item.BlockerCount+item.ReworkCount) / float64(item.AssignedTasks) * 0.22
	return clamp01(0.92 - penalty)
}

func capabilitiesForEvaluation(item EvaluationItemDTO) []EvaluationCapabilityDTO {
	return []EvaluationCapabilityDTO{
		{Name: "任务完成", Score: percentFloat(item.OnTimeRate)},
		{Name: "质量稳定", Score: percentFloat(item.QualityScore)},
		{Name: "协作响应", Score: percentFloat(item.CollaborationScore)},
		{Name: "运行效率", Score: percentFloat(item.EfficiencyScore)},
		{Name: "稳定性", Score: percentFloat(item.StabilityScore)},
		{Name: "资源使用", Score: percentFloat(math.Min(1, float64(item.ResourceUsageCount+1)/float64(item.AssignedTasks+1)))},
	}
}

func investmentForEvaluation(item EvaluationItemDTO) EvaluationInvestmentDTO {
	tokens := item.TokenConsumed
	return EvaluationInvestmentDTO{
		TokenConsumed: tokens,
		Summary:       "Token 只统计运行时真实用量；创造价值等待综合评价 Agent 估算人天。",
	}
}

func trendForEvaluation(item EvaluationItemDTO) []EvaluationTrendPointDTO {
	if len(item.TaskHistory) == 0 {
		return nil
	}
	byDate := map[string]*EvaluationTrendPointDTO{}
	for _, task := range item.TaskHistory {
		date := "未记录"
		if !task.Time.IsZero() {
			date = task.Time.Format("01-02")
		}
		point := byDate[date]
		if point == nil {
			point = &EvaluationTrendPointDTO{Date: date}
			byDate[date] = point
		}
		point.Tasks++
		point.Score += task.Quality * 20
		point.TokenConsumed += task.TokenConsumed
	}
	out := make([]EvaluationTrendPointDTO, 0, len(byDate))
	for _, point := range byDate {
		if point.Tasks > 0 {
			point.Score = math.Round(point.Score / float64(point.Tasks))
		}
		out = append(out, *point)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func summaryForEvaluation(item EvaluationItemDTO) string {
	return "结论：当前 Agent 适合继续在已验证场景中使用，并优先优化阻塞、返工和高 Token 任务。其近期任务完成、质量稳定、协作响应和资源使用已形成可追踪记录，可用于识别低效节点并安排后续优化。"
}

func suggestionsForEvaluation(item EvaluationItemDTO) []string {
	return []string{
		"优先复盘返工和阻塞任务，补齐规则或输入材料模板。",
		"将高 Token 任务拆分为更小的检查步骤，降低单次执行成本。",
		"沉淀高质量任务的交付样例，用于后续评估对比。",
	}
}

func advantagesForEvaluation(item EvaluationItemDTO) []string {
	out := []string{}
	if item.CompletedTasks > 0 {
		out = append(out, "已完成任务可追踪，具备持续评估基础")
	}
	if item.ResourceUsageCount > 0 {
		out = append(out, "能够产出工具或证据记录，便于复核")
	}
	if item.DiscussionReplyCount > 0 {
		out = append(out, "有协作响应记录，便于团队跟进")
	}
	if len(out) == 0 {
		out = append(out, "已接入人员目录，可纳入统一评估")
	}
	return out
}

func risksForEvaluation(item EvaluationItemDTO) []string {
	out := []string{}
	if item.BlockerCount > 0 {
		out = append(out, "存在阻塞任务，需要定位依赖或权限问题")
	}
	if item.ReworkCount > 0 {
		out = append(out, "存在返工记录，需要补充规则和验收标准")
	}
	if item.TokenConsumed > 0 && item.CompletedTasks == 0 {
		out = append(out, "已有 Token 消耗但完成产出不足")
	}
	if len(out) == 0 {
		out = append(out, "暂无明显风险，建议继续观察更多任务样本")
	}
	return out
}

func scenariosForEvaluation(item EvaluationItemDTO) []string {
	return []string{"规则明确的任务执行", "需要证据留痕的质量复核", "需要跨角色协作的任务推进"}
}

func casesForEvaluation(item EvaluationItemDTO) []EvaluationCaseDTO {
	out := []EvaluationCaseDTO{}
	for _, task := range item.TaskHistory {
		if task.Result != "通过" && task.Result != "待复核" {
			continue
		}
		out = append(out, EvaluationCaseDTO{Title: task.Goal, Result: task.Result, Summary: task.Responsibility, Date: task.Time})
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func percentFloat(value float64) float64 {
	return math.Round(clamp01(value) * 100)
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func addEvaluationMemberLookup(members map[string]projection.TeamMemberView, member projection.TeamMemberView) {
	agentID := stableEvaluationAgentID(member)
	if agentID == "" {
		return
	}
	for _, id := range []string{member.AgentID.String(), member.AgentProfileID.String(), member.AgentInstanceID.String()} {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		members[id] = member
	}
}

func canonicalEvaluationAgentID(id string, members map[string]projection.TeamMemberView) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", false
	}
	member, ok := members[id]
	if !ok {
		return "", false
	}
	agentID := stableEvaluationAgentID(member)
	if agentID == "" {
		return "", false
	}
	return agentID, true
}

func stableEvaluationAgentID(member projection.TeamMemberView) string {
	profileID := strings.TrimSpace(member.AgentProfileID.String())
	if profileID == "" {
		return ""
	}
	return profileID
}

func qualityScore(item EvaluationItemDTO) float64 {
	if item.AssignedTasks == 0 {
		return 0
	}
	score := float64(item.CompletedTasks) / float64(item.AssignedTasks)
	penalty := float64(item.ReworkCount+item.BlockerCount) * 0.1
	return math.Max(0, score-penalty)
}
