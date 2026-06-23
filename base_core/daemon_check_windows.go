//go:build windows
// +build windows

package base_core

import (
	"os"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32DLL        = windows.NewLazySystemDLL("kernel32.dll")
	openProcessProc    = kernel32DLL.NewProc("OpenProcess")
	getExitCodeProcess = kernel32DLL.NewProc("GetExitCodeProcess")
)

const (
	PROCESS_QUERY_INFORMATION = 0x0400
	STILL_ACTIVE              = 259
)

func checkDaemonPIDFile() (DaemonStatus, int) {
	pidFile := GetDaemonPIDFilePath()

	data, err := os.ReadFile(pidFile)
	if err != nil {
		return DaemonNotExist, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return DaemonNotExist, 0
	}

	if !isWindowsProcessRunning(pid) {
		os.Remove(pidFile)
		return DaemonDead, pid
	}

	return DaemonRunning, pid
}

func isWindowsProcessRunning(pid int) bool {
	handle, _, _ := openProcessProc.Call(
		uintptr(PROCESS_QUERY_INFORMATION),
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		return false
	}
	defer windows.CloseHandle(windows.Handle(handle))

	var exitCode uint32
	ret, _, _ := getExitCodeProcess.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	if ret == 0 {
		return false
	}

	return exitCode == STILL_ACTIVE
}

func WriteDaemonPIDFile() error {
	pidFile := GetDaemonPIDFilePath()
	pid := os.Getpid()
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
}

func RemoveDaemonPIDFile() error {
	pidFile := GetDaemonPIDFilePath()
	return os.Remove(pidFile)
}

func GetDaemonPID() int {
	_, pid := checkDaemonPIDFile()
	return pid
}
