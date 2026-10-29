//go:build !windows

package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func ConfigureProcessTree(command *exec.Cmd) {
	if command != nil {
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

func AttachProcessTree(*exec.Cmd) error { return nil }

func ReleaseProcessTree(*exec.Cmd) {}

func TerminateProcessTree(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	pid := command.Process.Pid
	if pid <= 1 {
		return fmt.Errorf("refusing to terminate unsafe process id %d", pid)
	}
	processGroupID, err := syscall.Getpgid(pid)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	serverProcessGroupID := syscall.Getpgrp()
	if processGroupID <= 1 || processGroupID == serverProcessGroupID {
		return fmt.Errorf("refusing to terminate unsafe process group %d for child %d", processGroupID, pid)
	}
	err = syscall.Kill(-processGroupID, syscall.SIGKILL)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
