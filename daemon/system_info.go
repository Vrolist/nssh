//go:build !windows
// +build !windows

package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"nssh/base_core"
)

var lokiURL string

// SetLokiURL 设置监控推送地址。
// 开源版默认空字符串（不推送），企业版可调用此接口接入自家 Loki 服务。
func SetLokiURL(url string) {
	lokiURL = url
}

var lokiBufferPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, 1024))
	},
}

type SystemInfo struct {
	KernelVersion string `json:"kernel_version"`
	Distro        string `json:"distro"`
	Arch          string `json:"arch"`
	OS            string `json:"os"`
	Hostname      string `json:"hostname"`
}

type ProcessMemory struct {
	PID    int    `json:"pid"`
	VmRSS  int64  `json:"vmrss"`
	VmSize int64  `json:"vmsize"`
}

type WorkerMemory struct {
	ID                   string `json:"id"`
	Status               string `json:"status"`
	StatusStableDuration string `json:"status_stable_duration"`
}

type MonitorReport struct {
	AllocMemory    int64          `json:"alloc_memory"`
	DaemonMemory   ProcessMemory  `json:"daemon_memory"`
	DaemonPID      int            `json:"daemon_pid"`
	GoroutineCount int            `json:"goroutine_count"`
	OfflineWorkers int            `json:"offline_workers"`
	OnlineWorkers  int            `json:"online_workers"`
	ReportType     string         `json:"report_type"`
	SystemInfo     SystemInfo     `json:"system_info"`
	Timestamp      string         `json:"timestamp"`
	TotalWorkers   int            `json:"total_workers"`
	Version        string         `json:"version"`
	VmRSSMemory    int64          `json:"vmrss_memory"`
	WorkersMemory  []WorkerMemory `json:"workers_memory"`
}

func GetSystemInfo() (SystemInfo, error) {
	info := SystemInfo{
		Arch: runtime.GOARCH,
		OS:   runtime.GOOS,
	}

	if kernel, err := exec.Command("uname", "-r").Output(); err == nil {
		info.KernelVersion = strings.TrimSpace(string(kernel))
	}

	if hostname, err := os.Hostname(); err == nil {
		info.Hostname = hostname
	}

	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.Distro = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
			if strings.HasPrefix(line, "NAME=") && info.Distro == "" {
				info.Distro = strings.Trim(strings.TrimPrefix(line, "NAME="), "\"")
			}
		}
	}

	return info, nil
}

func GetProcessMemory(pid int) (ProcessMemory, error) {
	mem := ProcessMemory{PID: pid}

	switch runtime.GOOS {
	case "linux":
		return getProcessMemoryLinux(pid)
	case "darwin":
		return getProcessMemoryDarwin(pid)
	default:
		return mem, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func getProcessMemoryLinux(pid int) (ProcessMemory, error) {
	mem := ProcessMemory{PID: pid}

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return mem, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "VmRSS:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if val, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					mem.VmRSS = val
				}
			}
		}
		if strings.HasPrefix(line, "VmSize:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if val, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					mem.VmSize = val
				}
			}
		}
	}

	return mem, nil
}

func getProcessMemoryDarwin(pid int) (ProcessMemory, error) {
	mem := ProcessMemory{PID: pid}

	output, err := exec.Command("ps", "-o", "rss=", "-o", "vsz=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return mem, err
	}

	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) >= 2 {
		if rss, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
			mem.VmRSS = rss
		}
		if vsz, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
			mem.VmSize = vsz
		}
	}

	return mem, nil
}

func formatDuration(d time.Duration) string {
	seconds := int(d.Seconds())

	if seconds >= 365*24*3600 {
		years := seconds / (365 * 24 * 3600)
		return fmt.Sprintf("%dy", years)
	}
	if seconds >= 30*24*3600 {
		months := seconds / (30 * 24 * 3600)
		return fmt.Sprintf("%dm", months)
	}
	if seconds >= 7*24*3600 {
		weeks := seconds / (7 * 24 * 3600)
		return fmt.Sprintf("%dw", weeks)
	}
	if seconds >= 24*3600 {
		days := seconds / (24 * 3600)
		return fmt.Sprintf("%dd", days)
	}
	if seconds >= 3600 {
		hours := seconds / 3600
		return fmt.Sprintf("%dh", hours)
	}
	if seconds >= 60 {
		minutes := seconds / 60
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", seconds)
}

func PushMonitorReport(report *MonitorReport) error {
	if lokiURL == "" {
		return nil
	}

	buf := lokiBufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		lokiBufferPool.Put(buf)
	}()

	buf.Reset()
	if err := json.NewEncoder(buf).Encode(report); err != nil {
		return fmt.Errorf("failed to marshal report: %v", err)
	}

	timestamp := time.Now().UnixNano()
	stream := map[string]string{
		"job":          "nssh",
		"process_type": "daemon",
		"report_type":  "monitor",
	}

	lokiData := map[string]interface{}{
		"streams": []map[string]interface{}{
			{
				"stream": stream,
				"values": [][]string{{fmt.Sprintf("%d", timestamp), buf.String()}},
			},
		},
	}

	buf.Reset()
	if err := json.NewEncoder(buf).Encode(lokiData); err != nil {
		return fmt.Errorf("failed to marshal loki data: %v", err)
	}

	req, err := http.NewRequest("POST", lokiURL, bytes.NewBuffer(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := base_core.GetHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("loki returned status: %d", resp.StatusCode)
	}

	return nil
}
