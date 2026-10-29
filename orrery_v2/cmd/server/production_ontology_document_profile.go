package main

import (
	"context"
	"encoding/json"
	"slices"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/permissionscope"
	"orrery_v2/internal/platform/db"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func documentProfileHash(profile agentcore.AgentProfile, agent string) (string, error) {
	if string(profile.ID) != agent || !profile.Enabled || profile.AgentKind == agentcore.AgentKindHuman {
		return "", ontologyscope.ErrAccessDenied
	}
	for _, grant := range []struct {
		id    agentcore.ToolID
		scope string
	}{
		{toolstd.OntologyInfer, permissionscope.OntologyManage}, {toolstd.OntologyInferenceWorkflow, permissionscope.OntologyManage}, {toolstd.OntologyInferenceRules, permissionscope.OntologyManage},
		{toolstd.OntologyDocumentInference, permissionscope.OntologyManage}, {toolstd.KnowledgeSearch, "knowledge.read"},
	} {
		if !slices.ContainsFunc(profile.ToolRefs, func(ref agentcore.ToolRef) bool {
			return ref.ToolID == grant.id && permissionscope.AnyAllows([]string{string(ref.PermissionScope)}, grant.scope)
		}) {
			return "", ontologyscope.ErrAccessDenied
		}
	}
	return inferenceDigest([]any{profile.ID, profile.Enabled, profile.AgentKind, profile.ToolRefs}), nil
}

// Lock the actual profile row until commit. Current grants cannot be revoked
// between the last authorization check and the document command's effects.
func (w *productionOntologyDocumentInference) fenceProfile(ctx context.Context, tx db.Tx, agent, expected string) error {
	var hash string
	if _, persistent := w.api.profiles.(*agentcore.PlatformProfileRepository); persistent {
		query := `SELECT payload_json FROM platform_records WHERE table_name='agent_profiles' AND record_key=?`
		if w.store.Core.Dialect() == db.DialectPostgres {
			query += ` FOR SHARE`
		}
		var raw string
		if err := tx.QueryRowContext(ctx, query, agent).Scan(&raw); err != nil {
			return ontologyscope.ErrAccessDenied
		}
		var record struct {
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return err
		}
		var profile agentcore.AgentProfile
		if err := json.Unmarshal([]byte(record.Payload), &profile); err != nil {
			return err
		}
		var err error
		hash, err = documentProfileHash(profile, agent)
		if err != nil {
			return err
		}
	} else {
		profile, err := w.api.profiles.GetProfile(ctx, agentcore.AgentProfileID(agent))
		if err != nil {
			return err
		}
		hash, err = documentProfileHash(profile, agent)
		if err != nil {
			return err
		}
	}
	if hash != expected {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}
