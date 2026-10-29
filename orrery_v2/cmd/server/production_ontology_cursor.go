package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"orrery_v2/internal/app/authorization"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/platform/secret"
)

const (
	ontologyCursorPrimaryKeyID      = "v1"
	ontologyCursorPrimaryKeySecret  = "ontology_cursor_" + "key_v1"
	ontologyCursorPreviousKeyID     = "v0"
	ontologyCursorPreviousKeySecret = "ontology_cursor_" + "key_v0"
	ontologyStateCursorMaxEntries   = 10000
)

type productionOntologyCursorState struct {
	Encrypted *ontologycontract.CursorCodec
	Stateful  *ontologycontract.StateCursorStore
}

func newProductionOntologyCursorState(
	ctx context.Context,
	provider secret.Provider,
	invalidations *authorization.InvalidationHub,
	requireEncryptionKey bool,
) (*productionOntologyCursorState, error) {
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: ontologyStateCursorMaxEntries})
	if err != nil {
		return nil, fmt.Errorf("create ontology state cursor store: %w", err)
	}
	state := &productionOntologyCursorState{Stateful: stateful}
	if invalidations != nil {
		invalidations.Subscribe(func(authorization.InvalidationEvent) {
			stateful.InvalidateAll()
		})
	}
	if provider == nil {
		if requireEncryptionKey {
			return nil, fmt.Errorf("ontology cursor encryption key is required")
		}
		return state, nil
	}

	primary, err := readOntologyCursorKey(ctx, provider, ontologyCursorPrimaryKeySecret, requireEncryptionKey)
	if err != nil {
		return nil, err
	}
	if len(primary) == 0 {
		return state, nil
	}
	defer clearBytes(primary)
	keys := []ontologycontract.CursorKey{{ID: ontologyCursorPrimaryKeyID, Key: primary}}

	previous, err := readOntologyCursorKey(ctx, provider, ontologyCursorPreviousKeySecret, false)
	if err != nil {
		return nil, err
	}
	if len(previous) > 0 {
		defer clearBytes(previous)
		keys = append(keys, ontologycontract.CursorKey{ID: ontologyCursorPreviousKeyID, Key: previous})
	}
	codec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{
		PrimaryID: ontologyCursorPrimaryKeyID,
		Keys:      keys,
	})
	if err != nil {
		return nil, fmt.Errorf("create ontology cursor codec: %w", err)
	}
	state.Encrypted = codec
	return state, nil
}

func readOntologyCursorKey(ctx context.Context, provider secret.Provider, name string, required bool) ([]byte, error) {
	value, err := provider.Get(ctx, name)
	if err != nil {
		if secret.IsNotFound(err) && !required {
			return nil, nil
		}
		return nil, fmt.Errorf("read ontology cursor key %s: %w", name, err)
	}
	encoded := strings.TrimSpace(value.Plaintext())
	if encoded == "" && !required {
		return nil, nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != 32 {
		clearBytes(key)
		return nil, fmt.Errorf("ontology cursor key %s must be strict base64 encoding of exactly 32 bytes", name)
	}
	return key, nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
