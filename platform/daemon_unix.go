//go:build !windows

package platform

import (
	"fmt"
	"os"
	"syscall"
)

func StartDaemonProcess(execPath string) (*os.Process, error) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open /dev/null: %w", err)
	}
	defer devNull.Close()

	procAttr := &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, devNull, devNull},
		Sys: &syscall.SysProcAttr{
			Setsid: true,
		},
	}

	return os.StartProcess(execPath, []string{"nssh", "--daemon-inner"}, procAttr)
}
