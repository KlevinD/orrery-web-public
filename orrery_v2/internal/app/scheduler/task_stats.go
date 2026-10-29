package scheduler

import "context"

type ScheduledTaskStats struct {
	Total     int `json:"total"`
	Enabled   int `json:"enabled"`
	Executors int `json:"executors"`
	System    int `json:"system"`
	Failed    int `json:"failed"`
}

func (s *Service) GetTaskStats(ctx context.Context, tenantID string) (ScheduledTaskStats, error) {
	if s == nil || s.repo == nil {
		return ScheduledTaskStats{}, ErrServiceDependency
	}
	tasks, err := s.repo.ListTasks(ctx, true)
	if err != nil {
		return ScheduledTaskStats{}, err
	}
	tasks = filterTasks(tasks, ListTasksQuery{TenantID: normalizeTenantID(tenantID)})
	taskIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ScheduledTaskID)
	}
	lastRuns, err := s.repo.LastRunsForTasks(ctx, taskIDs)
	if err != nil {
		return ScheduledTaskStats{}, err
	}

	stats := ScheduledTaskStats{}
	executors := make(map[string]struct{})
	for _, task := range tasks {
		// Preserve latest-run failure counts for archived tasks as well.
		if lastRuns[task.ScheduledTaskID].Status == RunStatusFailed {
			stats.Failed++
		}
		if task.Status == TaskStatusArchived {
			continue
		}
		stats.Total++
		if task.Enabled {
			stats.Enabled++
		}
		if agentID := executorAgentID(task.Target); agentID != "" {
			executors[agentID] = struct{}{}
		} else {
			stats.System++
		}
	}
	stats.Executors = len(executors)
	return stats, nil
}
