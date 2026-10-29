package scheduler

import (
	"context"
	"fmt"
	"time"

	cronlib "github.com/robfig/cron/v3"
)

// NextOccurrence returns the first scheduled occurrence after now. Exhausted
// one-time schedules have no future occurrence.
func NextOccurrence(schedule Schedule, now time.Time) (time.Time, error) {
	switch schedule.Type {
	case ScheduleTypeOnce:
		if schedule.RunAt.IsZero() {
			return time.Time{}, fmt.Errorf("%w: run_at is required for once schedules", ErrInvalidSchedule)
		}
		if !schedule.RunAt.After(now) {
			return time.Time{}, nil
		}
		return schedule.RunAt.UTC(), nil
	case ScheduleTypeCron:
		return nextCronOccurrence(schedule, now)
	default:
		return time.Time{}, fmt.Errorf("%w: unsupported schedule type %q", ErrInvalidSchedule, schedule.Type)
	}
}

func nextCronOccurrence(schedule Schedule, now time.Time) (time.Time, error) {
	if schedule.Cron == "" {
		return time.Time{}, fmt.Errorf("%w: cron is required", ErrInvalidSchedule)
	}
	if schedule.Timezone == "" {
		return time.Time{}, fmt.Errorf("%w: timezone is required for cron schedules", ErrInvalidSchedule)
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid timezone %q", ErrInvalidSchedule, schedule.Timezone)
	}
	parser := cronlib.NewParser(cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow)
	expr, err := parser.Parse(schedule.Cron)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
	}
	next := expr.Next(now.In(location))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("%w: cron has no next run", ErrInvalidSchedule)
	}
	return next.UTC(), nil
}

type OccurrenceReconcileIssue struct {
	TaskID  string
	Message string
}

type OccurrenceReconcileReport struct {
	Repaired int
	Issues   []OccurrenceReconcileIssue
}

func (s *Service) ReconcileDisabledOccurrences(ctx context.Context, now time.Time) (OccurrenceReconcileReport, error) {
	if s == nil || s.repo == nil {
		return OccurrenceReconcileReport{}, ErrServiceDependency
	}
	if err := ctx.Err(); err != nil {
		return OccurrenceReconcileReport{}, err
	}
	now = now.UTC()
	tasks, err := s.repo.ListTasks(ctx, false)
	if err != nil {
		return OccurrenceReconcileReport{}, err
	}
	report := OccurrenceReconcileReport{}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if task.Enabled || task.Status == TaskStatusArchived || task.NextRunAt.After(now) {
			continue
		}
		next, err := NextOccurrence(task.Schedule, now)
		if err != nil {
			report.Issues = append(report.Issues, OccurrenceReconcileIssue{TaskID: task.ScheduledTaskID, Message: err.Error()})
			continue
		}
		if next.IsZero() {
			continue
		}
		task.NextRunAt = next
		if err := s.repo.saveTaskRecord(ctx, task); err != nil {
			return report, err
		}
		report.Repaired++
	}
	return report, nil
}
