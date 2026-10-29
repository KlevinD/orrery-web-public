package problemhub

import (
	"context"
	"time"
)

type Repository interface {
	Observe(context.Context, Observation, ProblemCase) (ProblemCase, bool, error)
	Get(context.Context, string, string) (ProblemCase, error)
	GetBySource(context.Context, string, ObjectRef) (ProblemCase, error)
	List(context.Context, ListQuery) ([]ProblemCase, error)
	SyncStatusBySource(context.Context, string, ObjectRef, Status, time.Time) (ProblemCase, bool, error)
	RecordOutcome(context.Context, Outcome, time.Time) (ProblemCase, bool, error)
	Route(context.Context, RouteCommand, time.Time) (ProblemCase, bool, error)
	SetLessonCandidate(context.Context, string, string, int64, string, time.Time) (ProblemCase, bool, error)
}

type EventSink interface {
	PublishProblemHubEvent(context.Context, Event) error
}

type EventReplaySink interface {
	ReplayProblemHubEvent(context.Context, Event) error
}

type LessonSink interface {
	ProposeProblemLesson(context.Context, LessonProposal) (string, error)
}
