package lifecycle

type SignalName string

const (
	BeforeTaskReceived SignalName = "BeforeTaskReceived"
	AfterTaskReceived  SignalName = "AfterTaskReceived"
	BeforeTaskAccepted SignalName = "BeforeTaskAccepted"
	AfterTaskAccepted  SignalName = "AfterTaskAccepted"

	BeforeContextBuild SignalName = "BeforeContextBuild"
	AfterContextBuild  SignalName = "AfterContextBuild"

	BeforeRuntimeStart  SignalName = "BeforeRuntimeStart"
	RuntimeStarted      SignalName = "RuntimeStarted"
	RuntimeProgress     SignalName = "RuntimeProgress"
	BeforeRuntimeFinish SignalName = "BeforeRuntimeFinish"
	AfterRuntimeFinish  SignalName = "AfterRuntimeFinish"

	BeforeResultNormalize SignalName = "BeforeResultNormalize"
	AfterResultNormalize  SignalName = "AfterResultNormalize"
	BeforeResultValidate  SignalName = "BeforeResultValidate"
	AfterResultValidate   SignalName = "AfterResultValidate"
	BeforeResultCommit    SignalName = "BeforeResultCommit"
	AfterResultCommit     SignalName = "AfterResultCommit"

	BeforeAgentReply SignalName = "BeforeAgentReply"
	AfterAgentReply  SignalName = "AfterAgentReply"

	AgentBlocked   SignalName = "AgentBlocked"
	AgentFailed    SignalName = "AgentFailed"
	AgentCancelled SignalName = "AgentCancelled"
	AgentTimeout   SignalName = "AgentTimeout"
)

func AllSignalNames() []SignalName {
	return []SignalName{
		BeforeTaskReceived,
		AfterTaskReceived,
		BeforeTaskAccepted,
		AfterTaskAccepted,
		BeforeContextBuild,
		AfterContextBuild,
		BeforeRuntimeStart,
		RuntimeStarted,
		RuntimeProgress,
		BeforeRuntimeFinish,
		AfterRuntimeFinish,
		BeforeResultNormalize,
		AfterResultNormalize,
		BeforeResultValidate,
		AfterResultValidate,
		BeforeResultCommit,
		AfterResultCommit,
		BeforeAgentReply,
		AfterAgentReply,
		AgentBlocked,
		AgentFailed,
		AgentCancelled,
		AgentTimeout,
	}
}

func IsKnownSignalName(name SignalName) bool {
	for _, known := range AllSignalNames() {
		if name == known {
			return true
		}
	}
	return false
}

func IsExceptionSignal(name SignalName) bool {
	switch name {
	case AgentBlocked, AgentFailed, AgentCancelled, AgentTimeout:
		return true
	default:
		return false
	}
}
