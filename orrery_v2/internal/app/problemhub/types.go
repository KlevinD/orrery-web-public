package problemhub

import "time"

type Status string

const (
	StatusOpen        Status = "open"
	StatusTriaged     Status = "triaged"
	StatusContained   Status = "contained"
	StatusDiagnosed   Status = "diagnosed"
	StatusInProgress  Status = "in_progress"
	StatusMonitoring  Status = "monitoring"
	StatusResolved    Status = "resolved"
	StatusEscalated   Status = "escalated"
	StatusQuarantined Status = "quarantined"
)

type ActorRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type ObjectRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Observation struct {
	ID              string
	TenantID        string
	Fingerprint     string
	SourceDomain    string
	Source          ObjectRef
	Category        string
	Severity        string
	Title           string
	RedactedSummary string
	EvidenceID      string
	Actor           ActorRef
	CorrelationID   string
	CausationID     string
	OccurredAt      time.Time
}

type ProblemCase struct {
	ID                   string      `json:"id"`
	TenantID             string      `json:"tenant_id"`
	Fingerprint          string      `json:"fingerprint"`
	SourceDomain         string      `json:"source_domain"`
	Category             string      `json:"category"`
	Severity             string      `json:"severity"`
	Status               Status      `json:"status"`
	Title                string      `json:"title"`
	RedactedSummary      string      `json:"redacted_summary"`
	Owner                ActorRef    `json:"owner"`
	PrimarySource        ObjectRef   `json:"primary_source"`
	SourceRefs           []ObjectRef `json:"source_refs"`
	EvidenceIDs          []string    `json:"evidence_ids"`
	OccurrenceCount      int64       `json:"occurrence_count"`
	LatestOutcomeID      string      `json:"latest_outcome_id,omitempty"`
	LatestOutcomeSuccess bool        `json:"latest_outcome_success"`
	LatestOutcomeSummary string      `json:"latest_outcome_summary,omitempty"`
	LatestResolution     ObjectRef   `json:"latest_resolution"`
	LessonCandidateID    string      `json:"lesson_candidate_id,omitempty"`
	Version              int64       `json:"version"`
	FirstSeenAt          time.Time   `json:"first_seen_at"`
	LastSeenAt           time.Time   `json:"last_seen_at"`
	ResolvedAt           *time.Time  `json:"resolved_at,omitempty"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
}

type Outcome struct {
	ID              string
	TenantID        string
	ProblemCaseID   string
	Source          ObjectRef
	Success         bool
	Resolution      ObjectRef
	RedactedSummary string
	EvidenceID      string
	Actor           ActorRef
	CorrelationID   string
	CausationID     string
	OccurredAt      time.Time
}

type RouteCommand struct {
	ID            string
	TenantID      string
	ProblemCaseID string
	Owner         ActorRef
	Actor         ActorRef
	OccurredAt    time.Time
}

type ListQuery struct {
	TenantID     string
	Status       Status
	OwnerKind    string
	OwnerID      string
	SourceDomain string
	Search       string
	Limit        int
}

type LessonProposal struct {
	ID            string
	TenantID      string
	ProblemCaseID string
	Topic         string
	Summary       string
	Lesson        string
	Warning       string
	EvidenceRefs  []string
	LineageRefs   []string
	Confidence    float64
	CreatedBy     string
	CreatedAt     time.Time
}

type Event struct {
	ID            string
	TenantID      string
	Type          string
	StreamID      string
	Version       int64
	CorrelationID string
	CausationID   string
	OccurredAt    time.Time
	Actor         ActorRef
	Payload       map[string]any
}

const (
	EventProblemCaseUpserted     = "problem.case.upserted"
	EventLessonCandidateProposed = "problem.lesson_candidate.proposed"
)

func (c ProblemCase) Clone() ProblemCase {
	c.SourceRefs = append([]ObjectRef(nil), c.SourceRefs...)
	c.EvidenceIDs = append([]string(nil), c.EvidenceIDs...)
	if c.ResolvedAt != nil {
		resolvedAt := *c.ResolvedAt
		c.ResolvedAt = &resolvedAt
	}
	return c
}
