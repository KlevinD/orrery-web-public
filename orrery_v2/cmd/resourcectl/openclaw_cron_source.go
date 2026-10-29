package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"orrery_v2/internal/migration/nativescheduled"
)

type openClawCronSource struct {
	binary  string
	allowed map[string]bool
}

func newOpenClawCronSource(binary string) (*openClawCronSource, error) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return nil, fmt.Errorf("OpenClaw binary is required for source cutover")
	}
	allowed := make(map[string]bool, len(nativescheduled.Catalog()))
	for _, entry := range nativescheduled.Catalog() {
		allowed[entry.SourceJobID] = true
	}
	return &openClawCronSource{binary: binary, allowed: allowed}, nil
}

func (s *openClawCronSource) List(ctx context.Context) ([]nativescheduled.SourceScheduleState, error) {
	command := exec.CommandContext(ctx, s.binary, "cron", "list", "--all", "--json")
	raw, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list OpenClaw source schedules: %w", err)
	}
	var payload struct {
		Jobs []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
			State   struct {
				RunningAtMS int64 `json:"runningAtMs"`
			} `json:"state"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode OpenClaw source schedules: %w", err)
	}
	states := make([]nativescheduled.SourceScheduleState, 0, len(payload.Jobs))
	for _, job := range payload.Jobs {
		if !s.allowed[job.ID] {
			return nil, fmt.Errorf("OpenClaw returned unrecognized source schedule %q", job.ID)
		}
		states = append(states, nativescheduled.SourceScheduleState{
			ID: job.ID, Enabled: job.Enabled,
			Active: job.State.RunningAtMS > 0 || strings.EqualFold(strings.TrimSpace(job.Status), "running"),
		})
	}
	return states, nil
}

func (s *openClawCronSource) Disable(ctx context.Context, id string) error {
	return s.mutate(ctx, "disable", id)
}

func (s *openClawCronSource) Enable(ctx context.Context, id string) error {
	return s.mutate(ctx, "enable", id)
}

func (s *openClawCronSource) mutate(ctx context.Context, action, id string) error {
	if !s.allowed[id] {
		return fmt.Errorf("refuse to %s unrecognized source schedule %q", action, id)
	}
	command := exec.CommandContext(ctx, s.binary, "cron", action, id, "--expect-final", "--timeout", "30000")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("OpenClaw cron %s %s: %w: %s", action, id, err, strings.TrimSpace(string(output)))
	}
	return nil
}

var _ nativescheduled.SourceScheduler = (*openClawCronSource)(nil)
