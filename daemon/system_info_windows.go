//go:build windows
// +build windows

package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"nssh/base_core"
	"golang.org/x/sys/windows"
)

var lokiURL string

// SetLokiURL 设置监控推送地址。
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

var (
	psapiDLL             = windows.NewLazySystemDLL("psapi.dll")
	getProcessMemoryInfo = psapiDLL.NewProc("GetProcessMemoryInfo")
)

type PROCESS_MEMORY_COUNTERS struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func GetSystemInfo() (SystemInfo, error) {
	info := SystemInfo{
		Arch: runtime.GOARCH,
		OS:   runtime.GOOS,
	}

	if hostname, err := os.Hostname(); err == nil {
		info.Hostname = hostname
	}

	return info, nil
}

func GetProcessMemory(pid int) (ProcessMemory, error) {
	mem := ProcessMemory{PID: pid}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return mem, err
	}
	defer windows.CloseHandle(handle)

	var memCounters PROCESS_MEMORY_COUNTERS
	memCounters.CB = uint32(unsafe.Sizeof(memCounters))

	ret, _, err := getProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&memCounters)),
		uintptr(memCounters.CB),
	)
	if ret == 0 {
		return mem, err
	}

	mem.VmRSS = int64(memCounters.WorkingSetSize / 1024)
	mem.VmSize = int64(memCounters.PeakWorkingSetSize / 1024)

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
