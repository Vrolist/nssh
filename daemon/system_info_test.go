//go:build !windows
// +build !windows

package daemon

import (
	"testing"
	"time"
)

// === SetLokiURL 测试 ===

func TestSetLokiURL(t *testing.T) {
	// 保存原始值并恢复
	original := lokiURL
	defer func() { lokiURL = original }()

	SetLokiURL("http://localhost:3100/loki/api/v1/push")
	if lokiURL != "http://localhost:3100/loki/api/v1/push" {
		t.Errorf("lokiURL = %q, want http://localhost:3100/loki/api/v1/push", lokiURL)
	}
}

func TestSetLokiURL_Empty(t *testing.T) {
	original := lokiURL
	defer func() { lokiURL = original }()

	SetLokiURL("http://something")
	SetLokiURL("")
	if lokiURL != "" {
		t.Errorf("lokiURL = %q, want empty", lokiURL)
	}
}

// === PushMonitorReport 测试 ===

func TestPushMonitorReport_URL空时不推送(t *testing.T) {
	original := lokiURL
	defer func() { lokiURL = original }()

	lokiURL = ""
	report := &MonitorReport{
		Version:   "1.0.0",
		ReportType: "test",
	}

	err := PushMonitorReport(report)
	if err != nil {
		t.Errorf("PushMonitorReport with empty URL should return nil, got: %v", err)
	}
}

// === GetSystemInfo 测试 ===

func TestGetSystemInfo(t *testing.T) {
	info, err := GetSystemInfo()
	if err != nil {
		t.Fatalf("GetSystemInfo failed: %v", err)
	}

	if info.OS != "darwin" && info.OS != "linux" {
		t.Errorf("OS = %q, want darwin or linux", info.OS)
	}
	if info.Hostname == "" {
		t.Error("Hostname is empty")
	}
}

func TestGetProcessMemory(t *testing.T) {
	mem, err := GetProcessMemory(1) // PID 1，总是存在
	if err != nil {
		t.Logf("GetProcessMemory(1) failed (expected on some systems): %v", err)
		return
	}

	if mem.VmRSS <= 0 {
		t.Errorf("VmRSS = %d, want > 0", mem.VmRSS)
	}
}

// === formatDuration 测试 ===

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		input    time.Duration
		expected string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m"},
		{3660 * time.Second, "1h"},
		{86460 * time.Second, "1d"},
		{604860 * time.Second, "1w"},
		{2592060 * time.Second, "1m"},
		{31536060 * time.Second, "1y"},
	}

	for _, c := range cases {
		got := formatDuration(c.input)
		if got != c.expected {
			t.Errorf("formatDuration(%v) = %q, want %q", c.input, got, c.expected)
		}
	}
}
