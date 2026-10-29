package platform

import "orrery_v2/internal/platform/secret"

type SecretValue struct {
	value secret.Value
}

func (v SecretValue) Plaintext() string {
	return v.value.Plaintext()
}

func (v SecretValue) String() string {
	return "[REDACTED]"
}
