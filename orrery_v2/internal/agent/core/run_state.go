package core

import "time"

type SignalName string

const (
	SignalBeforeTaskReceived    SignalName = "BeforeTaskReceived"
	SignalAfterTaskReceived     SignalName = "AfterTaskReceived"
	SignalBeforeTaskAccepted    SignalName = "BeforeTaskAccepted"
	SignalAfterTaskAccepted     SignalName = "AfterTaskAccepted"
	SignalBeforeContextBuild    SignalName = "BeforeContextBuild"
	SignalAfterContextBuild     SignalName = "AfterContextBuild"
	SignalBeforeRuntimeStart    SignalName = "BeforeRuntimeStart"
	SignalRuntimeStarted        SignalName = "RuntimeStarted"
	SignalRuntimeProgress       SignalName = "RuntimeProgress"
	SignalBeforeRuntimeFinish   SignalName = "BeforeRuntimeFinish"
	SignalAfterRuntimeFinish    SignalName = "AfterRuntimeFinish"
	SignalBeforeResultNormalize SignalName = "BeforeResultNormalize"
	SignalAfterResultNormalize  SignalName = "AfterResultNormalize"
	SignalBeforeResultValidate  SignalName = "BeforeResultValidate"
	SignalAfterResultValidate   SignalName = "AfterResultValidate"
	SignalBeforeResultCommit    SignalName = "BeforeResultCommit"
	SignalAfterResultCommit     SignalName = "AfterResultCommit"
	SignalBeforeAgentReply      SignalName = "BeforeAgentReply"
	SignalAfterAgentReply       SignalName = "AfterAgentReply"
	SignalAgentBlocked          SignalName = "AgentBlocked"
	SignalAgentFailed           SignalName = "AgentFailed"
	SignalAgentCancelled        SignalName = "AgentCancelled"
	SignalAgentTimeout          SignalName = "AgentTimeout"
)

type Signal struct {
	Name           SignalName
	TenantID       string
	RunID          AgentRunID
	AgentID        AgentID
	AgentProfileID AgentProfileID
	MissionID      string
	TaskID         string
	At             time.Time
	Payload        map[string]string
}
