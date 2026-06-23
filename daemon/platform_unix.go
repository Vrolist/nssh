//go:build !windows
// +build !windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"

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
