//go:build windows
// +build windows

package base_core

import (
	"os"
	"syscall"
)

const (
	SIG_DAEMON_CMD  = syscall.SIGTERM
	SIG_WORKER_STOP = syscall.SIGTERM
)

func SendSignal(pid int, sig os.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(sig)
}

// TerminateProcess 结束进程（Windows 不支持 SIGTERM，退化为 Kill）。
func TerminateProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
