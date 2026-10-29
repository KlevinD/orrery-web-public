package daemon

import "time"

type AgentRunLease struct {
	RunID           string
	AgentID         string
	AgentProfileID  string
	AgentInstanceID string
	RuntimeID       string
	MissionID       string
	TaskID          string
	Status          RunStatus
	ClaimedBy       string
	LeaseExpiresAt  time.Time
	HeartbeatAt     time.Time
	Attempt         int
}

func LeaseFromRun(run AgentRun) AgentRunLease {
	return AgentRunLease{RunID: run.RunID.String(), AgentID: run.AgentID.String(), AgentProfileID: run.AgentProfileID.String(), AgentInstanceID: run.AgentInstanceID.String(), RuntimeID: run.RuntimeID, MissionID: run.MissionID, TaskID: run.TaskID, Status: run.Status, ClaimedBy: run.ClaimedBy, LeaseExpiresAt: run.LeaseExpiresAt, HeartbeatAt: run.HeartbeatAt, Attempt: run.Attempt}
}
