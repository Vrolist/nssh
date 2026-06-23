//go:build windows
// +build windows

package daemon

import (
	"os"
	"os/signal"
	"syscall"

	"nssh/base_core"
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
