package core

// RuntimeSession and RuntimeResult are the narrow transport contract used by
// legacy CLI and remote OpenClaw transports. Native providers adapt them into
// the canonical SessionBackend event stream at the provider boundary.
type RuntimeSession struct {
	Messages <-chan RuntimeMessage
	Result   <-chan RuntimeResult
}

type ResultStatus string

const (
	ResultStatusCompleted ResultStatus = "completed"
	ResultStatusFailed    ResultStatus = "failed"
	ResultStatusAborted   ResultStatus = "aborted"
	ResultStatusTimeout   ResultStatus = "timeout"
	ResultStatusCancelled ResultStatus = "cancelled"
	ResultStatusBlocked   ResultStatus = "blocked"
)

type RuntimeResult struct {
	Status     ResultStatus
	Output     string
	Error      string
	DurationMs int64
	SessionID  string
	Usage      map[string]TokenUsage
	Metadata   map[string]string
}
