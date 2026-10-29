package missioncontrol

import (
	"context"
	"errors"
	"testing"
)

type advicePort struct {
	calls  int
	tenant string
	fail   bool
}

func (p *advicePort) RecoveryAdvice(_ context.Context, tenant string, session RuntimeSessionDTO) ([]OpsFocusDTO, error) {
	p.calls++
	p.tenant = tenant
	if p.fail {
		return nil, errors.New("diagnostics unavailable")
	}
	return []OpsFocusDTO{{FocusID: "advice:" + session.RunID, Summary: "Check evidence", MissionID: "forged", AgentID: "forged"}}, nil
}

func TestMissionRecoveryAdviceIsBoundedAndCannotChangeExecution(t *testing.T) {
	port := &advicePort{}
	service := &QueryService{recoveryAdvice: port}
	view := MissionOpsViewDTO{MissionID: "mission", Status: "running", CurrentFocus: []OpsFocusDTO{{FocusID: "original"}}, RuntimeSessions: []RuntimeSessionDTO{{RunID: "healthy", Status: "running", IsAlive: true}, {RunID: "done", Status: "completed"}, {RunID: "failed", AgentID: "agent", TaskID: "task", Status: "failed"}, {RunID: "stale", AgentID: "agent", IsStale: true, Status: "running"}}}
	service.applyRecoveryAdvice(context.Background(), &view, ViewerRef{TenantID: "tenant"})
	if port.calls != 2 || port.tenant != "tenant" || len(view.CurrentFocus) != 3 || view.Status != "running" || len(view.ActionCards) != 0 {
		t.Fatalf("unexpected advice effects: %+v", view)
	}
	for _, focus := range view.CurrentFocus[1:] {
		if focus.MissionID != "mission" || focus.AgentID != "agent" || focus.Kind != "recovery_advice" {
			t.Fatal("advice supplied operational identity")
		}
	}
	port.fail = true
	service.applyRecoveryAdvice(context.Background(), &view, ViewerRef{TenantID: "tenant"})
	if len(view.CurrentFocus) != 3 {
		t.Fatal("dependency failure changed view")
	}
	many := MissionOpsViewDTO{}
	for i := 0; i < 20; i++ {
		many.RuntimeSessions = append(many.RuntimeSessions, RuntimeSessionDTO{Status: "failed"})
	}
	port.calls = 0
	service.applyRecoveryAdvice(context.Background(), &many, ViewerRef{})
	if port.calls != 4 {
		t.Fatal("diagnostic read budget exceeded")
	}
}
