package main

import (
	"context"
	"encoding/json"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/resourceadmin"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type productionLearningAdvice struct {
	samples *resourceadmin.PlatformRSILearningSampleStore
}

func (p productionLearningAdvice) ExecuteRSILearningTool(ctx context.Context, req mcptransport.RSILearningToolRequest) (map[string]any, error) {
	if p.samples == nil {
		return nil, mcptransport.ErrServiceDependency
	}
	scope, err := resourceadmin.RSILearningScope(req.TenantID, req.AgentID)
	if err != nil {
		return nil, mcptransport.ErrPermissionDenied
	}
	// Identities, prompts and raw evidence cannot be supplied through arguments.
	for key := range req.Arguments {
		if key != "limit" && key != "after" {
			return nil, mcptransport.ErrInvalidRequest
		}
	}
	limit := 30
	if value, ok := req.Arguments["limit"]; ok {
		switch value := value.(type) {
		case float64:
			if value < 1 || value > 100 || value != float64(int(value)) {
				return nil, mcptransport.ErrInvalidRequest
			}
			limit = int(value)
		case int:
			limit = value
		default:
			return nil, mcptransport.ErrInvalidRequest
		}
	}
	after := ""
	if value, ok := req.Arguments["after"]; ok {
		var valid bool
		after, valid = value.(string)
		if !valid {
			return nil, mcptransport.ErrInvalidRequest
		}
	}
	plan, err := p.samples.LearningPlan(ctx, scope, after, limit)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var output map[string]any
	if err = json.Unmarshal(raw, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func (p productionLearningAdvice) RecoveryAdvice(ctx context.Context, tenant string, session missioncontrol.RuntimeSessionDTO) ([]missioncontrol.OpsFocusDTO, error) {
	scope, err := resourceadmin.RSILearningScope(tenant, session.AgentID)
	if err != nil {
		return nil, err
	}
	advice, err := p.samples.RunAdvice(ctx, scope, session.RunID, 3)
	if err != nil {
		return nil, err
	}
	out := make([]missioncontrol.OpsFocusDTO, 0, len(advice))
	if len(advice) == 0 && session.IsStale {
		out = append(out, missioncontrol.OpsFocusDTO{FocusID: "runtime-health:" + session.RunID, Kind: "recovery_advice", Priority: "normal", Title: "运行心跳需要核实", Summary: "当前运行租约已过期：先核对进程、心跳和任务实际进度，再通过现有控制流程恢复；不能仅据心跳判定业务执行失败。", TaskID: session.TaskID, AgentID: session.AgentID, SourceType: "runtime_health", SourceID: session.RunID, CreatedAt: session.UpdatedAt})
	}
	for _, item := range advice {
		out = append(out, missioncontrol.OpsFocusDTO{FocusID: "learning-advice:" + item.ObservationID, Kind: "recovery_advice", Priority: "normal", Title: "失败核查建议（待验证）", Summary: item.Summary, TaskID: session.TaskID, AgentID: session.AgentID, SourceType: "rsi_observation", SourceID: item.ObservationID, CreatedAt: item.At})
	}
	return out, nil
}
