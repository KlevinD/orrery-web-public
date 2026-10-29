package main

import (
	"context"
	"encoding/hex"
	"strings"

	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform/filestore"
)

// Include tenant and exact runtime run in new host materializations. Legacy
// agent-runs keys lack tenant binding and are deliberately not guessed here.
func missionResultArtifactPrefix(tenant, run string) string {
	return "agent-runs/v2/" + scheduledArtifactHash(tenant, 64) + "/" + scheduledArtifactHash(run, 64) + "/"
}

type productionTaskArtifactReader struct{ store filestore.Store }

func (p productionTaskArtifactReader) OpenTaskArtifact(ctx context.Context, access improvement.TaskArtifactAccess) (improvement.TaskArtifactContent, error) {
	var content improvement.TaskArtifactContent
	ref := access.Reference
	if p.store == nil || access.ExecutionTenantID == "" || access.RunID == "" || ref.Kind == "url" {
		return content, improvement.ErrArtifactUnverifiable
	}
	var key, digest string
	var size int64
	if typed, ok := scheduledArtifactKeyFromTypedURI(ref.URI); ok {
		if !strings.HasPrefix(typed, "scheduled-runs/v1/"+scheduledArtifactHash(access.ExecutionTenantID, 16)+"/") {
			return content, improvement.ErrArtifactUnverifiable
		}
		_, size, digest, ok = scheduledArtifactMetadataFromTypedURI(ref.URI)
		if !ok {
			return content, improvement.ErrArtifactUnverifiable
		}
		key = typed
		if ref.SHA256 != "" && (ref.SHA256 != digest || ref.SizeBytes != size) {
			return content, improvement.ErrArtifactUnverifiable
		}
	} else {
		prefix := missionResultArtifactPrefix(access.ExecutionTenantID, access.RunID)
		decoded, err := hex.DecodeString(ref.SHA256)
		if !strings.HasPrefix(ref.ID, prefix) || err != nil || len(decoded) != 32 || strings.ToLower(ref.SHA256) != ref.SHA256 || ref.SizeBytes < 0 {
			return content, improvement.ErrArtifactUnverifiable
		}
		suffix := strings.TrimPrefix(ref.ID, prefix)
		if suffix != "result-output-"+ref.SHA256+".md" && suffix != "workspace-artifact-"+ref.SHA256 {
			return content, improvement.ErrArtifactUnverifiable
		}
		key, digest, size = ref.ID, ref.SHA256, ref.SizeBytes
	}
	reader, err := p.store.GetReader(ctx, key)
	if err != nil {
		return content, err
	}
	return improvement.TaskArtifactContent{Reader: reader, SHA256: digest, SizeBytes: size}, nil
}
