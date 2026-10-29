package core

import "time"

type AgentQualitySignal struct {
	AgentProfileID AgentProfileID
	Score          float64
	SampleSize     int
	EvidenceRefs   []EvidenceRef
	LastSuccessAt  time.Time
	LastFailureAt  time.Time
}
