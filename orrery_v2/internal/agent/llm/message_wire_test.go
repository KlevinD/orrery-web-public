package llm

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Generated with the production schema encoder at main 981cde06. A changed
// byte sequence invalidates persisted compaction source hashes on restart.
func TestProductionCheckpointWireHashesSurviveModelBoundaryMigration(t *testing.T) {
	raw, err := os.ReadFile("testdata/production-message-checkpoint.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.TrimSpace(raw)
	const expected = "6277574980ad0b58fd30ec0c7dcdd8bed087e6f83cdd7a9e7384f9ac38a02551"
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
		t.Fatal("production fixture changed")
	}
	var messages []*Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, raw) {
		t.Fatalf("persisted message hash changed:\n%s", encoded)
	}
}
