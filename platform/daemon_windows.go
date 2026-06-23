//go:build windows

package platform

import (
	"os"
	"syscall"
	"golang.org/x/sys/windows"
)

func StartDaemonProcess(execPath string) (*os.Process, error) {
	procAttr := &os.ProcAttr{
		Dir:   os.Getenv("TEMP"),
		Env:   os.Environ(),
		Files: []*os.File{nil, nil, nil},
		Sys:   &syscall.SysProcAttr{
			CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		},
	}

	return os.StartProcess(execPath, []string{execPath, "--daemon-inner"}, procAttr)
}
