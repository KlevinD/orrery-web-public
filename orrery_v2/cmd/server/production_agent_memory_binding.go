package main

import (
	"context"
	"fmt"

	"orrery_v2/internal/agent/assembly"
	memretrieval "orrery_v2/internal/memory/retrieval"
)

type disabledProductionMemoryBinder struct{}

func (disabledProductionMemoryBinder) Bind(context.Context, assembly.MemoryBindRequest) (assembly.MemoryBinding, error) {
	return assembly.MemoryBinding{}, nil
}

type productionMemoryBinder struct {
	inner *memretrieval.Binder
}

func (b productionMemoryBinder) Bind(ctx context.Context, req assembly.MemoryBindRequest) (assembly.MemoryBinding, error) {
	if b.inner == nil {
		return assembly.MemoryBinding{}, fmt.Errorf("memory binder is not configured")
	}
	subject := req.Subject
	if subject.AgentID == "" {
		subject.AgentID = req.AgentID
	}
	binding, err := b.inner.Bind(ctx, memretrieval.MemoryBindRequest{
		AgentID:               req.AgentID,
		MissionID:             req.MissionID,
		TaskID:                req.TaskID,
		Subject:               subject,
		PersonalProfileRef:    req.PersonalProfileRef,
		CollectiveProfileRefs: req.CollectiveProfileRefs,
	})
	if err != nil {
		return assembly.MemoryBinding{}, err
	}
	warnings := make([]assembly.MemoryBindingWarning, 0, len(binding.Warnings))
	for _, warning := range binding.Warnings {
		warnings = append(warnings, assembly.MemoryBindingWarning{Code: warning.Code, Message: warning.Message})
	}
	return assembly.MemoryBinding{
		PersonalDigest:    binding.PersonalDigest,
		CollectiveDigests: binding.CollectiveDigests,
		Warnings:          warnings,
	}, nil
}
