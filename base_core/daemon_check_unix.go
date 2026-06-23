//go:build !windows
// +build !windows

package base_core

import (
	"net"
	"os"
)

func CheckDaemonStatus() (DaemonStatus, string) {
	socketPath := GetDaemonSocketPath()

	_, err := os.Stat(socketPath)
	if os.IsNotExist(err) {
		return DaemonNotExist, socketPath
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return DaemonDead, socketPath
	}
	conn.Close()
	return DaemonRunning, socketPath
}

func GetDaemonPID() int {
	return 0
}
