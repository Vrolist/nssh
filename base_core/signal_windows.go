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
