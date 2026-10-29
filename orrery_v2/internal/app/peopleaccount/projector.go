package peopleaccount

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type KratosAccountClient interface {
	GetIdentity(ctx context.Context, identityID string) (KratosIdentity, bool, error)
	FindIdentityByUsername(ctx context.Context, username string) (KratosIdentity, bool, error)
	CreateIdentity(ctx context.Context, projection Projection, credential KratosCredential) (KratosIdentity, error)
	UpdateIdentity(ctx context.Context, identity KratosIdentity, projection Projection) (KratosIdentity, error)
	RevokeIdentitySessions(ctx context.Context, identityID string) error
}

type LegacyCredentialSource interface {
	LegacyPasswordHash(ctx context.Context, personID string) (string, error)
}

type OAuth2SessionRevoker interface {
	RevokeSubjectSessions(ctx context.Context, subject string) error
}

type Projector struct {
	store                       *Store
	client                      KratosAccountClient
	oauth2Sessions              OAuth2SessionRevoker
	legacyCredentials           LegacyCredentialSource
	allowLegacyIdentityAdoption bool
	passwordGenerator           func() (string, error)
}

func (p *Projector) WithOAuth2SessionRevoker(revoker OAuth2SessionRevoker) *Projector {
	if p != nil {
		p.oauth2Sessions = revoker
	}
	return p
}

func NewProjector(store *Store, client KratosAccountClient) *Projector {
	return &Projector{store: store, client: client, passwordGenerator: generateInitialPassword}
}

func (p *Projector) WithLegacyCredentialSource(source LegacyCredentialSource) *Projector {
	if p != nil {
		p.legacyCredentials = source
	}
	return p
}

func (p *Projector) WithLegacyIdentityAdoption() *Projector {
	if p != nil {
		p.allowLegacyIdentityAdoption = true
	}
	return p
}

func (p *Projector) WithPasswordGenerator(generator func() (string, error)) *Projector {
	if p != nil && generator != nil {
		p.passwordGenerator = generator
	}
	return p
}

func (p *Projector) ProjectLatest(ctx context.Context, personID string) (ProjectionResult, error) {
	if p == nil || p.store == nil || p.client == nil || p.passwordGenerator == nil {
		return ProjectionResult{}, fmt.Errorf("People account projector is not configured")
	}
	state, err := p.store.ProjectionState(ctx, personID)
	if err != nil {
		return ProjectionResult{}, err
	}
	result := ProjectionResult{
		PersonID: state.Projection.PersonID, Revision: state.Projection.Revision,
		Status: state.Status, KratosIdentityID: state.KratosIdentityID,
	}
	if state.Status == ProjectionStatusApplied && state.AppliedRevision >= state.Projection.Revision {
		return result, nil
	}
	claimed, err := p.store.ClaimLatest(ctx, state.Projection.PersonID, state.Projection.Revision)
	if err != nil {
		return ProjectionResult{}, err
	}
	if !claimed {
		latest, latestErr := p.store.ProjectionState(ctx, state.Projection.PersonID)
		if latestErr != nil {
			return ProjectionResult{}, latestErr
		}
		result.Status = latest.Status
		result.KratosIdentityID = latest.KratosIdentityID
		return result, nil
	}
	if !state.Projection.Enabled && strings.TrimSpace(state.Projection.Username) == "" && strings.TrimSpace(state.KratosIdentityID) == "" {
		if err := p.store.MarkApplied(ctx, state.Projection.PersonID, state.Projection.Revision, ""); err != nil {
			return ProjectionResult{}, err
		}
		result.Status = ProjectionStatusApplied
		result.KratosIdentityID = ""
		return result, nil
	}
	var identity KratosIdentity
	var found bool
	if strings.TrimSpace(state.KratosIdentityID) != "" {
		identity, found, err = p.client.GetIdentity(ctx, state.KratosIdentityID)
		if err == nil && !found {
			identity, found, err = p.client.FindIdentityByUsername(ctx, state.Projection.Username)
		}
	} else {
		identity, found, err = p.client.FindIdentityByUsername(ctx, state.Projection.Username)
	}
	if err != nil {
		return ProjectionResult{}, p.fail(ctx, state.Projection, err)
	}
	if found {
		existingPersonID := strings.TrimSpace(identity.PersonID)
		if (existingPersonID == "" && !p.allowLegacyIdentityAdoption) || (existingPersonID != "" && existingPersonID != state.Projection.PersonID) {
			return ProjectionResult{}, p.fail(ctx, state.Projection, ErrProjectionConflict)
		}
	}
	if found {
		identity, err = p.client.UpdateIdentity(ctx, identity, state.Projection)
		if err != nil {
			return ProjectionResult{}, p.fail(ctx, state.Projection, err)
		}
		if !state.Projection.Enabled {
			if err := p.client.RevokeIdentitySessions(ctx, identity.ID); err != nil {
				return ProjectionResult{}, p.fail(ctx, state.Projection, err)
			}
			if p.oauth2Sessions != nil {
				if err := p.oauth2Sessions.RevokeSubjectSessions(ctx, state.Projection.PersonID); err != nil {
					return ProjectionResult{}, p.fail(ctx, state.Projection, err)
				}
			}
		}
	} else if state.Projection.Enabled {
		credential, initialPassword, credentialErr := p.newCredential(ctx, state.Projection)
		if credentialErr != nil {
			return ProjectionResult{}, p.fail(ctx, state.Projection, credentialErr)
		}
		identity, err = p.client.CreateIdentity(ctx, state.Projection, credential)
		if err != nil {
			return ProjectionResult{}, p.fail(ctx, state.Projection, err)
		}
		result.InitialPassword = initialPassword
	}
	if err := p.store.MarkApplied(ctx, state.Projection.PersonID, state.Projection.Revision, identity.ID); err != nil {
		return ProjectionResult{}, err
	}
	result.Status = ProjectionStatusApplied
	result.KratosIdentityID = identity.ID
	return result, nil
}

