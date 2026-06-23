//go:build windows
// +build windows

package base_core

func CheckDaemonStatus() (DaemonStatus, string) {
	status, _ := checkDaemonPIDFile()
	return status, GetDaemonPIDFilePath()
}
