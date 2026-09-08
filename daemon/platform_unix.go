//go:build !windows
// +build !windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Vrolist/nssh/base_core"
)

func getSocketPath() string {
	return base_core.GetDaemonSocketPath()
}

func writePIDFile() {
	pidPath := base_core.GetDaemonPIDFilePath()
	os.MkdirAll(filepath.Dir(pidPath), 0755)
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

func removePIDFile() {
	os.Remove(base_core.GetDaemonPIDFilePath())
}

// agentProcessAlive 探测 nssh-agent 进程是否存活（kill(0) 仅探测、不发送信号）
func agentProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
