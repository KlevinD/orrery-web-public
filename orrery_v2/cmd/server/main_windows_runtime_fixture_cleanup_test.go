package main

import (
	"os"
	"testing"
	"time"
)

func removeWindowsRuntimeFixtureBeforeTempDirCleanup(t *testing.T, executablePath string) {
	t.Helper()
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := os.Remove(executablePath)
			if err == nil || os.IsNotExist(err) {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("remove released Windows runtime fixture %s: %v", executablePath, err)
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	})
}
