package base_core

import (
	"os"
	"path/filepath"
	"runtime"
)

func GetDaemonSocketPath() string {
	if path := os.Getenv("NSSH_SOCKET_PATH"); path != "" {
		return path
	}

	switch runtime.GOOS {
	case "darwin", "freebsd":
		return getUserSocketPath()
	case "linux":
		return getLinuxSocketPath()
	case "windows":
		return `\\.\pipe\nssh_daemon`
	default:
		return "/tmp/nssh_daemon.sock"
	}
}

func getLinuxSocketPath() string {
	return getUserSocketPath()
}

func getUserSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return "/tmp/nssh_daemon.sock"
	}

	socketDir := filepath.Join(home, ".nssh")
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return "/tmp/nssh_daemon.sock"
	}

	return filepath.Join(socketDir, "daemon.sock")
}

func GetDaemonPIDFilePath() string {
	if path := os.Getenv("NSSH_PID_FILE"); path != "" {
		return path
	}

	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = os.Getenv("TEMP")
			if appData == "" {
				appData = "C:\\temp"
			}
		}
		pidDir := filepath.Join(appData, "nssh")
		os.MkdirAll(pidDir, 0755)
		return filepath.Join(pidDir, "daemon.pid")
	default:
		home, err := os.UserHomeDir()
		if err != nil || home == "" || home == "/" {
			return "/tmp/nssh_daemon.pid"
		}
		pidDir := filepath.Join(home, ".nssh")
		os.MkdirAll(pidDir, 0755)
		return filepath.Join(pidDir, "daemon.pid")
	}
}

var DaemonSocketPath = GetDaemonSocketPath()

type DaemonStatus int

const (
	DaemonNotExist DaemonStatus = iota
	DaemonRunning
	DaemonDead
)

func IsDaemonRunning() bool {
	status, _ := CheckDaemonStatus()
	return status == DaemonRunning
}

func CleanupDeadDaemon() error {
	status, socketPath := CheckDaemonStatus()
	if status == DaemonDead {
		if runtime.GOOS == "windows" {
			return nil
		}
		return os.Remove(socketPath)
	}
	return nil
}
