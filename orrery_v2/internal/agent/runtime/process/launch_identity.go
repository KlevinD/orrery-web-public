package process

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// LaunchIdentity returns a non-reversible cache key. Environment values can
// contain credentials and therefore must never be retained in plaintext keys.
func LaunchIdentity(executable string, args []string, environment map[string]string, version string) string {
	hash := sha256.New()
	writeIdentityPart(hash, strings.TrimSpace(executable))
	writeIdentityPart(hash, strings.TrimSpace(version))
	for _, argument := range args {
		writeIdentityPart(hash, argument)
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeIdentityPart(hash, key)
		writeIdentityPart(hash, environment[key])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type identityWriter interface {
	Write([]byte) (int, error)
}

func writeIdentityPart(writer identityWriter, value string) {
	_, _ = writer.Write([]byte{byte(len(value) >> 24), byte(len(value) >> 16), byte(len(value) >> 8), byte(len(value))})
	_, _ = writer.Write([]byte(value))
}
