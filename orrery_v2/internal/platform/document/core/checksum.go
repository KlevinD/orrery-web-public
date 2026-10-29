package core

import (
	"crypto/sha256"
	"encoding/hex"
)

func ChecksumText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
