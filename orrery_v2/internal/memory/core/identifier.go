package core

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// MaxIdentifierBytes keeps memory identifiers compatible with authorization
// access references and storage keys.
const MaxIdentifierBytes = 512

func ValidIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= MaxIdentifierBytes
}

func BoundedIdentifier(value, namespace string) string {
	value = strings.TrimSpace(value)
	if ValidIdentifier(value) {
		return value
	}
	if value == "" {
		return ""
	}
	namespace = strings.Trim(strings.TrimSpace(namespace), ".")
	if namespace == "" || len(namespace) > 128 {
		namespace = "memory.id"
	}
	sum := sha256.Sum256([]byte(value))
	return namespace + ".sha256." + hex.EncodeToString(sum[:])
}

func RecordIdentifierForCandidate(candidateID string) string {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return ""
	}
	recordID := "memory.record." + candidateID
	if ValidIdentifier(recordID) {
		return recordID
	}
	return BoundedIdentifier(recordID, "memory.record")
}
