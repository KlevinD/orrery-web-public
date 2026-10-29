package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/app/authorization"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/platform/secret"
)

func TestProductionOntologyCursorRequiresValidProductionSecret(t *testing.T) {
	ctx := context.Background()
	if _, err := newProductionOntologyCursorState(ctx, secret.NewMemoryProvider(nil), nil, true); err == nil {
		t.Fatal("missing production cursor key returned nil error")
	}
	plaintext := "not-secret-base64-material"
	if _, err := newProductionOntologyCursorState(ctx, secret.NewMemoryProvider(map[string]string{
		ontologyCursorPrimaryKeySecret: plaintext,
	}), nil, true); err == nil || strings.Contains(err.Error(), plaintext) {
		t.Fatalf("invalid key error = %v", err)
	}
	state, err := newProductionOntologyCursorState(ctx, secret.NewMemoryProvider(map[string]string{
		ontologyCursorPrimaryKeySecret:  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		ontologyCursorPreviousKeySecret: "",
	}), nil, true)
	if err != nil {
		t.Fatalf("new cursor state: %v", err)
	}
	if state.Encrypted == nil || state.Stateful == nil {
		t.Fatalf("cursor state = %+v", state)
	}
}

func TestProductionOntologyCursorSupportsPreviousKeyAndInvalidation(t *testing.T) {
	ctx := context.Background()
	previousKey := bytes.Repeat([]byte{3}, 32)
	oldCodec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{
		PrimaryID: ontologyCursorPreviousKeyID,
		Keys:      []ontologycontract.CursorKey{{ID: ontologyCursorPreviousKeyID, Key: previousKey}},
	})
	if err != nil {
		t.Fatalf("old codec: %v", err)
	}
	binding := productionCursorTestBinding()
	oldToken, err := oldCodec.Encode(ontologycontract.CursorPosition{Binding: binding, Seek: []string{"object-1"}})
	if err != nil {
		t.Fatalf("encode old token: %v", err)
	}
	hub := authorization.NewInvalidationHub()
	state, err := newProductionOntologyCursorState(ctx, secret.NewMemoryProvider(map[string]string{
		ontologyCursorPrimaryKeySecret:  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)),
		ontologyCursorPreviousKeySecret: base64.StdEncoding.EncodeToString(previousKey),
	}), hub, true)
	if err != nil {
		t.Fatalf("new rotated cursor state: %v", err)
	}
	if _, err := state.Encrypted.Decode(oldToken, binding); err != nil {
		t.Fatalf("decode previous key token: %v", err)
	}
	binding.AuthorizationRevision = ""
	stateToken, err := state.Stateful.Issue(ontologycontract.CursorPosition{Binding: binding, Seek: []string{"object-1"}})
	if err != nil {
		t.Fatalf("issue state token: %v", err)
	}
	hub.AuthorizationRevisionAdvanced(2)
	if _, err := state.Stateful.Resolve(stateToken, binding); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("invalidated state token error = %v", err)
	}
}

func TestProductionOntologyCursorAllowsStateOnlyInDevelopment(t *testing.T) {
	state, err := newProductionOntologyCursorState(context.Background(), secret.NewMemoryProvider(nil), nil, false)
	if err != nil {
		t.Fatalf("new development cursor state: %v", err)
	}
	if state.Encrypted != nil || state.Stateful == nil {
		t.Fatalf("development cursor state = %+v", state)
	}
}

func productionCursorTestBinding() ontologycontract.CursorBinding {
	return ontologycontract.CursorBinding{
		PrincipalKey:          "oidc:subject-1",
		TenantID:              "mission:1",
		BindingVersion:        1,
		QueryHash:             strings.Repeat("a", 64),
		AuthorizationRevision: "authorization:1",
		PolicyRevision:        "policy:1",
	}
}
