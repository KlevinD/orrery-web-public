package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefinitionHash identifies the behavior-bearing fields of a Skill. Timestamp
// bookkeeping is excluded so a rehydration does not invalidate an evaluation.
func DefinitionHash(definition SkillDefinition) string {
	definition = definition.Clone()
	definition.CreatedAt = time.Time{}
	definition.UpdatedAt = time.Time{}
	raw, _ := json.Marshal(definition)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func versionMatches(version, constraint string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" {
		return true
	}
	switch {
	case strings.HasPrefix(constraint, ">="):
		return compareVersion(version, strings.TrimSpace(strings.TrimPrefix(constraint, ">="))) >= 0
	case strings.HasPrefix(constraint, "^"):
		base := strings.TrimSpace(strings.TrimPrefix(constraint, "^"))
		return caretMatches(version, base)
	case strings.HasSuffix(constraint, ".x"):
		prefix := strings.TrimSuffix(constraint, ".x")
		return strings.HasPrefix(version, prefix+".")
	default:
		return version == constraint
	}
}

func caretMatches(version, base string) bool {
	v := parseVersion(version)
	b := parseVersion(base)
	if len(v) == 0 || len(b) == 0 || v[0] != b[0] {
		return false
	}
	return compareVersion(version, base) >= 0
}

func compareVersion(a, b string) int {
	av := parseVersion(a)
	bv := parseVersion(b)
	maxLen := len(av)
	if len(bv) > maxLen {
		maxLen = len(bv)
	}
	for i := 0; i < maxLen; i++ {
		var ai, bi int
		if i < len(av) {
			ai = av[i]
		}
		if i < len(bv) {
			bi = bv[i]
		}
		if ai > bi {
			return 1
		}
		if ai < bi {
			return -1
		}
	}
	return 0
}

func parseVersion(version string) []int {
	parts := strings.Split(version, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil
		}
		out = append(out, value)
	}
	return out
}

func versionMismatch(version, constraint string) error {
	return fmt.Errorf("%w: %s does not satisfy %s", ErrVersionMismatch, version, constraint)
}
