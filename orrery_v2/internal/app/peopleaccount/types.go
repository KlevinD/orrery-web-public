package peopleaccount

import "errors"

var ErrProjectionNotFound = errors.New("people account projection not found")
var ErrProjectionConflict = errors.New("people account projection conflicts with an existing identity")

const (
	ProjectionStatusPending    = "pending"
	ProjectionStatusProcessing = "processing"
	ProjectionStatusApplied    = "applied"
	ProjectionStatusFailed     = "provisioning_failed"
)

type Projection struct {
	PersonID        string `json:"person_id"`
	Revision        int64  `json:"revision"`
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	EnterpriseEmail string `json:"enterprise_email"`
	FeishuSubject   string `json:"feishu_subject,omitempty"`
	Enabled         bool   `json:"enabled"`
	LegacyPassword  string `json:"-"`
}

type ProjectionState struct {
	Projection       Projection
	Status           string
	AppliedRevision  int64
	KratosIdentityID string
	LastError        string
}

type OutboxEntry struct {
	ID         string
	Projection Projection
	Status     string
	Attempts   int
}

type ProjectionResult struct {
	PersonID         string
	Revision         int64
	Status           string
	KratosIdentityID string
	InitialPassword  string
}

type KratosIdentity struct {
	ID            string
	PersonID      string
	FeishuSubject string
	State         string
	MetadataAdmin map[string]any

	passwordCredentialPresent bool
	hashedPassword            string
	oidcProviders             []map[string]any
	unsupportedCredentials    bool
}

type KratosCredential struct {
	HashedPassword         string
	PasswordChangeRequired bool
}
