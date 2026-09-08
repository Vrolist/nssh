//go:build windows
// +build windows

package daemon

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/Vrolist/nssh/base_core"
)

func init() {
	setupSignals = func() chan os.Signal {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
		return sigChan
	}
}

func writePIDFile() {
	base_core.WriteDaemonPIDFile()
}

func removePIDFile() {
	base_core.RemoveDaemonPIDFile()
}

// agentProcessAlive 探测 nssh-agent 进程是否存活（OpenProcess 探测句柄）
func agentProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(handle)
	return true
}
