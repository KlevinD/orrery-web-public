//go:build !windows

package process

import (
	"os"
	"os/exec"
	"testing"
)

func TestTerminateProcessTreeRefusesCurrentProcessGroup(t *testing.T) {
	command := &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}
	if err := TerminateProcessTree(command); err == nil {
		t.Fatal("current process group termination was accepted")
	}
}

func TestTerminateProcessTreeStopsIsolatedChildGroup(t *testing.T) {
	command := exec.Command("sh", "-c", "sleep 60")
	ConfigureProcessTree(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := TerminateProcessTree(command); err != nil {
		t.Fatalf("TerminateProcessTree: %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("terminated child exited successfully")
	}
}
