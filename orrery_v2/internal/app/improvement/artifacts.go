package improvement

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"orrery_v2/internal/platform/db"
)

const artifactTable = "ontology_improvement_artifacts"

// ArtifactVault is tenant-scoped, content-addressed storage. A seal is issued
// only by the host experiment coordinator; the model and task container never
// receive the key or database access. This is not a resource-apply authority.
type ArtifactVault struct {
	core *db.Store
	key  []byte
}

type storedArtifact struct {
	TenantID string          `json:"tenant_id"`
	Kind     string          `json:"kind"`
	Body     json.RawMessage `json:"body"`
	Seal     string          `json:"seal,omitempty"`
}

func NewArtifactVault(core *db.Store, sealKey []byte) (*ArtifactVault, error) {
	if core == nil || len(sealKey) < 32 {
		return nil, fmt.Errorf("%w: database and at least 32 bytes of host seal key are required", ErrInvalid)
	}
	return &ArtifactVault{core: core, key: append([]byte(nil), sealKey...)}, nil
}

func (v *ArtifactVault) signature(a storedArtifact) string {
	a.Seal = ""
	mac := hmac.New(sha256.New, v.key)
	_, _ = mac.Write([]byte(Digest(a)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (v *ArtifactVault) put(ctx context.Context, tenant, kind string, body any, sealed bool) (ArtifactRef, error) {
	if v == nil || v.core == nil || !textOK(tenant, 256) || !textOK(kind, 128) {
		return ArtifactRef{}, ErrInvalid
	}
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 8<<20 {
		return ArtifactRef{}, fmt.Errorf("%w: artifact must be valid bounded JSON", ErrInvalid)
	}
	a := storedArtifact{TenantID: tenant, Kind: kind, Body: raw}
	if sealed {
		a.Seal = v.signature(a)
	}
	ref := ArtifactRef{SHA256: Digest(a)}
	ref.ID = "improvement-artifact:" + ref.SHA256
	key := tenantPrefix(tenant) + ref.SHA256
	err = v.core.WithTx(ctx, func(tx db.Tx) error {
		created, err := db.InsertRecordTx(ctx, tx, artifactTable, key, encode(a))
		if err != nil || created {
			return err
		}
		existing, exists, err := db.GetRecordTx(ctx, tx, artifactTable, key)
		if err != nil {
			return err
		}
		got, err := decode[storedArtifact](existing)
		if !exists || err != nil || Digest(got) != ref.SHA256 {
			return ErrConflict
		}
		return nil
	})
	return ref, err
}

func (v *ArtifactVault) read(ctx context.Context, tenant string, ref ArtifactRef, kind string, sealed bool, into any) error {
	if v == nil || !textOK(tenant, 256) || !artifactOK(ref) || ref.ID != "improvement-artifact:"+ref.SHA256 {
		return ErrInvalid
	}
	record, exists, err := v.core.GetRecord(ctx, artifactTable, tenantPrefix(tenant)+ref.SHA256)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	a, err := decode[storedArtifact](record)
	if err != nil || a.TenantID != tenant || a.Kind != kind || Digest(a) != ref.SHA256 {
		return fmt.Errorf("%w: artifact binding failed", ErrInvalid)
	}
	if sealed && !hmac.Equal([]byte(a.Seal), []byte(v.signature(a))) {
		return fmt.Errorf("%w: trusted executor seal failed", ErrInvalid)
	}
	if err := json.Unmarshal(a.Body, into); err != nil {
		return ErrInvalid
	}
	return nil
}
