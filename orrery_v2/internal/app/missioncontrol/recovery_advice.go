package missioncontrol

import (
	"context"
	"time"
)

// RecoveryAdvicePort is called only with sessions from the authorized Mission.
// It returns display advice, never commands or state transitions.
type RecoveryAdvicePort interface {
	RecoveryAdvice(context.Context, string, RuntimeSessionDTO) ([]OpsFocusDTO, error)
}

func (s *QueryService) SetRecoveryAdvicePort(port RecoveryAdvicePort) {
	if s != nil {
		s.recoveryAdvice = port
	}
}

func (s *QueryService) applyRecoveryAdvice(ctx context.Context, view *MissionOpsViewDTO, viewer ViewerRef) {
	if s.recoveryAdvice == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	added, checked := 0, 0
	seen := map[string]bool{}
	for _, focus := range view.CurrentFocus {
		seen[focus.FocusID] = true
	}
	for _, session := range view.RuntimeSessions {
		// Healthy or completed sessions must not display historical failure hints.
		if session.Status != "failed" && !session.IsStale {
			continue
		}
		if checked >= 4 || added >= 4 || ctx.Err() != nil {
			break
		}
		checked++
		rows, err := s.recoveryAdvice.RecoveryAdvice(ctx, viewer.Tenant(), session)
		if err != nil {
			continue
		} // Diagnostics must not make the operational view fail.
		for _, row := range rows {
			if row.FocusID == "" || seen[row.FocusID] || added >= 4 {
				continue
			}
			row.MissionID = view.MissionID
			row.TaskID, row.AgentID = session.TaskID, session.AgentID
			row.Kind = "recovery_advice"
			view.CurrentFocus = append(view.CurrentFocus, row)
			seen[row.FocusID] = true
			added++
		}
	}
}
