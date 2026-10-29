package websockettransport

type CloseReason string

const (
	CloseReasonHeartbeatTimeout CloseReason = "heartbeat_timeout"
	CloseReasonSlowClient       CloseReason = "slow_client"
	CloseReasonUnsubscribed     CloseReason = "unsubscribed"
)
