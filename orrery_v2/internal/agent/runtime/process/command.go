package process

import "time"

type Command struct {
	Executable string
	Args       []string
	Dir        string
	Env        map[string]string
	Stdin      string
	Timeout    time.Duration
}

type Status string

const (
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusTimeout   Status = "timeout"
	StatusCancelled Status = "cancelled"
)

type Result struct {
	Status      Status
	ExitCode    int
	Stdout      string
	StderrTail  string
	StartedAt   time.Time
	CompletedAt time.Time
	Duration    time.Duration
	Error       string
}