func (p *Projector) Sweep(ctx context.Context, limit int) error {
	personIDs, err := p.store.PendingPersonIDs(ctx, limit)
	if err != nil {
		return err
	}
	var sweepErr error
	for _, personID := range personIDs {
		if _, err := p.ProjectLatest(ctx, personID); err != nil {
			sweepErr = errors.Join(sweepErr, err)
		}
	}
	return sweepErr
}

func (p *Projector) newCredential(ctx context.Context, projection Projection) (KratosCredential, string, error) {
	legacyHash := strings.TrimSpace(projection.LegacyPassword)
	if legacyHash == "" && p.legacyCredentials != nil {
		var err error
		legacyHash, err = p.legacyCredentials.LegacyPasswordHash(ctx, projection.PersonID)
		if err != nil {
			return KratosCredential{}, "", err
		}
		legacyHash = strings.TrimSpace(legacyHash)
	}
	if usableKratosPasswordHash(legacyHash) {
		return KratosCredential{HashedPassword: legacyHash}, "", nil
	}
	password, err := p.passwordGenerator()
	if err != nil {
		return KratosCredential{}, "", fmt.Errorf("generate initial password: %w", err)
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return KratosCredential{}, "", fmt.Errorf("hash initial password: %w", err)
	}
	return KratosCredential{HashedPassword: string(hashed), PasswordChangeRequired: true}, password, nil
}

func (p *Projector) fail(ctx context.Context, projection Projection, projectionErr error) error {
	if markErr := p.store.MarkFailed(ctx, projection.PersonID, projection.Revision, projectionErr); markErr != nil {
		return errors.Join(projectionErr, markErr)
	}
	return projectionErr
}

func generateInitialPassword() (string, error) {
	random := make([]byte, 15)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func usableKratosPasswordHash(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "$argon2id$") || strings.HasPrefix(value, "$2a$") || strings.HasPrefix(value, "$2b$") || strings.HasPrefix(value, "$2y$")
}

var _ KratosAccountClient = (*KratosAdminClient)(nil)
