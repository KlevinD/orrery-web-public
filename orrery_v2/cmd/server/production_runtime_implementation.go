package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"orrery_v2/internal/app/improvement"
)

var runtimeImplementationOnce sync.Once
var runtimeImplementation *improvement.RuntimeImplementation
var runtimeBuildCommit string // Set by the release build; optional in development.

// Bind evidence to this process's executable, never a caller-provided version
// string or the moving current-release symlink. The source archive hash is
// supplementary provenance; the executable hash identifies the running bytes.
func productionRuntimeImplementation() *improvement.RuntimeImplementation {
	runtimeImplementationOnce.Do(func() {
		executable, err := os.Executable()
		if err != nil {
			log.Print("runtime implementation identity unavailable")
			return
		}
		digest, err := runtimeFileSHA256(executable)
		if err != nil {
			log.Print("runtime implementation identity unavailable")
			return
		}
		identity := &improvement.RuntimeImplementation{BinarySHA256: digest, BuildVersion: runtimeBuildCommit}
		archive := filepath.Join(filepath.Dir(filepath.Dir(executable)), "SOURCE_SNAPSHOT.tar.gz")
		identity.SourceSHA256, _ = runtimeFileSHA256(archive)
		runtimeImplementation = identity
	})
	if runtimeImplementation == nil {
		return nil
	}
	copy := *runtimeImplementation
	return &copy
}

func runtimeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
