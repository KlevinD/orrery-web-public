package main

import (
	"encoding/json"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	agentidentity "orrery_v2/internal/agent/identity"
	"orrery_v2/internal/platform/config"
)

const defaultDiscussionAgentTimeout = 20 * time.Minute

func configuredDiscussionAgentID(cfg config.Config) string {
	if value, ok := cfg.String("discussion.agent_id"); ok && strings.TrimSpace(value) != "" {
		return agentidentity.CanonicalProfileID(value)
	}
	return defaultLeadAgentID
}

func configuredDiscussionAgentTimeout(cfg config.Config) time.Duration {
	if timeout, ok := cfg.Duration("discussion.agent.timeout"); ok && timeout > 0 {
		return timeout
	}
	return defaultDiscussionAgentTimeout
}

func defaultAgentRuntimeSpec(cfg config.Config, agentID string) agentcore.RuntimeSpec {
	agentID = agentidentity.CanonicalProfileID(agentID)
	if agentID == "" {
		return defaultFallbackAgentRuntimeSpec(cfg)
	}
	if spec := runtimeSpecFromConfigPath(cfg, "agents."+agentID); spec.HasKind() {
		return spec
	}
	legacyAgentID := agentidentity.LegacyProfileID(agentID)
	if legacyAgentID != agentID {
		if spec := runtimeSpecFromConfigPath(cfg, "agents."+legacyAgentID); spec.HasKind() {
			return spec
		}
	}
	return defaultFallbackAgentRuntimeSpec(cfg)
}

func defaultFallbackAgentRuntimeSpec(cfg config.Config) agentcore.RuntimeSpec {
	spec := runtimeSpecFromConfigPath(cfg, "agent_defaults")
	if !spec.HasKind() {
		spec.Kind = agentcore.RuntimeKindPi
		spec.ConfigRef = configString(cfg, "agent_defaults.runtime_config_ref")
		spec.Config = runtimeConfigRaw(cfg, "agent_defaults.runtime_config")
	}
	return spec
}

func runtimeSpecFromConfigPath(cfg config.Config, path string) agentcore.RuntimeSpec {
	kind, ok := cfg.String(path + ".runtime_kind")
	spec := agentcore.RuntimeSpec{}
	if !ok || strings.TrimSpace(kind) == "" {
		return spec
	}
	spec.Kind = agentcore.RuntimeKind(strings.TrimSpace(kind))
	if value, ok := cfg.String(path + ".runtime_config_ref"); ok {
		spec.ConfigRef = strings.TrimSpace(value)
	}
	if raw, ok := cfg.Lookup(path + ".runtime_config"); ok && raw != nil {
		if encoded, err := json.Marshal(raw); err == nil && string(encoded) != "null" {
			spec.Config = encoded
		}
	}
	return spec
}

func configString(cfg config.Config, path string) string {
	value, _ := cfg.String(path)
	return strings.TrimSpace(value)
}

func runtimeConfigRaw(cfg config.Config, path string) json.RawMessage {
	raw, ok := cfg.Lookup(path)
	if !ok || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil || string(encoded) == "null" {
		return nil
	}
	return encoded
}
