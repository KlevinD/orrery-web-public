//go:build windows

package process

import (
	"errors"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNewProcessGroup = 0x00000200

var processJobs sync.Map

func ConfigureProcessTree(command *exec.Cmd) {
	if command != nil {
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	}
}

func AttachProcessTree(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	err = windows.AssignProcessToJobObject(job, processHandle)
	_ = windows.CloseHandle(processHandle)
	if err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	processJobs.Store(command.Process.Pid, job)
	return nil
}

func ReleaseProcessTree(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	if value, ok := processJobs.LoadAndDelete(command.Process.Pid); ok {
		_ = windows.CloseHandle(value.(windows.Handle))
	}
}

func TerminateProcessTree(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	if value, ok := processJobs.LoadAndDelete(command.Process.Pid); ok {
		job := value.(windows.Handle)
		err := windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
		if err == nil || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil
		}
	}
	pid := strconv.Itoa(command.Process.Pid)
	kill := exec.Command("taskkill.exe", "/PID", pid, "/T", "/F")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		treeErr := terminateSnapshotTree(uint32(command.Process.Pid))
		if treeErr == nil {
			return nil
		}
		rootErr := command.Process.Kill()
		if rootErr == nil || errors.Is(rootErr, windows.ERROR_ACCESS_DENIED) {
			return treeErr
		}
		return errors.Join(treeErr, rootErr)
	}
	return nil
}

func terminateSnapshotTree(root uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	children := make(map[uint32][]uint32)
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
	}
	if err != nil && !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	pids := make([]uint32, 0)
	var collect func(uint32)
	collect = func(parent uint32) {
		for _, child := range children[parent] {
			if child == 0 || child == uint32(windows.GetCurrentProcessId()) {
				continue
			}
			collect(child)
			pids = append(pids, child)
		}
	}
	collect(root)
	if root != uint32(windows.GetCurrentProcessId()) {
		pids = append(pids, root)
	}
	var joined error
	killed := false
	for _, pid := range pids {
		handle, openErr := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
		if openErr != nil {
			if !errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
				joined = errors.Join(joined, openErr)
			}
			continue
		}
		terminateErr := windows.TerminateProcess(handle, 1)
		_ = windows.CloseHandle(handle)
		if terminateErr == nil {
			killed = true
		} else if !errors.Is(terminateErr, windows.ERROR_ACCESS_DENIED) {
			joined = errors.Join(joined, terminateErr)
		}
	}
	if killed {
		return nil
	}
	if joined != nil {
		return joined
	}
	return windows.ERROR_NOT_FOUND
}
