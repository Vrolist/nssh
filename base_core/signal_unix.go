//go:build !windows
// +build !windows

package base_core

import (
	"os"
	"syscall"
)

const (
	SIG_DAEMON_CMD  = syscall.SIGUSR1
	SIG_WORKER_STOP = syscall.SIGTERM
)

func SendSignal(pid int, sig os.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(sig)
}

// TerminateProcess 请求进程优雅退出（Unix 发 SIGTERM，由目标进程自行清理 socket/PID）。
// 用途：daemon 不认识 CMD_STOP_DAEMON 的旧版本只能靠信号退出。
func TerminateProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.SIGTERM)
}
